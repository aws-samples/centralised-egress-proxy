SHELL += -eu

build: clear
	env CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -o ./dist/cr/trigger-fn ./src/constructs/trigger-fn
	env CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -o ./dist/cr/webhook-manager-fn ./src/constructs/webhook-manager-fn
	env CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -o ./dist/cr/vpc-endpoint-configurator ./src/constructs/vpc-endpoint-configurator
	env CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -o ./dist/cr/ssm-reader ./src/constructs/ssm-reader
	env CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -o ./dist/lambda/updater ./src/lambda/updater

	strip ./dist/lambda/*
	strip ./dist/cr/*
	zip ./dist/cr/webhook-manager-fn.zip ./dist/cr/webhook-manager-fn
	zip ./dist/cr/vpc-endpoint-configurator.zip ./dist/cr/vpc-endpoint-configurator
	zip ./dist/cr/ssm-reader.zip ./dist/cr/ssm-reader	
	zip ./dist/trigger-fn.zip ./dist/cr/trigger-fn
	zip ./dist/updater.zip ./dist/lambda/updater

clear: gendirectory
	rm -rf ./dist/*

dia:
	cd docs && npx cdk-dia --tree ../cdk.out/tree.json  \
		--include EgressProxy-CICD-Stack \
		--include cross-region-stack-000000000000:eu-west-2 \
		--include EgressProxy-CICD-Stack/eu-west-1-Egress/ProxyStack \
		--include EgressProxy-CICD-Stack/eu-west-1-Egress/ProxyPipeline \
		--include EgressProxy-CICD-Stack/eu-west-1-Spoke/SpokeStack222222222222 \
		--include EgressProxy-CICD-Stack/eu-west-1-Spoke/SpokeStack333333333333

gendirectory:
	mkdir -p dist

get_ips:
	@export AWS_PROFILE=hub-prod && \
	printf "| %-10s | %-15s | %-15s |\n" "AZ" "eu-west-1" "eu-west-2" && \
	printf "| %-10s | %-15s | %-15s |\n" "----------" "---------------" "---------------" && \
	for region in eu-west-1 eu-west-2; do \
	    export AWS_DEFAULT_REGION=$$region && \
	    IGW_IDS=$$(aws ec2 describe-internet-gateways --query 'InternetGateways[*].[InternetGatewayId]' --output text) && \
	    PUBLIC_SUBNET_IDS=() && \
	    for IGW_ID in $$IGW_IDS; do \
	        ROUTE_TABLE_IDS=$$(aws ec2 describe-route-tables --filters Name=route.gateway-id,Values=$$IGW_ID --query 'RouteTables[*].[RouteTableId]' --output text) ;\
	        for ROUTE_TABLE_ID in $$ROUTE_TABLE_IDS; do \
	            SUBNET_IDS=$$(aws ec2 describe-route-tables --route-table-ids $$ROUTE_TABLE_ID --query 'RouteTables[*].Associations[*].SubnetId' --output text) ;\
	            PUBLIC_SUBNET_IDS+=($$SUBNET_IDS) ;\
	        done ;\
	    done && \
	    for SUBNET_ID in $${PUBLIC_SUBNET_IDS[@]}; do \
	        AZ=$$(aws ec2 describe-subnets --subnet-ids $$SUBNET_ID --query 'Subnets[0].AvailabilityZone' --output text) && \
	        NAT_GATEWAYS=$$(aws ec2 describe-nat-gateways --filter Name=subnet-id,Values=$$SUBNET_ID --query 'NatGateways[*].[NatGatewayAddresses[0].PublicIp]' --output text) && \
	        if [ "$$region" = "eu-west-1" ]; then \
	            printf "| %-10s | %-15s | %-15s |\n" $$AZ $$NAT_GATEWAYS " " ;\
	        else \
	            printf "| %-10s | %-15s | %-15s |\n" $$AZ " " $$NAT_GATEWAYS ;\
	        fi ;\
	    done ;\
	done

describe_vpc_endpoints:
	@echo "\n========== AWS VPC Endpoints Info ==========\n"; \
	echo "Please enter the required information or press enter to use defaults"; \
	echo "\n-----------------------------------\n"; \
	read -p "AWS Profile [default]: " profile; \
	if [ -z "$$profile" ]; then \
	    profile='default'; \
	fi; \
	echo "\n-----------------------------------\n"; \
	read -p "Region(s) (comma-separated) [eu-west-1,eu-west-2]: " regions_input; \
	if [ -z "$$regions_input" ]; then \
	    regions_input='eu-west-1,eu-west-2'; \
	fi; \
	echo "\n-----------------------------------\n"; \
	if [ $$profile != "default" ]; then \
	    aws sso login --profile=$$profile; \
	    cdk-sso-sync $$profile; \
	fi; \
	IFS=',' read -ra regions <<< "$$regions_input"; \
	echo "| Region | OwnerId | VpcId | VpcEndpointId | CreationTimestamp |"; \
	echo "| ------ | ------- | ----- | -------------- | ----------------- |"; \
	for region in "$${regions[@]}"; do \
	    export AWS_DEFAULT_REGION=$$region ;\
	    vpcs=$$(aws ec2 describe-vpcs --query "Vpcs[].VpcId" --output text --profile $$profile); \
	    for vpc_id in $$vpcs; do \
	        results=$$(AWS_PROFILE=$$profile aws ec2 describe-vpc-endpoints --no-paginate --filters Name=vpc-id,Values=$$vpc_id --query 'VpcEndpoints[*].[VpcEndpointId,VpcId,OwnerId,CreationTimestamp]' --output text); \
	        while IFS=$$'\t' read -r id vpc owner timestamp; do \
	            if [ -n "$$owner" ]; then \
	                echo "| $$region | $$owner | $$vpc | $$id | $$timestamp |"; \
	            fi; \
	        done <<< "$$results"; \
	    done; \
	done

describe_endpoint_services:
	@read -p "Enter AWS Profile: " profile; \
	read -p "Enter Region(s) (comma-separated): " regions_input; \
	if [ $$profile != "default" ]; then \
	    aws sso login --profile=$$profile; \
	    cdk-sso-sync $$profile; \
	fi; \
	IFS=',' read -ra regions <<< "$$regions_input"; \
	echo "| Region | Service Name | Service Id | Service Type | Private DNS Name | Availability Zones |"; \
	echo "| ------ | ------------ | ---------- | ------------ | ---------------- | ------------------ |"; \
	for region in "$${regions[@]}"; do \
	    export AWS_DEFAULT_REGION=$$region ;\
	    results=$$(AWS_PROFILE=$$profile aws ec2 describe-vpc-endpoint-services --query 'ServiceDetails[?Owner!=`amazon`].[ServiceName,ServiceId,ServiceType[0].ServiceType,PrivateDnsName,AvailabilityZones]' --output json); \
	    echo "$$results" | jq -r --arg region $$region '.[] | "| \($$region) | \(.[0]) | \(.[1]) | \(.[2]) | \(.[3]) | \(.[4] | join(", ")) |"'; \
	done




