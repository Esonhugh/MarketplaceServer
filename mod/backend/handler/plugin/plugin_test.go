package plugin

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

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/juanjiTech/jin"
)

type lifecycleFake struct {
	principal          auth.Principal
	createInput        CreateInput
	listNamespace      string
	listPage           int
	listSize           int
	getNamespace       string
	getPlugin          string
	archivePlugin      string
	restorePlugin      string
	visibility         string
	listVersionsPlugin string
	listVersionsPage   int
	listVersionsSize   int
	getVersionTag      string
	publishInput       PublishInput
	setDefaultTag      string
	clearDefaultPlugin string
	createResult       Plugin
	listResult         PluginPage
	getResult          Plugin
	versionsResult     VersionPage
	versionResult      Version
	createErr          error
	listErr            error
	getErr             error
	archiveErr         error
	restoreErr         error
	visibilityErr      error
	listVersionsErr    error
	getVersionErr      error
	publishErr         error
	setDefaultErr      error
	clearDefaultErr    error
	repositoryRef      string
	repositoryPath     string
	repositoryPage     int
	repositorySize     int
	repositoryErr      error
	repositoryRefs     gitservice.RepositoryRefs
	repositoryTree     gitservice.RepositoryTree
	repositoryBlob     gitservice.RepositoryBlob
	repositoryCommits  gitservice.RepositoryCommitPage
}

func (fake *lifecycleFake) Create(_ context.Context, principal auth.Principal, input CreateInput) (Plugin, error) {
	fake.principal, fake.createInput = principal, input
	return fake.createResult, fake.createErr
}

func (fake *lifecycleFake) List(_ context.Context, principal auth.Principal, namespace string, page, size int) (PluginPage, error) {
	fake.principal, fake.listNamespace, fake.listPage, fake.listSize = principal, namespace, page, size
	return fake.listResult, fake.listErr
}

func (fake *lifecycleFake) Get(_ context.Context, principal auth.Principal, namespace, plugin string) (Plugin, error) {
	fake.principal, fake.getNamespace, fake.getPlugin = principal, namespace, plugin
	return fake.getResult, fake.getErr
}

func (fake *lifecycleFake) Archive(_ context.Context, principal auth.Principal, namespace, plugin string) error {
	fake.principal, fake.getNamespace, fake.archivePlugin = principal, namespace, plugin
	return fake.archiveErr
}

func (fake *lifecycleFake) Restore(_ context.Context, principal auth.Principal, namespace, plugin string) error {
	fake.principal, fake.getNamespace, fake.restorePlugin = principal, namespace, plugin
	return fake.restoreErr
}

func (fake *lifecycleFake) SetVisibility(_ context.Context, principal auth.Principal, namespace, plugin, visibility string) error {
	fake.principal, fake.getNamespace, fake.getPlugin, fake.visibility = principal, namespace, plugin, visibility
	return fake.visibilityErr
}

func (fake *lifecycleFake) ListVersions(_ context.Context, principal auth.Principal, namespace, plugin string, page, size int) (VersionPage, error) {
	fake.principal, fake.getNamespace, fake.listVersionsPlugin, fake.listVersionsPage, fake.listVersionsSize = principal, namespace, plugin, page, size
	return fake.versionsResult, fake.listVersionsErr
}

func (fake *lifecycleFake) GetVersion(_ context.Context, principal auth.Principal, namespace, plugin, tag string) (Version, error) {
	fake.principal, fake.getNamespace, fake.getPlugin, fake.getVersionTag = principal, namespace, plugin, tag
	return fake.versionResult, fake.getVersionErr
}

func (fake *lifecycleFake) Publish(_ context.Context, principal auth.Principal, namespace, plugin string, input PublishInput) (Version, error) {
	fake.principal, fake.getNamespace, fake.getPlugin, fake.publishInput = principal, namespace, plugin, input
	return fake.versionResult, fake.publishErr
}

func (fake *lifecycleFake) SetDefaultVersion(_ context.Context, principal auth.Principal, namespace, plugin, tag string) error {
	fake.principal, fake.getNamespace, fake.getPlugin, fake.setDefaultTag = principal, namespace, plugin, tag
	return fake.setDefaultErr
}

func (fake *lifecycleFake) ClearDefaultVersion(_ context.Context, principal auth.Principal, namespace, plugin string) error {
	fake.principal, fake.getNamespace, fake.clearDefaultPlugin = principal, namespace, plugin
	return fake.clearDefaultErr
}

func (fake *lifecycleFake) ListRepositoryRefs(_ context.Context, principal auth.Principal, namespace, plugin string) (gitservice.RepositoryRefs, error) {
	fake.principal, fake.getNamespace, fake.getPlugin = principal, namespace, plugin
	return fake.repositoryRefs, fake.repositoryErr
}

func (fake *lifecycleFake) ReadRepositoryTree(_ context.Context, principal auth.Principal, namespace, plugin, revision, path string) (gitservice.RepositoryTree, error) {
	fake.principal, fake.getNamespace, fake.getPlugin, fake.repositoryRef, fake.repositoryPath = principal, namespace, plugin, revision, path
	return fake.repositoryTree, fake.repositoryErr
}

func (fake *lifecycleFake) ReadRepositoryBlob(_ context.Context, principal auth.Principal, namespace, plugin, revision, path string) (gitservice.RepositoryBlob, error) {
	fake.principal, fake.getNamespace, fake.getPlugin, fake.repositoryRef, fake.repositoryPath = principal, namespace, plugin, revision, path
	return fake.repositoryBlob, fake.repositoryErr
}

func (fake *lifecycleFake) ListRepositoryCommits(_ context.Context, principal auth.Principal, namespace, plugin, revision, path string, page, size int) (gitservice.RepositoryCommitPage, error) {
	fake.principal, fake.getNamespace, fake.getPlugin, fake.repositoryRef, fake.repositoryPath, fake.repositoryPage, fake.repositorySize = principal, namespace, plugin, revision, path, page, size
	return fake.repositoryCommits, fake.repositoryErr
}

type authenticatorFake struct {
	principal auth.Principal
	err       error
	encoded   string
	calls     int
}

func (fake *authenticatorFake) AuthenticateBearer(_ context.Context, encoded string) (auth.Principal, error) {
	fake.calls++
	fake.encoded = encoded
	return fake.principal, fake.err
}

func testPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialJWT, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func pluginEngine(t *testing.T, service *lifecycleFake, authenticator *authenticatorFake) *jin.Engine {
	t.Helper()
	if !authenticator.principal.IsUser() && authenticator.err == nil {
		authenticator.principal = testPrincipal(t)
	}
	engine := jin.New()
	NewHandler(service, authenticator).Register(engine)
	return engine
}

func authorizedRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer jwt-token")
	return request
}

func samplePlugin() Plugin {
	defaultTag := "v1.2.0"
	return Plugin{
		Namespace: "research", Name: "scanner", Status: "active", Visibility: "public", RepositoryStatus: "ready",
		CloneURL: "/git/research/scanner.git", DefaultVersion: &defaultTag,
		CreatedAt: time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC),
	}
}

func sampleVersion() Version {
	return Version{
		Tag: "v1.2.0", Status: "available", CommitSHA: strings.Repeat("a", 40),
		PublishedAt: time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC), UpdatedAt: time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC),
	}
}

func TestHandlerCreateAndListUseJWTAndAPIValues(t *testing.T) {
	service := &lifecycleFake{createResult: samplePlugin(), listResult: PluginPage{Items: []Plugin{samplePlugin()}, Page: 2, Size: 50, Total: 51}}
	engine := pluginEngine(t, service, &authenticatorFake{})

	create := httptest.NewRecorder()
	engine.ServeHTTP(create, authorizedRequest(http.MethodPost, "/api/v1/namespaces/research/plugins", `{"name":"scanner","futureField":true}`))
	if create.Code != http.StatusCreated || service.createInput != (CreateInput{Namespace: "research", Name: "scanner", Visibility: "public"}) || strings.Contains(create.Body.String(), "storageKey") || strings.Contains(create.Body.String(), `"id"`) {
		t.Fatalf("create = %d/%#v/%s", create.Code, service.createInput, create.Body.String())
	}

	list := httptest.NewRecorder()
	engine.ServeHTTP(list, authorizedRequest(http.MethodGet, "/api/v1/namespaces/research/plugins?page=2&size=50", ""))
	if list.Code != http.StatusOK || service.listNamespace != "research" || service.listPage != 2 || service.listSize != 50 || !service.principal.IsUser() {
		t.Fatalf("list = %d/%q/%d/%d/%s", list.Code, service.listNamespace, service.listPage, service.listSize, list.Body.String())
	}
}

func TestHandlerEmptyListsRenderArrays(t *testing.T) {
	service := &lifecycleFake{listResult: PluginPage{Page: 1, Size: 20}, versionsResult: VersionPage{Page: 1, Size: 20}}
	engine := pluginEngine(t, service, &authenticatorFake{})
	for _, target := range []string{
		"/api/v1/namespaces/research/plugins",
		"/api/v1/namespaces/research/plugins/scanner/versions",
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, authorizedRequest(http.MethodGet, target, ""))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) {
			t.Fatalf("target/response = %q/%d/%s", target, response.Code, response.Body.String())
		}
	}
}

func TestHandlerExactPublicReadsDelegateAnonymousAndInvalidBearerCannotFallback(t *testing.T) {
	service := &lifecycleFake{getResult: samplePlugin(), versionResult: sampleVersion()}
	authenticator := &authenticatorFake{}
	engine := pluginEngine(t, service, authenticator)
	for _, target := range []string{
		"/api/v1/namespaces/research/plugins/scanner",
		"/api/v1/namespaces/research/plugins/scanner/versions/v1.2.0",
	} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK || !service.principal.IsAnonymous() || authenticator.calls != 0 {
			t.Fatalf("anonymous target/response/principal/calls = %q/%d/%#v/%d", target, response.Code, service.principal, authenticator.calls)
		}
	}

	invalidService := &lifecycleFake{getResult: samplePlugin()}
	invalidAuthenticator := &authenticatorFake{err: errors.New("bad token")}
	invalid := httptest.NewRecorder()
	pluginEngine(t, invalidService, invalidAuthenticator).ServeHTTP(invalid, authorizedRequest(http.MethodGet, "/api/v1/namespaces/research/plugins/scanner", ""))
	if invalid.Code != http.StatusUnauthorized || invalidAuthenticator.calls != 1 || invalidService.getPlugin != "" || invalid.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("invalid bearer = %d/%d/%q/%q", invalid.Code, invalidAuthenticator.calls, invalidService.getPlugin, invalid.Body.String())
	}
}

func TestHandlerActionDispatchersAcceptOnlyApprovedSuffixes(t *testing.T) {
	service := &lifecycleFake{versionResult: sampleVersion()}
	for name, test := range map[string]struct {
		method string
		target string
		body   string
		check  func(*testing.T, *lifecycleFake)
	}{
		"archive": {http.MethodPost, "/api/v1/namespaces/research/plugins/scanner:archive", "", func(t *testing.T, fake *lifecycleFake) {
			if fake.archivePlugin != "scanner" {
				t.Fatalf("archive plugin = %q", fake.archivePlugin)
			}
		}},
		"restore": {http.MethodPost, "/api/v1/namespaces/research/plugins/scanner:restore", "", func(t *testing.T, fake *lifecycleFake) {
			if fake.restorePlugin != "scanner" {
				t.Fatalf("restore plugin = %q", fake.restorePlugin)
			}
		}},
		"visibility": {http.MethodPost, "/api/v1/namespaces/research/plugins/scanner:set-visibility", `{"visibility":"private"}`, func(t *testing.T, fake *lifecycleFake) {
			if fake.getPlugin != "scanner" || fake.visibility != "private" {
				t.Fatalf("visibility = %q/%q", fake.getPlugin, fake.visibility)
			}
		}},
		"publish": {http.MethodPost, "/api/v1/namespaces/research/plugins/scanner/versions:publish", `{"tag":"v1.2.0","makeDefault":true}`, func(t *testing.T, fake *lifecycleFake) {
			if fake.publishInput != (PublishInput{Tag: "v1.2.0", MakeDefault: true}) {
				t.Fatalf("publish input = %#v", fake.publishInput)
			}
		}},
		"default": {http.MethodPost, "/api/v1/namespaces/research/plugins/scanner/versions/v1.2.0:set-default", "", func(t *testing.T, fake *lifecycleFake) {
			if fake.setDefaultTag != "v1.2.0" {
				t.Fatalf("default tag = %q", fake.setDefaultTag)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			service = &lifecycleFake{versionResult: sampleVersion()}
			response := httptest.NewRecorder()
			engine := pluginEngine(t, service, &authenticatorFake{})
			engine.ServeHTTP(response, authorizedRequest(test.method, test.target, test.body))
			want := http.StatusNoContent
			if name == "publish" {
				want = http.StatusCreated
			}
			if response.Code != want || want == http.StatusNoContent && response.Body.Len() != 0 {
				t.Fatalf("response = %d/%s", response.Code, response.Body.String())
			}
			test.check(t, service)
		})
	}

	for _, target := range []string{
		"/api/v1/namespaces/research/plugins/scanner:destroy",
		"/api/v1/namespaces/research/plugins/BAD:archive",
		"/api/v1/namespaces/research/plugins/scanner:destroy",
		"/api/v1/namespaces/research/plugins/scanner/versions/v1.2.0:destroy",
	} {
		service = &lifecycleFake{}
		response := httptest.NewRecorder()
		pluginEngine(t, service, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodPost, target, ""))
		if response.Code != http.StatusNotFound || service.getPlugin != "" || service.archivePlugin != "" || service.publishInput.Tag != "" {
			t.Fatalf("invalid suffix target/response/service = %q/%d/%#v", target, response.Code, service)
		}
	}
}

func TestHandlerRejectsMissingRequiredJSONMembersAt422(t *testing.T) {
	for name, test := range map[string]struct {
		target string
		body   string
	}{
		"create visibility null": {"/api/v1/namespaces/research/plugins", `{"name":"scanner","visibility":null}`},
		"set visibility absent":  {"/api/v1/namespaces/research/plugins/scanner:set-visibility", `{}`},
		"set visibility null":    {"/api/v1/namespaces/research/plugins/scanner:set-visibility", `{"visibility":null}`},
		"publish tag absent":     {"/api/v1/namespaces/research/plugins/scanner/versions:publish", `{}`},
		"publish tag null":       {"/api/v1/namespaces/research/plugins/scanner/versions:publish", `{"tag":null}`},
		"publish default null":   {"/api/v1/namespaces/research/plugins/scanner/versions:publish", `{"tag":"v1.2.0","makeDefault":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			service := &lifecycleFake{}
			response := httptest.NewRecorder()
			pluginEngine(t, service, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodPost, test.target, test.body))
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("response = %d/%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestHandlerRejectsMalformedMutationBodiesAt400(t *testing.T) {
	for name, body := range map[string]string{
		"missing":   "",
		"null":      "null",
		"trailing":  `{"name":"scanner"} {}`,
		"malformed": `{"name":`,
		"oversized": `{"name":"` + strings.Repeat("a", (64<<10)+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			service := &lifecycleFake{}
			response := httptest.NewRecorder()
			pluginEngine(t, service, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodPost, "/api/v1/namespaces/research/plugins", body))
			if response.Code != http.StatusBadRequest || service.createInput.Name != "" {
				t.Fatalf("response/input = %d/%#v/%s", response.Code, service.createInput, response.Body.String())
			}
		})
	}
}

func TestHandlerMapsServiceErrorsAndInputValidation(t *testing.T) {
	for name, test := range map[string]struct {
		err  error
		want int
	}{
		"forbidden": {ErrForbidden, http.StatusForbidden},
		"not found": {ErrNotFound, http.StatusNotFound},
		"conflict":  {ErrConflict, http.StatusConflict},
		"gone":      {ErrGone, http.StatusGone},
		"invalid":   {ErrInvalidInput, http.StatusUnprocessableEntity},
		"internal":  {errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			service := &lifecycleFake{getErr: test.err}
			response := httptest.NewRecorder()
			pluginEngine(t, service, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodGet, "/api/v1/namespaces/research/plugins/scanner", ""))
			if response.Code != test.want {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			assertErrorRequestID(t, response)
		})
	}

	service := &lifecycleFake{}
	response := httptest.NewRecorder()
	pluginEngine(t, service, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodPost, "/api/v1/namespaces/research/plugins", `{"name":"Upper"}`))
	if response.Code != http.StatusUnprocessableEntity || service.createInput.Name != "" {
		t.Fatalf("invalid create = %d/%#v", response.Code, service.createInput)
	}
}

func TestHandlerRejectsMalformedVersionActionSuffixesWithoutServiceDelegation(t *testing.T) {
	for _, target := range []string{
		"/api/v1/namespaces/research/plugins/scanner/versions:destroy",
		"/api/v1/namespaces/research/plugins/scanner/versions/v1.2.0:destroy",
		"/api/v1/namespaces/research/plugins/scanner/versions/latest:set-default",
	} {
		service := &lifecycleFake{}
		response := httptest.NewRecorder()
		pluginEngine(t, service, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodPost, target, ""))
		if response.Code != http.StatusNotFound || service.getPlugin != "" || service.publishInput.Tag != "" || service.setDefaultTag != "" {
			t.Fatalf("target/response/service = %q/%d/%#v", target, response.Code, service)
		}
	}
}

func TestHandlerRequiresJWTForMutationsAndLists(t *testing.T) {
	for _, target := range []string{
		"/api/v1/namespaces/research/plugins",
		"/api/v1/namespaces/research/plugins/scanner/versions",
	} {
		response := httptest.NewRecorder()
		pluginEngine(t, &lifecycleFake{}, &authenticatorFake{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("target/response = %q/%d/%s", target, response.Code, response.Body.String())
		}
	}
}

func TestHandlerRepositoryBrowsingDelegatesPaginationAndMapsErrors(t *testing.T) {
	for name, test := range map[string]struct {
		target string
		err    error
		want   int
		check  func(*testing.T, *lifecycleFake)
	}{
		"tree forwards revision and path": {
			target: "/api/v1/namespaces/research/plugins/scanner/repository/tree?ref=v1.2.0&path=src/main.go", want: http.StatusOK,
			check: func(t *testing.T, fake *lifecycleFake) {
				if fake.repositoryRef != "v1.2.0" || fake.repositoryPath != "src/main.go" || fake.getNamespace != "research" || fake.getPlugin != "scanner" {
					t.Fatalf("tree delegation = %#v", fake)
				}
			},
		},
		"commits forwards pagination": {
			target: "/api/v1/namespaces/research/plugins/scanner/repository/commits?ref=main&path=src&page=2&size=1", want: http.StatusOK,
			check: func(t *testing.T, fake *lifecycleFake) {
				if fake.repositoryRef != "main" || fake.repositoryPath != "src" || fake.repositoryPage != 2 || fake.repositorySize != 1 {
					t.Fatalf("commit delegation = %#v", fake)
				}
			},
		},
		"missing revision maps not found": {target: "/api/v1/namespaces/research/plugins/scanner/repository/tree?ref=missing", err: gitservice.ErrRevisionNotFound, want: http.StatusNotFound},
		"missing path maps not found":     {target: "/api/v1/namespaces/research/plugins/scanner/repository/blob?ref=main&path=missing", err: gitservice.ErrPathNotFound, want: http.StatusNotFound},
		"invalid path maps validation":    {target: "/api/v1/namespaces/research/plugins/scanner/repository/blob?ref=main&path=..%2Fsecret", err: gitservice.ErrInvalidBrowseInput, want: http.StatusUnprocessableEntity},
		"binary maps conflict":            {target: "/api/v1/namespaces/research/plugins/scanner/repository/blob?ref=main&path=binary", err: gitservice.ErrPathNotText, want: http.StatusConflict},
		"large blob maps 413":             {target: "/api/v1/namespaces/research/plugins/scanner/repository/blob?ref=main&path=large", err: gitservice.ErrBlobTooLarge, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(name, func(t *testing.T) {
			service := &lifecycleFake{repositoryErr: test.err}
			response := httptest.NewRecorder()
			pluginEngine(t, service, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodGet, test.target, ""))
			if response.Code != test.want {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if test.check != nil {
				test.check(t, service)
			}
		})
	}

	response := httptest.NewRecorder()
	pluginEngine(t, &lifecycleFake{}, &authenticatorFake{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/research/plugins/scanner/repository/refs", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous repository browse status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	pluginEngine(t, &lifecycleFake{}, &authenticatorFake{}).ServeHTTP(response, authorizedRequest(http.MethodGet, "/api/v1/namespaces/research/plugins/scanner/repository/commits?page=0", ""))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid repository pagination status = %d", response.Code)
	}
}

func TestHandlerClearDefaultAndExactVersionInput(t *testing.T) {
	service := &lifecycleFake{versionResult: sampleVersion()}
	engine := pluginEngine(t, service, &authenticatorFake{})
	clear := httptest.NewRecorder()
	engine.ServeHTTP(clear, authorizedRequest(http.MethodDelete, "/api/v1/namespaces/research/plugins/scanner/default-version", ""))
	if clear.Code != http.StatusNoContent || clear.Body.Len() != 0 || service.clearDefaultPlugin != "scanner" {
		t.Fatalf("clear = %d/%q/%q", clear.Code, clear.Body.String(), service.clearDefaultPlugin)
	}

	invalid := httptest.NewRecorder()
	engine.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/research/plugins/scanner/versions/latest", nil))
	if invalid.Code != http.StatusNotFound || service.getVersionTag != "" {
		t.Fatalf("invalid version = %d/%q", invalid.Code, service.getVersionTag)
	}
}

func assertErrorRequestID(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	requestID := response.Header().Get("X-Request-Id")
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(requestID) {
		t.Fatalf("request ID = %q", requestID)
	}
	var body struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RequestID != requestID {
		t.Fatalf("body/header IDs = %q/%q", body.RequestID, requestID)
	}
}
