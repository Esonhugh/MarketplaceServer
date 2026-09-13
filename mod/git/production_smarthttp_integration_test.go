package git

import (
	"encoding/json"
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
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
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
	engine     *jinengine.Engine
	db         *gorm.DB
	fake       *fakeGit
	password   string
	subReadPAT string
	clonePAT   string
	writePAT   string
	repoID     string
	userID     string
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
		{name: "account password denied", path: "git-upload-pack", username: "alice", credential: harness.password, wantStatus: http.StatusUnauthorized},
		{name: "subscription PAT denied", path: "git-upload-pack", username: "alice", credential: harness.subReadPAT, wantStatus: http.StatusUnauthorized},
		{name: "clone PAT reads", path: "git-upload-pack", username: "alice", credential: harness.clonePAT, wantStatus: http.StatusOK, wantGitCall: true},
		{name: "clone PAT cannot write", path: "git-receive-pack", username: "alice", credential: harness.clonePAT, wantStatus: http.StatusUnauthorized},
		{name: "write PAT writes", path: "git-receive-pack", username: "alice", credential: harness.writePAT, wantStatus: http.StatusOK, wantGitCall: true},
		{name: "write PAT reads", path: "git-upload-pack", username: "alice", credential: harness.writePAT, wantStatus: http.StatusOK, wantGitCall: true},
		{name: "invalid PAT", path: "git-upload-pack", username: "alice", credential: "wrong", wantStatus: http.StatusUnauthorized},
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
		if err := harness.db.Model(&identitymodel.PersonalAccessToken{}).Where("name = ?", "clone").Update("revoked_at", now).Error; err != nil {
			t.Fatal(err)
		}
		harness.assertDeniedWithoutGit(t, harness.clonePAT)
	})

	t.Run("expired key is rejected", func(t *testing.T) {
		key := harness.createToken(t, "expired", identitymodel.TokenPresetGitClone, timePointer(time.Now().Add(-time.Minute)), nil)
		harness.assertDeniedWithoutGit(t, key)
	})

	t.Run("disabled user invalidates password and key", func(t *testing.T) {
		if err := harness.db.Model(&identitymodel.User{}).Where("id = ?", harness.userID).Update("status", identitymodel.UserStatusDisabled).Error; err != nil {
			t.Fatal(err)
		}
		for _, credential := range []string{harness.password, harness.writePAT} {
			harness.assertDeniedWithoutGit(t, credential)
		}
	})
}

func TestProductionAPIKeyRealGitPushAndClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is unavailable")
	}
	harness := newProductionSmartHTTPHarness(t, false)
	key := harness.createToken(t, "git-client", identitymodel.TokenPresetGitWrite, nil, nil)
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
	run(source, "config", "commit.gpgSign", "false")
	run(source, "config", "tag.gpgSign", "false")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("production auth\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		".claude-plugin/plugin.json": `{"name":"plugin-one","description":"Production Git fixture","version":"1.0.0"}`,
		"skills/example/SKILL.md":    "---\nname: example\ndescription: Production Git test skill\n---\nExplain the test fixture.\n",
	} {
		filename := filepath.Join(source, path)
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(source, "add", ".")
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
	t.Setenv(identitymodel.APIKeyPepperEnvironment, identitymodel.EncodeAPIKeyPepper(pepper))
	t.Setenv(identitymodel.JWTSecretEnvironment, "production-git-integration-test-jwt-secret")
	password := "correct horse battery staple"
	passwordHash, err := auth.HashPassword(password, auth.Argon2idParams{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, HashLength: 16})
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.NewString()
	if err := db.Create(&identitymodel.User{ID: userID, Username: "alice", DisplayName: "Alice", Status: identitymodel.UserStatusActive, PasswordHash: passwordHash}).Error; err != nil {
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
	if err := db.Create(&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindUser, Slug: "alice", DisplayName: "Alice", OwnerUserID: &ownerID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := backendModule.Load(&hub); err != nil {
		t.Fatal(err)
	}
	login := httptest.NewRecorder()
	loginBody, err := json.Marshal(map[string]string{"username": "alice", "password": password})
	if err != nil {
		t.Fatal(err)
	}
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(loginBody)))
	loginRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(login, loginRequest)
	var session struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if login.Code != http.StatusOK || json.Unmarshal(login.Body.Bytes(), &session) != nil || session.Data.Token == "" {
		t.Fatalf("fixture login failed: status=%d", login.Code)
	}
	create := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/alice/plugins", strings.NewReader(`{"name":"plugin-one","visibility":"private"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Authorization", "Bearer "+session.Data.Token)
	engine.ServeHTTP(create, createRequest)
	if create.Code != http.StatusCreated {
		t.Fatalf("fixture Plugin creation failed: status=%d", create.Code)
	}
	resolved, err := backendModule.Resolve(t.Context(), "alice", "plugin-one")
	if err != nil {
		t.Fatalf("resolve fixture Plugin: %v", err)
	}
	repositoryID := resolved.ID
	if _, err := uuid.Parse(repositoryID); err != nil {
		t.Fatal("production Plugin resolver did not return a UUID")
	}
	if resolved.NamespaceID != namespaceID || resolved.Status != gitservice.StatusReady {
		t.Fatalf("fixture resolution has expected namespace=%t, status=%q", resolved.NamespaceID == namespaceID, resolved.Status)
	}
	if err := gitModule.Load(&hub); err != nil {
		t.Fatal(err)
	}

	harness := &productionSmartHTTPHarness{engine: engine, db: db, fake: fake, password: password, repoID: repositoryID, userID: userID}
	harness.subReadPAT = harness.createToken(t, "subscription", identitymodel.TokenPresetSubscriptionRead, nil, nil)
	harness.clonePAT = harness.createToken(t, "clone", identitymodel.TokenPresetGitClone, nil, nil)
	harness.writePAT = harness.createToken(t, "write", identitymodel.TokenPresetGitWrite, nil, nil)
	return harness
}

func (harness *productionSmartHTTPHarness) createToken(t *testing.T, name, preset string, expiresAt, revokedAt *time.Time) string {
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
	token := identitymodel.PersonalAccessToken{ID: tokenID, UserID: harness.userID, Name: name, Preset: preset, SecretPlaintext: plaintext, SecretHMAC: index, ExpiresAt: expiresAt, RevokedAt: revokedAt}
	if expiresAt != nil {
		token.CreatedAt = expiresAt.Add(-time.Minute)
	}
	if err := harness.db.Create(&token).Error; err != nil {
		t.Fatal(err)
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
