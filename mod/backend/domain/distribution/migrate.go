package distribution

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var migrationModels = []any{
	&MarketplaceTemplate{},
	&MarketplaceRevision{},
	&MarketplaceRevisionItem{},
	&MarketplaceDistribution{},
	&MarketplaceDistributionProjection{},
	&PluginDistribution{},
}

func MigrationModels() []any {
	return append([]any(nil), migrationModels...)
}

func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate distribution models: nil database")
	}
	driver := strings.ToLower(strings.TrimSpace(db.Dialector.Name()))
	if driver != "postgres" && driver != "sqlite" {
		return fmt.Errorf("migrate distribution models: unsupported database %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable("namespaces") {
		return fmt.Errorf("migrate distribution models: identity namespaces table is missing")
	}
	for _, table := range []string{"plugins", "repositories", "plugin_versions", "projection_artifacts", "revision_projection_pointers"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("migrate distribution models: plugin lifecycle table %s is missing", table)
		}
	}
	if err := db.AutoMigrate(migrationModels...); err != nil {
		return fmt.Errorf("migrate distribution models: %w", err)
	}
	return nil
}
