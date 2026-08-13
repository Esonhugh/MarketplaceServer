package dao

import (
	"errors"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

func TestMySQLFixedSystemGroupTriggerAllowsOnlyExactNoOpUpdates(t *testing.T) {
	capture := &queryCaptureLogger{Interface: logger.Default.LogMode(logger.Info)}
	db, err := gorm.Open(mysql.New(mysql.Config{DSN: "user:pass@tcp(localhost:3306)/test", SkipInitializeWithVersion: true}), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, Logger: capture,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := installIdentityDatabaseGuards(db); err != nil {
		t.Fatalf("installIdentityDatabaseGuards: %v", err)
	}
	queries := capture.joined()
	const triggerName = "marketplace_system_groups_immutable_update"
	start := strings.Index(queries, "create trigger "+triggerName)
	if start < 0 {
		t.Fatalf("missing %s statement in SQL:\n%s", triggerName, queries)
	}
	statement := queries[start:]
	if end := strings.Index(statement, "\ndrop trigger"); end >= 0 {
		statement = statement[:end]
	}
	for _, comparison := range []string{
		"not (old.name <=> new.name)",
		"not (old.description <=> new.description)",
		"not (old.created_at <=> new.created_at)",
		"not (old.updated_at <=> new.updated_at)",
	} {
		if !strings.Contains(statement, comparison) {
			t.Fatalf("fixed group update trigger does not compare %q:\n%s", comparison, statement)
		}
	}
	if !strings.Contains(statement, " then signal sqlstate '45000'") {
		t.Fatalf("fixed group update trigger does not reject changed rows:\n%s", statement)
	}
}
