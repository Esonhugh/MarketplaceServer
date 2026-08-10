package identity

import (
	"context"
	"fmt"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"gorm.io/gorm"
)

type Services struct {
	Repository    *Repository
	Authenticator auth.BasicAuthenticator
}

func Initialize(ctx context.Context, db *gorm.DB, environment Environment) (Services, error) {
	repository, err := NewRepository(db)
	if err != nil {
		return Services{}, err
	}
	bootstrapper, err := NewBootstrapper(db, environment)
	if err != nil {
		return Services{}, err
	}
	if err := bootstrapper.Bootstrap(ctx); err != nil {
		return Services{}, fmt.Errorf("bootstrap identity: %w", err)
	}
	authenticator, err := NewBasicAuthenticator(repository, environment)
	if err != nil {
		return Services{}, fmt.Errorf("create basic authenticator: %w", err)
	}
	return Services{Repository: repository, Authenticator: authenticator}, nil
}
