package receive

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrUnavailable    = errors.New("receive coordination unavailable")
	ErrRejected       = errors.New("receive batch rejected")
	ErrManualRequired = errors.New("receive reconciliation requires manual recovery")
)

type Options struct {
	PostgresLockTimeout time.Duration
	LockRetryInterval   time.Duration
	Now                 func() time.Time
}

type Coordinator struct {
	db                *gorm.DB
	builder           gitservice.ProjectionBuilder
	lockTimeout       time.Duration
	lockRetryInterval time.Duration
	now               func() time.Time
}

func NewCoordinator(db *gorm.DB, builder gitservice.ProjectionBuilder, options Options) (*Coordinator, error) {
	if db == nil {
		return nil, errors.New("receive coordinator requires database")
	}
	if builder == nil {
		return nil, errors.New("receive coordinator requires projection builder")
	}
	driver := strings.ToLower(strings.TrimSpace(db.Dialector.Name()))
	if driver != "sqlite" && driver != "postgres" {
		return nil, errors.New("receive coordinator requires PostgreSQL or SQLite")
	}
	if options.PostgresLockTimeout <= 0 {
		options.PostgresLockTimeout = 5 * time.Second
	}
	if options.LockRetryInterval <= 0 {
		options.LockRetryInterval = 25 * time.Millisecond
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Coordinator{
		db: db, builder: builder, lockTimeout: options.PostgresLockTimeout,
		lockRetryInterval: options.LockRetryInterval, now: options.Now,
	}, nil
}

func (coordinator *Coordinator) Open(ctx context.Context, pluginID, sessionID string) (gitservice.ReceiveCoordination, error) {
	if coordinator == nil || pluginID == "" || sessionID == "" {
		return nil, ErrUnavailable
	}
	unlock, err := coordinator.acquirePluginLock(ctx, pluginID)
	if err != nil {
		return nil, ErrUnavailable
	}
	var count int64
	err = coordinator.db.WithContext(ctx).
		Table("plugins AS p").
		Joins("JOIN repositories AS r ON r.id = p.id").
		Where("p.id = ? AND p.status <> ? AND r.status = ?", pluginID, plugindomain.PluginStatusArchived, plugindomain.RepositoryStatusReady).
		Count(&count).Error
	if err != nil || count != 1 {
		_ = unlock()
		return nil, ErrUnavailable
	}
	return &coordination{coordinator: coordinator, pluginID: pluginID, sessionID: sessionID, unlock: unlock}, nil
}

type coordination struct {
	coordinator *Coordinator
	pluginID    string
	sessionID   string
	unlock      func() error
	closeOnce   sync.Once
	closeErr    error
}

type receivePlan struct {
	command     gitservice.ReceiveTagCommand
	version     plugindomain.PluginVersion
	intentID    string
	revisions   []revisionPlan
	projections []builtProjection
}

type revisionPlan struct {
	revisionID  string
	publishedAt time.Time
	pointer     plugindomain.RevisionProjectionPointer
}

type builtProjection struct {
	revision revisionPlan
	result   gitservice.PluginProjectionResult
}

func (coordination *coordination) Prepare(ctx context.Context, batch gitservice.ReceiveBatch) (gitservice.PreparedReceiveBatch, error) {
	if coordination == nil || batch.Plugin.ID != coordination.pluginID || batch.SessionID == "" || batch.SessionID != coordination.sessionID {
		return gitservice.PreparedReceiveBatch{}, ErrRejected
	}
	if prepared, found, err := coordination.loadSession(ctx); err != nil {
		return gitservice.PreparedReceiveBatch{}, ErrRejected
	} else if found {
		return prepared, nil
	}
	plans, err := coordination.classify(ctx, batch.CanonicalTags)
	if err != nil {
		return gitservice.PreparedReceiveBatch{}, ErrRejected
	}
	if len(plans) == 0 {
		return gitservice.PreparedReceiveBatch{}, nil
	}
	if err := coordination.preflightPlans(ctx, plans); err != nil {
		return gitservice.PreparedReceiveBatch{}, errors.Join(ErrRejected, err)
	}
	built := make([]gitservice.ImmutableProjection, 0)
	for index := range plans {
		if plans[index].command.Operation != gitservice.ReceiveTagMove {
			continue
		}
		for _, revision := range plans[index].revisions {
			projectionID := uuid.New()
			result, buildErr := coordination.coordinator.builder.BuildPluginProjection(ctx, gitservice.BuildPluginProjectionCommand{
				ProjectionID:   projectionID,
				RepositoryID:   coordination.pluginID,
				TagName:        plans[index].command.Tag,
				SourceObjectID: plans[index].command.NewObjectID,
				PublishedAt:    revision.publishedAt,
			})
			if buildErr != nil || !validBuildResult(result, plans[index].command, projectionID) {
				if result.Projection.Kind == gitservice.ProjectionKindPlugin && result.Projection.StorageKey != "" {
					built = append(built, result.Projection)
				}
				coordination.removeBuilt(ctx, built)
				return gitservice.PreparedReceiveBatch{}, errors.Join(ErrRejected, errors.New("invalid projection build result"))
			}
			built = append(built, result.Projection)
			plans[index].projections = append(plans[index].projections, builtProjection{revision: revision, result: result})
		}
	}

	prepared, err := coordination.persistPrepared(ctx, plans)
	if err != nil {
		coordination.removeBuilt(ctx, built)
		return gitservice.PreparedReceiveBatch{}, errors.Join(ErrRejected, sanitizeError(err))
	}
	return prepared, nil
}

func (coordination *coordination) classify(ctx context.Context, commands []gitservice.ReceiveTagCommand) ([]receivePlan, error) {
	seen := make(map[string]struct{}, len(commands))
	plans := make([]receivePlan, 0, len(commands))
	for _, command := range commands {
		if command.Tag == "" || command.RefName != "refs/tags/"+command.Tag {
			return nil, ErrRejected
		}
		if _, duplicate := seen[command.Tag]; duplicate {
			return nil, ErrRejected
		}
		seen[command.Tag] = struct{}{}
		var version plugindomain.PluginVersion
		err := coordination.coordinator.db.WithContext(ctx).
			Where("plugin_id = ? AND tag = ? AND status = ?", coordination.pluginID, command.Tag, plugindomain.VersionStatusAvailable).
			Take(&version).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil || version.CommitSHA == nil || *version.CommitSHA == "" {
			return nil, ErrRejected
		}
		if command.Operation != gitservice.ReceiveTagMove && command.Operation != gitservice.ReceiveTagDelete {
			return nil, ErrRejected
		}
		if command.OldObjectID == "" || command.OldCommitObjectID != *version.CommitSHA {
			return nil, ErrRejected
		}
		if command.Operation == gitservice.ReceiveTagMove && (command.NewObjectID == "" || command.NewCommitObjectID == "" || len(command.NewManifestDigest) != 64 || len(command.NewManifestSnapshot) == 0) {
			return nil, ErrRejected
		}
		if command.Operation == gitservice.ReceiveTagDelete && (command.NewObjectID == "" || strings.Trim(command.NewObjectID, "0") != "" || command.NewCommitObjectID != "") {
			return nil, ErrRejected
		}
		plans = append(plans, receivePlan{command: command, version: version, intentID: uuid.NewString()})
	}
	return plans, nil
}

func (coordination *coordination) preflightPlans(ctx context.Context, plans []receivePlan) error {
	for index := range plans {
		plan := &plans[index]
		var unresolved int64
		if err := coordination.coordinator.db.WithContext(ctx).Model(&plugindomain.ReceiveIntent{}).
			Where("plugin_id = ? AND tag = ? AND state IN ?", coordination.pluginID, plan.command.Tag,
				[]string{plugindomain.ReceiveBatchStatePrepared, plugindomain.ReceiveBatchStateFinalizing, plugindomain.ReceiveBatchStateManualRequired}).
			Count(&unresolved).Error; err != nil || unresolved != 0 {
			return ErrRejected
		}
		if plan.command.Operation == gitservice.ReceiveTagDelete {
			var activeReferences int64
			err := coordination.coordinator.db.WithContext(ctx).
				Table("marketplace_revision_items AS i").
				Joins("JOIN marketplace_revisions AS r ON r.id = i.revision_id").
				Joins("JOIN marketplace_templates AS t ON t.id = r.template_id").
				Where("i.plugin_id = ? AND i.plugin_tag = ? AND t.published_revision_id = r.id AND t.status = ? AND r.status = ?",
					coordination.pluginID, plan.command.Tag, "active", "active").
				Count(&activeReferences).Error
			if err != nil || activeReferences != 0 {
				return ErrRejected
			}
		}
		var revisions []struct {
			RevisionID  string
			PublishedAt time.Time
		}
		if err := coordination.coordinator.db.WithContext(ctx).
			Table("marketplace_revision_items AS i").
			Select("i.revision_id, r.published_at").
			Joins("JOIN marketplace_revisions AS r ON r.id = i.revision_id").
			Where("i.plugin_id = ? AND i.plugin_tag = ?", coordination.pluginID, plan.command.Tag).
			Order("i.revision_id").Scan(&revisions).Error; err != nil {
			return ErrRejected
		}
		for _, revision := range revisions {
			var pointer plugindomain.RevisionProjectionPointer
			err := coordination.coordinator.db.WithContext(ctx).
				Where("revision_id = ? AND plugin_id = ? AND tag = ?", revision.RevisionID, coordination.pluginID, plan.command.Tag).
				Take(&pointer).Error
			if err != nil || !pointer.Available || pointer.ArtifactID == nil || *pointer.ArtifactID == "" {
				return ErrRejected
			}
			var artifact plugindomain.ProjectionArtifact
			err = coordination.coordinator.db.WithContext(ctx).
				Where("id = ? AND plugin_id = ? AND tag = ? AND revision_id = ? AND state = ? AND source_commit_sha = ?",
					*pointer.ArtifactID, coordination.pluginID, plan.command.Tag, revision.RevisionID,
					plugindomain.ArtifactStateReady, plan.command.OldCommitObjectID).
				Take(&artifact).Error
			if err != nil {
				return ErrRejected
			}
			plan.revisions = append(plan.revisions, revisionPlan{revisionID: revision.RevisionID, publishedAt: revision.PublishedAt, pointer: pointer})
		}
	}
	return nil
}

func validBuildResult(result gitservice.PluginProjectionResult, command gitservice.ReceiveTagCommand, projectionID uuid.UUID) bool {
	if result.Projection.Kind != gitservice.ProjectionKindPlugin || result.Projection.StorageKey == "" ||
		strings.ContainsAny(result.Projection.StorageKey, `/\\`) || strings.Contains(result.Projection.StorageKey, "..") ||
		result.TagName != command.Tag || result.SourceCommitSHA != command.NewCommitObjectID ||
		result.SourceTreeSHA == "" || result.DistributionSHA == "" || len(result.ContentDigest) != 64 {
		return false
	}
	if result.SourceTagType == gitservice.SourceTagAnnotated {
		return result.SourceTagObjectID == command.NewObjectID
	}
	if result.SourceTagType == gitservice.SourceTagLightweight {
		return result.SourceTagObjectID == "" && command.NewObjectID == command.NewCommitObjectID
	}
	_ = projectionID
	return false
}

func (coordination *coordination) persistPrepared(ctx context.Context, plans []receivePlan) (gitservice.PreparedReceiveBatch, error) {
	now := coordination.coordinator.now().UTC()
	batchID := uuid.NewString()
	err := coordination.coordinator.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		batch := &plugindomain.ReceiveBatch{
			ID: batchID, PluginID: coordination.pluginID, State: plugindomain.ReceiveBatchStatePrepared,
			CorrelationID: coordination.sessionID, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(batch).Error; err != nil {
			return err
		}
		for _, plan := range plans {
			var proposedObjectID, proposedCommit, proposedManifestDigest *string
			var proposedManifestSnapshot []byte
			if plan.command.Operation == gitservice.ReceiveTagMove {
				proposedObjectID = stringPointer(plan.command.NewObjectID)
				proposedCommit = stringPointer(plan.command.NewCommitObjectID)
				proposedManifestDigest = stringPointer(plan.command.NewManifestDigest)
				proposedManifestSnapshot = append([]byte(nil), plan.command.NewManifestSnapshot...)
			}
			intent := &plugindomain.ReceiveIntent{
				ID: plan.intentID, BatchID: batchID, PluginID: coordination.pluginID,
				Operation: string(plan.command.Operation), Tag: plan.command.Tag,
				ExpectedOldObjectID: plan.command.OldObjectID, ProposedNewObjectID: proposedObjectID,
				ExpectedOldCommitSHA: plan.command.OldCommitObjectID, ProposedNewCommitSHA: proposedCommit,
				ProposedManifestDigest: proposedManifestDigest, ProposedManifestSnapshot: proposedManifestSnapshot,
				ExpectedVersionStatus: plugindomain.VersionStatusAvailable,
				State:                 plugindomain.ReceiveBatchStatePrepared, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(intent).Error; err != nil {
				return err
			}
			projectionByRevision := make(map[string]gitservice.PluginProjectionResult, len(plan.projections))
			for _, projection := range plan.projections {
				projectionByRevision[projection.revision.revisionID] = projection.result
			}
			for _, revision := range plan.revisions {
				var stagedArtifactID *string
				if result, ok := projectionByRevision[revision.revisionID]; ok {
					artifactID := uuid.NewString()
					revisionID := revision.revisionID
					artifact := &plugindomain.ProjectionArtifact{
						ID: artifactID, Kind: plugindomain.ArtifactKindPlugin, PluginID: coordination.pluginID,
						Tag: plan.command.Tag, SourceObjectID: plan.command.NewObjectID,
						SourceCommitSHA: result.SourceCommitSHA, SourceTreeSHA: result.SourceTreeSHA,
						RevisionID: &revisionID, ContentDigest: result.ContentDigest,
						DistributionSHA: result.DistributionSHA, StorageKey: result.Projection.StorageKey,
						State: plugindomain.ArtifactStateStaged, CreatedAt: now, UpdatedAt: now,
					}
					if err := tx.Create(artifact).Error; err != nil {
						return err
					}
					stagedArtifactID = &artifactID
				}
				transition := &plugindomain.RevisionProjectionTransition{
					ID: uuid.NewString(), IntentID: plan.intentID, RevisionID: revision.revisionID,
					PointerID: revision.pointer.ID, ExpectedGeneration: revision.pointer.Generation,
					CurrentArtifactID: revision.pointer.ArtifactID, StagedArtifactID: stagedArtifactID,
					State: plugindomain.ProjectionTransitionStatePending, CreatedAt: now, UpdatedAt: now,
				}
				if err := tx.Create(transition).Error; err != nil {
					return err
				}
				query := tx.Model(&plugindomain.RevisionProjectionPointer{}).
					Where("id = ? AND generation = ? AND available = ?", revision.pointer.ID, revision.pointer.Generation, true)
				query = whereNullableID(query, "artifact_id", revision.pointer.ArtifactID)
				result := query.Updates(map[string]any{"available": false, "updated_at": now})
				if result.Error != nil || result.RowsAffected != 1 {
					return ErrManualRequired
				}
			}
		}
		return nil
	})
	if err != nil {
		return gitservice.PreparedReceiveBatch{}, err
	}
	return coordination.loadPrepared(ctx, batchID)
}

func (coordination *coordination) Resolve(ctx context.Context, prepared gitservice.PreparedReceiveBatch, resolution gitservice.ReceiveResolution) error {
	if coordination == nil || prepared.ID == "" {
		return ErrRejected
	}
	batch, intents, transitions, authoritative, err := coordination.loadResolutionState(ctx, prepared.ID)
	if err != nil || batch.PluginID != coordination.pluginID || batch.CorrelationID != coordination.sessionID {
		return ErrRejected
	}
	if isTerminalBatchState(batch.State) {
		return nil
	}
	disposition := classifyResolution(authoritative, resolution.Observed)
	if resolution.Disposition == gitservice.ReceiveManualRequired || disposition == gitservice.ReceiveManualRequired ||
		(resolution.Disposition != "" && resolution.Disposition != disposition) {
		if err := coordination.markManual(ctx, batch.ID, transitions, "unexpected_ref"); err != nil {
			return ErrManualRequired
		}
		return nil
	}
	if disposition == gitservice.ReceiveCompleted {
		for _, transition := range transitions {
			if transition.StagedArtifactID == nil {
				continue
			}
			var artifact plugindomain.ProjectionArtifact
			if err := coordination.coordinator.db.WithContext(ctx).First(&artifact, "id = ?", *transition.StagedArtifactID).Error; err != nil {
				_ = coordination.markManual(ctx, batch.ID, transitions, "artifact_missing")
				return ErrManualRequired
			}
			projection := gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindPlugin, StorageKey: artifact.StorageKey}
			if err := coordination.coordinator.builder.VerifyProjection(ctx, projection, artifact.ContentDigest); err != nil {
				_ = coordination.coordinator.db.WithContext(ctx).Model(&plugindomain.ProjectionArtifact{}).
					Where("id = ?", artifact.ID).Updates(map[string]any{"state": plugindomain.ArtifactStateFailed, "safe_error": "digest_verification_failed", "updated_at": coordination.coordinator.now().UTC()}).Error
				_ = coordination.markManual(ctx, batch.ID, transitions, "artifact_verification_failed")
				return ErrManualRequired
			}
		}
	}
	if err := coordination.applyResolution(ctx, batch, intents, transitions, disposition); err != nil {
		_ = coordination.markManual(ctx, batch.ID, transitions, "sql_cas_mismatch")
		return ErrManualRequired
	}
	return nil
}

func (coordination *coordination) applyResolution(ctx context.Context, batch plugindomain.ReceiveBatch, intents []plugindomain.ReceiveIntent, transitions []plugindomain.RevisionProjectionTransition, disposition gitservice.ReceiveDisposition) error {
	now := coordination.coordinator.now().UTC()
	transitionsByIntent := make(map[string][]plugindomain.RevisionProjectionTransition)
	for _, transition := range transitions {
		transitionsByIntent[transition.IntentID] = append(transitionsByIntent[transition.IntentID], transition)
	}
	return coordination.coordinator.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var currentBatch plugindomain.ReceiveBatch
		if err := tx.First(&currentBatch, "id = ?", batch.ID).Error; err != nil {
			return err
		}
		if isTerminalBatchState(currentBatch.State) {
			return nil
		}
		for _, intent := range intents {
			intentTransitions := transitionsByIntent[intent.ID]
			switch disposition {
			case gitservice.ReceiveCompleted:
				if err := coordination.completeIntent(tx, currentBatch, intent, intentTransitions, now); err != nil {
					return err
				}
			case gitservice.ReceiveAborted:
				if err := coordination.abortIntent(tx, intent, intentTransitions, now); err != nil {
					return err
				}
			default:
				return ErrManualRequired
			}
		}
		state := plugindomain.ReceiveBatchStateCompleted
		if disposition == gitservice.ReceiveAborted {
			state = plugindomain.ReceiveBatchStateAborted
		}
		result := tx.Model(&plugindomain.ReceiveBatch{}).
			Where("id = ? AND state IN ?", batch.ID, []string{plugindomain.ReceiveBatchStatePrepared, plugindomain.ReceiveBatchStateFinalizing}).
			Updates(map[string]any{"state": state, "updated_at": now, "completed_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return ErrManualRequired
		}
		return nil
	})
}

func (coordination *coordination) completeIntent(tx *gorm.DB, batch plugindomain.ReceiveBatch, intent plugindomain.ReceiveIntent, transitions []plugindomain.RevisionProjectionTransition, now time.Time) error {
	versionQuery := tx.Model(&plugindomain.PluginVersion{}).
		Where("plugin_id = ? AND tag = ? AND status = ? AND commit_sha = ?", intent.PluginID, intent.Tag, intent.ExpectedVersionStatus, intent.ExpectedOldCommitSHA)
	var historyOperation string
	var newCommit *string
	if intent.Operation == plugindomain.ReceiveOperationMove && intent.ProposedNewObjectID != nil && intent.ProposedNewCommitSHA != nil && intent.ProposedManifestDigest != nil && len(intent.ProposedManifestSnapshot) != 0 {
		newCommit = intent.ProposedNewCommitSHA
		historyOperation = plugindomain.VersionHistoryOperationMove
		result := versionQuery.Updates(map[string]any{
			"raw_tag_object_id": *intent.ProposedNewObjectID, "commit_sha": *newCommit,
			"manifest_digest": *intent.ProposedManifestDigest, "manifest_snapshot": append([]byte(nil), intent.ProposedManifestSnapshot...),
			"updated_at": now,
		})
		if result.Error != nil || result.RowsAffected != 1 {
			return ErrManualRequired
		}
	} else if intent.Operation == plugindomain.ReceiveOperationDelete {
		historyOperation = plugindomain.VersionHistoryOperationDelete
		result := versionQuery.Updates(map[string]any{
			"status": plugindomain.VersionStatusDeleted, "raw_tag_object_id": nil, "commit_sha": nil, "manifest_digest": nil,
			"manifest_snapshot": nil, "deleted_at": now, "updated_at": now,
		})
		if result.Error != nil || result.RowsAffected != 1 {
			return ErrManualRequired
		}
		if err := tx.Model(&plugindomain.Plugin{}).
			Where("id = ? AND default_version_tag = ?", intent.PluginID, intent.Tag).
			Updates(map[string]any{"default_version_tag": nil, "updated_at": now}).Error; err != nil {
			return err
		}
	} else {
		return ErrManualRequired
	}
	intentID := intent.ID
	history := &plugindomain.PluginVersionHistory{
		ID: uuid.NewString(), PluginID: intent.PluginID, Tag: intent.Tag, Operation: historyOperation,
		OldCommitSHA: stringPointer(intent.ExpectedOldCommitSHA), NewCommitSHA: newCommit,
		IntentID: &intentID, CorrelationID: batch.CorrelationID, CreatedAt: now,
	}
	if err := tx.Create(history).Error; err != nil {
		return err
	}
	for _, transition := range transitions {
		updates := map[string]any{"generation": transition.ExpectedGeneration + 1, "updated_at": now}
		if intent.Operation == plugindomain.ReceiveOperationMove && transition.StagedArtifactID != nil {
			updates["artifact_id"] = *transition.StagedArtifactID
			updates["available"] = true
			artifactUpdate := tx.Model(&plugindomain.ProjectionArtifact{}).
				Where("id = ? AND state = ?", *transition.StagedArtifactID, plugindomain.ArtifactStateStaged).
				Updates(map[string]any{"state": plugindomain.ArtifactStateReady, "ready_at": now, "updated_at": now})
			if artifactUpdate.Error != nil || artifactUpdate.RowsAffected != 1 {
				return ErrManualRequired
			}
		} else if intent.Operation == plugindomain.ReceiveOperationDelete {
			updates["artifact_id"] = nil
			updates["available"] = false
		} else {
			return ErrManualRequired
		}
		query := tx.Model(&plugindomain.RevisionProjectionPointer{}).
			Where("id = ? AND generation = ? AND available = ?", transition.PointerID, transition.ExpectedGeneration, false)
		query = whereNullableID(query, "artifact_id", transition.CurrentArtifactID)
		result := query.Updates(updates)
		if result.Error != nil || result.RowsAffected != 1 {
			return ErrManualRequired
		}
		if transition.CurrentArtifactID != nil && (transition.StagedArtifactID == nil || *transition.CurrentArtifactID != *transition.StagedArtifactID) {
			if err := enqueueGC(tx, *transition.CurrentArtifactID, now); err != nil {
				return err
			}
		}
		if err := updateTransition(tx, transition.ID, plugindomain.ProjectionTransitionStateCompleted, now); err != nil {
			return err
		}
	}
	return updateIntent(tx, intent.ID, plugindomain.ReceiveBatchStateCompleted, now)
}

func (coordination *coordination) abortIntent(tx *gorm.DB, intent plugindomain.ReceiveIntent, transitions []plugindomain.RevisionProjectionTransition, now time.Time) error {
	for _, transition := range transitions {
		query := tx.Model(&plugindomain.RevisionProjectionPointer{}).
			Where("id = ? AND generation = ? AND available = ?", transition.PointerID, transition.ExpectedGeneration, false)
		query = whereNullableID(query, "artifact_id", transition.CurrentArtifactID)
		result := query.Updates(map[string]any{"available": true, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return ErrManualRequired
		}
		if transition.StagedArtifactID != nil {
			if err := enqueueGC(tx, *transition.StagedArtifactID, now); err != nil {
				return err
			}
		}
		if err := updateTransition(tx, transition.ID, plugindomain.ProjectionTransitionStateAborted, now); err != nil {
			return err
		}
	}
	return updateIntent(tx, intent.ID, plugindomain.ReceiveBatchStateAborted, now)
}

func (coordination *coordination) markManual(ctx context.Context, batchID string, transitions []plugindomain.RevisionProjectionTransition, safeError string) error {
	now := coordination.coordinator.now().UTC()
	return coordination.coordinator.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&plugindomain.ReceiveBatch{}).Where("id = ? AND state NOT IN ?", batchID,
			[]string{plugindomain.ReceiveBatchStateCompleted, plugindomain.ReceiveBatchStateAborted}).
			Updates(map[string]any{"state": plugindomain.ReceiveBatchStateManualRequired, "safe_error": safeError, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&plugindomain.ReceiveIntent{}).Where("batch_id = ? AND state NOT IN ?", batchID,
			[]string{plugindomain.ReceiveBatchStateCompleted, plugindomain.ReceiveBatchStateAborted}).
			Updates(map[string]any{"state": plugindomain.ReceiveBatchStateManualRequired, "safe_error": safeError, "updated_at": now}).Error; err != nil {
			return err
		}
		for _, transition := range transitions {
			if err := tx.Model(&plugindomain.RevisionProjectionPointer{}).Where("id = ?", transition.PointerID).
				Updates(map[string]any{"available": false, "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&plugindomain.RevisionProjectionTransition{}).Where("id = ? AND state NOT IN ?", transition.ID,
				[]string{plugindomain.ProjectionTransitionStateCompleted, plugindomain.ProjectionTransitionStateAborted}).
				Updates(map[string]any{"state": plugindomain.ProjectionTransitionStateManualRequired, "safe_error": safeError, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (coordinator *Coordinator) Reconcile(ctx context.Context, prepared gitservice.PreparedReceiveBatch, observed []gitservice.ObservedReceiveRef) error {
	if coordinator == nil || prepared.ID == "" {
		return ErrRejected
	}
	var batch plugindomain.ReceiveBatch
	if err := coordinator.db.WithContext(ctx).First(&batch, "id = ?", prepared.ID).Error; err != nil {
		return ErrRejected
	}
	coordination, err := coordinator.Open(ctx, batch.PluginID, batch.CorrelationID)
	if err != nil {
		return err
	}
	defer coordination.Close()
	return coordination.Resolve(ctx, prepared, gitservice.ReceiveResolution{Observed: observed})
}

func (coordination *coordination) loadSession(ctx context.Context) (gitservice.PreparedReceiveBatch, bool, error) {
	var batch plugindomain.ReceiveBatch
	err := coordination.coordinator.db.WithContext(ctx).
		Where("plugin_id = ? AND correlation_id = ?", coordination.pluginID, coordination.sessionID).
		Order("created_at").Take(&batch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return gitservice.PreparedReceiveBatch{}, false, nil
	}
	if err != nil {
		return gitservice.PreparedReceiveBatch{}, false, err
	}
	prepared, err := coordination.loadPrepared(ctx, batch.ID)
	return prepared, true, err
}

func (coordination *coordination) loadPrepared(ctx context.Context, batchID string) (gitservice.PreparedReceiveBatch, error) {
	var intents []plugindomain.ReceiveIntent
	if err := coordination.coordinator.db.WithContext(ctx).Where("batch_id = ?", batchID).Order("tag").Find(&intents).Error; err != nil {
		return gitservice.PreparedReceiveBatch{}, err
	}
	prepared := gitservice.PreparedReceiveBatch{ID: batchID, Transitions: make([]gitservice.PreparedReceiveTransition, 0, len(intents))}
	for _, intent := range intents {
		proposed := strings.Repeat("0", len(intent.ExpectedOldObjectID))
		if intent.ProposedNewObjectID != nil {
			proposed = *intent.ProposedNewObjectID
		}
		prepared.Transitions = append(prepared.Transitions, gitservice.PreparedReceiveTransition{
			Tag: intent.Tag, RefName: "refs/tags/" + intent.Tag,
			ExpectedOldObjectID: intent.ExpectedOldObjectID, ProposedNewObjectID: proposed,
		})
	}
	return prepared, nil
}

func (coordination *coordination) loadResolutionState(ctx context.Context, batchID string) (plugindomain.ReceiveBatch, []plugindomain.ReceiveIntent, []plugindomain.RevisionProjectionTransition, gitservice.PreparedReceiveBatch, error) {
	var batch plugindomain.ReceiveBatch
	if err := coordination.coordinator.db.WithContext(ctx).First(&batch, "id = ?", batchID).Error; err != nil {
		return batch, nil, nil, gitservice.PreparedReceiveBatch{}, err
	}
	var intents []plugindomain.ReceiveIntent
	if err := coordination.coordinator.db.WithContext(ctx).Where("batch_id = ?", batchID).Order("tag").Find(&intents).Error; err != nil {
		return batch, nil, nil, gitservice.PreparedReceiveBatch{}, err
	}
	intentIDs := make([]string, 0, len(intents))
	for _, intent := range intents {
		intentIDs = append(intentIDs, intent.ID)
	}
	var transitions []plugindomain.RevisionProjectionTransition
	if len(intentIDs) != 0 {
		if err := coordination.coordinator.db.WithContext(ctx).Where("intent_id IN ?", intentIDs).Order("revision_id").Find(&transitions).Error; err != nil {
			return batch, nil, nil, gitservice.PreparedReceiveBatch{}, err
		}
	}
	authoritative, err := coordination.loadPrepared(ctx, batchID)
	return batch, intents, transitions, authoritative, err
}

func classifyResolution(prepared gitservice.PreparedReceiveBatch, observed []gitservice.ObservedReceiveRef) gitservice.ReceiveDisposition {
	if prepared.ID == "" || len(prepared.Transitions) == 0 || len(prepared.Transitions) != len(observed) {
		return gitservice.ReceiveManualRequired
	}
	byRef := make(map[string]gitservice.ObservedReceiveRef, len(observed))
	for _, ref := range observed {
		if ref.RefName == "" {
			return gitservice.ReceiveManualRequired
		}
		if _, duplicate := byRef[ref.RefName]; duplicate {
			return gitservice.ReceiveManualRequired
		}
		byRef[ref.RefName] = ref
	}
	allOld, allProposed := true, true
	for _, transition := range prepared.Transitions {
		ref, ok := byRef[transition.RefName]
		if !ok {
			return gitservice.ReceiveManualRequired
		}
		oldMatches := observedMatches(ref, transition.ExpectedOldObjectID)
		proposedMatches := observedMatches(ref, transition.ProposedNewObjectID)
		if !oldMatches && !proposedMatches {
			return gitservice.ReceiveManualRequired
		}
		allOld = allOld && oldMatches
		allProposed = allProposed && proposedMatches
	}
	if allProposed {
		return gitservice.ReceiveCompleted
	}
	if allOld {
		return gitservice.ReceiveAborted
	}
	return gitservice.ReceiveManualRequired
}

func observedMatches(observed gitservice.ObservedReceiveRef, expected string) bool {
	if expected != "" && strings.Trim(expected, "0") == "" {
		return !observed.Exists
	}
	return observed.Exists && observed.ObjectID == expected
}

func updateIntent(tx *gorm.DB, intentID, state string, now time.Time) error {
	result := tx.Model(&plugindomain.ReceiveIntent{}).
		Where("id = ? AND state IN ?", intentID, []string{plugindomain.ReceiveBatchStatePrepared, plugindomain.ReceiveBatchStateFinalizing}).
		Updates(map[string]any{"state": state, "updated_at": now, "completed_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		return ErrManualRequired
	}
	return nil
}

func updateTransition(tx *gorm.DB, transitionID, state string, now time.Time) error {
	result := tx.Model(&plugindomain.RevisionProjectionTransition{}).
		Where("id = ? AND state IN ?", transitionID, []string{plugindomain.ProjectionTransitionStatePrepared, plugindomain.ProjectionTransitionStatePending}).
		Updates(map[string]any{"state": state, "updated_at": now, "completed_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		return ErrManualRequired
	}
	return nil
}

func enqueueGC(tx *gorm.DB, artifactID string, now time.Time) error {
	job := &plugindomain.ProjectionGCJob{
		ID: uuid.NewString(), ArtifactID: artifactID, State: plugindomain.ProjectionGCStatePending,
		CreatedAt: now, UpdatedAt: now,
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "artifact_id"}}, DoNothing: true}).Create(job).Error
}

func whereNullableID(query *gorm.DB, column string, value *string) *gorm.DB {
	if value == nil {
		return query.Where(column + " IS NULL")
	}
	return query.Where(column+" = ?", *value)
}

func sanitizeError(err error) error {
	if errors.Is(err, ErrManualRequired) {
		return ErrManualRequired
	}
	return errors.New("durable receive prepare failed")
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}

func isTerminalBatchState(state string) bool {
	return state == plugindomain.ReceiveBatchStateCompleted || state == plugindomain.ReceiveBatchStateAborted || state == plugindomain.ReceiveBatchStateManualRequired
}

func (coordination *coordination) removeBuilt(ctx context.Context, projections []gitservice.ImmutableProjection) {
	for index := len(projections) - 1; index >= 0; index-- {
		_ = coordination.coordinator.builder.RemoveProjection(ctx, projections[index])
	}
}

func (coordination *coordination) Close() error {
	if coordination == nil {
		return nil
	}
	coordination.closeOnce.Do(func() {
		coordination.closeErr = coordination.unlock()
	})
	return coordination.closeErr
}

var sqliteProcessLocks = newKeyedLocks()

func (coordinator *Coordinator) acquirePluginLock(ctx context.Context, pluginID string) (func() error, error) {
	if strings.EqualFold(coordinator.db.Dialector.Name(), "sqlite") {
		unlock, err := sqliteProcessLocks.lock(ctx, pluginID)
		if err != nil {
			return nil, err
		}
		return func() error { unlock(); return nil }, nil
	}
	return coordinator.acquirePostgresLock(ctx, pluginID)
}

func (coordinator *Coordinator) acquirePostgresLock(ctx context.Context, pluginID string) (func() error, error) {
	sqlDB, err := coordinator.db.DB()
	if err != nil {
		return nil, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, coordinator.lockTimeout)
	defer cancel()
	conn, err := sqlDB.Conn(lockCtx)
	if err != nil {
		return nil, err
	}
	locked := false
	defer func() {
		if !locked {
			_ = conn.Close()
		}
	}()
	ticker := time.NewTicker(coordinator.lockRetryInterval)
	defer ticker.Stop()
	for {
		if err := conn.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", pluginID).Scan(&locked); err != nil {
			return nil, err
		}
		if locked {
			break
		}
		select {
		case <-lockCtx.Done():
			return nil, lockCtx.Err()
		case <-ticker.C:
		}
	}
	return func() error {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), coordinator.lockTimeout)
		defer unlockCancel()
		var unlocked bool
		err := conn.QueryRowContext(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", pluginID).Scan(&unlocked)
		closeErr := conn.Close()
		if err != nil {
			return err
		}
		if !unlocked {
			return errors.New("receive advisory lock was not held")
		}
		return closeErr
	}, nil
}

type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	semaphore chan struct{}
	users     int
}

func newKeyedLocks() *keyedLocks {
	return &keyedLocks{locks: make(map[string]*keyedLock)}
}

func (locks *keyedLocks) lock(ctx context.Context, key string) (func(), error) {
	locks.mu.Lock()
	entry := locks.locks[key]
	if entry == nil {
		entry = &keyedLock{semaphore: make(chan struct{}, 1)}
		entry.semaphore <- struct{}{}
		locks.locks[key] = entry
	}
	entry.users++
	locks.mu.Unlock()

	select {
	case <-ctx.Done():
		locks.releaseReference(key, entry)
		return nil, ctx.Err()
	case <-entry.semaphore:
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.semaphore <- struct{}{}
			locks.releaseReference(key, entry)
		})
	}, nil
}

func (locks *keyedLocks) releaseReference(key string, entry *keyedLock) {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	entry.users--
	if entry.users == 0 {
		delete(locks.locks, key)
	}
}

var _ gitservice.ReceiveCoordinator = (*Coordinator)(nil)
var _ gitservice.ReceiveCoordination = (*coordination)(nil)
