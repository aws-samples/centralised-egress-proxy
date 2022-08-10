# Providing controlled internet access through centralised proxy servers using AWS Fargate and PrivateLink

## Summary
This solution is based on a fleet of opensource Squid proxies running on Amazon Elastic Container Service (ECS) with AWS Fargate. Internet access is provided centrally via a NAT Gateway and an Internet Gateway deployed in the central VPC. Amazon ECS uses a Network Load Balancer (NLB) configured as an endpoint service to make the solution available to ‘spoke’ VPCs. Interface endpoints are deployed into the ‘spoke’ (application) VPCs to enable resources inside these VPCs to use the deployed endpoint as its proxy server. 

## Deploying the Solution
The solution can be deployed in 4 steps to get you up and running:

1. Create a CodeCommit repo and stage the Dockerfile and associated configuration files. 
2. Create a Service Liked Role for ECS. 
3. Deploy the solution via the Cloudformation template provided. This solution deploys everything required for the central hub account in the solution overview
4. Create your VPC endpoint in your spoke account along with an EC2 instance for testing. 

For detailed instructions, please refer to the following AWS blog post: LINK TO POST TO BE ADDED





## Security

See [CONTRIBUTING](CONTRIBUTING.md#security-issue-notifications) for more information.

## License

This library is licensed under the MIT-0 License. See the LICENSE file.

