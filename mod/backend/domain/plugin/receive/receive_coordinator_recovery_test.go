package receive

import (
	"context"
	"errors"
	"testing"
	"time"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func TestPrepareRejectsAvailableTransitionWithStaleCommitFacts(t *testing.T) {
	for _, test := range []struct {
		name    string
		command gitservice.ReceiveTagCommand
	}{
		{
			name: "move",
			command: gitservice.ReceiveTagCommand{
				Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
				OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("f"), NewCommitObjectID: objectID("b"),
			},
		},
		{
			name: "delete",
			command: gitservice.ReceiveTagCommand{
				Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagDelete,
				OldObjectID: objectID("1"), NewObjectID: zeroObjectID(), OldCommitObjectID: objectID("f"),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoordinatorFixture(t)
			fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)

			if _, err := fixture.prepareResult([]gitservice.ReceiveTagCommand{test.command}); err == nil {
				t.Fatal("Prepare() accepted stale available-Version commit facts")
			}
			assertReceiveRowCounts(t, fixture, 0, 0, 0, 0)
		})
	}
}

func TestPrepareRejectsUnresolvedTransitionForSameCanonicalTag(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
	fixture.addReceiveIntent(t, "v1.0.0", plugindomain.ReceiveBatchStatePrepared)

	_, err := fixture.prepareResult([]gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
		OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("a"), NewCommitObjectID: objectID("b"),
	}})
	if err == nil {
		t.Fatal("Prepare() accepted a second transition for an unresolved canonical tag")
	}
	assertReceiveRowCounts(t, fixture, 1, 1, 0, 0)
}

func TestPrepareBuildFailureLeavesNoDurableOrFailClosedState(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
	fixture.addRevisionSelection(t, "v1.0.0", false)
	coordinator, err := NewCoordinator(fixture.db, failingProjectionBuilder{ProjectionBuilder: fixture.builder}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.coordinator = coordinator

	_, err = fixture.prepareResult([]gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
		OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("a"), NewCommitObjectID: objectID("b"),
	}})
	if err == nil {
		t.Fatal("Prepare() accepted a move whose projection build failed")
	}
	var batches, intents, staged, transitions int64
	fixture.count(t, &plugindomain.ReceiveBatch{}, "", nil, &batches)
	fixture.count(t, &plugindomain.ReceiveIntent{}, "", nil, &intents)
	fixture.count(t, &plugindomain.ProjectionArtifact{}, "state = ?", []any{plugindomain.ArtifactStateStaged}, &staged)
	fixture.count(t, &plugindomain.RevisionProjectionTransition{}, "", nil, &transitions)
	if batches != 0 || intents != 0 || staged != 0 || transitions != 0 {
		t.Fatalf("failed prepare facts = batches %d, intents %d, staged artifacts %d, transitions %d; want zero", batches, intents, staged, transitions)
	}

	var unavailable int64
	fixture.count(t, &plugindomain.RevisionProjectionPointer{}, "plugin_id = ? AND tag = ? AND available = ?", []any{fixture.pluginID, "v1.0.0", false}, &unavailable)
	if unavailable != 0 {
		t.Fatalf("failed prepare left %d fail-closed pointers", unavailable)
	}
}

func TestResolveCompletedMoveIsIdempotentAndCASBound(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	oldCommit := objectID("a")
	newCommit := objectID("b")
	fixture.addAvailableVersion(t, "v1.0.0", oldCommit, false)
	revisionID := fixture.addRevisionSelection(t, "v1.0.0", false)
	fixture.builder.sourceCommitByTag = map[string]string{"v1.0.0": newCommit}

	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
		OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: oldCommit, NewCommitObjectID: newCommit,
	}})
	resolution := gitservice.ReceiveResolution{
		Disposition: gitservice.ReceiveCompleted,
		Observed:    []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: objectID("2"), Exists: true}},
	}
	fixture.resolve(t, prepared, resolution)
	fixture.resolve(t, prepared, resolution)

	assertReceiveState(t, fixture, prepared.ID, plugindomain.ReceiveBatchStateCompleted)
	version := fixture.loadVersion(t, "v1.0.0")
	if version.CommitSHA == nil || *version.CommitSHA != newCommit || version.Status != plugindomain.VersionStatusAvailable {
		t.Fatalf("completed Version = %#v", version)
	}
	pointer := fixture.loadPointer(t, revisionID, "v1.0.0")
	if !pointer.Available || pointer.Generation != 2 || pointer.ArtifactID == nil {
		t.Fatalf("completed pointer = %#v", pointer)
	}
	var ready, completed, history int64
	fixture.count(t, &plugindomain.ProjectionArtifact{}, "plugin_id = ? AND tag = ? AND state = ?", []any{fixture.pluginID, "v1.0.0", plugindomain.ArtifactStateReady}, &ready)
	fixture.count(t, &plugindomain.RevisionProjectionTransition{}, "state = ?", []any{plugindomain.ProjectionTransitionStateCompleted}, &completed)
	fixture.count(t, &plugindomain.PluginVersionHistory{}, "intent_id IS NOT NULL AND operation = ?", []any{plugindomain.VersionHistoryOperationMove}, &history)
	if ready != 2 || completed != 1 || history != 1 {
		t.Fatalf("idempotent completion facts = ready %d, transitions %d, history %d; want 2, 1, 1", ready, completed, history)
	}
}

func TestResolveAbortedMoveRestoresStablePointersIdempotently(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	oldCommit := objectID("a")
	fixture.addAvailableVersion(t, "v1.0.0", oldCommit, false)
	revisionID := fixture.addRevisionSelection(t, "v1.0.0", false)

	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
		OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: oldCommit, NewCommitObjectID: objectID("b"),
	}})
	resolution := gitservice.ReceiveResolution{
		Disposition: gitservice.ReceiveAborted,
		Observed:    []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: objectID("1"), Exists: true}},
	}
	fixture.resolve(t, prepared, resolution)
	fixture.resolve(t, prepared, resolution)

	assertReceiveState(t, fixture, prepared.ID, plugindomain.ReceiveBatchStateAborted)
	version := fixture.loadVersion(t, "v1.0.0")
	if version.CommitSHA == nil || *version.CommitSHA != oldCommit || version.Status != plugindomain.VersionStatusAvailable {
		t.Fatalf("aborted Version = %#v", version)
	}
	pointer := fixture.loadPointer(t, revisionID, "v1.0.0")
	if !pointer.Available || pointer.Generation != 1 || pointer.ArtifactID == nil {
		t.Fatalf("aborted pointer = %#v", pointer)
	}
	var aborted, staged, gcJobs int64
	fixture.count(t, &plugindomain.RevisionProjectionTransition{}, "state = ?", []any{plugindomain.ProjectionTransitionStateAborted}, &aborted)
	fixture.count(t, &plugindomain.ProjectionArtifact{}, "state = ?", []any{plugindomain.ArtifactStateStaged}, &staged)
	fixture.count(t, &plugindomain.ProjectionGCJob{}, "state = ?", []any{plugindomain.ProjectionGCStatePending}, &gcJobs)
	if aborted != 1 || staged != 1 || gcJobs != 1 {
		t.Fatalf("abort facts = transitions %d, staged artifacts %d, GC jobs %d; want 1 each", aborted, staged, gcJobs)
	}
}

func TestResolveUnexpectedOrCorruptMoveRequiresManualRecovery(t *testing.T) {
	for _, test := range []struct {
		name       string
		corrupt    bool
		resolution gitservice.ReceiveResolution
	}{
		{
			name: "unexpected ref",
			resolution: gitservice.ReceiveResolution{
				Disposition: gitservice.ReceiveManualRequired,
				Observed:    []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: objectID("f"), Exists: true}},
			},
		},
		{
			name:    "artifact verification failure",
			corrupt: true,
			resolution: gitservice.ReceiveResolution{
				Disposition: gitservice.ReceiveCompleted,
				Observed:    []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: objectID("2"), Exists: true}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCoordinatorFixture(t)
			fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
			revisionID := fixture.addRevisionSelection(t, "v1.0.0", false)
			prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{{
				Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
				OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("a"), NewCommitObjectID: objectID("b"),
			}})
			if test.corrupt {
				fixture.builder.verifyError = errors.New("corrupt")
			}

			if err := fixture.resolveResult(prepared, test.resolution); test.corrupt && err == nil {
				t.Fatal("Resolve() reported success for corrupt staged projection")
			}
			assertReceiveState(t, fixture, prepared.ID, plugindomain.ReceiveBatchStateManualRequired)
			pointer := fixture.loadPointer(t, revisionID, "v1.0.0")
			if pointer.Available {
				t.Fatalf("manual-required pointer remained available: %#v", pointer)
			}
			version := fixture.loadVersion(t, "v1.0.0")
			if version.CommitSHA == nil || *version.CommitSHA != objectID("a") {
				t.Fatalf("manual-required resolution mutated Version: %#v", version)
			}
		})
	}
}

func TestResolveMixedMultiTagOutcomeRequiresManualRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
	fixture.addAvailableVersion(t, "v1.1.0", objectID("b"), false)

	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{
		{Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove, OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("a"), NewCommitObjectID: objectID("c")},
		{Tag: "v1.1.0", RefName: "refs/tags/v1.1.0", Operation: gitservice.ReceiveTagDelete, OldObjectID: objectID("3"), NewObjectID: zeroObjectID(), OldCommitObjectID: objectID("b")},
	})
	if err := fixture.resolveResult(prepared, gitservice.ReceiveResolution{
		Disposition: gitservice.ReceiveManualRequired,
		Observed: []gitservice.ObservedReceiveRef{
			{RefName: "refs/tags/v1.0.0", ObjectID: objectID("2"), Exists: true},
			{RefName: "refs/tags/v1.1.0", ObjectID: objectID("3"), Exists: true},
		},
	}); err != nil {
		t.Fatalf("Resolve() mixed outcome error = %v", err)
	}
	assertReceiveState(t, fixture, prepared.ID, plugindomain.ReceiveBatchStateManualRequired)
	if version := fixture.loadVersion(t, "v1.0.0"); version.CommitSHA == nil || *version.CommitSHA != objectID("a") {
		t.Fatalf("mixed outcome changed moved Version: %#v", version)
	}
	if version := fixture.loadVersion(t, "v1.1.0"); version.Status != plugindomain.VersionStatusAvailable {
		t.Fatalf("mixed outcome changed deleted Version: %#v", version)
	}
}

func TestResolvePointerCASMismatchRequiresManualRecovery(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
	revisionID := fixture.addRevisionSelection(t, "v1.0.0", false)
	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
		OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("a"), NewCommitObjectID: objectID("b"),
	}})
	if err := fixture.db.Model(&plugindomain.RevisionProjectionPointer{}).
		Where("revision_id = ? AND plugin_id = ?", revisionID, fixture.pluginID).
		Update("generation", 9).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.resolveResult(prepared, gitservice.ReceiveResolution{
		Disposition: gitservice.ReceiveCompleted,
		Observed:    []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: objectID("2"), Exists: true}},
	}); !errors.Is(err, ErrManualRequired) {
		t.Fatalf("Resolve() CAS mismatch error = %v, want manual-required", err)
	}
	assertReceiveState(t, fixture, prepared.ID, plugindomain.ReceiveBatchStateManualRequired)
	pointer := fixture.loadPointer(t, revisionID, "v1.0.0")
	if pointer.Available {
		t.Fatalf("CAS mismatch pointer is available: %#v", pointer)
	}
	if version := fixture.loadVersion(t, "v1.0.0"); version.CommitSHA == nil || *version.CommitSHA != objectID("a") {
		t.Fatalf("CAS mismatch changed Version: %#v", version)
	}
}

func TestReconcileUsesObservedRefsWithoutMutatingGit(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.addAvailableVersion(t, "v1.0.0", objectID("a"), false)
	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagMove,
		OldObjectID: objectID("1"), NewObjectID: objectID("2"), OldCommitObjectID: objectID("a"), NewCommitObjectID: objectID("b"),
	}})
	if err := fixture.coordinator.Reconcile(context.Background(), prepared, []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: objectID("2"), Exists: true}}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	assertReceiveState(t, fixture, prepared.ID, plugindomain.ReceiveBatchStateCompleted)
	if version := fixture.loadVersion(t, "v1.0.0"); version.CommitSHA == nil || *version.CommitSHA != objectID("b") {
		t.Fatalf("reconciled Version = %#v", version)
	}
}

func TestResolveDeleteCompletedClearsDefaultAndIsolatesHistoricalPointer(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	oldCommit := objectID("a")
	fixture.addAvailableVersion(t, "v1.0.0", oldCommit, true)
	revisionID := fixture.addRevisionSelection(t, "v1.0.0", false)

	prepared := fixture.prepare(t, []gitservice.ReceiveTagCommand{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", Operation: gitservice.ReceiveTagDelete,
		OldObjectID: objectID("1"), NewObjectID: zeroObjectID(), OldCommitObjectID: oldCommit,
	}})
	resolution := gitservice.ReceiveResolution{
		Disposition: gitservice.ReceiveCompleted,
		Observed:    []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", Exists: false}},
	}
	fixture.resolve(t, prepared, resolution)
	fixture.resolve(t, prepared, resolution)

	version := fixture.loadVersion(t, "v1.0.0")
	if version.Status != plugindomain.VersionStatusDeleted || version.CommitSHA != nil || version.ManifestDigest != nil || len(version.ManifestSnapshot) != 0 || version.DeletedAt == nil {
		t.Fatalf("deleted Version = %#v", version)
	}
	var plugin plugindomain.Plugin
	if err := fixture.db.First(&plugin, "id = ?", fixture.pluginID).Error; err != nil {
		t.Fatal(err)
	}
	if plugin.DefaultVersionTag != nil {
		t.Fatalf("default Version = %q, want cleared", *plugin.DefaultVersionTag)
	}
	pointer := fixture.loadPointer(t, revisionID, "v1.0.0")
	if pointer.Available || pointer.ArtifactID != nil || pointer.Generation != 2 {
		t.Fatalf("deleted pointer = %#v", pointer)
	}
	var history, gcJobs int64
	fixture.count(t, &plugindomain.PluginVersionHistory{}, "operation = ?", []any{plugindomain.VersionHistoryOperationDelete}, &history)
	fixture.count(t, &plugindomain.ProjectionGCJob{}, "state = ?", []any{plugindomain.ProjectionGCStatePending}, &gcJobs)
	if history != 1 || gcJobs != 1 {
		t.Fatalf("delete facts = history %d, GC jobs %d; want 1 each", history, gcJobs)
	}
}

type failingProjectionBuilder struct {
	gitservice.ProjectionBuilder
}

func (failingProjectionBuilder) BuildPluginProjection(context.Context, gitservice.BuildPluginProjectionCommand) (gitservice.PluginProjectionResult, error) {
	return gitservice.PluginProjectionResult{}, errors.New("build failed")
}

func (fixture coordinatorFixture) prepareResult(tags []gitservice.ReceiveTagCommand) (gitservice.PreparedReceiveBatch, error) {
	tags = withManifestFacts(tags)
	sessionID := uuid.NewString()
	coordination, err := fixture.coordinator.Open(context.Background(), fixture.pluginID, sessionID)
	if err != nil {
		return gitservice.PreparedReceiveBatch{}, err
	}
	defer coordination.Close()
	return coordination.Prepare(context.Background(), gitservice.ReceiveBatch{
		SessionID:     sessionID,
		Plugin:        gitservice.ReceivePlugin{ID: fixture.pluginID, Name: "scanner"},
		CanonicalTags: tags,
	})
}

func (fixture coordinatorFixture) resolve(t *testing.T, prepared gitservice.PreparedReceiveBatch, resolution gitservice.ReceiveResolution) {
	t.Helper()
	if err := fixture.resolveResult(prepared, resolution); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func (fixture coordinatorFixture) resolveResult(prepared gitservice.PreparedReceiveBatch, resolution gitservice.ReceiveResolution) error {
	var batch plugindomain.ReceiveBatch
	if err := fixture.db.First(&batch, "id = ?", prepared.ID).Error; err != nil {
		return err
	}
	coordination, err := fixture.coordinator.Open(context.Background(), fixture.pluginID, batch.CorrelationID)
	if err != nil {
		return err
	}
	defer coordination.Close()
	return coordination.Resolve(context.Background(), prepared, resolution)
}

func (fixture coordinatorFixture) addReceiveIntent(t *testing.T, tag, state string) {
	t.Helper()
	now := time.Now().UTC()
	batchID := uuid.NewString()
	if err := fixture.db.Create(&plugindomain.ReceiveBatch{
		ID: batchID, PluginID: fixture.pluginID, State: state, CorrelationID: uuid.NewString(), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	proposedObjectID := objectID("2")
	proposedCommit := objectID("b")
	if err := fixture.db.Create(&plugindomain.ReceiveIntent{
		ID: uuid.NewString(), BatchID: batchID, PluginID: fixture.pluginID, Operation: plugindomain.ReceiveOperationMove, Tag: tag,
		ExpectedOldObjectID: objectID("1"), ProposedNewObjectID: &proposedObjectID,
		ExpectedOldCommitSHA: objectID("a"), ProposedNewCommitSHA: &proposedCommit,
		ExpectedVersionStatus: plugindomain.VersionStatusAvailable, State: state, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func (fixture coordinatorFixture) loadVersion(t *testing.T, tag string) plugindomain.PluginVersion {
	t.Helper()
	var version plugindomain.PluginVersion
	if err := fixture.db.First(&version, "plugin_id = ? AND tag = ?", fixture.pluginID, tag).Error; err != nil {
		t.Fatal(err)
	}
	return version
}

func (fixture coordinatorFixture) loadPointer(t *testing.T, revisionID, tag string) plugindomain.RevisionProjectionPointer {
	t.Helper()
	var pointer plugindomain.RevisionProjectionPointer
	if err := fixture.db.First(&pointer, "revision_id = ? AND plugin_id = ? AND tag = ?", revisionID, fixture.pluginID, tag).Error; err != nil {
		t.Fatal(err)
	}
	return pointer
}

func assertReceiveState(t *testing.T, fixture coordinatorFixture, batchID, want string) {
	t.Helper()
	var batch plugindomain.ReceiveBatch
	if err := fixture.db.First(&batch, "id = ?", batchID).Error; err != nil {
		t.Fatal(err)
	}
	if batch.State != want {
		t.Fatalf("batch state = %q, want %q", batch.State, want)
	}
	var wrong int64
	fixture.count(t, &plugindomain.ReceiveIntent{}, "batch_id = ? AND state <> ?", []any{batchID, want}, &wrong)
	if wrong != 0 {
		t.Fatalf("%d intents do not have state %q", wrong, want)
	}
}

func assertReceiveRowCounts(t *testing.T, fixture coordinatorFixture, batches, intents, artifacts, transitions int64) {
	t.Helper()
	for _, assertion := range []struct {
		model any
		want  int64
	}{
		{model: &plugindomain.ReceiveBatch{}, want: batches},
		{model: &plugindomain.ReceiveIntent{}, want: intents},
		{model: &plugindomain.ProjectionArtifact{}, want: artifacts},
		{model: &plugindomain.RevisionProjectionTransition{}, want: transitions},
	} {
		var got int64
		fixture.count(t, assertion.model, "", nil, &got)
		if got != assertion.want {
			t.Fatalf("%T rows = %d, want %d", assertion.model, got, assertion.want)
		}
	}
}
