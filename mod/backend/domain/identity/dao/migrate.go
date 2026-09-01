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
			`CREATE OR REPLACE FUNCTION marketplace_validate_team_namespace() RETURNS trigger AS $$ BEGIN IF NOT EXISTS (SELECT 1 FROM namespaces WHERE id = NEW.namespace_id AND kind = 'team') THEN RAISE EXCEPTION 'team namespace required'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql`,
			`DROP TRIGGER IF EXISTS marketplace_team_memberships_namespace ON team_memberships`,
			`CREATE TRIGGER marketplace_team_memberships_namespace BEFORE INSERT OR UPDATE ON team_memberships FOR EACH ROW EXECUTE FUNCTION marketplace_validate_team_namespace()`,
			`DROP TRIGGER IF EXISTS marketplace_team_invitations_namespace ON team_invitations`,
			`CREATE TRIGGER marketplace_team_invitations_namespace BEFORE INSERT OR UPDATE ON team_invitations FOR EACH ROW EXECUTE FUNCTION marketplace_validate_team_namespace()`,
			`DROP TRIGGER IF EXISTS marketplace_team_memberships_identity_immutable ON team_memberships`,
			`CREATE TRIGGER marketplace_team_memberships_identity_immutable BEFORE UPDATE OF namespace_id, user_id ON team_memberships FOR EACH ROW WHEN (OLD.namespace_id IS DISTINCT FROM NEW.namespace_id OR OLD.user_id IS DISTINCT FROM NEW.user_id) EXECUTE FUNCTION marketplace_reject_identity_immutable_update()`,
			`DROP TRIGGER IF EXISTS marketplace_team_invitations_identity_immutable ON team_invitations`,
			`CREATE TRIGGER marketplace_team_invitations_identity_immutable BEFORE UPDATE OF id, namespace_id, user_id, role, invited_by_user_id, expires_at, created_at ON team_invitations FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id OR OLD.namespace_id IS DISTINCT FROM NEW.namespace_id OR OLD.user_id IS DISTINCT FROM NEW.user_id OR OLD.role IS DISTINCT FROM NEW.role OR OLD.invited_by_user_id IS DISTINCT FROM NEW.invited_by_user_id OR OLD.expires_at IS DISTINCT FROM NEW.expires_at OR OLD.created_at IS DISTINCT FROM NEW.created_at) EXECUTE FUNCTION marketplace_reject_identity_immutable_update()`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uidx_team_invitations_pending ON team_invitations(namespace_id, user_id) WHERE accepted_at IS NULL AND rejected_at IS NULL AND revoked_at IS NULL`,
		}
		for _, statement := range statements {
			if err := db.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	case "sqlite":
		statements := []string{
			`CREATE TRIGGER IF NOT EXISTS marketplace_users_identity_immutable BEFORE UPDATE OF username, email ON users FOR EACH ROW WHEN OLD.username IS NOT NEW.username OR OLD.email IS NOT NEW.email BEGIN SELECT RAISE(ABORT, 'immutable identity field'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_namespaces_identity_immutable BEFORE UPDATE OF kind, slug, owner_user_id ON namespaces FOR EACH ROW WHEN OLD.kind IS NOT NEW.kind OR OLD.slug IS NOT NEW.slug OR OLD.owner_user_id IS NOT NEW.owner_user_id BEGIN SELECT RAISE(ABORT, 'immutable identity field'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_system_groups_immutable_update BEFORE UPDATE ON system_groups FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'immutable system group'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_system_groups_immutable_delete BEFORE DELETE ON system_groups FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'immutable system group'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_team_memberships_namespace_insert BEFORE INSERT ON team_memberships FOR EACH ROW WHEN NOT EXISTS (SELECT 1 FROM namespaces WHERE id = NEW.namespace_id AND kind = 'team') BEGIN SELECT RAISE(ABORT, 'team namespace required'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_team_memberships_namespace_update BEFORE UPDATE ON team_memberships FOR EACH ROW WHEN NOT EXISTS (SELECT 1 FROM namespaces WHERE id = NEW.namespace_id AND kind = 'team') BEGIN SELECT RAISE(ABORT, 'team namespace required'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_team_invitations_namespace_insert BEFORE INSERT ON team_invitations FOR EACH ROW WHEN NOT EXISTS (SELECT 1 FROM namespaces WHERE id = NEW.namespace_id AND kind = 'team') BEGIN SELECT RAISE(ABORT, 'team namespace required'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_team_invitations_namespace_update BEFORE UPDATE ON team_invitations FOR EACH ROW WHEN NOT EXISTS (SELECT 1 FROM namespaces WHERE id = NEW.namespace_id AND kind = 'team') BEGIN SELECT RAISE(ABORT, 'team namespace required'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_team_memberships_identity_immutable BEFORE UPDATE OF namespace_id, user_id ON team_memberships FOR EACH ROW WHEN OLD.namespace_id IS NOT NEW.namespace_id OR OLD.user_id IS NOT NEW.user_id BEGIN SELECT RAISE(ABORT, 'immutable identity field'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_team_invitations_identity_immutable BEFORE UPDATE OF id, namespace_id, user_id, role, invited_by_user_id, expires_at, created_at ON team_invitations FOR EACH ROW WHEN OLD.id IS NOT NEW.id OR OLD.namespace_id IS NOT NEW.namespace_id OR OLD.user_id IS NOT NEW.user_id OR OLD.role IS NOT NEW.role OR OLD.invited_by_user_id IS NOT NEW.invited_by_user_id OR OLD.expires_at IS NOT NEW.expires_at OR OLD.created_at IS NOT NEW.created_at BEGIN SELECT RAISE(ABORT, 'immutable identity field'); END`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uidx_team_invitations_pending ON team_invitations(namespace_id, user_id) WHERE accepted_at IS NULL AND rejected_at IS NULL AND revoked_at IS NULL`,
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
