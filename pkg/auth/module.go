package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt"
)

// ErrLegacyJWTDisabled is returned by the retained legacy JWT API. The former
// hard-coded signing key was insecure; callers must migrate to an injected
// credential service before issuing or accepting JWTs.
var ErrLegacyJWTDisabled = errors.New("auth: legacy JWT support is disabled")

// Info is retained only so dormant legacy callers continue to compile.
type Info struct {
	UID            string
	OrgID          string
	IsRefreshToken bool
}

// JWTClaims is retained only so dormant legacy callers continue to compile.
type JWTClaims struct {
	Info Info
	jwt.StandardClaims
}

const (
	AccessTokenExpireIn  = time.Hour * 24
	RefreshTokenExpireIn = time.Hour * 24 * 30
)

// GenToken is a fail-closed compatibility shim for the retired hard-coded JWT implementation.
func GenToken(Info, ...time.Duration) (string, error) {
	return "", ErrLegacyJWTDisabled
}

// ParseToken is a fail-closed compatibility shim for the retired hard-coded JWT implementation.
func ParseToken(string) (*JWTClaims, error) {
	return nil, ErrLegacyJWTDisabled
}
