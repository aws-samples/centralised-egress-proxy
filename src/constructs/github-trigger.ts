import path from 'path';
import {
  CustomResource,
  Duration,
  PhysicalName,
  RemovalPolicy,
} from 'aws-cdk-lib';
import { Pipeline } from 'aws-cdk-lib/aws-codepipeline';
import { Effect, PolicyStatement } from 'aws-cdk-lib/aws-iam';
import {
  Code,
  Function,
  FunctionUrlAuthType,
  Runtime,
  SingletonFunction,
} from 'aws-cdk-lib/aws-lambda';
import { RetentionDays } from 'aws-cdk-lib/aws-logs';
import { Provider } from 'aws-cdk-lib/custom-resources';
import { Construct } from 'constructs';

export interface GithubSourceProps {
  readonly repo: string;
  readonly owner: string;
  readonly branch: string;
  readonly githubTokenArn: string;

  // Filters is just a list of prefixes.
  // It'll check all modified/removed/added files and start codepipeline
  // if any of them have files with the matching prefixes
  readonly filters: string[];
  readonly codepipeline: Pipeline;
}
export class GithubSource extends Construct {
  constructor(scope: Construct, id: string, props: GithubSourceProps) {
    super(scope, id);

    const triggerFn = new Function(this, 'TriggerFn', {
      runtime: Runtime.GO_1_X,
      code: Code.fromAsset(
        path.join(__dirname, '..', '..', 'dist', 'trigger-fn.zip'),
      ),
      handler: 'dist/cr/trigger-fn',
      memorySize: 128,
      timeout: Duration.seconds(60),
      description:
        'This lambda runs when there is a new event in the repo and starts codepipeline for matching events',
      functionName: PhysicalName.GENERATE_IF_NEEDED,
      environment: {
        CODEPIPELINE_NAME: props.codepipeline.pipelineName,
        GITHUB_BRANCH: props.branch,
        FILTERS: props.filters.join(','),
      },
      logRetention: RetentionDays.ONE_DAY,
    });

    triggerFn.addToRolePolicy(
      new PolicyStatement({
        effect: Effect.ALLOW,
        actions: ['codepipeline:StartPipelineExecution'],
        resources: [props.codepipeline.pipelineArn],
        sid: 'AllowTriggerFnToStartCodepipeline',
      }),
    );

    // Add a function URL for triggerFn
    let triggerFnUrl = triggerFn.addFunctionUrl({
      authType: FunctionUrlAuthType.NONE,
    });

    // Create the lambda function which will run when there is a new event
    // This lambda also manages the webhook it'll create.
    // i.e. Create/update webhook on stack creation/updation and remove it when stack is erased
    const webhookManagerFn = new SingletonFunction(this, 'WebhookManagerFn', {
      runtime: Runtime.GO_1_X,
      code: Code.fromAsset(
        path.join(__dirname, '..', '..', 'dist', 'cr', 'webhook-manager-fn.zip'),
      ),
      handler: 'dist/cr/webhook-manager-fn',
      memorySize: 128,
      timeout: Duration.seconds(60),
      retryAttempts: 0,
      uuid: '95483890-8772-4e42-a3ec-3a06b1234567', // Need any random UUID for the Singleton Lambda
      description: 'This lambda manages the webhook in Github repository',
      functionName: PhysicalName.GENERATE_IF_NEEDED,
      initialPolicy: [
        new PolicyStatement({
          effect: Effect.ALLOW,
          actions: ['secretsmanager:GetSecretValue'],
          sid: 'AllowWebhookManagerToReadSecrets',
          // This is not ideal but it's a singleton function
          // We can try adding policy statement for each secret id but that can overflow the IAM policy size limits
          resources: ['*'],
        }),
      ],
      logRetention: RetentionDays.ONE_DAY,
    });

    const provider = new Provider(this, 'WebhookManagerProvider', {
      onEventHandler: webhookManagerFn,
      logRetention: RetentionDays.ONE_DAY,
    });

    const cr = new CustomResource(this, 'WebhookManager', {
      serviceToken: provider.serviceToken,
      properties: {
        GithubTokenArn: props.githubTokenArn,
        GithubOwner: props.owner,
        GithubRepo: props.repo,
        GithubBranch: props.branch,
        WebhookURL: triggerFnUrl.url,
      },
      removalPolicy: RemovalPolicy.DESTROY,
    });
    cr.node.addDependency(triggerFn);
    cr.node.addDependency(triggerFnUrl);
  }
}
