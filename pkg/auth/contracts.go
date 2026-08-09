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
	CredentialAPIKey          CredentialKind = "api_key"
)

// Action identifies an operation that can be authorized or placed in a scope.
type Action string

const (
	ActionMarketplaceRead Action = "marketplace.read"
	ActionPluginRead      Action = "plugin.read"
	ActionRepositoryRead  Action = "repository.read"
	ActionRepositoryWrite Action = "repository.write"
	ActionTokenRead       Action = "token.read"
	ActionTokenWrite      Action = "token.write"
)

// ResourceRef identifies a resource without exposing persistence models.
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
	if credential != CredentialAccountPassword && credential != CredentialAPIKey {
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

// BasicAuthenticator validates an HTTP Basic username/password pair.
type BasicAuthenticator interface {
	AuthenticateBasic(ctx context.Context, username, password string) (Principal, error)
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
