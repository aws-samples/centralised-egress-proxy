package main

import (
	"context"
	"fmt"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/codepipeline"
	lambdas "github.com/aws/aws-sdk-go/service/lambda"
	"github.com/aws/aws-sdk-go/service/ssm"

	log "github.com/sirupsen/logrus"
)

type Input struct {
	CodepipelineJob struct {
		ID        string `json:"id"`
		AccountId string `json:"accountId"`
	} `json:"CodePipeline.job"`
}

func main() {
	config := readConfigFromEnv()

	lambda.Start(func(ctx context.Context, evt Input) error {
		return handler(ctx, evt, config)
	})
}

func handler(ctx context.Context, ip Input, config Config) error {
	lc, _ := lambdacontext.FromContext(ctx)
	sess := session.Must(session.NewSession())
	cpsvc := codepipeline.New(sess)
	lambdasvc := lambdas.New(sess)
	ssmsvc := ssm.New(sess)

	imageName, err := ssmsvc.GetParameter(&ssm.GetParameterInput{
		Name: aws.String(config.ImageParameterName),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"input": ip,
		}).Warnf("error in reading image name from ssm: %v", err.Error())

		_, err := cpsvc.PutJobFailureResult(&codepipeline.PutJobFailureResultInput{
			JobId: aws.String(ip.CodepipelineJob.ID),
			FailureDetails: &codepipeline.FailureDetails{
				Message:             aws.String(fmt.Sprintf("error in reading image name from ssm: %v", err.Error())),
				Type:                aws.String(codepipeline.FailureTypeJobFailed),
				ExternalExecutionId: &lc.AwsRequestID,
			},
		})
		if err != nil {
			log.WithFields(log.Fields{
				"input": ip,
			}).Warnf("could not write failure job result: %v", err.Error())
		}

		return nil
	}
	imageTag, err := ssmsvc.GetParameter(&ssm.GetParameterInput{
		Name: aws.String(config.ImageTagParameterName),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"input": ip,
		}).Warnf("error in reading image tag from ssm: %v", err.Error())

		_, err := cpsvc.PutJobFailureResult(&codepipeline.PutJobFailureResultInput{
			JobId: aws.String(ip.CodepipelineJob.ID),
			FailureDetails: &codepipeline.FailureDetails{
				Message:             aws.String(fmt.Sprintf("error in reading image tag from ssm: %v", err.Error())),
				Type:                aws.String(codepipeline.FailureTypeJobFailed),
				ExternalExecutionId: &lc.AwsRequestID,
			},
		})
		if err != nil {
			log.WithFields(log.Fields{
				"input": ip,
			}).Warnf("could not write failure job result: %v", err.Error())
		}

		return nil
	}

	// Publish a new version with the new Image URI
	imageUri := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s:%s", ip.CodepipelineJob.AccountId, config.Region, *imageName.Parameter.Value, *imageTag.Parameter.Value)
	log.WithField("image_uri", imageUri).Infoln("proceeding with given image uri")

	resp, err := lambdasvc.UpdateFunctionCode(&lambdas.UpdateFunctionCodeInput{
		FunctionName: aws.String(config.FunctionArn),
		ImageUri:     aws.String(imageUri),
		Publish:      aws.Bool(true),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"image_uri":    imageUri,
			"function_arn": config.FunctionArn,
		}).Errorf("error in updating function code: %v", err.Error())

		_, err = cpsvc.PutJobFailureResult(&codepipeline.PutJobFailureResultInput{
			JobId: aws.String(ip.CodepipelineJob.ID),
			FailureDetails: &codepipeline.FailureDetails{
				Message:             aws.String(fmt.Sprintf("error in updating function code: %v", err.Error())),
				Type:                aws.String(codepipeline.FailureTypeJobFailed),
				ExternalExecutionId: &lc.AwsRequestID,
			},
		})
		if err != nil {
			log.WithFields(log.Fields{
				"input": ip,
			}).Warnf("could not write failure job result: %v", err.Error())
		}

		return nil
	}

	log.WithFields(log.Fields{
		"function_arn":  config.FunctionArn,
		"version":       aws.StringValue(resp.Version),
		"last_modified": aws.StringValue(resp.LastModified),
	}).Infoln("updated function code and published new version")

	_, err = cpsvc.PutJobSuccessResult(&codepipeline.PutJobSuccessResultInput{
		JobId: aws.String(ip.CodepipelineJob.ID),
		ExecutionDetails: &codepipeline.ExecutionDetails{
			ExternalExecutionId: &lc.AwsRequestID,
			PercentComplete:     aws.Int64(100),
			Summary:             aws.String("Created new version of lambda with newest release and published a new version"),
		},
		CurrentRevision: &codepipeline.CurrentRevision{
			Revision:         resp.RevisionId,
			ChangeIdentifier: resp.CodeSha256,
		},
	})
	if err != nil {
		log.WithFields(log.Fields{
			"input": ip,
		}).Warnf("could not write success job result: %v", err.Error())
	}

	return nil
}
