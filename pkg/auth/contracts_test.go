package auth_test

import (
	"context"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

func TestPrincipalAndScopeBehavior(t *testing.T) {
	t.Parallel()

	anonymous := auth.AnonymousPrincipal()
	if !anonymous.IsAnonymous() || anonymous.IsUser() {
		t.Fatal("anonymous principal has the wrong kind")
	}
	if anonymous.UserID() != "" || anonymous.Username() != "" {
		t.Fatal("anonymous principal contains user identity")
	}
	if anonymous.CredentialKind() != auth.CredentialNone {
		t.Fatalf("anonymous credential kind = %q", anonymous.CredentialKind())
	}

	zeroScopesUser, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialAccountPassword, auth.ScopeSet{})
	if err != nil {
		t.Fatalf("NewUserPrincipal with zero scopes: %v", err)
	}
	if zeroScopesUser.Allows(auth.ActionRepositoryRead) {
		t.Fatal("zero-value scope set must fail closed")
	}

	passwordUser, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialAccountPassword, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatalf("NewUserPrincipal: %v", err)
	}
	if !passwordUser.IsUser() || passwordUser.UserID() != "user-1" || passwordUser.Username() != "alice" {
		t.Fatal("user principal lost immutable identity")
	}
	if !passwordUser.Allows(auth.ActionRepositoryWrite) {
		t.Fatal("unrestricted principal should allow repository.write")
	}

	patUser, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialPAT,
		auth.RestrictedScopes(auth.ActionRepositoryRead, auth.ActionPluginRead, auth.ActionRepositoryRead))
	if err != nil {
		t.Fatalf("NewUserPrincipal: %v", err)
	}
	if !patUser.Allows(auth.ActionRepositoryRead) || patUser.Allows(auth.ActionRepositoryWrite) {
		t.Fatal("restricted scopes were not enforced")
	}
	got := patUser.Scopes().Actions()
	if len(got) != 2 || got[0] != auth.ActionPluginRead || got[1] != auth.ActionRepositoryRead {
		t.Fatalf("normalized scopes = %v", got)
	}
}

func TestPrincipalValidationAndContext(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		userID     string
		username   string
		credential auth.CredentialKind
	}{
		{name: "missing user id", username: "alice", credential: auth.CredentialAccountPassword},
		{name: "missing username", userID: "user-1", credential: auth.CredentialAccountPassword},
		{name: "none credential", userID: "user-1", username: "alice", credential: auth.CredentialNone},
		{name: "unknown credential", userID: "user-1", username: "alice", credential: auth.CredentialKind("cookie")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := auth.NewUserPrincipal(tc.userID, tc.username, tc.credential, auth.UnrestrictedScopes()); err == nil {
				t.Fatal("NewUserPrincipal unexpectedly succeeded")
			}
		})
	}

	if _, ok := auth.PrincipalFromContext(context.Background()); ok {
		t.Fatal("empty context unexpectedly contained a principal")
	}
	principal, err := auth.NewUserPrincipal("user-1", "alice", auth.CredentialJWT,
		auth.RestrictedScopes(auth.ActionTokenRead))
	if err != nil {
		t.Fatalf("NewUserPrincipal: %v", err)
	}
	ctx := auth.ContextWithPrincipal(context.Background(), principal)
	got, ok := auth.PrincipalFromContext(ctx)
	if !ok || got.UserID() != principal.UserID() || got.Username() != principal.Username() {
		t.Fatal("principal did not round trip through context")
	}
}

func TestGitOperationValues(t *testing.T) {
	t.Parallel()

	if auth.GitOperationRead != "read" || auth.GitOperationWrite != "write" {
		t.Fatalf("Git operations = %q/%q", auth.GitOperationRead, auth.GitOperationWrite)
	}
}

func TestApprovedActionValues(t *testing.T) {
	t.Parallel()

	got := []auth.Action{
		auth.ActionMarketplaceRead,
		auth.ActionPluginRead,
		auth.ActionRepositoryRead,
		auth.ActionRepositoryWrite,
		auth.ActionTokenRead,
		auth.ActionTokenWrite,
	}
	want := []string{"marketplace.read", "plugin.read", "repository.read", "repository.write", "token.read", "token.write"}
	for i := range got {
		if string(got[i]) != want[i] {
			t.Fatalf("action %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResolvedJWTPATPrincipalsRequireUserID(t *testing.T) {
	t.Parallel()

	for _, credential := range []auth.CredentialKind{auth.CredentialJWT, auth.CredentialPAT} {
		principal, err := auth.NewUserPrincipal("user-1", "alice", credential, auth.UnrestrictedScopes())
		if err != nil || principal.CredentialKind() != credential {
			t.Fatalf("NewUserPrincipal(%q) = %#v, %v", credential, principal, err)
		}
		if _, err := auth.NewUserPrincipal("", "alice", credential, auth.UnrestrictedScopes()); err == nil {
			t.Fatalf("NewUserPrincipal(%q) accepted unresolved user", credential)
		}
	}
}

var (
	_ auth.GitPATAuthenticator          = gitPATAuthenticatorStub{}
	_ auth.SubscriptionPATAuthenticator = subscriptionPATAuthenticatorStub{}
	_ auth.Authorizer                   = authorizerStub{}
)

type gitPATAuthenticatorStub struct{}

func (gitPATAuthenticatorStub) AuthenticateGitPAT(context.Context, string, string, auth.GitOperation) (auth.Principal, error) {
	return auth.AnonymousPrincipal(), nil
}

type subscriptionPATAuthenticatorStub struct{}

func (subscriptionPATAuthenticatorStub) AuthenticateSubscriptionPAT(context.Context, string, string) (auth.Principal, error) {
	return auth.AnonymousPrincipal(), nil
}

type authorizerStub struct{}

func (authorizerStub) Authorize(context.Context, auth.Principal, auth.Action, auth.ResourceRef) error {
	return nil
}
