package model

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"

	TokenPresetSubscriptionRead = "sub-read"
	TokenPresetGitClone         = "git-clone"
	TokenPresetGitWrite         = "git-write"

	NamespaceKindUser = "user"
	NamespaceKindTeam = "team"

	AdminSystemGroupID     = "00000000-0000-4000-8000-000000000001"
	DefaultSystemGroupID   = "00000000-0000-4000-8000-000000000002"
	AdminSystemGroupName   = "admin"
	DefaultSystemGroupName = "default"
)

var (
	ErrImmutableSystemGroup       = errors.New("identity: system groups are immutable")
	ErrPersistedDefaultMembership = errors.New("identity: default group membership is dynamic and must not be persisted")
	ErrInvalidTokenPreset         = errors.New("identity: invalid token preset")
	ErrInvalidPersonalAccessToken = errors.New("identity: invalid personal access token")
	ErrInvalidUser                = errors.New("identity: invalid user")
	ErrInvalidNamespace           = errors.New("identity: invalid namespace")
	ErrInvalidSystemGroup         = errors.New("identity: invalid fixed system group")
)

var canonicalIdentitySlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var migrationModels = []any{
	&User{},
	&Namespace{},
	&SystemGroup{},
	&UserGroupMembership{},
	&PersonalAccessToken{},
}

func MigrationModels() []any {
	return append([]any(nil), migrationModels...)
}

type User struct {
	ID           string    `gorm:"type:char(36);primaryKey"`
	Username     string    `gorm:"size:64;not null;uniqueIndex:uidx_users_username;check:chk_users_username_canonical,username = LOWER(TRIM(username))"`
	Email        *string   `gorm:"size:320;uniqueIndex:uidx_users_email;check:chk_users_email_canonical,email IS NULL OR email = LOWER(TRIM(email))"`
	DisplayName  string    `gorm:"size:255;not null"`
	Status       string    `gorm:"size:32;not null;index;check:chk_users_status,status IN ('active','disabled')"`
	PasswordHash string    `gorm:"size:512;not null"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

func (User) TableName() string { return "users" }

func (user *User) BeforeCreate(*gorm.DB) error {
	if user.Username != NormalizeUsername(user.Username) || len(user.Username) > 64 || !canonicalIdentitySlug.MatchString(user.Username) {
		return ErrInvalidUser
	}
	if user.Status != UserStatusActive && user.Status != UserStatusDisabled {
		return ErrInvalidUser
	}
	if user.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*user.Email))
		if normalized == "" || normalized != *user.Email {
			return ErrInvalidUser
		}
	}
	return nil
}

type Namespace struct {
	ID          string    `gorm:"type:char(36);primaryKey"`
	Kind        string    `gorm:"size:32;not null;check:chk_namespaces_kind,kind IN ('user','team')"`
	Slug        string    `gorm:"size:128;not null;uniqueIndex:uidx_namespaces_slug;check:chk_namespaces_slug_canonical,slug = LOWER(TRIM(slug))"`
	DisplayName string    `gorm:"size:255;not null"`
	OwnerUserID *string   `gorm:"type:char(36);uniqueIndex:uidx_namespaces_owner_user;check:chk_namespaces_owner_kind,(kind = 'user' AND owner_user_id IS NOT NULL) OR (kind = 'team' AND owner_user_id IS NULL)"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
	OwnerUser   *User     `gorm:"foreignKey:OwnerUserID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (Namespace) TableName() string { return "namespaces" }

func (namespace *Namespace) BeforeCreate(*gorm.DB) error {
	if namespace.Slug != NormalizeUsername(namespace.Slug) || len(namespace.Slug) > 128 || !canonicalIdentitySlug.MatchString(namespace.Slug) {
		return ErrInvalidNamespace
	}
	switch namespace.Kind {
	case NamespaceKindUser:
		if namespace.OwnerUserID == nil || *namespace.OwnerUserID == "" {
			return ErrInvalidNamespace
		}
	case NamespaceKindTeam:
		if namespace.OwnerUserID != nil {
			return ErrInvalidNamespace
		}
	default:
		return ErrInvalidNamespace
	}
	return nil
}

type SystemGroup struct {
	ID          string    `gorm:"type:char(36);primaryKey;check:chk_system_groups_id,id IN ('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002')"`
	Name        string    `gorm:"size:64;not null;uniqueIndex:uidx_system_groups_name;check:chk_system_groups_name,(id = '00000000-0000-4000-8000-000000000001' AND name = 'admin') OR (id = '00000000-0000-4000-8000-000000000002' AND name = 'default')"`
	Description string    `gorm:"size:255;not null;check:chk_system_groups_description,(id = '00000000-0000-4000-8000-000000000001' AND description = 'Global system administrators') OR (id = '00000000-0000-4000-8000-000000000002' AND description = 'All active users')"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (SystemGroup) TableName() string { return "system_groups" }

func (group *SystemGroup) BeforeCreate(*gorm.DB) error {
	if !isFixedSystemGroupDefinition(*group) {
		return ErrInvalidSystemGroup
	}
	return nil
}

func (group *SystemGroup) BeforeUpdate(*gorm.DB) error {
	if IsFixedSystemGroup(group.ID) {
		return ErrImmutableSystemGroup
	}
	return nil
}

func (group *SystemGroup) BeforeDelete(*gorm.DB) error {
	if IsFixedSystemGroup(group.ID) {
		return ErrImmutableSystemGroup
	}
	return nil
}

type UserGroupMembership struct {
	UserID    string      `gorm:"type:char(36);primaryKey"`
	GroupID   string      `gorm:"type:char(36);primaryKey;check:chk_memberships_not_default,group_id <> '00000000-0000-4000-8000-000000000002'"`
	CreatedAt time.Time   `gorm:"not null"`
	User      User        `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:CASCADE"`
	Group     SystemGroup `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (UserGroupMembership) TableName() string { return "user_group_memberships" }

func (membership *UserGroupMembership) BeforeCreate(*gorm.DB) error {
	if membership.GroupID == DefaultSystemGroupID {
		return ErrPersistedDefaultMembership
	}
	return nil
}

func (membership *UserGroupMembership) BeforeUpdate(*gorm.DB) error {
	if membership.GroupID == DefaultSystemGroupID {
		return ErrPersistedDefaultMembership
	}
	return nil
}

type PersonalAccessToken struct {
	ID              string     `gorm:"type:char(36);primaryKey"`
	UserID          string     `gorm:"type:char(36);not null;index:idx_personal_access_tokens_owner_page,priority:1"`
	Name            string     `gorm:"size:128;not null"`
	Preset          string     `gorm:"size:32;not null;check:chk_personal_access_tokens_preset,preset IN ('sub-read','git-clone','git-write')"`
	SecretPlaintext string     `gorm:"size:64;not null"`
	SecretHMAC      string     `gorm:"size:128;not null;uniqueIndex:uidx_personal_access_tokens_secret_hmac"`
	ExpiresAt       *time.Time `gorm:"index;check:chk_personal_access_tokens_expiry,expires_at IS NULL OR expires_at > created_at"`
	LastUsedAt      *time.Time
	RevokedAt       *time.Time `gorm:"index"`
	CreatedAt       time.Time  `gorm:"not null;index:idx_personal_access_tokens_owner_page,priority:2,sort:desc"`
	UpdatedAt       time.Time  `gorm:"not null"`
	User            User       `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:CASCADE"`
}

func (PersonalAccessToken) TableName() string { return "personal_access_tokens" }

func (token *PersonalAccessToken) BeforeCreate(*gorm.DB) error {
	if token == nil || !isCanonicalUUID(token.ID) || !isCanonicalUUID(token.UserID) ||
		auth.ValidateAPIKey(token.SecretPlaintext) != nil || auth.ValidateAPIKeyIndex(token.SecretHMAC) != nil {
		return ErrInvalidPersonalAccessToken
	}
	if !IsSupportedPreset(token.Preset) {
		return ErrInvalidTokenPreset
	}
	if token.ExpiresAt != nil && !token.ExpiresAt.After(token.CreatedAt) {
		return ErrInvalidPersonalAccessToken
	}
	return nil
}

func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func isCanonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func IsFixedSystemGroup(id string) bool {
	return id == AdminSystemGroupID || id == DefaultSystemGroupID
}

func isFixedSystemGroupDefinition(group SystemGroup) bool {
	switch group.ID {
	case AdminSystemGroupID:
		return group.Name == AdminSystemGroupName && group.Description == "Global system administrators"
	case DefaultSystemGroupID:
		return group.Name == DefaultSystemGroupName && group.Description == "All active users"
	default:
		return false
	}
}

func IsSupportedPreset(preset string) bool {
	switch preset {
	case TokenPresetSubscriptionRead, TokenPresetGitClone, TokenPresetGitWrite:
		return true
	default:
		return false
	}
}
