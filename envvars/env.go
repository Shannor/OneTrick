package envvars

import (
	"log"
	"log/slog"
	"os"
)

const (
	D2ApiKey       = "D2_API_KEY"
	D2ClientID     = "D2_CLIENT_ID"
	D2ClientSecret = "D2_CLIENT_SECRET"
	D2RedirectURI  = "D2_REDIRECT_URI"
	Environment    = "ENVIRONMENT"
)

type Env struct {
	ApiKey         string
	Environment    string
	D2ClientID     string
	D2ClientSecret string
	RedirectURI    string
}

func GetEvn() Env {
	apiKey, ok := os.LookupEnv(D2ApiKey)
	if !ok {
		log.Fatalf("%s required", D2ApiKey)
	}
	clientID, ok := os.LookupEnv(D2ClientID)
	if !ok {
		log.Fatalf("%s required", D2ApiKey)
	}
	clientSecret, ok := os.LookupEnv(D2ClientSecret)
	if !ok {
		log.Fatalf("%s required", D2ClientSecret)
	}
	environment, ok := os.LookupEnv(Environment)
	if !ok {
		slog.Debug("Environment not set, defaulting to dev")
		environment = "dev"
	}

	return Env{
		ApiKey:         apiKey,
		Environment:    environment,
		D2ClientID:     clientID,
		D2ClientSecret: clientSecret,
	}
}
