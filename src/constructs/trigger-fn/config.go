package main

import (
	"context"
	"strings"

	"github.com/sethvargo/go-envconfig"
	log "github.com/sirupsen/logrus"
)

type Config struct {
	CodepipelineName string `env:"CODEPIPELINE_NAME,required"`
	GithubBranch     string `env:"GITHUB_BRANCH,required"`
	Filters_         string `env:"FILTERS,required"`
	Filters          []string
}

func readConfigFromEnv() Config {
	var config Config
	ctx := context.Background()

	err := envconfig.Process(ctx, &config)
	if err != nil {
		log.Fatalln(err)
	}

	filters := strings.Split(config.Filters_, ",")
	config.Filters = filters

	return config
}
