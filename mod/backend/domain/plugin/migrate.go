package plugin

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var (
	ErrLegacyPluginSchema  = errors.New("plugin: legacy independent-ID schema requires operator rebuild")
	ErrUnsupportedDatabase = errors.New("plugin: unsupported database")
)

var migrationModels = []any{
	&Plugin{},
	&Repository{},
	&PluginVersion{},
	&PluginVersionHistory{},
	&RepositoryOrphanCleanup{},
	&ReceiveBatch{},
	&ReceiveIntent{},
	&ProjectionArtifact{},
	&RevisionProjectionPointer{},
	&RevisionProjectionTransition{},
	&ProjectionGCJob{},
}

func MigrationModels() []any {
	return append([]any(nil), migrationModels...)
}

type schemaInspector interface {
	HasTable(any) bool
	HasColumn(any, string) bool
}

func Migrate(db *gorm.DB) error {
	if db == nil {
		return errors.New("migrate plugin lifecycle models: nil database")
	}
	driver := strings.ToLower(strings.TrimSpace(db.Dialector.Name()))
	if driver != "postgres" && driver != "sqlite" {
		return fmt.Errorf("migrate plugin lifecycle models: %w %q", ErrUnsupportedDatabase, db.Dialector.Name())
	}
	if err := guardLegacySchema(db.Migrator()); err != nil {
		return fmt.Errorf("migrate plugin lifecycle models: %w", err)
	}
	if !db.Migrator().HasTable("namespaces") {
		return errors.New("migrate plugin lifecycle models: identity namespaces table is missing")
	}
	if driver == "sqlite" {
		if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
			return fmt.Errorf("enable SQLite foreign keys: %w", err)
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(migrationModels...); err != nil {
			return fmt.Errorf("migrate plugin lifecycle models: %w", err)
		}
		if err := installDatabaseGuards(tx, driver); err != nil {
			return fmt.Errorf("install plugin lifecycle database guards: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func guardLegacySchema(inspector schemaInspector) error {
	for _, table := range []string{"marketplace_revision_items", "plugin_distributions"} {
		if inspector.HasTable(table) && inspector.HasColumn(table, "plugin_version_id") {
			return ErrLegacyPluginSchema
		}
	}
	if inspector.HasTable("plugins") {
		for _, column := range []string{"repository_id", "name"} {
			if inspector.HasColumn("plugins", column) {
				return ErrLegacyPluginSchema
			}
		}
		for _, column := range []string{"archived_from", "default_version_tag"} {
			if !inspector.HasColumn("plugins", column) {
				return ErrLegacyPluginSchema
			}
		}
	}
	if inspector.HasTable("repositories") {
		for _, column := range []string{"namespace_id", "slug", "visibility", "default_branch"} {
			if inspector.HasColumn("repositories", column) {
				return ErrLegacyPluginSchema
			}
		}
	}
	if inspector.HasTable("plugin_versions") {
		for _, column := range []string{"version", "tag_name"} {
			if inspector.HasColumn("plugin_versions", column) {
				return ErrLegacyPluginSchema
			}
		}
		for _, column := range []string{"tag", "deleted_at"} {
			if !inspector.HasColumn("plugin_versions", column) {
				return ErrLegacyPluginSchema
			}
		}
	}
	return nil
}

func installDatabaseGuards(db *gorm.DB, driver string) error {
	var statements []string
	switch driver {
	case "postgres":
		statements = []string{
			`ALTER TABLE plugins DROP CONSTRAINT IF EXISTS chk_plugins_archived_from`,
			`ALTER TABLE plugins ADD CONSTRAINT chk_plugins_archived_from CHECK ((status = 'archived' AND archived_from IS NOT NULL AND archived_from IN ('draft','active')) OR (status <> 'archived' AND archived_from IS NULL))`,
			`ALTER TABLE repositories DROP CONSTRAINT IF EXISTS fk_repositories_plugin`,
			`ALTER TABLE repositories ADD CONSTRAINT fk_repositories_plugin FOREIGN KEY (id) REFERENCES plugins(id) ON UPDATE RESTRICT ON DELETE RESTRICT`,
			`ALTER TABLE plugin_versions DROP CONSTRAINT IF EXISTS chk_plugin_versions_content`,
			`ALTER TABLE plugin_versions ADD CONSTRAINT chk_plugin_versions_content CHECK ((status = 'available' AND commit_sha IS NOT NULL AND manifest_digest IS NOT NULL AND manifest_snapshot IS NOT NULL AND deleted_at IS NULL) OR (status = 'deleted' AND raw_tag_object_id IS NULL AND commit_sha IS NULL AND manifest_digest IS NULL AND manifest_snapshot IS NULL AND deleted_at IS NOT NULL))`,
			`CREATE OR REPLACE FUNCTION marketplace_reject_plugin_immutable_update() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'immutable plugin lifecycle data'; END; $$ LANGUAGE plpgsql`,
			`DROP TRIGGER IF EXISTS marketplace_plugins_identity_immutable ON plugins`,
			`CREATE TRIGGER marketplace_plugins_identity_immutable BEFORE UPDATE OF id, namespace_id, slug, created_at ON plugins FOR EACH ROW WHEN (OLD.id IS DISTINCT FROM NEW.id OR OLD.namespace_id IS DISTINCT FROM NEW.namespace_id OR OLD.slug IS DISTINCT FROM NEW.slug OR OLD.created_at IS DISTINCT FROM NEW.created_at) EXECUTE FUNCTION marketplace_reject_plugin_immutable_update()`,
			`DROP TRIGGER IF EXISTS marketplace_version_history_immutable ON plugin_version_history`,
			`CREATE TRIGGER marketplace_version_history_immutable BEFORE UPDATE OR DELETE ON plugin_version_history FOR EACH ROW EXECUTE FUNCTION marketplace_reject_plugin_immutable_update()`,
			`DROP TRIGGER IF EXISTS marketplace_projection_artifact_immutable ON projection_artifacts`,
			`CREATE TRIGGER marketplace_projection_artifact_immutable BEFORE UPDATE OF id, kind, plugin_id, tag, source_object_id, source_commit_sha, source_tree_sha, revision_id, content_digest, distribution_sha, storage_key, created_at ON projection_artifacts FOR EACH ROW EXECUTE FUNCTION marketplace_reject_plugin_immutable_update()`,
			`CREATE OR REPLACE FUNCTION marketplace_validate_plugin_default() RETURNS trigger AS $$ BEGIN IF NEW.default_version_tag IS NOT NULL AND NOT EXISTS (SELECT 1 FROM plugin_versions v WHERE v.plugin_id = NEW.id AND v.tag = NEW.default_version_tag AND v.status = 'available') THEN RAISE EXCEPTION 'default version is unavailable'; END IF; RETURN NEW; END; $$ LANGUAGE plpgsql`,
			`DROP TRIGGER IF EXISTS marketplace_plugins_default_available ON plugins`,
			`CREATE TRIGGER marketplace_plugins_default_available BEFORE INSERT OR UPDATE OF default_version_tag ON plugins FOR EACH ROW EXECUTE FUNCTION marketplace_validate_plugin_default()`,
		}
	case "sqlite":
		statements = []string{
			`CREATE TRIGGER IF NOT EXISTS marketplace_plugins_identity_immutable BEFORE UPDATE OF id, namespace_id, slug, created_at ON plugins FOR EACH ROW WHEN OLD.id IS NOT NEW.id OR OLD.namespace_id IS NOT NEW.namespace_id OR OLD.slug IS NOT NEW.slug OR OLD.created_at IS NOT NEW.created_at BEGIN SELECT RAISE(ABORT, 'immutable plugin identity'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_version_history_immutable_update BEFORE UPDATE ON plugin_version_history FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'immutable version history'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_version_history_immutable_delete BEFORE DELETE ON plugin_version_history FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'immutable version history'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_projection_artifact_immutable BEFORE UPDATE OF id, kind, plugin_id, tag, source_object_id, source_commit_sha, source_tree_sha, revision_id, content_digest, distribution_sha, storage_key, created_at ON projection_artifacts FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'immutable projection artifact'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_plugins_default_available BEFORE INSERT ON plugins FOR EACH ROW WHEN NEW.default_version_tag IS NOT NULL AND NOT EXISTS (SELECT 1 FROM plugin_versions v WHERE v.plugin_id = NEW.id AND v.tag = NEW.default_version_tag AND v.status = 'available') BEGIN SELECT RAISE(ABORT, 'default version is unavailable'); END`,
			`CREATE TRIGGER IF NOT EXISTS marketplace_plugins_default_available_update BEFORE UPDATE OF default_version_tag ON plugins FOR EACH ROW WHEN NEW.default_version_tag IS NOT NULL AND NOT EXISTS (SELECT 1 FROM plugin_versions v WHERE v.plugin_id = NEW.id AND v.tag = NEW.default_version_tag AND v.status = 'available') BEGIN SELECT RAISE(ABORT, 'default version is unavailable'); END`,
		}
	default:
		return fmt.Errorf("%w %q", ErrUnsupportedDatabase, driver)
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
