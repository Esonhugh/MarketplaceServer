package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/marketplacejson"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PublishCommand struct {
	TemplateID       uuid.UUID
	PluginVersionIDs []uuid.UUID
	Origin           string
	PublishedAt      time.Time
}

type PublishResult struct {
	DistributionID uuid.UUID
	PublicKey      distributionservice.MarketplacePublicKey
	RevisionID     uuid.UUID
	ProjectionID   uuid.UUID
	ContentJSON    []byte
	ContentDigest  string
}

const marketplacePublicKeyCreateAttempts = 3

type PublicationService struct {
	repository PublicationRepository
	builder    gitservice.ProjectionBuilder
}

func NewPublicationService(repository PublicationRepository, builder gitservice.ProjectionBuilder) *PublicationService {
	return &PublicationService{repository: repository, builder: builder}
}

func (service *PublicationService) Publish(ctx context.Context, command PublishCommand) (PublishResult, error) {
	origin, err := validateOrigin(command.Origin)
	if err != nil {
		return PublishResult{}, err
	}
	if command.TemplateID == uuid.Nil || len(command.PluginVersionIDs) == 0 || command.PublishedAt.IsZero() {
		return PublishResult{}, errors.New("template, plugin versions, and publication time are required")
	}
	input, err := service.repository.LoadPublicationInput(ctx, command.TemplateID, command.PluginVersionIDs)
	if err != nil {
		return PublishResult{}, err
	}
	sort.Slice(input.Versions, func(i, j int) bool {
		if input.Versions[i].Plugin.Slug == input.Versions[j].Plugin.Slug {
			return strings.TrimPrefix(input.Versions[i].Version.Tag, "v") < strings.TrimPrefix(input.Versions[j].Version.Tag, "v")
		}
		return input.Versions[i].Plugin.Slug < input.Versions[j].Plugin.Slug
	})

	builtPluginProjections := make([]gitservice.ImmutableProjection, 0, len(input.Versions))
	newPluginDistributions := make([]PluginDistribution, 0, len(input.Versions))
	plugins := make([]marketplacejson.Plugin, 0, len(input.Versions))
	items := make([]MarketplaceRevisionItem, 0, len(input.Versions))
	for position, selected := range input.Versions {
		distribution, findErr := service.repository.FindPluginDistribution(ctx, command.TemplateID, mustUUID(selected.Plugin.ID), selected.Version.Tag)
		if findErr != nil && !errors.Is(findErr, distributionservice.ErrNotFound) && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return PublishResult{}, findErr
		}
		if findErr != nil {
			projectionID := uuid.New()
			built, buildErr := service.builder.BuildPluginProjection(ctx, gitservice.BuildPluginProjectionCommand{
				ProjectionID: projectionID, RepositoryID: selected.Repository.ID,
				TagName: selected.Version.Tag, PublishedAt: command.PublishedAt,
			})
			if buildErr != nil {
				service.compensate(ctx, builtPluginProjections)
				return PublishResult{}, buildErr
			}
			builtPluginProjections = append(builtPluginProjections, built.Projection)
			distribution = PluginDistribution{
				ID: projectionID.String(), TemplateID: command.TemplateID.String(), PluginID: selected.Plugin.ID,
				PluginTag: selected.Version.Tag, RepositoryID: selected.Repository.ID, TagName: built.TagName,
				SourceTagType: string(built.SourceTagType), SourceTagObjectID: built.SourceTagObjectID,
				SourceCommitSHA: built.SourceCommitSHA, SourceTreeSHA: built.SourceTreeSHA,
				DistributionSHA: built.DistributionSHA, StorageKey: built.Projection.StorageKey,
				ContentDigest: built.ContentDigest, Status: StatusActive,
			}
			newPluginDistributions = append(newPluginDistributions, distribution)
		}
		distributionID := mustUUID(distribution.ID)
		sourceURL := origin + "/distribution/plugins/" + distributionID.String() + ".git"
		plugins = append(plugins, marketplacejson.Plugin{
			Name: selected.Plugin.Slug, Description: selected.Plugin.Description, Version: strings.TrimPrefix(selected.Version.Tag, "v"),
			Source: marketplacejson.URLSource{Source: marketplacejson.URLSourceType, URL: sourceURL, Ref: distribution.TagName, SHA: distribution.DistributionSHA},
		})
		items = append(items, MarketplaceRevisionItem{
			ID: uuid.NewString(), PluginID: selected.Plugin.ID, PluginTag: selected.Version.Tag,
			PluginDistributionID: distribution.ID, SourceURL: sourceURL,
			DistributionSHA: distribution.DistributionSHA, Position: position,
		})
	}

	contentJSON, err := marketplacejson.Marshal(marketplacejson.Marketplace{
		Name: input.Template.Slug, Description: input.Template.Description,
		Owner: marketplacejson.Owner{Name: input.Namespace.DisplayName}, Plugins: plugins,
	})
	if err != nil {
		service.compensate(ctx, builtPluginProjections)
		return PublishResult{}, err
	}
	contentSum := sha256.Sum256(contentJSON)
	contentDigest := hex.EncodeToString(contentSum[:])
	distribution, err := service.repository.FindMarketplaceDistribution(ctx, command.TemplateID)
	if err != nil && !errors.Is(err, distributionservice.ErrNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
		service.compensate(ctx, builtPluginProjections)
		return PublishResult{}, err
	}
	if err != nil {
		distribution, err = service.createMarketplaceDistribution(ctx, command.TemplateID, input.Template.Name)
		if err != nil {
			service.compensate(ctx, builtPluginProjections)
			return PublishResult{}, err
		}
	}
	publicKey, err := distributionservice.ParseMarketplacePublicKey(distribution.PublicKey)
	if err != nil {
		service.compensate(ctx, builtPluginProjections)
		return PublishResult{}, distributionservice.ErrUnavailable
	}
	revisionNumber, err := service.repository.NextRevision(ctx, command.TemplateID)
	if err != nil {
		service.compensate(ctx, builtPluginProjections)
		return PublishResult{}, err
	}
	revisionID := uuid.New()
	projectionID := uuid.New()
	marketplaceProjection, err := service.builder.BuildMarketplaceProjection(ctx, gitservice.BuildMarketplaceProjectionCommand{
		ProjectionID: projectionID, ContentJSON: contentJSON, PublishedAt: command.PublishedAt,
	})
	if err != nil {
		service.compensate(ctx, builtPluginProjections)
		return PublishResult{}, err
	}
	allBuilt := append(builtPluginProjections, marketplaceProjection.Projection)
	artifacts := make([]plugindomain.ProjectionArtifact, 0, len(items))
	pointers := make([]plugindomain.RevisionProjectionPointer, 0, len(items))
	for i := range items {
		items[i].RevisionID = revisionID.String()
		distributionID := items[i].PluginDistributionID
		var pluginDistribution PluginDistribution
		for _, candidate := range newPluginDistributions {
			if candidate.ID == distributionID {
				pluginDistribution = candidate
				break
			}
		}
		if pluginDistribution.ID == "" {
			pluginDistribution, err = service.repository.FindPluginDistribution(ctx, command.TemplateID, mustUUID(items[i].PluginID), items[i].PluginTag)
			if err != nil {
				service.compensate(ctx, allBuilt)
				return PublishResult{}, err
			}
		}
		artifactID := uuid.NewString()
		revisionIDString := revisionID.String()
		artifacts = append(artifacts, plugindomain.ProjectionArtifact{
			ID: artifactID, Kind: plugindomain.ArtifactKindPlugin, PluginID: items[i].PluginID, Tag: items[i].PluginTag,
			SourceObjectID: pluginDistribution.SourceTagObjectID, SourceCommitSHA: pluginDistribution.SourceCommitSHA,
			SourceTreeSHA: pluginDistribution.SourceTreeSHA, RevisionID: &revisionIDString,
			ContentDigest: pluginDistribution.ContentDigest, DistributionSHA: pluginDistribution.DistributionSHA,
			StorageKey: pluginDistribution.StorageKey, State: plugindomain.ArtifactStateReady,
			CreatedAt: command.PublishedAt.UTC(), UpdatedAt: command.PublishedAt.UTC(), ReadyAt: timePointer(command.PublishedAt.UTC()),
		})
		pointers = append(pointers, plugindomain.RevisionProjectionPointer{
			ID: uuid.NewString(), RevisionID: revisionIDString, PluginID: items[i].PluginID, Tag: items[i].PluginTag,
			ArtifactID: &artifactID, Generation: 1, Available: true, UpdatedAt: command.PublishedAt.UTC(),
		})
	}
	revision := MarketplaceRevision{
		ID: revisionID.String(), TemplateID: command.TemplateID.String(), Revision: revisionNumber,
		ContentJSON: contentJSON, ContentDigest: contentDigest, Status: StatusActive, PublishedAt: command.PublishedAt.UTC(),
	}
	projection := MarketplaceDistributionProjection{
		ID: projectionID.String(), MarketplaceDistributionID: distribution.ID, RevisionID: revisionID.String(),
		StorageKey: marketplaceProjection.Projection.StorageKey, DistributionSHA: marketplaceProjection.DistributionSHA,
		ContentDigest: marketplaceProjection.ContentDigest, Status: StatusActive,
	}
	err = service.repository.Transaction(ctx, func(store RepositoryStore) error {
		publicationStore, ok := store.(PublicationRepository)
		if !ok {
			return errors.New("transaction does not support publication")
		}
		for index := range newPluginDistributions {
			if err := publicationStore.CreatePluginDistribution(ctx, &newPluginDistributions[index]); err != nil {
				return err
			}
		}
		if err := publicationStore.CreateMarketplaceRevision(ctx, &revision, items, &projection, artifacts, pointers); err != nil {
			return err
		}
		return publicationStore.SwitchMarketplacePointers(ctx, command.TemplateID, revisionID, mustUUID(distribution.ID), projectionID)
	})
	if err != nil {
		service.compensate(ctx, allBuilt)
		return PublishResult{}, err
	}
	return PublishResult{
		DistributionID: mustUUID(distribution.ID), PublicKey: publicKey, RevisionID: revisionID, ProjectionID: projectionID,
		ContentJSON: append([]byte(nil), contentJSON...), ContentDigest: contentDigest,
	}, nil
}

func (service *PublicationService) createMarketplaceDistribution(ctx context.Context, templateID uuid.UUID, marketplaceName string) (MarketplaceDistribution, error) {
	for attempt := 0; attempt < marketplacePublicKeyCreateAttempts; attempt++ {
		publicKey, err := distributionservice.NewMarketplacePublicKey(marketplaceName)
		if err != nil {
			return MarketplaceDistribution{}, err
		}
		distribution := MarketplaceDistribution{
			ID: uuid.NewString(), TemplateID: templateID.String(), PublicKey: publicKey.String(), Status: StatusActive,
		}
		if err := service.repository.CreateMarketplaceDistribution(ctx, &distribution); err != nil {
			if errors.Is(err, ErrMarketplacePublicKeyConflict) {
				continue
			}
			return MarketplaceDistribution{}, err
		}
		return distribution, nil
	}
	return MarketplaceDistribution{}, ErrMarketplacePublicKeyConflict
}

func (service *PublicationService) Rollback(ctx context.Context, distributionID, revisionID uuid.UUID) error {
	projection, err := service.repository.FindMarketplaceProjectionByRevision(ctx, distributionID, revisionID)
	if err != nil {
		return err
	}
	immutable := gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindMarketplace, StorageKey: projection.StorageKey}
	if err := service.builder.VerifyProjection(ctx, immutable, projection.ContentDigest); err != nil {
		return fmt.Errorf("verify rollback projection: %w", err)
	}
	distribution, err := service.repository.FindActiveMarketplaceByID(ctx, distributionID)
	if err != nil {
		return err
	}
	templateID := mustUUID(distribution.Distribution.TemplateID)
	return service.repository.Transaction(ctx, func(store RepositoryStore) error {
		publicationStore, ok := store.(PublicationRepository)
		if !ok {
			return errors.New("transaction does not support publication")
		}
		return publicationStore.SwitchMarketplacePointers(ctx, templateID, revisionID, distributionID, mustUUID(projection.ID))
	})
}

func validateOrigin(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must be an HTTP or HTTPS scheme and host only")
	}
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func mustUUID(raw string) uuid.UUID {
	return uuid.MustParse(raw)
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func (service *PublicationService) compensate(ctx context.Context, projections []gitservice.ImmutableProjection) {
	for i := len(projections) - 1; i >= 0; i-- {
		_ = service.builder.RemoveProjection(ctx, projections[i])
	}
}
