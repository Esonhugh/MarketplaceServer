package identity

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

var ErrInvalidAPIKeyPepper = errors.New("identity: API key pepper must be valid base64 encoding at least 32 bytes")

func APIKeyPepper(environment Environment) ([]byte, error) {
	if environment == nil {
		return nil, ErrInvalidAPIKeyPepper
	}
	encoded, ok := environment.LookupEnv(APIKeyPepperEnvironment)
	if !ok || strings.TrimSpace(encoded) == "" {
		return nil, ErrInvalidAPIKeyPepper
	}
	pepper, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidAPIKeyPepper
	}
	if len(pepper) < auth.MinimumAPIKeyPepperBytes {
		return nil, ErrInvalidAPIKeyPepper
	}
	return pepper, nil
}

func EncodeAPIKeyPepper(pepper []byte) string {
	return base64.StdEncoding.EncodeToString(pepper)
}

func validateAPIKeyPepper(environment Environment) error {
	if _, err := APIKeyPepper(environment); err != nil {
		return fmt.Errorf("configure %s: %w", APIKeyPepperEnvironment, err)
	}
	return nil
}
