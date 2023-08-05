import { StackProps } from 'aws-cdk-lib';
import { InterfaceVpcEndpointService, Vpc } from 'aws-cdk-lib/aws-ec2';
import { Role } from 'aws-cdk-lib/aws-iam';
import { Construct } from 'constructs';
import { SpokeAccount } from './config';
import { SSMReader } from './constructs/ssm-reader';
import TaggingStack from './tagging';
interface SpokeStackProps extends StackProps, SpokeAccount {
  readonly roleArn: string;
  readonly serviceNameSSMPath: string;
  readonly endpointRegion: string;
}

export class SpokeStack extends TaggingStack {
  constructor(scope: Construct, id: string, props: SpokeStackProps) {
    super(scope, id, props);

    let dedupVpcs = [
      ...new Map(props.vpcs.map((vpc) => [vpc.id, vpc])).values(),
    ];
    // Add Private link endpoint
    // 1. Lookup supported AZs and only add it to those AZs
    // 2. Allow all resources in VPC to send traffic to this endpoint
    // 3. Add endpoint in all subnets
    let serviceName = new SSMReader(this, 'ServiceNameReader', {
      name: props.serviceNameSSMPath,
      role: Role.fromRoleArn(this, 'CrossAccountSSMReaderRole', props.roleArn),
      region: props.endpointRegion,
    });

    for (let vpc of dedupVpcs) {
      // Note: This can be replaced with fromVpcAttributes but it'll require entering a lot of
      // information about the VPC in config.yml
      // fromLookup requires the credentials that are used to synthesize stack should also
      // have access to deploy into the target stack
      // It'll read values for this VPC and cache them into cdk.context.json
      // and refer to the cached values after that
      let vpcRef = Vpc.fromLookup(this, `vpc-${vpc.id}`, {
        vpcId: vpc.id,
      });

      vpcRef.addInterfaceEndpoint('egress-proxy', {
        lookupSupportedAzs: false,
        open: true,
        privateDnsEnabled: true,
        // NLB in egress stack will be proxying traffic from 1080 -> 3128
        service: new InterfaceVpcEndpointService(serviceName.value, 1080),
      });
    }
  }
}
