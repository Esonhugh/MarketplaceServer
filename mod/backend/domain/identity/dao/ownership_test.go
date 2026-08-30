package dao

import (
	"errors"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
)

type schemaInspectorFake struct {
	tables  map[string]bool
	columns map[string]bool
}

func (fake schemaInspectorFake) HasTable(value any) bool {
	switch value.(type) {
	case *model.PersonalAccessToken:
		return fake.tables["personal_access_tokens"]
	default:
		return fake.tables[value.(string)]
	}
}

func (fake schemaInspectorFake) HasColumn(_ any, field string) bool { return fake.columns[field] }

func TestLegacyIdentitySchemaGuard(t *testing.T) {
	if err := guardLegacyIdentitySchema(schemaInspectorFake{tables: map[string]bool{"personal_access_token_scopes": true}}); !errors.Is(err, ErrLegacyIdentitySchema) {
		t.Fatalf("scope table guard error = %v", err)
	}
	if err := guardLegacyIdentitySchema(schemaInspectorFake{tables: map[string]bool{"personal_access_tokens": true}, columns: map[string]bool{"SecretHMAC": true}}); !errors.Is(err, ErrLegacyIdentitySchema) {
		t.Fatalf("legacy PAT guard error = %v", err)
	}
	if err := guardLegacyIdentitySchema(schemaInspectorFake{tables: map[string]bool{"personal_access_tokens": true}, columns: map[string]bool{"Preset": true, "SecretPlaintext": true, "SecretHMAC": true}}); err != nil {
		t.Fatalf("target schema guard error = %v", err)
	}
	if err := guardLegacyIdentitySchema(schemaInspectorFake{}); err != nil {
		t.Fatalf("clean schema guard error = %v", err)
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
