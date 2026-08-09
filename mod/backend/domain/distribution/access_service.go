package distribution

import (
	"context"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

type AccessService struct {
	repository RepositoryStore
}

func NewAccessService(repository RepositoryStore) *AccessService {
	return &AccessService{repository: repository}
}

func (service *AccessService) ResolvePlugin(ctx context.Context, id uuid.UUID) (distributionservice.PluginGrant, error) {
	record, err := service.repository.FindActivePlugin(ctx, id)
	if err != nil {
		return distributionservice.PluginGrant{}, err
	}
	return distributionservice.PluginGrant{
		DistributionID: id,
		Projection: gitservice.ImmutableProjection{
			Kind:       gitservice.ProjectionKindPlugin,
			StorageKey: record.StorageKey,
		},
	}, nil
}

func (service *AccessService) ResolveMarketplace(ctx context.Context, publicKey distributionservice.MarketplacePublicKey) (distributionservice.MarketplaceGrant, error) {
	if _, err := distributionservice.ParseMarketplacePublicKey(publicKey.String()); err != nil {
		return distributionservice.MarketplaceGrant{}, distributionservice.ErrNotFound
	}
	snapshot, err := service.repository.FindActiveMarketplace(ctx, publicKey)
	if err != nil {
		return distributionservice.MarketplaceGrant{}, err
	}
	distributionID, err := uuid.Parse(snapshot.Distribution.ID)
	if err != nil || snapshot.Distribution.PublicKey != publicKey.String() {
		return distributionservice.MarketplaceGrant{}, distributionservice.ErrUnavailable
	}
	revisionID, err := uuid.Parse(snapshot.Revision.ID)
	if err != nil {
		return distributionservice.MarketplaceGrant{}, distributionservice.ErrUnavailable
	}
	return distributionservice.MarketplaceGrant{
		DistributionID: distributionID,
		PublicKey:      publicKey,
		RevisionID:     revisionID,
		Projection: gitservice.ImmutableProjection{
			Kind:       gitservice.ProjectionKindMarketplace,
			StorageKey: snapshot.Projection.StorageKey,
		},
		ContentJSON:   append([]byte(nil), snapshot.Revision.ContentJSON...),
		ContentDigest: snapshot.Revision.ContentDigest,
	}, nil
}

var _ distributionservice.Resolver = (*AccessService)(nil)
