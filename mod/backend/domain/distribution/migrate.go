package distribution

import (
	"fmt"

	"gorm.io/gorm"
)

var migrationModels = []any{
	&Repository{},
	&Plugin{},
	&PluginVersion{},
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
	if !db.Migrator().HasTable("namespaces") {
		return fmt.Errorf("migrate distribution models: identity namespaces table is missing")
	}
	if err := db.AutoMigrate(migrationModels...); err != nil {
		return fmt.Errorf("migrate distribution models: %w", err)
	}
	return nil
}
