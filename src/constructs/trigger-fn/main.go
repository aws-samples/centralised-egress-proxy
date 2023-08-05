package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/codepipeline"
	log "github.com/sirupsen/logrus"
)

type GithubEvent struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		ID    int `json:"id"`
		Owner struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"owner"`
		CreatedAt int       `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		PushedAt  int       `json:"pushed_at"`
	} `json:"repository"`
	Pusher struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"pusher"`
	Commits    []Commit `json:"commits"`
	HeadCommit Commit   `json:"head_commit"`
	HookID     int      `json:"hook_id"`
	Hook       struct {
		Type   string   `json:"type"`
		ID     int      `json:"id"`
		Name   string   `json:"name"`
		Active bool     `json:"active"`
		Events []string `json:"events"`
		Config struct {
			ContentType string `json:"content_type"`
			InsecureSsl string `json:"insecure_ssl"`
			URL         string `json:"url"`
		} `json:"config"`
		UpdatedAt     time.Time `json:"updated_at"`
		CreatedAt     time.Time `json:"created_at"`
		URL           string    `json:"url"`
		TestURL       string    `json:"test_url"`
		PingURL       string    `json:"ping_url"`
		DeliveriesURL string    `json:"deliveries_url"`
		LastResponse  struct {
			Code    interface{} `json:"code"`
			Status  string      `json:"status"`
			Message interface{} `json:"message"`
		} `json:"last_response"`
	} `json:"hook"`
}

type Commit struct {
	ID        string    `json:"id"`
	TreeID    string    `json:"tree_id"`
	Distinct  bool      `json:"distinct"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	URL       string    `json:"url"`
	Author    struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Username string `json:"username"`
	} `json:"author"`
	Committer struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Username string `json:"username"`
	} `json:"committer"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

func main() {
	config := readConfigFromEnv()

	lambda.Start(func(ctx context.Context, evt events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
		return handler(ctx, config, evt)
	})
}

func handler(ctx context.Context, config Config, evt events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	// Ignore if the event type is not `push`
	if v, ok := evt.Headers["x-github-event"]; ok {
		if v != "push" {
			log.Infof("event type is %s, ignoring it\n", v)
			return buildResponse(http.StatusOK)
		}
	}

	ghEvt := GithubEvent{}
	err := json.Unmarshal([]byte(evt.Body), &ghEvt)
	if err != nil {
		log.WithFields(log.Fields{
			"request_body": evt.Body,
		}).Errorf("error in umarshalling request body: %v", err.Error())

		return events.LambdaFunctionURLResponse{
			StatusCode: http.StatusBadRequest,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		}, nil
	}

	// Check the branch
	branch := strings.TrimPrefix(ghEvt.Ref, "refs/heads/")

	if branch != config.GithubBranch {
		log.WithFields(log.Fields{
			"config_branch": config.GithubBranch,
			"evt_branch":    branch,
			"author":        ghEvt.HeadCommit.Author.Username,
			"pushed_at":     ghEvt.Repository.PushedAt,
		}).Infof("ignoring event. branch is not equal to config.GithubBranch")

		return buildResponse(http.StatusOK)
	}

	if !checkCommits(ghEvt.Commits, config.Filters) {
		log.WithFields(log.Fields{
			"branch":      branch,
			"head_commit": ghEvt.HeadCommit.ID,
			"author":      ghEvt.HeadCommit.Author.Username,
			"pushed_at":   ghEvt.Repository.PushedAt,
		}).Infoln("skipping event, did not find any matching changes")

		return buildResponse(http.StatusOK)
	}

	sess := session.Must(session.NewSession())
	cpsvc := codepipeline.New(sess)

	resp, err := cpsvc.StartPipelineExecution(&codepipeline.StartPipelineExecutionInput{
		Name: aws.String(config.CodepipelineName),
	})
	if err != nil {
		log.WithFields(log.Fields{
			"codepipeline_name": config.CodepipelineName,
			"branch":            branch,
			"head_commit":       ghEvt.HeadCommit.ID,
			"author":            ghEvt.HeadCommit.Author.Username,
			"pushed_at":         ghEvt.Repository.PushedAt,
		}).Errorf("error in starting codepipeline: %v", err.Error())

		return buildResponse(http.StatusInternalServerError)
	}

	log.WithFields(log.Fields{
		"pipeline_execution_id": *resp.PipelineExecutionId,
		"codepipeline_name":     config.CodepipelineName,
		"branch":                branch,
		"head_commit":           ghEvt.HeadCommit.ID,
		"author":                ghEvt.HeadCommit.Author.Username,
		"pushed_at":             ghEvt.Repository.PushedAt,
	}).Infoln("started codepipeline")

	body := map[string]string{
		"pipeline_execution_id": *resp.PipelineExecutionId,
		"pipeline_name":         config.CodepipelineName,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return buildResponse(http.StatusInternalServerError)
	}

	return events.LambdaFunctionURLResponse{
		StatusCode: http.StatusOK,
		Body:       string(b),
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		IsBase64Encoded: false,
	}, nil
}

func checkCommits(commits []Commit, filters []string) bool {
	// Check _all_ commits in the push
	for _, commit := range commits {
		if findMatch(commit.Modified, filters) ||
			findMatch(commit.Added, filters) ||
			findMatch(commit.Removed, filters) {
			return true
		}
	}

	return false
}

func findMatch(files []string, filters []string) bool {
	for _, filter := range filters {
		for _, file := range files {

			if strings.HasPrefix(file, filter) {
				return true
			}
		}
	}

	return false
}

func buildResponse(statusCode int) (events.LambdaFunctionURLResponse, error) {
	return events.LambdaFunctionURLResponse{
		StatusCode: statusCode,
	}, nil
}
