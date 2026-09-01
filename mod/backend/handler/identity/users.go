package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	managementhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/management"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"github.com/juanjiTech/jin"
)

type UserLifecycle interface {
	Register(context.Context, identityservice.CreateUserInput) (identityservice.UserProfile, error)
	Create(context.Context, auth.Principal, identityservice.CreateUserInput) (identityservice.UserProfile, error)
	Me(context.Context, auth.Principal) (identityservice.UserProfile, error)
	List(context.Context, auth.Principal, int, int, string) (identityservice.UserPage, error)
	Get(context.Context, auth.Principal, string) (identityservice.UserProfile, error)
	UpdateDisplayName(context.Context, auth.Principal, string, string) (identityservice.UserProfile, error)
	SetEnabled(context.Context, auth.Principal, string, bool) error
	SetSystemAdmin(context.Context, auth.Principal, string, bool) error
}

type JWTIssuer interface {
	Issue(string) (string, time.Time, error)
}
type UserHandler struct {
	service             UserLifecycle
	authenticator       ManagementAuthenticator
	jwt                 JWTIssuer
	registrationEnabled bool
}

func NewUserHandler(service UserLifecycle, authenticator ManagementAuthenticator, jwt JWTIssuer, registrationEnabled bool) *UserHandler {
	return &UserHandler{service: service, authenticator: authenticator, jwt: jwt, registrationEnabled: registrationEnabled}
}
func (h *UserHandler) Register(engine *jin.Engine) {
	engine.GET("/api/v1/auth/capabilities", h.Capabilities)
	if h.registrationEnabled {
		engine.POST("/api/v1/auth/register", h.RegisterAccount)
	}
	engine.GET("/api/v1/me", h.Me)
	engine.GET("/api/v1/admin/users", h.List)
	engine.POST("/api/v1/admin/users", h.Create)
	engine.GET("/api/v1/admin/users/:userId", h.Get)
	engine.PATCH("/api/v1/admin/users/:userId", h.Update)
	// jin does not support a parameter followed by a literal suffix in the same
	// path segment. A terminal wildcard retains the externally specified command
	// paths while this handler parses only the exact supported forms below.
	engine.POST("/api/v1/admin/users/*userCommand", h.UserCommand)
	engine.PUT("/api/v1/admin/users/*userCommand", h.UserCommand)
	engine.DELETE("/api/v1/admin/users/*userCommand", h.UserCommand)
}

type createUserRequest struct {
	Username    string  `json:"username"`
	DisplayName string  `json:"displayName"`
	Email       *string `json:"email"`
	Password    string  `json:"password"`
	Status      string  `json:"status"`
}
type updateUserRequest struct {
	DisplayName string `json:"displayName"`
}

func (h *UserHandler) Capabilities(c *jin.Context) {
	c.Writer.Header().Set("Cache-Control", "no-cache")
	renderSuccess(c, http.StatusOK, struct {
		RegistrationEnabled bool `json:"registrationEnabled"`
	}{h.registrationEnabled})
}
func (h *UserHandler) RegisterAccount(c *jin.Context) {
	var request createUserRequest
	if !decodeUserBody(c, &request) {
		return
	}
	profile, err := h.service.Register(c.Request.Context(), userInput(request))
	if err != nil {
		h.renderError(c, err)
		return
	}
	token, expiresAt, err := h.jwt.Issue(profile.Username)
	if err != nil {
		renderAPIError(c, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusCreated, struct {
		Username  string    `json:"username"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	}{Username: profile.Username, Token: token, ExpiresAt: expiresAt})
}
func (h *UserHandler) Me(c *jin.Context) {
	principal, ok := h.authenticate(c)
	if !ok {
		return
	}
	profile, err := h.service.Me(c.Request.Context(), principal)
	if err != nil {
		h.renderError(c, err)
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusOK, profile)
}
func (h *UserHandler) List(c *jin.Context) {
	principal, ok := h.authenticate(c)
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	status := c.Request.URL.Query().Get("status")
	if status != "" && status != "active" && status != "disabled" {
		renderAPIError(c, http.StatusUnprocessableEntity, "validation_failed", "status is invalid")
		return
	}
	result, err := h.service.List(c.Request.Context(), principal, page, size, status)
	if err != nil {
		h.renderError(c, err)
		return
	}
	if result.Items == nil {
		result.Items = []identityservice.UserProfile{}
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusOK, result)
}
func (h *UserHandler) Create(c *jin.Context) {
	principal, ok := h.authenticate(c)
	if !ok {
		return
	}
	var request createUserRequest
	if !decodeUserBody(c, &request) {
		return
	}
	profile, err := h.service.Create(c.Request.Context(), principal, userInput(request))
	if err != nil {
		h.renderError(c, err)
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusCreated, profile)
}
func (h *UserHandler) Get(c *jin.Context) {
	principal, ok := h.authenticate(c)
	if !ok {
		return
	}
	id, ok := h.userID(c)
	if !ok {
		return
	}
	profile, err := h.service.Get(c.Request.Context(), principal, id)
	if err != nil {
		h.renderError(c, err)
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusOK, profile)
}
func (h *UserHandler) Update(c *jin.Context) {
	principal, ok := h.authenticate(c)
	if !ok {
		return
	}
	id, ok := h.userID(c)
	if !ok {
		return
	}
	var request updateUserRequest
	if !decodeUserBody(c, &request) {
		return
	}
	profile, err := h.service.UpdateDisplayName(c.Request.Context(), principal, id, request.DisplayName)
	if err != nil {
		h.renderError(c, err)
		return
	}
	setSecretCacheHeaders(c)
	renderSuccess(c, http.StatusOK, profile)
}
func (h *UserHandler) UserCommand(c *jin.Context) {
	command := strings.TrimPrefix(c.Params.ByName("userCommand"), "/")
	var id string
	switch {
	case c.Request.Method == http.MethodPost && strings.HasSuffix(command, ":disable"):
		id = strings.TrimSuffix(command, ":disable")
		h.setEnabledID(c, id, false)
	case c.Request.Method == http.MethodPost && strings.HasSuffix(command, ":enable"):
		id = strings.TrimSuffix(command, ":enable")
		h.setEnabledID(c, id, true)
	case c.Request.Method == http.MethodPut && strings.HasSuffix(command, "/system-admin"):
		id = strings.TrimSuffix(command, "/system-admin")
		h.setAdminID(c, id, true)
	case c.Request.Method == http.MethodDelete && strings.HasSuffix(command, "/system-admin"):
		id = strings.TrimSuffix(command, "/system-admin")
		h.setAdminID(c, id, false)
	default:
		renderAPIError(c, http.StatusNotFound, "not_found", "user not found")
	}
}
func (h *UserHandler) Disable(c *jin.Context) { h.setEnabled(c, false) }
func (h *UserHandler) Enable(c *jin.Context)  { h.setEnabled(c, true) }
func (h *UserHandler) setEnabledID(c *jin.Context, id string, enabled bool) {
	if !isCanonicalUserID(id) {
		renderAPIError(c, http.StatusNotFound, "not_found", "user not found")
		return
	}
	h.setEnabledForID(c, id, enabled)
}
func (h *UserHandler) setAdminID(c *jin.Context, id string, enabled bool) {
	if !isCanonicalUserID(id) {
		renderAPIError(c, http.StatusNotFound, "not_found", "user not found")
		return
	}
	h.setAdminForID(c, id, enabled)
}
func (h *UserHandler) setEnabled(c *jin.Context, enabled bool) {
	id, ok := h.userID(c)
	if !ok {
		return
	}
	h.setEnabledForID(c, id, enabled)
}
func (h *UserHandler) setEnabledForID(c *jin.Context, id string, enabled bool) {
	principal, ok := h.authenticate(c)
	if !ok {
		return
	}
	if err := h.service.SetEnabled(c.Request.Context(), principal, id, enabled); err != nil {
		h.renderError(c, err)
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}
func (h *UserHandler) GrantSystemAdmin(c *jin.Context)  { h.setAdmin(c, true) }
func (h *UserHandler) RevokeSystemAdmin(c *jin.Context) { h.setAdmin(c, false) }
func (h *UserHandler) setAdmin(c *jin.Context, enabled bool) {
	id, ok := h.userID(c)
	if !ok {
		return
	}
	h.setAdminForID(c, id, enabled)
}
func (h *UserHandler) setAdminForID(c *jin.Context, id string, enabled bool) {
	principal, ok := h.authenticate(c)
	if !ok {
		return
	}
	if err := h.service.SetSystemAdmin(c.Request.Context(), principal, id, enabled); err != nil {
		h.renderError(c, err)
		return
	}
	c.Writer.WriteHeader(http.StatusNoContent)
}
func (h *UserHandler) authenticate(c *jin.Context) (auth.Principal, bool) {
	return managementhandler.AuthenticateRequired(c, h.authenticator)
}
func (h *UserHandler) userID(c *jin.Context) (string, bool) {
	id := c.Params.ByName("userId")
	if !isCanonicalUserID(id) {
		renderAPIError(c, http.StatusNotFound, "not_found", "user not found")
		return "", false
	}
	return id, true
}
func isCanonicalUserID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
func (h *UserHandler) renderError(c *jin.Context, err error) {
	switch {
	case errors.Is(err, identityservice.ErrInvalidUserInput):
		renderAPIError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid")
	case errors.Is(err, identityservice.ErrUserNotFound):
		renderAPIError(c, http.StatusNotFound, "not_found", "user not found")
	case errors.Is(err, identityservice.ErrUserConflict), errors.Is(err, identityservice.ErrUserOperationDenied):
		renderAPIError(c, http.StatusConflict, "conflict", "operation conflicts with current state")
	case errors.Is(err, identityservice.ErrUserAuthorization):
		renderAPIError(c, http.StatusForbidden, "forbidden", "permission denied")
	default:
		renderAPIError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
func decodeUserBody(c *jin.Context, destination any) bool {
	if err := decodeJSONBody(c, destination); err != nil {
		renderAPIError(c, http.StatusBadRequest, "bad_request", "request body is invalid")
		return false
	}
	return true
}
func userInput(request createUserRequest) identityservice.CreateUserInput {
	return identityservice.CreateUserInput{Username: request.Username, DisplayName: request.DisplayName, Email: request.Email, Password: request.Password, Status: strings.TrimSpace(request.Status)}
}
