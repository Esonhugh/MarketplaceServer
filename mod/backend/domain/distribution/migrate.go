package distribution

import (
	"fmt"

	"gorm.io/gorm"
)

var migrationModels = []any{
	&Namespace{},
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

func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate distribution models: nil database")
	}
	if err := db.AutoMigrate(migrationModels...); err != nil {
		return fmt.Errorf("migrate distribution models: %w", err)
	}
	return nil
}
