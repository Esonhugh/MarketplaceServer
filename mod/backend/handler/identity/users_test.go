package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/juanjiTech/jin"
)

type userHandlerFake struct {
	registered    identityservice.CreateUserInput
	registerErr   error
	profile       identityservice.UserProfile
	enabledID     string
	enabled       bool
	setEnabledErr error
}

func (f *userHandlerFake) Register(_ context.Context, in identityservice.CreateUserInput) (identityservice.UserProfile, error) {
	f.registered = in
	return f.profile, f.registerErr
}
func (f *userHandlerFake) Create(context.Context, auth.Principal, identityservice.CreateUserInput) (identityservice.UserProfile, error) {
	return f.profile, nil
}
func (f *userHandlerFake) Me(context.Context, auth.Principal) (identityservice.UserProfile, error) {
	return f.profile, nil
}
func (f *userHandlerFake) List(context.Context, auth.Principal, int, int, string) (identityservice.UserPage, error) {
	return identityservice.UserPage{}, nil
}
func (f *userHandlerFake) Get(context.Context, auth.Principal, string) (identityservice.UserProfile, error) {
	return f.profile, nil
}
func (f *userHandlerFake) UpdateDisplayName(context.Context, auth.Principal, string, string) (identityservice.UserProfile, error) {
	return f.profile, nil
}
func (f *userHandlerFake) SetEnabled(_ context.Context, _ auth.Principal, id string, enabled bool) error {
	f.enabledID, f.enabled = id, enabled
	return f.setEnabledErr
}
func (f *userHandlerFake) SetSystemAdmin(context.Context, auth.Principal, string, bool) error {
	return nil
}

type issuerFake struct {
	token string
	err   error
}

func (f issuerFake) Issue(string) (string, time.Time, error) { return f.token, time.Now().UTC(), f.err }
func userEngine(t *testing.T, service *userHandlerFake, enabled bool) *jin.Engine {
	t.Helper()
	e := jin.New()
	NewUserHandler(service, &managementAuthenticatorFake{principal: handlerPrincipal(t)}, issuerFake{token: "jwt"}, enabled).Register(e)
	return e
}
func TestCapabilitiesAndRegistrationGate(t *testing.T) {
	disabled := httptest.NewRecorder()
	userEngine(t, &userHandlerFake{}, false).ServeHTTP(disabled, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{}`)))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled=%d", disabled.Code)
	}
	cap := httptest.NewRecorder()
	userEngine(t, &userHandlerFake{}, false).ServeHTTP(cap, httptest.NewRequest(http.MethodGet, "/api/v1/auth/capabilities", nil))
	if cap.Code != http.StatusOK || !strings.Contains(cap.Body.String(), `"registrationEnabled":false`) {
		t.Fatalf("capabilities=%d/%s", cap.Code, cap.Body.String())
	}
}
func TestRegistrationReturnsJWTAndDoesNotEchoPassword(t *testing.T) {
	service := &userHandlerFake{profile: identityservice.UserProfile{ID: "id", Username: "alice", DisplayName: "Alice", Status: "active"}}
	response := httptest.NewRecorder()
	userEngine(t, service, true).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{"username":"alice","displayName":"Alice","password":"long enough password"}`)))
	if response.Code != http.StatusCreated || service.registered.Password != "long enough password" || !strings.Contains(response.Body.String(), `"username":"alice"`) || !strings.Contains(response.Body.String(), `"token":"jwt"`) || strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), `"displayName"`) {
		t.Fatalf("register=%d/%#v/%s", response.Code, service.registered, response.Body.String())
	}
	assertSecretCacheHeaders(t, response)
}
func TestAdminUserCommandRejectsMalformedIDAndMapsConflict(t *testing.T) {
	service := &userHandlerFake{setEnabledErr: identityservice.ErrUserOperationDenied}
	e := userEngine(t, service, true)
	bad := httptest.NewRecorder()
	e.ServeHTTP(bad, bearerRequest(http.MethodPost, "/api/v1/admin/users/not-a-uuid:disable", ""))
	if bad.Code != http.StatusNotFound {
		t.Fatalf("bad=%d", bad.Code)
	}
	conflict := httptest.NewRecorder()
	e.ServeHTTP(conflict, bearerRequest(http.MethodPost, "/api/v1/admin/users/11111111-1111-1111-1111-111111111111:disable", ""))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict=%d/%s", conflict.Code, conflict.Body.String())
	}
}
func TestMeRequiresBearer(t *testing.T) {
	response := httptest.NewRecorder()
	userEngine(t, &userHandlerFake{}, true).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != bearerChallenge {
		t.Fatalf("me=%d/%q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}
func TestRegistrationMapsConflict(t *testing.T) {
	service := &userHandlerFake{registerErr: identityservice.ErrUserConflict}
	response := httptest.NewRecorder()
	userEngine(t, service, true).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(`{"username":"alice"}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("conflict=%d", response.Code)
	}
	_ = json.Valid
	_ = errors.New
}
