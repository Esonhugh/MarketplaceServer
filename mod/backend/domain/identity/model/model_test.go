package model

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
)

func TestMigrationModelsFollowDependencyOrder(t *testing.T) {
	want := []reflect.Type{
		reflect.TypeOf(&User{}), reflect.TypeOf(&Namespace{}), reflect.TypeOf(&SystemGroup{}),
		reflect.TypeOf(&UserGroupMembership{}), reflect.TypeOf(&TeamMembership{}),
		reflect.TypeOf(&TeamInvitation{}), reflect.TypeOf(&PersonalAccessToken{}),
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

	plaintext, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	secretHMAC, err := auth.IndexAPIKey(plaintext, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	validToken := PersonalAccessToken{ID: uuid.NewString(), UserID: uuid.NewString(), Preset: TokenPresetGitWrite, SecretPlaintext: plaintext, SecretHMAC: secretHMAC}
	if err := validToken.BeforeCreate(nil); err != nil {
		t.Fatalf("valid token error = %v", err)
	}
	for name, mutate := range map[string]func(*PersonalAccessToken){
		"non-canonical ID":  func(token *PersonalAccessToken) { token.ID = strings.ToUpper(token.ID) },
		"invalid owner ID":  func(token *PersonalAccessToken) { token.UserID = "user-1" },
		"invalid plaintext": func(token *PersonalAccessToken) { token.SecretPlaintext = "mpsk_invalid" },
		"empty HMAC":        func(token *PersonalAccessToken) { token.SecretHMAC = "" },
		"malformed HMAC":    func(token *PersonalAccessToken) { token.SecretHMAC = auth.APIKeyIndexPrefix + "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			token := validToken
			mutate(&token)
			if err := token.BeforeCreate(nil); !errors.Is(err, ErrInvalidPersonalAccessToken) {
				t.Fatalf("invalid token error = %v", err)
			}
		})
	}
	invalidPreset := validToken
	invalidPreset.Preset = "unknown"
	if err := invalidPreset.BeforeCreate(nil); !errors.Is(err, ErrInvalidTokenPreset) {
		t.Fatalf("invalid preset error = %v", err)
	}

	createdAt := time.Date(2026, time.August, 12, 2, 0, 0, 0, time.UTC)
	for name, expiresAt := range map[string]time.Time{
		"equal to creation": createdAt,
		"before creation":   createdAt.Add(-time.Nanosecond),
	} {
		t.Run("expiry "+name, func(t *testing.T) {
			token := validToken
			token.CreatedAt = createdAt
			token.ExpiresAt = &expiresAt
			if err := token.BeforeCreate(nil); !errors.Is(err, ErrInvalidPersonalAccessToken) {
				t.Fatalf("invalid token expiry error = %v", err)
			}
		})
	}
	validExpiry := createdAt.Add(time.Nanosecond)
	validToken.CreatedAt = createdAt
	validToken.ExpiresAt = &validExpiry
	if err := validToken.BeforeCreate(nil); err != nil {
		t.Fatalf("future token expiry error = %v", err)
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
