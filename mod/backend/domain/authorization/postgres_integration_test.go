package authorization

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const authorizationPostgresDSNEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"

func TestPostgresGORMIdentityStateReader(t *testing.T) {
	db := openAuthorizationPostgres(t)
	user := identitydomain.User{ID: uuid.NewString(), Username: "alice", DisplayName: "Alice", Status: identitydomain.UserStatusActive, PasswordHash: "not-used"}
	ownerID := user.ID
	namespace := identitydomain.Namespace{ID: uuid.NewString(), Kind: identitydomain.NamespaceKindUser, Slug: "alice", DisplayName: "Alice", OwnerUserID: &ownerID}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatal(err)
	}
	groups := []identitydomain.SystemGroup{
		{ID: identitydomain.AdminSystemGroupID, Name: identitydomain.AdminSystemGroupName, Description: "Global system administrators"},
		{ID: identitydomain.DefaultSystemGroupID, Name: identitydomain.DefaultSystemGroupName, Description: "All active users"},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&identitydomain.UserGroupMembership{UserID: user.ID, GroupID: identitydomain.AdminSystemGroupID}).Error; err != nil {
		t.Fatal(err)
	}
	repository := distributiondomain.Repository{ID: uuid.NewString(), NamespaceID: namespace.ID, Slug: "repo", Visibility: "public", Status: distributiondomain.RepositoryStatusReady, StorageKey: uuid.NewString()}
	if err := db.Create(&repository).Error; err != nil {
		t.Fatal(err)
	}

	identities, err := identitydomain.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewGORMIdentityStateReader(db, identities)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewUserPrincipal(user.ID, user.Username, auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	state, err := reader.ReadAuthorizationState(context.Background(), principal, auth.ResourceRef{Type: "repository", ID: repository.ID})
	if err != nil {
		t.Fatalf("ReadAuthorizationState: %v", err)
	}
	if !state.Active || !state.SystemAdmin || !state.OwnsPersonalNamespace || state.OwnsResource || !state.Public {
		t.Fatalf("state = %+v", state)
	}

	token := identitydomain.PersonalAccessToken{ID: uuid.NewString(), UserID: user.ID, Name: "token", SecretHMAC: "index"}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	state, err = reader.ReadAuthorizationState(context.Background(), principal, auth.ResourceRef{Type: "token", ID: token.ID, NamespaceID: "forged-namespace"})
	if err != nil || !state.OwnsResource || !state.OwnsPersonalNamespace {
		t.Fatalf("token state = %+v, %v", state, err)
	}
	state, err = reader.ReadAuthorizationState(context.Background(), principal, auth.ResourceRef{Type: "user", ID: user.ID, NamespaceID: "forged-namespace"})
	if err != nil || !state.OwnsResource || !state.OwnsPersonalNamespace {
		t.Fatalf("user state = %+v, %v", state, err)
	}
}

func TestPostgresGORMIdentityStateReaderAnonymousResourceVisibility(t *testing.T) {
	db := openAuthorizationPostgres(t)
	namespace := identitydomain.Namespace{ID: uuid.NewString(), Kind: identitydomain.NamespaceKindTeam, Slug: "public", DisplayName: "Public"}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatal(err)
	}
	repository := distributiondomain.Repository{ID: uuid.NewString(), NamespaceID: namespace.ID, Slug: "repo", Visibility: "public", Status: distributiondomain.RepositoryStatusReady, StorageKey: uuid.NewString()}
	if err := db.Create(&repository).Error; err != nil {
		t.Fatal(err)
	}
	marketplace := distributiondomain.MarketplaceTemplate{ID: uuid.NewString(), NamespaceID: namespace.ID, Slug: "marketplace", Name: "Marketplace", Visibility: "public", Status: distributiondomain.StatusActive}
	if err := db.Create(&marketplace).Error; err != nil {
		t.Fatal(err)
	}
	identities, err := identitydomain.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewGORMIdentityStateReader(db, identities)
	if err != nil {
		t.Fatal(err)
	}
	for name, resource := range map[string]auth.ResourceRef{
		"repository":  {Type: "repository", ID: repository.ID},
		"marketplace": {Type: "marketplace", ID: marketplace.ID},
	} {
		t.Run(name, func(t *testing.T) {
			state, err := reader.ReadAuthorizationState(context.Background(), auth.AnonymousPrincipal(), resource)
			if err != nil || !state.Public || state.Active || state.SystemAdmin {
				t.Fatalf("anonymous state = %+v, %v", state, err)
			}
		})
	}
	if err := db.Model(&marketplace).Update("status", distributiondomain.StatusRevoked).Error; err != nil {
		t.Fatal(err)
	}
	state, err := reader.ReadAuthorizationState(context.Background(), auth.AnonymousPrincipal(), auth.ResourceRef{Type: "marketplace", ID: marketplace.ID})
	if err != nil || state.Public {
		t.Fatalf("inactive public marketplace state = %+v, %v", state, err)
	}
}

func openAuthorizationPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(authorizationPostgresDSNEnvironment)
	if dsn == "" {
		t.Skip(authorizationPostgresDSNEnvironment + " is not configured; skipping PostgreSQL authorization integration test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	schema := "marketplace_authorization_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error })
	db, err := gorm.Open(postgres.Open(authorizationPostgresDSNWithSearchPath(t, dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration schema: %v", err)
	}
	if err := identitydomain.Migrate(db); err != nil {
		t.Fatalf("migrate identity models: %v", err)
	}
	if err := distributiondomain.Migrate(db); err != nil {
		t.Fatalf("migrate distribution models: %v", err)
	}
	return db
}

func authorizationPostgresDSNWithSearchPath(t *testing.T, dsn, schema string) string {
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
