// import * as path from 'path';
// import { Duration } from 'aws-cdk-lib';
import { IApiKey } from 'aws-cdk-lib/aws-apigateway';
import { Effect, PolicyStatement } from 'aws-cdk-lib/aws-iam';
import { RetentionDays } from 'aws-cdk-lib/aws-logs';


import {
  AwsCustomResource,
  AwsCustomResourcePolicy,
  AwsSdkCall,
  PhysicalResourceId,
} from 'aws-cdk-lib/custom-resources';
import { Construct } from 'constructs';


export interface GetApiKeyCrProps {
  apiKey: IApiKey;
}

export class GetApiKeyCr extends Construct {
  apikeyValue: string;
  constructor(scope: Construct, id: string, props: GetApiKeyCrProps) {
    super(scope, id);

    const apiKey: AwsSdkCall = {
      service: 'APIGateway',
      action: 'getApiKey',
      parameters: {
        apiKey: props.apiKey.keyId,
        includeValue: true,
      },
      physicalResourceId: PhysicalResourceId.of(`APIKey:${props.apiKey.keyId}`),
    };

    const apiKeyCr = new AwsCustomResource(this, 'api-key-cr', {
      policy: AwsCustomResourcePolicy.fromStatements([
        new PolicyStatement({
          effect: Effect.ALLOW,
          resources: [props.apiKey.keyArn],
          actions: ['apigateway:GET'],
        }),
      ]),
      logRetention: RetentionDays.ONE_DAY,
      onCreate: apiKey,
      onUpdate: apiKey,
    });

    apiKeyCr.node.addDependency(props.apiKey);
    this.apikeyValue = apiKeyCr.getResponseField('value');
  }
}

export function getLogRetentionPeriod(envName: string): RetentionDays {
  let logRetentionPeriod = RetentionDays.TWO_WEEKS;
  switch (envName) {
    case 'dev':
      logRetentionPeriod = RetentionDays.ONE_WEEK;
      break;
    case 'staging':
      logRetentionPeriod = RetentionDays.ONE_WEEK;
      break;
    case 'prod':
      logRetentionPeriod = RetentionDays.THREE_MONTHS;
      break;
    case 'production':
      logRetentionPeriod = RetentionDays.THREE_MONTHS;
      break;
    default:
      logRetentionPeriod = RetentionDays.TWO_WEEKS;
  }

  return logRetentionPeriod;
}

// export function generateLambdaFnFromAsset(fnName: string, binName: string): Function {
//   const asset = new Asset(this, `${fnName}Asset`, {
//     path: path.join(__dirname, '..', 'dist', 'source', `${binName}.zip`),
//   });

//   const lambdaFn = new Function(this, `${fnName}Function`, {
//     functionName: binName,
//     code: Code.fromBucket(asset.bucket, asset.s3ObjectKey),
//     runtime: Runtime.GO_1_X,
//     handler: `dist/source/${binName}`,
//     tracing: Tracing.ACTIVE,
//     timeout: Duration.seconds(900),
//     memorySize: 512,
//   });

//   return lambdaFn;
// }