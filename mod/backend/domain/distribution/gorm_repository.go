package distribution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) (*GORMRepository, error) {
	if db == nil {
		return nil, errors.New("distribution repository requires database")
	}
	return &GORMRepository{db: db}, nil
}

func (repository *GORMRepository) FindActivePlugin(ctx context.Context, id uuid.UUID) (PluginDistribution, error) {
	var record PluginDistribution
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var disclosed struct{ ID string }
		err := tx.
			Table("plugin_distributions AS d").
			Select("d.id").
			Joins("JOIN marketplace_templates AS t ON t.id = d.template_id").
			Joins("JOIN plugins AS p ON p.id = d.plugin_id AND p.namespace_id = t.namespace_id").
			Where(`d.id = ? AND d.status = ? AND d.revoked_at IS NULL
				AND t.status = ? AND t.visibility = ?
				AND p.status IN ? AND p.visibility = ?`,
				id.String(), StatusActive,
				StatusActive, plugindomain.VisibilityPublic,
				[]string{plugindomain.PluginStatusActive, plugindomain.PluginStatusArchived}, plugindomain.VisibilityPublic).
			Take(&disclosed).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return distributionservice.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve plugin distribution identity: %w", err)
		}

		err = tx.
			Table("plugin_distributions AS d").
			Select("d.id, d.template_id, d.plugin_id, d.plugin_tag, d.repository_id, d.tag_name, d.source_tag_type, d.source_tag_object_id, d.source_commit_sha, d.source_tree_sha, d.distribution_sha, a.storage_key, d.content_digest, d.status, d.created_at, d.updated_at, d.revoked_at").
			Joins("JOIN marketplace_templates AS t ON t.id = d.template_id").
			Joins("JOIN marketplace_revisions AS mr ON mr.id = t.published_revision_id AND mr.template_id = t.id AND mr.status = ?", StatusActive).
			Joins("JOIN marketplace_revision_items AS i ON i.revision_id = mr.id AND i.plugin_distribution_id = d.id AND i.plugin_id = d.plugin_id AND i.plugin_tag = d.plugin_tag").
			Joins("JOIN plugins AS p ON p.id = d.plugin_id AND p.namespace_id = t.namespace_id").
			Joins("JOIN plugin_versions AS v ON v.plugin_id = p.id AND v.tag = i.plugin_tag AND v.status = ?", plugindomain.VersionStatusAvailable).
			Joins("JOIN repositories AS r ON r.id = p.id AND r.id = d.repository_id AND r.status IN ?", []string{RepositoryStatusReady, RepositoryStatusReadOnly}).
			Joins("JOIN revision_projection_pointers AS ptr ON ptr.revision_id = i.revision_id AND ptr.plugin_id = i.plugin_id AND ptr.tag = i.plugin_tag AND ptr.available = ? AND ptr.artifact_id IS NOT NULL", true).
			Joins("JOIN projection_artifacts AS a ON a.id = ptr.artifact_id AND a.kind = ? AND a.state = ? AND a.storage_key <> '' AND a.plugin_id = ptr.plugin_id AND a.tag = ptr.tag AND a.revision_id = ptr.revision_id AND a.source_commit_sha = v.commit_sha", plugindomain.ArtifactKindPlugin, plugindomain.ArtifactStateReady).
			Where(`d.id = ? AND d.status = ? AND d.revoked_at IS NULL
				AND t.status = ? AND t.visibility = ?
				AND p.status IN ? AND p.visibility = ?`,
				id.String(), StatusActive,
				StatusActive, plugindomain.VisibilityPublic,
				[]string{plugindomain.PluginStatusActive, plugindomain.PluginStatusArchived}, plugindomain.VisibilityPublic).
			Take(&record).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var deletedVersion int64
			if countErr := tx.Table("plugin_distributions AS d").
				Joins("JOIN marketplace_templates AS t ON t.id = d.template_id").
				Joins("JOIN marketplace_revision_items AS i ON i.plugin_distribution_id = d.id AND i.plugin_id = d.plugin_id AND i.plugin_tag = d.plugin_tag").
				Joins("JOIN plugin_versions AS v ON v.plugin_id = i.plugin_id AND v.tag = i.plugin_tag AND v.status = ?", plugindomain.VersionStatusDeleted).
				Where("d.id = ? AND d.status = ? AND d.revoked_at IS NULL AND t.status = ? AND t.visibility = ?", id.String(), StatusActive, StatusActive, plugindomain.VisibilityPublic).
				Count(&deletedVersion).Error; countErr != nil {
				return fmt.Errorf("classify deleted plugin distribution: %w", countErr)
			}
			if deletedVersion != 0 {
				return distributionservice.ErrGone
			}
			return distributionservice.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("resolve plugin projection: %w", err)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return PluginDistribution{}, err
	}
	return record, nil
}

func (repository *GORMRepository) FindActiveMarketplace(ctx context.Context, publicKey distributionservice.MarketplacePublicKey) (MarketplaceSnapshot, error) {
	return repository.findActiveMarketplace(ctx, "public_key = ?", publicKey.String())
}

func (repository *GORMRepository) FindActiveMarketplaceByID(ctx context.Context, id uuid.UUID) (MarketplaceSnapshot, error) {
	return repository.findActiveMarketplace(ctx, "id = ?", id.String())
}

func (repository *GORMRepository) findActiveMarketplace(ctx context.Context, identityQuery string, identity any) (MarketplaceSnapshot, error) {
	var snapshot MarketplaceSnapshot
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var distribution MarketplaceDistribution
		err := tx.
			Table("marketplace_distributions AS d").
			Select("d.*").
			Joins("JOIN marketplace_templates AS t ON t.id = d.template_id").
			Where("d."+identityQuery+" AND d.status = ? AND d.revoked_at IS NULL AND t.status = ? AND t.visibility = ?", identity, StatusActive, StatusActive, "public").
			Take(&distribution).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return distributionservice.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("resolve marketplace distribution: %w", err)
		}
		if distribution.CurrentProjectionID == nil || *distribution.CurrentProjectionID == "" {
			return distributionservice.ErrUnavailable
		}

		var projection MarketplaceDistributionProjection
		err = tx.
			Where("id = ? AND marketplace_distribution_id = ? AND status = ?", *distribution.CurrentProjectionID, distribution.ID, StatusActive).
			Take(&projection).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return distributionservice.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("resolve marketplace projection: %w", err)
		}

		var revision MarketplaceRevision
		err = tx.
			Where("id = ? AND template_id = ? AND status = ?", projection.RevisionID, distribution.TemplateID, StatusActive).
			Take(&revision).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return distributionservice.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("resolve marketplace revision: %w", err)
		}
		if projection.ContentDigest == "" || projection.ContentDigest != revision.ContentDigest {
			return distributionservice.ErrUnavailable
		}
		revision.ContentJSON = append([]byte(nil), revision.ContentJSON...)
		snapshot = MarketplaceSnapshot{Distribution: distribution, Projection: projection, Revision: revision}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return MarketplaceSnapshot{}, err
	}
	return snapshot, nil
}

func (repository *GORMRepository) Transaction(ctx context.Context, fn func(RepositoryStore) error) error {
	if fn == nil {
		return errors.New("distribution transaction callback is required")
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&GORMRepository{db: tx})
	})
}

var _ RepositoryStore = (*GORMRepository)(nil)
