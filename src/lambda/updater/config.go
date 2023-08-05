package main

import (
	"context"
	"log"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	FunctionArn           string `env:"FUNCTION_ARN,required"`
	ImageParameterName    string `env:"IMAGE_PARAMETER_NAME,required"`
	ImageTagParameterName string `env:"IMAGE_TAG_PARAMETER_NAME,required"`
	Region                string `env:"REGION,required"`
}

func readConfigFromEnv() Config {
	var config Config
	ctx := context.Background()

	err := envconfig.Process(ctx, &config)
	if err != nil {
		log.Fatalln(err)
	}
	return config
}
