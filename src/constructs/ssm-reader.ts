// The only purpose of this module and the ssm-reader module written in Go is to allow us to
// read SSM Parameters across account and regions
// This is necessary because AWS does not support it at all and cross account roles in this specific context do not work with `AwsSdkCall`

import * as crypto from 'crypto';
import * as fs from 'fs';
import path from 'path';
import {
  CustomResource,
  Duration,
  PhysicalName,
  RemovalPolicy,
} from 'aws-cdk-lib';
import { Effect, IRole, PolicyStatement } from 'aws-cdk-lib/aws-iam';
import { Code, Runtime, SingletonFunction } from 'aws-cdk-lib/aws-lambda';
import { RetentionDays } from 'aws-cdk-lib/aws-logs';
import { Provider } from 'aws-cdk-lib/custom-resources';
import { Construct } from 'constructs';

export interface SSMReaderProps {
  readonly name: string;
  readonly role: IRole;
  readonly region: string;
}

export class SSMReader extends Construct {
  value: string;

  constructor(scope: Construct, id: string, props: SSMReaderProps) {
    super(scope, id);

    const fnBase = path.join(__dirname, '..', '..', 'dist', 'cr');

    const fn = new SingletonFunction(this, 'SSMReader', {
      runtime: Runtime.GO_1_X,
      handler: 'dist/cr/ssm-reader',
      functionName: PhysicalName.GENERATE_IF_NEEDED,
      logRetention: RetentionDays.ONE_DAY,
      timeout: Duration.seconds(30),
      code: Code.fromAsset(path.join(fnBase, 'ssm-reader.zip')),
      uuid: '15483890-8772-4e42-a3ec-3a06b0fc1112', // Need any random UUID for the Singleton Lambda
      description:
        'This lambda function can be used to read ssm parameters in different account/regions',
    });

    fn.addToRolePolicy(
      new PolicyStatement({
        effect: Effect.ALLOW,
        resources: [props.role.roleArn],
        actions: ['sts:AssumeRole'],
      }),
    );

    const provider = new Provider(this, 'SSMReaderProvider', {
      onEventHandler: fn,
      logRetention: RetentionDays.ONE_DAY,
    });

    const response = new CustomResource(this, 'SSMReaderCR', {
      serviceToken: provider.serviceToken,
      properties: {
        Region: props.region,
        ParameterName: props.name,
        CrossAccountRoleArn: props.role.roleArn,
        // This key is added to ensure this lambda will run if anything in the golang function is changed
        // Cloudformation only runs this lambda when any of these parameters change
        // These parameters will not change if only the golang code was changed
        // So we need another parameter which is tied to the golang function
        // Every time the function changes, This parameter should also change
        // This'll ensure cloudformation runs the lambda as expected
        // It serves no other purpose
        // We compute hash of the binary rather than the final zip because the output of compiler is stable
        // where as output of zipped asset is not stable
        FnHash: this.getBuiltLambdaSha256(path.join(fnBase, 'ssm-reader')),
      },
      removalPolicy: RemovalPolicy.DESTROY,
    });

    this.value = response.getAtt('Value').toString();
  }
  private getBuiltLambdaSha256(codePath: string): string {
    const fileContents = fs.readFileSync(codePath);

    return crypto.createHash('sha256').update(fileContents).digest('hex');
  }
}
