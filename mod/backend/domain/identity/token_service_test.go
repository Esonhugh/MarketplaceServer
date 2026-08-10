package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

type tokenRepositoryFake struct {
	created      PersonalAccessToken
	list         []PersonalAccessToken
	listCursor   tokenPageCursor
	listLimit    int
	findToken    PersonalAccessToken
	findID       string
	findUserID   string
	findErr      error
	revokeID     string
	revokeUserID string
	revokeAt     time.Time
}

func (fake *tokenRepositoryFake) CreateToken(_ context.Context, token PersonalAccessToken) error {
	fake.created = token
	return nil
}

func (fake *tokenRepositoryFake) ListTokens(_ context.Context, _ string, cursor tokenPageCursor, limit int) ([]PersonalAccessToken, error) {
	fake.listCursor = cursor
	fake.listLimit = limit
	return fake.list, nil
}

func (fake *tokenRepositoryFake) FindTokenByIDAndUserID(_ context.Context, id string, userID string) (PersonalAccessToken, error) {
	fake.findID, fake.findUserID = id, userID
	return fake.findToken, fake.findErr
}

func (fake *tokenRepositoryFake) RevokeToken(_ context.Context, id, userID string, revokedAt time.Time) error {
	fake.revokeID, fake.revokeUserID, fake.revokeAt = id, userID, revokedAt
	return nil
}

type tokenAuthorizerFake struct {
	calls []authorizationCall
	err   error
}

type authorizationCall struct {
	action   auth.Action
	resource auth.ResourceRef
}

func (fake *tokenAuthorizerFake) Authorize(_ context.Context, _ auth.Principal, action auth.Action, resource auth.ResourceRef) error {
	fake.calls = append(fake.calls, authorizationCall{action: action, resource: resource})
	return fake.err
}

func testTokenPrincipal(t *testing.T, credential auth.CredentialKind, scopes auth.ScopeSet) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal("user-1", "alice", credential, scopes)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func TestTokenServiceCreateStoresOnlyIndexAndEnforcesDelegation(t *testing.T) {
	repository := &tokenRepositoryFake{}
	authorizer := &tokenAuthorizerFake{}
	service, err := NewTokenService(repository, authorizer, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.newID = func() string { return "token-1" }
	service.now = func() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC) }

	principal := testTokenPrincipal(t, auth.CredentialAPIKey, auth.RestrictedScopes(auth.ActionTokenWrite, auth.ActionRepositoryRead))
	created, err := service.Create(context.Background(), principal, CreateTokenInput{
		Name: " CI deploy ", Scopes: []auth.Action{auth.ActionRepositoryRead},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Plaintext == "" || !auth.HasAPIKeyPrefix(created.Plaintext) {
		t.Fatalf("Create() plaintext = %q, want mpsk_ key", created.Plaintext)
	}
	if repository.created.SecretHMAC == "" || repository.created.SecretHMAC == created.Plaintext {
		t.Fatalf("stored secret = %q, want HMAC index only", repository.created.SecretHMAC)
	}
	if repository.created.Name != "CI deploy" || repository.created.ID != "token-1" {
		t.Fatalf("stored token = %#v", repository.created)
	}
	if got := repository.created.Scopes; len(got) != 1 || got[0].Action != auth.ActionRepositoryRead {
		t.Fatalf("stored scopes = %#v", got)
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0] != (authorizationCall{auth.ActionTokenWrite, auth.ResourceRef{Type: "token_collection", ID: "user-1"}}) {
		t.Fatalf("authorizer calls = %#v", authorizer.calls)
	}

	_, err = service.Create(context.Background(), principal, CreateTokenInput{Name: "limited", Scopes: []auth.Action{auth.ActionPluginRead}})
	if !errors.Is(err, ErrTokenScopeNotAllowed) {
		t.Fatalf("Create() delegating unowned scope error = %v, want ErrTokenScopeNotAllowed", err)
	}
}

func TestTokenServiceListUsesSignedOpaqueCursorAndMetadata(t *testing.T) {
	repository := &tokenRepositoryFake{list: []PersonalAccessToken{
		{ID: "token-2", UserID: "user-1", Name: "automation", SecretHMAC: "not-for-output", CreatedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), Scopes: []PersonalAccessTokenScope{{Action: auth.ActionRepositoryRead}}},
		{ID: "token-1", UserID: "user-1", Name: "older", SecretHMAC: "also-not-for-output", CreatedAt: time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC), Scopes: []PersonalAccessTokenScope{{Action: auth.ActionPluginRead}}},
	}}
	authorizer := &tokenAuthorizerFake{}
	service, err := NewTokenService(repository, authorizer, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	principal := testTokenPrincipal(t, auth.CredentialAccountPassword, auth.UnrestrictedScopes())

	page, err := service.List(context.Background(), principal, 1, "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "token-2" || len(page.Items[0].Scopes) != 1 {
		t.Fatalf("List() items = %#v", page.Items)
	}
	if page.NextCursor == "" || page.NextCursor == "token-2" {
		t.Fatalf("List() next cursor = %q, want opaque cursor", page.NextCursor)
	}
	if repository.listLimit != 2 {
		t.Fatalf("List() repository limit = %d, want limit + 1", repository.listLimit)
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0].action != auth.ActionTokenRead {
		t.Fatalf("List() authorizer calls = %#v", authorizer.calls)
	}

	if _, err := service.List(context.Background(), principal, 1, "tampered"); !errors.Is(err, ErrInvalidTokenCursor) {
		t.Fatalf("List() invalid cursor error = %v, want ErrInvalidTokenCursor", err)
	}
}

func TestTokenServiceRejectsCursorFromAnotherOwner(t *testing.T) {
	service, err := NewTokenService(&tokenRepositoryFake{}, &tokenAuthorizerFake{}, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := service.encodeCursor(tokenPageCursor{UserID: "user-2", CreatedAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), ID: "token-2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(context.Background(), testTokenPrincipal(t, auth.CredentialAccountPassword, auth.UnrestrictedScopes()), 1, cursor); !errors.Is(err, ErrInvalidTokenCursor) {
		t.Fatalf("List() cross-owner cursor error = %v, want ErrInvalidTokenCursor", err)
	}
}

func TestTokenServiceRejectsOversizedCursor(t *testing.T) {
	service, err := NewTokenService(&tokenRepositoryFake{}, &tokenAuthorizerFake{}, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	principal := testTokenPrincipal(t, auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if _, err := service.List(context.Background(), principal, 1, strings.Repeat("a", MaximumTokenCursorBytes+1)); !errors.Is(err, ErrInvalidTokenCursor) {
		t.Fatalf("List() oversized cursor error = %v, want ErrInvalidTokenCursor", err)
	}
}

func TestTokenServiceRevokeActiveToken(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	repository := &tokenRepositoryFake{findToken: PersonalAccessToken{ID: "token-1", UserID: "user-1"}}
	service, err := NewTokenService(repository, &tokenAuthorizerFake{}, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	principal := testTokenPrincipal(t, auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if err := service.Revoke(context.Background(), principal, "token-1"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if repository.revokeID != "token-1" || repository.revokeUserID != "user-1" || !repository.revokeAt.Equal(now) {
		t.Fatalf("Revoke() persistence = %q/%q/%v", repository.revokeID, repository.revokeUserID, repository.revokeAt)
	}
}

func TestTokenServiceRevokeIsOwnerConstrainedAndIdempotent(t *testing.T) {
	revoked := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	repository := &tokenRepositoryFake{findToken: PersonalAccessToken{ID: "token-1", UserID: "user-1", RevokedAt: &revoked}}
	authorizer := &tokenAuthorizerFake{}
	service, err := NewTokenService(repository, authorizer, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return revoked.Add(time.Hour) }
	principal := testTokenPrincipal(t, auth.CredentialAccountPassword, auth.UnrestrictedScopes())

	if err := service.Revoke(context.Background(), principal, "token-1"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if repository.revokeID != "" {
		t.Fatalf("Revoke() updated already revoked token: %#v", repository)
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0] != (authorizationCall{auth.ActionTokenWrite, auth.ResourceRef{Type: "token", ID: "token-1"}}) {
		t.Fatalf("Revoke() authorizer calls = %#v", authorizer.calls)
	}
	if repository.findID != "token-1" || repository.findUserID != principal.UserID() {
		t.Fatalf("Revoke() owner lookup = %q/%q", repository.findID, repository.findUserID)
	}

	repository.findErr = ErrIdentityNotFound
	if err := service.Revoke(context.Background(), principal, "other-user-token"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("Revoke() other owner error = %v, want ErrTokenNotFound", err)
	}
}
