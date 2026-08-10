package identity

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"gorm.io/gorm"
)

const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"

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
	ErrInvalidTokenScope          = errors.New("identity: invalid token scope")
	ErrInvalidUser                = errors.New("identity: invalid user")
	ErrInvalidNamespace           = errors.New("identity: invalid namespace")
	ErrInvalidSystemGroup         = errors.New("identity: invalid fixed system group")
)

var canonicalIdentitySlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

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
	if user.Username != normalizeUsername(user.Username) || len(user.Username) > 64 || !canonicalIdentitySlug.MatchString(user.Username) {
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
	if namespace.Slug != normalizeUsername(namespace.Slug) || len(namespace.Slug) > 128 || !canonicalIdentitySlug.MatchString(namespace.Slug) {
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
	if isFixedSystemGroup(group.ID) {
		return ErrImmutableSystemGroup
	}
	return nil
}

func (group *SystemGroup) BeforeDelete(*gorm.DB) error {
	if isFixedSystemGroup(group.ID) {
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
	ID         string     `gorm:"type:char(36);primaryKey"`
	UserID     string     `gorm:"type:char(36);not null;index"`
	Name       string     `gorm:"size:128;not null"`
	SecretHMAC string     `gorm:"size:128;not null;uniqueIndex:uidx_personal_access_tokens_secret_hmac"`
	ExpiresAt  *time.Time `gorm:"index"`
	LastUsedAt *time.Time
	RevokedAt  *time.Time                 `gorm:"index"`
	CreatedAt  time.Time                  `gorm:"not null"`
	UpdatedAt  time.Time                  `gorm:"not null"`
	User       User                       `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:CASCADE"`
	Scopes     []PersonalAccessTokenScope `gorm:"foreignKey:TokenID;constraint:OnUpdate:RESTRICT,OnDelete:CASCADE"`
}

func (PersonalAccessToken) TableName() string { return "personal_access_tokens" }

type PersonalAccessTokenScope struct {
	TokenID string              `gorm:"type:char(36);primaryKey"`
	Action  auth.Action         `gorm:"size:128;primaryKey;check:chk_personal_access_token_scopes_action,action IN ('marketplace.read','plugin.read','repository.read','repository.write','token.read','token.write')"`
	Token   PersonalAccessToken `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:CASCADE"`
}

func (PersonalAccessTokenScope) TableName() string { return "personal_access_token_scopes" }

func (scope *PersonalAccessTokenScope) BeforeSave(*gorm.DB) error {
	if !isSupportedScope(scope.Action) {
		return ErrInvalidTokenScope
	}
	return nil
}

func isFixedSystemGroup(id string) bool {
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

func isSupportedScope(action auth.Action) bool {
	switch action {
	case auth.ActionMarketplaceRead,
		auth.ActionPluginRead,
		auth.ActionRepositoryRead,
		auth.ActionRepositoryWrite,
		auth.ActionTokenRead,
		auth.ActionTokenWrite:
		return true
	default:
		return false
	}
}
