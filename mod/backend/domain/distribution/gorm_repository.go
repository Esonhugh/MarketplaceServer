package distribution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	err := repository.db.WithContext(ctx).
		Table("plugin_distributions AS d").
		Select("d.*").
		Joins("JOIN marketplace_templates AS t ON t.id = d.template_id").
		Joins("JOIN plugins AS p ON p.id = d.plugin_id").
		Joins("JOIN plugin_versions AS v ON v.id = d.plugin_version_id AND v.plugin_id = p.id").
		Joins("JOIN repositories AS r ON r.id = d.repository_id AND r.id = p.repository_id AND r.namespace_id = p.namespace_id").
		Where(`d.id = ? AND d.status = ? AND d.revoked_at IS NULL
			AND t.status = ? AND t.visibility = ?
			AND p.status = ? AND p.visibility = ?
			AND v.status = ?
			AND r.status IN ? AND r.visibility = ?`,
			id.String(), StatusActive,
			StatusActive, "public",
			StatusActive, "public",
			StatusActive,
			[]string{RepositoryStatusReady, RepositoryStatusReadOnly}, "public").
		Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginDistribution{}, distributionservice.ErrNotFound
	}
	if err != nil {
		return PluginDistribution{}, fmt.Errorf("resolve plugin distribution: %w", err)
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
