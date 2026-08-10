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

// IdentityState contains the current identity and resource facts required by
// the initial policy. Resource ownership and visibility are resolved here
// because auth.ResourceRef is deliberately only a stable resource locator.
type IdentityState struct {
	Active                bool
	SystemAdmin           bool
	OwnsPersonalNamespace bool
	OwnsResource          bool
	Public                bool
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

// Authorize intersects credential scopes with role, ownership, and visibility
// rules, and denies any action absent from the initial matrix.
func (p *Policy) Authorize(ctx context.Context, principal auth.Principal, action auth.Action, resource auth.ResourceRef) error {
	if !isKnownAction(action) || !supportsResource(action, resource.Type) || p.identities == nil {
		return ErrDenied
	}

	if principal.IsAnonymous() {
		if !isPublicRead(action) {
			return ErrDenied
		}
		state, err := p.identities.ReadAuthorizationState(ctx, principal, resource)
		if err == nil && state.Public {
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

	if state.SystemAdmin || state.Public && isPublicRead(action) {
		return nil
	}

	if state.OwnsResource && isSelfAction(action) {
		return nil
	}

	if state.OwnsPersonalNamespace && isPersonalNamespaceAction(action) {
		return nil
	}

	return ErrDenied
}

func isKnownAction(action auth.Action) bool {
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

func supportsResource(action auth.Action, resourceType string) bool {
	switch action {
	case auth.ActionMarketplaceRead:
		return resourceType == "marketplace" || resourceType == "user"
	case auth.ActionPluginRead:
		return resourceType == "plugin"
	case auth.ActionRepositoryRead, auth.ActionRepositoryWrite:
		return resourceType == "repository"
	case auth.ActionTokenRead, auth.ActionTokenWrite:
		return resourceType == "token" || resourceType == "token_collection"
	default:
		return false
	}
}

func isPublicRead(action auth.Action) bool {
	return action == auth.ActionMarketplaceRead || action == auth.ActionPluginRead || action == auth.ActionRepositoryRead
}

func isSelfAction(action auth.Action) bool {
	switch action {
	case auth.ActionTokenRead, auth.ActionTokenWrite:
		return true
	default:
		return false
	}
}

func isPersonalNamespaceAction(action auth.Action) bool {
	switch action {
	case auth.ActionMarketplaceRead, auth.ActionPluginRead, auth.ActionRepositoryRead, auth.ActionRepositoryWrite:
		return true
	default:
		return false
	}
}
