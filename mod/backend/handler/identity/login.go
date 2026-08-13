package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	"github.com/juanjiTech/jin"
)

type LoginService interface {
	Login(context.Context, string, string) (identityservice.LoginResult, error)
}

type LoginHandler struct {
	service LoginService
}

func NewLoginHandler(service LoginService) *LoginHandler {
	return &LoginHandler{service: service}
}

func (handler *LoginHandler) Register(engine *jin.Engine) {
	engine.POST("/api/v1/auth/login", handler.Login)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Username  string    `json:"username"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (handler *LoginHandler) Login(c *jin.Context) {
	var request loginRequest
	if err := decodeJSONBody(c, &request); err != nil {
		renderAPIError(c, http.StatusBadRequest, "bad_request", "request body is invalid")
		return
	}
	if strings.TrimSpace(request.Username) == "" || request.Password == "" {
		renderAPIError(c, http.StatusUnauthorized, "unauthenticated", "authentication failed")
		return
	}
	result, err := handler.service.Login(c.Request.Context(), request.Username, request.Password)
	if err != nil {
		if errors.Is(err, identityservice.ErrInvalidCredentials) {
			renderAPIError(c, http.StatusUnauthorized, "unauthenticated", "authentication failed")
			return
		}
		renderAPIError(c, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusOK, loginResponse{Username: result.Username, Token: result.Token, ExpiresAt: result.ExpiresAt})
}
