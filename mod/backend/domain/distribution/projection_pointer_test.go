package distribution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestResolvePluginUsesReadyRevisionProjectionArtifact(t *testing.T) {
	fixture := newPluginProjectionFixture(t, true)

	grant, err := NewAccessService(fixture.repository).ResolvePlugin(context.Background(), fixture.distributionID)
	if err != nil {
		t.Fatalf("ResolvePlugin() error = %v", err)
	}
	if grant.DistributionID != fixture.distributionID {
		t.Fatalf("distribution ID = %s, want %s", grant.DistributionID, fixture.distributionID)
	}
	if grant.Projection.Kind != gitservice.ProjectionKindPlugin {
		t.Fatalf("projection kind = %q, want plugin", grant.Projection.Kind)
	}
	if grant.Projection.StorageKey != fixture.artifactStorageKey {
		t.Fatalf("projection storage key = %q, want artifact key %q", grant.Projection.StorageKey, fixture.artifactStorageKey)
	}
	if grant.Projection.StorageKey == fixture.legacyStorageKey {
		t.Fatal("ResolvePlugin() trusted the legacy PluginDistribution storage pointer")
	}
}

func TestResolvePluginRejectsLegacyPublicationWithoutProjectionPointer(t *testing.T) {
	fixture := newPluginProjectionFixture(t, false)

	_, err := NewAccessService(fixture.repository).ResolvePlugin(context.Background(), fixture.distributionID)
	if !errors.Is(err, distributionservice.ErrUnavailable) {
		t.Fatalf("ResolvePlugin() missing pointer error = %v, want unavailable", err)
	}
}

func TestResolvePluginKeepsPublicArchivedPluginDistributionReadable(t *testing.T) {
	fixture := newPluginProjectionFixture(t, true)
	if err := fixture.db.Model(&plugindomain.Plugin{}).
		Where("id = ?", fixture.pluginID).
		Updates(map[string]any{"status": plugindomain.PluginStatusArchived, "archived_from": plugindomain.PluginStatusActive}).Error; err != nil {
		t.Fatal(err)
	}

	grant, err := NewAccessService(fixture.repository).ResolvePlugin(context.Background(), fixture.distributionID)
	if err != nil {
		t.Fatalf("ResolvePlugin() archived public Plugin error = %v", err)
	}
	if grant.Projection.StorageKey != fixture.artifactStorageKey {
		t.Fatalf("archived Plugin projection storage key = %q, want %q", grant.Projection.StorageKey, fixture.artifactStorageKey)
	}
}

func TestResolvePluginRejectsDraftPluginDistribution(t *testing.T) {
	fixture := newPluginProjectionFixture(t, true)
	if err := fixture.db.Model(&plugindomain.Plugin{}).
		Where("id = ?", fixture.pluginID).Update("status", plugindomain.PluginStatusDraft).Error; err != nil {
		t.Fatal(err)
	}

	_, err := NewAccessService(fixture.repository).ResolvePlugin(context.Background(), fixture.distributionID)
	if !errors.Is(err, distributionservice.ErrNotFound) {
		t.Fatalf("ResolvePlugin() draft Plugin error = %v, want not found", err)
	}
}

func TestProjectionArtifactSourceAndStorageAreDatabaseImmutable(t *testing.T) {
	for _, update := range []string{
		"UPDATE projection_artifacts SET source_commit_sha = ? WHERE id = ?",
		"UPDATE projection_artifacts SET storage_key = ? WHERE id = ?",
	} {
		fixture := newPluginProjectionFixture(t, true)
		if err := fixture.db.Exec(update, strings.Repeat("f", 40), fixture.artifactID).Error; err == nil {
			t.Fatalf("immutable artifact update succeeded: %s", update)
		}
	}
}

func TestResolvePluginFailsClosedForInvalidProjectionOrResourceState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture pluginProjectionFixture)
		want   error
	}{
		{
			name: "pending pointer",
			mutate: func(t *testing.T, fixture pluginProjectionFixture) {
				t.Helper()
				if err := fixture.db.Model(&plugindomain.RevisionProjectionPointer{}).
					Where("id = ?", fixture.pointerID).Update("available", false).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: distributionservice.ErrUnavailable,
		},
		{
			name: "artifact failed",
			mutate: func(t *testing.T, fixture pluginProjectionFixture) {
				t.Helper()
				if err := fixture.db.Model(&plugindomain.ProjectionArtifact{}).
					Where("id = ?", fixture.artifactID).Update("state", plugindomain.ArtifactStateFailed).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: distributionservice.ErrUnavailable,
		},
		{
			name: "deleted version",
			mutate: func(t *testing.T, fixture pluginProjectionFixture) {
				t.Helper()
				now := time.Now().UTC()
				if err := fixture.db.Model(&plugindomain.PluginVersion{}).
					Where("id = ?", fixture.versionID).
					Updates(map[string]any{
						"status":            plugindomain.VersionStatusDeleted,
						"raw_tag_object_id": nil,
						"commit_sha":        nil,
						"manifest_digest":   nil,
						"manifest_snapshot": nil,
						"deleted_at":        now,
					}).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: distributionservice.ErrGone,
		},
		{
			name: "repository error",
			mutate: func(t *testing.T, fixture pluginProjectionFixture) {
				t.Helper()
				if err := fixture.db.Model(&plugindomain.Repository{}).
					Where("id = ?", fixture.pluginID).Update("status", plugindomain.RepositoryStatusError).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: distributionservice.ErrUnavailable,
		},
		{
			name: "private plugin",
			mutate: func(t *testing.T, fixture pluginProjectionFixture) {
				t.Helper()
				if err := fixture.db.Model(&plugindomain.Plugin{}).
					Where("id = ?", fixture.pluginID).Update("visibility", plugindomain.VisibilityPrivate).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: distributionservice.ErrNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPluginProjectionFixture(t, true)
			test.mutate(t, fixture)

			_, err := NewAccessService(fixture.repository).ResolvePlugin(context.Background(), fixture.distributionID)
			if !errors.Is(err, test.want) {
				t.Fatalf("ResolvePlugin() error = %v, want %v", err, test.want)
			}
		})
	}
}

type pluginProjectionFixture struct {
	db                 *gorm.DB
	repository         *GORMRepository
	distributionID     uuid.UUID
	pluginID           string
	versionID          string
	pointerID          string
	artifactID         string
	legacyStorageKey   string
	artifactStorageKey string
}

func newPluginProjectionFixture(t *testing.T, withPointer bool) pluginProjectionFixture {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.Namespace{}); err != nil {
		t.Fatalf("migrate identity fixture: %v", err)
	}
	if err := plugindomain.Migrate(db); err != nil {
		t.Fatalf("migrate Plugin lifecycle fixture: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate distribution fixture: %v", err)
	}
	repository, err := NewGORMRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	namespaceID := uuid.NewString()
	pluginID := uuid.NewString()
	templateID := uuid.NewString()
	revisionID := uuid.NewString()
	distributionID := uuid.New()
	versionID := uuid.NewString()
	pointerID := uuid.NewString()
	artifactID := uuid.NewString()
	legacyStorageKey := "legacy-" + uuid.NewString()
	artifactStorageKey := "artifact-" + uuid.NewString()
	commitSHA := strings.Repeat("a", 40)
	manifestDigest := strings.Repeat("b", 64)

	for _, record := range []any{
		&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security", CreatedAt: now, UpdatedAt: now},
		&plugindomain.Plugin{ID: pluginID, NamespaceID: namespaceID, Slug: "scanner", Visibility: plugindomain.VisibilityPublic, Status: plugindomain.PluginStatusActive, CreatedAt: now, UpdatedAt: now},
		&plugindomain.Repository{ID: pluginID, StorageKey: uuid.NewString(), Status: plugindomain.RepositoryStatusReady, CreatedAt: now, UpdatedAt: now},
		&plugindomain.PluginVersion{ID: versionID, PluginID: pluginID, Tag: "v1.0.0", Status: plugindomain.VersionStatusAvailable, RawTagObjectID: &commitSHA, CommitSHA: &commitSHA, ManifestDigest: &manifestDigest, ManifestSnapshot: []byte(`{}`), PublishedAt: now, CreatedAt: now, UpdatedAt: now},
		&MarketplaceTemplate{ID: templateID, NamespaceID: namespaceID, Slug: "web", Name: "Web", Visibility: plugindomain.VisibilityPublic, Status: StatusActive, CreatedAt: now, UpdatedAt: now},
		&MarketplaceRevision{ID: revisionID, TemplateID: templateID, Revision: 1, ContentJSON: []byte(`{"name":"web"}`), ContentDigest: strings.Repeat("c", 64), Status: StatusActive, PublishedAt: now, CreatedAt: now},
		&PluginDistribution{ID: distributionID.String(), TemplateID: templateID, PluginID: pluginID, PluginTag: "v1.0.0", RepositoryID: pluginID, TagName: "v1.0.0", SourceTagType: "lightweight", SourceCommitSHA: commitSHA, SourceTreeSHA: strings.Repeat("d", 40), DistributionSHA: strings.Repeat("e", 40), StorageKey: legacyStorageKey, ContentDigest: strings.Repeat("f", 64), Status: StatusActive, CreatedAt: now, UpdatedAt: now},
		&MarketplaceRevisionItem{ID: uuid.NewString(), RevisionID: revisionID, PluginID: pluginID, PluginTag: "v1.0.0", PluginDistributionID: distributionID.String(), SourceURL: "https://marketplace.example/distribution/plugins/" + distributionID.String() + ".git", DistributionSHA: strings.Repeat("e", 40), Position: 0, CreatedAt: now},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatalf("create fixture %T: %v", record, err)
		}
	}
	if err := db.Model(&MarketplaceTemplate{}).Where("id = ?", templateID).Update("published_revision_id", revisionID).Error; err != nil {
		t.Fatalf("activate fixture revision: %v", err)
	}
	if withPointer {
		revisionIDCopy := revisionID
		if err := db.Create(&plugindomain.ProjectionArtifact{
			ID: artifactID, Kind: plugindomain.ArtifactKindPlugin, PluginID: pluginID, Tag: "v1.0.0",
			SourceObjectID: commitSHA, SourceCommitSHA: commitSHA, SourceTreeSHA: strings.Repeat("d", 40), RevisionID: &revisionIDCopy,
			ContentDigest: strings.Repeat("f", 64), DistributionSHA: strings.Repeat("e", 40), StorageKey: artifactStorageKey,
			State: plugindomain.ArtifactStateReady, CreatedAt: now, UpdatedAt: now, ReadyAt: &now,
		}).Error; err != nil {
			t.Fatalf("create ready Plugin projection artifact: %v", err)
		}
		if err := db.Create(&plugindomain.RevisionProjectionPointer{
			ID: pointerID, RevisionID: revisionID, PluginID: pluginID, Tag: "v1.0.0", ArtifactID: &artifactID, Generation: 1, Available: true, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatalf("create available revision projection pointer: %v", err)
		}
	}

	return pluginProjectionFixture{
		db: db, repository: repository, distributionID: distributionID, pluginID: pluginID, versionID: versionID,
		pointerID: pointerID, artifactID: artifactID, legacyStorageKey: legacyStorageKey, artifactStorageKey: artifactStorageKey,
	}
}
