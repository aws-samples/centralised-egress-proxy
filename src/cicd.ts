import { StackProps, SecretValue, Stage, StageProps } from 'aws-cdk-lib';
import { GitHubTrigger } from 'aws-cdk-lib/aws-codepipeline-actions';
import {
  CodePipeline,
  CodePipelineSource,
  ManualApprovalStep,
  ShellStep,
} from 'aws-cdk-lib/pipelines';
import { Construct } from 'constructs';
import { AppEnvConfig, RepoEntry } from './config';
import { GithubSource } from './constructs/github-trigger';
import { PipelineStack } from './pipeline';
import { SpokeStack } from './spoke-stack';
import { AppStack } from './stack';
import TaggingStack from './tagging';

interface EgressCicdStageProps extends StageProps {
  readonly config: AppEnvConfig;
  readonly environment: string;
}

class EgressCicdStage extends Stage {
  constructor(scope: Construct, id: string, props: EgressCicdStageProps) {
    super(scope, id, props);

    const appStack = new AppStack(this, 'ProxyStack', {
      config: props.config,
      env: props.config.env,
      environment: props.environment,
      vpc: props.config.vpc,
    });

    const appPipelineStack = new PipelineStack(this, 'ProxyPipeline', {
      env: props.config.env,
      ecr: appStack.ecr,
      codestarConnectionArn: props.config.codestarConnectionArn,
      repo: props.config.repo,
      environment: props.environment,
      fargateService: appStack.fargateService,
      releaseParameters: {
        releaseArn: appStack.releaseArn,
        releaseName: appStack.releaseName,
        releaseTag: appStack.releaseTag,
      },
    });
    appPipelineStack.addDependency(appStack);
  }
}
interface SpokeCicdStageProps extends StageProps {
  readonly config: AppEnvConfig;
  readonly environment: string;
}

class SpokeCicdStage extends Stage {
  constructor(scope: Construct, id: string, props: SpokeCicdStageProps) {
    super(scope, id, props);

    for (let spoke of props.config.spokeAccounts) {
      let SpokeStackName = 'SpokeStack' + spoke.account;

      new SpokeStack(this, SpokeStackName, {
        env: {
          account: spoke.account,
          region: props.config.env.region,
        },
        ...spoke,
        roleArn: `arn:aws:iam::${props.config.env.account}:role/egress-proxy-cross-account-ssm-reader-${props.config.env.region}`,
        serviceNameSSMPath: props.config.privateDns.serviceNameSSMPath,
        endpointRegion: props.config.env.region,
      });
    }
  }
}

interface CicdStackProps extends StackProps {
  readonly githubTokenArn: string;
  readonly repo: RepoEntry;
  readonly stacks: {
    readonly [name: string]: AppEnvConfig;
  };
}

export class CicdStack extends TaggingStack {
  constructor(scope: Construct, id: string, props: CicdStackProps) {
    super(scope, id, props);
    const oauthToken = SecretValue.secretsManager(props.githubTokenArn);

    const pipeline = new CodePipeline(this, 'CDKPipeline', {
      dockerEnabledForSynth: true,
      pipelineName: props.repo.pipelineName,
      crossAccountKeys: true,
      publishAssetsInParallel: true,
      synth: new ShellStep('Synth', {
        input: CodePipelineSource.gitHub(
          `${props.repo.owner}/${props.repo.repo}`,
          props.repo.branch,
          {
            authentication: oauthToken,
            trigger: GitHubTrigger.NONE,
          }
        ),
        env: {
          GO_VERSION: '1.19',
        },
        installCommands: [
          'wget https://storage.googleapis.com/golang/go${GO_VERSION}.linux-amd64.tar.gz',
          'tar -C /usr/local -xzf go${GO_VERSION}.linux-amd64.tar.gz',
          'export PATH="/usr/local/go/bin:$PATH" && export GOPATH="$HOME/go" && export PATH="$GOPATH/bin:$PATH"',
        ],
        commands: [
          `cd ./${props.repo.path}`,
          'make',

          'yarn install --immutable --immutable-cache --check-cache',
          'npm run build',
          'npx cdk synth',
        ],
        primaryOutputDirectory: `./${props.repo.path}/cdk.out`,
      }),
    });

    for (let stackName in props.stacks) {
      let stack = props.stacks[stackName];

      let egressStack = new EgressCicdStage(this, `${stackName}-Egress`, {
        env: stack.env,
        config: stack,
        environment: 'dev',
      });

      pipeline.addStage(egressStack);

      pipeline.addStage(
        new SpokeCicdStage(this, `${stackName}-Spoke`, {
          config: stack,
          environment: 'dev',
        }),
        {
          pre: [
            new ManualApprovalStep('Approve Spoke stacks deployment', {
              comment: 'Approve deployments to spoke accounts',
            }),
          ],
        }
      );
    }

    pipeline.buildPipeline();

    const ghSource = new GithubSource(this, 'GithubTrigger', {
      branch: props.repo.branch,
      owner: props.repo.owner,
      repo: props.repo.repo,
      filters: [props.repo.path],
      githubTokenArn: props.githubTokenArn,
      codepipeline: pipeline.pipeline,
    });
    ghSource.node.addDependency(pipeline);
  }
}
