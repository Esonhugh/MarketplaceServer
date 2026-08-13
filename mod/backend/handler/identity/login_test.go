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
	"github.com/juanjiTech/jin"
)

type loginServiceFake struct {
	username string
	password string
	result   identityservice.LoginResult
	err      error
}

func (fake *loginServiceFake) Login(_ context.Context, username, password string) (identityservice.LoginResult, error) {
	fake.username = username
	fake.password = password
	return fake.result, fake.err
}

func TestLoginHandlerRejectsMalformedBodyAt400AndParsedAuthFailuresAt401(t *testing.T) {
	for name, test := range map[string]struct {
		body       string
		serviceErr error
		wantStatus int
	}{
		"missing body":        {body: "", wantStatus: http.StatusBadRequest},
		"null body":           {body: " \nnull\t", wantStatus: http.StatusBadRequest},
		"malformed body":      {body: `{"username":`, wantStatus: http.StatusBadRequest},
		"trailing JSON":       {body: `{"username":"alice","password":"correct"} {}`, wantStatus: http.StatusBadRequest},
		"missing username":    {body: `{"password":"correct"}`, wantStatus: http.StatusUnauthorized},
		"missing password":    {body: `{"username":"alice"}`, wantStatus: http.StatusUnauthorized},
		"invalid credentials": {body: `{"username":"alice","password":"wrong"}`, serviceErr: identityservice.ErrInvalidCredentials, wantStatus: http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			service := &loginServiceFake{err: test.serviceErr}
			engine := jin.New()
			NewLoginHandler(service).Register(engine)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(test.body)))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertErrorRequestID(t, response, "")
		})
	}
}

func TestLoginHandlerMapsInternalFailureTo500AndWrongPasswordTo401(t *testing.T) {
	for name, test := range map[string]struct {
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		"persistence failure": {serviceErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
		"wrong password":      {serviceErr: identityservice.ErrInvalidCredentials, wantStatus: http.StatusUnauthorized, wantCode: "unauthenticated"},
	} {
		t.Run(name, func(t *testing.T) {
			service := &loginServiceFake{err: test.serviceErr}
			engine := jin.New()
			NewLoginHandler(service).Register(engine)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"alice","password":"password"}`)))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("response = %d/%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestLoginHandlerPreservesPropertyNullAsParsedAuthenticationFailure(t *testing.T) {
	service := &loginServiceFake{}
	engine := jin.New()
	NewLoginHandler(service).Register(engine)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":null,"password":"password"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestLoginHandlerRejectsOversizedBodyAt400(t *testing.T) {
	service := &loginServiceFake{}
	engine := jin.New()
	NewLoginHandler(service).Register(engine)
	body := `{"username":"alice","password":"` + strings.Repeat("a", (64<<10)+1) + `"}`
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestLoginHandlerIssuesJWT(t *testing.T) {
	expiresAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	service := &loginServiceFake{result: identityservice.LoginResult{
		Username:  "alice",
		Token:     "jwt-token",
		ExpiresAt: expiresAt,
	}}
	engine := jin.New()
	NewLoginHandler(service).Register(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"alice","password":"correct","futureField":true}`))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if service.username != "alice" || service.password != "correct" {
		t.Fatalf("Login() input = %q/%q", service.username, service.password)
	}
	if response.Header().Get("X-Request-Id") != "" {
		t.Fatalf("success X-Request-Id = %q", response.Header().Get("X-Request-Id"))
	}
	assertSecretCacheHeaders(t, response)
	var body struct {
		Data struct {
			Username  string    `json:"username"`
			Token     string    `json:"token"`
			ExpiresAt time.Time `json:"expiresAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Username != "alice" || body.Data.Token != "jwt-token" || !body.Data.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("response body = %s", response.Body.String())
	}
}
