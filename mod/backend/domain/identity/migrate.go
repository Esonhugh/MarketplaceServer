package identity

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var migrationModels = []any{
	&User{},
	&Namespace{},
	&SystemGroup{},
	&UserGroupMembership{},
	&PersonalAccessToken{},
	&PersonalAccessTokenScope{},
}

func MigrationModels() []any {
	return append([]any(nil), migrationModels...)
}

func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate identity models: nil database")
	}
	if err := db.AutoMigrate(migrationModels...); err != nil {
		return fmt.Errorf("migrate identity models: %w", err)
	}
	if err := installIdentityDatabaseGuards(db); err != nil {
		return fmt.Errorf("install identity database guards: %w", err)
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
			`CREATE TRIGGER marketplace_system_groups_immutable_update BEFORE UPDATE ON system_groups FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'immutable system group'`,
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
