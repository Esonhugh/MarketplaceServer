package dao

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"gorm.io/gorm"
)

var ErrLegacyIdentitySchema = errors.New("identity: legacy credential schema requires operator rebuild")

func MigrationModels() []any {
	return model.MigrationModels()
}

func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate identity models: nil database")
	}
	if err := guardLegacyIdentitySchema(db.Migrator()); err != nil {
		return fmt.Errorf("migrate identity models: %w", err)
	}
	if err := db.AutoMigrate(model.MigrationModels()...); err != nil {
		return fmt.Errorf("migrate identity models: %w", err)
	}
	if err := installIdentityDatabaseGuards(db); err != nil {
		return fmt.Errorf("install identity database guards: %w", err)
	}
	return nil
}

type identitySchemaInspector interface {
	HasTable(any) bool
	HasColumn(any, string) bool
}

func guardLegacyIdentitySchema(inspector identitySchemaInspector) error {
	if inspector.HasTable("personal_access_token_scopes") {
		return ErrLegacyIdentitySchema
	}
	if !inspector.HasTable(&model.PersonalAccessToken{}) {
		return nil
	}
	for _, field := range []string{"Preset", "SecretPlaintext", "SecretHMAC"} {
		if !inspector.HasColumn(&model.PersonalAccessToken{}, field) {
			return ErrLegacyIdentitySchema
		}
	}
	return nil
}

func installIdentityDatabaseGuards(db *gorm.DB) error {
	switch strings.ToLower(db.Dialector.Name()) {
	case "postgres":
		statements := []string{
			`CREATE OR REPLACE FUNCTION marketplace_reject_identity_immutable_update() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'immutable identity field'; END; $$ LANGUAGE plpgsql`,
			`DROP TRIGGER IF EXISTS marketplace_users_identity_immutable ON users`,
			`CREATE TRIGGER marketplace_users_identity_immutable BEFORE UPDATE OF username, email ON users FOR EACH ROW WHEN (OLD.username IS DISTINCT FROM NEW.username OR OLD.email IS DISTINCT FROM NEW.email) EXECUTE FUNCTION marketplace_reject_identity_immutable_update()`,
			`DROP TRIGGER IF EXISTS marketplace_namespaces_identity_immutable ON namespaces`,
			`CREATE TRIGGER marketplace_namespaces_identity_immutable BEFORE UPDATE OF kind, slug, owner_user_id ON namespaces FOR EACH ROW WHEN (OLD.kind IS DISTINCT FROM NEW.kind OR OLD.slug IS DISTINCT FROM NEW.slug OR OLD.owner_user_id IS DISTINCT FROM NEW.owner_user_id) EXECUTE FUNCTION marketplace_reject_identity_immutable_update()`,
			`DROP TRIGGER IF EXISTS marketplace_system_groups_immutable ON system_groups`,
			`CREATE TRIGGER marketplace_system_groups_immutable BEFORE UPDATE OR DELETE ON system_groups FOR EACH ROW EXECUTE FUNCTION marketplace_reject_identity_immutable_update()`,
		}
		for _, statement := range statements {
			if err := db.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	case "mysql":
		statements := []string{
			`DROP TRIGGER IF EXISTS marketplace_users_identity_immutable`,
			`CREATE TRIGGER marketplace_users_identity_immutable BEFORE UPDATE ON users FOR EACH ROW BEGIN IF OLD.username <> NEW.username OR NOT (OLD.email <=> NEW.email) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'immutable identity field'; END IF; END`,
			`DROP TRIGGER IF EXISTS marketplace_namespaces_identity_immutable`,
			`CREATE TRIGGER marketplace_namespaces_identity_immutable BEFORE UPDATE ON namespaces FOR EACH ROW BEGIN IF OLD.kind <> NEW.kind OR OLD.slug <> NEW.slug OR NOT (OLD.owner_user_id <=> NEW.owner_user_id) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'immutable identity field'; END IF; END`,
			`DROP TRIGGER IF EXISTS marketplace_system_groups_immutable_update`,
			`CREATE TRIGGER marketplace_system_groups_immutable_update BEFORE UPDATE ON system_groups FOR EACH ROW BEGIN IF NOT (OLD.id <=> NEW.id) OR NOT (OLD.name <=> NEW.name) OR NOT (OLD.description <=> NEW.description) OR NOT (OLD.created_at <=> NEW.created_at) OR NOT (OLD.updated_at <=> NEW.updated_at) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'immutable system group'; END IF; END`,
			`DROP TRIGGER IF EXISTS marketplace_system_groups_immutable_delete`,
			`CREATE TRIGGER marketplace_system_groups_immutable_delete BEFORE DELETE ON system_groups FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'immutable system group'`,
		}
		for _, statement := range statements {
			if err := db.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported identity database driver %q", db.Dialector.Name())
	}
}
