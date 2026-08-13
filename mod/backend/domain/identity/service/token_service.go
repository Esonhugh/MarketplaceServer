package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
)

const (
	DefaultTokenListSize  = 20
	MaximumTokenListSize  = 100
	maximumTokenNameBytes = 128

	TokenStatusActive  = "active"
	TokenStatusExpired = "expired"
	TokenStatusRevoked = "revoked"
)

var (
	ErrInvalidTokenInput  = errors.New("identity: invalid personal access token input")
	ErrTokenNotFound      = errors.New("identity: personal access token not found")
	ErrTokenAuthorization = errors.New("identity: token authorization denied")
)

type TokenStore interface {
	CreateToken(context.Context, model.PersonalAccessToken) error
	ListTokenMetadata(context.Context, string, int, int) ([]dao.TokenMetadataRecord, int64, error)
	FindTokenMetadataByIDAndUserID(context.Context, string, string) (dao.TokenMetadataRecord, error)
	FindTokenRevealByIDAndUserID(context.Context, string, string) (dao.TokenRevealRecord, error)
	RevokeToken(context.Context, string, string, time.Time) error
	FindUserByUsername(context.Context, string) (model.User, error)
}

type CreateTokenInput struct {
	Name      string
	Preset    string
	ExpiresAt *time.Time
}

type CreatedToken struct {
	TokenMetadata
	Token string `json:"token"`
}

type TokenMetadata struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Preset     string     `json:"preset"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type TokenPage struct {
	Items []TokenMetadata `json:"items"`
	Page  int             `json:"page"`
	Size  int             `json:"size"`
	Total int64           `json:"total"`
}

type TokenService struct {
	repository TokenStore
	authorizer auth.Authorizer
	pepper     []byte
	now        func() time.Time
	newID      func() string
}

func NewTokenService(repository TokenStore, authorizer auth.Authorizer, pepper []byte) (*TokenService, error) {
	if repository == nil || authorizer == nil {
		return nil, errors.New("identity token service requires repository and authorizer")
	}
	if len(pepper) < auth.MinimumAPIKeyPepperBytes {
		return nil, auth.ErrWeakAPIKeyPepper
	}
	return &TokenService{repository: repository, authorizer: authorizer, pepper: append([]byte(nil), pepper...), now: time.Now, newID: uuid.NewString}, nil
}

func (service *TokenService) Create(ctx context.Context, principal auth.Principal, input CreateTokenInput) (CreatedToken, error) {
	if err := service.authorizeCurrentUser(ctx, principal, auth.ActionTokenWrite); err != nil {
		return CreatedToken{}, err
	}
	now := service.now().UTC()
	name, expiresAt, err := validateCreateTokenInput(input, now)
	if err != nil {
		return CreatedToken{}, err
	}
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		return CreatedToken{}, fmt.Errorf("generate personal access token: %w", err)
	}
	secretHMAC, err := auth.IndexAPIKey(plaintext, service.pepper)
	if err != nil {
		return CreatedToken{}, fmt.Errorf("index personal access token: %w", err)
	}
	token := model.PersonalAccessToken{
		ID: service.newID(), UserID: principal.UserID(), Name: name, Preset: input.Preset,
		SecretPlaintext: plaintext, SecretHMAC: secretHMAC, ExpiresAt: expiresAt,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := service.repository.CreateToken(ctx, token); err != nil {
		return CreatedToken{}, fmt.Errorf("create personal access token: %w", err)
	}
	return CreatedToken{TokenMetadata: metadataFromRecord(recordFromToken(token), now), Token: plaintext}, nil
}

func (service *TokenService) List(ctx context.Context, principal auth.Principal, page, size int) (TokenPage, error) {
	if err := service.authorizeCurrentUser(ctx, principal, auth.ActionTokenRead); err != nil {
		return TokenPage{}, err
	}
	if page < 1 || size < 1 || size > MaximumTokenListSize || page-1 > math.MaxInt/size {
		return TokenPage{}, ErrInvalidTokenInput
	}
	records, total, err := service.repository.ListTokenMetadata(ctx, principal.UserID(), page, size)
	if errors.Is(err, dao.ErrInvalidPagination) {
		return TokenPage{}, ErrInvalidTokenInput
	}
	if err != nil {
		return TokenPage{}, fmt.Errorf("list personal access tokens: %w", err)
	}
	now := service.now().UTC()
	items := make([]TokenMetadata, 0, len(records))
	for _, record := range records {
		items = append(items, metadataFromRecord(record, now))
	}
	return TokenPage{Items: items, Page: page, Size: size, Total: total}, nil
}

func (service *TokenService) Reveal(ctx context.Context, principal auth.Principal, tokenID, password string) (CreatedToken, error) {
	if err := service.authorizeCurrentUser(ctx, principal, auth.ActionTokenRead); err != nil {
		return CreatedToken{}, err
	}
	if strings.TrimSpace(tokenID) == "" {
		return CreatedToken{}, ErrTokenNotFound
	}
	account, err := NewAccountService(service.repository)
	if err != nil || account.VerifyPassword(ctx, principal.Username(), password) != nil {
		return CreatedToken{}, ErrInvalidCredentials
	}
	record, err := service.repository.FindTokenRevealByIDAndUserID(ctx, tokenID, principal.UserID())
	if errors.Is(err, dao.ErrIdentityNotFound) {
		return CreatedToken{}, ErrTokenNotFound
	}
	if err != nil {
		return CreatedToken{}, fmt.Errorf("find personal access token: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, principal, auth.ActionTokenRead, auth.ResourceRef{Type: "token", ID: record.ID}); err != nil {
		return CreatedToken{}, ErrTokenAuthorization
	}
	return CreatedToken{TokenMetadata: metadataFromRecord(record.TokenMetadataRecord, service.now().UTC()), Token: record.SecretPlaintext}, nil
}

func (service *TokenService) Revoke(ctx context.Context, principal auth.Principal, tokenID string) error {
	if err := service.authorizeCurrentUser(ctx, principal, auth.ActionTokenWrite); err != nil {
		return err
	}
	if strings.TrimSpace(tokenID) == "" {
		return ErrTokenNotFound
	}
	record, err := service.repository.FindTokenMetadataByIDAndUserID(ctx, tokenID, principal.UserID())
	if errors.Is(err, dao.ErrIdentityNotFound) {
		return ErrTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("find personal access token: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, principal, auth.ActionTokenWrite, auth.ResourceRef{Type: "token", ID: record.ID}); err != nil {
		return ErrTokenAuthorization
	}
	if record.RevokedAt != nil {
		return nil
	}
	if err := service.repository.RevokeToken(ctx, record.ID, principal.UserID(), service.now().UTC()); err != nil {
		return fmt.Errorf("revoke personal access token: %w", err)
	}
	return nil
}

func (service *TokenService) authorizeCurrentUser(ctx context.Context, principal auth.Principal, action auth.Action) error {
	if !principal.IsUser() || principal.UserID() == "" || principal.CredentialKind() != auth.CredentialJWT {
		return ErrTokenAuthorization
	}
	if err := service.authorizer.Authorize(ctx, principal, action, auth.ResourceRef{Type: "token_collection", ID: principal.UserID()}); err != nil {
		return ErrTokenAuthorization
	}
	return nil
}

func validateCreateTokenInput(input CreateTokenInput, now time.Time) (string, *time.Time, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > maximumTokenNameBytes || !model.IsSupportedPreset(input.Preset) {
		return "", nil, ErrInvalidTokenInput
	}
	var expiresAt *time.Time
	if input.ExpiresAt != nil {
		normalized := input.ExpiresAt.UTC()
		if !normalized.After(now) {
			return "", nil, ErrInvalidTokenInput
		}
		expiresAt = &normalized
	}
	return name, expiresAt, nil
}

func metadataFromRecord(record dao.TokenMetadataRecord, now time.Time) TokenMetadata {
	status := TokenStatusActive
	if record.RevokedAt != nil {
		status = TokenStatusRevoked
	} else if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
		status = TokenStatusExpired
	}
	return TokenMetadata{
		ID: record.ID, Name: record.Name, Preset: record.Preset, Status: status,
		ExpiresAt: record.ExpiresAt, LastUsedAt: record.LastUsedAt, RevokedAt: record.RevokedAt, CreatedAt: record.CreatedAt,
	}
}

func recordFromToken(token model.PersonalAccessToken) dao.TokenMetadataRecord {
	return dao.TokenMetadataRecord{
		ID: token.ID, UserID: token.UserID, Name: token.Name, Preset: token.Preset,
		ExpiresAt: token.ExpiresAt, LastUsedAt: token.LastUsedAt, RevokedAt: token.RevokedAt, CreatedAt: token.CreatedAt,
	}
}
