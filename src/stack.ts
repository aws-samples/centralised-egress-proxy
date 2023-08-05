import * as path from 'path';
import {
  CfnOutput,
  Duration,
  PhysicalName,
  RemovalPolicy,
  StackProps,
} from 'aws-cdk-lib';
import {
  ISecurityGroup,
  IVpc,
  FlowLogDestination,
  FlowLogFileFormat,
  FlowLogTrafficType,
  FlowLogMaxAggregationInterval,
  Peer,
  Port,
  SecurityGroup,
  Vpc,
  GatewayVpcEndpointAwsService,
  IpAddresses,
  NatProvider,
} from 'aws-cdk-lib/aws-ec2';
import { Repository } from 'aws-cdk-lib/aws-ecr';
import { DockerImageAsset } from 'aws-cdk-lib/aws-ecr-assets';
import {
  AwsLogDriver,
  Cluster,
  ContainerImage,
  FargateService,
  FargateTaskDefinition,
} from 'aws-cdk-lib/aws-ecs';
import { NetworkLoadBalancedFargateService } from 'aws-cdk-lib/aws-ecs-patterns';
import {
  AccessPoint,
  FileSystem,
  LifecyclePolicy,
  OutOfInfrequentAccessPolicy,
  PerformanceMode,
  ThroughputMode,
} from 'aws-cdk-lib/aws-efs';
import {
  NetworkLoadBalancer,
  Protocol,
} from 'aws-cdk-lib/aws-elasticloadbalancingv2';
import {
  AccountPrincipal,
  CompositePrincipal,
  Effect,
  PolicyDocument,
  PolicyStatement,
  Role,
  ServicePrincipal,
} from 'aws-cdk-lib/aws-iam';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import { Bucket } from 'aws-cdk-lib/aws-s3';
import { PrivateDnsNamespace } from 'aws-cdk-lib/aws-servicediscovery';
import { ParameterDataType, StringParameter } from 'aws-cdk-lib/aws-ssm';
import { Construct } from 'constructs';
import {
  AppEnvConfig,
  EcsConfig,
  FargateConfig,
  PrivateDnsConfig,
  SpokeAccount,
  VpcConfig,
} from './config';
import { CustomVpcEndpointServiceDomainName } from './constructs/custom-vpc-endpoint-service-domain-name';
import TaggingStack from './tagging';

interface AppStackProps extends StackProps {
  readonly config: AppEnvConfig;
  readonly environment: string;
  readonly vpc: VpcConfig;
}

export class AppStack extends TaggingStack {
  vpc: IVpc;
  vpcSecurityGroup: ISecurityGroup;
  ecr: Repository;
  fargateService!: FargateService; // Added ! to declare fargateService as non-nullable
  fargateNlb: NetworkLoadBalancer;
  readonly releaseTag: StringParameter;
  readonly releaseName: StringParameter;
  readonly releaseArn: StringParameter;
  readonly proxyFileSystemId: string;

  constructor(scope: Construct, id: string, props: AppStackProps) {
    super(scope, id, props);
    const flowLogBucket = Bucket.fromBucketArn(
      this,
      'FlowLogBucket',
      props.vpc.flowLogBucketArn
    );
    // VPC
    const vpc = new Vpc(this, 'InfraVPC', {
      ipAddresses: IpAddresses.cidr(props.vpc.cidr),
      enableDnsSupport: true,
      enableDnsHostnames: true,
      maxAzs: props.vpc.maxAzs,
      natGatewayProvider: NatProvider.gateway(),
      gatewayEndpoints: {
        // Keep S3 <-> Apps traffic and
        // ECR <-> Apps traffic in the VPC to reduce data transfer costs
        S3: { service: GatewayVpcEndpointAwsService.S3 },
      },
      flowLogs: {
        FlowLogS3: {
          destination: FlowLogDestination.toS3(
            flowLogBucket,
            props.vpc.flowLogPrefix,
            {
              // perHourPartition: true,
              fileFormat: FlowLogFileFormat.PARQUET,
              hiveCompatiblePartitions: true,
            }
          ),
        },
      },
    });
    this.vpc = vpc;

    // Only reject traffic and interval every minute.
    this.vpc.addFlowLog('FlowLogCloudWatch', {
      trafficType: FlowLogTrafficType.REJECT,
      maxAggregationInterval: FlowLogMaxAggregationInterval.ONE_MINUTE,
    });

    // EFS
    let ProxyFileSystem = new FileSystem(this, 'Efs', {
      vpc: this.vpc,
      encrypted: true,
      lifecyclePolicy: LifecyclePolicy.AFTER_14_DAYS, // files are not transitioned to infrequent access (IA) storage by default
      performanceMode: PerformanceMode.GENERAL_PURPOSE, // default
      outOfInfrequentAccessPolicy: OutOfInfrequentAccessPolicy.AFTER_1_ACCESS, // files are not transitioned back from (infrequent access) IA to primary storage by default
      throughputMode: ThroughputMode.BURSTING,
    });
    this.proxyFileSystemId = ProxyFileSystem.fileSystemId;
    ProxyFileSystem.connections.allowDefaultPortFrom(
      Peer.ipv4(this.vpc.vpcCidrBlock),
      'Allow NFS traffic from anyone in the VPC'
    );
    // Create the access point for /var/log/squid
    const logAccessPoint = ProxyFileSystem.addAccessPoint('LogAccessPoint', {
      path: '/var/log/squid',
      createAcl: {
        ownerGid: '13', // Update with the updated GID
        ownerUid: '13', // Update with the updated UID
        permissions: '755', // Update with the desired permissions
      },
      posixUser: {
        gid: '13',
        uid: '13',
      },
    });
    // Create the access point for /var/spool/squid
    const spoolAccessPoint = ProxyFileSystem.addAccessPoint(
      'SpoolAccessPoint',
      {
        path: '/var/spool/squid',
        createAcl: {
          ownerGid: '13', // Update with the updated GID
          ownerUid: '13', // Update with the updated UID
          permissions: '755', // Update with the desired permissions
        },
        posixUser: {
          gid: '13',
          uid: '13',
        },
      }
    );

    // Create VPC Security Group
    this.vpcSecurityGroup = new SecurityGroup(this, 'VpcSecurityGroup', {
      vpc: this.vpc,
    });

    this.ecr = new Repository(this, 'EgressProxyECR', {
      removalPolicy: RemovalPolicy.DESTROY,
      repositoryName: PhysicalName.GENERATE_IF_NEEDED,
      imageScanOnPush: true,
    });
    this.ecr.addToResourcePolicy(
      new PolicyStatement({
        sid: 'AllowLambdaToPullImages',
        effect: Effect.ALLOW,
        principals: [new ServicePrincipal('lambda.amazonaws.com')],
        actions: ['ecr:BatchGetImage', 'ecr:GetDownloadUrlForLayer'],
      })
    );

    const bootstrapAsset = new DockerImageAsset(this, 'BootstrapAsset', {
      directory: path.join(__dirname, 'lambda', 'placeholder'),
    });
    this.releaseArn = new StringParameter(this, 'ReleaseRepoArn', {
      parameterName: `/egress-proxy/${props.environment}/repository-arn`,
      // This is used for the first deployment
      // This parameter will be updated by the pipeline stack after successfully pushing a new image
      stringValue: bootstrapAsset.repository.repositoryArn,
    });
    this.releaseName = new StringParameter(this, 'ReleaseRepoName', {
      parameterName: `/egress-proxy/${props.environment}/repository-name`,
      stringValue: bootstrapAsset.repository.repositoryName,
    });
    this.releaseTag = new StringParameter(this, 'ReleaseImageTag', {
      parameterName: `/egress-proxy/${props.environment}/release-tag`,
      stringValue: bootstrapAsset.imageTag,
    });
    const logGroup = new LogGroup(this, 'EgressProxyLogs', {
      logGroupName: 'EgressProxyLogs',
      removalPolicy: RemovalPolicy.DESTROY,
    });

    let response = this.createService(
      props.config.fargateConfig,
      props.config.ecs,
      logGroup,
      ProxyFileSystem,
      logAccessPoint,
      spoolAccessPoint
    );
    this.fargateService = response.service.service;
    this.fargateNlb = response.service.loadBalancer;

    this.setupPrivateLink(
      response.service,
      props.config.privateDns,
      props.config.spokeAccounts,
      props.config.env.region
    );
  }

  createService(
    fargateConfig: FargateConfig,
    ecsConfig: EcsConfig,
    logGroup: LogGroup,
    ProxyFileSystem: FileSystem,
    logAccessPoint: AccessPoint,
    spoolAccessPoint: AccessPoint
  ): {
    service: NetworkLoadBalancedFargateService;
    port: number;
  } {
    // Create the Network Load Balancer
    const nlb = new NetworkLoadBalancer(this, 'NLB', {
      vpc: this.vpc,
      internetFacing: false,
      crossZoneEnabled: true,
    });

    const taskDef = new FargateTaskDefinition(this, 'fargate-task-def', {
      memoryLimitMiB: 1024,
    });
    taskDef.addVolume({
      name: 'logVolume',
      efsVolumeConfiguration: {
        fileSystemId: ProxyFileSystem.fileSystemId,
        authorizationConfig: {
          accessPointId: logAccessPoint.accessPointId,
        },
        transitEncryption: 'ENABLED',
      },
    });

    taskDef.addVolume({
      name: 'spoolVolume',
      efsVolumeConfiguration: {
        fileSystemId: ProxyFileSystem.fileSystemId,
        authorizationConfig: {
          accessPointId: spoolAccessPoint.accessPointId,
        },
        transitEncryption: 'ENABLED',
      },
    });

    // Grep for 3128 if you are going to change it in this code base
    // cross account sharing is extremely annoying and this value is directly referenced in a few places
    const appPort = 3128;
    const container = taskDef.addContainer('egress-proxy', {
      image: ContainerImage.fromEcrRepository(
        Repository.fromRepositoryAttributes(this, 'BootstrapRepo', {
          repositoryArn: this.releaseArn.stringValue,
          repositoryName: this.releaseName.stringValue,
        }),
        this.releaseTag.stringValue
      ),
      logging: new AwsLogDriver({ streamPrefix: 'egress-proxy', logGroup }),
      portMappings: [{ containerPort: appPort }],
      environment: {
        PORT: `${appPort}`,
      },
      healthCheck: {
        command: ['CMD-SHELL', 'exit 0'],
        interval: Duration.seconds(5),
        retries: 2,
        timeout: Duration.seconds(3),
      },
    });

    container.addMountPoints({
      containerPath: '/var/spool/squid',
      sourceVolume: 'spoolVolume',
      readOnly: false,
    });
    container.addMountPoints({
      containerPath: '/var/log/squid',
      sourceVolume: 'logVolume',
      readOnly: false,
    });

    if (fargateConfig != null && fargateConfig.env != null) {
      for (let key in fargateConfig.env) {
        container.addEnvironment(key, fargateConfig.env[key]);
      }
    }

    const cluster = new Cluster(this, 'cluster', {
      clusterName: PhysicalName.GENERATE_IF_NEEDED,
      vpc: this.vpc,
      enableFargateCapacityProviders: true,
    });

    // https://docs.aws.amazon.com/cdk/api/v2/docs/aws-cdk-lib.aws_ecs_patterns.NetworkLoadBalancedFargateService.html
    const service = new NetworkLoadBalancedFargateService(this, 'service', {
      cluster: cluster,
      serviceName: PhysicalName.GENERATE_IF_NEEDED,
      loadBalancer: nlb,
      assignPublicIp: false,
      minHealthyPercent: 0, // for zero downtime rolling deployment set desiredcount=2 and minHealty = 50
      taskDefinition: taskDef,
      capacityProviderStrategies: [
        {
          capacityProvider: 'FARGATE_SPOT',
          weight: 4,
        },
        {
          capacityProvider: 'FARGATE',
          weight: 2,
        },
      ],
      desiredCount: ecsConfig.desiredCount,
      cloudMapOptions: {
        name: 'proxy',
        cloudMapNamespace: new PrivateDnsNamespace(this, 'ns', {
          name: 'proxy.arpa',
          vpc: this.vpc,
        }),
        containerPort: appPort,
      },
      publicLoadBalancer: false,
      listenerPort: 1080,
    });
    service.targetGroup.configureHealthCheck({
      enabled: true,
      port: appPort.toString(),
      protocol: Protocol.TCP,
    });

    service.service.connections.allowFrom(
      Peer.ipv4(this.vpc.vpcCidrBlock),
      Port.tcp(appPort),
      `Allow inbound traffic from vpc on ${appPort}/tcp`
    );
    // Setup AutoScaling policy
    const scaling = service.service.autoScaleTaskCount({
      maxCapacity: 5,
    });
    scaling.scaleOnCpuUtilization('CpuScaling', {
      targetUtilizationPercent: 70,
      scaleInCooldown: Duration.seconds(180),
      scaleOutCooldown: Duration.seconds(60),
    });
    // Allow the task container to pull images from ECR
    this.ecr.grantPull(service.taskDefinition.obtainExecutionRole());

    new CfnOutput(this, 'AlbDNSName', {
      value: service.loadBalancer.loadBalancerDnsName,
      description: 'DNS of ALB in this stack',
    });

    return {
      service,
      port: appPort,
    };
  }

  setupPrivateLink(
    service: NetworkLoadBalancedFargateService,
    privateDnsConfig: PrivateDnsConfig,
    spokeAccounts: SpokeAccount[],
    region: string
  ) {
    let vpce = new CustomVpcEndpointServiceDomainName(this, 'vpce', {
      acceptanceRequired: false,
      loadBalancers: [service.loadBalancer],
      privateDnsName: privateDnsConfig.domainName,
      allowedPrincipals: spokeAccounts.map((spoke) => {
        return new AccountPrincipal(spoke.account);
      }),
    });

    const serviceNameParam = new StringParameter(this, 'ServiceNameSSM', {
      stringValue: vpce.serviceName,
      description: 'Service name from egress-proxy project',
      dataType: ParameterDataType.TEXT,
      parameterName: privateDnsConfig.serviceNameSSMPath,
    });

    // The role name is hardcoded here. It should not collide if there are multiple
    // egress stacks deployed into 1 account.
    // Any changes made here should also be updated in spoke stacks
    // IAM roles are global and not region specific
    new Role(this, 'CrossAccountSSMRole', {
      roleName: `egress-proxy-cross-account-ssm-reader-${region}`,
      assumedBy: new CompositePrincipal(
        ...spokeAccounts.map((spoke) => {
          return new AccountPrincipal(spoke.account);
        }),
        new ServicePrincipal('lambda.amazonaws.com')
      ),
      description: 'Allow reading SSM Parameter from Spoke Accounts',
      inlinePolicies: {
        SSM: new PolicyDocument({
          assignSids: true,
          minimize: true,
          statements: [
            new PolicyStatement({
              effect: Effect.ALLOW,
              actions: ['ssm:GetParameter'],
              resources: [serviceNameParam.parameterArn],
            }),
          ],
        }),
      },
    });

    new CfnOutput(this, 'private-link-service-name', {
      value: vpce.serviceName,
      description: 'Service name of private link',
    });
    new CfnOutput(this, 'private-dns-domain', {
      value: privateDnsConfig.domainName,
      description: 'Domain name associated with private DNS',
    });
    new CfnOutput(this, 'private-dns-verification-name', {
      exportName: 'PrivateDnsVerificationName',
      value: vpce.privateDnsNameConfiguration.name.toString(),
      description: 'Name of record to add for domain verification',
    });
    new CfnOutput(this, 'private-dns-verification-type', {
      exportName: 'PrivateDnsVerificationType',
      value: vpce.privateDnsNameConfiguration.type.toString(),
      description: 'Type of record to add for domain verification',
    });
    new CfnOutput(this, 'private-dns-verification-value', {
      exportName: 'PrivateDnsVerificationValue',
      value: vpce.privateDnsNameConfiguration.value.toString(),
      description: 'Value of record to add for domain verification',
    });
    new CfnOutput(this, 'private-dns-verification-state', {
      exportName: 'PrivateDnsVerificationState',
      value: vpce.privateDnsNameConfiguration.state.toString(),
      description: 'Domain Verification State',
    });
    new CfnOutput(this, 'domain-txt-record', {
      exportName: 'DnsTxtRecord',
      value: `${vpce.privateDnsNameConfiguration.name.toString()} ${vpce.privateDnsNameConfiguration.value.toString()} TXT`,
      description: 'TXT record for CloudFlare',
    });
  }
}
