package authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

var pluginActions = []auth.Action{
	auth.ActionPluginCreate,
	auth.ActionPluginList,
	auth.ActionPluginRead,
	auth.ActionPluginWrite,
	auth.ActionPluginArchive,
	auth.ActionPluginPublish,
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

func TestPolicyPluginNamespaceActionsRequireResolvedNamespace(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "user-1")
	resource := auth.ResourceRef{Type: auth.ResourceNamespace, ID: "namespace-1"}
	for _, tc := range []struct {
		name  string
		state IdentityState
		want  bool
	}{
		{
			name:  "personal namespace owner",
			state: IdentityState{Active: true, OwnsPersonalNamespace: true},
			want:  true,
		},
		{
			name:  "system admin",
			state: IdentityState{Active: true, SystemAdmin: true},
			want:  true,
		},
		{
			name:  "unrelated user",
			state: IdentityState{Active: true},
			want:  false,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, action := range []auth.Action{auth.ActionPluginCreate, auth.ActionPluginList} {
				reader := &stubIdentityStateReader{states: map[string]IdentityState{
					stateKey(principal, resource): tc.state,
				}}
				err := NewPolicy(reader).Authorize(context.Background(), principal, action, resource)
				if got := err == nil; got != tc.want {
					t.Fatalf("Authorize(%q) allowed = %v, want %v (err = %v)", action, got, tc.want, err)
				}
			}
		})
	}

	for _, resource := range []auth.ResourceRef{
		{Type: auth.ResourceNamespace},
		{Type: auth.ResourceNamespace, ID: "namespace-1", NamespaceID: "forged"},
		{Type: auth.ResourcePlugin, ID: "plugin-1", NamespaceID: "namespace-1"},
	} {
		reader := &stubIdentityStateReader{}
		if err := NewPolicy(reader).Authorize(context.Background(), principal, auth.ActionPluginCreate, resource); !errors.Is(err, ErrDenied) {
			t.Fatalf("Authorize(%q, %#v) = %v, want ErrDenied", auth.ActionPluginCreate, resource, err)
		}
		if reader.calls != 0 {
			t.Fatalf("reader calls = %d for invalid namespace resource, want 0", reader.calls)
		}
	}
}

func TestPolicyPluginActionsRequireTenantQualifiedPlugin(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "user-1")
	resource := pluginResource()
	ownerState := IdentityState{
		Active:                true,
		OwnsPersonalNamespace: true,
		Plugin:                activePluginFacts(auth.RepositoryOperationalReady),
	}
	for _, action := range []auth.Action{auth.ActionPluginRead, auth.ActionPluginWrite, auth.ActionPluginArchive, auth.ActionPluginPublish} {
		reader := &stubIdentityStateReader{states: map[string]IdentityState{
			stateKey(principal, resource): ownerState,
		}}
		if err := NewPolicy(reader).Authorize(context.Background(), principal, action, resource); err != nil {
			t.Fatalf("owner Authorize(%q) = %v, want allow", action, err)
		}
	}

	for _, invalid := range []auth.ResourceRef{
		{Type: auth.ResourcePlugin, ID: "plugin-1"},
		{Type: auth.ResourcePlugin, NamespaceID: "namespace-1"},
		{Type: auth.ResourceNamespace, ID: "namespace-1"},
	} {
		reader := &stubIdentityStateReader{}
		if err := NewPolicy(reader).Authorize(context.Background(), principal, auth.ActionPluginRead, invalid); !errors.Is(err, ErrDenied) {
			t.Fatalf("Authorize(plugin.read, %#v) = %v, want ErrDenied", invalid, err)
		}
		if reader.calls != 0 {
			t.Fatalf("reader calls = %d for invalid plugin resource, want 0", reader.calls)
		}
	}
}

func TestPolicyPluginPublicAndLifecycleReadRules(t *testing.T) {
	t.Parallel()

	resource := pluginResource()
	for _, tc := range []struct {
		name  string
		facts auth.PluginAuthorizationFacts
		want  bool
	}{
		{name: "public active", facts: activePluginFacts(auth.RepositoryOperationalReady), want: true},
		{name: "public archived", facts: archivedPluginFacts(auth.RepositoryOperationalReady), want: true},
		{name: "public draft", facts: draftPluginFacts(auth.RepositoryOperationalReady)},
		{name: "private active", facts: privateActivePluginFacts(auth.RepositoryOperationalReady)},
		{name: "public active repository read only", facts: activePluginFacts(auth.RepositoryOperationalReadOnly), want: true},
		{name: "public active repository error", facts: activePluginFacts(auth.RepositoryOperationalError)},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			principal := auth.AnonymousPrincipal()
			reader := &stubIdentityStateReader{states: map[string]IdentityState{
				stateKey(principal, resource): {Plugin: tc.facts},
			}}
			for _, action := range pluginActions {
				err := NewPolicy(reader).Authorize(context.Background(), principal, action, resource)
				want := tc.want && action == auth.ActionPluginRead
				if got := err == nil; got != want {
					t.Errorf("Authorize(%q) allowed = %v, want %v (err = %v)", action, got, want, err)
				}
			}
			if reader.identityCalls != 0 {
				t.Fatalf("identity lookup attempted %d times for anonymous principal", reader.identityCalls)
			}
		})
	}

	principal := passwordPrincipal(t, "reader-1")
	reader := &stubIdentityStateReader{states: map[string]IdentityState{
		stateKey(principal, resource): {Active: true, Plugin: activePluginFacts(auth.RepositoryOperationalReady)},
	}}
	if err := NewPolicy(reader).Authorize(context.Background(), principal, auth.ActionPluginRead, resource); err != nil {
		t.Fatalf("authenticated public active plugin read = %v, want allow", err)
	}
	for _, facts := range []auth.PluginAuthorizationFacts{
		draftPluginFacts(auth.RepositoryOperationalReady),
		privateActivePluginFacts(auth.RepositoryOperationalReady),
	} {
		reader := &stubIdentityStateReader{states: map[string]IdentityState{
			stateKey(principal, resource): {Active: true, Plugin: facts},
		}}
		if err := NewPolicy(reader).Authorize(context.Background(), principal, auth.ActionPluginRead, resource); !errors.Is(err, ErrDenied) {
			t.Fatalf("unrelated authenticated read with facts %#v = %v, want ErrDenied", facts, err)
		}
	}
}

func TestPolicyPluginUnknownLifecycleFactsDefaultDeny(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "owner-1")
	resource := pluginResource()
	for _, facts := range []auth.PluginAuthorizationFacts{
		{Visibility: auth.PluginVisibility("unknown"), Status: auth.PluginStatusActive, RepositoryStatus: auth.RepositoryOperationalReady},
		{Visibility: auth.PluginVisibilityPublic, Status: auth.PluginStatus("unknown"), RepositoryStatus: auth.RepositoryOperationalReady},
		{Visibility: auth.PluginVisibilityPublic, Status: auth.PluginStatusActive, RepositoryStatus: auth.RepositoryOperationalStatus("unknown")},
	} {
		for _, action := range []auth.Action{auth.ActionPluginRead, auth.ActionPluginWrite, auth.ActionPluginArchive, auth.ActionPluginPublish} {
			reader := &stubIdentityStateReader{states: map[string]IdentityState{
				stateKey(principal, resource): {
					Active:                true,
					OwnsPersonalNamespace: true,
					Plugin:                facts,
				},
			}}
			if err := NewPolicy(reader).Authorize(context.Background(), principal, action, resource); !errors.Is(err, ErrDenied) {
				t.Fatalf("Authorize(%q, %+v) = %v, want ErrDenied", action, facts, err)
			}
		}
	}
}

func TestPolicyPluginLifecycleAndRepositoryOperationalStrictness(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "owner-1")
	resource := pluginResource()
	for _, tc := range []struct {
		name   string
		facts  auth.PluginAuthorizationFacts
		allows map[auth.Action]bool
	}{
		{
			name:  "ready active plugin",
			facts: activePluginFacts(auth.RepositoryOperationalReady),
			allows: map[auth.Action]bool{
				auth.ActionPluginRead: true, auth.ActionPluginWrite: true, auth.ActionPluginArchive: true, auth.ActionPluginPublish: true,
			},
		},
		{
			name:  "read only plugin",
			facts: activePluginFacts(auth.RepositoryOperationalReadOnly),
			allows: map[auth.Action]bool{
				auth.ActionPluginRead: true, auth.ActionPluginArchive: true,
			},
		},
		{
			name:  "repository error",
			facts: activePluginFacts(auth.RepositoryOperationalError),
			allows: map[auth.Action]bool{
				auth.ActionPluginArchive: true,
			},
		},
		{
			name:  "archived plugin",
			facts: archivedPluginFacts(auth.RepositoryOperationalReady),
			allows: map[auth.Action]bool{
				auth.ActionPluginRead: true, auth.ActionPluginArchive: true,
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, action := range []auth.Action{auth.ActionPluginRead, auth.ActionPluginWrite, auth.ActionPluginArchive, auth.ActionPluginPublish} {
				reader := &stubIdentityStateReader{states: map[string]IdentityState{
					stateKey(principal, resource): {
						Active:                true,
						OwnsPersonalNamespace: true,
						Plugin:                tc.facts,
					},
				}}
				err := NewPolicy(reader).Authorize(context.Background(), principal, action, resource)
				if got := err == nil; got != tc.allows[action] {
					t.Fatalf("Authorize(%q) allowed = %v, want %v (err = %v)", action, got, tc.allows[action], err)
				}
			}
		})
	}
}

func TestPolicyPluginAggregateDoesNotAuthorizeRepositoryResources(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "owner-1")
	resource := auth.ResourceRef{Type: "repository", ID: "plugin-1", NamespaceID: "namespace-1"}
	reader := &stubIdentityStateReader{}
	if err := NewPolicy(reader).Authorize(context.Background(), principal, auth.ActionPluginRead, resource); !errors.Is(err, ErrDenied) {
		t.Fatalf("Authorize(plugin.read, repository resource) = %v, want ErrDenied", err)
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d for repository resource, want 0", reader.calls)
	}
}

func TestPolicyPluginPATScopesIntersectFinalPluginAction(t *testing.T) {
	t.Parallel()

	resource := pluginResource()
	state := IdentityState{Active: true, OwnsPersonalNamespace: true, Plugin: activePluginFacts(auth.RepositoryOperationalReady)}
	for _, tc := range []struct {
		name   string
		scopes []auth.Action
		allows map[auth.Action]bool
	}{
		{
			name:   "read preset cannot write",
			scopes: []auth.Action{auth.ActionPluginRead},
			allows: map[auth.Action]bool{auth.ActionPluginRead: true},
		},
		{
			name:   "write preset intersects read and write",
			scopes: []auth.Action{auth.ActionPluginRead, auth.ActionPluginWrite},
			allows: map[auth.Action]bool{auth.ActionPluginRead: true, auth.ActionPluginWrite: true},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			principal := patPrincipal(t, "owner-1", tc.scopes...)
			for _, action := range []auth.Action{auth.ActionPluginRead, auth.ActionPluginWrite, auth.ActionPluginArchive, auth.ActionPluginPublish} {
				reader := &stubIdentityStateReader{states: map[string]IdentityState{
					stateKey(principal, resource): state,
				}}
				err := NewPolicy(reader).Authorize(context.Background(), principal, action, resource)
				if got := err == nil; got != tc.allows[action] {
					t.Fatalf("Authorize(%q) allowed = %v, want %v (err = %v)", action, got, tc.allows[action], err)
				}
			}
		})
	}
}

func TestPolicyPreservesMarketplaceAndTokenSelfActions(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "user-1")
	for _, tc := range []struct {
		name     string
		action   auth.Action
		resource auth.ResourceRef
		state    IdentityState
		want     bool
	}{
		{
			name:     "token owner read",
			action:   auth.ActionTokenRead,
			resource: auth.ResourceRef{Type: auth.ResourceToken, ID: "token-1"},
			state:    IdentityState{Active: true, OwnsResource: true},
			want:     true,
		},
		{
			name:     "token owner write",
			action:   auth.ActionTokenWrite,
			resource: auth.ResourceRef{Type: auth.ResourceToken, ID: "token-1"},
			state:    IdentityState{Active: true, OwnsResource: true},
			want:     true,
		},
		{
			name:     "personal namespace marketplace read",
			action:   auth.ActionMarketplaceRead,
			resource: auth.ResourceRef{Type: auth.ResourceUser, ID: principal.UserID()},
			state:    IdentityState{Active: true, OwnsPersonalNamespace: true, OwnsResource: true},
			want:     true,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reader := &stubIdentityStateReader{states: map[string]IdentityState{
				stateKey(principal, tc.resource): tc.state,
			}}
			err := NewPolicy(reader).Authorize(context.Background(), principal, tc.action, tc.resource)
			if got := err == nil; got != tc.want {
				t.Fatalf("Authorize(%q) allowed = %v, want %v (err = %v)", tc.action, got, tc.want, err)
			}
		})
	}
}

func TestPolicyDefaultDenyAndReaderFailures(t *testing.T) {
	t.Parallel()

	principal := passwordPrincipal(t, "user-1")
	resource := pluginResource()
	for _, tc := range []struct {
		name   string
		action auth.Action
		reader *stubIdentityStateReader
	}{
		{
			name:   "unknown action",
			action: auth.Action("repository.read"),
			reader: &stubIdentityStateReader{},
		},
		{
			name:   "inactive identity",
			action: auth.ActionPluginRead,
			reader: &stubIdentityStateReader{states: map[string]IdentityState{
				stateKey(principal, resource): {Plugin: activePluginFacts(auth.RepositoryOperationalReady)},
			}},
		},
		{
			name:   "reader failure",
			action: auth.ActionPluginRead,
			reader: &stubIdentityStateReader{err: errors.New("database unavailable")},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := NewPolicy(tc.reader).Authorize(context.Background(), principal, tc.action, resource); !errors.Is(err, ErrDenied) {
				t.Fatalf("Authorize() error = %v, want ErrDenied", err)
			}
		})
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

func pluginResource() auth.ResourceRef {
	return auth.ResourceRef{Type: auth.ResourcePlugin, ID: "plugin-1", NamespaceID: "namespace-1"}
}

func activePluginFacts(repositoryStatus auth.RepositoryOperationalStatus) auth.PluginAuthorizationFacts {
	return auth.PluginAuthorizationFacts{
		Visibility:       auth.PluginVisibilityPublic,
		Status:           auth.PluginStatusActive,
		RepositoryStatus: repositoryStatus,
	}
}

func privateActivePluginFacts(repositoryStatus auth.RepositoryOperationalStatus) auth.PluginAuthorizationFacts {
	return auth.PluginAuthorizationFacts{
		Visibility:       auth.PluginVisibilityPrivate,
		Status:           auth.PluginStatusActive,
		RepositoryStatus: repositoryStatus,
	}
}

func draftPluginFacts(repositoryStatus auth.RepositoryOperationalStatus) auth.PluginAuthorizationFacts {
	return auth.PluginAuthorizationFacts{
		Visibility:       auth.PluginVisibilityPublic,
		Status:           auth.PluginStatusDraft,
		RepositoryStatus: repositoryStatus,
	}
}

func archivedPluginFacts(repositoryStatus auth.RepositoryOperationalStatus) auth.PluginAuthorizationFacts {
	return auth.PluginAuthorizationFacts{
		Visibility:       auth.PluginVisibilityPublic,
		Status:           auth.PluginStatusArchived,
		RepositoryStatus: repositoryStatus,
	}
}

func stateKey(principal auth.Principal, resource auth.ResourceRef) string {
	return principal.UserID() + "/" + resource.Type + "/" + resource.ID + "/" + resource.NamespaceID
}
