# Summary

![Overview](./docs/Centralised-proxy-blog-figure-1.png "Overview")
With AWS PrivateLink, service consumers create interface VPC endpoints to connect to endpoint services that are hosted by service providers. The hub account for our solution is the ‘Service Provider’ and the AWS account where the proxy endpoint is deployed is the ‘Service Consumer’.

## CDK Infra

![Infra](./docs/diagram.png "Infra")

> **Note:** The proxy endpoint can be deployed across thousands of VPCs within the same region, extending to any AWS account that has been granted permission to use the endpoint service in the list of principals. However, due to regional specificity of the proxy endpoint deployment, it's crucial to remember that if you intend to use the proxy in different regions, the `EgressProxyStack` must be deployed separately in each of these regions. Therefore, for every region where you plan to utilize the proxy, a corresponding EgressStack deployment is essential.

Public IP Addresses, you can optain these by:

```terminal
make get_ips
```

| AZ         | eu-west-1      | eu-west-2    |
| ---------- | -------------- | ------------ |
| eu-west-1c | 54.154.***.*** |              |
| eu-west-1b | 34.248.***.*** |              |
| eu-west-1a | 54.73.***.***  |              |
| eu-west-2a |                | 13.42.***.***|
| eu-west-2c |                | 13.42.***.***|
| eu-west-2b |                | 13.41.***.***|


To test, access a machine that is in a VPC and execute the following command. This will use `curl` to make a web request through the proxy to `icanhazip.com`, a service that responds with the public IP address of the requester:
:

```terminal
[ec2-user@ip-10-85-241-21 ~]$ curl --proxy http://proxy.domain.io:1080 icanhazip.com
54.154.201.240
```

This should yeild the IP address from the NatInstances in the HUB account

To get all the public IPs

```terminal
aws ec2 describe-instances --query 'Reservations[*].Instances[*].[PublicIpAddress]' --region eu-west-1 --output text --profile hub-prod
```

## Working locally

You need to create a tunnel to the Bastion host first:

```terminal
ssh -i ~/.ssh/domain/api/bastion.pem -L 1080:proxy.domain.io:1080 ec2-user@$(aws --profile account-a --region eu-west-1 ec2 describe-instances --filters "Name=instance-state-name,Values=running" "Name=tag:Name,Values=BastionHost" --query 'Reservations[*].Instances[*].[PublicIpAddress]' --output text)
```

Ensure you have setup your profile and region correctly.

Then in a second terminal you can now run:

```terminal
❯ curl --proxy http://localhost:1080 icanhazip.com

54.154.***.***
```

## Installation

> **Note:** Ensure to increase the EC2-VPC Elastic IP quota to 10 - <https://eu-west-1.console.aws.amazon.com/servicequotas/home/services/ec2/quotas/L-0263D0A3> in each of the regions you plan to deploy the EgressProxyStack

Currently the `EgressProxyStack` has been deployed to `eu-west-1` and `eu-west-2` regions on the `HUB` account and a custom domain has been configured `proxy.domain.io`

If you want to use this from a different region, you must:

* Ensure the region has been bootstrapped correctly
* Update the config.yml
* Run `npx projen build` - you will get permission errors as you need to get some VPC entries from the account, to fix this, execute:

```terminal
npx cdk list --all
npx cdk synth EgressProxy-CICD-Stack/dev-eu-west-2/SpokeStack --profile spoke-account-a
```

Ensure to change the PROFILE and the stack that is failing.

* The cdk.context.json will then be updated, commit the changes and push to the `main` branch

> **Note:** There is a race condition in that in order to deploy the `SpokeStack` in the new region, the custom domain will need to be validated. You can get this from the CloudFormation outputs.

```terminal
aws cloudformation describe-stacks --stack-name dev-eu-west-1-ProxyStack --query 'Stacks[0].Outputs' --output json --region eu-west-1 --profile hub-dev | jq -r '[.[] | select(.OutputKey=="privatednsverificationname" or .OutputKey=="privatednsverificationvalue") .OutputValue] | join(",")'

vpce:8PJUeMyd7hqedAxAeEk5,_mc2j40daj0fqxfhusj3x
```

## How to erase the stack

There are 3 stacks in this project.

* `Egress` stack
* `Pipeline` stack
* `Spoke` stacks
`Pipeline` and `Egress` stack are deployed in the same account and `Pipeline` stack has a direct explicit dependency on the `Egress` stack - see `cicd.ts` file.

Any attempts to erase egress stack before erasing pipeline stack will fail instantly. Spoke stacks also have a dependency on Egress stacks but spoke stacks are deployed in different accounts so cloudformation can not enforce it for you.

> **Warning:** If you try to erase egress stack before erasing all connected spoke stacks, it'll try to erase `Endpoint service`, `NLB`, `Subnets` and then `VPC`, in that order. It'll take hours and eventually it'll fail to do any of this.
`Endpoint service` can not be erased until all allowed endpoint connections are removed from the list. It'll not let you erase `NLB` until endpoint service is erased and that'll hold deletion of subnets which'll hold deletion of VPC.

To erase every thing related to this project, you can try running this:

```terminal
# Delete all spoke stacks
for stack in $(npx cdk list --all| grep -i spoke); do npx cdk destroy --profile <name> $stack; done;

# Delete pipeline stack
npx cdk destroy --profile <name> EgressProxy-CICD-Stack/dev-eu-west-1/ProxyPipeline

# Delete egress stack
npx cdk destroy --profile <name> EgressProxy-CICD-Stack/dev-eu-west-1/ProxyStack

# CICD Stack can be erased independently of others.
npx cdk destroy --profile <name> EgressProxy-CICD-Stack/dev-eu-west-1/ProxyStack

# You can also run this to erase CICD Stack
npx cdk destroy --profile <name>
```

## Other

Listing all the vpc endpoints.

```terminal
❯ make list_endpoints
Enter AWS Profile: api-dev
Enter Region(s) (comma-separated): eu-west-1,eu-west-2
Successully logged into Start URL: https://ops.awsapps.com/start
Running cdk-sso-sync version 0.0.6
You are all set! Now you can run cdk commands with "--profile api-dev" options !
| Region    | OwnerId      | VpcId                 | VpcEndpointId          | CreationTimestamp                |
| --------- | ------------ | --------------------- | ---------------------- | -------------------------------- |
| eu-west-1 |              |                       |                        |                                  |

```

> **Warning:** In some rare cases if you want to deleting the endpoint services, you can do:-

Describe the endpoint services, excluding `amazon`

```terminal
❯ make describe_endpoint_services
Enter AWS Profile: hub-prod
Enter Region(s) (comma-separated): eu-west-1,eu-west-2
Successully logged into Start URL: <https://ops.awsapps.com/start>
Running cdk-sso-sync version 0.0.6
You are all set! Now you can run cdk commands with "--profile hub-prod" options !
| Region    | Service Name                                            | Service Id                 | Service Type | Private DNS Name | Availability Zones                 |
| --------- | ------------------------------------------------------- | -------------------------- | ------------ | ---------------- | ---------------------------------- |
| eu-west-1 | com.amazonaws.vpce.eu-west-1.vpce-svc-aaaaa | vpce-svc-aaaaa | Interface    | proxy.domain.io    | eu-west-1a, eu-west-1b, eu-west-1c |

```

Delete the service:

```terminal
aws ec2 delete-vpc-endpoint-service-configurations --service-ids <your-service-id> --region <region> --profile <profile-name>
```

Reference:
[0]: <https://aws.amazon.com/blogs/networking-and-content-delivery/providing-controlled-internet-access-through-centralised-proxy-servers-using-aws-fargate-and-privatelink/>
