import * as fs from 'fs';
import * as YAML from 'yaml';

export interface RepoEntry {
  readonly owner: string;
  readonly repo: string;
  readonly branch: string;
  readonly path: string;
  readonly pipelineName: string;
  readonly codestarConnectionArn: string;
}

export interface VpcConfig {
  readonly cidr: string;
  readonly maxAzs: number;
  readonly flowLogBucketArn: string;
  readonly flowLogPrefix: string;
}

export interface Env {
  readonly account: string;
  readonly region: string;
}

export interface BaseAppConfig {
  readonly env: Env;
}

export interface SpokeAccount {
  readonly account: string;
  readonly vpcs: SpokeAccountVpcConfig[];
}

export interface SpokeAccountVpcConfig {
  readonly id : string;
}

export interface CicdStackConfig extends BaseAppConfig {
  readonly repo: RepoEntry;
  readonly githubTokenArn: string;
}

export interface FargateConfig {
  readonly env: {
    readonly [name: string]: string;
  };
  readonly secrets: {
    readonly ssmParameter: string;
    readonly fields: string[];
  }[];
}

export interface PrivateDnsConfig {
  readonly domainName: string;
  readonly serviceNameSSMPath: string;
}

export interface EcsConfig {
  readonly desiredCount: number;
}
export interface AppEnvConfig extends BaseAppConfig {
  readonly vpc: VpcConfig;
  readonly fargateConfig: FargateConfig;
  readonly repo: RepoEntry;
  readonly codestarConnectionArn: string;
  readonly cmsSecretsParameter: string;
  readonly spokeAccounts: SpokeAccount[];
  readonly privateDns: PrivateDnsConfig;
  readonly ecs: EcsConfig;
}

export class Config {
  readonly cicd: CicdStackConfig;
  stacks: {
    [name: string]: AppEnvConfig;
  };

  constructor(fileName?: string) {
    const filename = fileName || 'config.yml';
    const file = fs.readFileSync(filename, 'utf-8');

    const yaml = YAML.parse(file);
    this.cicd = yaml.cicd;
    this.stacks = yaml.stacks;

    console.log(JSON.stringify(this, null, 2));
  }
}
