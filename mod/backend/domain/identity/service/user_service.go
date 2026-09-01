package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	DefaultUserListSize = 20
	MaximumUserListSize = 100
)

var (
	ErrInvalidUserInput    = errors.New("identity: invalid user input")
	ErrUserNotFound        = errors.New("identity: user not found")
	ErrUserAuthorization   = errors.New("identity: user authorization denied")
	ErrUserConflict        = errors.New("identity: user conflict")
	ErrUserOperationDenied = errors.New("identity: user operation conflict")
)

type UserStore interface {
	CreateUserWithPersonalNamespace(context.Context, model.User, model.Namespace) error
	ListUsers(context.Context, int, int, string) ([]model.User, int64, error)
	FindUserByID(context.Context, string) (model.User, error)
	UpdateUserDisplayName(context.Context, string, string, time.Time) (model.User, error)
	SetUserStatus(context.Context, string, string, string, time.Time) error
	SetSystemAdmin(context.Context, string, string, bool) error
	IsSystemAdmin(context.Context, string) (bool, error)
}

type CreateUserInput struct {
	Username    string
	DisplayName string
	Email       *string
	Password    string
	Status      string
}

type UserProfile struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	Email       *string   `json:"email"`
	DisplayName string    `json:"displayName"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	SystemAdmin bool      `json:"systemAdmin"`
}

type UserPage struct {
	Items []UserProfile `json:"items"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
	Total int64         `json:"total"`
}

type UserService struct {
	repository UserStore
	authorizer auth.Authorizer
	hash       func(string, auth.Argon2idParams) (string, error)
	params     auth.Argon2idParams
	now        func() time.Time
	newID      func() string
	log        *zap.Logger
}

func NewUserService(repository UserStore, authorizer auth.Authorizer) (*UserService, error) {
	if repository == nil || authorizer == nil {
		return nil, errors.New("identity user service requires repository and authorizer")
	}
	return &UserService{repository: repository, authorizer: authorizer, hash: auth.HashPassword, params: auth.DefaultArgon2idParams(), now: time.Now, newID: uuid.NewString, log: zap.L()}, nil
}

func (s *UserService) Register(ctx context.Context, input CreateUserInput) (UserProfile, error) {
	input.Status = model.UserStatusActive
	return s.create(ctx, auth.Principal{}, input, false)
}

func (s *UserService) Create(ctx context.Context, principal auth.Principal, input CreateUserInput) (UserProfile, error) {
	return s.create(ctx, principal, input, true)
}

func (s *UserService) create(ctx context.Context, principal auth.Principal, input CreateUserInput, requireAuthorization bool) (UserProfile, error) {
	if requireAuthorization {
		if err := s.authorize(ctx, principal, auth.ActionUserCreate, auth.ResourceRef{Type: auth.ResourceUserCollection, ID: "users"}); err != nil {
			return UserProfile{}, err
		}
	}
	username, displayName, email, password, status, err := validateCreateUser(input)
	if err != nil {
		return UserProfile{}, err
	}
	passwordHash, err := s.hash(password, s.params)
	if err != nil {
		return UserProfile{}, fmt.Errorf("hash user password: %w", err)
	}
	now := s.now().UTC()
	userID := s.newID()
	user := model.User{ID: userID, Username: username, DisplayName: displayName, Email: email, Status: status, PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now}
	namespace := model.Namespace{ID: s.newID(), Kind: model.NamespaceKindUser, Slug: username, DisplayName: displayName, OwnerUserID: &userID, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateUserWithPersonalNamespace(ctx, user, namespace); err != nil {
		if errors.Is(err, dao.ErrIdentityConflict) {
			s.logMutation(principal, "user.create", userID, "conflict", "duplicate")
			return UserProfile{}, ErrUserConflict
		}
		s.logMutation(principal, "user.create", userID, "failed", "persistence_error")
		return UserProfile{}, fmt.Errorf("create user: %w", err)
	}
	s.logMutation(principal, "user.create", userID, "success", "")
	return profileFromUser(user, false), nil
}

func (s *UserService) Me(ctx context.Context, principal auth.Principal) (UserProfile, error) {
	if !principal.IsUser() || principal.CredentialKind() != auth.CredentialJWT {
		return UserProfile{}, ErrUserAuthorization
	}
	user, err := s.repository.FindUserByID(ctx, principal.UserID())
	if errors.Is(err, dao.ErrIdentityNotFound) {
		return UserProfile{}, ErrUserNotFound
	}
	if err != nil {
		return UserProfile{}, fmt.Errorf("read current user: %w", err)
	}
	admin, err := s.repository.IsSystemAdmin(ctx, user.ID)
	if err != nil {
		return UserProfile{}, fmt.Errorf("read current user admin: %w", err)
	}
	return profileFromUser(user, admin), nil
}

func (s *UserService) List(ctx context.Context, principal auth.Principal, page, size int, status string) (UserPage, error) {
	if err := s.authorize(ctx, principal, auth.ActionUserList, auth.ResourceRef{Type: auth.ResourceUserCollection, ID: "users"}); err != nil {
		return UserPage{}, err
	}
	if page < 1 || size < 1 || size > MaximumUserListSize || (status != "" && status != model.UserStatusActive && status != model.UserStatusDisabled) {
		return UserPage{}, ErrInvalidUserInput
	}
	users, total, err := s.repository.ListUsers(ctx, page, size, status)
	if err != nil {
		return UserPage{}, fmt.Errorf("list users: %w", err)
	}
	items := make([]UserProfile, 0, len(users))
	for _, user := range users {
		admin, e := s.repository.IsSystemAdmin(ctx, user.ID)
		if e != nil {
			return UserPage{}, fmt.Errorf("read user admin: %w", e)
		}
		items = append(items, profileFromUser(user, admin))
	}
	return UserPage{Items: items, Page: page, Size: size, Total: total}, nil
}

func (s *UserService) Get(ctx context.Context, principal auth.Principal, id string) (UserProfile, error) {
	if err := s.authorize(ctx, principal, auth.ActionUserRead, userResource(id)); err != nil {
		return UserProfile{}, err
	}
	return s.getProfile(ctx, id)
}

func (s *UserService) UpdateDisplayName(ctx context.Context, principal auth.Principal, id, displayName string) (UserProfile, error) {
	if err := s.authorize(ctx, principal, auth.ActionUserUpdate, userResource(id)); err != nil {
		return UserProfile{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || utf8.RuneCountInString(displayName) > 255 {
		return UserProfile{}, ErrInvalidUserInput
	}
	user, err := s.repository.UpdateUserDisplayName(ctx, id, displayName, s.now().UTC())
	if errors.Is(err, dao.ErrIdentityNotFound) {
		return UserProfile{}, ErrUserNotFound
	}
	if err != nil {
		return UserProfile{}, fmt.Errorf("update user display name: %w", err)
	}
	admin, err := s.repository.IsSystemAdmin(ctx, id)
	if err != nil {
		return UserProfile{}, fmt.Errorf("read user admin: %w", err)
	}
	s.logMutation(principal, "user.update", id, "success", "")
	return profileFromUser(user, admin), nil
}

func (s *UserService) SetEnabled(ctx context.Context, principal auth.Principal, id string, enabled bool) error {
	action := auth.ActionUserDisable
	status := model.UserStatusDisabled
	if enabled {
		action, status = auth.ActionUserEnable, model.UserStatusActive
	}
	if err := s.authorize(ctx, principal, action, userResource(id)); err != nil {
		return err
	}
	if principal.UserID() == id {
		return ErrUserOperationDenied
	}
	if err := s.repository.SetUserStatus(ctx, principal.UserID(), id, status, s.now().UTC()); err != nil {
		if errors.Is(err, dao.ErrIdentityNotFound) {
			return ErrUserNotFound
		}
		if errors.Is(err, dao.ErrIdentityConflict) {
			return ErrUserOperationDenied
		}
		return fmt.Errorf("set user status: %w", err)
	}
	s.logMutation(principal, string(action), id, "success", "")
	return nil
}

func (s *UserService) SetSystemAdmin(ctx context.Context, principal auth.Principal, id string, enabled bool) error {
	action := auth.ActionUserAdminRevoke
	if enabled {
		action = auth.ActionUserAdminGrant
	}
	if err := s.authorize(ctx, principal, action, userResource(id)); err != nil {
		return err
	}
	if principal.UserID() == id {
		return ErrUserOperationDenied
	}
	if err := s.repository.SetSystemAdmin(ctx, principal.UserID(), id, enabled); err != nil {
		if errors.Is(err, dao.ErrIdentityNotFound) {
			return ErrUserNotFound
		}
		if errors.Is(err, dao.ErrIdentityConflict) {
			return ErrUserOperationDenied
		}
		return fmt.Errorf("set system admin: %w", err)
	}
	s.logMutation(principal, string(action), id, "success", "")
	return nil
}

func (s *UserService) getProfile(ctx context.Context, id string) (UserProfile, error) {
	user, err := s.repository.FindUserByID(ctx, id)
	if errors.Is(err, dao.ErrIdentityNotFound) {
		return UserProfile{}, ErrUserNotFound
	}
	if err != nil {
		return UserProfile{}, fmt.Errorf("read user: %w", err)
	}
	admin, err := s.repository.IsSystemAdmin(ctx, id)
	if err != nil {
		return UserProfile{}, fmt.Errorf("read user admin: %w", err)
	}
	return profileFromUser(user, admin), nil
}
func (s *UserService) authorize(ctx context.Context, principal auth.Principal, action auth.Action, resource auth.ResourceRef) error {
	if !principal.IsUser() || principal.CredentialKind() != auth.CredentialJWT || s.authorizer.Authorize(ctx, principal, action, resource) != nil {
		return ErrUserAuthorization
	}
	return nil
}
func (s *UserService) logMutation(principal auth.Principal, action, targetID, outcome, reason string) {
	s.log.Info("identity user mutation", zap.String("actor_user_id", principal.UserID()), zap.String("action", action), zap.String("target_type", "user"), zap.String("target_id", targetID), zap.String("outcome", outcome), zap.String("reason_code", reason))
}
func userResource(id string) auth.ResourceRef {
	return auth.ResourceRef{Type: auth.ResourceUser, ID: id}
}
func profileFromUser(user model.User, admin bool) UserProfile {
	return UserProfile{ID: user.ID, Username: user.Username, Email: user.Email, DisplayName: user.DisplayName, Status: user.Status, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt, SystemAdmin: admin}
}
func validateCreateUser(input CreateUserInput) (string, string, *string, string, string, error) {
	username := model.NormalizeUsername(input.Username)
	displayName := strings.TrimSpace(input.DisplayName)
	password := input.Password
	status := input.Status
	if status == "" {
		status = model.UserStatusActive
	}
	if len(username) == 0 || len(username) > 64 || !identitySlug(username) || displayName == "" || utf8.RuneCountInString(displayName) > 255 || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 1024 || auth.HasAPIKeyPrefix(password) || (status != model.UserStatusActive && status != model.UserStatusDisabled) {
		return "", "", nil, "", "", ErrInvalidUserInput
	}
	var email *string
	if input.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*input.Email))
		if normalized == "" || len(normalized) > 320 || !strings.Contains(normalized, "@") {
			return "", "", nil, "", "", ErrInvalidUserInput
		}
		email = &normalized
	}
	return username, displayName, email, password, status, nil
}
func identitySlug(value string) bool {
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		if r == '-' && i > 0 && i < len(value)-1 && value[i-1] != '-' && value[i+1] != '-' {
			continue
		}
		return false
	}
	return true
}
