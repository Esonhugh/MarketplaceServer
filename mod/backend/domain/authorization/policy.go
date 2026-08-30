package authorization

import (
	"context"
	"errors"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

var (
	// ErrDenied is returned for every policy rejection. The policy intentionally
	// does not expose whether an identity was unknown, inactive, or unauthorized.
	ErrDenied = errors.New("authorization denied")

	// ErrIdentityUnknown lets an IdentityStateReader report a missing identity.
	// Policy converts it, and all other reader failures, to ErrDenied.
	ErrIdentityUnknown = errors.New("identity unknown")
)

// IdentityState contains the current identity and value-only resource facts
// required by policy. Resource ownership and Plugin lifecycle facts are
// resolved here because auth.ResourceRef is deliberately only a stable locator.
type IdentityState struct {
	Active                bool
	SystemAdmin           bool
	OwnsPersonalNamespace bool
	OwnsResource          bool
	Plugin                auth.PluginAuthorizationFacts
}

// IdentityStateReader resolves authorization facts for a principal and
// resource. For anonymous principals, implementations only need to resolve
// resource visibility and must not perform an identity lookup.
type IdentityStateReader interface {
	ReadAuthorizationState(ctx context.Context, principal auth.Principal, resource auth.ResourceRef) (IdentityState, error)
}

// Policy implements the initial, deliberately small authorization matrix.
type Policy struct {
	identities IdentityStateReader
}

func NewPolicy(identities IdentityStateReader) *Policy {
	return &Policy{identities: identities}
}

// Authorize intersects credential scopes with role, ownership, lifecycle, and
// hidden-repository operational facts. It denies any action absent from the
// approved action matrix.
func (p *Policy) Authorize(ctx context.Context, principal auth.Principal, action auth.Action, resource auth.ResourceRef) error {
	if !isKnownAction(action) || !supportsResource(action, resource) || p.identities == nil {
		return ErrDenied
	}

	if principal.IsAnonymous() {
		if action != auth.ActionPluginRead {
			return ErrDenied
		}
		state, err := p.identities.ReadAuthorizationState(ctx, principal, resource)
		if err == nil && allowsAnonymousPluginRead(state.Plugin) {
			return nil
		}
		return ErrDenied
	}

	if !principal.IsUser() || principal.UserID() == "" || !principal.Allows(action) {
		return ErrDenied
	}

	state, err := p.identities.ReadAuthorizationState(ctx, principal, resource)
	if err != nil || !state.Active {
		return ErrDenied
	}

	if isPluginNamespaceAction(action) {
		if state.SystemAdmin || state.OwnsPersonalNamespace {
			return nil
		}
		return ErrDenied
	}
	if isPluginAction(action) {
		if !allowsPluginAction(action, state.Plugin) {
			return ErrDenied
		}
		if action == auth.ActionPluginRead && allowsPublicPluginRead(state.Plugin) {
			return nil
		}
		if state.SystemAdmin || state.OwnsPersonalNamespace {
			return nil
		}
		return ErrDenied
	}

	if state.SystemAdmin {
		return nil
	}
	if state.OwnsResource && isSelfAction(action) {
		return nil
	}
	if state.OwnsPersonalNamespace && action == auth.ActionMarketplaceRead {
		return nil
	}
	return ErrDenied
}

func isKnownAction(action auth.Action) bool {
	switch action {
	case auth.ActionMarketplaceRead,
		auth.ActionPluginCreate,
		auth.ActionPluginList,
		auth.ActionPluginRead,
		auth.ActionPluginWrite,
		auth.ActionPluginArchive,
		auth.ActionPluginPublish,
		auth.ActionTokenRead,
		auth.ActionTokenWrite:
		return true
	default:
		return false
	}
}

func supportsResource(action auth.Action, resource auth.ResourceRef) bool {
	switch action {
	case auth.ActionPluginCreate, auth.ActionPluginList:
		return resource.Type == auth.ResourceNamespace && resource.ID != "" && resource.NamespaceID == ""
	case auth.ActionPluginRead, auth.ActionPluginWrite, auth.ActionPluginArchive, auth.ActionPluginPublish:
		return resource.Type == auth.ResourcePlugin && resource.ID != "" && resource.NamespaceID != ""
	case auth.ActionMarketplaceRead:
		return resource.Type == auth.ResourceMarketplace || resource.Type == auth.ResourceUser
	case auth.ActionTokenRead, auth.ActionTokenWrite:
		return resource.Type == auth.ResourceToken || resource.Type == auth.ResourceTokenCollection
	default:
		return false
	}
}

func isPluginNamespaceAction(action auth.Action) bool {
	return action == auth.ActionPluginCreate || action == auth.ActionPluginList
}

func isPluginAction(action auth.Action) bool {
	switch action {
	case auth.ActionPluginRead, auth.ActionPluginWrite, auth.ActionPluginArchive, auth.ActionPluginPublish:
		return true
	default:
		return false
	}
}

func allowsAnonymousPluginRead(facts auth.PluginAuthorizationFacts) bool {
	return allowsPublicPluginRead(facts)
}

func allowsPublicPluginRead(facts auth.PluginAuthorizationFacts) bool {
	return validPluginFacts(facts) && facts.Visibility == auth.PluginVisibilityPublic &&
		(facts.Status == auth.PluginStatusActive || facts.Status == auth.PluginStatusArchived) &&
		facts.RepositoryStatus != auth.RepositoryOperationalError
}

func allowsPluginAction(action auth.Action, facts auth.PluginAuthorizationFacts) bool {
	if !validPluginFacts(facts) {
		return false
	}
	if action == auth.ActionPluginRead && facts.RepositoryStatus == auth.RepositoryOperationalError {
		return false
	}
	if (action == auth.ActionPluginWrite || action == auth.ActionPluginPublish) && facts.RepositoryStatus != auth.RepositoryOperationalReady {
		return false
	}
	if facts.Status == auth.PluginStatusArchived && (action == auth.ActionPluginWrite || action == auth.ActionPluginPublish) {
		return false
	}
	return true
}

func validPluginFacts(facts auth.PluginAuthorizationFacts) bool {
	if facts.Visibility != auth.PluginVisibilityPublic && facts.Visibility != auth.PluginVisibilityPrivate {
		return false
	}
	if facts.Status != auth.PluginStatusDraft && facts.Status != auth.PluginStatusActive && facts.Status != auth.PluginStatusArchived {
		return false
	}
	return facts.RepositoryStatus == auth.RepositoryOperationalReady ||
		facts.RepositoryStatus == auth.RepositoryOperationalReadOnly ||
		facts.RepositoryStatus == auth.RepositoryOperationalError
}

func isSelfAction(action auth.Action) bool {
	return action == auth.ActionTokenRead || action == auth.ActionTokenWrite
}
