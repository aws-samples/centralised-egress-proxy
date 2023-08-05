package main

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-lambda-go/cfn"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ssm"
	log "github.com/sirupsen/logrus"
)

const PhyIdSeparator = ":"

type Config struct {
	Region              string
	ParameterName       string
	CrossAccountRoleArn string
}

func readResourceProperties(evt cfn.Event) (*Config, string, error) {

	name, err := readProperty[string](evt, "ParameterName")
	if err != nil {
		return nil, "ParameterName", err
	}
	region, err := readProperty[string](evt, "Region")
	if err != nil {
		return nil, "Region", err
	}
	role, err := readProperty[string](evt, "CrossAccountRoleArn")
	if err != nil {
		return nil, "CrossAccountRoleArn", err
	}

	return &Config{
		Region:              *region,
		ParameterName:       *name,
		CrossAccountRoleArn: *role,
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
	case cfn.RequestUpdate, cfn.RequestCreate:
		return readParam(ctx, config, evt)
	}

	// This should be unreachable!
	resp := buildResponse(evt, cfn.StatusSuccess, "execution successfull", nil)
	if e := resp.Send(); e != nil {
		log.WithFields(log.Fields{
			"evt": evt,
		}).Fatalf("error in sending response: %v", e.Error())
	}

	return resp, nil
}

func readParam(ctx context.Context, config *Config, evt cfn.Event) (cfn.Response, error) {
	stdLogFields := log.Fields{
		"region":         config.Region,
		"parameter_name": config.ParameterName,
	}
	sess := session.Must(session.NewSession())
	creds := stscreds.NewCredentials(sess, config.CrossAccountRoleArn)
	svc := ssm.New(sess, &aws.Config{
		Credentials: creds,
		Region:      aws.String(config.Region),
	})

	resp, err := svc.GetParameter(&ssm.GetParameterInput{
		Name: aws.String(config.ParameterName),
	})
	if err != nil {
		log.WithFields(stdLogFields).Errorf("error in reading parameter: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in reading parameter: %s", err.Error()), nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in reading parameter: %v", err.Error())
	}

	r := buildResponse(evt, cfn.StatusSuccess, "read successfully", map[string]interface{}{
		"Value": resp.Parameter.Value,
		"Type":  resp.Parameter.DataType,
	})
	if e := r.Send(); e != nil {
		log.Fatalf("error in sending response: %v", e.Error())
	}
	return r, nil
}

func readPropertyErrorHandler(evt cfn.Event, propertyName string, err error) (cfn.Response, error) {
	log.Errorf("error in reading %s: %v\n", propertyName, err.Error())

	resp := buildResponse(evt, cfn.StatusFailed, fmt.Sprintf("error in reading property: %s", propertyName), nil)
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

func buildResponse(evt cfn.Event, status cfn.StatusType, reason string, data map[string]interface{}) cfn.Response {
	resp := cfn.NewResponse(&evt)
	resp.Status = status
	resp.Reason = reason
	resp.Data = data

	if evt.PhysicalResourceID != "" {
		resp.PhysicalResourceID = evt.PhysicalResourceID
	} else {
		// We include time in the phy resource id
		// because we want to read it fresh every single time instead of returning potentially stale values
		resp.PhysicalResourceID = fmt.Sprintf("SSMParameter%s%s", PhyIdSeparator, time.Now().UTC().Format(time.RFC3339))
	}

	return *resp
}
