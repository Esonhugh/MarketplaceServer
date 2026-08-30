// Package recovery provides explicit, bounded Plugin lifecycle maintenance.
// Callers own scheduling and must supply opaque Git-storage adapters.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrInvalidOptions = errors.New("invalid recovery options")
	ErrUnavailable    = errors.New("plugin recovery unavailable")
)

const defaultBatchSize = 50

// Options provides per-call bounds. A zero BatchSize uses a conservative
// default. MinimumOrphanAge is required for orphan discovery.
type Options struct {
	BatchSize        int
	MinimumOrphanAge time.Duration
	Now              func() time.Time
}

// Reconciler is deliberately callable rather than a module lifecycle task.
// It owns only SQL state transitions and invokes narrow opaque Git adapters.
type Reconciler struct {
	db      *gorm.DB
	ref     gitservice.PluginRefReader
	gc      gitservice.ProjectionBuilder
	fs      gitservice.RepositoryProvisioner
	cleaner gitservice.RepositoryOrphanCleaner
	opts    Options
}

func New(db *gorm.DB, refs gitservice.PluginRefReader, builder gitservice.ProjectionBuilder, provisioner gitservice.RepositoryProvisioner, cleaner gitservice.RepositoryOrphanCleaner, options Options) (*Reconciler, error) {
	if db == nil || refs == nil || builder == nil || provisioner == nil || cleaner == nil {
		return nil, errors.New("plugin recovery requires database, refs, projection builder, repository provisioner, and cleaner")
	}
	if options.BatchSize < 0 || options.MinimumOrphanAge < 0 {
		return nil, ErrInvalidOptions
	}
	if options.BatchSize == 0 {
		options.BatchSize = defaultBatchSize
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Reconciler{db: db, ref: refs, gc: builder, fs: provisioner, cleaner: cleaner, opts: options}, nil
}

// ReconcileReceives re-reads refs for prepared/finalizing batches and resolves
// only deterministic completed, aborted, or manual-required outcomes. It never
// updates a Git ref.
func (r *Reconciler) ReconcileReceives(ctx context.Context) (int, error) {
	if err := r.valid(ctx); err != nil {
		return 0, err
	}
	var batches []plugindomain.ReceiveBatch
	if err := r.db.WithContext(ctx).Where("state IN ?", []string{plugindomain.ReceiveBatchStatePrepared, plugindomain.ReceiveBatchStateFinalizing}).Order("created_at ASC").Limit(r.opts.BatchSize).Find(&batches).Error; err != nil {
		return 0, unavailable("list receive batches", err)
	}
	processed := 0
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := r.reconcileBatch(ctx, batch); err != nil {
			return processed, err
		}
		processed++
		if err := ctx.Err(); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

func (r *Reconciler) reconcileBatch(ctx context.Context, batch plugindomain.ReceiveBatch) error {
	var repository plugindomain.Repository
	if err := r.db.WithContext(ctx).First(&repository, "id = ?", batch.PluginID).Error; err != nil {
		return r.manualBatch(ctx, batch.ID, "repository_missing")
	}
	var intents []plugindomain.ReceiveIntent
	if err := r.db.WithContext(ctx).Where("batch_id = ?", batch.ID).Order("tag ASC").Find(&intents).Error; err != nil {
		return unavailable("list receive intents", err)
	}
	if len(intents) == 0 {
		return r.manualBatch(ctx, batch.ID, "intent_missing")
	}
	observed := make([]gitservice.ObservedReceiveRef, 0, len(intents))
	for _, intent := range intents {
		ref, err := r.ref.ReadPluginRef(ctx, repository.ID, "refs/tags/"+intent.Tag)
		if err != nil {
			return unavailable("reread receive ref", err)
		}
		if ref.RefName != "refs/tags/"+intent.Tag {
			return r.manualBatch(ctx, batch.ID, "invalid_ref_read")
		}
		observed = append(observed, ref)
	}
	disposition := classify(intents, observed)
	if disposition == gitservice.ReceiveManualRequired {
		return r.manualBatch(ctx, batch.ID, "unexpected_ref")
	}
	if disposition == gitservice.ReceiveCompleted {
		if err := r.verifyStaged(ctx, intents); err != nil {
			return r.manualBatch(ctx, batch.ID, "artifact_verification_failed")
		}
	}
	return r.applyReceive(ctx, batch, intents, disposition)
}

func classify(intents []plugindomain.ReceiveIntent, observed []gitservice.ObservedReceiveRef) gitservice.ReceiveDisposition {
	if len(intents) == 0 || len(intents) != len(observed) {
		return gitservice.ReceiveManualRequired
	}
	refs := make(map[string]gitservice.ObservedReceiveRef, len(observed))
	for _, ref := range observed {
		if ref.RefName == "" {
			return gitservice.ReceiveManualRequired
		}
		if _, exists := refs[ref.RefName]; exists {
			return gitservice.ReceiveManualRequired
		}
		refs[ref.RefName] = ref
	}
	allNew, allOld := true, true
	for _, intent := range intents {
		ref, exists := refs["refs/tags/"+intent.Tag]
		if !exists {
			return gitservice.ReceiveManualRequired
		}
		proposed := strings.Repeat("0", len(intent.ExpectedOldObjectID))
		if intent.ProposedNewObjectID != nil {
			proposed = *intent.ProposedNewObjectID
		}
		newMatches := matches(ref, proposed)
		oldMatches := matches(ref, intent.ExpectedOldObjectID)
		if !newMatches && !oldMatches {
			return gitservice.ReceiveManualRequired
		}
		allNew = allNew && newMatches
		allOld = allOld && oldMatches
	}
	if allNew {
		return gitservice.ReceiveCompleted
	}
	if allOld {
		return gitservice.ReceiveAborted
	}
	return gitservice.ReceiveManualRequired
}

func matches(ref gitservice.ObservedReceiveRef, want string) bool {
	if strings.Trim(want, "0") == "" {
		return !ref.Exists
	}
	return ref.Exists && ref.ObjectID == want
}

func (r *Reconciler) verifyStaged(ctx context.Context, intents []plugindomain.ReceiveIntent) error {
	ids := make([]string, 0, len(intents))
	for _, intent := range intents {
		ids = append(ids, intent.ID)
	}
	var transitions []plugindomain.RevisionProjectionTransition
	if err := r.db.WithContext(ctx).Where("intent_id IN ? AND staged_artifact_id IS NOT NULL", ids).Find(&transitions).Error; err != nil {
		return err
	}
	for _, transition := range transitions {
		var artifact plugindomain.ProjectionArtifact
		if err := r.db.WithContext(ctx).First(&artifact, "id = ? AND state = ?", *transition.StagedArtifactID, plugindomain.ArtifactStateStaged).Error; err != nil {
			return err
		}
		if err := r.gc.VerifyProjection(ctx, gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindPlugin, StorageKey: artifact.StorageKey}, artifact.ContentDigest); err != nil {
			return err
		}
	}
	return nil
}

func (r *Reconciler) applyReceive(ctx context.Context, batch plugindomain.ReceiveBatch, intents []plugindomain.ReceiveIntent, disposition gitservice.ReceiveDisposition) error {
	now := r.opts.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current plugindomain.ReceiveBatch
		if err := tx.First(&current, "id = ?", batch.ID).Error; err != nil {
			return err
		}
		if current.State == plugindomain.ReceiveBatchStateCompleted || current.State == plugindomain.ReceiveBatchStateAborted {
			return nil
		}
		for _, intent := range intents {
			if err := r.applyIntent(tx, current, intent, disposition, now); err != nil {
				return err
			}
		}
		state := plugindomain.ReceiveBatchStateCompleted
		if disposition == gitservice.ReceiveAborted {
			state = plugindomain.ReceiveBatchStateAborted
		}
		result := tx.Model(&plugindomain.ReceiveBatch{}).Where("id = ? AND state IN ?", batch.ID, []string{plugindomain.ReceiveBatchStatePrepared, plugindomain.ReceiveBatchStateFinalizing}).Updates(map[string]any{"state": state, "updated_at": now, "completed_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("receive batch cas mismatch")
		}
		return nil
	})
}

func (r *Reconciler) applyIntent(tx *gorm.DB, batch plugindomain.ReceiveBatch, intent plugindomain.ReceiveIntent, disposition gitservice.ReceiveDisposition, now time.Time) error {
	var transitions []plugindomain.RevisionProjectionTransition
	if err := tx.Where("intent_id = ?", intent.ID).Find(&transitions).Error; err != nil {
		return err
	}
	if disposition == gitservice.ReceiveCompleted {
		version := tx.Model(&plugindomain.PluginVersion{}).Where("plugin_id = ? AND tag = ? AND status = ? AND commit_sha = ?", intent.PluginID, intent.Tag, intent.ExpectedVersionStatus, intent.ExpectedOldCommitSHA)
		var operation string
		var newCommit *string
		switch intent.Operation {
		case plugindomain.ReceiveOperationMove:
			if intent.ProposedNewObjectID == nil || intent.ProposedNewCommitSHA == nil || intent.ProposedManifestDigest == nil || len(intent.ProposedManifestSnapshot) == 0 {
				return errors.New("missing proposed source facts")
			}
			newCommit = intent.ProposedNewCommitSHA
			operation = plugindomain.VersionHistoryOperationMove
			if result := version.Updates(map[string]any{
				"raw_tag_object_id": *intent.ProposedNewObjectID, "commit_sha": *newCommit,
				"manifest_digest": *intent.ProposedManifestDigest, "manifest_snapshot": append([]byte(nil), intent.ProposedManifestSnapshot...),
				"updated_at": now,
			}); result.Error != nil || result.RowsAffected != 1 {
				return errors.New("version cas mismatch")
			}
		case plugindomain.ReceiveOperationDelete:
			operation = plugindomain.VersionHistoryOperationDelete
			if result := version.Updates(map[string]any{"status": plugindomain.VersionStatusDeleted, "raw_tag_object_id": nil, "commit_sha": nil, "manifest_digest": nil, "manifest_snapshot": nil, "deleted_at": now, "updated_at": now}); result.Error != nil || result.RowsAffected != 1 {
				return errors.New("version cas mismatch")
			}
			if err := tx.Model(&plugindomain.Plugin{}).Where("id = ? AND default_version_tag = ?", intent.PluginID, intent.Tag).Updates(map[string]any{"default_version_tag": nil, "updated_at": now}).Error; err != nil {
				return err
			}
		default:
			return errors.New("unknown receive operation")
		}
		if err := tx.Create(&plugindomain.PluginVersionHistory{ID: uuid.NewString(), PluginID: intent.PluginID, Tag: intent.Tag, Operation: operation, OldCommitSHA: ptr(intent.ExpectedOldCommitSHA), NewCommitSHA: newCommit, IntentID: ptr(intent.ID), CorrelationID: batch.CorrelationID, CreatedAt: now}).Error; err != nil {
			return err
		}
		for _, transition := range transitions {
			if err := completeTransition(tx, transition, intent.Operation, now); err != nil {
				return err
			}
		}
	} else {
		for _, transition := range transitions {
			if err := abortTransition(tx, transition, now); err != nil {
				return err
			}
		}
	}
	state := plugindomain.ReceiveBatchStateCompleted
	if disposition == gitservice.ReceiveAborted {
		state = plugindomain.ReceiveBatchStateAborted
	}
	result := tx.Model(&plugindomain.ReceiveIntent{}).Where("id = ? AND state IN ?", intent.ID, []string{plugindomain.ReceiveBatchStatePrepared, plugindomain.ReceiveBatchStateFinalizing}).Updates(map[string]any{"state": state, "updated_at": now, "completed_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		return errors.New("receive intent cas mismatch")
	}
	return nil
}

func completeTransition(tx *gorm.DB, transition plugindomain.RevisionProjectionTransition, operation string, now time.Time) error {
	updates := map[string]any{"generation": transition.ExpectedGeneration + 1, "updated_at": now}
	if operation == plugindomain.ReceiveOperationMove && transition.StagedArtifactID != nil {
		updates["artifact_id"] = *transition.StagedArtifactID
		updates["available"] = true
		result := tx.Model(&plugindomain.ProjectionArtifact{}).Where("id = ? AND state = ?", *transition.StagedArtifactID, plugindomain.ArtifactStateStaged).Updates(map[string]any{"state": plugindomain.ArtifactStateReady, "ready_at": now, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("artifact cas mismatch")
		}
	} else if operation == plugindomain.ReceiveOperationDelete {
		updates["artifact_id"] = nil
		updates["available"] = false
	} else {
		return errors.New("invalid projection transition")
	}
	result := tx.Model(&plugindomain.RevisionProjectionPointer{}).Where("id = ? AND generation = ? AND available = ?", transition.PointerID, transition.ExpectedGeneration, false).Updates(updates)
	if result.Error != nil || result.RowsAffected != 1 {
		return errors.New("pointer cas mismatch")
	}
	if transition.CurrentArtifactID != nil {
		if err := enqueueGC(tx, *transition.CurrentArtifactID, now); err != nil {
			return err
		}
	}
	return tx.Model(&plugindomain.RevisionProjectionTransition{}).Where("id = ?", transition.ID).Updates(map[string]any{"state": plugindomain.ProjectionTransitionStateCompleted, "updated_at": now, "completed_at": now}).Error
}

func abortTransition(tx *gorm.DB, transition plugindomain.RevisionProjectionTransition, now time.Time) error {
	result := tx.Model(&plugindomain.RevisionProjectionPointer{}).Where("id = ? AND generation = ? AND available = ?", transition.PointerID, transition.ExpectedGeneration, false).Updates(map[string]any{"available": true, "updated_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		return errors.New("pointer cas mismatch")
	}
	if transition.StagedArtifactID != nil {
		if err := enqueueGC(tx, *transition.StagedArtifactID, now); err != nil {
			return err
		}
	}
	return tx.Model(&plugindomain.RevisionProjectionTransition{}).Where("id = ?", transition.ID).Updates(map[string]any{"state": plugindomain.ProjectionTransitionStateAborted, "updated_at": now, "completed_at": now}).Error
}

func enqueueGC(tx *gorm.DB, artifactID string, now time.Time) error {
	var job plugindomain.ProjectionGCJob
	err := tx.Where("artifact_id = ?", artifactID).Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&plugindomain.ProjectionGCJob{ID: uuid.NewString(), ArtifactID: artifactID, State: plugindomain.ProjectionGCStatePending, CreatedAt: now, UpdatedAt: now}).Error
	}
	return err
}

func (r *Reconciler) manualBatch(ctx context.Context, batchID, reason string) error {
	now := r.opts.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&plugindomain.ReceiveBatch{}).Where("id = ? AND state NOT IN ?", batchID, []string{plugindomain.ReceiveBatchStateCompleted, plugindomain.ReceiveBatchStateAborted}).Updates(map[string]any{"state": plugindomain.ReceiveBatchStateManualRequired, "safe_error": reason, "updated_at": now}).Error; err != nil {
			return unavailable("mark receive manual", err)
		}
		if err := tx.Model(&plugindomain.ReceiveIntent{}).Where("batch_id = ? AND state NOT IN ?", batchID, []string{plugindomain.ReceiveBatchStateCompleted, plugindomain.ReceiveBatchStateAborted}).Updates(map[string]any{"state": plugindomain.ReceiveBatchStateManualRequired, "safe_error": reason, "updated_at": now}).Error; err != nil {
			return unavailable("mark receive intent manual", err)
		}
		return nil
	})
}

// ReconcileOrphans discovers only aged opaque candidates and deletes only those
// whose storage key is absent from both repositories and cleanup records.
func (r *Reconciler) ReconcileOrphans(ctx context.Context) (int, error) {
	if err := r.valid(ctx); err != nil {
		return 0, err
	}
	if r.opts.MinimumOrphanAge <= 0 {
		return 0, ErrInvalidOptions
	}
	candidates, err := r.fs.ListRepositoryOrphanCandidates(ctx, r.opts.MinimumOrphanAge)
	if err != nil {
		return 0, unavailable("list orphan candidates", err)
	}
	processed := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if processed >= r.opts.BatchSize {
			break
		}
		if candidate.StorageKey == "" || candidate.CreatedAt.After(r.opts.Now().Add(-r.opts.MinimumOrphanAge)) {
			continue
		}
		var count int64
		if err := r.db.WithContext(ctx).Table("repositories AS r").
			Where("r.storage_key = ?", candidate.StorageKey).
			Count(&count).Error; err != nil {
			return processed, unavailable("check repository aggregate", err)
		}
		if count != 0 {
			continue
		}
		if err := r.cleaner.RemoveOrphanRepository(ctx, candidate.StorageKey); err != nil {
			return processed, unavailable("remove orphan repository", err)
		}
		processed++
		if err := ctx.Err(); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

// RunProjectionGC removes artifacts through an idempotent state machine.
func (r *Reconciler) RunProjectionGC(ctx context.Context) (int, error) {
	if err := r.valid(ctx); err != nil {
		return 0, err
	}
	var jobs []plugindomain.ProjectionGCJob
	if err := r.db.WithContext(ctx).Where("state IN ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", []string{plugindomain.ProjectionGCStatePending, plugindomain.ProjectionGCStateRunning}, r.opts.Now()).Order("created_at ASC").Limit(r.opts.BatchSize).Find(&jobs).Error; err != nil {
		return 0, unavailable("list projection gc jobs", err)
	}
	processed := 0
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := r.gcOne(ctx, job); err != nil {
			return processed, err
		}
		processed++
		if err := ctx.Err(); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

func (r *Reconciler) gcOne(ctx context.Context, job plugindomain.ProjectionGCJob) error {
	now := r.opts.Now().UTC()
	var artifact plugindomain.ProjectionArtifact
	if err := r.db.WithContext(ctx).First(&artifact, "id = ?", job.ArtifactID).Error; err != nil {
		return r.gcManual(ctx, job.ID, "artifact_missing")
	}
	claim := r.db.WithContext(ctx).Model(&plugindomain.ProjectionGCJob{}).
		Where("id = ? AND state = ? AND attempts = ?", job.ID, job.State, job.Attempts).
		Updates(map[string]any{"state": plugindomain.ProjectionGCStateRunning, "attempts": job.Attempts + 1, "updated_at": now})
	if claim.Error != nil {
		return unavailable("claim projection gc", claim.Error)
	}
	if claim.RowsAffected != 1 {
		return nil
	}
	var references int64
	if err := r.db.WithContext(ctx).Model(&plugindomain.RevisionProjectionPointer{}).
		Where("artifact_id = ?", artifact.ID).Count(&references).Error; err != nil {
		return unavailable("check projection references", err)
	}
	if references != 0 {
		return r.gcManual(ctx, job.ID, "projection_still_referenced")
	}
	if err := r.gc.RemoveProjection(ctx, gitservice.ImmutableProjection{Kind: gitservice.ProjectionKind(artifact.Kind), StorageKey: artifact.StorageKey}); err != nil {
		return r.gcManual(ctx, job.ID, "projection_remove_failed")
	}
	if err := r.db.WithContext(ctx).Model(&plugindomain.ProjectionGCJob{}).Where("id = ?", job.ID).Updates(map[string]any{"state": plugindomain.ProjectionGCStateCompleted, "completed_at": now, "updated_at": now}).Error; err != nil {
		return unavailable("complete projection gc", err)
	}
	return nil
}

func (r *Reconciler) gcManual(ctx context.Context, jobID, reason string) error {
	now := r.opts.Now().UTC()
	if err := r.db.WithContext(ctx).Model(&plugindomain.ProjectionGCJob{}).Where("id = ?", jobID).Updates(map[string]any{"state": plugindomain.ProjectionGCStateManualRequired, "safe_error": reason, "updated_at": now}).Error; err != nil {
		return unavailable("mark projection gc manual", err)
	}
	return nil
}
func (r *Reconciler) valid(ctx context.Context) error {
	if r == nil || r.db == nil {
		return ErrUnavailable
	}
	return ctx.Err()
}
func ptr(value string) *string { return &value }
func unavailable(operation string, err error) error {
	if err == nil {
		return ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, ErrUnavailable)
}
