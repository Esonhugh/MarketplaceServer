package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

type jwtVerifierFake struct {
	encoded  string
	username string
	err      error
}

func (fake *jwtVerifierFake) Verify(encoded string) (string, error) {
	fake.encoded = encoded
	return fake.username, fake.err
}

type principalResolverFake struct {
	username  string
	principal auth.Principal
	err       error
	calls     int
}

func (fake *principalResolverFake) ResolveJWTPrincipal(_ context.Context, username string) (auth.Principal, error) {
	fake.calls++
	fake.username = username
	return fake.principal, fake.err
}

func TestJWTManagementAuthenticatorVerifiesThenResolvesPrincipal(t *testing.T) {
	verifier := &jwtVerifierFake{username: "alice"}
	resolver := &principalResolverFake{principal: handlerPrincipal(t)}

	principal, err := NewManagementAuthenticator(verifier, resolver).AuthenticateBearer(context.Background(), "encoded-jwt")
	if err != nil {
		t.Fatalf("AuthenticateBearer() error = %v", err)
	}
	if verifier.encoded != "encoded-jwt" || resolver.username != "alice" || principal.CredentialKind() != auth.CredentialJWT {
		t.Fatalf("verification/resolution = %q/%q/%q", verifier.encoded, resolver.username, principal.CredentialKind())
	}
}

func TestJWTManagementAuthenticatorDoesNotResolveInvalidJWT(t *testing.T) {
	verifier := &jwtVerifierFake{err: errors.New("invalid")}
	resolver := &principalResolverFake{}
	if _, err := NewManagementAuthenticator(verifier, resolver).AuthenticateBearer(context.Background(), "invalid"); err == nil {
		t.Fatal("AuthenticateBearer() error = nil")
	}
	if resolver.calls != 0 {
		t.Fatalf("ResolveJWTPrincipal() calls = %d", resolver.calls)
	}
}
