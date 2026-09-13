package plugin

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const postgresTestDSNEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"

func TestPostgresPluginLifecycleConstraints(t *testing.T) {
	dsn := os.Getenv(postgresTestDSNEnvironment)
	if dsn == "" {
		t.Skip(postgresTestDSNEnvironment + " is not configured; skipping PostgreSQL Plugin persistence test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	schema := "marketplace_plugin_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error; err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})
	db, err := gorm.Open(postgres.Open(postgresDSNWithSearchPath(t, dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := identitydao.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	// Re-running migration must work with the guards installed, including
	// PostgreSQL's ALTER TYPE checks on columns referenced by trigger WHEN clauses.
	for range 2 {
		if err := Migrate(db); err != nil {
			t.Fatalf("repeat migration with installed guards: %v", err)
		}
	}

	results := make(chan error, 2)
	for range 2 {
		go func() { results <- Migrate(db) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent repeat migration: %v", err)
		}
	}

	now := time.Now().UTC()
	namespace := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security"}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatal(err)
	}
	pluginID := uuid.NewString()
	if err := db.Create(&Plugin{ID: pluginID, NamespaceID: namespace.ID, Slug: "scanner", Visibility: VisibilityPublic, Status: PluginStatusDraft, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Repository{ID: pluginID, StorageKey: uuid.NewString(), Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Repository{ID: uuid.NewString(), StorageKey: uuid.NewString(), Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now}).Error; err == nil {
		t.Fatal("PostgreSQL accepted an orphan Repository")
	}
	if err := db.Exec("UPDATE plugins SET slug = ? WHERE namespace_id = ? AND id = ?", "renamed", namespace.ID, pluginID).Error; err == nil {
		t.Fatal("PostgreSQL accepted immutable Plugin slug update")
	}
	rollback := errors.New("simulate interrupted migration")
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := Migrate(tx); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("migration rollback: %v", err)
	}
	if err := db.Exec("UPDATE plugins SET slug = ? WHERE id = ?", "after-rollback", pluginID).Error; err == nil {
		t.Fatal("migration rollback lost immutable identity guard")
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if err := SetDefaultVersion(t.Context(), db, namespace.ID, pluginID, "v1.0.0"); !errors.Is(err, ErrVersionNotAvailable) {
		t.Fatalf("missing default Version error = %v", err)
	}
}

func postgresDSNWithSearchPath(t *testing.T, dsn, schema string) string {
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
