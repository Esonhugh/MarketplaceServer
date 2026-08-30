package auth

import (
	"context"
	"errors"
	"sort"
)

// CredentialKind identifies how a principal authenticated.
type CredentialKind string

const (
	CredentialNone            CredentialKind = "none"
	CredentialAccountPassword CredentialKind = "account_password"
	CredentialJWT             CredentialKind = "jwt"
	CredentialPAT             CredentialKind = "pat"
)

// Action identifies an operation that can be authorized or placed in a scope.
type Action string

const (
	ActionMarketplaceRead Action = "marketplace.read"
	ActionPluginCreate    Action = "plugin.create"
	ActionPluginList      Action = "plugin.list"
	ActionPluginRead      Action = "plugin.read"
	ActionPluginWrite     Action = "plugin.write"
	ActionPluginArchive   Action = "plugin.archive"
	ActionPluginPublish   Action = "plugin.publish"
	ActionTokenRead       Action = "token.read"
	ActionTokenWrite      Action = "token.write"
)

const (
	ResourceNamespace       = "namespace"
	ResourcePlugin          = "plugin"
	ResourceMarketplace     = "marketplace"
	ResourceToken           = "token"
	ResourceTokenCollection = "token_collection"
	ResourceUser            = "user"
)

// PluginVisibility is the current authorization-relevant Plugin visibility.
type PluginVisibility string

const (
	PluginVisibilityPublic  PluginVisibility = "public"
	PluginVisibilityPrivate PluginVisibility = "private"
)

// PluginStatus is the current authorization-relevant Plugin lifecycle status.
type PluginStatus string

const (
	PluginStatusDraft    PluginStatus = "draft"
	PluginStatusActive   PluginStatus = "active"
	PluginStatusArchived PluginStatus = "archived"
)

// RepositoryOperationalStatus is the operational status of a Plugin's hidden
// repository. It is a fact about the Plugin aggregate, not an independent
// authorization resource.
type RepositoryOperationalStatus string

const (
	RepositoryOperationalReady    RepositoryOperationalStatus = "ready"
	RepositoryOperationalReadOnly RepositoryOperationalStatus = "readOnly"
	RepositoryOperationalError    RepositoryOperationalStatus = "error"
)

// PluginAuthorizationFacts is the value-only lifecycle and operational state
// required to authorize a tenant-qualified Plugin. It deliberately contains no
// persistence model, storage path, or repository identity.
type PluginAuthorizationFacts struct {
	Visibility       PluginVisibility
	Status           PluginStatus
	RepositoryStatus RepositoryOperationalStatus
}

// ResourceRef identifies a resource without exposing persistence models. A
// namespace action targets ResourceNamespace with ID only. A Plugin action
// other than create/list targets ResourcePlugin with both ID and NamespaceID.
type ResourceRef struct {
	Type        string
	ID          string
	NamespaceID string
}

// ScopeSet is either unrestricted or a restricted set of actions.
type ScopeSet struct {
	unrestricted bool
	actions      map[Action]struct{}
}

func UnrestrictedScopes() ScopeSet {
	return ScopeSet{unrestricted: true}
}

func RestrictedScopes(actions ...Action) ScopeSet {
	if len(actions) == 0 {
		return ScopeSet{}
	}
	s := ScopeSet{actions: make(map[Action]struct{}, len(actions))}
	for _, action := range actions {
		s.actions[action] = struct{}{}
	}
	return s
}

func (s ScopeSet) Restricted() bool {
	return !s.unrestricted
}

func (s ScopeSet) Allows(action Action) bool {
	if s.unrestricted {
		return true
	}
	_, ok := s.actions[action]
	return ok
}

func (s ScopeSet) Actions() []Action {
	if s.unrestricted {
		return nil
	}
	actions := make([]Action, 0, len(s.actions))
	for action := range s.actions {
		actions = append(actions, action)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i] < actions[j] })
	return actions
}

type principalKind uint8

const (
	principalAnonymous principalKind = iota
	principalUser
)

// Principal is immutable outside this package and represents an anonymous or user identity.
type Principal struct {
	kind       principalKind
	userID     string
	username   string
	credential CredentialKind
	scopes     ScopeSet
}

func AnonymousPrincipal() Principal {
	return Principal{kind: principalAnonymous, credential: CredentialNone, scopes: RestrictedScopes()}
}

func NewUserPrincipal(userID, username string, credential CredentialKind, scopes ScopeSet) (Principal, error) {
	if userID == "" {
		return Principal{}, errors.New("auth: user ID is required")
	}
	if username == "" {
		return Principal{}, errors.New("auth: username is required")
	}
	switch credential {
	case CredentialAccountPassword, CredentialJWT, CredentialPAT:
	default:
		return Principal{}, errors.New("auth: invalid user credential kind")
	}
	return Principal{
		kind:       principalUser,
		userID:     userID,
		username:   username,
		credential: credential,
		scopes:     cloneScopes(scopes),
	}, nil
}

func (p Principal) IsAnonymous() bool              { return p.kind == principalAnonymous }
func (p Principal) IsUser() bool                   { return p.kind == principalUser }
func (p Principal) UserID() string                 { return p.userID }
func (p Principal) Username() string               { return p.username }
func (p Principal) CredentialKind() CredentialKind { return p.credential }
func (p Principal) Allows(action Action) bool      { return p.IsUser() && p.scopes.Allows(action) }
func (p Principal) Scopes() ScopeSet               { return cloneScopes(p.scopes) }

func cloneScopes(scopes ScopeSet) ScopeSet {
	if scopes.unrestricted {
		return UnrestrictedScopes()
	}
	return RestrictedScopes(scopes.Actions()...)
}

// GitOperation identifies the capability required by a Git protocol request.
type GitOperation string

const (
	GitOperationRead  GitOperation = "read"
	GitOperationWrite GitOperation = "write"
)

// GitPATAuthenticator accepts only a PAT appropriate for the requested Git operation.
type GitPATAuthenticator interface {
	AuthenticateGitPAT(ctx context.Context, username, plaintext string, operation GitOperation) (Principal, error)
}

// SubscriptionPATAuthenticator accepts only a PAT with subscription read capability.
type SubscriptionPATAuthenticator interface {
	AuthenticateSubscriptionPAT(ctx context.Context, username, plaintext string) (Principal, error)
}

// Authorizer decides whether a principal can perform an action on a resource.
type Authorizer interface {
	Authorize(ctx context.Context, principal Principal, action Action, resource ResourceRef) error
}

type principalContextKey struct{}

func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}
