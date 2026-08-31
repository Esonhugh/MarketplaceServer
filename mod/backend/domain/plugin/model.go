package plugin

import (
	"errors"
	"time"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"gorm.io/gorm"
)

const (
	PluginStatusDraft    = "draft"
	PluginStatusActive   = "active"
	PluginStatusArchived = "archived"

	VisibilityPublic  = "public"
	VisibilityPrivate = "private"

	RepositoryStatusReady    = "ready"
	RepositoryStatusReadOnly = "readOnly"
	RepositoryStatusError    = "error"

	VersionStatusAvailable = "available"
	VersionStatusDeleted   = "deleted"
)

type Plugin struct {
	ID                string                  `gorm:"type:char(36);primaryKey"`
	NamespaceID       string                  `gorm:"type:char(36);not null;uniqueIndex:uidx_plugin_namespace_slug"`
	Slug              string                  `gorm:"size:128;not null;uniqueIndex:uidx_plugin_namespace_slug"`
	Description       string                  `gorm:"type:text"`
	Visibility        string                  `gorm:"size:32;not null;check:chk_plugins_visibility,visibility IN ('public','private')"`
	Status            string                  `gorm:"size:32;not null;check:chk_plugins_lifecycle,status IN ('draft','active','archived') AND ((status = 'archived' AND archived_from IS NOT NULL AND archived_from IN ('draft','active')) OR (status <> 'archived' AND archived_from IS NULL))"`
	ArchivedFrom      *string                 `gorm:"size:32"`
	DefaultVersionTag *string                 `gorm:"size:255"`
	CreatedAt         time.Time               `gorm:"not null"`
	UpdatedAt         time.Time               `gorm:"not null"`
	Namespace         identitymodel.Namespace `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Repository        Repository              `gorm:"foreignKey:ID;references:ID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (Plugin) TableName() string { return "plugins" }

func (plugin *Plugin) BeforeUpdate(tx *gorm.DB) error {
	for _, field := range []string{"ID", "NamespaceID", "Slug", "CreatedAt"} {
		if tx.Statement.Changed(field) {
			return errors.New("plugin identity is immutable")
		}
	}
	return nil
}

type Repository struct {
	ID         string    `gorm:"type:char(36);primaryKey;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	StorageKey string    `gorm:"size:255;not null;uniqueIndex"`
	Status     string    `gorm:"size:32;not null;check:chk_repositories_status,status IN ('ready','readOnly','error')"`
	CreatedAt  time.Time `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

func (Repository) TableName() string { return "repositories" }

func (repository *Repository) BeforeUpdate(tx *gorm.DB) error {
	for _, field := range []string{"ID", "StorageKey", "CreatedAt"} {
		if tx.Statement.Changed(field) {
			return errors.New("repository identity is immutable")
		}
	}
	return nil
}

type PluginVersion struct {
	ID               string     `gorm:"type:char(36);primaryKey"`
	PluginID         string     `gorm:"type:char(36);not null;uniqueIndex:uidx_plugin_version_tag"`
	Tag              string     `gorm:"size:255;not null;uniqueIndex:uidx_plugin_version_tag"`
	Status           string     `gorm:"size:32;not null;check:chk_plugin_versions_lifecycle,status IN ('available','deleted') AND ((status = 'available' AND raw_tag_object_id IS NOT NULL AND commit_sha IS NOT NULL AND manifest_digest IS NOT NULL AND manifest_snapshot IS NOT NULL AND deleted_at IS NULL) OR (status = 'deleted' AND raw_tag_object_id IS NULL AND commit_sha IS NULL AND manifest_digest IS NULL AND manifest_snapshot IS NULL AND deleted_at IS NOT NULL))"`
	RawTagObjectID   *string    `gorm:"size:255"`
	CommitSHA        *string    `gorm:"size:255"`
	ManifestDigest   *string    `gorm:"type:char(64)"`
	ManifestSnapshot []byte     `gorm:"type:json"`
	PublishedAt      time.Time  `gorm:"not null"`
	UpdatedAt        time.Time  `gorm:"not null"`
	DeletedAt        *time.Time `gorm:"index"`
	CreatedAt        time.Time  `gorm:"not null"`
	Plugin           Plugin     `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (PluginVersion) TableName() string { return "plugin_versions" }

const (
	VersionHistoryOperationPublish = "publish"
	VersionHistoryOperationMove    = "move"
	VersionHistoryOperationDelete  = "delete"
	VersionHistoryOperationRestore = "restore"

	OrphanCleanupStatePending        = "pending"
	OrphanCleanupStateRunning        = "running"
	OrphanCleanupStateCompleted      = "completed"
	OrphanCleanupStateManualRequired = "manual_required"

	ReceiveBatchStatePrepared       = "prepared"
	ReceiveBatchStateFinalizing     = "finalizing"
	ReceiveBatchStateCompleted      = "completed"
	ReceiveBatchStateAborted        = "aborted"
	ReceiveBatchStateManualRequired = "manual_required"

	ReceiveOperationMove   = "move"
	ReceiveOperationDelete = "delete"

	ArtifactKindPlugin      = "plugin"
	ArtifactKindMarketplace = "marketplace"
	ArtifactStateStaged     = "staged"
	ArtifactStateReady      = "ready"
	ArtifactStateFailed     = "failed"

	ProjectionTransitionStatePrepared       = "prepared"
	ProjectionTransitionStatePending        = "pending"
	ProjectionTransitionStateCompleted      = "completed"
	ProjectionTransitionStateAborted        = "aborted"
	ProjectionTransitionStateManualRequired = "manual_required"

	ProjectionGCStatePending        = "pending"
	ProjectionGCStateRunning        = "running"
	ProjectionGCStateCompleted      = "completed"
	ProjectionGCStateManualRequired = "manual_required"
)

type PluginVersionHistory struct {
	ID            string    `gorm:"type:char(36);primaryKey"`
	PluginID      string    `gorm:"type:char(36);not null;index:idx_plugin_version_history_identity"`
	Tag           string    `gorm:"size:255;not null;index:idx_plugin_version_history_identity"`
	Operation     string    `gorm:"size:32;not null;check:chk_plugin_version_history_operation,operation IN ('publish','move','delete','restore')"`
	OldCommitSHA  *string   `gorm:"size:255"`
	NewCommitSHA  *string   `gorm:"size:255"`
	IntentID      *string   `gorm:"type:char(36);index"`
	CorrelationID string    `gorm:"size:128;not null;index"`
	CreatedAt     time.Time `gorm:"not null"`
	Plugin        Plugin    `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (PluginVersionHistory) TableName() string { return "plugin_version_history" }

func (*PluginVersionHistory) BeforeUpdate(*gorm.DB) error {
	return errors.New("plugin version history is append-only")
}

func (*PluginVersionHistory) BeforeDelete(*gorm.DB) error {
	return errors.New("plugin version history is append-only")
}

type RepositoryOrphanCleanup struct {
	ID            string     `gorm:"type:char(36);primaryKey"`
	PluginID      string     `gorm:"type:char(36);not null;index"`
	StorageKey    string     `gorm:"size:255;not null;index"`
	State         string     `gorm:"size:32;not null;index;check:chk_repository_orphan_cleanups_state,state IN ('pending','running','completed','manual_required')"`
	Attempts      uint       `gorm:"not null"`
	SafeError     string     `gorm:"size:128"`
	NextAttemptAt *time.Time `gorm:"index"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
	CompletedAt   *time.Time
}

func (RepositoryOrphanCleanup) TableName() string { return "repository_orphan_cleanups" }

type ReceiveBatch struct {
	ID            string    `gorm:"type:char(36);primaryKey"`
	PluginID      string    `gorm:"type:char(36);not null;index"`
	State         string    `gorm:"size:32;not null;index;check:chk_receive_batches_state,state IN ('prepared','finalizing','completed','aborted','manual_required')"`
	CorrelationID string    `gorm:"size:128;not null;index"`
	Attempts      uint      `gorm:"not null"`
	SafeError     string    `gorm:"size:128"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
	CompletedAt   *time.Time
	Plugin        Plugin `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (ReceiveBatch) TableName() string { return "receive_batches" }

type ReceiveIntent struct {
	ID                       string    `gorm:"type:char(36);primaryKey"`
	BatchID                  string    `gorm:"type:char(36);not null;uniqueIndex:uidx_receive_batch_tag"`
	PluginID                 string    `gorm:"type:char(36);not null;index"`
	Operation                string    `gorm:"size:32;not null;check:chk_receive_intents_operation,operation IN ('move','delete')"`
	Tag                      string    `gorm:"size:255;not null;uniqueIndex:uidx_receive_batch_tag"`
	ExpectedOldObjectID      string    `gorm:"size:255;not null"`
	ProposedNewObjectID      *string   `gorm:"size:255"`
	ExpectedOldCommitSHA     string    `gorm:"size:255;not null"`
	ProposedNewCommitSHA     *string   `gorm:"size:255"`
	ProposedManifestDigest   *string   `gorm:"type:char(64)"`
	ProposedManifestSnapshot []byte    `gorm:"type:json"`
	ExpectedVersionStatus    string    `gorm:"size:32;not null"`
	State                    string    `gorm:"size:32;not null;index;check:chk_receive_intents_state,state IN ('prepared','finalizing','completed','aborted','manual_required')"`
	Attempts                 uint      `gorm:"not null"`
	SafeError                string    `gorm:"size:128"`
	CreatedAt                time.Time `gorm:"not null"`
	UpdatedAt                time.Time `gorm:"not null"`
	CompletedAt              *time.Time
	Batch                    ReceiveBatch `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Plugin                   Plugin       `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (ReceiveIntent) TableName() string { return "receive_intents" }

type ProjectionArtifact struct {
	ID              string    `gorm:"type:char(36);primaryKey"`
	Kind            string    `gorm:"size:32;not null;check:chk_projection_artifacts_kind,kind IN ('plugin','marketplace')"`
	PluginID        string    `gorm:"type:char(36);not null;index:idx_projection_artifact_source"`
	Tag             string    `gorm:"size:255;not null;index:idx_projection_artifact_source"`
	SourceObjectID  string    `gorm:"size:255;not null"`
	SourceCommitSHA string    `gorm:"size:255;not null;index:idx_projection_artifact_source"`
	SourceTreeSHA   string    `gorm:"size:255"`
	RevisionID      *string   `gorm:"type:char(36);index"`
	ContentDigest   string    `gorm:"type:char(64);not null"`
	DistributionSHA string    `gorm:"size:255"`
	StorageKey      string    `gorm:"size:255;not null;uniqueIndex"`
	State           string    `gorm:"size:32;not null;index;check:chk_projection_artifacts_state,state IN ('staged','ready','failed')"`
	SafeError       string    `gorm:"size:128"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
	ReadyAt         *time.Time
	Plugin          Plugin `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (ProjectionArtifact) TableName() string { return "projection_artifacts" }

func (artifact *ProjectionArtifact) BeforeUpdate(tx *gorm.DB) error {
	for _, field := range []string{
		"ID", "Kind", "PluginID", "Tag", "SourceObjectID", "SourceCommitSHA", "SourceTreeSHA",
		"RevisionID", "ContentDigest", "DistributionSHA", "StorageKey", "CreatedAt",
	} {
		if tx.Statement.Changed(field) {
			return errors.New("projection artifact source and bytes are immutable")
		}
	}
	return nil
}

type RevisionProjectionPointer struct {
	ID         string              `gorm:"type:char(36);primaryKey"`
	RevisionID string              `gorm:"type:char(36);not null;uniqueIndex:uidx_revision_plugin_pointer"`
	PluginID   string              `gorm:"type:char(36);not null;uniqueIndex:uidx_revision_plugin_pointer"`
	Tag        string              `gorm:"size:255;not null;uniqueIndex:uidx_revision_plugin_pointer"`
	ArtifactID *string             `gorm:"type:char(36);index"`
	Generation uint64              `gorm:"not null"`
	Available  bool                `gorm:"not null;index"`
	UpdatedAt  time.Time           `gorm:"not null"`
	Artifact   *ProjectionArtifact `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Plugin     Plugin              `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (RevisionProjectionPointer) TableName() string { return "revision_projection_pointers" }

type RevisionProjectionTransition struct {
	ID                 string    `gorm:"type:char(36);primaryKey"`
	IntentID           string    `gorm:"type:char(36);not null;uniqueIndex:uidx_intent_revision_transition"`
	RevisionID         string    `gorm:"type:char(36);not null;uniqueIndex:uidx_intent_revision_transition"`
	PointerID          string    `gorm:"type:char(36);not null;index"`
	ExpectedGeneration uint64    `gorm:"not null"`
	CurrentArtifactID  *string   `gorm:"type:char(36)"`
	StagedArtifactID   *string   `gorm:"type:char(36)"`
	State              string    `gorm:"size:32;not null;index;check:chk_revision_projection_transitions_state,state IN ('prepared','pending','completed','aborted','manual_required')"`
	SafeError          string    `gorm:"size:128"`
	CreatedAt          time.Time `gorm:"not null"`
	UpdatedAt          time.Time `gorm:"not null"`
	CompletedAt        *time.Time
	Intent             ReceiveIntent             `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Pointer            RevisionProjectionPointer `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	StagedArtifact     *ProjectionArtifact       `gorm:"foreignKey:StagedArtifactID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (RevisionProjectionTransition) TableName() string { return "revision_projection_transitions" }

type ProjectionGCJob struct {
	ID            string     `gorm:"type:char(36);primaryKey"`
	ArtifactID    string     `gorm:"type:char(36);not null;uniqueIndex"`
	State         string     `gorm:"size:32;not null;index;check:chk_projection_gc_jobs_state,state IN ('pending','running','completed','manual_required')"`
	Attempts      uint       `gorm:"not null"`
	SafeError     string     `gorm:"size:128"`
	NextAttemptAt *time.Time `gorm:"index"`
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
	CompletedAt   *time.Time
	Artifact      ProjectionArtifact `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (ProjectionGCJob) TableName() string { return "projection_gc_jobs" }

func (version *PluginVersion) BeforeUpdate(tx *gorm.DB) error {
	for _, field := range []string{"ID", "PluginID", "Tag", "CreatedAt", "PublishedAt"} {
		if tx.Statement.Changed(field) {
			return errors.New("plugin version identity is immutable")
		}
	}
	return nil
}
