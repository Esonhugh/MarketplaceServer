package receive

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPrepareCandidateOnlyCreatesNoDurableState(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	sessionID := uuid.NewString()
	coordination, err := fixture.coordinator.Open(context.Background(), fixture.pluginID, sessionID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer coordination.Close()

	prepared, err := coordination.Prepare(context.Background(), gitservice.ReceiveBatch{
		SessionID: sessionID,
		Plugin:    gitservice.ReceivePlugin{ID: fixture.pluginID, Name: "scanner"},
		CanonicalTags: []gitservice.ReceiveTagCommand{{
			Tag: "v2.0.0", RefName: "refs/tags/v2.0.0", Operation: gitservice.ReceiveTagCreate,
			OldObjectID: zeroObjectID(), NewObjectID: objectID("b"), NewCommitObjectID: objectID("b"),
		}},
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared.ID != "" || len(prepared.Transitions) != 0 {
		t.Fatalf("Prepare() = %#v, want empty candidate result", prepared)
	}
	var batches, intents int64
	if err := fixture.db.Model(&plugindomain.ReceiveBatch{}).Count(&batches).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&plugindomain.ReceiveIntent{}).Count(&intents).Error; err != nil {
		t.Fatal(err)
	}
	if batches != 0 || intents != 0 {
		t.Fatalf("durable candidate state = batches %d, intents %d; want zero", batches, intents)
	}
}

func TestPrepareAvailableMoveStagesEveryRevisionInOneBatch(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	oldCommit := objectID("a")
	newCommit := objectID("b")
	fixture.addAvailableVersion(t, "v1.0.0", oldCommit, true)
	fixture.addRevisionSelection(t, "v1.0.0", true)
	fixture.addRevisionSelection(t, "v1.0.0", false)
	fixture.builder.sourceCommitByTag = map[string]string{"v1.0.0": newCommit}

	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
		OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: oldCommit, NewCommitObjectID: newCommit,
	}})
	if prepared.ID == "" || len(prepared.Transitions) != 1 {
		t.Fatalf("Prepare() = %#v, want one durable transition", prepared)
	}
	if prepared.Transitions[0].ExpectedOldObjectID != objectID("1") || prepared.Transitions[0].ProposedNewObjectID != objectID("2") {
		t.Fatalf("raw CAS facts = %#v", prepared.Transitions[0])
	}

	var batch plugindomain.ReceiveBatch
	if err := fixture.db.First(&batch, "id = ?", prepared.ID).Error; err != nil {
		t.Fatal(err)
	}
	if batch.State != plugindomain.ReceiveBatchStatePrepared {
		t.Fatalf("batch state = %q, want prepared", batch.State)
	}
	var intent plugindomain.ReceiveIntent
	if err := fixture.db.First(&intent, "batch_id = ?", prepared.ID).Error; err != nil {
		t.Fatal(err)
	}
	if intent.ExpectedOldObjectID != objectID("1") || intent.ExpectedOldCommitSHA != oldCommit || intent.ProposedNewObjectID == nil || *intent.ProposedNewObjectID != objectID("2") || intent.ProposedNewCommitSHA == nil || *intent.ProposedNewCommitSHA != newCommit {
		t.Fatalf("persisted intent facts = %#v", intent)
	}
	var artifacts, transitions, unavailable int64
	fixture.count(t, &plugindomain.ProjectionArtifact{}, "state = ?", []any{plugindomain.ArtifactStateStaged}, &artifacts)
	fixture.count(t, &plugindomain.RevisionProjectionTransition{}, "intent_id = ?", []any{intent.ID}, &transitions)
	fixture.count(t, &plugindomain.RevisionProjectionPointer{}, "plugin_id = ? AND tag = ? AND available = ?", []any{fixture.pluginID, "v1.0.0", false}, &unavailable)
	if artifacts != 2 || transitions != 2 || unavailable != 2 {
		t.Fatalf("prepared projection facts = artifacts %d, transitions %d, unavailable pointers %d; want 2 each", artifacts, transitions, unavailable)
	}
	if len(fixture.builder.builds) != 2 {
		t.Fatalf("BuildPluginProjection calls = %d, want 2", len(fixture.builder.builds))
	}
}

func TestPrepareMultiTagBatchPersistsOnlyAvailableVersions(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
	fixture.addAvailableVersion(t, "v1.1.0", objectID("b"), false)

	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{
		{Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove, OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("a"), NewCommitObjectID: objectID("c")},
		{Tag: "v1.1.0", RefName: "refs/tags/v1.1.0", Operation: gitservice.ReceiveTagDelete, OldObjectID: objectID("3"), NewObjectID: zeroObjectID(), OldCommitObjectID: objectID("b")},
		{Tag: "v2.0.0", RefName: "refs/tags/v2.0.0", Operation: gitservice.ReceiveTagCreate, OldObjectID: zeroObjectID(), NewObjectID: objectID("4"), NewCommitObjectID: objectID("d")},
	})
	if len(prepared.Transitions) != 2 {
		t.Fatalf("prepared transitions = %d, want 2", len(prepared.Transitions))
	}
	var batches, intents int64
	fixture.count(t, &plugindomain.ReceiveBatch{}, "", nil, &batches)
	fixture.count(t, &plugindomain.ReceiveIntent{}, "batch_id = ?", []any{prepared.ID}, &intents)
	if batches != 1 || intents != 2 {
		t.Fatalf("durable rows = batches %d, intents %d; want 1 and 2", batches, intents)
	}
}

func TestPrepareDeleteRejectsActiveMarketplaceReference(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
	fixture.addRevisionSelection(t, "v1.0.0", true)

	sessionID := uuid.NewString()
	coordination, err := fixture.coordinator.Open(context.Background(), fixture.pluginID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer coordination.Close()
	_, err = coordination.Prepare(context.Background(), gitservice.ReceiveBatch{
		SessionID: sessionID, Plugin: gitservice.ReceivePlugin{ID: fixture.pluginID, Name: "scanner"},
		CanonicalTags: []gitservice.ReceiveTagCommand{{Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagDelete, OldObjectID: objectID("1"), NewObjectID: zeroObjectID(), OldCommitObjectID: objectID("a")}},
	})
	if err == nil {
		t.Fatal("Prepare() allowed deleting an actively referenced Version")
	}
	var batches int64
	fixture.count(t, &plugindomain.ReceiveBatch{}, "", nil, &batches)
	if batches != 0 {
		t.Fatalf("rejected delete persisted %d batches", batches)
	}
}

func TestOpenRejectsArchivedOrNonReadyPlugin(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*coordinatorFixture) error
	}{
		{name: "archived", mutate: func(f *coordinatorFixture) error {
			from := plugindomain.PluginStatusActive
			return f.db.Model(&plugindomain.Plugin{}).Where("id = ?", f.pluginID).Updates(map[string]any{"status": plugindomain.PluginStatusArchived, "archived_from": from}).Error
		}},
		{name: "read only", mutate: func(f *coordinatorFixture) error {
			return f.db.Model(&plugindomain.Repository{}).Where("id = ?", f.pluginID).Update("status", plugindomain.RepositoryStatusReadOnly).Error
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoordinatorFixture(t)
			if err := test.mutate(&fixture); err != nil {
				t.Fatal(err)
			}
			if coordination, err := fixture.coordinator.Open(context.Background(), fixture.pluginID, uuid.NewString()); err == nil || coordination != nil {
				t.Fatalf("Open() = %#v, %v; want rejection", coordination, err)
			}
		})
	}
}

func (fixture coordinatorFixture) prepare(t *testing.T, tags []gitservice.ReceiveTagCommand) gitservice.PreparedReceiveBatch {
	t.Helper()
	tags = withManifestFacts(tags)
	sessionID := uuid.NewString()
	coordination, err := fixture.coordinator.Open(context.Background(), fixture.pluginID, sessionID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer coordination.Close()
	prepared, err := coordination.Prepare(context.Background(), gitservice.ReceiveBatch{SessionID: sessionID, Plugin: gitservice.ReceivePlugin{ID: fixture.pluginID, Name: "scanner"}, CanonicalTags: tags})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return prepared
}

func (fixture coordinatorFixture) addAvailableVersion(t *testing.T, tag, commit string, makeDefault bool) {
	t.Helper()
	now := time.Now().UTC()
	digest := strings.Repeat("f", 64)
	version := &plugindomain.PluginVersion{ID: uuid.NewString(), PluginID: fixture.pluginID, Tag: tag, Status: plugindomain.VersionStatusAvailable, CommitSHA: &commit, ManifestDigest: &digest, ManifestSnapshot: []byte(`{"name":"scanner"}`), PublishedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := fixture.db.Create(version).Error; err != nil {
		t.Fatal(err)
	}
	if makeDefault {
		if err := fixture.db.Model(&plugindomain.Plugin{}).Where("id = ?", fixture.pluginID).Update("default_version_tag", tag).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func (fixture coordinatorFixture) addRevisionSelection(t *testing.T, tag string, active bool) string {
	t.Helper()
	now := time.Now().UTC()
	templateID := uuid.NewString()
	revisionID := uuid.NewString()
	distributionID := uuid.NewString()
	status := distributiondomain.StatusRevoked
	if active {
		status = distributiondomain.StatusActive
	}
	for _, record := range []any{
		&distributiondomain.MarketplaceTemplate{ID: templateID, NamespaceID: fixture.namespaceID, Slug: "market-" + uuid.NewString(), Name: "Market", Visibility: plugindomain.VisibilityPublic, Status: status, CreatedAt: now, UpdatedAt: now},
		&distributiondomain.MarketplaceRevision{ID: revisionID, TemplateID: templateID, Revision: 1, ContentJSON: []byte(`{}`), ContentDigest: strings.Repeat("a", 64), Status: status, PublishedAt: now, CreatedAt: now},
		&distributiondomain.PluginDistribution{ID: distributionID, TemplateID: templateID, PluginID: fixture.pluginID, PluginTag: tag, RepositoryID: fixture.pluginID, TagName: tag, SourceTagType: string(gitservice.SourceTagLightweight), SourceCommitSHA: objectID("a"), SourceTreeSHA: objectID("b"), DistributionSHA: objectID("c"), StorageKey: "legacy-" + uuid.NewString(), ContentDigest: strings.Repeat("d", 64), Status: distributiondomain.StatusActive, CreatedAt: now, UpdatedAt: now},
		&distributiondomain.MarketplaceRevisionItem{ID: uuid.NewString(), RevisionID: revisionID, PluginID: fixture.pluginID, PluginTag: tag, PluginDistributionID: distributionID, SourceURL: "https://example.invalid/plugin.git", DistributionSHA: objectID("c"), Position: 0, CreatedAt: now},
	} {
		if err := fixture.db.Create(record).Error; err != nil {
			t.Fatalf("create revision fixture %T: %v", record, err)
		}
	}
	if active {
		if err := fixture.db.Model(&distributiondomain.MarketplaceTemplate{}).Where("id = ?", templateID).Update("published_revision_id", revisionID).Error; err != nil {
			t.Fatal(err)
		}
	}
	oldArtifactID := uuid.NewString()
	oldStorageKey := "old-" + oldArtifactID
	revisionIDCopy := revisionID
	if err := fixture.db.Create(&plugindomain.ProjectionArtifact{ID: oldArtifactID, Kind: plugindomain.ArtifactKindPlugin, PluginID: fixture.pluginID, Tag: tag, SourceObjectID: objectID("1"), SourceCommitSHA: objectID("a"), SourceTreeSHA: objectID("b"), RevisionID: &revisionIDCopy, ContentDigest: strings.Repeat("d", 64), DistributionSHA: objectID("c"), StorageKey: oldStorageKey, State: plugindomain.ArtifactStateReady, CreatedAt: now, UpdatedAt: now, ReadyAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&plugindomain.RevisionProjectionPointer{ID: uuid.NewString(), RevisionID: revisionID, PluginID: fixture.pluginID, Tag: tag, ArtifactID: &oldArtifactID, Generation: 1, Available: true, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return revisionID
}

func (fixture coordinatorFixture) count(t *testing.T, model any, where string, args []any, result *int64) {
	t.Helper()
	query := fixture.db.Model(model)
	if where != "" {
		query = query.Where(where, args...)
	}
	if err := query.Count(result).Error; err != nil {
		t.Fatal(err)
	}
}

type coordinatorFixture struct {
	db          *gorm.DB
	coordinator *Coordinator
	builder     *fakeProjectionBuilder
	namespaceID string
	pluginID    string
}

func newCoordinatorFixture(t *testing.T) coordinatorFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.Namespace{}); err != nil {
		t.Fatalf("migrate identity fixture: %v", err)
	}
	if err := plugindomain.Migrate(db); err != nil {
		t.Fatalf("migrate Plugin fixture: %v", err)
	}
	if err := distributiondomain.Migrate(db); err != nil {
		t.Fatalf("migrate distribution fixture: %v", err)
	}
	now := time.Now().UTC()
	namespaceID := uuid.NewString()
	pluginID := uuid.NewString()
	for _, record := range []any{
		&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security", CreatedAt: now, UpdatedAt: now},
		&plugindomain.Plugin{ID: pluginID, NamespaceID: namespaceID, Slug: "scanner", Visibility: plugindomain.VisibilityPublic, Status: plugindomain.PluginStatusActive, CreatedAt: now, UpdatedAt: now},
		&plugindomain.Repository{ID: pluginID, StorageKey: uuid.NewString(), Status: plugindomain.RepositoryStatusReady, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatalf("create fixture %T: %v", record, err)
		}
	}
	builder := &fakeProjectionBuilder{}
	coordinator, err := NewCoordinator(db, builder, Options{})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	return coordinatorFixture{db: db, coordinator: coordinator, builder: builder, namespaceID: namespaceID, pluginID: pluginID}
}

type fakeProjectionBuilder struct {
	mu                sync.Mutex
	sourceCommitByTag map[string]string
	builds            []gitservice.BuildPluginProjectionCommand
	removed           []gitservice.ImmutableProjection
	verifyError       error
	removeError       error
}

func (builder *fakeProjectionBuilder) BuildPluginProjection(_ context.Context, command gitservice.BuildPluginProjectionCommand) (gitservice.PluginProjectionResult, error) {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	builder.builds = append(builder.builds, command)
	sourceCommit := objectID("b")
	if configured := builder.sourceCommitByTag[command.TagName]; configured != "" {
		sourceCommit = configured
	}
	return gitservice.PluginProjectionResult{
		Projection:        gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindPlugin, StorageKey: command.ProjectionID.String()},
		TagName:           command.TagName,
		SourceTagType:     gitservice.SourceTagAnnotated,
		SourceTagObjectID: objectID("2"),
		SourceCommitSHA:   sourceCommit,
		SourceTreeSHA:     objectID("c"),
		DistributionSHA:   objectID("d"),
		ContentDigest:     strings.Repeat("e", 64),
	}, nil
}

func (*fakeProjectionBuilder) BuildMarketplaceProjection(context.Context, gitservice.BuildMarketplaceProjectionCommand) (gitservice.MarketplaceProjectionResult, error) {
	panic("unexpected BuildMarketplaceProjection call")
}

func (builder *fakeProjectionBuilder) RemoveProjection(_ context.Context, projection gitservice.ImmutableProjection) error {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	builder.removed = append(builder.removed, projection)
	return builder.removeError
}

func (builder *fakeProjectionBuilder) VerifyProjection(context.Context, gitservice.ImmutableProjection, string) error {
	return builder.verifyError
}

func withManifestFacts(commands []gitservice.ReceiveTagCommand) []gitservice.ReceiveTagCommand {
	for index := range commands {
		if commands[index].Operation != gitservice.ReceiveTagMove {
			continue
		}
		if commands[index].NewManifestDigest == "" {
			commands[index].NewManifestDigest = strings.Repeat("9", 64)
		}
		if len(commands[index].NewManifestSnapshot) == 0 {
			commands[index].NewManifestSnapshot = []byte(`{"name":"scanner"}`)
		}
	}
	return commands
}

func objectID(character string) string { return strings.Repeat(character, 40) }
func zeroObjectID() string             { return strings.Repeat("0", 40) }
