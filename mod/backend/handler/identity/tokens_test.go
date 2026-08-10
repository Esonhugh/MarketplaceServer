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

	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/juanjiTech/jin"
)

type tokenServiceFake struct {
	createdInput identitydomain.CreateTokenInput
	listLimit    int
	listCursor   string
	revokedID    string
	createResult identitydomain.CreatedToken
	listResult   identitydomain.TokenPage
	err          error
}

func (fake *tokenServiceFake) Create(_ context.Context, _ auth.Principal, input identitydomain.CreateTokenInput) (identitydomain.CreatedToken, error) {
	fake.createdInput = input
	return fake.createResult, fake.err
}
func (fake *tokenServiceFake) List(_ context.Context, _ auth.Principal, limit int, cursor string) (identitydomain.TokenPage, error) {
	fake.listLimit, fake.listCursor = limit, cursor
	return fake.listResult, fake.err
}
func (fake *tokenServiceFake) Revoke(_ context.Context, _ auth.Principal, tokenID string) error {
	fake.revokedID = tokenID
	return fake.err
}

type basicAuthenticatorFake struct {
	principal auth.Principal
	err       error
}

func (fake *basicAuthenticatorFake) AuthenticateBasic(context.Context, string, string) (auth.Principal, error) {
	return fake.principal, fake.err
}

func handlerPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func TestTokenHandlerCreateReturnsPlaintextOnlyAtCreation(t *testing.T) {
	service := &tokenServiceFake{createResult: identitydomain.CreatedToken{
		TokenMetadata: identitydomain.TokenMetadata{ID: "token-1", Name: "CI", Scopes: []auth.Action{auth.ActionRepositoryRead}, CreatedAt: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)},
		Plaintext:     "mpsk_example",
	}}
	engine := jin.New()
	NewTokenHandler(service, &basicAuthenticatorFake{principal: handlerPrincipal(t)}).Register(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/tokens", strings.NewReader(`{"name":"CI","scopes":["repository.read"]}`))
	request.SetBasicAuth("alice", "password")
	request.Header.Set("X-Request-Id", "request-1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("cache headers = %q/%q", response.Header().Get("Cache-Control"), response.Header().Get("Pragma"))
	}
	if service.createdInput.Name != "CI" || len(service.createdInput.Scopes) != 1 {
		t.Fatalf("create input = %#v", service.createdInput)
	}
	var body struct {
		Data struct {
			ID     string   `json:"id"`
			Token  string   `json:"token"`
			Scopes []string `json:"scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ID != "token-1" || body.Data.Token != "mpsk_example" || len(body.Data.Scopes) != 1 {
		t.Fatalf("response body = %s", response.Body.String())
	}
}

func TestTokenHandlerRejectsTrailingJSONValue(t *testing.T) {
	service := &tokenServiceFake{}
	engine := jin.New()
	NewTokenHandler(service, &basicAuthenticatorFake{principal: handlerPrincipal(t)}).Register(engine)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/tokens", strings.NewReader(`{"name":"CI","scopes":["repository.read"]} null`))
	request.SetBasicAuth("alice", "password")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || service.createdInput.Name != "" {
		t.Fatalf("status/input = %d/%#v; body=%s", response.Code, service.createdInput, response.Body.String())
	}
}

func TestTokenHandlerListAndDeleteUseStableErrors(t *testing.T) {
	service := &tokenServiceFake{listResult: identitydomain.TokenPage{Items: []identitydomain.TokenMetadata{{ID: "token-1", Name: "CI", Scopes: []auth.Action{auth.ActionRepositoryRead}}}, NextCursor: "opaque"}}
	engine := jin.New()
	NewTokenHandler(service, &basicAuthenticatorFake{principal: handlerPrincipal(t)}).Register(engine)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/me/tokens?limit=1&cursor=opaque-input", nil)
	listRequest.SetBasicAuth("alice", "password")
	listResponse := httptest.NewRecorder()
	engine.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || service.listLimit != 1 || service.listCursor != "opaque-input" {
		t.Fatalf("list status/input = %d/%d/%q; body=%s", listResponse.Code, service.listLimit, service.listCursor, listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), "secretHmac") || strings.Contains(listResponse.Body.String(), "mpsk_") {
		t.Fatalf("list leaked secret: %s", listResponse.Body.String())
	}

	service.err = identitydomain.ErrTokenNotFound
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/me/tokens/other", nil)
	deleteRequest.SetBasicAuth("alice", "password")
	deleteResponse := httptest.NewRecorder()
	engine.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNotFound {
		t.Fatalf("delete status = %d; body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	var errorBody struct {
		Code      string `json:"code"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(deleteResponse.Body.Bytes(), &errorBody); err != nil {
		t.Fatal(err)
	}
	if errorBody.Code != "not_found" || errorBody.RequestID == "" {
		t.Fatalf("error response = %s", deleteResponse.Body.String())
	}
}

func TestTokenHandlerRejectsOversizedCursorBeforeService(t *testing.T) {
	service := &tokenServiceFake{}
	engine := jin.New()
	NewTokenHandler(service, &basicAuthenticatorFake{principal: handlerPrincipal(t)}).Register(engine)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me/tokens?cursor="+strings.Repeat("a", identitydomain.MaximumTokenCursorBytes+1), nil)
	request.SetBasicAuth("alice", "password")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || service.listCursor != "" {
		t.Fatalf("status/cursor = %d/%q; body=%s", response.Code, service.listCursor, response.Body.String())
	}
}

func TestTokenHandlerSuccessfulDeleteIsEmpty(t *testing.T) {
	service := &tokenServiceFake{}
	engine := jin.New()
	NewTokenHandler(service, &basicAuthenticatorFake{principal: handlerPrincipal(t)}).Register(engine)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/me/tokens/token-1", nil)
	request.SetBasicAuth("alice", "password")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || service.revokedID != "token-1" {
		t.Fatalf("delete response = %d/%q; revoked=%q", response.Code, response.Body.String(), service.revokedID)
	}
}

func TestTokenHandlerRejectsUnauthenticatedRequests(t *testing.T) {
	engine := jin.New()
	NewTokenHandler(&tokenServiceFake{}, &basicAuthenticatorFake{err: errors.New("invalid")}).Register(engine)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/me/tokens", nil))
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("status/WWW-Authenticate = %d/%q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
}
