package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	APIKeyPrefix             = "mpsk_"
	APIKeyIndexPrefix        = "mpsk_hmac_sha256_"
	APIKeyRandomBytes        = 32
	APIKeyEncodedLength      = 43
	MinimumAPIKeyPepperBytes = 32
)

var (
	ErrMalformedAPIKey      = errors.New("auth: malformed API key")
	ErrMalformedAPIKeyIndex = errors.New("auth: malformed API key index")
	ErrWeakAPIKeyPepper     = errors.New("auth: API key pepper must be at least 32 bytes")
)

func GenerateAPIKey() (string, error) {
	secret := make([]byte, APIKeyRandomBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("auth: generate API key: %w", err)
	}
	return APIKeyPrefix + base64.RawURLEncoding.EncodeToString(secret), nil
}

func HasAPIKeyPrefix(plaintext string) bool {
	return strings.HasPrefix(plaintext, APIKeyPrefix)
}

func ValidateAPIKey(plaintext string) error {
	if !HasAPIKeyPrefix(plaintext) || len(plaintext) != len(APIKeyPrefix)+APIKeyEncodedLength {
		return ErrMalformedAPIKey
	}
	payload := strings.TrimPrefix(plaintext, APIKeyPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(decoded) != APIKeyRandomBytes {
		return ErrMalformedAPIKey
	}
	return nil
}

func ValidateAPIKeyIndex(index string) error {
	if !strings.HasPrefix(index, APIKeyIndexPrefix) {
		return ErrMalformedAPIKeyIndex
	}
	payload := strings.TrimPrefix(index, APIKeyIndexPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(decoded) != sha256.Size || base64.RawURLEncoding.EncodeToString(decoded) != payload {
		return ErrMalformedAPIKeyIndex
	}
	return nil
}

func IndexAPIKey(plaintext string, pepper []byte) (string, error) {
	if len(pepper) < MinimumAPIKeyPepperBytes {
		return "", ErrWeakAPIKeyPepper
	}
	if err := ValidateAPIKey(plaintext); err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(plaintext))
	return APIKeyIndexPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
