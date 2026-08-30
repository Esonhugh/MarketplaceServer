package dao

import (
	"context"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLiteMigrationAndBootstrap(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() returned error: %v", err)
	}

	bootstrapper, err := NewBootstrapper(db, validSQLiteEnvironment())
	if err != nil {
		t.Fatalf("NewBootstrapper() returned error: %v", err)
	}
	bootstrapper.params = auth.Argon2idParams{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, HashLength: 32}
	if err := bootstrapper.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() returned error: %v", err)
	}

	var users int64
	if err := db.Model(&model.User{}).Count(&users).Error; err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 1 {
		t.Fatalf("user count = %d, want 1", users)
	}
}

func validSQLiteEnvironment() model.MapEnvironment {
	return model.MapEnvironment{
		model.APIKeyPepperEnvironment:           model.EncodeAPIKeyPepper([]byte("0123456789abcdef0123456789abcdef")),
		model.BootstrapAdminPasswordEnvironment: "sqlite-development-password",
	}
}
