package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"

	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	managementhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/management"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"github.com/juanjiTech/jin"
)

type TeamLifecycle interface {
	Create(context.Context, auth.Principal, string, string) (identityservice.Team, error)
	List(context.Context, auth.Principal, int, int, bool) (identityservice.TeamPage, error)
	Get(context.Context, auth.Principal, string) (identityservice.Team, error)
	Update(context.Context, auth.Principal, string, string) (identityservice.Team, error)
	ListMembers(context.Context, auth.Principal, string, int, int) (identityservice.TeamMemberPage, error)
	PutMember(context.Context, auth.Principal, string, string, string) (identityservice.TeamMember, error)
	RemoveMember(context.Context, auth.Principal, string, string) error
	Invite(context.Context, auth.Principal, string, string, string) (identityservice.TeamInvitation, error)
	ListInvitations(context.Context, auth.Principal, string, int, int) (identityservice.TeamInvitationPage, error)
	Inbox(context.Context, auth.Principal, int, int) (identityservice.TeamInvitationPage, error)
	Respond(context.Context, auth.Principal, string, bool) (*identityservice.Team, error)
	Revoke(context.Context, auth.Principal, string, string) error
	Reissue(context.Context, auth.Principal, string, string) (identityservice.TeamInvitation, error)
}

type TeamHandler struct {
	service       TeamLifecycle
	authenticator ManagementAuthenticator
}

func NewTeamHandler(service TeamLifecycle, authenticator ManagementAuthenticator) *TeamHandler {
	return &TeamHandler{service, authenticator}
}
func (h *TeamHandler) Register(e *jin.Engine) {
	e.GET("/api/v1/teams", h.List)
	e.POST("/api/v1/teams", h.Create)
	e.GET("/api/v1/teams/:team", h.Get)
	e.PATCH("/api/v1/teams/:team", h.Update)
	e.GET("/api/v1/teams/:team/members", h.ListMembers)
	e.PUT("/api/v1/teams/:team/members/:userId", h.PutMember)
	e.DELETE("/api/v1/teams/:team/members/:userId", h.RemoveMember)
	e.GET("/api/v1/teams/:team/invitations", h.ListInvitations)
	e.POST("/api/v1/teams/:team/invitations", h.Invite)
	e.DELETE("/api/v1/teams/:team/invitations/:invitationId", h.Revoke)
	e.POST("/api/v1/teams/:team/invitations/*invitationCommand", h.TeamInvitationCommand)
	e.GET("/api/v1/me/team-invitations", h.Inbox)
	e.POST("/api/v1/me/team-invitations/*invitationCommand", h.InboxCommand)
}

type teamRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
}
type roleRequest struct {
	Role string `json:"role"`
}
type inviteRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (h *TeamHandler) auth(c *jin.Context) (auth.Principal, bool) {
	return managementhandler.AuthenticateRequired(c, h.authenticator)
}
func (h *TeamHandler) Create(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	var r teamRequest
	if !decodeTeamBody(c, &r) {
		return
	}
	v, e := h.service.Create(c.Request.Context(), p, r.Slug, r.DisplayName)
	h.result(c, http.StatusCreated, v, e)
}
func (h *TeamHandler) List(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	scope := c.Request.URL.Query().Get("scope")
	if scope != "" && scope != "all" {
		renderAPIError(c, 422, "validation_failed", "scope is invalid")
		return
	}
	v, e := h.service.List(c.Request.Context(), p, page, size, scope == "all")
	h.result(c, 200, v, e)
}
func (h *TeamHandler) Get(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	v, e := h.service.Get(c.Request.Context(), p, c.Params.ByName("team"))
	h.result(c, 200, v, e)
}
func (h *TeamHandler) Update(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	var r teamRequest
	if !decodeTeamBody(c, &r) {
		return
	}
	v, e := h.service.Update(c.Request.Context(), p, c.Params.ByName("team"), r.DisplayName)
	h.result(c, 200, v, e)
}
func (h *TeamHandler) ListMembers(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	v, e := h.service.ListMembers(c.Request.Context(), p, c.Params.ByName("team"), page, size)
	h.result(c, 200, v, e)
}
func (h *TeamHandler) PutMember(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	id := c.Params.ByName("userId")
	if !canonicalID(id) {
		h.fail(c, identityservice.ErrTeamNotFound)
		return
	}
	var r roleRequest
	if !decodeTeamBody(c, &r) {
		return
	}
	v, e := h.service.PutMember(c.Request.Context(), p, c.Params.ByName("team"), id, r.Role)
	h.result(c, 200, v, e)
}
func (h *TeamHandler) RemoveMember(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	id := c.Params.ByName("userId")
	if !canonicalID(id) {
		h.fail(c, identityservice.ErrTeamNotFound)
		return
	}
	if e := h.service.RemoveMember(c.Request.Context(), p, c.Params.ByName("team"), id); e != nil {
		h.fail(c, e)
		return
	}
	c.Writer.WriteHeader(204)
}
func (h *TeamHandler) Invite(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	var r inviteRequest
	if !decodeTeamBody(c, &r) {
		return
	}
	v, e := h.service.Invite(c.Request.Context(), p, c.Params.ByName("team"), r.Username, r.Role)
	h.result(c, 201, v, e)
}
func (h *TeamHandler) ListInvitations(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	v, e := h.service.ListInvitations(c.Request.Context(), p, c.Params.ByName("team"), page, size)
	h.result(c, 200, v, e)
}
func (h *TeamHandler) Revoke(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	id := c.Params.ByName("invitationId")
	if !canonicalID(id) {
		h.fail(c, identityservice.ErrTeamNotFound)
		return
	}
	if e := h.service.Revoke(c.Request.Context(), p, c.Params.ByName("team"), id); e != nil {
		h.fail(c, e)
		return
	}
	c.Writer.WriteHeader(204)
}
func (h *TeamHandler) TeamInvitationCommand(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	command := strings.TrimPrefix(c.Params.ByName("invitationCommand"), "/")
	if !strings.HasSuffix(command, ":reissue") {
		h.fail(c, identityservice.ErrTeamNotFound)
		return
	}
	id := strings.TrimSuffix(command, ":reissue")
	if !canonicalID(id) {
		h.fail(c, identityservice.ErrTeamNotFound)
		return
	}
	v, e := h.service.Reissue(c.Request.Context(), p, c.Params.ByName("team"), id)
	h.result(c, 201, v, e)
}
func (h *TeamHandler) Inbox(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	v, e := h.service.Inbox(c.Request.Context(), p, page, size)
	h.result(c, 200, v, e)
}
func (h *TeamHandler) InboxCommand(c *jin.Context) {
	p, ok := h.auth(c)
	if !ok {
		return
	}
	command := strings.TrimPrefix(c.Params.ByName("invitationCommand"), "/")
	accept := strings.HasSuffix(command, ":accept")
	reject := strings.HasSuffix(command, ":reject")
	if !accept && !reject {
		h.fail(c, identityservice.ErrTeamNotFound)
		return
	}
	id := strings.TrimSuffix(strings.TrimSuffix(command, ":accept"), ":reject")
	if !canonicalID(id) {
		h.fail(c, identityservice.ErrTeamNotFound)
		return
	}
	v, e := h.service.Respond(c.Request.Context(), p, id, accept)
	if e != nil {
		h.fail(c, e)
		return
	}
	if accept {
		renderSuccess(c, 200, v)
		return
	}
	c.Writer.WriteHeader(204)
}
func (h *TeamHandler) result(c *jin.Context, status int, v any, e error) {
	if e != nil {
		h.fail(c, e)
		return
	}
	renderSuccess(c, status, v)
}
func (h *TeamHandler) fail(c *jin.Context, e error) {
	switch {
	case errors.Is(e, identityservice.ErrInvalidTeamInput):
		renderAPIError(c, 422, "validation_failed", "request is invalid")
	case errors.Is(e, identityservice.ErrTeamNotFound):
		renderAPIError(c, 404, "not_found", "resource not found")
	case errors.Is(e, identityservice.ErrTeamForbidden):
		renderAPIError(c, 403, "forbidden", "permission denied")
	case errors.Is(e, identityservice.ErrTeamConflict):
		renderAPIError(c, 409, "conflict", "operation conflicts with current state")
	default:
		renderAPIError(c, 500, "internal_error", "internal server error")
	}
}
func decodeTeamBody(c *jin.Context, v any) bool {
	if decodeJSONBody(c, v) != nil {
		renderAPIError(c, 400, "bad_request", "request body is invalid")
		return false
	}
	return true
}
func canonicalID(v string) bool { p, e := uuid.Parse(v); return e == nil && p.String() == v }
