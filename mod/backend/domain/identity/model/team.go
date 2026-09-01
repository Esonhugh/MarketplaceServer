package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	TeamRoleOwner      = "owner"
	TeamRoleAdmin      = "admin"
	TeamRoleMaintainer = "maintainer"
	TeamRoleDeveloper  = "developer"
	TeamRoleViewer     = "viewer"
)

var (
	ErrInvalidTeamMembership = errors.New("identity: invalid team membership")
	ErrInvalidTeamInvitation = errors.New("identity: invalid team invitation")
)

type TeamMembership struct {
	NamespaceID string    `gorm:"type:char(36);primaryKey"`
	UserID      string    `gorm:"type:char(36);primaryKey;index:idx_team_memberships_user"`
	Role        string    `gorm:"size:32;not null;index:idx_team_memberships_namespace_role;check:chk_team_memberships_role,role IN ('owner','admin','maintainer','developer','viewer')"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
	Namespace   Namespace `gorm:"foreignKey:NamespaceID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	User        User      `gorm:"foreignKey:UserID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (TeamMembership) TableName() string { return "team_memberships" }

func (membership *TeamMembership) BeforeCreate(*gorm.DB) error {
	if membership == nil || !isCanonicalUUID(membership.NamespaceID) || !isCanonicalUUID(membership.UserID) || !IsTeamRole(membership.Role) {
		return ErrInvalidTeamMembership
	}
	return nil
}

type TeamInvitation struct {
	ID              string     `gorm:"type:char(36);primaryKey"`
	NamespaceID     string     `gorm:"type:char(36);not null;index:idx_team_invitations_namespace_page"`
	UserID          string     `gorm:"type:char(36);not null;index:idx_team_invitations_user_inbox"`
	Role            string     `gorm:"size:32;not null;check:chk_team_invitations_role,role IN ('owner','admin','maintainer','developer','viewer')"`
	InvitedByUserID string     `gorm:"type:char(36);not null"`
	ExpiresAt       time.Time  `gorm:"not null;index:idx_team_invitations_user_inbox"`
	AcceptedAt      *time.Time `gorm:"check:chk_team_invitations_terminal,(CASE WHEN accepted_at IS NULL THEN 0 ELSE 1 END + CASE WHEN rejected_at IS NULL THEN 0 ELSE 1 END + CASE WHEN revoked_at IS NULL THEN 0 ELSE 1 END) <= 1"`
	RejectedAt      *time.Time
	RevokedAt       *time.Time
	CreatedAt       time.Time `gorm:"not null;index:idx_team_invitations_namespace_page"`
	UpdatedAt       time.Time `gorm:"not null"`
	Namespace       Namespace `gorm:"foreignKey:NamespaceID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	User            User      `gorm:"foreignKey:UserID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	InvitedBy       User      `gorm:"foreignKey:InvitedByUserID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (TeamInvitation) TableName() string { return "team_invitations" }

func (invitation *TeamInvitation) BeforeCreate(*gorm.DB) error {
	if invitation == nil || !isCanonicalUUID(invitation.ID) || !isCanonicalUUID(invitation.NamespaceID) || !isCanonicalUUID(invitation.UserID) || !isCanonicalUUID(invitation.InvitedByUserID) || !IsTeamRole(invitation.Role) || !invitation.ExpiresAt.After(invitation.CreatedAt) {
		return ErrInvalidTeamInvitation
	}
	return nil
}

func TeamMigrationModels() []any {
	return []any{&TeamMembership{}, &TeamInvitation{}}
}

func IsTeamRole(role string) bool {
	switch role {
	case TeamRoleOwner, TeamRoleAdmin, TeamRoleMaintainer, TeamRoleDeveloper, TeamRoleViewer:
		return true
	default:
		return false
	}
}
