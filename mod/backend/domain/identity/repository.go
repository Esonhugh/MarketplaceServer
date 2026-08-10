package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"gorm.io/gorm"
)

var ErrIdentityNotFound = errors.New("identity: record not found")

type Repository struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("identity repository requires database")
	}
	return &Repository{db: db}, nil
}

func (repository *Repository) CountUsers(ctx context.Context) (int64, error) {
	var count int64
	if err := repository.db.WithContext(ctx).Model(&User{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func (repository *Repository) FindUserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	err := repository.db.WithContext(ctx).Where("username = ?", normalizeUsername(username)).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, ErrIdentityNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user by username: %w", err)
	}
	return user, nil
}

func (repository *Repository) FindUserByID(ctx context.Context, id string) (User, error) {
	var user User
	err := repository.db.WithContext(ctx).Where("id = ?", id).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, ErrIdentityNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user by ID: %w", err)
	}
	return user, nil
}

func (repository *Repository) FindTokenBySecretHMAC(ctx context.Context, secretHMAC string) (PersonalAccessToken, error) {
	var token PersonalAccessToken
	err := repository.db.WithContext(ctx).
		Preload("Scopes").
		Preload("User").
		Where("secret_hmac = ?", secretHMAC).
		Take(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PersonalAccessToken{}, ErrIdentityNotFound
	}
	if err != nil {
		return PersonalAccessToken{}, fmt.Errorf("find personal access token: %w", err)
	}
	return token, nil
}

func (repository *Repository) CreateToken(ctx context.Context, token PersonalAccessToken) error {
	if err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("create personal access token: %w", err)
	}
	return nil
}

func (repository *Repository) ListTokens(ctx context.Context, userID string, cursor tokenPageCursor, limit int) ([]PersonalAccessToken, error) {
	query := repository.db.WithContext(ctx).Preload("Scopes").
		Where("user_id = ?", userID).
		Order("created_at DESC").Order("id DESC").Limit(limit)
	if !cursor.CreatedAt.IsZero() {
		query = query.Where("(created_at < ?) OR (created_at = ? AND id < ?)", cursor.CreatedAt.UTC(), cursor.CreatedAt.UTC(), cursor.ID)
	}
	var tokens []PersonalAccessToken
	if err := query.Find(&tokens).Error; err != nil {
		return nil, fmt.Errorf("list personal access tokens: %w", err)
	}
	return tokens, nil
}

func (repository *Repository) FindTokenByIDAndUserID(ctx context.Context, id, userID string) (PersonalAccessToken, error) {
	var token PersonalAccessToken
	err := repository.db.WithContext(ctx).Preload("Scopes").Where("id = ? AND user_id = ?", id, userID).Take(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PersonalAccessToken{}, ErrIdentityNotFound
	}
	if err != nil {
		return PersonalAccessToken{}, fmt.Errorf("find owned personal access token: %w", err)
	}
	return token, nil
}

func (repository *Repository) RevokeToken(ctx context.Context, id, userID string, revokedAt time.Time) error {
	result := repository.db.WithContext(ctx).Model(&PersonalAccessToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, userID).
		Updates(map[string]any{"revoked_at": revokedAt.UTC(), "updated_at": revokedAt.UTC()})
	if result.Error != nil {
		return fmt.Errorf("revoke personal access token: %w", result.Error)
	}
	return nil
}

func (repository *Repository) IsSystemAdmin(ctx context.Context, userID string) (bool, error) {
	var count int64
	err := repository.db.WithContext(ctx).Model(&UserGroupMembership{}).
		Where("user_id = ? AND group_id = ?", userID, AdminSystemGroupID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("read admin membership: %w", err)
	}
	return count > 0, nil
}

func (repository *Repository) IsDefaultMember(ctx context.Context, userID string) (bool, error) {
	user, err := repository.FindUserByID(ctx, userID)
	if err != nil {
		return false, err
	}
	return user.Status == UserStatusActive, nil
}

func (repository *Repository) UpdateSystemGroup(ctx context.Context, id string, changes map[string]any) error {
	if isFixedSystemGroup(id) {
		return ErrImmutableSystemGroup
	}
	if err := repository.db.WithContext(ctx).Model(&SystemGroup{ID: id}).Updates(changes).Error; err != nil {
		return fmt.Errorf("update system group: %w", err)
	}
	return nil
}

func (repository *Repository) DeleteSystemGroup(ctx context.Context, id string) error {
	if isFixedSystemGroup(id) {
		return ErrImmutableSystemGroup
	}
	if err := repository.db.WithContext(ctx).Delete(&SystemGroup{ID: id}).Error; err != nil {
		return fmt.Errorf("delete system group: %w", err)
	}
	return nil
}

func (repository *Repository) TouchToken(ctx context.Context, id string, usedAt time.Time) error {
	result := repository.db.WithContext(ctx).Model(&PersonalAccessToken{}).
		Where("id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", id, usedAt.UTC()).
		Update("last_used_at", usedAt.UTC())
	if result.Error != nil {
		return fmt.Errorf("update personal access token use: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrIdentityNotFound
	}
	return nil
}

func tokenScopes(scopes []PersonalAccessTokenScope) (auth.ScopeSet, error) {
	actions := make([]auth.Action, 0, len(scopes))
	for _, scope := range scopes {
		if !isSupportedScope(scope.Action) {
			return auth.ScopeSet{}, ErrInvalidTokenScope
		}
		actions = append(actions, scope.Action)
	}
	return auth.RestrictedScopes(actions...), nil
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
