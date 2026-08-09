package distribution

import (
	"context"
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
		Where("id = ? AND status = ? AND revoked_at IS NULL", id.String(), StatusActive).
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
	var distribution MarketplaceDistribution
	err := repository.db.WithContext(ctx).
		Where(identityQuery+" AND status = ? AND revoked_at IS NULL", identity, StatusActive).
		Take(&distribution).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MarketplaceSnapshot{}, distributionservice.ErrNotFound
	}
	if err != nil {
		return MarketplaceSnapshot{}, fmt.Errorf("resolve marketplace distribution: %w", err)
	}
	if distribution.CurrentProjectionID == nil || *distribution.CurrentProjectionID == "" {
		return MarketplaceSnapshot{}, distributionservice.ErrUnavailable
	}

	var projection MarketplaceDistributionProjection
	err = repository.db.WithContext(ctx).
		Where("id = ? AND marketplace_distribution_id = ? AND status = ?", *distribution.CurrentProjectionID, distribution.ID, StatusActive).
		Take(&projection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MarketplaceSnapshot{}, distributionservice.ErrUnavailable
	}
	if err != nil {
		return MarketplaceSnapshot{}, fmt.Errorf("resolve marketplace projection: %w", err)
	}

	var revision MarketplaceRevision
	err = repository.db.WithContext(ctx).
		Where("id = ? AND template_id = ? AND status = ?", projection.RevisionID, distribution.TemplateID, StatusActive).
		Take(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MarketplaceSnapshot{}, distributionservice.ErrUnavailable
	}
	if err != nil {
		return MarketplaceSnapshot{}, fmt.Errorf("resolve marketplace revision: %w", err)
	}
	revision.ContentJSON = append([]byte(nil), revision.ContentJSON...)
	return MarketplaceSnapshot{Distribution: distribution, Projection: projection, Revision: revision}, nil
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
