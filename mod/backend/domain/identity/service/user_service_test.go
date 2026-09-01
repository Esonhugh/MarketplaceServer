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

type userStoreFake struct {
	createdUser      model.User
	createdNamespace model.Namespace
	createErr        error
	setStatusErr     error
	setAdminErr      error
}

func (f *userStoreFake) CreateUserWithPersonalNamespace(_ context.Context, u model.User, n model.Namespace) error {
	f.createdUser, f.createdNamespace = u, n
	return f.createErr
}
func (f *userStoreFake) ListUsers(context.Context, int, int, string) ([]model.User, int64, error) {
	return nil, 0, nil
}
func (f *userStoreFake) FindUserByID(context.Context, string) (model.User, error) {
	return model.User{}, dao.ErrIdentityNotFound
}
func (f *userStoreFake) UpdateUserDisplayName(context.Context, string, string, time.Time) (model.User, error) {
	return model.User{}, dao.ErrIdentityNotFound
}
func (f *userStoreFake) SetUserStatus(context.Context, string, string, string, time.Time) error {
	return f.setStatusErr
}
func (f *userStoreFake) SetSystemAdmin(context.Context, string, string, bool) error {
	return f.setAdminErr
}
func (f *userStoreFake) IsSystemAdmin(context.Context, string) (bool, error) { return false, nil }

type userAuthorizerFake struct{ err error }

func (f userAuthorizerFake) Authorize(context.Context, auth.Principal, auth.Action, auth.ResourceRef) error {
	return f.err
}
func userPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	p, err := auth.NewUserPrincipal("actor", "admin", auth.CredentialJWT, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func newUserService(t *testing.T, store *userStoreFake) *UserService {
	t.Helper()
	s, err := NewUserService(store, userAuthorizerFake{})
	if err != nil {
		t.Fatal(err)
	}
	s.params = fastPasswordParams
	s.newID = func() string { return "11111111-1111-1111-1111-111111111111" }
	return s
}
func TestRegisterCreatesActiveUserAndPersonalNamespace(t *testing.T) {
	store := &userStoreFake{}
	s := newUserService(t, store)
	result, err := s.Register(context.Background(), CreateUserInput{Username: " Alice ", DisplayName: " Alice ", Password: "long enough password"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Username != "alice" || store.createdUser.Status != model.UserStatusActive || store.createdNamespace.Kind != model.NamespaceKindUser || store.createdNamespace.Slug != "alice" || store.createdNamespace.OwnerUserID == nil || *store.createdNamespace.OwnerUserID != store.createdUser.ID {
		t.Fatalf("user/namespace=%#v/%#v", store.createdUser, store.createdNamespace)
	}
}
func TestRegisterMapsAtomicConflictAndRejectsInvalidPassword(t *testing.T) {
	store := &userStoreFake{createErr: dao.ErrIdentityConflict}
	s := newUserService(t, store)
	if _, err := s.Register(context.Background(), CreateUserInput{Username: "alice", DisplayName: "Alice", Password: "long enough password"}); !errors.Is(err, ErrUserConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if _, err := s.Register(context.Background(), CreateUserInput{Username: "alice", DisplayName: "Alice", Password: "short"}); !errors.Is(err, ErrInvalidUserInput) {
		t.Fatalf("invalid error=%v", err)
	}
}
func TestUserCommandsRejectSelfAndFinalAdminConflict(t *testing.T) {
	store := &userStoreFake{setStatusErr: dao.ErrIdentityConflict, setAdminErr: dao.ErrIdentityConflict}
	s := newUserService(t, store)
	p := userPrincipal(t)
	if err := s.SetEnabled(context.Background(), p, "actor", false); !errors.Is(err, ErrUserOperationDenied) {
		t.Fatalf("self disable=%v", err)
	}
	if err := s.SetSystemAdmin(context.Background(), p, "actor", false); !errors.Is(err, ErrUserOperationDenied) {
		t.Fatalf("self revoke=%v", err)
	}
	if err := s.SetEnabled(context.Background(), p, "other", false); !errors.Is(err, ErrUserOperationDenied) {
		t.Fatalf("final disable=%v", err)
	}
	if err := s.SetSystemAdmin(context.Background(), p, "other", false); !errors.Is(err, ErrUserOperationDenied) {
		t.Fatalf("final revoke=%v", err)
	}
}
func TestUserCommandsRequireAuthorization(t *testing.T) {
	store := &userStoreFake{}
	s, err := NewUserService(store, userAuthorizerFake{err: errors.New("denied")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(context.Background(), userPrincipal(t), "other", false); !errors.Is(err, ErrUserAuthorization) {
		t.Fatalf("error=%v", err)
	}
}
