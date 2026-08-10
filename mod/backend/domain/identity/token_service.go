package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
)

const (
	DefaultTokenListLimit   = 50
	MaximumTokenListLimit   = 100
	MaximumTokenCursorBytes = 1024
	maximumTokenNameBytes   = 128
)

var (
	ErrInvalidTokenInput    = errors.New("identity: invalid personal access token input")
	ErrTokenScopeNotAllowed = errors.New("identity: requested token scope is not allowed by the current credential")
	ErrInvalidTokenCursor   = errors.New("identity: invalid personal access token cursor")
	ErrTokenNotFound        = errors.New("identity: personal access token not found")
	ErrTokenAuthorization   = errors.New("identity: token authorization denied")
)

// TokenStore is the minimal persistence boundary needed by the personal API-key lifecycle.
type TokenStore interface {
	CreateToken(context.Context, PersonalAccessToken) error
	ListTokens(context.Context, string, tokenPageCursor, int) ([]PersonalAccessToken, error)
	FindTokenByIDAndUserID(context.Context, string, string) (PersonalAccessToken, error)
	RevokeToken(context.Context, string, string, time.Time) error
}

type CreateTokenInput struct {
	Name      string
	Scopes    []auth.Action
	ExpiresAt *time.Time
}

type CreatedToken struct {
	TokenMetadata
	Plaintext string
}

type TokenMetadata struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Scopes     []auth.Action `json:"scopes"`
	ExpiresAt  *time.Time    `json:"expiresAt,omitempty"`
	LastUsedAt *time.Time    `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time    `json:"revokedAt,omitempty"`
	CreatedAt  time.Time     `json:"createdAt"`
}

type TokenPage struct {
	Items      []TokenMetadata
	NextCursor string
}

type tokenPageCursor struct {
	UserID    string    `json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
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
	return &TokenService{
		repository: repository,
		authorizer: authorizer,
		pepper:     append([]byte(nil), pepper...),
		now:        time.Now,
		newID:      uuid.NewString,
	}, nil
}

func (service *TokenService) Create(ctx context.Context, principal auth.Principal, input CreateTokenInput) (CreatedToken, error) {
	if err := service.authorizeCurrentUser(ctx, principal, auth.ActionTokenWrite); err != nil {
		return CreatedToken{}, err
	}
	now := service.now().UTC()
	name, scopes, expiresAt, err := service.validateCreateInput(principal, input, now)
	if err != nil {
		return CreatedToken{}, err
	}
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		return CreatedToken{}, fmt.Errorf("generate personal access token: %w", err)
	}
	index, err := auth.IndexAPIKey(plaintext, service.pepper)
	if err != nil {
		return CreatedToken{}, fmt.Errorf("index personal access token: %w", err)
	}
	token := PersonalAccessToken{
		ID: service.newID(), UserID: principal.UserID(), Name: name, SecretHMAC: index,
		ExpiresAt: expiresAt, CreatedAt: now, UpdatedAt: now,
		Scopes: make([]PersonalAccessTokenScope, len(scopes)),
	}
	for i, scope := range scopes {
		token.Scopes[i] = PersonalAccessTokenScope{TokenID: token.ID, Action: scope}
	}
	if err := service.repository.CreateToken(ctx, token); err != nil {
		return CreatedToken{}, fmt.Errorf("create personal access token: %w", err)
	}
	return CreatedToken{TokenMetadata: metadataFromToken(token), Plaintext: plaintext}, nil
}

func (service *TokenService) List(ctx context.Context, principal auth.Principal, limit int, encodedCursor string) (TokenPage, error) {
	if err := service.authorizeCurrentUser(ctx, principal, auth.ActionTokenRead); err != nil {
		return TokenPage{}, err
	}
	if limit <= 0 {
		limit = DefaultTokenListLimit
	}
	if limit > MaximumTokenListLimit {
		limit = MaximumTokenListLimit
	}
	cursor, err := service.decodeCursor(encodedCursor)
	if err != nil {
		return TokenPage{}, err
	}
	if cursor.UserID != "" && cursor.UserID != principal.UserID() {
		return TokenPage{}, ErrInvalidTokenCursor
	}
	tokens, err := service.repository.ListTokens(ctx, principal.UserID(), cursor, limit+1)
	if err != nil {
		return TokenPage{}, fmt.Errorf("list personal access tokens: %w", err)
	}
	page := TokenPage{Items: make([]TokenMetadata, 0, min(limit, len(tokens)))}
	for _, token := range tokens[:min(limit, len(tokens))] {
		page.Items = append(page.Items, metadataFromToken(token))
	}
	if len(tokens) > limit {
		last := tokens[limit-1]
		page.NextCursor, err = service.encodeCursor(tokenPageCursor{UserID: principal.UserID(), CreatedAt: last.CreatedAt.UTC(), ID: last.ID})
		if err != nil {
			return TokenPage{}, fmt.Errorf("encode personal access token cursor: %w", err)
		}
	}
	return page, nil
}

func (service *TokenService) Revoke(ctx context.Context, principal auth.Principal, tokenID string) error {
	if strings.TrimSpace(tokenID) == "" {
		return ErrTokenNotFound
	}
	// Lookup is owner constrained before authorizing so a caller never gains a
	// distinguishable result for another user's token.
	token, err := service.repository.FindTokenByIDAndUserID(ctx, tokenID, principal.UserID())
	if errors.Is(err, ErrIdentityNotFound) {
		return ErrTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("find personal access token: %w", err)
	}
	if err := service.authorizer.Authorize(ctx, principal, auth.ActionTokenWrite, auth.ResourceRef{Type: "token", ID: token.ID}); err != nil {
		return ErrTokenAuthorization
	}
	if token.RevokedAt != nil {
		return nil
	}
	if err := service.repository.RevokeToken(ctx, token.ID, principal.UserID(), service.now().UTC()); err != nil {
		return fmt.Errorf("revoke personal access token: %w", err)
	}
	return nil
}

func (service *TokenService) authorizeCurrentUser(ctx context.Context, principal auth.Principal, action auth.Action) error {
	if !principal.IsUser() || principal.UserID() == "" {
		return ErrTokenAuthorization
	}
	// Token collections use the current user as their only owner locator; this
	// distinct resource type is resolved as self-owned by the policy reader.
	if err := service.authorizer.Authorize(ctx, principal, action, auth.ResourceRef{Type: "token_collection", ID: principal.UserID()}); err != nil {
		return ErrTokenAuthorization
	}
	return nil
}

func (service *TokenService) validateCreateInput(principal auth.Principal, input CreateTokenInput, now time.Time) (string, []auth.Action, *time.Time, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > maximumTokenNameBytes {
		return "", nil, nil, ErrInvalidTokenInput
	}
	if len(input.Scopes) == 0 {
		return "", nil, nil, ErrInvalidTokenInput
	}
	unique := make(map[auth.Action]struct{}, len(input.Scopes))
	for _, scope := range input.Scopes {
		if !isSupportedScope(scope) {
			return "", nil, nil, ErrInvalidTokenInput
		}
		if principal.CredentialKind() == auth.CredentialAPIKey && !principal.Allows(scope) {
			return "", nil, nil, ErrTokenScopeNotAllowed
		}
		unique[scope] = struct{}{}
	}
	scopes := auth.RestrictedScopes(input.Scopes...).Actions()
	var expiresAt *time.Time
	if input.ExpiresAt != nil {
		normalized := input.ExpiresAt.UTC()
		if !normalized.After(now) {
			return "", nil, nil, ErrInvalidTokenInput
		}
		expiresAt = &normalized
	}
	return name, scopes, expiresAt, nil
}

func metadataFromToken(token PersonalAccessToken) TokenMetadata {
	scopes := make([]auth.Action, 0, len(token.Scopes))
	for _, scope := range token.Scopes {
		scopes = append(scopes, scope.Action)
	}
	return TokenMetadata{
		ID: token.ID, Name: token.Name, Scopes: auth.RestrictedScopes(scopes...).Actions(),
		ExpiresAt: token.ExpiresAt, LastUsedAt: token.LastUsedAt, RevokedAt: token.RevokedAt, CreatedAt: token.CreatedAt,
	}
}

func (service *TokenService) cursorAEAD() (cipher.AEAD, error) {
	key := sha256.Sum256(service.pepper)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (service *TokenService) encodeCursor(cursor tokenPageCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	aead, err := service.cursorAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	// Cursor metadata is intentionally opaque to clients, so use a fresh AEAD
	// nonce on every emitted page boundary.
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(nonce, aead.Seal(nil, nonce, payload, nil)...)), nil
}

func (service *TokenService) decodeCursor(encoded string) (tokenPageCursor, error) {
	if encoded == "" {
		return tokenPageCursor{}, nil
	}
	if len(encoded) > MaximumTokenCursorBytes {
		return tokenPageCursor{}, ErrInvalidTokenCursor
	}
	blob, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return tokenPageCursor{}, ErrInvalidTokenCursor
	}
	aead, err := service.cursorAEAD()
	if err != nil || len(blob) < aead.NonceSize() {
		return tokenPageCursor{}, ErrInvalidTokenCursor
	}
	payload, err := aead.Open(nil, blob[:aead.NonceSize()], blob[aead.NonceSize():], nil)
	if err != nil {
		return tokenPageCursor{}, ErrInvalidTokenCursor
	}
	var cursor tokenPageCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.UserID == "" || cursor.ID == "" || cursor.CreatedAt.IsZero() {
		return tokenPageCursor{}, ErrInvalidTokenCursor
	}
	return cursor, nil
}
