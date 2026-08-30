package service

import (
	"context"
	"errors"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

var ErrInvalidCredentials = errors.New("identity: invalid credentials")

type PATCredentialStore interface {
	FindTokenCredentialBySecretHMAC(context.Context, string) (dao.TokenCredentialRecord, error)
	TouchToken(context.Context, string, time.Time) error
}

type PATAuthenticator struct {
	repository PATCredentialStore
	pepper     []byte
	now        func() time.Time
}

func NewPATAuthenticator(repository PATCredentialStore, environment model.Environment) (*PATAuthenticator, error) {
	if repository == nil {
		return nil, errors.New("identity PAT authenticator requires repository")
	}
	if environment == nil {
		environment = model.OSEnvironment{}
	}
	pepper, err := model.APIKeyPepper(environment)
	if err != nil {
		return nil, err
	}
	return &PATAuthenticator{repository: repository, pepper: append([]byte(nil), pepper...), now: time.Now}, nil
}

func (authenticator *PATAuthenticator) AuthenticateGitPAT(ctx context.Context, username, plaintext string, operation auth.GitOperation) (auth.Principal, error) {
	record, err := authenticator.authenticate(ctx, username, plaintext)
	if err != nil {
		return auth.Principal{}, err
	}
	if operation == auth.GitOperationRead && record.Preset != model.TokenPresetGitClone && record.Preset != model.TokenPresetGitWrite ||
		operation == auth.GitOperationWrite && record.Preset != model.TokenPresetGitWrite {
		return auth.Principal{}, ErrInvalidCredentials
	}
	if operation != auth.GitOperationRead && operation != auth.GitOperationWrite {
		return auth.Principal{}, ErrInvalidCredentials
	}
	return authenticator.finish(ctx, record)
}

func (authenticator *PATAuthenticator) AuthenticateSubscriptionPAT(ctx context.Context, username, plaintext string) (auth.Principal, error) {
	record, err := authenticator.authenticate(ctx, username, plaintext)
	if err != nil {
		return auth.Principal{}, err
	}
	return authenticator.finish(ctx, record)
}

func (authenticator *PATAuthenticator) finish(ctx context.Context, record dao.TokenCredentialRecord) (auth.Principal, error) {
	principal, err := principalForPAT(record)
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	if err := authenticator.repository.TouchToken(ctx, record.ID, authenticator.now().UTC()); err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	return principal, nil
}

func (authenticator *PATAuthenticator) authenticate(ctx context.Context, username, plaintext string) (dao.TokenCredentialRecord, error) {
	secretHMAC, err := auth.IndexAPIKey(plaintext, authenticator.pepper)
	if err != nil {
		return dao.TokenCredentialRecord{}, ErrInvalidCredentials
	}
	record, err := authenticator.repository.FindTokenCredentialBySecretHMAC(ctx, secretHMAC)
	if err != nil || record.UserStatus != model.UserStatusActive || record.Username != normalizeUsername(username) || !model.IsSupportedPreset(record.Preset) {
		return dao.TokenCredentialRecord{}, ErrInvalidCredentials
	}
	now := authenticator.now().UTC()
	if record.RevokedAt != nil || record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
		return dao.TokenCredentialRecord{}, ErrInvalidCredentials
	}
	return record, nil
}

func principalForPAT(record dao.TokenCredentialRecord) (auth.Principal, error) {
	actions := []auth.Action{auth.ActionMarketplaceRead, auth.ActionPluginRead}
	if record.Preset == model.TokenPresetGitWrite {
		actions = append(actions, auth.ActionPluginWrite)
	}
	principal, err := auth.NewUserPrincipal(record.UserID, record.Username, auth.CredentialPAT, auth.RestrictedScopes(actions...))
	if err != nil {
		return auth.Principal{}, ErrInvalidCredentials
	}
	return principal, nil
}

var (
	_ auth.GitPATAuthenticator          = (*PATAuthenticator)(nil)
	_ auth.SubscriptionPATAuthenticator = (*PATAuthenticator)(nil)
)
