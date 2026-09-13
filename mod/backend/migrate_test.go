package backend

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/migration"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
)

func TestAggregateMigrationDependenciesAreIdentityFirst(t *testing.T) {
	identityNamespace := reflect.TypeOf(&identitymodel.Namespace{})
	distributionModels := distributiondomain.MigrationModels()
	for _, model := range distributionModels {
		if reflect.TypeOf(model) == identityNamespace {
			t.Fatal("distribution migration models duplicate canonical identity Namespace")
		}
	}
	identityModels := identitydao.MigrationModels()
	if got := reflect.TypeOf(identityModels[0]); got != reflect.TypeOf(&identitymodel.User{}) {
		t.Fatalf("first identity model = %v", got)
	}
	pluginModels := plugindomain.MigrationModels()
	if got := reflect.TypeOf(pluginModels[0]); got != reflect.TypeOf(&plugindomain.Plugin{}) {
		t.Fatalf("first Plugin lifecycle model = %v", got)
	}
	if got := reflect.TypeOf(distributionModels[0]); got != reflect.TypeOf(&distributiondomain.MarketplaceTemplate{}) {
		t.Fatalf("first dependent distribution model = %v", got)
	}
}

func newPostgresBackendMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MARKETPLACE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MARKETPLACE_TEST_POSTGRES_DSN is not configured")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })
	schema := "marketplace_backend_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`).Error; err != nil {
			t.Error(err)
		}
	})
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestPostgresBackendConcurrentMigrate(t *testing.T) {
	db := newPostgresBackendMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	for _, phase := range []string{"fresh", "repeat"} {
		t.Run(phase, func(t *testing.T) {
			start := make(chan struct{})
			results := make(chan error, 4)
			for range 4 {
				go func() { <-start; results <- Migrate(db) }()
			}
			close(start)
			for range 4 {
				if err := <-results; err != nil {
					t.Errorf("concurrent backend migration: %v", err)
				}
			}
		})
	}
	namespace := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "migration-team", DisplayName: "Migration team"}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatal(err)
	}
	project := plugindomain.Plugin{ID: uuid.NewString(), NamespaceID: namespace.ID, Slug: "migration-plugin", Visibility: "public", Status: plugindomain.PluginStatusDraft}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	template := distributiondomain.MarketplaceTemplate{ID: uuid.NewString(), NamespaceID: namespace.ID, Slug: "migration-marketplace", Name: "Migration", Visibility: "public", Status: distributiondomain.StatusActive}
	if err := db.Create(&template).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("populated backend migration: %v", err)
	}
	if err := db.Model(&namespace).Update("slug", "changed").Error; err == nil {
		t.Error("identity immutable guard missing")
	}
	if err := db.Model(&project).Update("slug", "changed").Error; err == nil {
		t.Error("plugin immutable guard missing")
	}
	if err := db.Model(&template).Update("namespace_id", uuid.NewString()).Error; err == nil {
		t.Error("distribution namespace foreign key missing")
	}
}

func TestPostgresBackendMigrationLockCleanup(t *testing.T) {
	db := newPostgresBackendMigrationDB(t)
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	sentinel := errors.New("migration callback failed")
	for _, mode := range []string{"error", "cancel", "unlock-failure", "panic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var pid int
			run := func() error {
				return migration.WithBackendLock(db.WithContext(ctx), func(locked *gorm.DB) error {
					if err := locked.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
						return err
					}
					if err := locked.Transaction(func(tx *gorm.DB) error {
						var txPID int
						if err := tx.Raw("SELECT pg_backend_pid()").Scan(&txPID).Error; err != nil {
							return err
						}
						if txPID != pid {
							return errors.New("transaction escaped reserved connection")
						}
						return nil
					}); err != nil {
						return err
					}
					switch mode {
					case "cancel":
						cancel()
					case "unlock-failure":
						if err := locked.Exec("SELECT pg_advisory_unlock_all()").Error; err != nil {
							return err
						}
					case "panic":
						panic(sentinel)
					}
					return sentinel
				})
			}
			if mode == "panic" {
				func() {
					defer func() {
						if got := recover(); got != sentinel {
							t.Errorf("panic = %v", got)
						}
					}()
					_ = run()
					t.Error("callback did not panic")
				}()
			} else {
				err := run()
				if !errors.Is(err, sentinel) {
					t.Fatalf("callback error lost: %v", err)
				}
				if mode == "unlock-failure" && !strings.Contains(err.Error(), "unlock backend migration") {
					t.Fatalf("unlock failure lost: %v", err)
				}
			}
			var remaining int64
			if err := db.Raw("SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND pid = ?", pid).Scan(&remaining).Error; err != nil {
				t.Fatal(err)
			}
			if remaining != 0 {
				t.Fatalf("leaked %d advisory locks", remaining)
			}
			if pool.Stats().InUse != 0 {
				t.Fatal("reserved connection leaked")
			}
			var nextPID int
			if err := db.Raw("SELECT pg_backend_pid()").Scan(&nextPID).Error; err != nil {
				t.Fatal(err)
			}
			if mode == "unlock-failure" && nextPID == pid {
				t.Fatal("failed unlock recycled connection")
			}
			if err := Migrate(db); err != nil {
				t.Fatalf("retry after cleanup: %v", err)
			}
		})
	}
}

func TestPostgresBackendMigrationLockSerialization(t *testing.T) {
	db := newPostgresBackendMigrationDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err := migration.WithBackendLock(db.WithContext(ctx), func(locked *gorm.DB) error {
		// A second session cannot enter this schema's complete sequence. A canceled
		// waiter must not run its callback or retain a connection/lock.
		waitCtx, stop := context.WithTimeout(ctx, 150*time.Millisecond)
		defer stop()
		entered := false
		err := migration.WithBackendLock(db.WithContext(waitCtx), func(*gorm.DB) error { entered = true; return nil })
		if err == nil || entered || waitCtx.Err() == nil {
			return errors.New("same-schema migration was not serialized")
		}
		// Another schema is independent even while this sequence owns its lock.
		other := newPostgresBackendMigrationDB(t)
		return migration.WithBackendLock(other.WithContext(ctx), func(*gorm.DB) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db.WithContext(ctx)); err != nil {
		t.Fatalf("retry after canceled waiter: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return Migrate(tx) }); err == nil {
		t.Fatal("accepted outer transaction")
	}
}
