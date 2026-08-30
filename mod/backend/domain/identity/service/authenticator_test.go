package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

type patCredentialStoreFake struct {
	record     dao.TokenCredentialRecord
	findErr    error
	touchErr   error
	touchedID  string
	touchedAt  time.Time
	touchCount int
}

func (fake *patCredentialStoreFake) FindTokenCredentialBySecretHMAC(context.Context, string) (dao.TokenCredentialRecord, error) {
	return fake.record, fake.findErr
}

func (fake *patCredentialStoreFake) TouchToken(_ context.Context, id string, usedAt time.Time) error {
	fake.touchedID = id
	fake.touchedAt = usedAt
	fake.touchCount++
	return fake.touchErr
}

func TestPATAuthenticatorPlaneCapabilities(t *testing.T) {
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	pepper := []byte("01234567890123456789012345678901")
	environment := model.MapEnvironment{model.APIKeyPepperEnvironment: model.EncodeAPIKeyPepper(pepper)}
	for _, preset := range []string{model.TokenPresetSubscriptionRead, model.TokenPresetGitClone, model.TokenPresetGitWrite} {
		t.Run(preset, func(t *testing.T) {
			authenticator, err := NewPATAuthenticator(&patCredentialStoreFake{record: dao.TokenCredentialRecord{ID: "token-1", UserID: "user-1", Username: "alice", UserStatus: model.UserStatusActive, Preset: preset}}, environment)
			if err != nil {
				t.Fatal(err)
			}
			principal, err := authenticator.AuthenticateSubscriptionPAT(context.Background(), "alice", plaintext)
			if err != nil || principal.CredentialKind() != auth.CredentialPAT {
				t.Fatalf("subscription = %#v, %v", principal, err)
			}
			for _, action := range []auth.Action{auth.ActionMarketplaceRead, auth.ActionPluginRead} {
				if !principal.Allows(action) {
					t.Fatalf("preset %q does not allow %q", preset, action)
				}
			}
			if got := principal.Allows(auth.ActionPluginWrite); got != (preset == model.TokenPresetGitWrite) {
				t.Fatalf("preset %q plugin.write = %t", preset, got)
			}
			_, readErr := authenticator.AuthenticateGitPAT(context.Background(), "alice", plaintext, auth.GitOperationRead)
			_, writeErr := authenticator.AuthenticateGitPAT(context.Background(), "alice", plaintext, auth.GitOperationWrite)
			if preset == model.TokenPresetSubscriptionRead && (!errors.Is(readErr, ErrInvalidCredentials) || !errors.Is(writeErr, ErrInvalidCredentials)) {
				t.Fatalf("sub-read Git errors = %v/%v", readErr, writeErr)
			}
			if preset == model.TokenPresetGitClone && (readErr != nil || !errors.Is(writeErr, ErrInvalidCredentials)) {
				t.Fatalf("git-clone errors = %v/%v", readErr, writeErr)
			}
			if preset == model.TokenPresetGitWrite && (readErr != nil || writeErr != nil) {
				t.Fatalf("git-write errors = %v/%v", readErr, writeErr)
			}
		})
	}
}

func TestPATAuthenticatorTouchesOnlySuccessfulAuthentication(t *testing.T) {
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	environment := model.MapEnvironment{model.APIKeyPepperEnvironment: model.EncodeAPIKeyPepper([]byte("01234567890123456789012345678901"))}
	store := &patCredentialStoreFake{record: dao.TokenCredentialRecord{ID: "token-1", UserID: "user-1", Username: "alice", UserStatus: model.UserStatusActive, Preset: model.TokenPresetGitClone}}
	authenticator, err := NewPATAuthenticator(store, environment)
	if err != nil {
		t.Fatal(err)
	}
	authenticator.now = func() time.Time { return now }

	if _, err := authenticator.AuthenticateGitPAT(context.Background(), "alice", plaintext, auth.GitOperationWrite); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disallowed operation error = %v", err)
	}
	if store.touchCount != 0 {
		t.Fatalf("disallowed operation touched token %d times", store.touchCount)
	}
	if _, err := authenticator.AuthenticateSubscriptionPAT(context.Background(), "alice", plaintext); err != nil {
		t.Fatalf("successful authentication error = %v", err)
	}
	if store.touchCount != 1 || store.touchedID != "token-1" || !store.touchedAt.Equal(now) {
		t.Fatalf("touch = count %d, id %q, at %v", store.touchCount, store.touchedID, store.touchedAt)
	}
}

func TestPATAuthenticatorHidesTouchFailure(t *testing.T) {
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	environment := model.MapEnvironment{model.APIKeyPepperEnvironment: model.EncodeAPIKeyPepper([]byte("01234567890123456789012345678901"))}
	store := &patCredentialStoreFake{
		record:   dao.TokenCredentialRecord{ID: "token-1", UserID: "user-1", Username: "alice", UserStatus: model.UserStatusActive, Preset: model.TokenPresetSubscriptionRead},
		touchErr: errors.New("database contains secret reason"),
	}
	authenticator, err := NewPATAuthenticator(store, environment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticator.AuthenticateSubscriptionPAT(context.Background(), "alice", plaintext); err != ErrInvalidCredentials {
		t.Fatalf("touch failure error = %v", err)
	}
}

func TestPATAuthenticatorRejectsInactiveCredentialAndOwner(t *testing.T) {
	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	environment := model.MapEnvironment{model.APIKeyPepperEnvironment: model.EncodeAPIKeyPepper([]byte("01234567890123456789012345678901"))}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)
	for name, record := range map[string]dao.TokenCredentialRecord{
		"disabled": {UserID: "user-1", Username: "alice", UserStatus: model.UserStatusDisabled, Preset: model.TokenPresetGitWrite},
		"revoked":  {UserID: "user-1", Username: "alice", UserStatus: model.UserStatusActive, Preset: model.TokenPresetGitWrite, RevokedAt: &past},
		"expired":  {UserID: "user-1", Username: "alice", UserStatus: model.UserStatusActive, Preset: model.TokenPresetGitWrite, ExpiresAt: &past},
	} {
		t.Run(name, func(t *testing.T) {
			authenticator, err := NewPATAuthenticator(&patCredentialStoreFake{record: record}, environment)
			if err != nil {
				t.Fatal(err)
			}
			authenticator.now = func() time.Time { return now }
			if _, err := authenticator.AuthenticateGitPAT(context.Background(), "alice", plaintext, auth.GitOperationRead); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
