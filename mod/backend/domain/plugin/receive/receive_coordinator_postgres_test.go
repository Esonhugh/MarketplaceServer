package receive

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresCoordinatorUsesSessionPluginAdvisoryLock(t *testing.T) {
	const dsnEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"
	dsn := os.Getenv(dsnEnvironment)
	if dsn == "" {
		t.Skip(dsnEnvironment + " is not configured; skipping PostgreSQL ReceiveCoordinator lock test")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	schema := "marketplace_receive_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error; err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})
	open := func() *gorm.DB {
		db, openErr := gorm.Open(postgres.Open(receivePostgresDSNWithSearchPath(t, dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if openErr != nil {
			t.Fatal(openErr)
		}
		return db
	}
	firstDB := open()
	if err := identitydao.Migrate(firstDB); err != nil {
		t.Fatal(err)
	}
	if err := plugindomain.Migrate(firstDB); err != nil {
		t.Fatal(err)
	}
	if err := distributiondomain.Migrate(firstDB); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	namespaceID := uuid.NewString()
	pluginID := uuid.NewString()
	for _, record := range []any{
		&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security", CreatedAt: now, UpdatedAt: now},
		&plugindomain.Plugin{ID: pluginID, NamespaceID: namespaceID, Slug: "scanner", Visibility: plugindomain.VisibilityPublic, Status: plugindomain.PluginStatusActive, CreatedAt: now, UpdatedAt: now},
		&plugindomain.Repository{ID: pluginID, StorageKey: uuid.NewString(), Status: plugindomain.RepositoryStatusReady, CreatedAt: now, UpdatedAt: now},
	} {
		if err := firstDB.Create(record).Error; err != nil {
			t.Fatalf("create fixture %T: %v", record, err)
		}
	}
	first, err := NewCoordinator(firstDB, &fakeProjectionBuilder{}, Options{PostgresLockTimeout: time.Second, LockRetryInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCoordinator(open(), &fakeProjectionBuilder{}, Options{PostgresLockTimeout: 75 * time.Millisecond, LockRetryInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	held, err := first.Open(context.Background(), pluginID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if competing, err := second.Open(context.Background(), pluginID, uuid.NewString()); err == nil || competing != nil {
		t.Fatalf("second Open() = %#v, %v; want bounded advisory-lock rejection", competing, err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	acquired, err := second.Open(context.Background(), pluginID, uuid.NewString())
	if err != nil {
		t.Fatalf("Open() after unlock error = %v", err)
	}
	if err := acquired.Close(); err != nil {
		t.Fatal(err)
	}
}

func receivePostgresDSNWithSearchPath(t *testing.T, dsn, schema string) string {
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
