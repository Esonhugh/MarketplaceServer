package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

var currentActions = []auth.Action{
	auth.ActionMarketplaceRead,
	auth.ActionPluginRead,
	auth.ActionRepositoryRead,
	auth.ActionRepositoryWrite,
	auth.ActionTokenRead,
	auth.ActionTokenWrite,
}

type stubIdentityStateReader struct {
	states        map[string]IdentityState
	err           error
	calls         int
	identityCalls int
}

func (s *stubIdentityStateReader) ReadAuthorizationState(_ context.Context, principal auth.Principal, resource auth.ResourceRef) (IdentityState, error) {
	s.calls++
	if principal.IsUser() {
		s.identityCalls++
	}
	if s.err != nil {
		return IdentityState{}, s.err
	}
	state, ok := s.states[stateKey(principal, resource)]
	if !ok {
		return IdentityState{}, ErrIdentityUnknown
	}
	return state, nil
}

func TestPolicyAuthorizeRoleMatrix(t *testing.T) {
	t.Parallel()

	const (
		userID            = "user-1"
		personalNamespace = "ns-user-1"
		otherNamespace    = "ns-other"
	)

	tests := []struct {
		name     string
		resource auth.ResourceRef
		state    IdentityState
		want     map[auth.Action]bool
	}{
		{
			name:     "active token owner grants only token actions",
			resource: auth.ResourceRef{Type: "token", ID: "token-1"},
			state:    IdentityState{Active: true, OwnsResource: true},
			want: map[auth.Action]bool{
				auth.ActionTokenRead:  true,
				auth.ActionTokenWrite: true,
			},
		},
		{
			name:     "personal namespace owner combines user marketplace and self actions",
			resource: auth.ResourceRef{Type: "user", ID: userID, NamespaceID: personalNamespace},
			state: IdentityState{
				Active:                true,
				OwnsPersonalNamespace: true,
				OwnsResource:          true,
			},
			want: map[auth.Action]bool{
				auth.ActionMarketplaceRead: true,
			},
		},
		{
			name:     "active user has no implicit rights on another namespace",
			resource: auth.ResourceRef{Type: "repository", ID: "repo-2", NamespaceID: otherNamespace},
			state:    IdentityState{Active: true},
			want:     map[auth.Action]bool{},
		},
		{
			name:     "explicit system admin is independent of ownership",
			resource: auth.ResourceRef{Type: "repository", ID: "repo-2", NamespaceID: otherNamespace},
			state:    IdentityState{Active: true, SystemAdmin: true},
			want: map[auth.Action]bool{
				auth.ActionRepositoryRead:  true,
				auth.ActionRepositoryWrite: true,
			},
		},
		{
			name:     "personal namespace owner is not system admin or resource owner",
			resource: auth.ResourceRef{Type: "repository", ID: "repo-1", NamespaceID: personalNamespace},
			state:    IdentityState{Active: true, OwnsPersonalNamespace: true},
			want: map[auth.Action]bool{
				auth.ActionRepositoryRead:  true,
				auth.ActionRepositoryWrite: true,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			principal := passwordPrincipal(t, userID)
			for _, action := range currentActions {
				action := action
				t.Run(string(action), func(t *testing.T) {
					reader := &stubIdentityStateReader{states: map[string]IdentityState{
						stateKey(principal, tt.resource): tt.state,
					}}
					err := NewPolicy(reader).Authorize(context.Background(), principal, action, tt.resource)
					if got := err == nil; got != tt.want[action] {
						t.Fatalf("Authorize() allowed = %v, want %v (err = %v)", got, tt.want[action], err)
					}
					if err != nil && !errors.Is(err, ErrDenied) {
						t.Fatalf("Authorize() error = %v, want ErrDenied", err)
					}
				})
			}
		})
	}
}

func TestPolicyAuthorizeAnonymousPublicRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		public bool
		want   map[auth.Action]bool
	}{
		{
			name:   "public",
			public: true,
			want: map[auth.Action]bool{
				auth.ActionRepositoryRead: true,
			},
		},
		{name: "private", want: map[auth.Action]bool{}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			principal := auth.AnonymousPrincipal()
			resource := auth.ResourceRef{Type: "repository", ID: "repo-1", NamespaceID: "ns-1"}
			for _, action := range currentActions {
				reader := &stubIdentityStateReader{states: map[string]IdentityState{
					stateKey(principal, resource): {Public: tt.public},
				}}
				err := NewPolicy(reader).Authorize(context.Background(), principal, action, resource)
				want := tt.want[action]
				if got := err == nil; got != want {
					t.Errorf("Authorize(%q) allowed = %v, want %v", action, got, want)
				} else if !want && !errors.Is(err, ErrDenied) {
					t.Errorf("Authorize(%q) error = %v, want ErrDenied", action, err)
				}
				wantCalls := 0
				if supportsResource(action, resource.Type) && isPublicRead(action) {
					wantCalls = 1
				}
				if reader.calls != wantCalls {
					t.Fatalf("authorization state reader called %d times, want %d", reader.calls, wantCalls)
				}
				if reader.identityCalls != 0 {
					t.Fatalf("identity lookup attempted %d times for anonymous principal", reader.identityCalls)
				}
			}
		})
	}
}

func TestPolicyAuthorizeRequiresActiveKnownIdentity(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "user-1")
	resource := auth.ResourceRef{Type: "repository", ID: "repo-1", NamespaceID: "ns-user-1"}
	tests := []struct {
		name   string
		reader *stubIdentityStateReader
	}{
		{
			name: "inactive user",
			reader: &stubIdentityStateReader{states: map[string]IdentityState{
				stateKey(principal, resource): {
					SystemAdmin:           true,
					OwnsPersonalNamespace: true,
					OwnsResource:          true,
					Public:                true,
				},
			}},
		},
		{name: "unknown user", reader: &stubIdentityStateReader{states: map[string]IdentityState{}}},
		{name: "authorization state reader failure", reader: &stubIdentityStateReader{err: errors.New("database unavailable")}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := NewPolicy(tt.reader).Authorize(context.Background(), principal, auth.ActionRepositoryRead, resource)
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("Authorize() error = %v, want ErrDenied", err)
			}
		})
	}
}

func TestPolicyAuthorizeScopeIntersection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		principal  auth.Principal
		state      IdentityState
		resource   auth.ResourceRef
		wantAction auth.Action
	}{
		{
			name:       "API key intersects personal owner permissions",
			principal:  patPrincipal(t, "user-1", auth.ActionRepositoryRead),
			state:      IdentityState{Active: true, OwnsPersonalNamespace: true},
			resource:   auth.ResourceRef{Type: "repository", ID: "repo-1", NamespaceID: "ns-user-1"},
			wantAction: auth.ActionRepositoryRead,
		},
		{
			name:       "API key intersects system admin permissions",
			principal:  patPrincipal(t, "admin-1", auth.ActionPluginRead),
			state:      IdentityState{Active: true, SystemAdmin: true},
			resource:   auth.ResourceRef{Type: "plugin", ID: "plugin-2", NamespaceID: "ns-other"},
			wantAction: auth.ActionPluginRead,
		},
		{
			name:      "API key with empty scopes denies system admin",
			principal: patPrincipal(t, "admin-1"),
			state:     IdentityState{Active: true, SystemAdmin: true},
			resource:  auth.ResourceRef{Type: "repository", ID: "repo-2", NamespaceID: "ns-other"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, action := range currentActions {
				reader := &stubIdentityStateReader{states: map[string]IdentityState{
					stateKey(tt.principal, tt.resource): tt.state,
				}}
				err := NewPolicy(reader).Authorize(context.Background(), tt.principal, action, tt.resource)
				want := tt.wantAction != "" && action == tt.wantAction
				if got := err == nil; got != want {
					t.Errorf("Authorize(%q) allowed = %v, want %v", action, got, want)
				} else if !want && !errors.Is(err, ErrDenied) {
					t.Errorf("Authorize(%q) error = %v, want ErrDenied", action, err)
				}
			}
		})
	}
}

func TestPolicyAuthorizeRejectsIncompatibleResourceWithoutLookup(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "user-1")
	reader := &stubIdentityStateReader{states: map[string]IdentityState{}}
	if err := NewPolicy(reader).Authorize(context.Background(), principal, auth.ActionTokenWrite, auth.ResourceRef{Type: "repository", ID: "repo-1"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want ErrDenied", err)
	}
	if reader.calls != 0 {
		t.Fatalf("authorization state reader called %d times for incompatible resource", reader.calls)
	}
}

func TestPolicyAuthorizeDefaultDeny(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "user-1")
	resource := auth.ResourceRef{Type: "repository", ID: "repo-1", NamespaceID: "ns-user-1"}
	reader := &stubIdentityStateReader{states: map[string]IdentityState{
		stateKey(principal, resource): {
			Active:                true,
			SystemAdmin:           true,
			OwnsPersonalNamespace: true,
			OwnsResource:          true,
			Public:                true,
		},
	}}

	if err := NewPolicy(reader).Authorize(context.Background(), principal, auth.Action("unknown.action"), resource); !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize() error = %v, want ErrDenied", err)
	}
	if reader.calls != 0 {
		t.Fatalf("authorization state reader called %d times for unknown action", reader.calls)
	}
}

func TestPolicyImplementsAuthorizer(t *testing.T) {
	t.Parallel()

	var _ auth.Authorizer = NewPolicy(&stubIdentityStateReader{})
}

func passwordPrincipal(t *testing.T, userID string) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal(userID, userID, auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatalf("NewUserPrincipal(): %v", err)
	}
	return principal
}

func patPrincipal(t *testing.T, userID string, scopes ...auth.Action) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal(userID, userID, auth.CredentialPAT, auth.RestrictedScopes(scopes...))
	if err != nil {
		t.Fatalf("NewUserPrincipal(): %v", err)
	}
	return principal
}

func stateKey(principal auth.Principal, resource auth.ResourceRef) string {
	return principal.UserID() + "/" + resource.Type + "/" + resource.ID + "/" + resource.NamespaceID
}
