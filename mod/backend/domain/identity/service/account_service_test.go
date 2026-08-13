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

var fastPasswordParams = auth.Argon2idParams{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, HashLength: 16}

type accountRepositoryFake struct {
	user model.User
	err  error
}

func (fake accountRepositoryFake) FindUserByUsername(context.Context, string) (model.User, error) {
	return fake.user, fake.err
}

func TestAccountServiceLoginAndPasswordVerification(t *testing.T) {
	hash, err := auth.HashPassword("correct password", fastPasswordParams)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewAccountService(accountRepositoryFake{user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive, PasswordHash: hash}})
	if err != nil {
		t.Fatal(err)
	}

	user, err := service.AuthenticatePassword(context.Background(), "ALICE", "correct password")
	if err != nil || user.ID != "user-1" || user.Username != "alice" {
		t.Fatalf("AuthenticatePassword() = %#v, %v", user, err)
	}
	if err := service.VerifyPassword(context.Background(), "alice", "correct password"); err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
}

func TestAccountServiceLoginIssuesJWT(t *testing.T) {
	hash, err := auth.HashPassword("correct", fastPasswordParams)
	if err != nil {
		t.Fatal(err)
	}
	account, err := NewAccountService(accountRepositoryFake{user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive, PasswordHash: hash}})
	if err != nil {
		t.Fatal(err)
	}
	jwtService, err := NewJWTService("secret")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	jwtService.now = func() time.Time { return now }
	login := NewLoginService(account, jwtService)
	result, err := login.Login(context.Background(), "alice", "correct")
	if err != nil || result.Username != "alice" || result.Token == "" || !result.ExpiresAt.Equal(now.Add(JWTLifetime)) {
		t.Fatalf("Login() = %#v, %v", result, err)
	}
}

func TestLoginServicePropagatesRepositoryErrors(t *testing.T) {
	repositoryErr := errors.New("database unavailable")
	account, err := NewAccountService(accountRepositoryFake{err: repositoryErr})
	if err != nil {
		t.Fatal(err)
	}
	jwtService, err := NewJWTService("secret")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewLoginService(account, jwtService).Login(context.Background(), "alice", "password"); !errors.Is(err, repositoryErr) {
		t.Fatalf("Login() error = %v, want wrapped repository error", err)
	}
}

func TestAccountServiceResolvesVerifiedJWTPrincipalWithoutDisabledCheck(t *testing.T) {
	service, err := NewAccountService(accountRepositoryFake{user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusDisabled}})
	if err != nil {
		t.Fatal(err)
	}
	jwtService, err := NewJWTService("secret")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := jwtService.Issue("alice")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.ResolveJWT(context.Background(), jwtService, token)
	if err != nil || principal.UserID() != "user-1" || principal.CredentialKind() != auth.CredentialJWT {
		t.Fatalf("ResolveJWT() = %#v, %v", principal, err)
	}
}

func TestAccountServicePropagatesRepositoryErrors(t *testing.T) {
	repositoryErr := errors.New("database unavailable")
	service, err := NewAccountService(accountRepositoryFake{err: repositoryErr})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.AuthenticatePassword(context.Background(), "alice", "password"); !errors.Is(err, repositoryErr) {
		t.Fatalf("AuthenticatePassword() error = %v, want wrapped repository error", err)
	}
}

func TestAccountServiceUsesFixedValidDummyHashForUnknownAndDisabledUsers(t *testing.T) {
	cases := map[string]accountRepositoryFake{
		"unknown":  {err: dao.ErrIdentityNotFound},
		"disabled": {user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusDisabled, PasswordHash: "must-not-be-used"}},
	}
	for name, repository := range cases {
		t.Run(name, func(t *testing.T) {
			service, err := NewAccountService(repository)
			if err != nil {
				t.Fatal(err)
			}
			var verifiedHashes []string
			service.verifyPassword = func(_ string, encoded string) (bool, error) {
				verifiedHashes = append(verifiedHashes, encoded)
				return false, nil
			}

			if _, err := service.AuthenticatePassword(context.Background(), "alice", "supplied password"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("AuthenticatePassword() error = %v", err)
			}
			if len(verifiedHashes) != 1 || verifiedHashes[0] != dummyPasswordHash {
				t.Fatalf("verified hashes = %q", verifiedHashes)
			}
		})
	}

	if valid, err := auth.VerifyPassword("any supplied password", dummyPasswordHash); err != nil || valid {
		t.Fatalf("dummy password hash verification = %t, %v", valid, err)
	}
}

func TestAccountServiceReturnsGenericCredentialError(t *testing.T) {
	hash, err := auth.HashPassword("correct", fastPasswordParams)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]accountRepositoryFake{
		"unknown":        {err: dao.ErrIdentityNotFound},
		"disabled":       {user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusDisabled, PasswordHash: hash}},
		"wrong password": {user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive, PasswordHash: hash}},
		"bad hash":       {user: model.User{ID: "user-1", Username: "alice", Status: model.UserStatusActive, PasswordHash: "not-a-hash"}},
	}
	for name, repository := range cases {
		t.Run(name, func(t *testing.T) {
			service, err := NewAccountService(repository)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.AuthenticatePassword(context.Background(), "alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) || err.Error() != ErrInvalidCredentials.Error() {
				t.Fatalf("AuthenticatePassword() error = %v", err)
			}
		})
	}
}
