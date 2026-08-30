package receive

import (
	"context"
	"errors"
	"testing"
	"time"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/google/uuid"
)

func TestGCRunnerRemovesPendingProjectionOnce(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	now := time.Now().UTC()
	artifactID := uuid.NewString()
	storageKey := uuid.NewString()
	if err := fixture.db.Create(&plugindomain.ProjectionArtifact{
		ID: artifactID, Kind: plugindomain.ArtifactKindPlugin, PluginID: fixture.pluginID, Tag: "v1.0.0",
		SourceObjectID: objectID("1"), SourceCommitSHA: objectID("a"), SourceTreeSHA: objectID("b"),
		ContentDigest: strings64("c"), DistributionSHA: objectID("d"), StorageKey: storageKey,
		State: plugindomain.ArtifactStateReady, CreatedAt: now, UpdatedAt: now, ReadyAt: &now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewString()
	if err := fixture.db.Create(&plugindomain.ProjectionGCJob{ID: jobID, ArtifactID: artifactID, State: plugindomain.ProjectionGCStatePending, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	runner, err := NewGCRunner(fixture.db, fixture.builder, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if worked, err := runner.RunOnce(context.Background()); err != nil || !worked {
		t.Fatalf("RunOnce() = %v, %v; want work completed", worked, err)
	}
	if worked, err := runner.RunOnce(context.Background()); err != nil || worked {
		t.Fatalf("second RunOnce() = %v, %v; want idle", worked, err)
	}
	if len(fixture.builder.removed) != 1 || fixture.builder.removed[0].StorageKey != storageKey {
		t.Fatalf("removed projections = %#v, want one opaque key", fixture.builder.removed)
	}
	var job plugindomain.ProjectionGCJob
	if err := fixture.db.First(&job, "id = ?", jobID).Error; err != nil {
		t.Fatal(err)
	}
	if job.State != plugindomain.ProjectionGCStateCompleted || job.Attempts != 1 || job.CompletedAt == nil {
		t.Fatalf("completed GC job = %#v", job)
	}
}

func TestGCRunnerRetriesRemoveFailureWithoutLeakingBuilderError(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	now := time.Now().UTC()
	artifactID := uuid.NewString()
	if err := fixture.db.Create(&plugindomain.ProjectionArtifact{
		ID: artifactID, Kind: plugindomain.ArtifactKindPlugin, PluginID: fixture.pluginID, Tag: "v1.0.0",
		SourceObjectID: objectID("1"), SourceCommitSHA: objectID("a"), SourceTreeSHA: objectID("b"),
		ContentDigest: strings64("c"), DistributionSHA: objectID("d"), StorageKey: uuid.NewString(),
		State: plugindomain.ArtifactStateReady, CreatedAt: now, UpdatedAt: now, ReadyAt: &now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewString()
	if err := fixture.db.Create(&plugindomain.ProjectionGCJob{ID: jobID, ArtifactID: artifactID, State: plugindomain.ProjectionGCStatePending, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.builder.removeError = errors.New("/secret/storage/path: denied")
	runner, err := NewGCRunner(fixture.db, fixture.builder, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	worked, runErr := runner.RunOnce(context.Background())
	if !worked || runErr == nil {
		t.Fatalf("RunOnce() = %v, %v; want safe failure", worked, runErr)
	}
	if runErr.Error() != "remove projection" {
		t.Fatalf("RunOnce() error = %q, leaked builder details", runErr)
	}
	var job plugindomain.ProjectionGCJob
	if err := fixture.db.First(&job, "id = ?", jobID).Error; err != nil {
		t.Fatal(err)
	}
	if job.State != plugindomain.ProjectionGCStatePending || job.SafeError != "remove_failed" || job.Attempts != 1 || job.NextAttemptAt == nil {
		t.Fatalf("retryable GC job = %#v", job)
	}
}

func strings64(character string) string {
	var result string
	for range 64 {
		result += character
	}
	return result
}
