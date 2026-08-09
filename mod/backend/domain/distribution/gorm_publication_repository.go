package distribution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (repository *GORMRepository) LoadPublicationInput(ctx context.Context, templateID uuid.UUID, versionIDs []uuid.UUID) (PublicationInput, error) {
	var template MarketplaceTemplate
	if err := repository.db.WithContext(ctx).Where("id = ? AND status = ?", templateID.String(), StatusActive).Take(&template).Error; err != nil {
		return PublicationInput{}, mapPublicationLookupError("load marketplace template", err)
	}
	var namespace Namespace
	if err := repository.db.WithContext(ctx).Where("id = ?", template.NamespaceID).Take(&namespace).Error; err != nil {
		return PublicationInput{}, mapPublicationLookupError("load marketplace namespace", err)
	}
	ids := make([]string, len(versionIDs))
	for index, id := range versionIDs {
		ids[index] = id.String()
	}
	var versions []PluginVersion
	if err := repository.db.WithContext(ctx).Where("id IN ? AND status = ?", ids, StatusActive).Find(&versions).Error; err != nil {
		return PublicationInput{}, fmt.Errorf("load plugin versions: %w", err)
	}
	if len(versions) != len(ids) {
		return PublicationInput{}, distributionservice.ErrNotFound
	}
	result := PublicationInput{Template: template, Namespace: namespace, Versions: make([]PublicationVersion, 0, len(versions))}
	for _, version := range versions {
		var plugin Plugin
		if err := repository.db.WithContext(ctx).Where("id = ? AND status = ?", version.PluginID, StatusActive).Take(&plugin).Error; err != nil {
			return PublicationInput{}, mapPublicationLookupError("load plugin", err)
		}
		var sourceRepository Repository
		if err := repository.db.WithContext(ctx).Where("id = ? AND status = ?", plugin.RepositoryID, StatusActive).Take(&sourceRepository).Error; err != nil {
			return PublicationInput{}, mapPublicationLookupError("load plugin repository", err)
		}
		result.Versions = append(result.Versions, PublicationVersion{Version: version, Plugin: plugin, Repository: sourceRepository})
	}
	return result, nil
}

func (repository *GORMRepository) FindPluginDistribution(ctx context.Context, templateID, pluginID, versionID uuid.UUID) (PluginDistribution, error) {
	var record PluginDistribution
	err := repository.db.WithContext(ctx).
		Where("template_id = ? AND plugin_id = ? AND plugin_version_id = ? AND status = ? AND revoked_at IS NULL", templateID.String(), pluginID.String(), versionID.String(), StatusActive).
		Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginDistribution{}, distributionservice.ErrNotFound
	}
	if err != nil {
		return PluginDistribution{}, fmt.Errorf("find plugin distribution: %w", err)
	}
	return record, nil
}

func (repository *GORMRepository) FindMarketplaceDistribution(ctx context.Context, templateID uuid.UUID) (MarketplaceDistribution, error) {
	var record MarketplaceDistribution
	err := repository.db.WithContext(ctx).Where("template_id = ? AND status = ? AND revoked_at IS NULL", templateID.String(), StatusActive).Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MarketplaceDistribution{}, distributionservice.ErrNotFound
	}
	if err != nil {
		return MarketplaceDistribution{}, fmt.Errorf("find marketplace distribution: %w", err)
	}
	return record, nil
}

func (repository *GORMRepository) NextRevision(ctx context.Context, templateID uuid.UUID) (uint64, error) {
	var maximum struct{ Revision uint64 }
	if err := repository.db.WithContext(ctx).Model(&MarketplaceRevision{}).
		Select("COALESCE(MAX(revision), 0) AS revision").Where("template_id = ?", templateID.String()).Scan(&maximum).Error; err != nil {
		return 0, fmt.Errorf("select next marketplace revision: %w", err)
	}
	return maximum.Revision + 1, nil
}

func (repository *GORMRepository) CreatePluginDistribution(ctx context.Context, record *PluginDistribution) error {
	return repository.db.WithContext(ctx).Create(record).Error
}

func (repository *GORMRepository) CreateMarketplaceDistribution(ctx context.Context, record *MarketplaceDistribution) error {
	err := repository.db.WithContext(ctx).Create(record).Error
	if isMarketplacePublicKeyConflict(err) {
		return ErrMarketplacePublicKeyConflict
	}
	return err
}

func isMarketplacePublicKeyConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "duplicate") && !strings.Contains(message, "unique") {
		return false
	}
	return strings.Contains(message, "uidx_marketplace_distribution_public_key") ||
		strings.Contains(message, "marketplace_distributions_public_key_key") ||
		strings.Contains(message, "marketplace_distributions.public_key")
}

func (repository *GORMRepository) CreateMarketplaceRevision(ctx context.Context, revision *MarketplaceRevision, items []MarketplaceRevisionItem, projection *MarketplaceDistributionProjection) error {
	if err := repository.db.WithContext(ctx).Create(revision).Error; err != nil {
		return err
	}
	if len(items) > 0 {
		if err := repository.db.WithContext(ctx).Create(&items).Error; err != nil {
			return err
		}
	}
	return repository.db.WithContext(ctx).Create(projection).Error
}

func (repository *GORMRepository) SwitchMarketplacePointers(ctx context.Context, templateID, revisionID, distributionID, projectionID uuid.UUID) error {
	result := repository.db.WithContext(ctx).Model(&MarketplaceTemplate{}).
		Where("id = ?", templateID.String()).Update("published_revision_id", revisionID.String())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return distributionservice.ErrUnavailable
	}
	result = repository.db.WithContext(ctx).Model(&MarketplaceDistribution{}).
		Where("id = ? AND template_id = ?", distributionID.String(), templateID.String()).
		Update("current_projection_id", projectionID.String())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return distributionservice.ErrUnavailable
	}
	return nil
}

func (repository *GORMRepository) FindMarketplaceProjectionByRevision(ctx context.Context, distributionID, revisionID uuid.UUID) (MarketplaceDistributionProjection, error) {
	var projection MarketplaceDistributionProjection
	err := repository.db.WithContext(ctx).
		Where("marketplace_distribution_id = ? AND revision_id = ? AND status = ?", distributionID.String(), revisionID.String(), StatusActive).
		Take(&projection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MarketplaceDistributionProjection{}, distributionservice.ErrNotFound
	}
	if err != nil {
		return MarketplaceDistributionProjection{}, fmt.Errorf("find marketplace projection: %w", err)
	}
	return projection, nil
}

func mapPublicationLookupError(operation string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return distributionservice.ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ PublicationRepository = (*GORMRepository)(nil)
