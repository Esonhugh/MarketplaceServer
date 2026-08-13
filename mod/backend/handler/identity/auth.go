package identity

import (
	"context"
	"errors"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

type JWTVerifier interface {
	Verify(string) (string, error)
}

type JWTPrincipalResolver interface {
	ResolveJWTPrincipal(context.Context, string) (auth.Principal, error)
}

type JWTManagementAuthenticator struct {
	verifier JWTVerifier
	resolver JWTPrincipalResolver
}

func NewManagementAuthenticator(verifier JWTVerifier, resolver JWTPrincipalResolver) *JWTManagementAuthenticator {
	return &JWTManagementAuthenticator{verifier: verifier, resolver: resolver}
}

func (authenticator *JWTManagementAuthenticator) AuthenticateBearer(ctx context.Context, encoded string) (auth.Principal, error) {
	if authenticator == nil || authenticator.verifier == nil || authenticator.resolver == nil || encoded == "" {
		return auth.Principal{}, errors.New("management authentication failed")
	}
	username, err := authenticator.verifier.Verify(encoded)
	if err != nil {
		return auth.Principal{}, errors.New("management authentication failed")
	}
	principal, err := authenticator.resolver.ResolveJWTPrincipal(ctx, username)
	if err != nil || !principal.IsUser() || principal.CredentialKind() != auth.CredentialJWT {
		return auth.Principal{}, errors.New("management authentication failed")
	}
	return principal, nil
}
