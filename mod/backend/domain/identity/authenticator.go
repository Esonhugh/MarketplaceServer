package identity

import (
	"context"
	"errors"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

var ErrInvalidCredentials = errors.New("identity: invalid credentials")

type BasicAuthenticator struct {
	repository *Repository
	pepper     []byte
	now        func() time.Time
}

func NewBasicAuthenticator(repository *Repository, environment Environment) (*BasicAuthenticator, error) {
	if repository == nil {
		return nil, errors.New("identity authenticator requires repository")
	}
	if environment == nil {
		environment = OSEnvironment{}
	}
	pepper, err := APIKeyPepper(environment)
	if err != nil {
		return nil, err
	}
	return &BasicAuthenticator{repository: repository, pepper: append([]byte(nil), pepper...), now: time.Now}, nil
}

func (authenticator *BasicAuthenticator) AuthenticateBasic(ctx context.Context, username, credential string) (auth.Principal, error) {
	if auth.HasAPIKeyPrefix(credential) {
		return authenticator.authenticateAPIKey(ctx, username, credential)
	}
	return authenticator.authenticatePassword(ctx, username, credential)
}

func (authenticator *BasicAuthenticator) authenticatePassword(ctx context.Context, username, password string) (auth.Principal, error) {
	user, err := authenticator.repository.FindUserByUsername(ctx, username)
	if err != nil || user.Status != UserStatusActive {
		return auth.Principal{}, ErrInvalidCredentials
	}
	valid, err := auth.VerifyPassword(password, user.PasswordHash)
	if err != nil || !valid {
		return auth.Principal{}, ErrInvalidCredentials
	}
	principal, err := auth.NewUserPrincipal(user.ID, user.Username, auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	return principal, nil
}

func (authenticator *BasicAuthenticator) authenticateAPIKey(ctx context.Context, username, plaintext string) (auth.Principal, error) {
	secretHMAC, err := auth.IndexAPIKey(plaintext, authenticator.pepper)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	token, err := authenticator.repository.FindTokenBySecretHMAC(ctx, secretHMAC)
	if err != nil || token.User.Status != UserStatusActive || token.User.Username != normalizeUsername(username) {
		return auth.Principal{}, ErrInvalidCredentials
	}
	now := authenticator.now().UTC()
	if token.RevokedAt != nil || token.ExpiresAt != nil && !token.ExpiresAt.After(now) {
		return auth.Principal{}, ErrInvalidCredentials
	}
	scopes, err := tokenScopes(token.Scopes)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	principal, err := auth.NewUserPrincipal(token.User.ID, token.User.Username, auth.CredentialAPIKey, scopes)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	return principal, nil
}

var _ auth.BasicAuthenticator = (*BasicAuthenticator)(nil)
