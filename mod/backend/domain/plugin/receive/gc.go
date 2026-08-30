package receive

import (
	"context"
	"errors"
	"time"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"gorm.io/gorm"
)

type GCRunner struct {
	db      *gorm.DB
	builder gitservice.ProjectionBuilder
	now     func() time.Time
}

func NewGCRunner(db *gorm.DB, builder gitservice.ProjectionBuilder, options Options) (*GCRunner, error) {
	if db == nil {
		return nil, errors.New("projection GC requires database")
	}
	if builder == nil {
		return nil, errors.New("projection GC requires projection builder")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &GCRunner{db: db, builder: builder, now: options.Now}, nil
}

func (runner *GCRunner) RunOnce(ctx context.Context) (bool, error) {
	if runner == nil {
		return false, errors.New("projection GC is not configured")
	}
	var job plugindomain.ProjectionGCJob
	err := runner.db.WithContext(ctx).
		Where("state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", plugindomain.ProjectionGCStatePending, runner.now().UTC()).
		Order("created_at, id").Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("load projection GC job")
	}
	now := runner.now().UTC()
	claim := runner.db.WithContext(ctx).Model(&plugindomain.ProjectionGCJob{}).
		Where("id = ? AND state = ?", job.ID, plugindomain.ProjectionGCStatePending).
		Updates(map[string]any{"state": plugindomain.ProjectionGCStateRunning, "attempts": gorm.Expr("attempts + 1"), "updated_at": now})
	if claim.Error != nil {
		return false, errors.New("claim projection GC job")
	}
	if claim.RowsAffected != 1 {
		return false, nil
	}
	var artifact plugindomain.ProjectionArtifact
	if err := runner.db.WithContext(ctx).First(&artifact, "id = ?", job.ArtifactID).Error; err != nil {
		_ = runner.failJob(ctx, job.ID, "artifact_missing")
		return true, errors.New("projection GC artifact unavailable")
	}
	projection := gitservice.ImmutableProjection{Kind: projectionKind(artifact.Kind), StorageKey: artifact.StorageKey}
	if projection.Kind == "" || projection.StorageKey == "" {
		_ = runner.failJob(ctx, job.ID, "artifact_invalid")
		return true, errors.New("projection GC artifact unavailable")
	}
	if err := runner.builder.RemoveProjection(ctx, projection); err != nil {
		_ = runner.failJob(ctx, job.ID, "remove_failed")
		return true, errors.New("remove projection")
	}
	completedAt := runner.now().UTC()
	result := runner.db.WithContext(ctx).Model(&plugindomain.ProjectionGCJob{}).
		Where("id = ? AND state = ?", job.ID, plugindomain.ProjectionGCStateRunning).
		Updates(map[string]any{"state": plugindomain.ProjectionGCStateCompleted, "safe_error": "", "updated_at": completedAt, "completed_at": completedAt})
	if result.Error != nil || result.RowsAffected != 1 {
		return true, errors.New("complete projection GC job")
	}
	return true, nil
}

func (runner *GCRunner) failJob(ctx context.Context, jobID, safeError string) error {
	now := runner.now().UTC()
	return runner.db.WithContext(ctx).Model(&plugindomain.ProjectionGCJob{}).
		Where("id = ? AND state = ?", jobID, plugindomain.ProjectionGCStateRunning).
		Updates(map[string]any{
			"state": plugindomain.ProjectionGCStatePending, "safe_error": safeError,
			"next_attempt_at": now.Add(time.Minute), "updated_at": now,
		}).Error
}

func projectionKind(kind string) gitservice.ProjectionKind {
	switch kind {
	case plugindomain.ArtifactKindPlugin:
		return gitservice.ProjectionKindPlugin
	case plugindomain.ArtifactKindMarketplace:
		return gitservice.ProjectionKindMarketplace
	default:
		return ""
	}
}
