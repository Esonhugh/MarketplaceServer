package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	jwt "github.com/golang-jwt/jwt"
)

const (
	JWTLifetime  = 30 * 24 * time.Hour
	JWTClockSkew = 60 * time.Second
)

var ErrInvalidJWT = errors.New("identity: invalid JWT")

type JWTService struct {
	secret []byte
	now    func() time.Time
}

func JWTSecret(environment model.Environment, fallback string) (string, error) {
	if environment == nil {
		environment = model.OSEnvironment{}
	}
	if secret, ok := environment.LookupEnv(model.JWTSecretEnvironment); ok {
		if secret == "" {
			return "", errors.New("identity JWT secret is required")
		}
		return secret, nil
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", errors.New("identity JWT secret is required")
}

func NewJWTService(secret string) (*JWTService, error) {
	if secret == "" {
		return nil, errors.New("identity JWT service requires signing secret")
	}
	return &JWTService{secret: []byte(secret), now: time.Now}, nil
}

func (service *JWTService) Issue(username string) (string, time.Time, error) {
	if service == nil || len(service.secret) == 0 || service.now == nil || username == "" || username != normalizeUsername(username) {
		return "", time.Time{}, ErrInvalidJWT
	}
	now := service.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(JWTLifetime)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": username,
		"iat":      now.Unix(),
		"exp":      expiresAt.Unix(),
	})
	signed, err := token.SignedString(service.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issue JWT: %w", err)
	}
	return signed, expiresAt, nil
}

func (service *JWTService) Verify(encoded string) (string, error) {
	if service == nil || len(service.secret) == 0 || service.now == nil {
		return "", ErrInvalidJWT
	}
	if !validCompactJWTJSON(encoded) {
		return "", ErrInvalidJWT
	}
	parser := &jwt.Parser{ValidMethods: []string{jwt.SigningMethodHS256.Alg()}, SkipClaimsValidation: true}
	token, err := parser.Parse(encoded, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 || len(token.Header) != 2 || token.Header["alg"] != "HS256" || token.Header["typ"] != "JWT" {
			return nil, ErrInvalidJWT
		}
		return service.secret, nil
	})
	if err != nil || token == nil || !token.Valid {
		return "", ErrInvalidJWT
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || len(claims) != 3 {
		return "", ErrInvalidJWT
	}
	username, ok := claims["username"].(string)
	if !ok || username == "" || username != normalizeUsername(username) {
		return "", ErrInvalidJWT
	}
	iat, ok := numericDate(claims["iat"])
	if !ok {
		return "", ErrInvalidJWT
	}
	exp, ok := numericDate(claims["exp"])
	if !ok || exp-iat != int64(JWTLifetime/time.Second) {
		return "", ErrInvalidJWT
	}
	now := service.now().UTC().Unix()
	if iat > now+int64(JWTClockSkew/time.Second) || exp < now-int64(JWTClockSkew/time.Second) {
		return "", ErrInvalidJWT
	}
	return username, nil
}

func validCompactJWTJSON(encoded string) bool {
	parts := strings.Split(encoded, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts[:2] {
		data, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil || !validJSONObject(data) {
			return false
		}
	}
	return true
}

func validJSONObject(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	keys := make(map[string]struct{})
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		name, ok := key.(string)
		if !ok {
			return false
		}
		if _, duplicate := keys[name]; duplicate {
			return false
		}
		keys[name] = struct{}{}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return false
		}
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') {
		return false
	}
	_, err = decoder.Token()
	return errors.Is(err, io.EOF)
}

func numericDate(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number != float64(int64(number)) {
		return 0, false
	}
	return int64(number), true
}
