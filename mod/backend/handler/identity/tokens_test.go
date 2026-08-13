package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/juanjiTech/jin"
)

type tokenServiceFake struct {
	principal    auth.Principal
	createdInput identityservice.CreateTokenInput
	listPage     int
	listSize     int
	revealedID   string
	password     string
	revokedID    string
	createResult identityservice.CreatedToken
	listResult   identityservice.TokenPage
	revealResult identityservice.CreatedToken
	createErr    error
	listErr      error
	revealErr    error
	revokeErr    error
}

func (fake *tokenServiceFake) Create(_ context.Context, principal auth.Principal, input identityservice.CreateTokenInput) (identityservice.CreatedToken, error) {
	fake.principal, fake.createdInput = principal, input
	return fake.createResult, fake.createErr
}

func (fake *tokenServiceFake) List(_ context.Context, principal auth.Principal, page, size int) (identityservice.TokenPage, error) {
	fake.principal, fake.listPage, fake.listSize = principal, page, size
	return fake.listResult, fake.listErr
}

func (fake *tokenServiceFake) Reveal(_ context.Context, principal auth.Principal, tokenID, password string) (identityservice.CreatedToken, error) {
	fake.principal, fake.revealedID, fake.password = principal, tokenID, password
	return fake.revealResult, fake.revealErr
}

func (fake *tokenServiceFake) Revoke(_ context.Context, principal auth.Principal, tokenID string) error {
	fake.principal, fake.revokedID = principal, tokenID
	return fake.revokeErr
}

type managementAuthenticatorFake struct {
	encoded   string
	principal auth.Principal
	err       error
	calls     int
}

func (fake *managementAuthenticatorFake) AuthenticateBearer(_ context.Context, encoded string) (auth.Principal, error) {
	fake.calls++
	fake.encoded = encoded
	return fake.principal, fake.err
}

func handlerPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialJWT, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func tokenEngine(t *testing.T, service *tokenServiceFake, authenticator *managementAuthenticatorFake) *jin.Engine {
	t.Helper()
	if !authenticator.principal.IsUser() && authenticator.err == nil {
		authenticator.principal = handlerPrincipal(t)
	}
	engine := jin.New()
	NewTokenHandler(service, authenticator).Register(engine)
	return engine
}

func bearerRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer jwt-token")
	return request
}

func TestTokenHandlerCreateReturnsSecretWithoutPersistenceFields(t *testing.T) {
	createdAt := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	service := &tokenServiceFake{createResult: identityservice.CreatedToken{
		TokenMetadata: identityservice.TokenMetadata{
			ID: "11111111-1111-1111-1111-111111111111", Name: "CI", Preset: "git-clone",
			Status: identityservice.TokenStatusActive, CreatedAt: createdAt,
		},
		Token: "mpsk_example",
	}}
	authenticator := &managementAuthenticatorFake{}
	engine := tokenEngine(t, service, authenticator)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens", `{"name":"CI","preset":"git-clone","expiresAt":null,"futureField":true}`))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertSecretCacheHeaders(t, response)
	if service.createdInput.Name != "CI" || service.createdInput.Preset != "git-clone" || service.createdInput.ExpiresAt != nil {
		t.Fatalf("Create() input = %#v", service.createdInput)
	}
	if authenticator.encoded != "jwt-token" || service.principal.CredentialKind() != auth.CredentialJWT {
		t.Fatalf("authentication = %q/%q", authenticator.encoded, service.principal.CredentialKind())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"secretPlaintext", "secretHmac", "userId", "updatedAt"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
	var decoded struct {
		Data struct {
			ID         string     `json:"id"`
			Token      string     `json:"token"`
			ExpiresAt  *time.Time `json:"expiresAt"`
			LastUsedAt *time.Time `json:"lastUsedAt"`
			RevokedAt  *time.Time `json:"revokedAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Data.ID == "" || decoded.Data.Token != "mpsk_example" || decoded.Data.ExpiresAt != nil || decoded.Data.LastUsedAt != nil || decoded.Data.RevokedAt != nil {
		t.Fatalf("response = %s", body)
	}
}

func TestTokenHandlerCreatePreservesNullableExpiresAtProperty(t *testing.T) {
	service := &tokenServiceFake{createResult: identityservice.CreatedToken{
		TokenMetadata: identityservice.TokenMetadata{ID: "11111111-1111-1111-1111-111111111111", Name: "CI", Preset: "git-clone", Status: identityservice.TokenStatusActive, CreatedAt: time.Now().UTC()},
		Token:         "mpsk_secret",
	}}
	response := httptest.NewRecorder()
	tokenEngine(t, service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens", `{"name":"CI","preset":"git-clone","expiresAt":null}`))
	if response.Code != http.StatusCreated || service.createdInput.ExpiresAt != nil {
		t.Fatalf("status/input = %d/%#v; body=%s", response.Code, service.createdInput, response.Body.String())
	}
}

func TestTokenHandlerCreateMapsSemanticValidationTo422(t *testing.T) {
	service := &tokenServiceFake{createErr: identityservice.ErrInvalidTokenInput}
	response := httptest.NewRecorder()
	tokenEngine(t, service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens", `{"name":"","preset":"unknown"}`))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertErrorRequestID(t, response, "caller-id")
}

func TestTokenHandlerListDefaultsAndReturnsExactEnvelope(t *testing.T) {
	service := &tokenServiceFake{listResult: identityservice.TokenPage{
		Items: []identityservice.TokenMetadata{{
			ID: "11111111-1111-1111-1111-111111111111", Name: "CI", Preset: "git-clone",
			Status: identityservice.TokenStatusActive, CreatedAt: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		}},
		Page: 1, Size: 20, Total: 41,
	}}
	response := httptest.NewRecorder()
	tokenEngine(t, service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(http.MethodGet, "/api/v1/me/tokens", ""))
	if response.Code != http.StatusOK || service.listPage != 1 || service.listSize != 20 {
		t.Fatalf("status/page/size = %d/%d/%d; body=%s", response.Code, service.listPage, service.listSize, response.Body.String())
	}
	var body struct {
		Data identityservice.TokenPage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Page != 1 || body.Data.Size != 20 || body.Data.Total != 41 || len(body.Data.Items) != 1 {
		t.Fatalf("response = %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "token") || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("list leaked secret: %s", response.Body.String())
	}
}

func TestTokenHandlerListUsesRequestedPageAndRejectsInvalidSize(t *testing.T) {
	service := &tokenServiceFake{listResult: identityservice.TokenPage{Items: []identityservice.TokenMetadata{}, Page: 3, Size: 100}}
	engine := tokenEngine(t, service, &managementAuthenticatorFake{})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, bearerRequest(http.MethodGet, "/api/v1/me/tokens?page=3&size=100", ""))
	if response.Code != http.StatusOK || service.listPage != 3 || service.listSize != 100 {
		t.Fatalf("valid page = %d/%d/%d", response.Code, service.listPage, service.listSize)
	}

	invalid := httptest.NewRecorder()
	engine.ServeHTTP(invalid, bearerRequest(http.MethodGet, "/api/v1/me/tokens?size=101", ""))
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid size status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
}

func TestTokenHandlerRevealReturnsSecretAndWrongPasswordIs401(t *testing.T) {
	service := &tokenServiceFake{revealResult: identityservice.CreatedToken{
		TokenMetadata: identityservice.TokenMetadata{ID: "11111111-1111-1111-1111-111111111111", Name: "CI", Preset: "git-clone", Status: identityservice.TokenStatusRevoked, CreatedAt: time.Now().UTC()},
		Token:         "mpsk_secret",
	}}
	engine := tokenEngine(t, service, &managementAuthenticatorFake{})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111/reveal", `{"password":"correct","ignored":true}`))
	if response.Code != http.StatusOK || service.revealedID == "" || service.password != "correct" || !strings.Contains(response.Body.String(), `"token":"mpsk_secret"`) {
		t.Fatalf("reveal = %d/%q/%q/%s", response.Code, service.revealedID, service.password, response.Body.String())
	}
	assertSecretCacheHeaders(t, response)

	service.revealErr = identityservice.ErrInvalidCredentials
	wrong := httptest.NewRecorder()
	engine.ServeHTTP(wrong, bearerRequest(http.MethodPost, "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111/reveal", `{"password":"wrong"}`))
	if wrong.Code != http.StatusUnauthorized || wrong.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("wrong password = %d/%q/%s", wrong.Code, wrong.Header().Get("WWW-Authenticate"), wrong.Body.String())
	}
}

func TestTokenHandlerRevealPreservesPropertyNullAsAuthenticationFailure(t *testing.T) {
	service := &tokenServiceFake{}
	response := httptest.NewRecorder()
	tokenEngine(t, service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111/reveal", `{"password":null}`))
	if response.Code != http.StatusUnauthorized || service.revealedID != "" {
		t.Fatalf("status/id = %d/%q; body=%s", response.Code, service.revealedID, response.Body.String())
	}
}

func TestTokenHandlerInternalServiceFailuresReturn500(t *testing.T) {
	for name, test := range map[string]struct {
		service *tokenServiceFake
		method  string
		target  string
		body    string
	}{
		"create persistence failure": {
			service: &tokenServiceFake{createErr: errors.New("database unavailable")},
			method:  http.MethodPost,
			target:  "/api/v1/me/tokens",
			body:    `{"name":"CI","preset":"git-clone"}`,
		},
		"list persistence failure": {
			service: &tokenServiceFake{listErr: errors.New("database unavailable")},
			method:  http.MethodGet,
			target:  "/api/v1/me/tokens",
		},
		"reveal internal auth failure": {
			service: &tokenServiceFake{revealErr: errors.New("password lookup unavailable")},
			method:  http.MethodPost,
			target:  "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111/reveal",
			body:    `{"password":"correct"}`,
		},
		"revoke persistence failure": {
			service: &tokenServiceFake{revokeErr: errors.New("database unavailable")},
			method:  http.MethodDelete,
			target:  "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111",
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			tokenEngine(t, test.service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(test.method, test.target, test.body))
			if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"internal_error"`) {
				t.Fatalf("response = %d/%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTokenHandlerRevealRejectsMalformedBodiesAt400(t *testing.T) {
	for name, body := range map[string]string{
		"missing":   "",
		"null":      `null`,
		"malformed": `{"password":`,
		"trailing":  `{"password":"correct"} {}`,
		"oversized": `{"password":"` + strings.Repeat("a", (64<<10)+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			service := &tokenServiceFake{}
			response := httptest.NewRecorder()
			tokenEngine(t, service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111/reveal", body))
			if response.Code != http.StatusBadRequest || service.revealedID != "" {
				t.Fatalf("status/id = %d/%q; body=%s", response.Code, service.revealedID, response.Body.String())
			}
		})
	}
}

func TestTokenHandlerNonOwnerAndMissingTokensAreSame404(t *testing.T) {
	for _, operation := range []string{"revoke", "reveal"} {
		t.Run(operation, func(t *testing.T) {
			service := &tokenServiceFake{revokeErr: identityservice.ErrTokenNotFound, revealErr: identityservice.ErrTokenNotFound}
			engine := tokenEngine(t, service, &managementAuthenticatorFake{})
			response := httptest.NewRecorder()
			if operation == "revoke" {
				engine.ServeHTTP(response, bearerRequest(http.MethodDelete, "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111", ""))
			} else {
				engine.ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111/reveal", `{"password":"correct"}`))
			}
			if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
				t.Fatalf("response = %d/%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTokenHandlerRejectsNonCanonicalTokenIDAsHidden404(t *testing.T) {
	service := &tokenServiceFake{}
	engine := tokenEngine(t, service, &managementAuthenticatorFake{})
	for _, target := range []string{
		"/api/v1/me/tokens/not-a-uuid",
		"/api/v1/me/tokens/AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA",
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, bearerRequest(http.MethodDelete, target, ""))
		if response.Code != http.StatusNotFound || service.revokedID != "" {
			t.Fatalf("target/status/revoked = %q/%d/%q; body=%s", target, response.Code, service.revokedID, response.Body.String())
		}
	}
}

func TestTokenHandlerSuccessfulDeleteIsBodyless(t *testing.T) {
	service := &tokenServiceFake{}
	response := httptest.NewRecorder()
	tokenEngine(t, service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(http.MethodDelete, "/api/v1/me/tokens/11111111-1111-1111-1111-111111111111", ""))
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 || service.revokedID == "" || response.Header().Get("X-Request-Id") != "" {
		t.Fatalf("delete = %d/%q/%q/%q", response.Code, response.Body.String(), service.revokedID, response.Header().Get("X-Request-Id"))
	}
}

func TestTokenHandlerRejectsBasicAndPATWithoutBearerFallback(t *testing.T) {
	for _, authorization := range []string{"Basic YWxpY2U6cGFzc3dvcmQ=", "Basic YWxpY2U6bXBza19wYXQ=", ""} {
		t.Run(authorization, func(t *testing.T) {
			authenticator := &managementAuthenticatorFake{}
			engine := tokenEngine(t, &tokenServiceFake{}, authenticator)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/me/tokens", nil)
			request.Header.Set("Authorization", authorization)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != bearerChallenge || authenticator.calls != 0 {
				t.Fatalf("response = %d/%q, calls=%d", response.Code, response.Header().Get("WWW-Authenticate"), authenticator.calls)
			}
		})
	}
}

func TestTokenHandlerRejectsMalformedBodiesAt400(t *testing.T) {
	oversized := `{"name":"` + strings.Repeat("a", (64<<10)+1) + `","preset":"git-clone"}`
	for name, body := range map[string]string{
		"missing":   "",
		"null":      `null`,
		"malformed": `{"name":`,
		"trailing":  `{"name":"CI","preset":"git-clone"} {}`,
		"oversized": oversized,
	} {
		t.Run(name, func(t *testing.T) {
			service := &tokenServiceFake{}
			response := httptest.NewRecorder()
			tokenEngine(t, service, &managementAuthenticatorFake{}).ServeHTTP(response, bearerRequest(http.MethodPost, "/api/v1/me/tokens", body))
			if response.Code != http.StatusBadRequest || service.createdInput.Name != "" {
				t.Fatalf("status/input = %d/%#v; body=%s", response.Code, service.createdInput, response.Body.String())
			}
		})
	}
}

func TestTokenHandlerInvalidBearerReturnsFreshULIDAndIgnoresCallerID(t *testing.T) {
	authenticator := &managementAuthenticatorFake{err: errors.New("invalid JWT")}
	request := bearerRequest(http.MethodGet, "/api/v1/me/tokens", "")
	request.Header.Set("X-Request-Id", "caller-id")
	response := httptest.NewRecorder()
	tokenEngine(t, &tokenServiceFake{}, authenticator).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != bearerChallenge {
		t.Fatalf("response = %d/%q", response.Code, response.Header().Get("WWW-Authenticate"))
	}
	assertErrorRequestID(t, response, "caller-id")
}

func assertSecretCacheHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("cache headers = %q/%q", response.Header().Get("Cache-Control"), response.Header().Get("Pragma"))
	}
}

func assertErrorRequestID(t *testing.T, response *httptest.ResponseRecorder, callerID string) {
	t.Helper()
	requestID := response.Header().Get("X-Request-Id")
	if requestID == "" || requestID == callerID || !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(requestID) {
		t.Fatalf("X-Request-Id = %q", requestID)
	}
	var body struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RequestID != requestID {
		t.Fatalf("body/header request IDs = %q/%q", body.RequestID, requestID)
	}
}
