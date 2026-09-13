package dao

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const postgresTestDSNEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"

var fastPasswordParams = auth.Argon2idParams{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, HashLength: 16}

func TestPostgresIdentityConstraints(t *testing.T) {
	db := openIdentityPostgres(t)
	if err := ensureSystemGroups(db); err != nil {
		t.Fatalf("ensure groups: %v", err)
	}
	user := testUser(t, "postgres-user", "password")
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	duplicate := testUser(t, user.Username, "other")
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate username was accepted")
	}

	ownerID := user.ID
	namespace := model.Namespace{ID: uuid.NewString(), Kind: model.NamespaceKindUser, Slug: user.Username, DisplayName: user.DisplayName, OwnerUserID: &ownerID}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	secondNamespace := model.Namespace{ID: uuid.NewString(), Kind: model.NamespaceKindUser, Slug: user.Username + "-2", DisplayName: user.DisplayName, OwnerUserID: &ownerID}
	if err := db.Create(&secondNamespace).Error; err == nil {
		t.Fatal("second personal namespace owner row was accepted")
	}
	if err := db.Create(&model.UserGroupMembership{UserID: user.ID, GroupID: model.DefaultSystemGroupID}).Error; !errors.Is(err, model.ErrPersistedDefaultMembership) {
		t.Fatalf("persisted default group membership error = %v", err)
	}
	if err := db.Exec("INSERT INTO user_group_memberships (user_id, group_id, created_at) VALUES (?, ?, ?)", user.ID, model.DefaultSystemGroupID, time.Now().UTC()).Error; err == nil {
		t.Fatal("database accepted default membership when GORM hooks were bypassed")
	}
	if err := db.Exec("INSERT INTO users (id, username, display_name, status, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)", uuid.NewString(), "UPPER", "Upper", model.UserStatusActive, user.PasswordHash, time.Now().UTC(), time.Now().UTC()).Error; err == nil {
		t.Fatal("database accepted non-canonical username when GORM hooks were bypassed")
	}
	if err := db.Exec("UPDATE users SET username = ? WHERE id = ?", "renamed", user.ID).Error; err == nil {
		t.Fatal("database accepted immutable username update when GORM hooks were bypassed")
	}
	if err := db.Exec("UPDATE namespaces SET slug = ? WHERE id = ?", "renamed", namespace.ID).Error; err == nil {
		t.Fatal("database accepted immutable namespace update when GORM hooks were bypassed")
	}
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	secretHMAC, err := auth.IndexAPIKey(plaintext, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	token := model.PersonalAccessToken{ID: uuid.NewString(), UserID: user.ID, Name: "postgres", Preset: model.TokenPresetGitClone, SecretPlaintext: plaintext, SecretHMAC: secretHMAC}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := db.Exec("INSERT INTO personal_access_tokens (id, user_id, name, preset, secret_plaintext, secret_hmac, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", uuid.NewString(), user.ID, "invalid", "unknown", "mpsk_invalid", "invalid-index", time.Now().UTC(), time.Now().UTC()).Error; err == nil {
		t.Fatal("database accepted unsupported token preset when GORM hooks were bypassed")
	}
	createdAt := time.Now().UTC()
	if err := db.Exec("INSERT INTO personal_access_tokens (id, user_id, name, preset, secret_plaintext, secret_hmac, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", uuid.NewString(), user.ID, "invalid-expiry", model.TokenPresetGitClone, plaintext, "different-"+secretHMAC, createdAt, createdAt, createdAt).Error; err == nil {
		t.Fatal("database accepted personal access token expiry equal to creation when GORM hooks were bypassed")
	}
	if err := db.Exec("UPDATE system_groups SET description = ? WHERE id = ?", "tampered", model.AdminSystemGroupID).Error; err == nil {
		t.Fatal("database accepted fixed group update when GORM hooks were bypassed")
	}
	if err := db.Exec("DELETE FROM system_groups WHERE id = ?", model.AdminSystemGroupID).Error; err == nil {
		t.Fatal("database accepted fixed group delete when GORM hooks were bypassed")
	}
}

func TestPostgresIdentityRepositorySemantics(t *testing.T) {
	db := openIdentityPostgres(t)
	user := testUser(t, "repository-user", "password")
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	member, err := repository.IsDefaultMember(context.Background(), user.ID)
	if err != nil || !member {
		t.Fatalf("active user default membership = %v, %v", member, err)
	}
	if err := db.Model(&user).Update("status", model.UserStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	member, err = repository.IsDefaultMember(context.Background(), user.ID)
	if err != nil || member {
		t.Fatalf("disabled user default membership = %v, %v", member, err)
	}

	now := time.Now().UTC()
	newToken := func(name string) model.PersonalAccessToken {
		plaintext, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		secretHMAC, err := auth.IndexAPIKey(plaintext, []byte("01234567890123456789012345678901"))
		if err != nil {
			t.Fatal(err)
		}
		return model.PersonalAccessToken{ID: uuid.NewString(), UserID: user.ID, Name: name, Preset: model.TokenPresetSubscriptionRead, SecretPlaintext: plaintext, SecretHMAC: secretHMAC}
	}
	revoked, expired := newToken("revoked"), newToken("expired")
	revoked.RevokedAt = &now
	// A valid token lifetime that has already ended at the query time.
	expiresAt := now.Add(-time.Minute)
	expired.CreatedAt = now.Add(-2 * time.Minute)
	expired.ExpiresAt = &expiresAt
	for name, token := range map[string]model.PersonalAccessToken{
		"revoked": revoked,
		"expired": expired,
	} {
		t.Run(name, func(t *testing.T) {
			if err := db.Create(&token).Error; err != nil {
				t.Fatal(err)
			}
			if err := repository.TouchToken(context.Background(), token.ID, now); !errors.Is(err, ErrIdentityNotFound) {
				t.Fatalf("TouchToken error = %v", err)
			}
		})
	}
}

func TestPostgresRejectsMismatchedFixedSystemGroupDefinition(t *testing.T) {
	db := openIdentityPostgres(t)
	if err := db.Exec("INSERT INTO system_groups (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", model.AdminSystemGroupID, model.AdminSystemGroupName, "tampered", time.Now().UTC(), time.Now().UTC()).Error; err == nil {
		t.Fatal("database accepted mismatched fixed group definition")
	}
}

func TestPostgresBootstrapRejectsReservedAPIKeyPassword(t *testing.T) {
	db := openIdentityPostgres(t)
	bootstrapper := newFastBootstrapper(t, db, validEnvironment("mpsk_reserved-password", []byte("0123456789abcdef0123456789abcdef")))
	if err := bootstrapper.Bootstrap(context.Background()); !errors.Is(err, ErrBootstrapAdminPasswordReserved) {
		t.Fatalf("Bootstrap error = %v", err)
	}
	assertBootstrapCounts(t, db, 0, 0, 0, 0)
}

func TestPostgresBootstrapFreshRepeatRollbackAndConcurrency(t *testing.T) {
	t.Run("fresh and repeat do not reset password", func(t *testing.T) {
		db := openIdentityPostgres(t)
		env := validEnvironment("initial password", []byte("0123456789abcdef0123456789abcdef"))
		bootstrapper := newFastBootstrapper(t, db, env)
		if err := bootstrapper.Bootstrap(context.Background()); err != nil {
			t.Fatalf("first Bootstrap: %v", err)
		}
		var user model.User
		if err := db.Take(&user).Error; err != nil {
			t.Fatal(err)
		}
		originalHash := user.PasswordHash
		delete(env, model.BootstrapAdminPasswordEnvironment)
		var hashCalls atomic.Int32
		bootstrapper.hash = func(string, auth.Argon2idParams) (string, error) {
			hashCalls.Add(1)
			return "should-not-be-used", nil
		}
		if err := bootstrapper.Bootstrap(context.Background()); err != nil {
			t.Fatalf("repeat Bootstrap without password: %v", err)
		}
		if hashCalls.Load() != 0 {
			t.Fatalf("repeat bootstrap hashed password %d times", hashCalls.Load())
		}
		if err := db.Take(&user).Error; err != nil || user.PasswordHash != originalHash {
			t.Fatalf("password hash changed = %v, %v", user.PasswordHash != originalHash, err)
		}
		assertBootstrapCounts(t, db, 1, 1, 2, 1)
	})

	t.Run("partial groups complete", func(t *testing.T) {
		db := openIdentityPostgres(t)
		if err := db.Create(&model.SystemGroup{ID: model.AdminSystemGroupID, Name: model.AdminSystemGroupName, Description: "Global system administrators"}).Error; err != nil {
			t.Fatal(err)
		}
		bootstrapper := newFastBootstrapper(t, db, validEnvironment("password", []byte("0123456789abcdef0123456789abcdef")))
		if err := bootstrapper.Bootstrap(context.Background()); err != nil {
			t.Fatalf("Bootstrap: %v", err)
		}
		assertBootstrapCounts(t, db, 1, 1, 2, 1)
	})

	t.Run("hash failure rolls back groups", func(t *testing.T) {
		db := openIdentityPostgres(t)
		const sensitivePassword = "super-secret-bootstrap-value"
		bootstrapper := newFastBootstrapper(t, db, validEnvironment(sensitivePassword, []byte("0123456789abcdef0123456789abcdef")))
		bootstrapper.hash = func(string, auth.Argon2idParams) (string, error) { return "", errors.New("hash failure") }
		if err := bootstrapper.Bootstrap(context.Background()); err == nil || strings.Contains(err.Error(), sensitivePassword) {
			t.Fatalf("Bootstrap error = %v", err)
		}
		assertBootstrapCounts(t, db, 0, 0, 0, 0)
	})

	t.Run("concurrent callers create one administrator", func(t *testing.T) {
		db := openIdentityPostgres(t)
		const callers = 8
		bootstrappers := make([]*Bootstrapper, callers)
		for i := range bootstrappers {
			bootstrappers[i] = newFastBootstrapper(t, db, validEnvironment("password", []byte("0123456789abcdef0123456789abcdef")))
		}
		start := make(chan struct{})
		errs := make(chan error, callers)
		var wg sync.WaitGroup
		for _, bootstrapper := range bootstrappers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- bootstrapper.Bootstrap(context.Background())
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent Bootstrap: %v", err)
			}
		}
		assertBootstrapCounts(t, db, 1, 1, 2, 1)
	})
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
	schema := "marketplace_identity_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error })
	db, err := gorm.Open(postgres.Open(identityPostgresDSNWithSearchPath(t, dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration schema: %v", err)
	}
	if err := Migrate(db); err != nil {
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

func testUser(t *testing.T, username, password string) model.User {
	t.Helper()
	hash, err := auth.HashPassword(password, fastPasswordParams)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return model.User{ID: uuid.NewString(), Username: model.NormalizeUsername(username), DisplayName: username, Status: model.UserStatusActive, PasswordHash: hash}
}

func validEnvironment(password string, pepper []byte) model.MapEnvironment {
	return model.MapEnvironment{
		model.BootstrapAdminPasswordEnvironment: password,
		model.APIKeyPepperEnvironment:           model.EncodeAPIKeyPepper(pepper),
		model.JWTSecretEnvironment:              "test-jwt-secret",
	}
}

func newFastBootstrapper(t *testing.T, db *gorm.DB, env model.Environment) *Bootstrapper {
	t.Helper()
	bootstrapper, err := NewBootstrapper(db, env)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapper.params = fastPasswordParams
	return bootstrapper
}

func assertBootstrapCounts(t *testing.T, db *gorm.DB, users, namespaces, groups, memberships int64) {
	t.Helper()
	for name, expectation := range map[string]struct {
		model any
		want  int64
	}{
		"users": {&model.User{}, users}, "namespaces": {&model.Namespace{}, namespaces},
		"groups": {&model.SystemGroup{}, groups}, "memberships": {&model.UserGroupMembership{}, memberships},
	} {
		var got int64
		if err := db.Model(expectation.model).Count(&got).Error; err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if got != expectation.want {
			t.Fatalf("%s count = %d, want %d", name, got, expectation.want)
		}
	}
}
