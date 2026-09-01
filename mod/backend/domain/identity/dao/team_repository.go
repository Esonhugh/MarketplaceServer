package dao

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TeamRecord struct {
	ID, Slug, DisplayName, CurrentUserRole string
	CreatedAt, UpdatedAt                   time.Time
}

type TeamMemberRecord struct {
	UserID, Username, DisplayName, UserStatus, Role string
	CreatedAt, UpdatedAt                            time.Time
}

type TeamInvitationRecord struct {
	ID, NamespaceID, TeamSlug, TeamDisplayName string
	UserID, Username, UserDisplayName          string
	Role, InvitedByUserID, InviterUsername     string
	ExpiresAt                                  time.Time
	AcceptedAt, RejectedAt, RevokedAt          *time.Time
	CreatedAt, UpdatedAt                       time.Time
}

func (repository *Repository) CreateTeam(ctx context.Context, namespace model.Namespace, owner model.TeamMembership) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&namespace).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrIdentityConflict
			}
			return fmt.Errorf("create team namespace: %w", err)
		}
		if err := tx.Create(&owner).Error; err != nil {
			return fmt.Errorf("create team owner: %w", err)
		}
		return nil
	})
}

func teamSelect() string {
	return "namespaces.id, namespaces.slug, namespaces.display_name, COALESCE(team_memberships.role, '') AS current_user_role, namespaces.created_at, namespaces.updated_at"
}

func (repository *Repository) FindTeamBySlug(ctx context.Context, slug, userID string) (TeamRecord, error) {
	var row TeamRecord
	err := repository.db.WithContext(ctx).Table("namespaces").Select(teamSelect()).
		Joins("LEFT JOIN team_memberships ON team_memberships.namespace_id = namespaces.id AND team_memberships.user_id = ?", userID).
		Where("namespaces.kind = ? AND namespaces.slug = ?", model.NamespaceKindTeam, slug).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TeamRecord{}, ErrIdentityNotFound
	}
	if err != nil {
		return TeamRecord{}, fmt.Errorf("find team: %w", err)
	}
	return row, nil
}

func (repository *Repository) ListTeams(ctx context.Context, userID string, all bool, page, size int) ([]TeamRecord, int64, error) {
	if page < 1 || size < 1 {
		return nil, 0, ErrInvalidPagination
	}
	if page < 1 || size < 1 || page-1 > int(^uint(0)>>1)/size {
		return nil, 0, ErrInvalidPagination
	}
	base := repository.db.WithContext(ctx).Table("namespaces").Where("namespaces.kind = ?", model.NamespaceKindTeam)
	if !all {
		base = base.Joins("JOIN team_memberships scope_membership ON scope_membership.namespace_id = namespaces.id AND scope_membership.user_id = ?", userID)
	}
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count teams: %w", err)
	}
	var rows []TeamRecord
	query := base.Session(&gorm.Session{}).Select(teamSelect()).Joins("LEFT JOIN team_memberships ON team_memberships.namespace_id = namespaces.id AND team_memberships.user_id = ?", userID)
	if err := query.Order("namespaces.created_at DESC").Order("namespaces.id DESC").Limit(size).Offset((page - 1) * size).Scan(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("list teams: %w", err)
	}
	return rows, total, nil
}

func (repository *Repository) UpdateTeamDisplayName(ctx context.Context, namespaceID, displayName string, now time.Time) error {
	result := repository.db.WithContext(ctx).Model(&model.Namespace{}).Where("id = ? AND kind = ?", namespaceID, model.NamespaceKindTeam).Updates(map[string]any{"display_name": displayName, "updated_at": now.UTC()})
	if result.Error != nil {
		return fmt.Errorf("update team: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrIdentityNotFound
	}
	return nil
}

func (repository *Repository) TeamRole(ctx context.Context, namespaceID, userID string) (string, error) {
	var membership model.TeamMembership
	err := repository.db.WithContext(ctx).Where("namespace_id = ? AND user_id = ?", namespaceID, userID).Take(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrIdentityNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find team membership: %w", err)
	}
	return membership.Role, nil
}

func (repository *Repository) ListTeamMembers(ctx context.Context, namespaceID string, page, size int) ([]TeamMemberRecord, int64, error) {
	if page < 1 || size < 1 || page-1 > int(^uint(0)>>1)/size {
		return nil, 0, ErrInvalidPagination
	}
	base := repository.db.WithContext(ctx).Table("team_memberships").Where("team_memberships.namespace_id = ?", namespaceID)
	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []TeamMemberRecord
	err := base.Session(&gorm.Session{}).Select("users.id AS user_id, users.username, users.display_name, users.status AS user_status, team_memberships.role, team_memberships.created_at, team_memberships.updated_at").Joins("JOIN users ON users.id = team_memberships.user_id").Order("team_memberships.created_at ASC").Order("team_memberships.user_id ASC").Limit(size).Offset((page - 1) * size).Scan(&rows).Error
	return rows, total, err
}

func (repository *Repository) PutTeamMember(ctx context.Context, namespaceID, userID, role string, now time.Time) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTeamMemberships(tx, namespaceID); err != nil {
			return err
		}
		var user model.User
		if err := tx.Where("id = ? AND status = ?", userID, model.UserStatusActive).Take(&user).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIdentityNotFound
		} else if err != nil {
			return err
		}
		var current model.TeamMembership
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("namespace_id = ? AND user_id = ?", namespaceID, userID).Take(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(&model.TeamMembership{NamespaceID: namespaceID, UserID: userID, Role: role, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}).Error
		}
		if err != nil {
			return err
		}
		if current.Role == role {
			return nil
		}
		if current.Role == model.TeamRoleOwner && role != model.TeamRoleOwner {
			if err := ensureOtherOwner(tx, namespaceID, userID); err != nil {
				return err
			}
		}
		return tx.Model(&current).Updates(map[string]any{"role": role, "updated_at": now.UTC()}).Error
	})
}

func (repository *Repository) RemoveTeamMember(ctx context.Context, namespaceID, userID string) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockTeamMemberships(tx, namespaceID); err != nil {
			return err
		}
		var current model.TeamMembership
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("namespace_id = ? AND user_id = ?", namespaceID, userID).Take(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIdentityNotFound
		}
		if err != nil {
			return err
		}
		if current.Role == model.TeamRoleOwner {
			if err := ensureOtherOwner(tx, namespaceID, userID); err != nil {
				return err
			}
		}
		return tx.Delete(&current).Error
	})
}

func lockTeamMemberships(tx *gorm.DB, namespaceID string) error {
	if tx.Dialector.Name() == "postgres" {
		return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "team-membership:"+namespaceID).Error
	}
	// SQLite serializes writers. Touching membership rows acquires the write lock
	// before owner state is read, so concurrent owner mutations cannot both pass.
	if tx.Dialector.Name() == "sqlite" {
		return tx.Exec("UPDATE team_memberships SET updated_at = updated_at WHERE namespace_id = ?", namespaceID).Error
	}
	return nil
}

func ensureOtherOwner(tx *gorm.DB, namespaceID, excludedUserID string) error {
	var count int64
	if err := tx.Model(&model.TeamMembership{}).Where("namespace_id = ? AND role = ? AND user_id <> ?", namespaceID, model.TeamRoleOwner, excludedUserID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrIdentityConflict
	}
	return nil
}

func (repository *Repository) CreateTeamInvitation(ctx context.Context, invitation model.TeamInvitation) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Expiry is derived, but the partial uniqueness predicate cannot safely use
		// database current time. Terminalize stale rows in the same transaction
		// before inserting a fresh pending invitation.
		if err := tx.Model(&model.TeamInvitation{}).
			Where("namespace_id = ? AND user_id = ? AND accepted_at IS NULL AND rejected_at IS NULL AND revoked_at IS NULL AND expires_at <= ?", invitation.NamespaceID, invitation.UserID, invitation.CreatedAt).
			Updates(map[string]any{"revoked_at": invitation.CreatedAt.UTC(), "updated_at": invitation.CreatedAt.UTC()}).Error; err != nil {
			return fmt.Errorf("terminalize expired invitation: %w", err)
		}
		if err := tx.Create(&invitation).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrIdentityConflict
			}
			return fmt.Errorf("create invitation: %w", err)
		}
		return nil
	})
}

func invitationSelect() string {
	return "team_invitations.id, team_invitations.namespace_id, namespaces.slug AS team_slug, namespaces.display_name AS team_display_name, team_invitations.user_id, users.username, users.display_name AS user_display_name, team_invitations.role, team_invitations.invited_by_user_id, inviters.username AS inviter_username, team_invitations.expires_at, team_invitations.accepted_at, team_invitations.rejected_at, team_invitations.revoked_at, team_invitations.created_at, team_invitations.updated_at"
}
func (repository *Repository) invitationQuery(ctx context.Context) *gorm.DB {
	return repository.db.WithContext(ctx).Table("team_invitations").Select(invitationSelect()).Joins("JOIN namespaces ON namespaces.id = team_invitations.namespace_id").Joins("JOIN users ON users.id = team_invitations.user_id").Joins("JOIN users AS inviters ON inviters.id = team_invitations.invited_by_user_id")
}

func (repository *Repository) ListTeamInvitations(ctx context.Context, namespaceID string, page, size int) ([]TeamInvitationRecord, int64, error) {
	if page < 1 || size < 1 || page-1 > int(^uint(0)>>1)/size {
		return nil, 0, ErrInvalidPagination
	}
	base := repository.db.WithContext(ctx).Model(&model.TeamInvitation{}).Where("namespace_id = ?", namespaceID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []TeamInvitationRecord
	err := repository.invitationQuery(ctx).Where("team_invitations.namespace_id = ?", namespaceID).Order("team_invitations.created_at DESC").Order("team_invitations.id DESC").Limit(size).Offset((page - 1) * size).Scan(&rows).Error
	return rows, total, err
}
func (repository *Repository) ListUserPendingInvitations(ctx context.Context, userID string, now time.Time, page, size int) ([]TeamInvitationRecord, int64, error) {
	if page < 1 || size < 1 || page-1 > int(^uint(0)>>1)/size {
		return nil, 0, ErrInvalidPagination
	}
	base := repository.db.WithContext(ctx).Model(&model.TeamInvitation{}).Where("user_id = ? AND accepted_at IS NULL AND rejected_at IS NULL AND revoked_at IS NULL AND expires_at > ?", userID, now)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []TeamInvitationRecord
	err := repository.invitationQuery(ctx).Where("team_invitations.user_id = ? AND accepted_at IS NULL AND rejected_at IS NULL AND revoked_at IS NULL AND expires_at > ?", userID, now).Order("team_invitations.created_at DESC").Limit(size).Offset((page - 1) * size).Scan(&rows).Error
	return rows, total, err
}
func (repository *Repository) FindInvitation(ctx context.Context, namespaceID, id string) (TeamInvitationRecord, error) {
	var row TeamInvitationRecord
	err := repository.invitationQuery(ctx).Where("team_invitations.namespace_id = ? AND team_invitations.id = ?", namespaceID, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, ErrIdentityNotFound
	}
	return row, err
}

func (repository *Repository) RespondInvitation(ctx context.Context, id, userID string, accept bool, now time.Time) (string, error) {
	var namespaceID string
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var invitation model.TeamInvitation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).Take(&invitation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIdentityNotFound
		}
		if err != nil {
			return err
		}
		namespaceID = invitation.NamespaceID
		if invitation.AcceptedAt != nil || invitation.RejectedAt != nil || invitation.RevokedAt != nil || !invitation.ExpiresAt.After(now) {
			return ErrIdentityConflict
		}
		var activeUsers int64
		if err := tx.Model(&model.User{}).Where("id = ? AND status = ?", userID, model.UserStatusActive).Count(&activeUsers).Error; err != nil {
			return err
		}
		if activeUsers != 1 {
			return ErrIdentityConflict
		}
		if accept {
			if err := tx.Create(&model.TeamMembership{NamespaceID: invitation.NamespaceID, UserID: userID, Role: invitation.Role, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}).Error; err != nil {
				return ErrIdentityConflict
			}
			return tx.Model(&invitation).Updates(map[string]any{"accepted_at": now.UTC(), "updated_at": now.UTC()}).Error
		}
		return tx.Model(&invitation).Updates(map[string]any{"rejected_at": now.UTC(), "updated_at": now.UTC()}).Error
	})
	return namespaceID, err
}
func (repository *Repository) RevokeInvitation(ctx context.Context, namespaceID, id string, now time.Time) error {
	result := repository.db.WithContext(ctx).Model(&model.TeamInvitation{}).Where("namespace_id = ? AND id = ? AND accepted_at IS NULL AND rejected_at IS NULL AND revoked_at IS NULL", namespaceID, id).Updates(map[string]any{"revoked_at": now.UTC(), "updated_at": now.UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := repository.db.WithContext(ctx).Model(&model.TeamInvitation{}).Where("namespace_id = ? AND id = ?", namespaceID, id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrIdentityNotFound
		}
	}
	return nil
}

func (repository *Repository) ReissueInvitation(ctx context.Context, namespaceID, id, newID string, now, expiresAt time.Time) (model.TeamInvitation, error) {
	var replacement model.TeamInvitation
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old model.TeamInvitation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("namespace_id = ? AND id = ?", namespaceID, id).Take(&old).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrIdentityNotFound
		} else if err != nil {
			return err
		}
		var user model.User
		if err := tx.Where("id = ? AND status = ?", old.UserID, model.UserStatusActive).Take(&user).Error; err != nil {
			return ErrIdentityConflict
		}
		var membership int64
		if err := tx.Model(&model.TeamMembership{}).Where("namespace_id = ? AND user_id = ?", namespaceID, old.UserID).Count(&membership).Error; err != nil {
			return err
		}
		if membership > 0 {
			return ErrIdentityConflict
		}
		if old.AcceptedAt == nil && old.RejectedAt == nil && old.RevokedAt == nil {
			if err := tx.Model(&old).Updates(map[string]any{"revoked_at": now.UTC(), "updated_at": now.UTC()}).Error; err != nil {
				return err
			}
		}
		replacement = model.TeamInvitation{ID: newID, NamespaceID: namespaceID, UserID: old.UserID, Role: old.Role, InvitedByUserID: old.InvitedByUserID, ExpiresAt: expiresAt.UTC(), CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
		if err := tx.Create(&replacement).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrIdentityConflict
			}
			return err
		}
		return nil
	})
	return replacement, err
}
