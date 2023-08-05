import { Stack, StackProps, Tags } from 'aws-cdk-lib';
import { Construct } from 'constructs';

export default class TaggingStack extends Stack {
  constructor(scope: Construct, id: string, props?: StackProps) {
    super(scope, id, props);
    Tags.of(this).add('Application', 'egress-proxy');
    Tags.of(this).add(
      'Description',
      'Providing controlled internet access through centralised proxy servers using AWS Fargate and PrivateLink',
    );
    Tags.of(this).add('Tier', 'Proxy');
  }
}
