package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

type tokenRepositoryFake struct {
	created    model.PersonalAccessToken
	list       []dao.TokenMetadataRecord
	total      int64
	listErr    error
	listUserID string
	listPage   int
	listSize   int
	metadata   dao.TokenMetadataRecord
	reveal     dao.TokenRevealRecord
	findErr    error
	revokeID   string
	revokeUser string
	revokeAt   time.Time
	user       model.User
}

func (fake *tokenRepositoryFake) CreateToken(_ context.Context, token model.PersonalAccessToken) error {
	fake.created = token
	return nil
}
func (fake *tokenRepositoryFake) ListTokenMetadata(_ context.Context, userID string, page, size int) ([]dao.TokenMetadataRecord, int64, error) {
	fake.listUserID, fake.listPage, fake.listSize = userID, page, size
	return fake.list, fake.total, fake.listErr
}
func (fake *tokenRepositoryFake) FindTokenMetadataByIDAndUserID(context.Context, string, string) (dao.TokenMetadataRecord, error) {
	return fake.metadata, fake.findErr
}
func (fake *tokenRepositoryFake) FindTokenRevealByIDAndUserID(context.Context, string, string) (dao.TokenRevealRecord, error) {
	return fake.reveal, fake.findErr
}
func (fake *tokenRepositoryFake) RevokeToken(_ context.Context, id, userID string, at time.Time) error {
	fake.revokeID, fake.revokeUser, fake.revokeAt = id, userID, at
	return nil
}
func (fake *tokenRepositoryFake) FindUserByUsername(context.Context, string) (model.User, error) {
	if fake.user.ID == "" {
		return model.User{}, dao.ErrIdentityNotFound
	}
	return fake.user, nil
}

type tokenAuthorizerFake struct{ err error }

func (fake tokenAuthorizerFake) Authorize(context.Context, auth.Principal, auth.Action, auth.ResourceRef) error {
	return fake.err
}

func jwtPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialJWT, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func newTokenServiceForTest(t *testing.T, repository *tokenRepositoryFake) *TokenService {
	t.Helper()
	service, err := NewTokenService(repository, tokenAuthorizerFake{}, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC) }
	service.newID = func() string { return "token-1" }
	return service
}

func TestTokenServiceCreatePresetAndRepeatableSecret(t *testing.T) {
	repository := &tokenRepositoryFake{user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive}}
	service := newTokenServiceForTest(t, repository)

	created, err := service.Create(context.Background(), jwtPrincipal(t), CreateTokenInput{Name: " CI ", Preset: model.TokenPresetGitClone})
	if err != nil {
		t.Fatal(err)
	}
	if created.Token == "" || repository.created.SecretPlaintext != created.Token || repository.created.SecretHMAC == created.Token {
		t.Fatalf("created/stored secrets do not match target boundary")
	}
	if repository.created.Preset != model.TokenPresetGitClone || repository.created.Name != "CI" || repository.created.UserID != "user-1" {
		t.Fatalf("stored token = %#v", repository.created)
	}
	if created.Status != TokenStatusActive {
		t.Fatalf("status = %q", created.Status)
	}
}

func TestTokenServiceRequiresJWTAndValidPreset(t *testing.T) {
	repository := &tokenRepositoryFake{user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive}}
	service := newTokenServiceForTest(t, repository)
	passwordPrincipal, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), passwordPrincipal, CreateTokenInput{Name: "x", Preset: model.TokenPresetGitWrite}); !errors.Is(err, ErrTokenAuthorization) {
		t.Fatalf("password Create error = %v", err)
	}
	if _, err := service.Create(context.Background(), jwtPrincipal(t), CreateTokenInput{Name: "x", Preset: "unknown"}); !errors.Is(err, ErrInvalidTokenInput) {
		t.Fatalf("unknown preset Create error = %v", err)
	}
}

func TestTokenServiceListUsesPageSizeExactTotalAndComputedStatus(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	revoked := now.Add(-time.Hour)
	expired := now.Add(-time.Minute)
	repository := &tokenRepositoryFake{total: 5, list: []dao.TokenMetadataRecord{
		{ID: "revoked", UserID: "user-1", Name: "revoked", Preset: model.TokenPresetGitWrite, RevokedAt: &revoked},
		{ID: "expired", UserID: "user-1", Name: "expired", Preset: model.TokenPresetGitClone, ExpiresAt: &expired},
	}}
	service := newTokenServiceForTest(t, repository)
	page, err := service.List(context.Background(), jwtPrincipal(t), 2, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || page.Size != 20 || page.Total != 5 || repository.listUserID != "user-1" || repository.listPage != 2 || repository.listSize != 20 {
		t.Fatalf("page = %#v repository = %#v", page, repository)
	}
	if page.Items[0].Status != TokenStatusRevoked || page.Items[1].Status != TokenStatusExpired {
		t.Fatalf("statuses = %#v", page.Items)
	}
	if _, err := service.List(context.Background(), jwtPrincipal(t), 0, 20); !errors.Is(err, ErrInvalidTokenInput) {
		t.Fatalf("invalid page error = %v", err)
	}
	repository.listPage = 0
	if _, err := service.List(context.Background(), jwtPrincipal(t), math.MaxInt, MaximumTokenListSize); !errors.Is(err, ErrInvalidTokenInput) {
		t.Fatalf("overflow page error = %v", err)
	}
	if repository.listPage != 0 {
		t.Fatalf("overflow page reached repository with page %d", repository.listPage)
	}
}

func TestTokenServiceMapsDAOInvalidPagination(t *testing.T) {
	repository := &tokenRepositoryFake{listErr: dao.ErrInvalidPagination}
	service := newTokenServiceForTest(t, repository)
	if _, err := service.List(context.Background(), jwtPrincipal(t), 1, 20); !errors.Is(err, ErrInvalidTokenInput) {
		t.Fatalf("DAO pagination error = %v", err)
	}
}

func TestTokenServiceDTOsDoNotSerializePersistenceSecrets(t *testing.T) {
	repository := &tokenRepositoryFake{user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive}}
	service := newTokenServiceForTest(t, repository)
	created, err := service.Create(context.Background(), jwtPrincipal(t), CreateTokenInput{Name: "CI", Preset: model.TokenPresetGitClone})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), repository.created.SecretHMAC) || strings.Contains(string(encoded), "secret_hmac") || strings.Contains(string(encoded), "secret_plaintext") {
		t.Fatalf("created DTO serialized persistence secret: %s", encoded)
	}
}

func TestTokenServiceRevealConfirmsPasswordAndHidesOwnership(t *testing.T) {
	hash, err := auth.HashPassword("correct", fastPasswordParams)
	if err != nil {
		t.Fatal(err)
	}
	repository := &tokenRepositoryFake{
		user:   model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive, PasswordHash: hash},
		reveal: dao.TokenRevealRecord{TokenMetadataRecord: dao.TokenMetadataRecord{ID: "token-1", UserID: "user-1", Preset: model.TokenPresetGitWrite}, SecretPlaintext: "mpsk_secret"},
	}
	service := newTokenServiceForTest(t, repository)
	got, err := service.Reveal(context.Background(), jwtPrincipal(t), "token-1", "correct")
	if err != nil || got.Token != "mpsk_secret" {
		t.Fatalf("Reveal() = %#v, %v", got, err)
	}
	if _, err := service.Reveal(context.Background(), jwtPrincipal(t), "token-1", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	repository.findErr = dao.ErrIdentityNotFound
	if _, err := service.Reveal(context.Background(), jwtPrincipal(t), "other-owner", "correct"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("other owner error = %v", err)
	}
}

func TestTokenServiceRevokeIsOwnerHiddenAndIdempotent(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	repository := &tokenRepositoryFake{metadata: dao.TokenMetadataRecord{ID: "token-1", UserID: "user-1"}}
	service := newTokenServiceForTest(t, repository)
	if err := service.Revoke(context.Background(), jwtPrincipal(t), "token-1"); err != nil {
		t.Fatal(err)
	}
	if repository.revokeID != "token-1" || repository.revokeUser != "user-1" || !repository.revokeAt.Equal(now) {
		t.Fatalf("revoke = %#v", repository)
	}
	repository.metadata.RevokedAt = &now
	repository.revokeID = ""
	if err := service.Revoke(context.Background(), jwtPrincipal(t), "token-1"); err != nil || repository.revokeID != "" {
		t.Fatalf("idempotent revoke = %v, %#v", err, repository)
	}
	repository.findErr = dao.ErrIdentityNotFound
	if err := service.Revoke(context.Background(), jwtPrincipal(t), "other-owner"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("hidden owner error = %v", err)
	}
}
