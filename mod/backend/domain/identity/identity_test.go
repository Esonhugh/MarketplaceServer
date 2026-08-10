package identity

import (
	"encoding/base64"
	"errors"
	"reflect"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

func TestMigrationModelsFollowDependencyOrder(t *testing.T) {
	want := []reflect.Type{
		reflect.TypeOf(&User{}), reflect.TypeOf(&Namespace{}), reflect.TypeOf(&SystemGroup{}),
		reflect.TypeOf(&UserGroupMembership{}), reflect.TypeOf(&PersonalAccessToken{}),
		reflect.TypeOf(&PersonalAccessTokenScope{}),
	}
	models := MigrationModels()
	if len(models) != len(want) {
		t.Fatalf("migration model count = %d, want %d", len(models), len(want))
	}
	for i := range want {
		if got := reflect.TypeOf(models[i]); got != want[i] {
			t.Fatalf("migration model %d = %v, want %v", i, got, want[i])
		}
	}

	models[0] = &Namespace{}
	if got := reflect.TypeOf(MigrationModels()[0]); got != reflect.TypeOf(&User{}) {
		t.Fatalf("caller mutated migration model registry: first model = %v", got)
	}
}

func TestIdentityModelGuards(t *testing.T) {
	canonicalEmail := "alice@example.com"
	invalidUsers := []User{
		{Username: "Alice", Status: UserStatusActive},
		{Username: "alice", Status: "unknown"},
		{Username: "alice", Status: UserStatusActive, Email: func() *string { value := "Alice@Example.com"; return &value }()},
	}
	for _, user := range invalidUsers {
		if err := user.BeforeCreate(nil); !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("invalid user %#v error = %v", user, err)
		}
	}
	validUser := User{Username: "alice", Status: UserStatusActive, Email: &canonicalEmail}
	if err := validUser.BeforeCreate(nil); err != nil {
		t.Fatalf("valid user error = %v", err)
	}

	ownerID := "user-1"
	invalidNamespaces := []Namespace{
		{Kind: NamespaceKindUser, Slug: "alice"},
		{Kind: NamespaceKindTeam, Slug: "team", OwnerUserID: &ownerID},
		{Kind: "unknown", Slug: "unknown"},
		{Kind: NamespaceKindTeam, Slug: "Not-Canonical"},
	}
	for _, namespace := range invalidNamespaces {
		if err := namespace.BeforeCreate(nil); !errors.Is(err, ErrInvalidNamespace) {
			t.Fatalf("invalid namespace %#v error = %v", namespace, err)
		}
	}

	if err := (&SystemGroup{ID: AdminSystemGroupID, Name: AdminSystemGroupName, Description: "tampered"}).BeforeCreate(nil); !errors.Is(err, ErrInvalidSystemGroup) {
		t.Fatalf("mismatched fixed group create error = %v", err)
	}
	if err := (&SystemGroup{ID: "arbitrary", Name: "arbitrary", Description: "arbitrary"}).BeforeCreate(nil); !errors.Is(err, ErrInvalidSystemGroup) {
		t.Fatalf("arbitrary system group create error = %v", err)
	}

	for _, id := range []string{AdminSystemGroupID, DefaultSystemGroupID} {
		group := SystemGroup{ID: id}
		if err := group.BeforeUpdate(nil); !errors.Is(err, ErrImmutableSystemGroup) {
			t.Fatalf("fixed group %s update error = %v", id, err)
		}
		if err := group.BeforeDelete(nil); !errors.Is(err, ErrImmutableSystemGroup) {
			t.Fatalf("fixed group %s delete error = %v", id, err)
		}
	}

	membership := UserGroupMembership{GroupID: DefaultSystemGroupID}
	if err := membership.BeforeCreate(nil); !errors.Is(err, ErrPersistedDefaultMembership) {
		t.Fatalf("default membership create error = %v", err)
	}
	if err := membership.BeforeUpdate(nil); !errors.Is(err, ErrPersistedDefaultMembership) {
		t.Fatalf("default membership update error = %v", err)
	}

	scope := PersonalAccessTokenScope{Action: auth.Action("unknown")}
	if err := scope.BeforeSave(nil); !errors.Is(err, ErrInvalidTokenScope) {
		t.Fatalf("invalid scope save error = %v", err)
	}
}

func TestTokenScopes(t *testing.T) {
	scopes, err := tokenScopes([]PersonalAccessTokenScope{{Action: auth.ActionRepositoryRead}})
	if err != nil || !scopes.Allows(auth.ActionRepositoryRead) || scopes.Allows(auth.ActionRepositoryWrite) {
		t.Fatalf("token scopes = %#v, %v", scopes, err)
	}
	if _, err := tokenScopes([]PersonalAccessTokenScope{{Action: auth.Action("unknown")}}); !errors.Is(err, ErrInvalidTokenScope) {
		t.Fatalf("invalid token scope error = %v", err)
	}
}

func TestAPIKeyPepperValidation(t *testing.T) {
	for name, value := range map[string]string{
		"missing": "", "malformed": "not-base64", "short": base64.StdEncoding.EncodeToString(make([]byte, 31)),
	} {
		t.Run(name, func(t *testing.T) {
			env := MapEnvironment{}
			if value != "" {
				env[APIKeyPepperEnvironment] = value
			}
			if _, err := APIKeyPepper(env); !errors.Is(err, ErrInvalidAPIKeyPepper) {
				t.Fatalf("APIKeyPepper error = %v", err)
			}
		})
	}
}

func TestIdentityDatabaseHelpersRejectNil(t *testing.T) {
	if _, err := NewRepository(nil); err == nil {
		t.Fatal("NewRepository(nil) succeeded")
	}
	if _, err := NewBootstrapper(nil, nil); err == nil {
		t.Fatal("NewBootstrapper(nil) succeeded")
	}
	if err := ensureSystemGroups(nil); err == nil {
		t.Fatal("ensureSystemGroups(nil) succeeded")
	}
	if err := lockBootstrapTransaction(nil); err == nil {
		t.Fatal("lockBootstrapTransaction(nil) succeeded")
	}
}
