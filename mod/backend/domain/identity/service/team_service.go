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

const invitationLifetime = 7 * 24 * time.Hour

var (
	ErrInvalidTeamInput = errors.New("identity: invalid team input")
	ErrTeamNotFound     = errors.New("identity: team not found")
	ErrTeamForbidden    = errors.New("identity: team permission denied")
	ErrTeamConflict     = errors.New("identity: team conflict")
)

type TeamStore interface {
	CreateTeam(context.Context, model.Namespace, model.TeamMembership) error
	FindTeamBySlug(context.Context, string, string) (dao.TeamRecord, error)
	ListTeams(context.Context, string, bool, int, int) ([]dao.TeamRecord, int64, error)
	UpdateTeamDisplayName(context.Context, string, string, time.Time) error
	TeamRole(context.Context, string, string) (string, error)
	ListTeamMembers(context.Context, string, int, int) ([]dao.TeamMemberRecord, int64, error)
	PutTeamMember(context.Context, string, string, string, time.Time) error
	RemoveTeamMember(context.Context, string, string) error
	FindUserByUsername(context.Context, string) (model.User, error)
	FindUserByID(context.Context, string) (model.User, error)
	IsSystemAdmin(context.Context, string) (bool, error)
	CreateTeamInvitation(context.Context, model.TeamInvitation) error
	ListTeamInvitations(context.Context, string, int, int) ([]dao.TeamInvitationRecord, int64, error)
	ListUserPendingInvitations(context.Context, string, time.Time, int, int) ([]dao.TeamInvitationRecord, int64, error)
	FindInvitation(context.Context, string, string) (dao.TeamInvitationRecord, error)
	RespondInvitation(context.Context, string, string, bool, time.Time) (string, error)
	RevokeInvitation(context.Context, string, string, time.Time) error
	ReissueInvitation(context.Context, string, string, string, time.Time, time.Time) (model.TeamInvitation, error)
}

type TeamPermissions struct {
	ReadMembers    bool `json:"readMembers"`
	ManageMembers  bool `json:"manageMembers"`
	UpdateSettings bool `json:"updateSettings"`
	ManageOwners   bool `json:"manageOwners"`
}
type Team struct {
	ID              string          `json:"id"`
	Slug            string          `json:"slug"`
	DisplayName     string          `json:"displayName"`
	CurrentUserRole *string         `json:"currentUserRole"`
	Permissions     TeamPermissions `json:"permissions"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}
type TeamPage struct {
	Items []Team `json:"items"`
	Page  int    `json:"page"`
	Size  int    `json:"size"`
	Total int64  `json:"total"`
}
type TeamMember struct {
	UserID      string    `json:"userId"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Status      string    `json:"status"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}
type TeamMemberPage struct {
	Items []TeamMember `json:"items"`
	Page  int          `json:"page"`
	Size  int          `json:"size"`
	Total int64        `json:"total"`
}
type TeamInvitation struct {
	ID              string     `json:"id"`
	TeamID          string     `json:"teamId"`
	TeamSlug        string     `json:"teamSlug"`
	TeamDisplayName string     `json:"teamDisplayName"`
	UserID          string     `json:"userId"`
	Username        string     `json:"username"`
	UserDisplayName string     `json:"userDisplayName"`
	Role            string     `json:"role"`
	InvitedByUserID string     `json:"invitedByUserId"`
	InviterUsername string     `json:"inviterUsername"`
	Status          string     `json:"status"`
	ExpiresAt       time.Time  `json:"expiresAt"`
	AcceptedAt      *time.Time `json:"acceptedAt"`
	RejectedAt      *time.Time `json:"rejectedAt"`
	RevokedAt       *time.Time `json:"revokedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}
type TeamInvitationPage struct {
	Items []TeamInvitation `json:"items"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
	Total int64            `json:"total"`
}

type TeamService struct {
	repository TeamStore
	now        func() time.Time
	newID      func() string
	log        *zap.Logger
}

func NewTeamService(repository TeamStore) (*TeamService, error) {
	if repository == nil {
		return nil, errors.New("team service requires repository")
	}
	return &TeamService{repository: repository, now: time.Now, newID: uuid.NewString, log: zap.L()}, nil
}

func (s *TeamService) principal(p auth.Principal) error {
	if !p.IsUser() || p.CredentialKind() != auth.CredentialJWT || p.UserID() == "" {
		return ErrTeamForbidden
	}
	return nil
}
func validPage(page, size int) bool     { return page > 0 && size > 0 && size <= 100 }
func normalizeTeamSlug(v string) string { return model.NormalizeUsername(v) }
func validTeamText(v string, max int) bool {
	return strings.TrimSpace(v) != "" && utf8.RuneCountInString(strings.TrimSpace(v)) <= max
}

func (s *TeamService) Create(ctx context.Context, p auth.Principal, slug, displayName string) (Team, error) {
	if s.principal(p) != nil {
		return Team{}, ErrTeamForbidden
	}
	slug = normalizeTeamSlug(slug)
	displayName = strings.TrimSpace(displayName)
	if !identitySlug(slug) || len(slug) > 128 || !validTeamText(displayName, 255) {
		return Team{}, ErrInvalidTeamInput
	}
	now := s.now().UTC()
	id := s.newID()
	ns := model.Namespace{ID: id, Kind: model.NamespaceKindTeam, Slug: slug, DisplayName: displayName, CreatedAt: now, UpdatedAt: now}
	membership := model.TeamMembership{NamespaceID: id, UserID: p.UserID(), Role: model.TeamRoleOwner, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateTeam(ctx, ns, membership); err != nil {
		if errors.Is(err, dao.ErrIdentityConflict) {
			return Team{}, ErrTeamConflict
		}
		return Team{}, fmt.Errorf("create team: %w", err)
	}
	role := model.TeamRoleOwner
	s.logMutation(p, "team.create", id, "team", "success", "")
	return Team{ID: id, Slug: slug, DisplayName: displayName, CurrentUserRole: &role, Permissions: permissions(role, false), CreatedAt: now, UpdatedAt: now}, nil
}
func (s *TeamService) List(ctx context.Context, p auth.Principal, page, size int, all bool) (TeamPage, error) {
	if s.principal(p) != nil {
		return TeamPage{}, ErrTeamForbidden
	}
	if !validPage(page, size) {
		return TeamPage{}, ErrInvalidTeamInput
	}
	admin, err := s.repository.IsSystemAdmin(ctx, p.UserID())
	if err != nil {
		return TeamPage{}, err
	}
	if all && !admin {
		return TeamPage{}, ErrTeamForbidden
	}
	rows, total, err := s.repository.ListTeams(ctx, p.UserID(), all, page, size)
	if err != nil {
		return TeamPage{}, err
	}
	items := make([]Team, 0, len(rows))
	for _, r := range rows {
		items = append(items, teamFromRecord(r, admin))
	}
	return TeamPage{Items: items, Page: page, Size: size, Total: total}, nil
}
func (s *TeamService) Get(ctx context.Context, p auth.Principal, slug string) (Team, error) {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return Team{}, err
	}
	if r.CurrentUserRole == "" && !admin {
		return Team{}, ErrTeamNotFound
	}
	return teamFromRecord(r, admin), nil
}
func (s *TeamService) Update(ctx context.Context, p auth.Principal, slug, displayName string) (Team, error) {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return Team{}, err
	}
	if !admin && r.CurrentUserRole != model.TeamRoleOwner && r.CurrentUserRole != model.TeamRoleAdmin {
		return Team{}, ErrTeamForbidden
	}
	displayName = strings.TrimSpace(displayName)
	if !validTeamText(displayName, 255) {
		return Team{}, ErrInvalidTeamInput
	}
	now := s.now().UTC()
	if err := s.repository.UpdateTeamDisplayName(ctx, r.ID, displayName, now); err != nil {
		return Team{}, err
	}
	r.DisplayName = displayName
	r.UpdatedAt = now
	s.logMutation(p, "team.settings.write", r.ID, "team", "success", "")
	return teamFromRecord(r, admin), nil
}
func (s *TeamService) resolve(ctx context.Context, p auth.Principal, slug string) (dao.TeamRecord, bool, error) {
	if s.principal(p) != nil {
		return dao.TeamRecord{}, false, ErrTeamForbidden
	}
	slug = normalizeTeamSlug(slug)
	if !identitySlug(slug) {
		return dao.TeamRecord{}, false, ErrTeamNotFound
	}
	r, err := s.repository.FindTeamBySlug(ctx, slug, p.UserID())
	if errors.Is(err, dao.ErrIdentityNotFound) {
		return r, false, ErrTeamNotFound
	}
	if err != nil {
		return r, false, err
	}
	admin, err := s.repository.IsSystemAdmin(ctx, p.UserID())
	return r, admin, err
}
func teamFromRecord(r dao.TeamRecord, admin bool) Team {
	var role *string
	if r.CurrentUserRole != "" {
		v := r.CurrentUserRole
		role = &v
	}
	return Team{ID: r.ID, Slug: r.Slug, DisplayName: r.DisplayName, CurrentUserRole: role, Permissions: permissions(r.CurrentUserRole, admin), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func permissions(role string, admin bool) TeamPermissions {
	if admin {
		return TeamPermissions{true, true, true, true}
	}
	return TeamPermissions{ReadMembers: role != "", ManageMembers: role == model.TeamRoleOwner || role == model.TeamRoleAdmin, UpdateSettings: role == model.TeamRoleOwner || role == model.TeamRoleAdmin, ManageOwners: role == model.TeamRoleOwner}
}

func (s *TeamService) ListMembers(ctx context.Context, p auth.Principal, slug string, page, size int) (TeamMemberPage, error) {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return TeamMemberPage{}, err
	}
	if r.CurrentUserRole == "" && !admin {
		return TeamMemberPage{}, ErrTeamForbidden
	}
	if !validPage(page, size) {
		return TeamMemberPage{}, ErrInvalidTeamInput
	}
	rows, total, err := s.repository.ListTeamMembers(ctx, r.ID, page, size)
	if err != nil {
		return TeamMemberPage{}, err
	}
	items := make([]TeamMember, 0, len(rows))
	for _, m := range rows {
		items = append(items, TeamMember{m.UserID, m.Username, m.DisplayName, m.UserStatus, m.Role, m.CreatedAt, m.UpdatedAt})
	}
	return TeamMemberPage{items, page, size, total}, nil
}
func (s *TeamService) PutMember(ctx context.Context, p auth.Principal, slug, userID, role string) (TeamMember, error) {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return TeamMember{}, err
	}
	if !model.IsTeamRole(role) {
		return TeamMember{}, ErrInvalidTeamInput
	}
	if !canManageRole(r.CurrentUserRole, admin, role) {
		return TeamMember{}, ErrTeamForbidden
	}
	if current, e := s.repository.TeamRole(ctx, r.ID, userID); e == nil && !canManageRole(r.CurrentUserRole, admin, current) {
		return TeamMember{}, ErrTeamForbidden
	}
	now := s.now().UTC()
	if err := s.repository.PutTeamMember(ctx, r.ID, userID, role, now); err != nil {
		return TeamMember{}, mapTeamError(err)
	}
	u, err := s.repository.FindUserByID(ctx, userID)
	if err != nil {
		return TeamMember{}, mapTeamError(err)
	}
	s.logMutation(p, "team.members.manage", userID, "team_membership", "success", "")
	return TeamMember{u.ID, u.Username, u.DisplayName, u.Status, role, now, now}, nil
}
func (s *TeamService) RemoveMember(ctx context.Context, p auth.Principal, slug, userID string) error {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return err
	}
	role, err := s.repository.TeamRole(ctx, r.ID, userID)
	if err != nil {
		return mapTeamError(err)
	}
	if !canManageRole(r.CurrentUserRole, admin, role) {
		return ErrTeamForbidden
	}
	if err := mapTeamError(s.repository.RemoveTeamMember(ctx, r.ID, userID)); err != nil {
		return err
	}
	s.logMutation(p, "team.members.manage", userID, "team_membership", "success", "")
	return nil
}
func canManageRole(actor string, system bool, target string) bool {
	if system {
		return true
	}
	if actor == model.TeamRoleOwner {
		return true
	}
	return actor == model.TeamRoleAdmin && target != model.TeamRoleOwner
}

func (s *TeamService) Invite(ctx context.Context, p auth.Principal, slug, username, role string) (TeamInvitation, error) {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return TeamInvitation{}, err
	}
	if !model.IsTeamRole(role) {
		return TeamInvitation{}, ErrInvalidTeamInput
	}
	if !canManageRole(r.CurrentUserRole, admin, role) {
		return TeamInvitation{}, ErrTeamForbidden
	}
	user, err := s.repository.FindUserByUsername(ctx, username)
	if err != nil || user.Status != model.UserStatusActive {
		return TeamInvitation{}, ErrTeamNotFound
	}
	if _, err = s.repository.TeamRole(ctx, r.ID, user.ID); err == nil {
		return TeamInvitation{}, ErrTeamConflict
	} else if !errors.Is(err, dao.ErrIdentityNotFound) {
		return TeamInvitation{}, err
	}
	now := s.now().UTC()
	m := model.TeamInvitation{ID: s.newID(), NamespaceID: r.ID, UserID: user.ID, Role: role, InvitedByUserID: p.UserID(), ExpiresAt: now.Add(invitationLifetime), CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CreateTeamInvitation(ctx, m); err != nil {
		return TeamInvitation{}, mapTeamError(err)
	}
	inviter, _ := s.repository.FindUserByID(ctx, p.UserID())
	s.logMutation(p, "team.invitations.manage", m.ID, "team_invitation", "success", "")
	return invitationFromModel(m, r, user, inviter, now), nil
}
func (s *TeamService) ListInvitations(ctx context.Context, p auth.Principal, slug string, page, size int) (TeamInvitationPage, error) {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return TeamInvitationPage{}, err
	}
	if r.CurrentUserRole != model.TeamRoleOwner && r.CurrentUserRole != model.TeamRoleAdmin && !admin {
		return TeamInvitationPage{}, ErrTeamForbidden
	}
	if !validPage(page, size) {
		return TeamInvitationPage{}, ErrInvalidTeamInput
	}
	rows, total, err := s.repository.ListTeamInvitations(ctx, r.ID, page, size)
	return invitationPage(rows, page, size, total, s.now().UTC()), err
}
func (s *TeamService) Inbox(ctx context.Context, p auth.Principal, page, size int) (TeamInvitationPage, error) {
	if s.principal(p) != nil {
		return TeamInvitationPage{}, ErrTeamForbidden
	}
	if !validPage(page, size) {
		return TeamInvitationPage{}, ErrInvalidTeamInput
	}
	now := s.now().UTC()
	rows, total, err := s.repository.ListUserPendingInvitations(ctx, p.UserID(), now, page, size)
	return invitationPage(rows, page, size, total, now), err
}
func (s *TeamService) Respond(ctx context.Context, p auth.Principal, id string, accept bool) (*Team, error) {
	if s.principal(p) != nil {
		return nil, ErrTeamForbidden
	}
	if !canonicalUUID(id) {
		return nil, ErrTeamNotFound
	}
	namespaceID, err := s.repository.RespondInvitation(ctx, id, p.UserID(), accept, s.now().UTC())
	if err != nil {
		return nil, mapTeamError(err)
	}
	s.logMutation(p, "team.invitation.respond", id, "team_invitation", "success", "")
	if !accept {
		return nil, nil
	}
	rows, _, err := s.repository.ListTeams(ctx, p.UserID(), false, 1, 100)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.ID == namespaceID {
			t := teamFromRecord(r, false)
			return &t, nil
		}
	}
	return nil, ErrTeamNotFound
}
func (s *TeamService) Revoke(ctx context.Context, p auth.Principal, slug, id string) error {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return err
	}
	if r.CurrentUserRole != model.TeamRoleOwner && r.CurrentUserRole != model.TeamRoleAdmin && !admin {
		return ErrTeamForbidden
	}
	invitation, err := s.repository.FindInvitation(ctx, r.ID, id)
	if err != nil {
		return mapTeamError(err)
	}
	if !canManageRole(r.CurrentUserRole, admin, invitation.Role) {
		return ErrTeamForbidden
	}
	if err := mapTeamError(s.repository.RevokeInvitation(ctx, r.ID, id, s.now().UTC())); err != nil {
		return err
	}
	s.logMutation(p, "team.invitations.manage", id, "team_invitation", "success", "")
	return nil
}
func (s *TeamService) Reissue(ctx context.Context, p auth.Principal, slug, id string) (TeamInvitation, error) {
	r, admin, err := s.resolve(ctx, p, slug)
	if err != nil {
		return TeamInvitation{}, err
	}
	if r.CurrentUserRole != model.TeamRoleOwner && r.CurrentUserRole != model.TeamRoleAdmin && !admin {
		return TeamInvitation{}, ErrTeamForbidden
	}
	invitation, err := s.repository.FindInvitation(ctx, r.ID, id)
	if err != nil {
		return TeamInvitation{}, mapTeamError(err)
	}
	if !canManageRole(r.CurrentUserRole, admin, invitation.Role) {
		return TeamInvitation{}, ErrTeamForbidden
	}
	now := s.now().UTC()
	m, err := s.repository.ReissueInvitation(ctx, r.ID, id, s.newID(), now, now.Add(invitationLifetime))
	if err != nil {
		return TeamInvitation{}, mapTeamError(err)
	}
	user, _ := s.repository.FindUserByID(ctx, m.UserID)
	inviter, _ := s.repository.FindUserByID(ctx, m.InvitedByUserID)
	return invitationFromModel(m, r, user, inviter, now), nil
}
func invitationFromModel(m model.TeamInvitation, r dao.TeamRecord, u, i model.User, now time.Time) TeamInvitation {
	return TeamInvitation{ID: m.ID, TeamID: r.ID, TeamSlug: r.Slug, TeamDisplayName: r.DisplayName, UserID: u.ID, Username: u.Username, UserDisplayName: u.DisplayName, Role: m.Role, InvitedByUserID: i.ID, InviterUsername: i.Username, Status: "pending", ExpiresAt: m.ExpiresAt, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}
func invitationPage(rows []dao.TeamInvitationRecord, page, size int, total int64, now time.Time) TeamInvitationPage {
	items := make([]TeamInvitation, 0, len(rows))
	for _, r := range rows {
		status := "pending"
		switch {
		case r.AcceptedAt != nil:
			status = "accepted"
		case r.RejectedAt != nil:
			status = "rejected"
		case r.RevokedAt != nil:
			status = "revoked"
		case !r.ExpiresAt.After(now):
			status = "expired"
		}
		items = append(items, TeamInvitation{r.ID, r.NamespaceID, r.TeamSlug, r.TeamDisplayName, r.UserID, r.Username, r.UserDisplayName, r.Role, r.InvitedByUserID, r.InviterUsername, status, r.ExpiresAt, r.AcceptedAt, r.RejectedAt, r.RevokedAt, r.CreatedAt, r.UpdatedAt})
	}
	return TeamInvitationPage{items, page, size, total}
}
func mapTeamError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, dao.ErrIdentityNotFound) {
		return ErrTeamNotFound
	}
	if errors.Is(err, dao.ErrIdentityConflict) {
		return ErrTeamConflict
	}
	return err
}
func (s *TeamService) logMutation(principal auth.Principal, action, targetID, targetType, outcome, reason string) {
	s.log.Info("identity Team mutation", zap.String("actor_user_id", principal.UserID()), zap.String("action", action), zap.String("target_type", targetType), zap.String("target_id", targetID), zap.String("outcome", outcome), zap.String("reason_code", reason))
}

func canonicalUUID(v string) bool { p, e := uuid.Parse(v); return e == nil && p.String() == v }
