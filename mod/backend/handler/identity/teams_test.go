package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/juanjiTech/jin"
)

type teamLifecycleFake struct {
	createdSlug string
	err         error
}

func (f *teamLifecycleFake) Create(_ context.Context, _ auth.Principal, slug, display string) (identityservice.Team, error) {
	f.createdSlug = slug
	return identityservice.Team{Slug: slug, DisplayName: display}, f.err
}
func (f *teamLifecycleFake) List(context.Context, auth.Principal, int, int, bool) (identityservice.TeamPage, error) {
	return identityservice.TeamPage{Items: []identityservice.Team{}}, f.err
}
func (f *teamLifecycleFake) Get(context.Context, auth.Principal, string) (identityservice.Team, error) {
	return identityservice.Team{}, f.err
}
func (f *teamLifecycleFake) Update(context.Context, auth.Principal, string, string) (identityservice.Team, error) {
	return identityservice.Team{}, f.err
}
func (f *teamLifecycleFake) ListMembers(context.Context, auth.Principal, string, int, int) (identityservice.TeamMemberPage, error) {
	return identityservice.TeamMemberPage{}, f.err
}
func (f *teamLifecycleFake) PutMember(context.Context, auth.Principal, string, string, string) (identityservice.TeamMember, error) {
	return identityservice.TeamMember{}, f.err
}
func (f *teamLifecycleFake) RemoveMember(context.Context, auth.Principal, string, string) error {
	return f.err
}
func (f *teamLifecycleFake) Invite(context.Context, auth.Principal, string, string, string) (identityservice.TeamInvitation, error) {
	return identityservice.TeamInvitation{}, f.err
}
func (f *teamLifecycleFake) ListInvitations(context.Context, auth.Principal, string, int, int) (identityservice.TeamInvitationPage, error) {
	return identityservice.TeamInvitationPage{}, f.err
}
func (f *teamLifecycleFake) Inbox(context.Context, auth.Principal, int, int) (identityservice.TeamInvitationPage, error) {
	return identityservice.TeamInvitationPage{}, f.err
}
func (f *teamLifecycleFake) Respond(context.Context, auth.Principal, string, bool) (*identityservice.Team, error) {
	return nil, f.err
}
func (f *teamLifecycleFake) Revoke(context.Context, auth.Principal, string, string) error {
	return f.err
}
func (f *teamLifecycleFake) Reissue(context.Context, auth.Principal, string, string) (identityservice.TeamInvitation, error) {
	return identityservice.TeamInvitation{}, f.err
}
func teamHandlerEngine(t *testing.T, f *teamLifecycleFake) *jin.Engine {
	e := jin.New()
	NewTeamHandler(f, &managementAuthenticatorFake{principal: handlerPrincipal(t)}).Register(e)
	return e
}
func TestTeamHandlerAuthValidationAndEnvelope(t *testing.T) {
	f := &teamLifecycleFake{}
	e := teamHandlerEngine(t, f)
	unauth := httptest.NewRecorder()
	e.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/v1/teams", nil))
	if unauth.Code != 401 {
		t.Fatalf("unauth=%d", unauth.Code)
	}
	created := httptest.NewRecorder()
	e.ServeHTTP(created, bearerRequest(http.MethodPost, "/api/v1/teams", `{"slug":"security","displayName":"Security"}`))
	if created.Code != 201 || f.createdSlug != "security" || !strings.Contains(created.Body.String(), `"data"`) {
		t.Fatalf("created=%d/%s", created.Code, created.Body.String())
	}
	bad := httptest.NewRecorder()
	e.ServeHTTP(bad, bearerRequest(http.MethodPut, "/api/v1/teams/security/members/not-uuid", `{"role":"viewer"}`))
	if bad.Code != 404 {
		t.Fatalf("bad id=%d", bad.Code)
	}
}
func TestTeamHandlerMapsStableErrors(t *testing.T) {
	f := &teamLifecycleFake{err: identityservice.ErrTeamConflict}
	r := httptest.NewRecorder()
	teamHandlerEngine(t, f).ServeHTTP(r, bearerRequest(http.MethodPost, "/api/v1/teams", `{"slug":"security","displayName":"Security"}`))
	if r.Code != 409 || !strings.Contains(r.Body.String(), `"code":"conflict"`) {
		t.Fatalf("conflict=%d/%s", r.Code, r.Body.String())
	}
}
