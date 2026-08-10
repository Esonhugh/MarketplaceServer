package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	"github.com/Esonhugh/MarketplaceServer/pkg/api"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"github.com/juanjiTech/jin"
	"github.com/juanjiTech/jin/render"
)

type TokenLifecycle interface {
	Create(context.Context, auth.Principal, identitydomain.CreateTokenInput) (identitydomain.CreatedToken, error)
	List(context.Context, auth.Principal, int, string) (identitydomain.TokenPage, error)
	Revoke(context.Context, auth.Principal, string) error
}

type TokenHandler struct {
	service       TokenLifecycle
	authenticator auth.BasicAuthenticator
}

func NewTokenHandler(service TokenLifecycle, authenticator auth.BasicAuthenticator) *TokenHandler {
	return &TokenHandler{service: service, authenticator: authenticator}
}

func (handler *TokenHandler) Register(engine *jin.Engine) {
	engine.GET("/api/v1/me/tokens", handler.List)
	engine.POST("/api/v1/me/tokens", handler.Create)
	engine.DELETE("/api/v1/me/tokens/:tokenId", handler.Delete)
}

type createTokenRequest struct {
	Name      string        `json:"name"`
	Scopes    []auth.Action `json:"scopes"`
	ExpiresAt *time.Time    `json:"expiresAt"`
}

type createTokenResponse struct {
	identitydomain.TokenMetadata
	Token string `json:"token"`
}

func (handler *TokenHandler) Create(c *jin.Context) {
	principal, requestID, ok := handler.authenticate(c)
	if !ok {
		return
	}
	var request createTokenRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		handler.renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request body is invalid", requestID)
		return
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		handler.renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request body is invalid", requestID)
		return
	}
	created, err := handler.service.Create(c.Request.Context(), principal, identitydomain.CreateTokenInput{
		Name: request.Name, Scopes: request.Scopes, ExpiresAt: request.ExpiresAt,
	})
	if err != nil {
		handler.renderServiceError(c, err, requestID)
		return
	}
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	c.Writer.Header().Set("Pragma", "no-cache")
	c.Render(http.StatusCreated, render.JSON{Data: api.Success(createTokenResponse{TokenMetadata: created.TokenMetadata, Token: created.Plaintext})})
}

func (handler *TokenHandler) List(c *jin.Context) {
	principal, requestID, ok := handler.authenticate(c)
	if !ok {
		return
	}
	limit, err := parseLimit(c.Request.URL.Query().Get("limit"))
	if err != nil {
		handler.renderError(c, http.StatusUnprocessableEntity, "validation_failed", "limit must be a positive integer", requestID)
		return
	}
	cursor := c.Request.URL.Query().Get("cursor")
	if len(cursor) > identitydomain.MaximumTokenCursorBytes {
		handler.renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid", requestID)
		return
	}
	page, err := handler.service.List(c.Request.Context(), principal, limit, cursor)
	if err != nil {
		handler.renderServiceError(c, err, requestID)
		return
	}
	c.Render(http.StatusOK, render.JSON{Data: api.CursorList(page.Items, page.NextCursor)})
}

func (handler *TokenHandler) Delete(c *jin.Context) {
	principal, requestID, ok := handler.authenticate(c)
	if !ok {
		return
	}
	if err := handler.service.Revoke(c.Request.Context(), principal, c.Params.ByName("tokenId")); err != nil {
		handler.renderServiceError(c, err, requestID)
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}

func (handler *TokenHandler) authenticate(c *jin.Context) (auth.Principal, string, bool) {
	requestID := requestID(c)
	username, credential, ok := c.Request.BasicAuth()
	if !ok || username == "" || credential == "" {
		c.Writer.Header().Set("WWW-Authenticate", `Basic realm="MarketplaceServer"`)
		handler.renderError(c, http.StatusUnauthorized, "unauthenticated", "authentication is required", requestID)
		return auth.Principal{}, requestID, false
	}
	principal, err := handler.authenticator.AuthenticateBasic(c.Request.Context(), username, credential)
	if err != nil {
		c.Writer.Header().Set("WWW-Authenticate", `Basic realm="MarketplaceServer"`)
		handler.renderError(c, http.StatusUnauthorized, "unauthenticated", "authentication is required", requestID)
		return auth.Principal{}, requestID, false
	}
	return principal, requestID, true
}

func (handler *TokenHandler) renderServiceError(c *jin.Context, err error, requestID string) {
	switch {
	case errors.Is(err, identitydomain.ErrTokenAuthorization):
		handler.renderError(c, http.StatusForbidden, "forbidden", "permission denied", requestID)
	case errors.Is(err, identitydomain.ErrTokenNotFound):
		handler.renderError(c, http.StatusNotFound, "not_found", "token not found", requestID)
	case errors.Is(err, identitydomain.ErrInvalidTokenInput), errors.Is(err, identitydomain.ErrTokenScopeNotAllowed), errors.Is(err, identitydomain.ErrInvalidTokenCursor):
		handler.renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid", requestID)
	default:
		handler.renderError(c, http.StatusInternalServerError, "internal_error", "internal server error", requestID)
	}
}

func (handler *TokenHandler) renderError(c *jin.Context, status int, code, message, requestID string) {
	c.Writer.Header().Set("X-Request-Id", requestID)
	c.Render(status, render.JSON{Data: api.NewError(code, message, requestID)})
}

func requestID(c *jin.Context) string {
	if candidate := strings.TrimSpace(c.Request.Header.Get("X-Request-Id")); candidate != "" && len(candidate) <= 128 {
		return candidate
	}
	return uuid.NewString()
}

func parseLimit(value string) (int, error) {
	if value == "" {
		return identitydomain.DefaultTokenListLimit, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, errors.New("invalid limit")
	}
	if limit > identitydomain.MaximumTokenListLimit {
		return identitydomain.MaximumTokenListLimit, nil
	}
	return limit, nil
}
