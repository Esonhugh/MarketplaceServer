package identity

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"github.com/juanjiTech/jin"
)

const bearerChallenge = `Bearer realm="MarketplaceServer Management"`

type ManagementAuthenticator interface {
	AuthenticateBearer(context.Context, string) (auth.Principal, error)
}

type TokenLifecycle interface {
	Create(context.Context, auth.Principal, identityservice.CreateTokenInput) (identityservice.CreatedToken, error)
	List(context.Context, auth.Principal, int, int) (identityservice.TokenPage, error)
	Reveal(context.Context, auth.Principal, string, string) (identityservice.CreatedToken, error)
	Revoke(context.Context, auth.Principal, string) error
}

type TokenHandler struct {
	service       TokenLifecycle
	authenticator ManagementAuthenticator
}

func NewTokenHandler(service TokenLifecycle, authenticator ManagementAuthenticator) *TokenHandler {
	return &TokenHandler{service: service, authenticator: authenticator}
}

func (handler *TokenHandler) Register(engine *jin.Engine) {
	engine.GET("/api/v1/me/tokens", handler.List)
	engine.POST("/api/v1/me/tokens", handler.Create)
	engine.DELETE("/api/v1/me/tokens/:tokenId", handler.Delete)
	engine.POST("/api/v1/me/tokens/:tokenId/reveal", handler.Reveal)
}

type createTokenRequest struct {
	Name      string     `json:"name"`
	Preset    string     `json:"preset"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

type revealTokenRequest struct {
	Password string `json:"password"`
}

func (handler *TokenHandler) Create(c *jin.Context) {
	principal, ok := handler.authenticate(c)
	if !ok {
		return
	}
	var request createTokenRequest
	if err := decodeJSONBody(c, &request); err != nil {
		renderAPIError(c, http.StatusBadRequest, "bad_request", "request body is invalid")
		return
	}
	created, err := handler.service.Create(c.Request.Context(), principal, identityservice.CreateTokenInput{
		Name: request.Name, Preset: request.Preset, ExpiresAt: request.ExpiresAt,
	})
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusCreated, createdTokenResponse(created))
}

func (handler *TokenHandler) List(c *jin.Context) {
	principal, ok := handler.authenticate(c)
	if !ok {
		return
	}
	page, err := parsePage(c.Request.URL.Query().Get("page"))
	if err != nil {
		renderAPIError(c, http.StatusUnprocessableEntity, "validation_failed", "page must be a positive integer")
		return
	}
	size, err := parseSize(c.Request.URL.Query().Get("size"))
	if err != nil {
		renderAPIError(c, http.StatusUnprocessableEntity, "validation_failed", "size must be between 1 and 100")
		return
	}
	result, err := handler.service.List(c.Request.Context(), principal, page, size)
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	if result.Items == nil {
		result.Items = []identityservice.TokenMetadata{}
	}
	renderSuccess(c, http.StatusOK, result)
}

func (handler *TokenHandler) Delete(c *jin.Context) {
	principal, ok := handler.authenticate(c)
	if !ok {
		return
	}
	tokenID := c.Params.ByName("tokenId")
	if !isCanonicalUUID(tokenID) {
		renderAPIError(c, http.StatusNotFound, "not_found", "token not found")
		return
	}
	if err := handler.service.Revoke(c.Request.Context(), principal, tokenID); err != nil {
		handler.renderServiceError(c, err)
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}

func (handler *TokenHandler) Reveal(c *jin.Context) {
	principal, ok := handler.authenticate(c)
	if !ok {
		return
	}
	tokenID := c.Params.ByName("tokenId")
	if !isCanonicalUUID(tokenID) {
		renderAPIError(c, http.StatusNotFound, "not_found", "token not found")
		return
	}
	var request revealTokenRequest
	if err := decodeJSONBody(c, &request); err != nil {
		renderAPIError(c, http.StatusBadRequest, "bad_request", "request body is invalid")
		return
	}
	if request.Password == "" {
		renderAPIError(c, http.StatusUnauthorized, "unauthenticated", "authentication failed")
		return
	}
	revealed, err := handler.service.Reveal(c.Request.Context(), principal, tokenID, request.Password)
	if err != nil {
		if errors.Is(err, identityservice.ErrInvalidCredentials) {
			renderAPIError(c, http.StatusUnauthorized, "unauthenticated", "authentication failed")
			return
		}
		handler.renderServiceError(c, err)
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusOK, createdTokenResponse(revealed))
}

func (handler *TokenHandler) authenticate(c *jin.Context) (auth.Principal, bool) {
	header := c.Request.Header.Get("Authorization")
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		c.Writer.Header().Set("WWW-Authenticate", bearerChallenge)
		renderAPIError(c, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return auth.Principal{}, false
	}
	principal, err := handler.authenticator.AuthenticateBearer(c.Request.Context(), parts[1])
	if err != nil || !principal.IsUser() || principal.CredentialKind() != auth.CredentialJWT {
		c.Writer.Header().Set("WWW-Authenticate", bearerChallenge)
		renderAPIError(c, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return auth.Principal{}, false
	}
	return principal, true
}

func (handler *TokenHandler) renderServiceError(c *jin.Context, err error) {
	switch {
	case errors.Is(err, identityservice.ErrTokenAuthorization):
		renderAPIError(c, http.StatusForbidden, "forbidden", "permission denied")
	case errors.Is(err, identityservice.ErrTokenNotFound):
		renderAPIError(c, http.StatusNotFound, "not_found", "token not found")
	case errors.Is(err, identityservice.ErrInvalidTokenInput):
		renderAPIError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid")
	default:
		renderAPIError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func parsePage(value string) (int, error) {
	if value == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 {
		return 0, errors.New("invalid page")
	}
	return page, nil
}

func parseSize(value string) (int, error) {
	if value == "" {
		return identityservice.DefaultTokenListSize, nil
	}
	size, err := strconv.Atoi(value)
	if err != nil || size < 1 || size > identityservice.MaximumTokenListSize {
		return 0, errors.New("invalid size")
	}
	return size, nil
}

func isCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func createdTokenResponse(created identityservice.CreatedToken) struct {
	identityservice.TokenMetadata
	Token string `json:"token"`
} {
	return struct {
		identityservice.TokenMetadata
		Token string `json:"token"`
	}{TokenMetadata: created.TokenMetadata, Token: created.Token}
}

func setSecretCacheHeaders(c *jin.Context) {
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	c.Writer.Header().Set("Pragma", "no-cache")
}
