import * as crypto from 'crypto';
import * as fs from 'fs';
import path from 'path';
import {
  CustomResource,
  Duration,
  PhysicalName,
  Reference,
  RemovalPolicy,
} from 'aws-cdk-lib';
import { INetworkLoadBalancer } from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import { ArnPrincipal, Effect, PolicyStatement } from 'aws-cdk-lib/aws-iam';
import { Code, Runtime, SingletonFunction } from 'aws-cdk-lib/aws-lambda';
import { RetentionDays } from 'aws-cdk-lib/aws-logs';
import { Provider } from 'aws-cdk-lib/custom-resources';
import { Construct } from 'constructs';

export interface CustomVpcEndpointServiceDomainNameProps {
  readonly loadBalancers: INetworkLoadBalancer[];
  readonly acceptanceRequired: boolean;
  readonly privateDnsName: string;
  readonly allowedPrincipals: ArnPrincipal[];
}
export class CustomVpcEndpointServiceDomainName extends Construct {
  readonly serviceId: Reference;
  readonly serviceName: string;
  readonly privateDnsNameConfiguration: {
    readonly name: Reference;
    readonly type: Reference;
    readonly value: Reference;
    readonly state: Reference;
  };
  constructor(
    scope: Construct,
    id: string,
    props: CustomVpcEndpointServiceDomainNameProps,
  ) {
    super(scope, id);

    const fnBase = path.join(__dirname, '..', '..', 'dist', 'cr');

    const fn = new SingletonFunction(this, 'VpcEndpointConfigurator', {
      runtime: Runtime.GO_1_X,
      handler: 'dist/cr/vpc-endpoint-configurator',
      functionName: PhysicalName.GENERATE_IF_NEEDED,
      logRetention: RetentionDays.ONE_DAY,
      timeout: Duration.seconds(90),
      code: Code.fromAsset(path.join(fnBase, 'vpc-endpoint-configurator.zip')),
      uuid: '95483890-8772-4e42-a3ec-3a06b0fc1c12', // Need any random UUID for the Singleton Lambda
      description:
        'This lambda function is responsible for managing VPC Endpoints(Private Links)',
    });

    fn.addToRolePolicy(
      new PolicyStatement({
        effect: Effect.ALLOW,
        resources: ['*'],
        actions: [
          'ec2:CreateVpcEndpointServiceConfiguration',
          'ec2:DeleteVpcEndpointServiceConfigurations',
          'ec2:DescribeVpcEndpointServicePermissions',
          'ec2:ModifyVpcEndpointServicePermissions',
          'ec2:ModifyVpcEndpointServiceConfiguration',
          'ec2:DescribeVpcEndpointServiceConfigurations',
        ],
      }),
    );

    const provider = new Provider(this, 'VpcEndpointConfigurationProvider', {
      onEventHandler: fn,
      logRetention: RetentionDays.ONE_DAY,
    });

    const response = new CustomResource(
      this,
      'VpcEndpointConfiguratorResource',
      {
        serviceToken: provider.serviceToken,
        properties: {
          NetworkLoadBalancerArns: props.loadBalancers.map((lb) => {
            return lb.loadBalancerArn;
          }),
          AcceptanceRequired: props.acceptanceRequired,
          PrivateDnsName: props.privateDnsName,
          AllowedPrincipals: props.allowedPrincipals.map((principal) => {
            return principal.arn;
          }),
          // This key is added to ensure this lambda will run if anything in the golang function is changed
          // Cloudformation only runs this lambda when any of these parameters change
          // These parameters will not change if only the golang code was changed
          // So we need another parameter which is tied to the golang function
          // Every time the function changes, This parameter should also change
          // This'll ensure cloudformation runs the lambda as expected
          // It serves no other purpose
          // We compute hash of the binary rather than the final zip because the output of compiler is stable
          // where as output of zipped asset is not stable
          FnHash: this.getBuiltLambdaSha256(
            path.join(fnBase, 'vpc-endpoint-configurator'),
          ),
        },
        removalPolicy: RemovalPolicy.DESTROY,
      },
    );

    this.serviceId = response.getAtt('ServiceId');
    this.serviceName = response.getAtt('ServiceName').toString();
    this.privateDnsNameConfiguration = {
      name: response.getAtt('PrivateDnsNameConfigurationName'),
      type: response.getAtt('PrivateDnsNameConfigurationType'),
      value: response.getAtt('PrivateDnsNameConfigurationValue'),
      state: response.getAtt('PrivateDnsNameConfigurationState'),
    };
  }

  private getBuiltLambdaSha256(codePath: string): string {
    const fileContents = fs.readFileSync(codePath);

    return crypto.createHash('sha256').update(fileContents).digest('hex');
  }
}
