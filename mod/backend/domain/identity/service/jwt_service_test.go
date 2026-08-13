package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	jwt "github.com/golang-jwt/jwt"
)

func TestJWTServiceIssuesAndVerifiesExactThirtyDayClaims(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	service, err := NewJWTService("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }

	token, expiresAt, err := service.Issue("alice")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !expiresAt.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Fatalf("expiresAt = %v", expiresAt)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token segments = %d", len(parts))
	}
	decode := func(segment string) map[string]any {
		payload, decodeErr := base64.RawURLEncoding.DecodeString(segment)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		var value map[string]any
		if decodeErr := json.Unmarshal(payload, &value); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return value
	}
	header, claims := decode(parts[0]), decode(parts[1])
	if len(header) != 2 || header["alg"] != "HS256" || header["typ"] != "JWT" {
		t.Fatalf("header = %#v", header)
	}
	if len(claims) != 3 || claims["username"] != "alice" {
		t.Fatalf("claims = %#v", claims)
	}
	if got, err := service.Verify(token); err != nil || got != "alice" {
		t.Fatalf("Verify() = %q, %v", got, err)
	}
}

func TestJWTServiceStrictlyRejectsInvalidAlgorithmClaimsAndLifetime(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	service, err := NewJWTService("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }

	sign := func(method jwt.SigningMethod, claims jwt.MapClaims, header ...map[string]any) string {
		token := jwt.NewWithClaims(method, claims)
		if len(header) == 1 {
			token.Header = header[0]
		}
		signed, signErr := token.SignedString([]byte("test-secret"))
		if signErr != nil {
			t.Fatal(signErr)
		}
		return signed
	}
	valid := jwt.MapClaims{"username": "alice", "iat": now.Unix(), "exp": now.Add(30 * 24 * time.Hour).Unix()}
	tests := map[string]string{
		"wrong algorithm": sign(jwt.SigningMethodHS384, valid),
		"wrong type":      sign(jwt.SigningMethodHS256, valid, map[string]any{"alg": "HS256", "typ": "at+jwt"}),
		"missing type":    sign(jwt.SigningMethodHS256, valid, map[string]any{"alg": "HS256"}),
		"extra header":    sign(jwt.SigningMethodHS256, valid, map[string]any{"alg": "HS256", "typ": "JWT", "kid": "key-1"}),
		"extra claim":     sign(jwt.SigningMethodHS256, jwt.MapClaims{"username": "alice", "iat": now.Unix(), "exp": now.Add(30 * 24 * time.Hour).Unix(), "sub": "user-1"}),
		"fractional iat":  sign(jwt.SigningMethodHS256, jwt.MapClaims{"username": "alice", "iat": float64(now.Unix()) + 0.5, "exp": now.Add(30 * 24 * time.Hour).Unix()}),
		"wrong lifetime":  sign(jwt.SigningMethodHS256, jwt.MapClaims{"username": "alice", "iat": now.Unix(), "exp": now.Add(29 * 24 * time.Hour).Unix()}),
		"future issued":   sign(jwt.SigningMethodHS256, jwt.MapClaims{"username": "alice", "iat": now.Add(61 * time.Second).Unix(), "exp": now.Add(30*24*time.Hour + 61*time.Second).Unix()}),
		"expired":         sign(jwt.SigningMethodHS256, jwt.MapClaims{"username": "alice", "iat": now.Add(-30*24*time.Hour - 61*time.Second).Unix(), "exp": now.Add(-61 * time.Second).Unix()}),
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Verify(token); !errors.Is(err, ErrInvalidJWT) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestJWTServiceRejectsDuplicateKeysAndTrailingJSONValues(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	service, err := NewJWTService("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }

	signRaw := func(header, payload string) string {
		t.Helper()
		encodedHeader := base64.RawURLEncoding.EncodeToString([]byte(header))
		encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
		unsigned := encodedHeader + "." + encodedPayload
		mac := hmac.New(sha256.New, []byte("test-secret"))
		if _, err := mac.Write([]byte(unsigned)); err != nil {
			t.Fatal(err)
		}
		return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}

	validPayload := `{"username":"alice","iat":1786536000,"exp":1789128000}`
	tests := map[string]string{
		"duplicate header key":  signRaw(`{"alg":"HS256","alg":"HS256","typ":"JWT"}`, validPayload),
		"header trailing value": signRaw(`{"alg":"HS256","typ":"JWT"}{}`, validPayload),
		"duplicate claim key":   signRaw(`{"alg":"HS256","typ":"JWT"}`, `{"username":"alice","username":"mallory","iat":1786536000,"exp":1789128000}`),
		"claims trailing value": signRaw(`{"alg":"HS256","typ":"JWT"}`, validPayload+`{}`),
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Verify(token); !errors.Is(err, ErrInvalidJWT) {
				t.Fatalf("Verify() error = %v", err)
			}
		})
	}
}

func TestJWTSecretUsesEnvironmentThenYAMLFallback(t *testing.T) {
	if got, err := JWTSecret(model.MapEnvironment{model.JWTSecretEnvironment: "environment secret"}, "yaml secret"); err != nil || got != "environment secret" {
		t.Fatalf("JWTSecret(environment) = %q, %v", got, err)
	}
	if got, err := JWTSecret(model.MapEnvironment{}, "yaml secret"); err != nil || got != "yaml secret" {
		t.Fatalf("JWTSecret(fallback) = %q, %v", got, err)
	}
	if _, err := JWTSecret(model.MapEnvironment{model.JWTSecretEnvironment: ""}, "yaml secret"); err == nil {
		t.Fatal("JWTSecret accepted explicitly empty environment value")
	}
	if _, err := JWTSecret(model.MapEnvironment{}, ""); err == nil {
		t.Fatal("JWTSecret accepted missing value")
	}
}

func TestJWTServiceNilReceiverFailsClosed(t *testing.T) {
	var service *JWTService
	if _, err := service.Verify("token"); !errors.Is(err, ErrInvalidJWT) {
		t.Fatalf("nil Verify error = %v", err)
	}
	if _, _, err := service.Issue("alice"); !errors.Is(err, ErrInvalidJWT) {
		t.Fatalf("nil Issue error = %v", err)
	}
}

func TestJWTServiceRequiresSecretAndCanonicalUsername(t *testing.T) {
	if _, err := NewJWTService(""); err == nil {
		t.Fatal("NewJWTService accepted empty secret")
	}
	service, err := NewJWTService("secret")
	if err != nil {
		t.Fatal(err)
	}
	for _, username := range []string{"", " Alice ", "ALICE"} {
		if _, _, err := service.Issue(username); !errors.Is(err, ErrInvalidJWT) {
			t.Fatalf("Issue(%q) error = %v", username, err)
		}
	}
}
