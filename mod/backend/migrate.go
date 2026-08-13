package backend

import (
	"fmt"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate backend models: nil database")
	}
	if err := identitydao.Migrate(db); err != nil {
		return fmt.Errorf("migrate backend identity dependencies: %w", err)
	}
	if err := distributiondomain.Migrate(db); err != nil {
		return fmt.Errorf("migrate backend distribution models: %w", err)
	}
	return nil
}
