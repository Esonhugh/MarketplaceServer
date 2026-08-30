package management

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/api"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/juanjiTech/jin"
	"github.com/juanjiTech/jin/render"
	"github.com/oklog/ulid/v2"
)

const (
	MaximumJSONBodyBytes int64 = 64 << 10
	BearerChallenge            = `Bearer realm="MarketplaceServer Management"`
	DefaultPage                = 1
	DefaultSize                = 20
	MaximumSize                = 100
)

type Authenticator interface {
	AuthenticateBearer(context.Context, string) (auth.Principal, error)
}

func DecodeJSONBody(c *jin.Context, destination any) error {
	if c.Request.Body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, MaximumJSONBodyBytes))
	var body json.RawMessage
	if err := decoder.Decode(&body); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return errors.New("request body must be a JSON object")
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body contains a trailing JSON value")
		}
		return err
	}
	return nil
}

func AuthenticateRequired(c *jin.Context, authenticator Authenticator) (auth.Principal, bool) {
	header := c.Request.Header.Get("Authorization")
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		c.Writer.Header().Set("WWW-Authenticate", BearerChallenge)
		RenderError(c, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return auth.Principal{}, false
	}
	principal, err := authenticator.AuthenticateBearer(c.Request.Context(), parts[1])
	if err != nil || !principal.IsUser() || principal.CredentialKind() != auth.CredentialJWT {
		c.Writer.Header().Set("WWW-Authenticate", BearerChallenge)
		RenderError(c, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return auth.Principal{}, false
	}
	return principal, true
}

func AuthenticateOptional(c *jin.Context, authenticator Authenticator) (auth.Principal, bool) {
	if strings.TrimSpace(c.Request.Header.Get("Authorization")) == "" {
		return auth.AnonymousPrincipal(), true
	}
	return AuthenticateRequired(c, authenticator)
}

func ParsePagination(c *jin.Context) (int, int, bool) {
	page, err := parsePositive(c.Request.URL.Query().Get("page"), DefaultPage, 0)
	if err != nil {
		RenderError(c, http.StatusUnprocessableEntity, "validation_failed", "page must be a positive integer")
		return 0, 0, false
	}
	size, err := parsePositive(c.Request.URL.Query().Get("size"), DefaultSize, MaximumSize)
	if err != nil {
		RenderError(c, http.StatusUnprocessableEntity, "validation_failed", "size must be between 1 and 100")
		return 0, 0, false
	}
	return page, size, true
}

func parsePositive(value string, defaultValue, maximum int) (int, error) {
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || maximum > 0 && parsed > maximum {
		return 0, errors.New("invalid positive integer")
	}
	return parsed, nil
}

func RenderSuccess(c *jin.Context, status int, data any) {
	c.Render(status, render.JSON{Data: api.Success(data)})
}

func RenderError(c *jin.Context, status int, code, message string) {
	requestID := ulid.Make().String()
	c.Writer.Header().Set("X-Request-Id", requestID)
	c.Render(status, render.JSON{Data: api.NewError(code, message, requestID)})
}

func SetSecretCacheHeaders(c *jin.Context) {
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	c.Writer.Header().Set("Pragma", "no-cache")
}
