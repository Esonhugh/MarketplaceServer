package distribution

import (
	"context"
	"errors"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/google/uuid"
)

var ErrMarketplacePublicKeyConflict = errors.New("marketplace public key already exists")

type PublicationInput struct {
	Template  MarketplaceTemplate
	Namespace identitymodel.Namespace
	Versions  []PublicationVersion
}

type PublicationVersion struct {
	Version    PluginVersion
	Plugin     Plugin
	Repository Repository
}

type PublicationRepository interface {
	RepositoryStore
	LoadPublicationInput(ctx context.Context, templateID uuid.UUID, versionIDs []uuid.UUID) (PublicationInput, error)
	FindPluginDistribution(ctx context.Context, templateID, pluginID uuid.UUID, tag string) (PluginDistribution, error)
	FindMarketplaceDistribution(ctx context.Context, templateID uuid.UUID) (MarketplaceDistribution, error)
	NextRevision(ctx context.Context, templateID uuid.UUID) (uint64, error)
	CreatePluginDistribution(ctx context.Context, record *PluginDistribution) error
	CreateMarketplaceDistribution(ctx context.Context, record *MarketplaceDistribution) error
	CreateMarketplaceRevision(ctx context.Context, revision *MarketplaceRevision, items []MarketplaceRevisionItem, projection *MarketplaceDistributionProjection, artifacts []plugindomain.ProjectionArtifact, pointers []plugindomain.RevisionProjectionPointer) error
	SwitchMarketplacePointers(ctx context.Context, templateID, revisionID, distributionID, projectionID uuid.UUID) error
	FindMarketplaceProjectionByRevision(ctx context.Context, distributionID, revisionID uuid.UUID) (MarketplaceDistributionProjection, error)
}
