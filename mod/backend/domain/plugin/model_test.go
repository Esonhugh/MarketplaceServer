package plugin

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPluginLifecycleRecordContracts(t *testing.T) {
	assertTableFields(t, reflect.TypeOf(Plugin{}), []string{
		"ID", "NamespaceID", "Slug", "Visibility", "Status", "ArchivedFrom", "DefaultVersionTag", "CreatedAt", "UpdatedAt",
	})
	assertTableFields(t, reflect.TypeOf(PluginVersion{}), []string{
		"ID", "PluginID", "Tag", "Status", "RawTagObjectID", "CommitSHA", "ManifestDigest", "ManifestSnapshot", "PublishedAt", "UpdatedAt", "DeletedAt",
	})
	for name, record := range map[string]any{
		"history":               PluginVersionHistory{},
		"orphan cleanup":        RepositoryOrphanCleanup{},
		"receive batch":         ReceiveBatch{},
		"receive intent":        ReceiveIntent{},
		"projection artifact":   ProjectionArtifact{},
		"projection transition": RevisionProjectionTransition{},
		"stable pointer":        RevisionProjectionPointer{},
		"gc job":                ProjectionGCJob{},
	} {
		t.Run(name, func(t *testing.T) {
			typeOf := reflect.TypeOf(record)
			if _, ok := typeOf.FieldByName("ID"); !ok {
				t.Fatalf("%s has no stable ID", typeOf.Name())
			}
			if _, ok := typeOf.FieldByName("State"); !ok && name != "history" && name != "stable pointer" {
				t.Fatalf("%s has no durable state", typeOf.Name())
			}
		})
	}
}

func assertTableFields(t *testing.T, record reflect.Type, fields []string) {
	t.Helper()
	for _, field := range fields {
		if _, exists := record.FieldByName(field); !exists {
			t.Errorf("%s.%s is missing", record.Name(), field)
		}
	}
}

func TestMigrationModelsDefensivelyCopied(t *testing.T) {
	models := MigrationModels()
	models[0] = &Repository{}
	if got := reflect.TypeOf(MigrationModels()[0]); got != reflect.TypeOf(&Plugin{}) {
		t.Fatalf("MigrationModels() leaked mutable backing array: %v", got)
	}
}

func TestSQLiteMigrationEnforcesSharedIDAndLifecycleConstraints(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.User{}, &identitymodel.Namespace{}); err != nil {
		t.Fatalf("migrate identity fixture: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate(sqlite) error = %v", err)
	}

	namespaceID := uuid.NewString()
	if err := db.Create(&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security"}).Error; err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	now := time.Now().UTC()
	pluginID := uuid.NewString()
	aggregate := Plugin{ID: pluginID, NamespaceID: namespaceID, Slug: "scanner", Visibility: VisibilityPublic, Status: PluginStatusDraft, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&aggregate).Error; err != nil {
		t.Fatalf("create Plugin: %v", err)
	}
	if err := db.Create(&Repository{ID: pluginID, StorageKey: uuid.NewString(), Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("create shared-ID Repository: %v", err)
	}
	if err := db.Create(&Repository{ID: uuid.NewString(), StorageKey: uuid.NewString(), Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now}).Error; err == nil {
		t.Fatal("orphan Repository was accepted")
	}
	if err := db.Create(&Plugin{ID: uuid.NewString(), NamespaceID: namespaceID, Slug: aggregate.Slug, Visibility: VisibilityPrivate, Status: PluginStatusDraft, CreatedAt: now, UpdatedAt: now}).Error; err == nil {
		t.Fatal("duplicate namespace/slug Plugin was accepted")
	}
	otherNamespaceID := uuid.NewString()
	if err := db.Create(&identitymodel.Namespace{ID: otherNamespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "other", DisplayName: "Other"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Plugin{ID: uuid.NewString(), NamespaceID: otherNamespaceID, Slug: aggregate.Slug, Visibility: VisibilityPrivate, Status: PluginStatusDraft, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("same slug in another namespace was rejected: %v", err)
	}
	if err := db.Create(&Plugin{ID: uuid.NewString(), NamespaceID: namespaceID, Slug: "bad-status", Visibility: VisibilityPublic, Status: "unknown", CreatedAt: now, UpdatedAt: now}).Error; err == nil {
		t.Fatal("invalid Plugin status was accepted")
	}
	if err := db.Create(&Plugin{ID: uuid.NewString(), NamespaceID: namespaceID, Slug: "bad-archive", Visibility: VisibilityPublic, Status: PluginStatusArchived, CreatedAt: now, UpdatedAt: now}).Error; err == nil {
		t.Fatal("archived Plugin without archived_from was accepted")
	}

	commit := strings.Repeat("a", 40)
	digest := strings.Repeat("b", 64)
	version := PluginVersion{ID: uuid.NewString(), PluginID: pluginID, Tag: "v1.0.0", Status: VersionStatusAvailable, RawTagObjectID: &commit, CommitSHA: &commit, ManifestDigest: &digest, ManifestSnapshot: []byte(`{}`), PublishedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&version).Error; err != nil {
		t.Fatalf("create available Version: %v", err)
	}
	duplicate := version
	duplicate.ID = uuid.NewString()
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate Plugin/tag Version was accepted")
	}
	missingRawTag := PluginVersion{ID: uuid.NewString(), PluginID: pluginID, Tag: "v1.1.0", Status: VersionStatusAvailable, CommitSHA: &commit, ManifestDigest: &digest, ManifestSnapshot: []byte(`{}`), PublishedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&missingRawTag).Error; err == nil {
		t.Fatal("available Version without raw tag object ID was accepted")
	}
	invalidDeleted := PluginVersion{ID: uuid.NewString(), PluginID: pluginID, Tag: "v2.0.0", Status: VersionStatusDeleted, CommitSHA: &commit, PublishedAt: now, CreatedAt: now, UpdatedAt: now, DeletedAt: &now}
	if err := db.Create(&invalidDeleted).Error; err == nil {
		t.Fatal("deleted Version retaining current content was accepted")
	}
}

func TestSQLiteDefaultVersionTagReferencesAvailableVersion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.User{}, &identitymodel.Namespace{}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	namespaceID := uuid.NewString()
	pluginID := uuid.NewString()
	for _, record := range []any{
		&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security"},
		&Plugin{ID: pluginID, NamespaceID: namespaceID, Slug: "scanner", Visibility: VisibilityPublic, Status: PluginStatusActive, CreatedAt: now, UpdatedAt: now},
		&Repository{ID: pluginID, StorageKey: uuid.NewString(), Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := SetDefaultVersion(t.Context(), db, namespaceID, pluginID, "v1.0.0"); !errors.Is(err, ErrVersionNotAvailable) {
		t.Fatalf("set missing default error = %v, want ErrVersionNotAvailable", err)
	}
	commit := strings.Repeat("a", 40)
	digest := strings.Repeat("b", 64)
	version := PluginVersion{ID: uuid.NewString(), PluginID: pluginID, Tag: "v1.0.0", Status: VersionStatusAvailable, RawTagObjectID: &commit, CommitSHA: &commit, ManifestDigest: &digest, ManifestSnapshot: []byte(`{}`), PublishedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	if err := SetDefaultVersion(t.Context(), db, namespaceID, pluginID, version.Tag); err != nil {
		t.Fatalf("SetDefaultVersion() error = %v", err)
	}
	var persisted Plugin
	if err := db.Where("namespace_id = ? AND id = ?", namespaceID, pluginID).Take(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.DefaultVersionTag == nil || *persisted.DefaultVersionTag != version.Tag {
		t.Fatalf("default version = %v, want %q", persisted.DefaultVersionTag, version.Tag)
	}
}

func TestPluginRepositoryQueriesRequireTenantScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.User{}, &identitymodel.Namespace{}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	namespaceA := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "alpha", DisplayName: "Alpha"}
	namespaceB := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "bravo", DisplayName: "Bravo"}
	for _, namespace := range []identitymodel.Namespace{namespaceA, namespaceB} {
		if err := db.Create(&namespace).Error; err != nil {
			t.Fatal(err)
		}
		plugin := Plugin{ID: uuid.NewString(), NamespaceID: namespace.ID, Slug: "scanner", Visibility: VisibilityPublic, Status: PluginStatusDraft, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&plugin).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&Repository{ID: plugin.ID, StorageKey: uuid.NewString(), Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := repository.FindBySlug(t.Context(), namespaceA.ID, "scanner")
	if err != nil {
		t.Fatal(err)
	}
	if got.Plugin.NamespaceID != namespaceA.ID || got.Plugin.Slug != "scanner" || got.Repository.ID != got.Plugin.ID {
		t.Fatalf("FindBySlug() returned cross-tenant aggregate: %#v", got)
	}
	if _, err := repository.FindBySlug(t.Context(), "", "scanner"); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("empty tenant scope error = %v, want ErrInvalidScope", err)
	}
}

func TestReceiveBatchAllowsMultipleCanonicalTagTransitions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.User{}, &identitymodel.Namespace{}); err != nil {
		t.Fatalf("migrate identity fixture: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	namespaceID := uuid.NewString()
	pluginID := uuid.NewString()
	batchID := uuid.NewString()
	for _, record := range []any{
		&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security"},
		&Plugin{ID: pluginID, NamespaceID: namespaceID, Slug: "scanner", Visibility: VisibilityPublic, Status: PluginStatusActive, CreatedAt: now, UpdatedAt: now},
		&Repository{ID: pluginID, StorageKey: uuid.NewString(), Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now},
		&ReceiveBatch{ID: batchID, PluginID: pluginID, State: ReceiveBatchStatePrepared, CorrelationID: uuid.NewString(), CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatalf("create %T: %v", record, err)
		}
	}
	for _, tag := range []string{"v1.0.0", "v2.0.0"} {
		intent := ReceiveIntent{ID: uuid.NewString(), BatchID: batchID, PluginID: pluginID, Operation: ReceiveOperationDelete, Tag: tag, ExpectedOldObjectID: strings.Repeat("a", 40), ExpectedOldCommitSHA: strings.Repeat("a", 40), ExpectedVersionStatus: VersionStatusAvailable, State: ReceiveBatchStatePrepared, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&intent).Error; err != nil {
			t.Fatalf("create intent for %s: %v", tag, err)
		}
	}
	var count int64
	if err := db.Model(&ReceiveIntent{}).Where("batch_id = ? AND plugin_id = ?", batchID, pluginID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("batch transition count = %d, want 2", count)
	}
}

func TestMigrateRejectsUnsupportedDatabaseBeforeSchemaInspection(t *testing.T) {
	db, err := gorm.Open(mysql.New(mysql.Config{
		DSN: "user:pass@tcp(localhost:3306)/test", SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); !errors.Is(err, ErrUnsupportedDatabase) {
		t.Fatalf("Migrate(mysql) error = %v, want ErrUnsupportedDatabase", err)
	}
}

func TestLegacyIndependentIDSchemaRequiresRebuild(t *testing.T) {
	legacy := legacySchemaStub{tables: map[string]bool{"plugins": true, "repositories": true, "plugin_versions": true, "plugin_distributions": true}, columns: map[string]bool{
		"plugins.repository_id": true, "repositories.namespace_id": true, "plugin_versions.version": true, "plugin_distributions.plugin_version_id": true,
	}}
	if err := guardLegacySchema(legacy); !errors.Is(err, ErrLegacyPluginSchema) {
		t.Fatalf("guardLegacySchema() error = %v, want ErrLegacyPluginSchema", err)
	}
	fresh := legacySchemaStub{tables: map[string]bool{}}
	if err := guardLegacySchema(fresh); err != nil {
		t.Fatalf("guardLegacySchema(fresh) error = %v", err)
	}
}

type legacySchemaStub struct {
	tables  map[string]bool
	columns map[string]bool
}

func (stub legacySchemaStub) HasTable(value any) bool {
	name, _ := value.(string)
	return stub.tables[name]
}

func (stub legacySchemaStub) HasColumn(table any, column string) bool {
	name, _ := table.(string)
	return stub.columns[name+"."+column]
}

func TestMigrationModelsStartWithSharedIDPluginAggregate(t *testing.T) {
	models := MigrationModels()
	want := []reflect.Type{
		reflect.TypeOf(&Plugin{}),
		reflect.TypeOf(&Repository{}),
		reflect.TypeOf(&PluginVersion{}),
	}
	if len(models) < len(want) {
		t.Fatalf("migration model count = %d, want at least %d", len(models), len(want))
	}
	for index := range want {
		if got := reflect.TypeOf(models[index]); got != want[index] {
			t.Fatalf("migration model %d = %v, want %v", index, got, want[index])
		}
	}

	repository := reflect.TypeOf(Repository{})
	for _, forbidden := range []string{"NamespaceID", "Slug", "Visibility", "DefaultBranch"} {
		if _, exists := repository.FieldByName(forbidden); exists {
			t.Errorf("Repository retains product field %s", forbidden)
		}
	}
}
