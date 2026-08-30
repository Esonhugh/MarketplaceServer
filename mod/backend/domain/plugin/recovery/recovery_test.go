package recovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReconcileReceivesFinalizesAcceptedRefIdempotently(t *testing.T) {
	fixture := newFixture(t)
	fixture.addReceive(t, plugindomain.ReceiveBatchStatePrepared, "v1.0.0", oid("a"), oid("b"))
	fixture.refs.refs["refs/tags/v1.0.0"] = gitservice.ObservedReceiveRef{RefName: "refs/tags/v1.0.0", ObjectID: oid("b"), Exists: true}

	count, err := fixture.reconciler.ReconcileReceives(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("ReconcileReceives() = %d, %v", count, err)
	}
	count, err = fixture.reconciler.ReconcileReceives(t.Context())
	if err != nil || count != 0 {
		t.Fatalf("idempotent ReconcileReceives() = %d, %v", count, err)
	}
	var version plugindomain.PluginVersion
	if err := fixture.db.First(&version, "plugin_id = ? AND tag = ?", fixture.pluginID, "v1.0.0").Error; err != nil {
		t.Fatal(err)
	}
	if version.CommitSHA == nil || *version.CommitSHA != oid("b") {
		t.Fatalf("version after reconciliation = %#v", version)
	}
	var batch plugindomain.ReceiveBatch
	if err := fixture.db.First(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if batch.State != plugindomain.ReceiveBatchStateCompleted {
		t.Fatalf("batch state = %q", batch.State)
	}
}

func TestReconcileReceivesMarksUnexpectedRefManualWithoutWritingGit(t *testing.T) {
	fixture := newFixture(t)
	fixture.addReceive(t, plugindomain.ReceiveBatchStateFinalizing, "v1.0.0", oid("a"), oid("b"))
	fixture.refs.refs["refs/tags/v1.0.0"] = gitservice.ObservedReceiveRef{RefName: "refs/tags/v1.0.0", ObjectID: oid("f"), Exists: true}

	if _, err := fixture.reconciler.ReconcileReceives(t.Context()); err != nil {
		t.Fatal(err)
	}
	var batch plugindomain.ReceiveBatch
	if err := fixture.db.First(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if batch.State != plugindomain.ReceiveBatchStateManualRequired || batch.SafeError != "unexpected_ref" {
		t.Fatalf("batch = %#v", batch)
	}
	if fixture.refs.reads != 1 {
		t.Fatalf("ref reads = %d, want 1", fixture.refs.reads)
	}
	var version plugindomain.PluginVersion
	if err := fixture.db.First(&version, "plugin_id = ? AND tag = ?", fixture.pluginID, "v1.0.0").Error; err != nil {
		t.Fatal(err)
	}
	if version.CommitSHA == nil || *version.CommitSHA != oid("a") {
		t.Fatal("manual recovery changed version")
	}
}

func TestReconcileOrphansRequiresAgeAndAggregateAbsence(t *testing.T) {
	fixture := newFixture(t)
	old := fixture.now.Add(-time.Hour)
	fixture.provisioner.candidates = []gitservice.RepositoryOrphanCandidate{{StorageKey: "orphan", CreatedAt: old}, {StorageKey: "young", CreatedAt: fixture.now}, {StorageKey: fixture.storageKey, CreatedAt: old}}
	count, err := fixture.reconciler.ReconcileOrphans(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("ReconcileOrphans() = %d, %v", count, err)
	}
	if got := strings.Join(fixture.cleaner.removed, ","); got != "orphan" {
		t.Fatalf("removed = %q", got)
	}
}

func TestRunProjectionGCIsIdempotentAndFailureIsManual(t *testing.T) {
	fixture := newFixture(t)
	artifactID := uuid.NewString()
	if err := fixture.db.Create(&plugindomain.ProjectionArtifact{ID: artifactID, Kind: plugindomain.ArtifactKindPlugin, PluginID: fixture.pluginID, Tag: "v1.0.0", SourceObjectID: oid("a"), SourceCommitSHA: oid("a"), ContentDigest: strings.Repeat("d", 64), StorageKey: "artifact", State: plugindomain.ArtifactStateStaged, CreatedAt: fixture.now, UpdatedAt: fixture.now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Create(&plugindomain.ProjectionGCJob{ID: uuid.NewString(), ArtifactID: artifactID, State: plugindomain.ProjectionGCStatePending, CreatedAt: fixture.now, UpdatedAt: fixture.now}).Error; err != nil {
		t.Fatal(err)
	}
	count, err := fixture.reconciler.RunProjectionGC(t.Context())
	if err != nil || count != 1 || fixture.builder.removes != 1 {
		t.Fatalf("RunProjectionGC() = %d, %v; removes %d", count, err, fixture.builder.removes)
	}
	count, err = fixture.reconciler.RunProjectionGC(t.Context())
	if err != nil || count != 0 || fixture.builder.removes != 1 {
		t.Fatalf("idempotent gc = %d, %v; removes %d", count, err, fixture.builder.removes)
	}
}

type fixture struct {
	db                   *gorm.DB
	reconciler           *Reconciler
	refs                 *fakeRefs
	builder              *fakeBuilder
	provisioner          *fakeProvisioner
	cleaner              *fakeCleaner
	pluginID, storageKey string
	now                  time.Time
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.Namespace{}); err != nil {
		t.Fatal(err)
	}
	if err := plugindomain.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	namespaceID, pluginID, storageKey := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, record := range []any{&identitymodel.Namespace{ID: namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "team", DisplayName: "Team", CreatedAt: now, UpdatedAt: now}, &plugindomain.Plugin{ID: pluginID, NamespaceID: namespaceID, Slug: "scanner", Visibility: plugindomain.VisibilityPublic, Status: plugindomain.PluginStatusActive, CreatedAt: now, UpdatedAt: now}, &plugindomain.Repository{ID: pluginID, StorageKey: storageKey, Status: plugindomain.RepositoryStatusReady, CreatedAt: now, UpdatedAt: now}} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	refs, builder, provisioner, cleaner := &fakeRefs{refs: map[string]gitservice.ObservedReceiveRef{}}, &fakeBuilder{}, &fakeProvisioner{}, &fakeCleaner{}
	reconciler, err := New(db, refs, builder, provisioner, cleaner, Options{BatchSize: 2, MinimumOrphanAge: time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{db: db, reconciler: reconciler, refs: refs, builder: builder, provisioner: provisioner, cleaner: cleaner, pluginID: pluginID, storageKey: storageKey, now: now}
}
func (f fixture) addReceive(t *testing.T, state, tag, old, proposed string) {
	t.Helper()
	batchID, intentID := uuid.NewString(), uuid.NewString()
	digest := strings.Repeat("c", 64)
	if err := f.db.Create(&plugindomain.PluginVersion{ID: uuid.NewString(), PluginID: f.pluginID, Tag: tag, Status: plugindomain.VersionStatusAvailable, CommitSHA: &old, ManifestDigest: &digest, ManifestSnapshot: []byte(`{}`), PublishedAt: f.now, CreatedAt: f.now, UpdatedAt: f.now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&plugindomain.ReceiveBatch{ID: batchID, PluginID: f.pluginID, State: state, CorrelationID: "session", CreatedAt: f.now, UpdatedAt: f.now}).Error; err != nil {
		t.Fatal(err)
	}
	proposedDigest := strings.Repeat("d", 64)
	if err := f.db.Create(&plugindomain.ReceiveIntent{ID: intentID, BatchID: batchID, PluginID: f.pluginID, Operation: plugindomain.ReceiveOperationMove, Tag: tag, ExpectedOldObjectID: old, ProposedNewObjectID: &proposed, ExpectedOldCommitSHA: old, ProposedNewCommitSHA: &proposed, ProposedManifestDigest: &proposedDigest, ProposedManifestSnapshot: []byte(`{"name":"scanner"}`), ExpectedVersionStatus: plugindomain.VersionStatusAvailable, State: state, CreatedAt: f.now, UpdatedAt: f.now}).Error; err != nil {
		t.Fatal(err)
	}
}
func oid(c string) string { return strings.Repeat(c, 40) }

type fakeRefs struct {
	refs  map[string]gitservice.ObservedReceiveRef
	reads int
}

func (f *fakeRefs) ReadPluginRef(_ context.Context, _ string, ref string) (gitservice.ObservedReceiveRef, error) {
	f.reads++
	return f.refs[ref], nil
}

type fakeBuilder struct {
	removes   int
	removeErr error
}

func (*fakeBuilder) BuildPluginProjection(context.Context, gitservice.BuildPluginProjectionCommand) (gitservice.PluginProjectionResult, error) {
	return gitservice.PluginProjectionResult{}, errors.New("unexpected")
}
func (*fakeBuilder) BuildMarketplaceProjection(context.Context, gitservice.BuildMarketplaceProjectionCommand) (gitservice.MarketplaceProjectionResult, error) {
	return gitservice.MarketplaceProjectionResult{}, errors.New("unexpected")
}
func (f *fakeBuilder) RemoveProjection(context.Context, gitservice.ImmutableProjection) error {
	f.removes++
	return f.removeErr
}
func (*fakeBuilder) VerifyProjection(context.Context, gitservice.ImmutableProjection, string) error {
	return nil
}

type fakeProvisioner struct {
	candidates []gitservice.RepositoryOrphanCandidate
}

func (*fakeProvisioner) ProvisionRepository(context.Context, gitservice.RepositoryIdentity) (gitservice.ProvisionedRepository, error) {
	return gitservice.ProvisionedRepository{}, errors.New("unexpected")
}
func (*fakeProvisioner) RemoveProvisionedRepository(context.Context, gitservice.ProvisionedRepository) error {
	return errors.New("unexpected")
}
func (f *fakeProvisioner) ListRepositoryOrphanCandidates(context.Context, time.Duration) ([]gitservice.RepositoryOrphanCandidate, error) {
	return f.candidates, nil
}

type fakeCleaner struct{ removed []string }

func (f *fakeCleaner) RemoveOrphanRepository(_ context.Context, storageKey string) error {
	f.removed = append(f.removed, storageKey)
	return nil
}
