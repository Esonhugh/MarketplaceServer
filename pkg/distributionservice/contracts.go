package distributionservice

import (
	"context"
	"errors"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

var (
	ErrNotFound    = errors.New("distribution not found")
	ErrUnavailable = errors.New("distribution unavailable")
)

type PluginGrant struct {
	DistributionID uuid.UUID
	Projection     gitservice.ImmutableProjection
}

type MarketplaceGrant struct {
	DistributionID uuid.UUID
	PublicKey      MarketplacePublicKey
	RevisionID     uuid.UUID
	Projection     gitservice.ImmutableProjection
	ContentJSON    []byte
	ContentDigest  string
}

// Resolver snapshots the active immutable projection once for each request.
// The public slice returns grants only for active public distributions.
type Resolver interface {
	ResolvePlugin(ctx context.Context, distributionID uuid.UUID) (PluginGrant, error)
	ResolveMarketplace(ctx context.Context, publicKey MarketplacePublicKey) (MarketplaceGrant, error)
}
