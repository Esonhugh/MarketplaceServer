package service

import (
	"context"
	"fmt"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"gorm.io/gorm"
)

type Services struct {
	Repository      *dao.Repository
	Account         *AccountService
	Login           *LoginService
	JWT             *JWTService
	PAT             *PATAuthenticator
	GitPAT          auth.GitPATAuthenticator
	SubscriptionPAT auth.SubscriptionPATAuthenticator
}

func Initialize(ctx context.Context, db *gorm.DB, environment model.Environment, jwtSecret string) (Services, error) {
	jwtService, err := NewJWTService(jwtSecret)
	if err != nil {
		return Services{}, fmt.Errorf("create JWT service: %w", err)
	}
	repository, err := dao.NewRepository(db)
	if err != nil {
		return Services{}, err
	}
	bootstrapper, err := dao.NewBootstrapper(db, environment)
	if err != nil {
		return Services{}, err
	}
	if err := bootstrapper.Bootstrap(ctx); err != nil {
		return Services{}, fmt.Errorf("bootstrap identity: %w", err)
	}
	account, err := NewAccountService(repository)
	if err != nil {
		return Services{}, fmt.Errorf("create account service: %w", err)
	}
	pat, err := NewPATAuthenticator(repository, environment)
	if err != nil {
		return Services{}, fmt.Errorf("create PAT authenticator: %w", err)
	}
	return Services{
		Repository: repository, Account: account, Login: NewLoginService(account, jwtService), JWT: jwtService, PAT: pat,
		GitPAT: pat, SubscriptionPAT: pat,
	}, nil
}
