package backend

import (
	"fmt"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate backend models: nil database")
	}
	if err := identitydomain.Migrate(db); err != nil {
		return fmt.Errorf("migrate backend identity dependencies: %w", err)
	}
	if err := distributiondomain.Migrate(db); err != nil {
		return fmt.Errorf("migrate backend distribution models: %w", err)
	}
	return nil
}
