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

type teamStoreFake struct {
	team       dao.TeamRecord
	admin      bool
	created    model.Namespace
	owner      model.TeamMembership
	role       string
	putRole    string
	invitation dao.TeamInvitationRecord
	revoked    bool
	reissued   bool
}

func (f *teamStoreFake) CreateTeam(_ context.Context, n model.Namespace, m model.TeamMembership) error {
	f.created = n
	f.owner = m
	return nil
}
func (f *teamStoreFake) FindTeamBySlug(context.Context, string, string) (dao.TeamRecord, error) {
	return f.team, nil
}
func (f *teamStoreFake) ListTeams(context.Context, string, bool, int, int) ([]dao.TeamRecord, int64, error) {
	return []dao.TeamRecord{f.team}, 1, nil
}
func (f *teamStoreFake) UpdateTeamDisplayName(context.Context, string, string, time.Time) error {
	return nil
}
func (f *teamStoreFake) TeamRole(context.Context, string, string) (string, error) {
	if f.role == "" {
		return "", dao.ErrIdentityNotFound
	}
	return f.role, nil
}
func (f *teamStoreFake) ListTeamMembers(context.Context, string, int, int) ([]dao.TeamMemberRecord, int64, error) {
	return nil, 0, nil
}
func (f *teamStoreFake) PutTeamMember(_ context.Context, _, _, role string, _ time.Time) error {
	f.putRole = role
	return nil
}
func (f *teamStoreFake) RemoveTeamMember(context.Context, string, string) error { return nil }
func (f *teamStoreFake) FindUserByUsername(context.Context, string) (model.User, error) {
	return model.User{}, dao.ErrIdentityNotFound
}
func (f *teamStoreFake) FindUserByID(context.Context, string) (model.User, error) {
	return model.User{ID: "22222222-2222-4222-8222-222222222222", Username: "member", DisplayName: "Member", Status: model.UserStatusActive}, nil
}
func (f *teamStoreFake) IsSystemAdmin(context.Context, string) (bool, error)              { return f.admin, nil }
func (f *teamStoreFake) CreateTeamInvitation(context.Context, model.TeamInvitation) error { return nil }
func (f *teamStoreFake) ListTeamInvitations(context.Context, string, int, int) ([]dao.TeamInvitationRecord, int64, error) {
	return nil, 0, nil
}
func (f *teamStoreFake) ListUserPendingInvitations(context.Context, string, time.Time, int, int) ([]dao.TeamInvitationRecord, int64, error) {
	return nil, 0, nil
}
func (f *teamStoreFake) FindInvitation(context.Context, string, string) (dao.TeamInvitationRecord, error) {
	return f.invitation, nil
}
func (f *teamStoreFake) RespondInvitation(context.Context, string, string, bool, time.Time) (string, error) {
	return "", nil
}
func (f *teamStoreFake) RevokeInvitation(context.Context, string, string, time.Time) error {
	f.revoked = true
	return nil
}
func (f *teamStoreFake) ReissueInvitation(context.Context, string, string, string, time.Time, time.Time) (model.TeamInvitation, error) {
	f.reissued = true
	return model.TeamInvitation{}, nil
}
func teamPrincipal(t *testing.T) auth.Principal {
	p, e := auth.NewUserPrincipal("11111111-1111-4111-8111-111111111111", "owner", auth.CredentialJWT, auth.UnrestrictedScopes())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestTeamServiceCreateAndIsolation(t *testing.T) {
	f := &teamStoreFake{}
	s, _ := NewTeamService(f)
	s.newID = func() string { return "33333333-3333-4333-8333-333333333333" }
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	team, e := s.Create(context.Background(), teamPrincipal(t), " Security ", " Security Team ")
	if e != nil || team.Slug != "security" || f.owner.Role != model.TeamRoleOwner || f.owner.NamespaceID != f.created.ID {
		t.Fatalf("create=%#v,%#v,%v", team, f.owner, e)
	}
	f.team = dao.TeamRecord{ID: f.created.ID, Slug: "hidden", DisplayName: "Hidden"}
	if _, e = s.Get(context.Background(), teamPrincipal(t), "hidden"); !errors.Is(e, ErrTeamNotFound) {
		t.Fatalf("nonmember get=%v", e)
	}
}
func TestTeamServiceAdminCannotManageOwner(t *testing.T) {
	f := &teamStoreFake{team: dao.TeamRecord{ID: "33333333-3333-4333-8333-333333333333", Slug: "security", CurrentUserRole: model.TeamRoleAdmin}, role: model.TeamRoleOwner}
	s, _ := NewTeamService(f)
	_, e := s.PutMember(context.Background(), teamPrincipal(t), "security", "22222222-2222-4222-8222-222222222222", model.TeamRoleViewer)
	if !errors.Is(e, ErrTeamForbidden) || f.putRole != "" {
		t.Fatalf("admin owner mutation=%v/%q", e, f.putRole)
	}
}

func TestTeamServiceAdminCannotRevokeOrReissueOwnerInvitation(t *testing.T) {
	f := &teamStoreFake{
		team:       dao.TeamRecord{ID: "33333333-3333-4333-8333-333333333333", Slug: "security", CurrentUserRole: model.TeamRoleAdmin},
		invitation: dao.TeamInvitationRecord{ID: "44444444-4444-4444-8444-444444444444", Role: model.TeamRoleOwner},
	}
	s, _ := NewTeamService(f)
	if err := s.Revoke(context.Background(), teamPrincipal(t), "security", f.invitation.ID); !errors.Is(err, ErrTeamForbidden) || f.revoked {
		t.Fatalf("admin owner revoke=%v/revoked=%v", err, f.revoked)
	}
	if _, err := s.Reissue(context.Background(), teamPrincipal(t), "security", f.invitation.ID); !errors.Is(err, ErrTeamForbidden) || f.reissued {
		t.Fatalf("admin owner reissue=%v/reissued=%v", err, f.reissued)
	}
}
