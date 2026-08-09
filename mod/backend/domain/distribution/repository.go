package distribution

import (
	"context"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/google/uuid"
)

type MarketplaceSnapshot struct {
	Distribution MarketplaceDistribution
	Projection   MarketplaceDistributionProjection
	Revision     MarketplaceRevision
}

type RepositoryStore interface {
	FindActivePlugin(ctx context.Context, id uuid.UUID) (PluginDistribution, error)
	FindActiveMarketplace(ctx context.Context, publicKey distributionservice.MarketplacePublicKey) (MarketplaceSnapshot, error)
	FindActiveMarketplaceByID(ctx context.Context, id uuid.UUID) (MarketplaceSnapshot, error)
	Transaction(ctx context.Context, fn func(RepositoryStore) error) error
}
