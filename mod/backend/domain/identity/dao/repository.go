package dao

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrIdentityNotFound  = errors.New("identity: record not found")
	ErrIdentityConflict  = errors.New("identity: conflict")
	ErrInvalidPagination = errors.New("identity dao: invalid pagination")
)

type TokenMetadataRecord struct {
	ID         string
	UserID     string
	Name       string
	Preset     string
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

type TokenRevealRecord struct {
	TokenMetadataRecord
	SecretPlaintext string
}

type TokenCredentialRecord struct {
	ID         string
	UserID     string
	Username   string
	UserStatus string
	Preset     string
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
}

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
	if err := repository.db.WithContext(ctx).Model(&model.User{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func (repository *Repository) FindUserByUsername(ctx context.Context, username string) (model.User, error) {
	var user model.User
	err := repository.db.WithContext(ctx).Where("username = ?", model.NormalizeUsername(username)).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.User{}, ErrIdentityNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("find user by username: %w", err)
	}
	return user, nil
}

func (repository *Repository) FindUserByID(ctx context.Context, id string) (model.User, error) {
	var user model.User
	err := repository.db.WithContext(ctx).Where("id = ?", id).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.User{}, ErrIdentityNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("find user by ID: %w", err)
	}
	return user, nil
}

func (repository *Repository) CreateUserWithPersonalNamespace(ctx context.Context, user model.User, namespace model.Namespace) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrIdentityConflict
			}
			return fmt.Errorf("create user: %w", err)
		}
		if err := tx.Create(&namespace).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrIdentityConflict
			}
			return fmt.Errorf("create personal namespace: %w", err)
		}
		return nil
	})
}

func (repository *Repository) ListUsers(ctx context.Context, page, size int, status string) ([]model.User, int64, error) {
	if page < 1 || size < 1 || page-1 > int(^uint(0)>>1)/size {
		return nil, 0, ErrInvalidPagination
	}
	base := repository.db.WithContext(ctx).Model(&model.User{})
	if status != "" {
		base = base.Where("status = ?", status)
	}
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	var users []model.User
	if err := base.Session(&gorm.Session{}).Order("created_at DESC").Order("id DESC").Limit(size).Offset((page - 1) * size).Find(&users).Error; err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	return users, total, nil
}

func (repository *Repository) UpdateUserDisplayName(ctx context.Context, id, displayName string, updatedAt time.Time) (model.User, error) {
	result := repository.db.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Updates(map[string]any{"display_name": displayName, "updated_at": updatedAt.UTC()})
	if result.Error != nil {
		return model.User{}, fmt.Errorf("update user display name: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return model.User{}, ErrIdentityNotFound
	}
	return repository.FindUserByID(ctx, id)
}

func (repository *Repository) SetUserStatus(ctx context.Context, actorID, id, status string, updatedAt time.Time) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSystemAdministrators(tx, id); err != nil {
			return fmt.Errorf("lock administrator state: %w", err)
		}
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&user).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIdentityNotFound
		} else if err != nil {
			return fmt.Errorf("find user for status update: %w", err)
		}
		if user.Status == status {
			return nil
		}
		if status == model.UserStatusDisabled {
			var activeAdmins int64
			if err := tx.Table("user_group_memberships AS membership").Joins("JOIN users ON users.id = membership.user_id").Where("membership.group_id = ? AND users.status = ?", model.AdminSystemGroupID, model.UserStatusActive).Count(&activeAdmins).Error; err != nil {
				return fmt.Errorf("count active administrators: %w", err)
			}
			var targetAdmin int64
			if err := tx.Model(&model.UserGroupMembership{}).Where("user_id = ? AND group_id = ?", id, model.AdminSystemGroupID).Count(&targetAdmin).Error; err != nil {
				return fmt.Errorf("read administrator membership: %w", err)
			}
			if targetAdmin > 0 && activeAdmins <= 1 {
				return ErrIdentityConflict
			}
		}
		if err := tx.Model(&model.User{}).Where("id = ? AND status = ?", id, user.Status).Updates(map[string]any{"status": status, "updated_at": updatedAt.UTC()}).Error; err != nil {
			return fmt.Errorf("set user status: %w", err)
		}
		return nil
	})
}

func (repository *Repository) SetSystemAdmin(ctx context.Context, actorID, id string, enabled bool) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSystemAdministrators(tx, id); err != nil {
			return fmt.Errorf("lock administrator state: %w", err)
		}
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).Take(&user).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIdentityNotFound
		} else if err != nil {
			return fmt.Errorf("find user for admin update: %w", err)
		}
		if enabled {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.UserGroupMembership{UserID: id, GroupID: model.AdminSystemGroupID}).Error; err != nil {
				return fmt.Errorf("grant administrator membership: %w", err)
			}
			return nil
		}
		var activeAdmins int64
		if err := tx.Table("user_group_memberships AS membership").Joins("JOIN users ON users.id = membership.user_id").Where("membership.group_id = ? AND users.status = ?", model.AdminSystemGroupID, model.UserStatusActive).Count(&activeAdmins).Error; err != nil {
			return fmt.Errorf("count active administrators: %w", err)
		}
		if user.Status == model.UserStatusActive && activeAdmins <= 1 {
			return ErrIdentityConflict
		}
		if err := tx.Where("user_id = ? AND group_id = ?", id, model.AdminSystemGroupID).Delete(&model.UserGroupMembership{}).Error; err != nil {
			return fmt.Errorf("revoke administrator membership: %w", err)
		}
		return nil
	})
}

func lockSystemAdministrators(tx *gorm.DB, targetUserID string) error {
	if tx.Dialector.Name() == "postgres" {
		return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "identity:system-administrators").Error
	}
	// SQLite serializes writers. Touching the target user acquires the write lock
	// before the active-administrator count is read without mutating immutable
	// system-group rows.
	if tx.Dialector.Name() == "sqlite" {
		return tx.Exec("UPDATE users SET updated_at = updated_at WHERE id = ?", targetUserID).Error
	}
	return nil
}

func isUniqueConstraintError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key")
}

func (repository *Repository) FindTokenCredentialBySecretHMAC(ctx context.Context, secretHMAC string) (TokenCredentialRecord, error) {
	var record TokenCredentialRecord
	err := repository.db.WithContext(ctx).Table("personal_access_tokens AS pat").
		Select("pat.id, pat.user_id, users.username, users.status AS user_status, pat.preset, pat.expires_at, pat.revoked_at").
		Joins("JOIN users ON users.id = pat.user_id").
		Where("pat.secret_hmac = ?", secretHMAC).
		Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TokenCredentialRecord{}, ErrIdentityNotFound
	}
	if err != nil {
		return TokenCredentialRecord{}, fmt.Errorf("find personal access token credential: %w", err)
	}
	return record, nil
}

func (repository *Repository) CreateToken(ctx context.Context, token model.PersonalAccessToken) error {
	if err := repository.db.WithContext(ctx).Create(&token).Error; err != nil {
		return fmt.Errorf("create personal access token: %w", err)
	}
	return nil
}

func (repository *Repository) ListTokenMetadata(ctx context.Context, userID string, page, size int) ([]TokenMetadataRecord, int64, error) {
	if page < 1 || size < 1 || page-1 > int(^uint(0)>>1)/size {
		return nil, 0, ErrInvalidPagination
	}
	base := repository.db.WithContext(ctx).Model(&model.PersonalAccessToken{}).Where("user_id = ?", userID)
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count personal access tokens: %w", err)
	}
	var records []TokenMetadataRecord
	if err := base.Session(&gorm.Session{}).Select(tokenMetadataColumns()).
		Order("created_at DESC").Order("id DESC").
		Limit(size).Offset((page - 1) * size).
		Find(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("list personal access tokens: %w", err)
	}
	return records, total, nil
}

func (repository *Repository) FindTokenMetadataByIDAndUserID(ctx context.Context, id, userID string) (TokenMetadataRecord, error) {
	var record TokenMetadataRecord
	err := repository.db.WithContext(ctx).Model(&model.PersonalAccessToken{}).
		Select(tokenMetadataColumns()).
		Where("id = ? AND user_id = ?", id, userID).
		Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TokenMetadataRecord{}, ErrIdentityNotFound
	}
	if err != nil {
		return TokenMetadataRecord{}, fmt.Errorf("find owned personal access token: %w", err)
	}
	return record, nil
}

func (repository *Repository) FindTokenRevealByIDAndUserID(ctx context.Context, id, userID string) (TokenRevealRecord, error) {
	var record TokenRevealRecord
	err := repository.db.WithContext(ctx).Model(&model.PersonalAccessToken{}).
		Select(tokenMetadataColumns()+", secret_plaintext").
		Where("id = ? AND user_id = ?", id, userID).
		Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TokenRevealRecord{}, ErrIdentityNotFound
	}
	if err != nil {
		return TokenRevealRecord{}, fmt.Errorf("reveal owned personal access token: %w", err)
	}
	return record, nil
}

func (repository *Repository) RevokeToken(ctx context.Context, id, userID string, revokedAt time.Time) error {
	result := repository.db.WithContext(ctx).Model(&model.PersonalAccessToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, userID).
		Updates(map[string]any{"revoked_at": revokedAt.UTC(), "updated_at": revokedAt.UTC()})
	if result.Error != nil {
		return fmt.Errorf("revoke personal access token: %w", result.Error)
	}
	return nil
}

func (repository *Repository) IsSystemAdmin(ctx context.Context, userID string) (bool, error) {
	var count int64
	err := repository.db.WithContext(ctx).Model(&model.UserGroupMembership{}).
		Where("user_id = ? AND group_id = ?", userID, model.AdminSystemGroupID).
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
	return user.Status == model.UserStatusActive, nil
}

func (repository *Repository) UpdateSystemGroup(ctx context.Context, id string, changes map[string]any) error {
	if model.IsFixedSystemGroup(id) {
		return model.ErrImmutableSystemGroup
	}
	if err := repository.db.WithContext(ctx).Model(&model.SystemGroup{ID: id}).Updates(changes).Error; err != nil {
		return fmt.Errorf("update system group: %w", err)
	}
	return nil
}

func (repository *Repository) DeleteSystemGroup(ctx context.Context, id string) error {
	if model.IsFixedSystemGroup(id) {
		return model.ErrImmutableSystemGroup
	}
	if err := repository.db.WithContext(ctx).Delete(&model.SystemGroup{ID: id}).Error; err != nil {
		return fmt.Errorf("delete system group: %w", err)
	}
	return nil
}

func (repository *Repository) TouchToken(ctx context.Context, id string, usedAt time.Time) error {
	usedAt = usedAt.UTC()
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := touchTokenUpdate(tx, id, usedAt)
		if result.Error != nil {
			return fmt.Errorf("update personal access token use: %w", result.Error)
		}
		if result.RowsAffected == 1 {
			return nil
		}
		active, err := validateTouchedToken(tx, id, usedAt)
		if err != nil {
			return fmt.Errorf("validate personal access token use: %w", err)
		}
		if !active {
			return ErrIdentityNotFound
		}
		return nil
	})
}

func touchTokenUpdate(tx *gorm.DB, id string, usedAt time.Time) *gorm.DB {
	return tx.Model(&model.PersonalAccessToken{}).
		Where("id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?) AND (last_used_at IS NULL OR last_used_at < ?)", id, usedAt, usedAt).
		Update("last_used_at", usedAt)
}

func validateTouchedToken(tx *gorm.DB, id string, usedAt time.Time) (bool, error) {
	var token model.PersonalAccessToken
	err := tx.Model(&model.PersonalAccessToken{}).
		Select("id").
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?) AND last_used_at >= ?", id, usedAt, usedAt).
		Take(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func tokenMetadataColumns() string {
	return "id, user_id, name, preset, expires_at, last_used_at, revoked_at, created_at"
}
