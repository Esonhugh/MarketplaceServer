package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

type AccountStore interface {
	FindUserByUsername(context.Context, string) (model.User, error)
}

const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$bWFya2V0cGxhY2UtZHVtbXk$k7Ts/CF78xXP2lka6aDYD6QmLPmoeWqksbOOOAVlsCI"

type AccountService struct {
	repository     AccountStore
	verifyPassword func(string, string) (bool, error)
}

type LoginResult struct {
	Username  string
	Token     string
	ExpiresAt time.Time
}

type LoginService struct {
	account *AccountService
	jwt     *JWTService
}

func NewLoginService(account *AccountService, jwtService *JWTService) *LoginService {
	return &LoginService{account: account, jwt: jwtService}
}

func (service *LoginService) Login(ctx context.Context, username, password string) (LoginResult, error) {
	if service == nil || service.account == nil || service.jwt == nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	user, err := service.account.AuthenticatePassword(ctx, username, password)
	if err != nil {
		return LoginResult{}, err
	}
	token, expiresAt, err := service.jwt.Issue(user.Username)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Username: user.Username, Token: token, ExpiresAt: expiresAt}, nil
}

func NewAccountService(repository AccountStore) (*AccountService, error) {
	if repository == nil {
		return nil, errors.New("identity account service requires repository")
	}
	return &AccountService{repository: repository, verifyPassword: auth.VerifyPassword}, nil
}

func (service *AccountService) AuthenticatePassword(ctx context.Context, username, password string) (model.User, error) {
	user, err := service.repository.FindUserByUsername(ctx, username)
	if err != nil && !errors.Is(err, dao.ErrIdentityNotFound) {
		return model.User{}, fmt.Errorf("authenticate password: %w", err)
	}

	passwordHash := user.PasswordHash
	eligible := err == nil && user.Status == model.UserStatusActive
	if !eligible {
		passwordHash = dummyPasswordHash
	}
	valid, verifyErr := service.verifyPassword(password, passwordHash)
	if verifyErr != nil || !valid || !eligible {
		return model.User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (service *AccountService) VerifyPassword(ctx context.Context, username, password string) error {
	_, err := service.AuthenticatePassword(ctx, username, password)
	return err
}

func (service *AccountService) ResolveJWT(ctx context.Context, jwtService *JWTService, encoded string) (auth.Principal, error) {
	if jwtService == nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	username, err := jwtService.Verify(encoded)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	return service.ResolveJWTPrincipal(ctx, username)
}

func (service *AccountService) ResolveJWTPrincipal(ctx context.Context, username string) (auth.Principal, error) {
	user, err := service.repository.FindUserByUsername(ctx, username)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	principal, err := auth.NewUserPrincipal(user.ID, user.Username, auth.CredentialJWT, auth.UnrestrictedScopes())
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	return principal, nil
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
