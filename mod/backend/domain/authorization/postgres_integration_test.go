package authorization

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const authorizationPostgresDSNEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"

func TestPostgresGORMIdentityStateReaderPluginFacts(t *testing.T) {
	db := openAuthorizationPostgres(t)
	user := identitymodel.User{ID: uuid.NewString(), Username: "alice", DisplayName: "Alice", Status: identitymodel.UserStatusActive, PasswordHash: "not-used"}
	ownerID := user.ID
	namespace := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindUser, Slug: "alice", DisplayName: "Alice", OwnerUserID: &ownerID}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatal(err)
	}
	groups := []identitymodel.SystemGroup{
		{ID: identitymodel.AdminSystemGroupID, Name: identitymodel.AdminSystemGroupName, Description: "Global system administrators"},
		{ID: identitymodel.DefaultSystemGroupID, Name: identitymodel.DefaultSystemGroupName, Description: "All active users"},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&identitymodel.UserGroupMembership{UserID: user.ID, GroupID: identitymodel.AdminSystemGroupID}).Error; err != nil {
		t.Fatal(err)
	}
	pluginID := uuid.NewString()
	insertAuthorizationPlugin(t, db, pluginID, namespace.ID, "public", "active", "ready")

	identities, err := identitydao.NewRepository(db)
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
	resource := auth.ResourceRef{Type: auth.ResourcePlugin, ID: pluginID, NamespaceID: namespace.ID}
	state, err := reader.ReadAuthorizationState(context.Background(), principal, resource)
	if err != nil {
		t.Fatalf("ReadAuthorizationState: %v", err)
	}
	if !state.Active || !state.SystemAdmin || !state.OwnsPersonalNamespace || state.OwnsResource {
		t.Fatalf("identity state = %+v", state)
	}
	wantFacts := auth.PluginAuthorizationFacts{
		Visibility:       auth.PluginVisibilityPublic,
		Status:           auth.PluginStatusActive,
		RepositoryStatus: auth.RepositoryOperationalReady,
	}
	if state.Plugin != wantFacts {
		t.Fatalf("Plugin facts = %+v, want %+v", state.Plugin, wantFacts)
	}

	if _, err := reader.ReadAuthorizationState(context.Background(), principal, auth.ResourceRef{Type: auth.ResourcePlugin, ID: pluginID, NamespaceID: uuid.NewString()}); !errorsIsIdentityUnknown(err) {
		t.Fatalf("tenant-mismatched Plugin lookup error = %v, want identity unknown", err)
	}
}

func TestPostgresGORMIdentityStateReaderAnonymousPluginFacts(t *testing.T) {
	db := openAuthorizationPostgres(t)
	namespace := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "public", DisplayName: "Public"}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatal(err)
	}
	pluginID := uuid.NewString()
	insertAuthorizationPlugin(t, db, pluginID, namespace.ID, "public", "archived", "readOnly")

	identities, err := identitydao.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewGORMIdentityStateReader(db, identities)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reader.ReadAuthorizationState(context.Background(), auth.AnonymousPrincipal(), auth.ResourceRef{Type: auth.ResourcePlugin, ID: pluginID, NamespaceID: namespace.ID})
	if err != nil || state.Active || state.SystemAdmin {
		t.Fatalf("anonymous state = %+v, %v", state, err)
	}
	wantFacts := auth.PluginAuthorizationFacts{
		Visibility:       auth.PluginVisibilityPublic,
		Status:           auth.PluginStatusArchived,
		RepositoryStatus: auth.RepositoryOperationalReadOnly,
	}
	if state.Plugin != wantFacts {
		t.Fatalf("Plugin facts = %+v, want %+v", state.Plugin, wantFacts)
	}
}

func insertAuthorizationPlugin(t *testing.T, db *gorm.DB, pluginID, namespaceID, visibility, status, repositoryStatus string) {
	t.Helper()
	if err := db.Exec("INSERT INTO repositories (id, status) VALUES (?, ?)", pluginID, repositoryStatus).Error; err != nil {
		t.Fatalf("insert hidden repository: %v", err)
	}
	if err := db.Exec("INSERT INTO plugins (id, namespace_id, visibility, status) VALUES (?, ?, ?, ?)", pluginID, namespaceID, visibility, status).Error; err != nil {
		t.Fatalf("insert Plugin: %v", err)
	}
}

func errorsIsIdentityUnknown(err error) bool {
	return err == ErrIdentityUnknown
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
	if err := identitydao.Migrate(db); err != nil {
		t.Fatalf("migrate identity models: %v", err)
	}
	if err := db.Exec(`CREATE TABLE repositories (id text PRIMARY KEY, status text NOT NULL)`).Error; err != nil {
		t.Fatalf("create hidden repositories table: %v", err)
	}
	if err := db.Exec(`CREATE TABLE plugins (id text PRIMARY KEY, namespace_id text NOT NULL, visibility text NOT NULL, status text NOT NULL)`).Error; err != nil {
		t.Fatalf("create Plugins table: %v", err)
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
