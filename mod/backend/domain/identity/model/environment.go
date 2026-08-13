package model

import "os"

const (
	BootstrapAdminPasswordEnvironment = "MARKETPLACE_BOOTSTRAP_ADMIN_PASSWORD"
	APIKeyPepperEnvironment           = "MARKETPLACE_API_KEY_PEPPER"
	JWTSecretEnvironment              = "MARKETPLACE_JWT_SECRET"
)

type Environment interface {
	LookupEnv(key string) (string, bool)
}

type OSEnvironment struct{}

func (OSEnvironment) LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

type MapEnvironment map[string]string

func (environment MapEnvironment) LookupEnv(key string) (string, bool) {
	value, ok := environment[key]
	return value, ok
}
