package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const postgresTestDSNEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"

var fastIntegrationPasswordParams = auth.Argon2idParams{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, HashLength: 16}

func TestPostgresPATAuthenticatorRejectsPasswordAndEnforcesPreset(t *testing.T) {
	db := openIdentityPostgres(t)
	user := integrationTestUser(t, "alice", "correct password")
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	pepper := []byte("0123456789abcdef0123456789abcdef")
	repository, err := dao.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := NewPATAuthenticator(repository, validIntegrationEnvironment(pepper))
	if err != nil {
		t.Fatalf("NewPATAuthenticator: %v", err)
	}
	fixedNow := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	authenticator.now = func() time.Time { return fixedNow }

	for _, password := range []string{"correct password", "wrong", "mpsk_bad"} {
		if _, err := authenticator.AuthenticateGitPAT(context.Background(), "alice", password, auth.GitOperationRead); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("invalid credential %q error = %v", password, err)
		}
	}

	key, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	index, err := auth.IndexAPIKey(key, pepper)
	if err != nil {
		t.Fatalf("IndexAPIKey: %v", err)
	}
	token := model.PersonalAccessToken{ID: uuid.NewString(), UserID: user.ID, Name: "test", Preset: model.TokenPresetGitClone, SecretPlaintext: key, SecretHMAC: index}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	principal, err := authenticator.AuthenticateGitPAT(context.Background(), "alice", key, auth.GitOperationRead)
	if err != nil || principal.CredentialKind() != auth.CredentialPAT || !principal.Allows(auth.ActionRepositoryRead) || principal.Allows(auth.ActionRepositoryWrite) {
		t.Fatalf("PAT principal = %#v, %v", principal, err)
	}
	if _, err := authenticator.AuthenticateGitPAT(context.Background(), "alice", key, auth.GitOperationWrite); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("clone PAT write error = %v", err)
	}
	var authenticatedToken model.PersonalAccessToken
	if err := db.Where("id = ?", token.ID).Take(&authenticatedToken).Error; err != nil {
		t.Fatal(err)
	}
	if authenticatedToken.LastUsedAt == nil || !authenticatedToken.LastUsedAt.Equal(fixedNow) {
		t.Fatalf("API key authentication last_used_at = %v", authenticatedToken.LastUsedAt)
	}

	revokedAt := fixedNow.Add(-time.Minute)
	if err := db.Model(&token).Update("revoked_at", revokedAt).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.AuthenticateGitPAT(context.Background(), "alice", key, auth.GitOperationRead); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked token error = %v", err)
	}
}

func openIdentityPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(postgresTestDSNEnvironment)
	if dsn == "" {
		t.Skip(postgresTestDSNEnvironment + " is not configured; skipping PostgreSQL identity integration test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	schema := "marketplace_identity_service_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error })
	db, err := gorm.Open(postgres.Open(identityPostgresDSNWithSearchPath(t, dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration schema: %v", err)
	}
	if err := dao.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func identityPostgresDSNWithSearchPath(t *testing.T, dsn, schema string) string {
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

func integrationTestUser(t *testing.T, username, password string) model.User {
	t.Helper()
	hash, err := auth.HashPassword(password, fastIntegrationPasswordParams)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return model.User{ID: uuid.NewString(), Username: model.NormalizeUsername(username), DisplayName: username, Status: model.UserStatusActive, PasswordHash: hash}
}

func validIntegrationEnvironment(pepper []byte) model.MapEnvironment {
	return model.MapEnvironment{
		model.APIKeyPepperEnvironment: model.EncodeAPIKeyPepper(pepper),
		model.JWTSecretEnvironment:    "test-jwt-secret",
	}
}
