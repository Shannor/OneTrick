package envvars

import (
	"log"
	"os"
)

type Env struct {
	ApiKey      string
	Environment string
}

func GetEvn() Env {
	apiKey, ok := os.LookupEnv("D2_API_KEY")
	if !ok {
		log.Fatalf("D2_API_KEY required")
	}
	environment, ok := os.LookupEnv("ENVIRONMENT")
	if !ok {
		environment = "dev"
	}
	return Env{
		ApiKey:      apiKey,
		Environment: environment,
	}
}
