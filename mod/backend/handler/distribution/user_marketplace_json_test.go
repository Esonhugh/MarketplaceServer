package distribution

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/juanjiTech/jin"
)

func TestUserMarketplaceJSONAuthenticationOriginAndHeaders(t *testing.T) {
	principal := handlerMarketplacePrincipal(t, "user-1", "alice")
	authenticator := &subscriptionPATAuthenticatorStub{principal: principal}
	engine := jin.New()
	service := handlerMarketplaceService()
	engine.GET("/distribution/users/:username/marketplace.json", NewUserMarketplaceJSONHandler(authenticator, service).Get)

	request := httptest.NewRequest(http.MethodGet, "http://untrusted.example/distribution/users/alice/marketplace.json", nil)
	request.Host = "market.example:8443"
	request.TLS = &tls.ConnectionState{}
	request.SetBasicAuth("alice", "secret")
	request.Header.Set("X-Forwarded-Host", "evil.example")
	request.Header.Set("X-Forwarded-Proto", "http")
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if want := `https://market.example:8443/git/alice/repo.git`; !strings.Contains(recorder.Body.String(), want) {
		t.Fatalf("source origin missing %q in %s", want, recorder.Body.String())
	}
	for key, want := range map[string]string{"Content-Type": "application/json", "Cache-Control": "private, no-store", "Pragma": "no-cache", "Vary": "Authorization, Host", "X-Content-Type-Options": "nosniff"} {
		if got := recorder.Header().Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if recorder.Header().Get("ETag") == "" {
		t.Error("ETag is missing")
	}

	for _, username := range []string{"mallory", "ALICE"} {
		request := httptest.NewRequest(http.MethodGet, "/distribution/users/"+username+"/marketplace.json", nil)
		request.SetBasicAuth("alice", "secret")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("route username %q status = %d, want 404", username, recorder.Code)
		}
	}
	if authenticator.calls != 1 {
		t.Fatalf("auth calls = %d; mismatch must be rejected before authentication", authenticator.calls)
	}
}

func TestUserMarketplaceJSONUsesSubscriptionPATAndRejectsPasswordOrJWT(t *testing.T) {
	principal := handlerMarketplacePrincipal(t, "user-1", "alice")
	authenticator := &subscriptionPATAuthenticatorStub{principal: principal}
	engine := jin.New()
	NewUserMarketplaceJSONHandler(authenticator, handlerMarketplaceService()).Register(engine)

	for _, plaintext := range []string{"sub-read-pat", "git-clone-pat", "git-write-pat"} {
		request := httptest.NewRequest(http.MethodGet, "/distribution/users/alice/marketplace.json", nil)
		request.Host = "market.example"
		request.SetBasicAuth("alice", plaintext)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("PAT %q status = %d, want 200", plaintext, response.Code)
		}
	}
	if authenticator.calls != 3 {
		t.Fatalf("authenticator calls = %d, want 3", authenticator.calls)
	}

	for _, test := range []struct {
		name          string
		authorization string
	}{
		{name: "account password", authorization: "Basic YWxpY2U6YWNjb3VudC1wYXNzd29yZA=="},
		{name: "JWT bearer", authorization: "Bearer jwt-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			authenticator.err = errors.New("invalid credentials")
			request := httptest.NewRequest(http.MethodGet, "/distribution/users/alice/marketplace.json", nil)
			request.Host = "market.example"
			request.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", response.Code)
			}
		})
	}
}

func TestUserMarketplaceJSONRejectsInvalidHost(t *testing.T) {
	principal := handlerMarketplacePrincipal(t, "user-1", "alice")
	authenticator := &subscriptionPATAuthenticatorStub{principal: principal}
	engine := jin.New()
	NewUserMarketplaceJSONHandler(authenticator, handlerMarketplaceService()).Register(engine)

	for _, host := range []string{"evil.example@market.example", "market.example:0", "market.example:65536", "market.example/path", "[::1]"} {
		request := httptest.NewRequest(http.MethodGet, "/distribution/users/alice/marketplace.json", nil)
		request.Host = host
		request.SetBasicAuth("alice", "secret")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("host %q status = %d, want 404", host, response.Code)
		}
	}
}

func TestUserMarketplaceJSONReturnsGenericNotFoundAndRechecksBefore304(t *testing.T) {
	principal := handlerMarketplacePrincipal(t, "user-1", "alice")
	authenticator := &subscriptionPATAuthenticatorStub{principal: principal}
	authorizer := &countingAuthorizer{}
	service := distributiondomain.NewUserMarketplaceService(&handlerRepository{}, authorizer)
	engine := jin.New()
	engine.GET("/distribution/users/:username/marketplace.json", NewUserMarketplaceJSONHandler(authenticator, service).Get)

	firstRequest := httptest.NewRequest(http.MethodGet, "/distribution/users/alice/marketplace.json", nil)
	firstRequest.Host = "market.example"
	firstRequest.SetBasicAuth("alice", "secret")
	first := httptest.NewRecorder()
	engine.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d", first.Code)
	}
	calls := authorizer.calls
	conditional := httptest.NewRequest(http.MethodGet, "/distribution/users/alice/marketplace.json", nil)
	conditional.Host = "market.example"
	conditional.SetBasicAuth("alice", "secret")
	conditional.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	engine.ServeHTTP(second, conditional)
	if second.Code != http.StatusNotModified || authorizer.calls <= calls {
		t.Fatalf("conditional status/calls = %d/%d, initial calls = %d", second.Code, authorizer.calls, calls)
	}

	authenticator.err = errors.New("invalid")
	failed := httptest.NewRecorder()
	engine.ServeHTTP(failed, conditional)
	if failed.Code != http.StatusNotFound {
		t.Fatalf("failed authentication status = %d", failed.Code)
	}
}

func handlerMarketplaceService() *distributiondomain.UserMarketplaceService {
	return distributiondomain.NewUserMarketplaceService(&handlerRepository{}, &countingAuthorizer{})
}

type subscriptionPATAuthenticatorStub struct {
	principal auth.Principal
	err       error
	calls     int
}

func (stub *subscriptionPATAuthenticatorStub) AuthenticateSubscriptionPAT(context.Context, string, string) (auth.Principal, error) {
	stub.calls++
	return stub.principal, stub.err
}

type handlerRepository struct{}

func (handlerRepository) FindUserMarketplace(context.Context, string) (distributiondomain.UserMarketplace, error) {
	return distributiondomain.UserMarketplace{UserID: "user-1", Username: "alice", Status: "active", NamespaceID: "namespace-1", NamespaceSlug: "alice", Candidates: []distributiondomain.UserMarketplaceCandidate{{NamespaceID: "namespace-1", NamespaceSlug: "alice", PluginID: "plugin-1", PluginSlug: "plugin", PluginStatus: "active", RepositoryID: "repository-1", RepositorySlug: "repo", RepositoryStatus: "ready", Version: "1.0.0", VersionStatus: "published", TagName: "v1.0.0", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}, nil
}

type countingAuthorizer struct{ calls int }

func (stub *countingAuthorizer) Authorize(_ context.Context, _ auth.Principal, _ auth.Action, _ auth.ResourceRef) error {
	stub.calls++
	return nil
}

func handlerMarketplacePrincipal(t *testing.T, id, username string) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal(id, username, auth.CredentialPAT, auth.RestrictedScopes(auth.ActionMarketplaceRead, auth.ActionPluginRead))
	if err != nil {
		t.Fatal(err)
	}
	return principal
}
