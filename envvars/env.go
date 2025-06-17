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
	Environment    EnvironmentKey
	D2ClientID     string
	D2ClientSecret string
	RedirectURI    string
}

type EnvironmentKey string

const (
	ProductionEnv EnvironmentKey = "production"
	DevEnv        EnvironmentKey = "dev"
)

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
		Environment:    EnvironmentKey(environment),
		D2ClientID:     clientID,
		D2ClientSecret: clientSecret,
	}
}

func IsProd(env Env) bool {
	return env.Environment == ProductionEnv
}

func IsDev(env Env) bool {
	return env.Environment == DevEnv
}
