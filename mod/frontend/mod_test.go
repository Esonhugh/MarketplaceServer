package frontend

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/juanjiTech/inject/v2"
	"github.com/juanjiTech/jin"
)

func TestModuleName(t *testing.T) {
	m := &Mod{}
	if got := m.Name(); got != "frontend" {
		t.Fatalf("Name() = %q, want frontend", got)
	}
}

func TestConfigTags(t *testing.T) {
	typ := reflect.TypeOf(Config{})
	for _, name := range []string{"Disabled", "BasePath"} {
		field, ok := typ.FieldByName(name)
		if !ok {
			t.Fatalf("Config missing %s field", name)
		}
		if got := field.Tag.Get("yaml"); got == "" {
			t.Errorf("Config.%s yaml tag is empty", name)
		}
		if got := field.Tag.Get("mapstructure"); got == "" {
			t.Errorf("Config.%s mapstructure tag is empty", name)
		}
	}
}

func TestWebPackageDefinesRunnableChecks(t *testing.T) {
	data, err := fs.ReadFile(os.DirFS("web"), "package.json")
	if err != nil {
		t.Fatalf("read web/package.json: %v", err)
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("parse web/package.json: %v", err)
	}
	for _, name := range []string{"check", "test", "build"} {
		if strings.TrimSpace(pkg.Scripts[name]) == "" {
			t.Errorf("web/package.json scripts.%s is missing", name)
		}
	}
}

func TestLoadFailsWithoutJinEngine(t *testing.T) {
	m := &Mod{}
	hub := &kernel.Hub{Injector: inject.New()}

	err := m.Load(hub)
	if err == nil {
		t.Fatal("Load() succeeded without *jin.Engine in DI, want fail-fast error")
	}
	if !strings.Contains(err.Error(), "jin.Engine") {
		t.Fatalf("Load() error = %q, want it to mention jin.Engine", err.Error())
	}
}

func TestDisabledDoesNotInstallSPAFallback(t *testing.T) {
	engine := loadFrontendForTest(t, Config{Disabled: true})

	rec := performRequest(engine, http.MethodGet, "/dashboard")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /dashboard with frontend disabled status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "MarketplaceServer") {
		t.Fatal("disabled frontend returned SPA index body")
	}
}

func TestEmbeddedIndexReferencesExistingAssets(t *testing.T) {
	index, err := fs.ReadFile(embeddedDist, "dist/index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}

	assetReference := regexp.MustCompile(`(?:src|href)=["']/?([^"']+)["']`)
	matches := assetReference.FindAllSubmatch(index, -1)
	if len(matches) == 0 {
		t.Fatal("embedded index contains no asset references")
	}
	for _, match := range matches {
		name := strings.TrimPrefix(string(match[1]), "./")
		if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") || strings.HasPrefix(name, "//") {
			continue
		}
		if _, err := fs.Stat(embeddedDist, "dist/"+name); err != nil {
			t.Errorf("embedded index asset %q does not exist: %v", name, err)
		}
	}
}

func TestSPAFallbackServesIndexForNavigation(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})

	rec := performRequest(engine, http.MethodGet, "/dashboard")
	assertIndexResponse(t, rec)
}

func TestReservedPathsDoNotFallback(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})
	reservedPaths := []string{"/api", "/api/v1", "/gapi", "/git", "/distribution", "/marketplaces", "/healthz", "/debug", "/metrics"}

	for _, reservedPath := range reservedPaths {
		for _, requestPath := range []string{reservedPath, reservedPath + "/child"} {
			t.Run(requestPath, func(t *testing.T) {
				rec := performRequest(engine, http.MethodGet, requestPath)
				assertNotFoundWithoutIndex(t, requestPath, rec)
			})
		}
	}
}

func TestMissingStaticResourcesWithExtensionsDoNotFallback(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})

	for _, requestPath := range []string{
		"/assets/missing.js",
		"/assets/missing.css",
		"/assets/missing.js.map",
		"/assets/missing.css.map",
		"/missing.svg",
	} {
		t.Run(requestPath, func(t *testing.T) {
			rec := performRequest(engine, http.MethodGet, requestPath)
			assertNotFoundWithoutIndex(t, requestPath, rec)
		})
	}
}

func TestBasePathIndexUsesScopedAssetURLs(t *testing.T) {
	engine := loadFrontendForTest(t, Config{BasePath: "/console"})

	rec := performRequest(engine, http.MethodGet, "/console/dashboard")
	assertIndexResponse(t, rec)
	body := rec.Body.String()
	if strings.Contains(body, `src="/assets/`) || strings.Contains(body, `href="/assets/`) {
		t.Fatalf("base-path index contains root-scoped asset URL: %q", body)
	}
	if !strings.Contains(body, `/console/assets/`) {
		t.Fatalf("base-path index does not contain scoped asset URL: %q", body)
	}
}

func TestBasePathServingAndReservedPathBoundaries(t *testing.T) {
	engine := loadFrontendForTest(t, Config{BasePath: "/console"})
	assetPath := findEmbeddedAsset(t, ".js")

	for _, tc := range []struct {
		name       string
		path       string
		wantStatus int
		wantIndex  bool
	}{
		{name: "base root", path: "/console", wantStatus: http.StatusOK, wantIndex: true},
		{name: "base navigation", path: "/console/dashboard", wantStatus: http.StatusOK, wantIndex: true},
		{name: "base asset", path: "/console" + assetPath, wantStatus: http.StatusOK},
		{name: "outside base", path: "/dashboard", wantStatus: http.StatusNotFound},
		{name: "outside asset", path: assetPath, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := performRequest(engine, http.MethodGet, tc.path)
			if rec.Code != tc.wantStatus {
				t.Fatalf("GET %s status = %d, want %d", tc.path, rec.Code, tc.wantStatus)
			}
			if tc.wantIndex {
				assertIndexResponse(t, rec)
			}
			if tc.name == "base asset" {
				if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
					t.Fatalf("GET %s Content-Type = %q, want javascript", tc.path, contentType)
				}
			}
		})
	}

	reservedPaths := []string{"/api", "/api/v1", "/gapi", "/git", "/distribution", "/marketplaces", "/healthz", "/debug", "/metrics"}
	for _, reservedPath := range reservedPaths {
		for _, requestPath := range []string{reservedPath, reservedPath + "/child", "/console" + reservedPath, "/console" + reservedPath + "/child"} {
			t.Run("reserved "+requestPath, func(t *testing.T) {
				rec := performRequest(engine, http.MethodGet, requestPath)
				assertNotFoundWithoutIndex(t, requestPath, rec)
			})
		}
	}
}

func TestMalformedPathsDoNotFallback(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})

	for _, requestPath := range []string{
		"/%2e%2e/dashboard",
		"/assets/%2e%2e/dashboard",
		"/%00dashboard",
		"//dashboard",
		"/assets//missing.js",
	} {
		t.Run(requestPath, func(t *testing.T) {
			rec := performRequest(engine, http.MethodGet, requestPath)
			assertNotFoundWithoutIndex(t, requestPath, rec)
		})
	}
}

func TestServedFrontendContentDisablesMIMESniffing(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})

	for _, requestPath := range []string{"/dashboard", findEmbeddedAsset(t, ".js")} {
		t.Run(requestPath, func(t *testing.T) {
			rec := performRequest(engine, http.MethodGet, requestPath)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want 200", requestPath, rec.Code)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("GET %s X-Content-Type-Options = %q, want nosniff", requestPath, got)
			}
		})
	}
}

func TestStaticAssetCacheHeadersAndConditionalGet(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})
	assetPath := findEmbeddedAsset(t, ".js")

	rec := performRequest(engine, http.MethodGet, assetPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "javascript") {
		t.Fatalf("Content-Type = %q, want javascript", contentType)
	}
	if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q, want immutable asset cache", cacheControl)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag header is empty for static asset")
	}

	conditional := performRequestWithHeaders(engine, http.MethodGet, assetPath, http.Header{"If-None-Match": []string{etag}})
	if conditional.Code != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d, want 304", conditional.Code)
	}
	if conditional.Body.Len() != 0 {
		t.Fatalf("conditional GET body length = %d, want 0", conditional.Body.Len())
	}
}

func TestReservedPathsDoNotUseFrontendMethodHandling(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})

	for _, requestPath := range []string{"/api/v1/missing", "/git/team/repo.git/git-receive-pack"} {
		t.Run(requestPath, func(t *testing.T) {
			rec := performRequest(engine, http.MethodPost, requestPath)
			assertNotFoundWithoutIndex(t, requestPath, rec)
			if allow := rec.Header().Get("Allow"); allow != "" {
				t.Fatalf("POST %s Allow = %q, want no frontend method header", requestPath, allow)
			}
		})
	}
}

func TestNonGetHeadRejected(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})

	rec := performRequest(engine, http.MethodPost, "/dashboard")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /dashboard status = %d, want 405", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "MarketplaceServer") {
		t.Fatal("POST /dashboard returned SPA index body, want method rejection")
	}
}

func TestHeadResponsesHaveNoBody(t *testing.T) {
	engine := loadFrontendForTest(t, Config{})

	for _, requestPath := range []string{"/dashboard", findEmbeddedAsset(t, ".js")} {
		t.Run(requestPath, func(t *testing.T) {
			rec := performRequest(engine, http.MethodHead, requestPath)
			if rec.Code != http.StatusOK {
				t.Fatalf("HEAD %s status = %d, want 200", requestPath, rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("HEAD %s body length = %d, want 0", requestPath, rec.Body.Len())
			}
		})
	}
}

func loadFrontendForTest(t *testing.T, cfg Config) *jin.Engine {
	t.Helper()

	engine := jin.New()
	hub := &kernel.Hub{Injector: inject.New()}
	hub.Map(&engine)

	m := &Mod{config: cfg}
	if err := m.Load(hub); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return engine
}

func performRequest(engine *jin.Engine, method, target string) *httptest.ResponseRecorder {
	return performRequestWithHeaders(engine, method, target, nil)
}

func performRequestWithHeaders(engine *jin.Engine, method, target string, headers http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for name, values := range headers {
		req.Header[name] = append([]string(nil), values...)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func assertIndexResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
	if cacheControl := rec.Header().Get("Cache-Control"); cacheControl != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cacheControl)
	}
	if body := rec.Body.String(); !strings.Contains(body, "MarketplaceServer") {
		t.Fatalf("index body did not contain MarketplaceServer: %q", body)
	}
}

func assertNotFoundWithoutIndex(t *testing.T, requestPath string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET %s status = %d, want 404", requestPath, rec.Code)
	}
	if strings.Contains(rec.Body.String(), "MarketplaceServer") {
		t.Fatalf("GET %s returned SPA index body, want 404", requestPath)
	}
}

func findEmbeddedAsset(t *testing.T, ext string) string {
	t.Helper()

	var found string
	err := fs.WalkDir(embeddedDist, "dist/assets", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(name) != ext || found != "" {
			return err
		}
		found = "/" + strings.TrimPrefix(name, "dist/")
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded dist assets: %v", err)
	}
	if found == "" {
		t.Fatalf("embedded dist missing %s asset", ext)
	}
	return found
}
