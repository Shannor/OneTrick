package envvars

import (
	"os"
	"reflect"
	"testing"
)

func TestGetEvn(t *testing.T) {
	// Backup and defer restore of environment variables
	backup := os.Environ()
	defer func() {
		os.Clearenv()
		for _, env := range backup {
			pair := splitEnv(env)
			os.Setenv(pair[0], pair[1])
		}
	}()

	t.Run("all env vars set", func(t *testing.T) {
		os.Clearenv()
		os.Setenv(D2ApiKey, "test_api_key")
		os.Setenv(D2ClientID, "test_client_id")
		os.Setenv(D2ClientSecret, "test_client_secret")
		os.Setenv(Environment, "production")
		os.Setenv(AlgoliaAPIKey, "test_algolia_key")

		expected := Env{
			ApiKey:         "test_api_key",
			Environment:    ProductionEnv,
			D2ClientID:     "test_client_id",
			D2ClientSecret: "test_client_secret",
			AlgoliaAPIKey:  "test_algolia_key",
		}

		if got := GetEvn(); !reflect.DeepEqual(got, expected) {
			t.Errorf("GetEvn() = %v, want %v", got, expected)
		}
	})

	t.Run("environment defaults to dev", func(t *testing.T) {
		os.Clearenv()
		os.Setenv(D2ApiKey, "test_api_key")
		os.Setenv(D2ClientID, "test_client_id")
		os.Setenv(D2ClientSecret, "test_client_secret")

		got := GetEvn()
		if got.Environment != DevEnv {
			t.Errorf("Expected environment to default to dev, got %s", got.Environment)
		}
	})
}

func TestIsProd(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want bool
	}{
		{"production env", Env{Environment: ProductionEnv}, true},
		{"dev env", Env{Environment: DevEnv}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsProd(tt.env); got != tt.want {
				t.Errorf("IsProd() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsDev(t *testing.T) {
	tests := []struct {
		name string
		env  Env
		want bool
	}{
		{"production env", Env{Environment: ProductionEnv}, false},
		{"dev env", Env{Environment: DevEnv}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDev(tt.env); got != tt.want {
				t.Errorf("IsDev() = %v, want %v", got, tt.want)
			}
		})
	}
}

func splitEnv(env string) []string {
	var s []string
	for i := 0; i < len(env); i++ {
		if env[i] == '=' {
			s = append(s, env[:i])
			s = append(s, env[i+1:])
			return s
		}
	}
	// Return slice with empty strings if no '=' is found
	return []string{"", ""}
}
