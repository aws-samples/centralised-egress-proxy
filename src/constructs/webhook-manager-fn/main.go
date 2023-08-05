package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/cfn"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/secretsmanager"
	"github.com/google/go-github/github"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

type Config struct {
	GithubOwner  string
	GithubRepo   string
	GithubBranch string
	GithubToken  string
	WebhookURL   string
}

func readResourceProperties(evt cfn.Event) (*Config, string, error) {
	ghOwner, err := readProperty[string](evt, "GithubOwner")
	if err != nil {
		return nil, "GithubOwner", err
	}
	ghRepo, err := readProperty[string](evt, "GithubRepo")
	if err != nil {
		return nil, "GithubRepo", err
	}
	ghBranch, err := readProperty[string](evt, "GithubBranch")
	if err != nil {
		return nil, "GithubBranch", err
	}

	ghTokenArn, err := readProperty[string](evt, "GithubTokenArn")
	if err != nil {
		return nil, "GithubTokenArn", err
	}
	ghToken, err := readGithubToken(*ghTokenArn)
	if err != nil {
		return nil, "GithubTokenArn", fmt.Errorf("error in reading secret from secretsmanager: %v", err.Error())
	}

	webhookURL, err := readProperty[string](evt, "WebhookURL")
	if err != nil {
		return nil, "WebhookURL", err
	}

	return &Config{
		GithubOwner:  *ghOwner,
		GithubRepo:   *ghRepo,
		GithubBranch: *ghBranch,
		GithubToken:  *ghToken,
		WebhookURL:   *webhookURL,
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
	resp := buildResponse(evt, cfn.StatusFailed, nil)
	if e := resp.Send(); e != nil {
		log.WithFields(log.Fields{
			"evt": evt,
		}).Fatalf("error in sending response: %v", e.Error())
	}

	return resp, nil
}

// deleteHandler parses the `hookid` from physical resource id and deletes the corresponding webhook
// if there is no hook id then it just exits silently
func deleteHandler(ctx context.Context, config *Config, evt cfn.Event) (cfn.Response, error) {
	log.Infoln("starting delete handler")

	phyResId := strings.Split(evt.PhysicalResourceID, PhyIdSeparator)
	if len(phyResId) < 2 {
		// There is no hook id, just exit
		log.WithFields(log.Fields{
			"physical_resource_id": evt.PhysicalResourceID,
		}).Warnf("did not find a hook id in physical resource id, exiting")

		// exit silently
		resp := buildResponse(evt, cfn.StatusSuccess, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return resp, nil
	}

	ghClient := github.NewClient(oauth2.NewClient(ctx, &Token{
		PersonalAccessToken: config.GithubToken,
	}))

	hookIdStr := phyResId[1]
	hookId, err := strconv.ParseInt(hookIdStr, 10, 64)
	if err != nil {
		log.WithFields(log.Fields{
			"physical_resource_id": evt.PhysicalResourceID,
		}).Warnf("could not parse hook id from the physical resource id: %v", err.Error())

		// exit silently
		resp := buildResponse(evt, cfn.StatusSuccess, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return resp, nil
	}

	_, err = ghClient.Repositories.DeleteHook(ctx, config.GithubOwner, config.GithubRepo, hookId)
	if err != nil {
		log.WithFields(log.Fields{
			"webhook_url": config.WebhookURL,
			"gh_owner":    config.GithubOwner,
			"gh_repo":     config.GithubRepo,
			"gh_branch":   config.GithubBranch,
		}).Errorf("error in deleting webhook: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return resp, fmt.Errorf("error in  deleting webhook: %v", err.Error())
	}

	r := buildResponse(evt, cfn.StatusSuccess, nil)
	if e := r.Send(); e != nil {
		log.Fatalf("error in sending response: %v", e.Error())
	}

	return r, nil
}

// updateHandler creates a webhook in the github repo and when it is successfull returns a physical id like,
// githubwebhookmanager-${hookid}
// The existing hookid will be different since updateHandler is also just creating another webhook
// Soo, Cloudformation will issue a delete request with that previous hookid(or actually the physical resource id) automatically.
func updateHandler(ctx context.Context, config *Config, evt cfn.Event) (cfn.Response, error) {
	log.Infoln("starting update handler")

	ghClient := github.NewClient(oauth2.NewClient(ctx, &Token{
		PersonalAccessToken: config.GithubToken,
	}))

	hook, resp, err := ghClient.Repositories.CreateHook(ctx, config.GithubOwner, config.GithubRepo, &github.Hook{
		Config: map[string]interface{}{
			"url":          config.WebhookURL,
			"content_type": "json",
			"insecure_ssl": "0",
			// TODO: Look into adding a secret and validating HMAC
		},
		// Note
		// `push` event is not triggered if the changes were pushed to more than 3 tags/branches at once
		// https://docs.github.com/en/developers/webhooks-and-events/webhooks/webhook-events-and-payloads#push
		Events: []string{"push"},
	})
	if err != nil {
		log.WithFields(log.Fields{
			"webhook_url": config.WebhookURL,
			"gh_owner":    config.GithubOwner,
			"gh_repo":     config.GithubRepo,
			"gh_branch":   config.GithubBranch,
		}).Errorf("error in registering webhook: %v", err.Error())

		resp := buildResponse(evt, cfn.StatusFailed, nil)
		if e := resp.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return resp, fmt.Errorf("error in registering webhook: %v", err.Error())
	}

	switch resp.Response.StatusCode {
	// https://docs.github.com/en/rest/orgs/webhooks#update-an-organization-webhook
	case 201:
		r := buildResponse(evt, cfn.StatusSuccess, hook.ID)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, nil
	case 403:
		r := buildResponse(evt, cfn.StatusFailed, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, fmt.Errorf("received 403 in response to create webhook")
	case 404:
		r := buildResponse(evt, cfn.StatusFailed, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, fmt.Errorf("received 404 in response to create webhook")
	case 422:

		log.WithFields(log.Fields{
			"webhook_url": config.WebhookURL,
			"gh_owner":    config.GithubOwner,
			"gh_repo":     config.GithubRepo,
			"gh_branch":   config.GithubBranch,
		}).Infoln("got 422 response from github. webhook already exists")

		r := buildResponse(evt, cfn.StatusSuccess, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return r, nil

	default:
		r := buildResponse(evt, cfn.StatusFailed, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, fmt.Errorf("received %d in response to create webhook", resp.Response.StatusCode)
	}
}

// createHandler creates a webhook in the github repo and when it is successfull returns a physical id like,
// githubwebhookmanager-${hookid}
func createHandler(ctx context.Context, config *Config, evt cfn.Event) (cfn.Response, error) {
	log.Infoln("starting create handler")

	ghClient := github.NewClient(oauth2.NewClient(ctx, &Token{
		PersonalAccessToken: config.GithubToken,
	}))

	hook, resp, err := ghClient.Repositories.CreateHook(ctx, config.GithubOwner, config.GithubRepo, &github.Hook{
		Config: map[string]interface{}{
			"url":          config.WebhookURL,
			"content_type": "json",
			"insecure_ssl": "0",
			// TODO: Look into adding a secret and validating HMAC
		},
		// Note
		// `push` event is not triggered if the changes were pushed to more than 3 tags/branches at once
		// https://docs.github.com/en/developers/webhooks-and-events/webhooks/webhook-events-and-payloads#push
		Events: []string{"push"},
	})
	if err != nil {
		log.WithFields(log.Fields{
			"webhook_url": config.WebhookURL,
			"gh_owner":    config.GithubOwner,
			"gh_repo":     config.GithubRepo,
			"gh_branch":   config.GithubBranch,
		}).Errorf("error in registering webhook: %v", err.Error())

		r := buildResponse(evt, cfn.StatusFailed, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, fmt.Errorf("error in registering webhook: %v", err.Error())
	}

	switch resp.Response.StatusCode {
	case 201:
		r := buildResponse(evt, cfn.StatusSuccess, hook.ID)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}

		return r, nil
	case 403:
		r := buildResponse(evt, cfn.StatusFailed, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, fmt.Errorf("received 403 in response to create webhook")
	case 404:
		r := buildResponse(evt, cfn.StatusFailed, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, fmt.Errorf("received 404 in response to create webhook")
	case 422:

		log.WithFields(log.Fields{
			"webhook_url": config.WebhookURL,
			"gh_owner":    config.GithubOwner,
			"gh_repo":     config.GithubRepo,
			"gh_branch":   config.GithubBranch,
		}).Infoln("got 422 response from github. webhook already exists")

		r := buildResponse(evt, cfn.StatusSuccess, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, nil

	default:
		r := buildResponse(evt, cfn.StatusFailed, nil)
		if e := r.Send(); e != nil {
			log.Fatalf("error in sending response: %v", e.Error())
		}
		return r, fmt.Errorf("received %d in response to create webhook", resp.Response.StatusCode)
	}
}

type Token struct {
	PersonalAccessToken string
}

func (t *Token) Token() (*oauth2.Token, error) {
	token := &oauth2.Token{
		AccessToken: t.PersonalAccessToken,
	}
	return token, nil
}

func readPropertyErrorHandler(evt cfn.Event, propertyName string, err error) (cfn.Response, error) {
	log.Errorf("error in reading %s: %v\n", propertyName, err.Error())

	resp := buildResponse(evt, cfn.StatusFailed, nil)
	if e := resp.Send(); e != nil {
		log.Fatalf("error in sending response: %v", e.Error())
	}
	return resp, fmt.Errorf("error in reading %s: %v", propertyName, err.Error())
}

type ResourceProperty interface {
	~int | ~int64 | ~string
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

const PhyIdSeparator = "-"

func buildResponse(evt cfn.Event, status cfn.StatusType, hookID *int64) cfn.Response {
	resp := cfn.NewResponse(&evt)
	resp.Status = status

	if evt.PhysicalResourceID != "" {
		resp.PhysicalResourceID = evt.PhysicalResourceID
	} else if hookID != nil {
		resp.PhysicalResourceID = fmt.Sprintf("githubwebhookmanager%s%d", PhyIdSeparator, *hookID)
	} else {
		resp.PhysicalResourceID = "githubwebhookmanager"
	}

	return *resp
}

func readGithubToken(secretArn string) (*string, error) {
	sess := session.Must(session.NewSession())
	secretssvc := secretsmanager.New(sess)

	resp, err := secretssvc.GetSecretValue(&secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretArn),
	})
	if err != nil {
		return nil, fmt.Errorf("error in reading secret %s: %v", secretArn, err.Error())
	}

	return resp.SecretString, nil
}
