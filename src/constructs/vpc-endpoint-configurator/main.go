package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-lambda-go/cfn"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	log "github.com/sirupsen/logrus"
)

const PhyIdSeparator = ":"

type Config struct {
	NetworkLoadBalancerArns []string
	AllowedPrincipals       []string
	AcceptanceRequired      bool
	PrivateDnsName          string
}

func readResourceProperties(evt cfn.Event) (*Config, string, error) {
	networkLoadBalancerArns, err := readProperty[[]interface{}](evt, "NetworkLoadBalancerArns")
	if err != nil {
		return nil, "NetworkLoadBalancerArns", err
	}
	allowedPrincipals, err := readProperty[[]interface{}](evt, "AllowedPrincipals")
	if err != nil {
		return nil, "AllowedPrincipals", err
	}
	acceptanceRequired, err := readProperty[string](evt, "AcceptanceRequired")
	if err != nil {
		return nil, "AcceptanceRequired", err
	}
	privateDnsName, err := readProperty[string](evt, "PrivateDnsName")
	if err != nil {
		return nil, "PrivateDnsName", err
	}

	nlbArns := []string{}
	for _, v := range *networkLoadBalancerArns {
		nlbArns = append(nlbArns, v.(string))
	}
	arns := []string{}
	for _, v := range *allowedPrincipals {
		arns = append(arns, v.(string))
	}
	ar := false
	if *acceptanceRequired == "true" {
		ar = true
	}
	return &Config{
		NetworkLoadBalancerArns: nlbArns,
		AcceptanceRequired:      ar,
		AllowedPrincipals:       arns,
		PrivateDnsName:          *privateDnsName,
	}, "", nil
}

func main() {
	lambda.Start(func(ctx context.Context, evt cfn.Event) (cfn.Response, error) {
		return handler(ctx, evt)
	})
}

func handler(ctx context.Context, evt cfn.Event) (cfn.Response, error) {
	config, field, err := readResourceProperties(evt)
	if err != nil {
		return readPropertyErrorHandler(evt, field, err)
	}

	switch evt.RequestType {
	case cfn.RequestUpdate:
		return updateHandler(ctx, config, evt)
	case cfn.RequestCreate:
		return createHandler(ctx, config, evt)
	case cfn.RequestDelete:
		return deleteHandler(ctx, config, evt)
	}

	// This should be unreachable!
	resp := buildResponse(evt, cfn.StatusFailed, "encountered unreachable branch", nil, nil)
	if e := resp.Send(); e != nil {
		log.WithFields(log.Fields{
			"evt": evt,
		}).Fatalf("error in sending response: %v", e.Error())
	}

	return resp, nil
}

// deleteHandler parses the `service id` from physical resource id and deletes the service
// it exits silently if there is no such service
func deleteHandler(ctx context.Context, config *Config, evt cfn.Event) (cfn.Response, error) {
	log.Infoln("starting delete handler")

	stdLogFields := log.Fields{
		"network_load_balancer_arns": config.NetworkLoadBalancerArns,
		"acceptance_required":        config.AcceptanceRequired,
		"private_dns_name":           config.PrivateDnsName,
		"physical_resource_id":       evt.PhysicalResourceID,
	}

	phyResId := strings.Split(evt.PhysicalResourceID, PhyIdSeparator)
	if len(phyResId) < 2 {
		// There is no service id, just exit
		log.WithFields(stdLogFields).Warnf("did not find a service id in physical resource id, exiting")

		// exit silently
		resp := buildResponse(evt, cfn.StatusSuccess, fmt.Sprintf("could not reach service id from physical resource id: %s", evt.PhysicalResourceID), nil, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return resp, nil
	}

	serviceId := phyResId[1]

	sess := session.Must(session.NewSession())
	svc := ec2.New(sess)
	resp, err := svc.DeleteVpcEndpointServiceConfigurations(&ec2.DeleteVpcEndpointServiceConfigurationsInput{
		ServiceIds: aws.StringSlice([]string{serviceId}),
	})
	if err != nil {
		log.WithFields(stdLogFields).WithFields(log.Fields{
			"service_id": serviceId,
		}).Warnf("could not erase vpc endpoint configuration: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("could not erase vpc endpoint configuration: %s", err.Error()), &serviceId, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return resp, nil
	}

	if len(resp.Unsuccessful) != 0 {
		log.WithFields(stdLogFields).WithFields(log.Fields{
			"service_id": serviceId,
			"response":   resp.Unsuccessful,
		}).Warnf("did not find a service id in physical resource id, exiting")

		resp := buildResponse(evt, cfn.StatusFailed, "unsuccessful in erasing vpc endpoint configuration. please see logs", &serviceId, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return resp, nil
	}

	r := buildResponse(evt, cfn.StatusSuccess, "deleted successfully", &serviceId, evt.ResourceProperties)
	if e := r.Send(); e != nil {
		log.Fatalf("error in sending response: %v", e.Error())
	}

	return r, nil
}

func updateHandler(ctx context.Context, config *Config, evt cfn.Event) (cfn.Response, error) {
	log.Infoln("starting update handler")
	stdLogFields := log.Fields{
		"network_load_balancer_arns": config.NetworkLoadBalancerArns,
		"acceptance_required":        config.AcceptanceRequired,
		"private_dns_name":           config.PrivateDnsName,
		"physical_resource_id":       evt.PhysicalResourceID,
	}

	sess := session.Must(session.NewSession())
	svc := ec2.New(sess)

	phyResId := strings.Split(evt.PhysicalResourceID, PhyIdSeparator)
	if len(phyResId) < 2 {
		// There is no service id, just exit
		log.WithFields(stdLogFields).Warnf("did not find a service id in physical resource id, exiting")

		// exit silently
		resp := buildResponse(evt, cfn.StatusSuccess, fmt.Sprintf("could not reach service id from physical resource id: %s", evt.PhysicalResourceID), nil, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return resp, nil
	}

	serviceId := phyResId[1]

	// Disable private dns domain on this vpc endpoint
	// before doing any thing else.

	endpoint, err := svc.DescribeVpcEndpointServiceConfigurations(&ec2.DescribeVpcEndpointServiceConfigurationsInput{
		ServiceIds: aws.StringSlice([]string{serviceId}),
	})
	if err != nil {
		log.WithFields(stdLogFields).Errorf("error in describing vpc endpoint: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in describing vpc endpoint: %s", err.Error()), &serviceId, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in describing vpc endpoint: %v", err.Error())
	}

	if len(endpoint.ServiceConfigurations) == 0 {
		log.WithFields(stdLogFields).Errorln("service configurations array is empty")

		resp := buildResponse(evt, cfn.StatusFailed, "service configurations array is empty", &serviceId, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in describing vpc endpoint: %v", err.Error())
	}

	output := map[string]interface{}{
		"ServiceId":                        serviceId,
		"ServiceName":                      *endpoint.ServiceConfigurations[0].ServiceName,
		"PrivateDnsNameConfigurationName":  *endpoint.ServiceConfigurations[0].PrivateDnsNameConfiguration.Name,
		"PrivateDnsNameConfigurationType":  *endpoint.ServiceConfigurations[0].PrivateDnsNameConfiguration.Type,
		"PrivateDnsNameConfigurationValue": *endpoint.ServiceConfigurations[0].PrivateDnsNameConfiguration.Value,
		"PrivateDnsNameConfigurationState": *endpoint.ServiceConfigurations[0].PrivateDnsNameConfiguration.State,
	}
	for k, v := range evt.ResourceProperties {
		output[k] = v
	}

	resp, err := svc.DescribeVpcEndpointServicePermissions(&ec2.DescribeVpcEndpointServicePermissionsInput{
		ServiceId: aws.String(serviceId),
	})
	if err != nil {
		log.WithFields(stdLogFields).Errorf("error in describing vpc endpoint permissions: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in describing vpc endpoint permissions: %s", err.Error()), &serviceId, output)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in describing vpc endpoint permissions: %v", err.Error())
	}

	addPrincipals := map[string]struct{}{}
	for _, v := range config.AllowedPrincipals {
		addPrincipals[v] = struct{}{}
	}
	removePrincipals := map[string]struct{}{}

	for _, v := range resp.AllowedPrincipals {
		// Remove a principal if it doesn't exist in configurePrincipals
		if _, ok := addPrincipals[*v.Principal]; !ok {
			removePrincipals[*v.Principal] = struct{}{}
		} else {
			delete(addPrincipals, *v.Principal)
		}
	}

	addPrincipalsSlice := []string{}
	for k := range addPrincipals {
		addPrincipalsSlice = append(addPrincipalsSlice, k)
	}
	removePrincipalsSlice := []string{}
	for k := range removePrincipals {
		removePrincipalsSlice = append(removePrincipalsSlice, k)
	}
	permissionsInput := &ec2.ModifyVpcEndpointServicePermissionsInput{
		ServiceId: aws.String(serviceId),
	}
	if len(addPrincipalsSlice) != 0 {
		permissionsInput.AddAllowedPrincipals = aws.StringSlice(addPrincipalsSlice)
	}
	if len(removePrincipalsSlice) != 0 {
		permissionsInput.RemoveAllowedPrincipals = aws.StringSlice(removePrincipalsSlice)
	}
	_, err = svc.ModifyVpcEndpointServicePermissions(permissionsInput)
	if err != nil {
		log.WithFields(stdLogFields).Errorf("error in configuring vpc principals: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in configuring vpc principals: %s", err.Error()), &serviceId, output)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in configuring vpc principals: %v", err.Error())
	}

	// Configure every thing else about this endpoint

	_, err = svc.ModifyVpcEndpointServiceConfiguration(&ec2.ModifyVpcEndpointServiceConfigurationInput{
		ServiceId:          aws.String(serviceId),
		AcceptanceRequired: aws.Bool(config.AcceptanceRequired),
		PrivateDnsName:     aws.String(config.PrivateDnsName),
	})
	if err != nil {
		log.WithFields(stdLogFields).Errorf("error in configuring vpc endpoint: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in configuring vpc endpoint: %s", err.Error()), &serviceId, output)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in configuring vpc endpoint: %v", err.Error())
	}

	r := buildResponse(evt, cfn.StatusSuccess, "updated successfully", &serviceId, output)
	if e := r.Send(); e != nil {
		log.Fatalf("error in sending response: %v", e.Error())
	}

	return r, nil
}

func createHandler(ctx context.Context, config *Config, evt cfn.Event) (cfn.Response, error) {
	log.Infoln("starting create handler")
	stdLogFields := log.Fields{
		"network_load_balancer_arns": config.NetworkLoadBalancerArns,
		"acceptance_required":        config.AcceptanceRequired,
		"private_dns_name":           config.PrivateDnsName,
	}
	sess := session.Must(session.NewSession())
	svc := ec2.New(sess)

	resp, err := svc.CreateVpcEndpointServiceConfiguration(&ec2.CreateVpcEndpointServiceConfigurationInput{
		AcceptanceRequired:      &config.AcceptanceRequired,
		NetworkLoadBalancerArns: aws.StringSlice(config.NetworkLoadBalancerArns),
		PrivateDnsName:          aws.String(config.PrivateDnsName),
	})
	if err != nil {
		log.WithFields(stdLogFields).Errorf("error in creating vpc endpoint: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in creating vpc endpoint: %s", err.Error()), nil, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in creating vpc endpoint: %v", err.Error())
	}

	_, err = svc.ModifyVpcEndpointServicePermissions(&ec2.ModifyVpcEndpointServicePermissionsInput{
		ServiceId:            resp.ServiceConfiguration.ServiceId,
		AddAllowedPrincipals: aws.StringSlice(config.AllowedPrincipals),
	})
	if err != nil {
		log.WithFields(stdLogFields).Errorf("error in configuring vpc principals: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in configuring vpc principals: %s", err.Error()), resp.ServiceConfiguration.ServiceId, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in configuring vpc principals: %v", err.Error())
	}

	r := buildResponse(evt, cfn.StatusSuccess, "created successfully", resp.ServiceConfiguration.ServiceId, map[string]interface{}{
		"ServiceId":                        *resp.ServiceConfiguration.ServiceId,
		"ServiceName":                      *resp.ServiceConfiguration.ServiceName,
		"PrivateDnsNameConfigurationName":  *resp.ServiceConfiguration.PrivateDnsNameConfiguration.Name,
		"PrivateDnsNameConfigurationType":  *resp.ServiceConfiguration.PrivateDnsNameConfiguration.Type,
		"PrivateDnsNameConfigurationValue": *resp.ServiceConfiguration.PrivateDnsNameConfiguration.Value,
		"PrivateDnsNameConfigurationState": *resp.ServiceConfiguration.PrivateDnsNameConfiguration.State,
	})
	if e := r.Send(); e != nil {
		log.Fatalf("error in sending response: %v", e.Error())
	}
	return r, nil
}

func readPropertyErrorHandler(evt cfn.Event, propertyName string, err error) (cfn.Response, error) {
	log.Errorf("error in reading %s: %v\n", propertyName, err.Error())

	resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in reading property: %s", propertyName), nil, nil)
	if e := resp.Send(); e != nil {
		log.Fatalf("error in sending response: %v", e.Error())
	}
	return resp, fmt.Errorf("error in reading %s: %v", propertyName, err.Error())
}

type ResourceProperty interface {
	~int | ~int64 | ~string | ~[]string | ~bool | ~[]interface{}
}

func readProperty[T ResourceProperty](evt cfn.Event, propertyName string) (*T, error) {
	d, ok := evt.ResourceProperties[propertyName]
	if !ok {
		return nil, fmt.Errorf("could not find %s", propertyName)
	}

	v, ok := d.(T)
	if !ok {
		return nil, fmt.Errorf("property %s is not the generic type T", propertyName)
	}

	return &v, nil
}

func buildResponse(evt cfn.Event, status cfn.StatusType, reason string, serviceId *string, data map[string]interface{}) cfn.Response {
	resp := cfn.NewResponse(&evt)
	resp.Status = status
	resp.Reason = reason
	resp.Data = data

	if evt.PhysicalResourceID != "" {
		resp.PhysicalResourceID = evt.PhysicalResourceID
	} else if serviceId != nil {
		resp.PhysicalResourceID = fmt.Sprintf("VpcEndpointService%s%s", PhyIdSeparator, *serviceId)
	} else {
		resp.PhysicalResourceID = "VpcEndpointService"
	}

	return *resp
}
