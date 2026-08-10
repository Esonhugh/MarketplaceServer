package git

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	backendmod "github.com/Esonhugh/MarketplaceServer/mod/backend"
	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"github.com/juanjiTech/inject/v2"
	jinengine "github.com/juanjiTech/jin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const productionGitPostgresDSNEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"

type productionSmartHTTPHarness struct {
	engine   *jinengine.Engine
	db       *gorm.DB
	fake     *fakeGit
	password string
	readKey  string
	writeKey string
	repoID   string
	userID   string
}

func TestProductionSmartHTTPAuthenticationAndAuthorization(t *testing.T) {
	harness := newProductionSmartHTTPHarness(t, true)

	tests := []struct {
		name        string
		path        string
		username    string
		credential  string
		wantStatus  int
		wantGitCall bool
	}{
		{name: "password read", path: "git-upload-pack", username: "alice", credential: harness.password, wantStatus: http.StatusOK, wantGitCall: true},
		{name: "password write", path: "git-receive-pack", username: "alice", credential: harness.password, wantStatus: http.StatusOK, wantGitCall: true},
		{name: "read key reads", path: "git-upload-pack", username: "alice", credential: harness.readKey, wantStatus: http.StatusOK, wantGitCall: true},
		{name: "read key cannot write", path: "git-receive-pack", username: "alice", credential: harness.readKey, wantStatus: http.StatusForbidden},
		{name: "write key writes", path: "git-receive-pack", username: "alice", credential: harness.writeKey, wantStatus: http.StatusOK, wantGitCall: true},
		{name: "write key cannot read", path: "git-upload-pack", username: "alice", credential: harness.writeKey, wantStatus: http.StatusForbidden},
		{name: "invalid password", path: "git-upload-pack", username: "alice", credential: "wrong", wantStatus: http.StatusUnauthorized},
		{name: "anonymous private read", path: "git-upload-pack", wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := harness.fake.readLog(t)
			request := httptest.NewRequest(http.MethodPost, "/git/alice/plugin-one.git/"+test.path, strings.NewReader("request"))
			if test.username != "" {
				request.SetBasicAuth(test.username, test.credential)
			}
			response := httptest.NewRecorder()
			harness.engine.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %q", response.Code, test.wantStatus, response.Body.String())
			}
			after := harness.fake.readLog(t)
			if test.wantGitCall && after == before {
				t.Fatal("authorized request did not invoke Git")
			}
			if !test.wantGitCall && after != before {
				t.Fatalf("denied request invoked Git; before=%q after=%q", before, after)
			}
			if test.wantStatus == http.StatusUnauthorized && response.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("401 response is missing Basic challenge")
			}
		})
	}

	t.Run("revoked key is rejected immediately", func(t *testing.T) {
		now := time.Now().UTC()
		if err := harness.db.Model(&identitydomain.PersonalAccessToken{}).Where("name = ?", "read").Update("revoked_at", now).Error; err != nil {
			t.Fatal(err)
		}
		harness.assertDeniedWithoutGit(t, harness.readKey)
	})

	t.Run("expired key is rejected", func(t *testing.T) {
		key := harness.createToken(t, "expired", []auth.Action{auth.ActionRepositoryRead}, timePointer(time.Now().Add(-time.Minute)), nil)
		harness.assertDeniedWithoutGit(t, key)
	})

	t.Run("disabled user invalidates password and key", func(t *testing.T) {
		if err := harness.db.Model(&identitydomain.User{}).Where("id = ?", harness.userID).Update("status", identitydomain.UserStatusDisabled).Error; err != nil {
			t.Fatal(err)
		}
		for _, credential := range []string{harness.password, harness.writeKey} {
			harness.assertDeniedWithoutGit(t, credential)
		}
	})
}

func TestProductionAPIKeyRealGitPushAndClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is unavailable")
	}
	harness := newProductionSmartHTTPHarness(t, false)
	key := harness.createToken(t, "git-client", []auth.Action{auth.ActionRepositoryRead, auth.ActionRepositoryWrite}, nil, nil)
	server := httptest.NewServer(harness.engine)
	defer server.Close()

	askpass := filepath.Join(t.TempDir(), "askpass.sh")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\nprintf '%s\\n' \"$MARKETPLACE_TEST_GIT_PASSWORD\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(directory string, arguments ...string) string {
		t.Helper()
		command := exec.Command("git", arguments...)
		command.Dir = directory
		command.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_ASKPASS="+askpass,
			"MARKETPLACE_TEST_GIT_PASSWORD="+key,
		)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git command failed: %v\n%s", err, output)
		}
		return string(output)
	}

	source := t.TempDir()
	run(source, "init")
	run(source, "config", "user.name", "Marketplace Test")
	run(source, "config", "user.email", "marketplace@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("production auth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(source, "add", "README.md")
	run(source, "commit", "-m", "initial")
	remote := strings.Replace(server.URL, "://", "://alice@", 1) + "/git/alice/plugin-one.git"
	run(source, "push", remote, "HEAD:refs/heads/main")
	cloneParent := t.TempDir()
	clone := filepath.Join(cloneParent, "clone")
	run(cloneParent, "clone", remote, clone)
	content, err := os.ReadFile(filepath.Join(clone, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "production auth\n" {
		t.Fatalf("cloned content = %q", content)
	}
}

func newProductionSmartHTTPHarness(t *testing.T, fakeBinary bool) *productionSmartHTTPHarness {
	t.Helper()
	db := openProductionGitPostgres(t)
	pepper := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv(identitydomain.APIKeyPepperEnvironment, identitydomain.EncodeAPIKeyPepper(pepper))
	password := "correct horse battery staple"
	passwordHash, err := auth.HashPassword(password, auth.Argon2idParams{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, HashLength: 16})
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	if err := db.Create(&identitydomain.User{ID: userID, Username: "alice", DisplayName: "Alice", Status: identitydomain.UserStatusActive, PasswordHash: passwordHash}).Error; err != nil {
		t.Fatal(err)
	}

	engine := jinengine.New()
	hub := kernel.Hub{Injector: inject.New()}
	hub.Map(&engine, &db)
	gitModule := &Mod{}
	configuration := gitModule.Config().(*Config)
	configuration.StorageRoot = t.TempDir()
	var fake *fakeGit
	if fakeBinary {
		created := newFakeGit(t)
		fake = &created
		configuration.gitBinary = fake.path
	}
	if err := gitModule.Init(&hub); err != nil {
		t.Fatal(err)
	}
	backendModule := &backendmod.Mod{}
	if err := backendModule.PostInit(&hub); err != nil {
		t.Fatal(err)
	}

	ownerID := userID
	namespaceID := uuid.NewString()
	repositoryID := testRepositoryID
	if err := db.Create(&identitydomain.Namespace{ID: namespaceID, Kind: identitydomain.NamespaceKindUser, Slug: "alice", DisplayName: "Alice", OwnerUserID: &ownerID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&distributiondomain.Repository{ID: repositoryID, NamespaceID: namespaceID, Slug: "plugin-one", Visibility: "private", Status: distributiondomain.RepositoryStatusReady, StorageKey: uuid.NewString()}).Error; err != nil {
		t.Fatal(err)
	}
	var repositories gitservice.RepositoryService
	if err := hub.Load(&repositories); err != nil {
		t.Fatal(err)
	}
	if err := repositories.InitBareRepository(context.Background(), repositoryID); err != nil {
		t.Fatal(err)
	}
	if err := gitModule.Load(&hub); err != nil {
		t.Fatal(err)
	}

	harness := &productionSmartHTTPHarness{engine: engine, db: db, fake: fake, password: password, repoID: repositoryID, userID: userID}
	harness.readKey = harness.createToken(t, "read", []auth.Action{auth.ActionRepositoryRead}, nil, nil)
	harness.writeKey = harness.createToken(t, "write", []auth.Action{auth.ActionRepositoryWrite}, nil, nil)
	return harness
}

func (harness *productionSmartHTTPHarness) createToken(t *testing.T, name string, scopes []auth.Action, expiresAt, revokedAt *time.Time) string {
	t.Helper()
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	pepper := []byte("0123456789abcdef0123456789abcdef")
	index, err := auth.IndexAPIKey(plaintext, pepper)
	if err != nil {
		t.Fatal(err)
	}
	tokenID := uuid.NewString()
	token := identitydomain.PersonalAccessToken{ID: tokenID, UserID: harness.userID, Name: name, SecretHMAC: index, ExpiresAt: expiresAt, RevokedAt: revokedAt}
	if err := harness.db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	for _, action := range scopes {
		if err := harness.db.Create(&identitydomain.PersonalAccessTokenScope{TokenID: tokenID, Action: action}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return plaintext
}

func (harness *productionSmartHTTPHarness) assertDeniedWithoutGit(t *testing.T, credential string) {
	t.Helper()
	before := harness.fake.readLog(t)
	request := httptest.NewRequest(http.MethodPost, "/git/alice/plugin-one.git/git-upload-pack", strings.NewReader("request"))
	request.SetBasicAuth("alice", credential)
	response := httptest.NewRecorder()
	harness.engine.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("response = (%d, %q), want 401 challenge", response.Code, response.Body.String())
	}
	if after := harness.fake.readLog(t); after != before {
		t.Fatalf("denied credential invoked Git; before=%q after=%q", before, after)
	}
}

func openProductionGitPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(productionGitPostgresDSNEnvironment)
	if dsn == "" {
		t.Skip(productionGitPostgresDSNEnvironment + " is not configured; skipping production Git authentication integration test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	schema := "marketplace_git_auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error })
	db, err := gorm.Open(postgres.Open(productionGitPostgresDSN(t, dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration schema: %v", err)
	}
	if err := backendmod.Migrate(db); err != nil {
		t.Fatalf("migrate backend: %v", err)
	}
	return db
}

func productionGitPostgresDSN(t *testing.T, dsn, schema string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return fmt.Sprintf("%s search_path=%s", dsn, schema)
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
