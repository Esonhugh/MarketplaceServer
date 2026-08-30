package distribution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (repository *GORMRepository) LoadPublicationInput(ctx context.Context, templateID uuid.UUID, versionIDs []uuid.UUID) (PublicationInput, error) {
	var template MarketplaceTemplate
	if err := repository.db.WithContext(ctx).Where("id = ? AND status = ?", templateID.String(), StatusActive).Take(&template).Error; err != nil {
		return PublicationInput{}, mapPublicationLookupError("load marketplace template", err)
	}
	var namespace identitymodel.Namespace
	if err := repository.db.WithContext(ctx).Where("id = ?", template.NamespaceID).Take(&namespace).Error; err != nil {
		return PublicationInput{}, mapPublicationLookupError("load marketplace namespace", err)
	}
	ids := make([]string, len(versionIDs))
	for index, id := range versionIDs {
		ids[index] = id.String()
	}
	type publicationRow struct {
		VersionID           string
		PluginID            string
		Tag                 string
		CommitSHA           *string
		ManifestDigest      *string
		ManifestSnapshot    []byte
		PublishedAt         time.Time
		VersionUpdatedAt    time.Time
		VersionCreatedAt    time.Time
		Slug                string
		Description         string
		Visibility          string
		PluginStatus        string
		ArchivedFrom        *string
		DefaultVersionTag   *string
		PluginCreatedAt     time.Time
		PluginUpdatedAt     time.Time
		StorageKey          string
		RepositoryStatus    string
		RepositoryCreatedAt time.Time
		RepositoryUpdatedAt time.Time
	}
	var rows []publicationRow
	if err := repository.db.WithContext(ctx).
		Table("plugin_versions AS v").
		Select(`v.id AS version_id, v.plugin_id, v.tag, v.commit_sha, v.manifest_digest, v.manifest_snapshot,
			v.published_at, v.updated_at AS version_updated_at, v.created_at AS version_created_at,
			p.slug, p.description, p.visibility, p.status AS plugin_status, p.archived_from, p.default_version_tag,
			p.created_at AS plugin_created_at, p.updated_at AS plugin_updated_at,
			r.storage_key, r.status AS repository_status, r.created_at AS repository_created_at, r.updated_at AS repository_updated_at`).
		Joins("JOIN plugins AS p ON p.id = v.plugin_id AND p.namespace_id = ?", template.NamespaceID).
		Joins("JOIN repositories AS r ON r.id = p.id").
		Where("v.id IN ? AND v.status = ? AND p.status = ? AND r.status IN ?", ids, plugindomain.VersionStatusAvailable, plugindomain.PluginStatusActive, []string{RepositoryStatusReady, RepositoryStatusReadOnly}).
		Find(&rows).Error; err != nil {
		return PublicationInput{}, fmt.Errorf("load scoped plugin versions: %w", err)
	}
	if len(rows) != len(ids) {
		return PublicationInput{}, distributionservice.ErrNotFound
	}
	result := PublicationInput{Template: template, Namespace: namespace, Versions: make([]PublicationVersion, 0, len(rows))}
	for _, row := range rows {
		result.Versions = append(result.Versions, PublicationVersion{
			Version:    PluginVersion{ID: row.VersionID, PluginID: row.PluginID, Tag: row.Tag, Status: plugindomain.VersionStatusAvailable, CommitSHA: row.CommitSHA, ManifestDigest: row.ManifestDigest, ManifestSnapshot: append([]byte(nil), row.ManifestSnapshot...), PublishedAt: row.PublishedAt, UpdatedAt: row.VersionUpdatedAt, CreatedAt: row.VersionCreatedAt},
			Plugin:     Plugin{ID: row.PluginID, NamespaceID: template.NamespaceID, Slug: row.Slug, Description: row.Description, Visibility: row.Visibility, Status: row.PluginStatus, ArchivedFrom: row.ArchivedFrom, DefaultVersionTag: row.DefaultVersionTag, CreatedAt: row.PluginCreatedAt, UpdatedAt: row.PluginUpdatedAt},
			Repository: Repository{ID: row.PluginID, StorageKey: row.StorageKey, Status: row.RepositoryStatus, CreatedAt: row.RepositoryCreatedAt, UpdatedAt: row.RepositoryUpdatedAt},
		})
	}
	return result, nil
}

func (repository *GORMRepository) FindPluginDistribution(ctx context.Context, templateID, pluginID uuid.UUID, tag string) (PluginDistribution, error) {
	var record PluginDistribution
	err := repository.db.WithContext(ctx).
		Where("template_id = ? AND plugin_id = ? AND plugin_tag = ? AND status = ? AND revoked_at IS NULL", templateID.String(), pluginID.String(), tag, StatusActive).
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

func (repository *GORMRepository) CreateMarketplaceRevision(ctx context.Context, revision *MarketplaceRevision, items []MarketplaceRevisionItem, projection *MarketplaceDistributionProjection, artifacts []plugindomain.ProjectionArtifact, pointers []plugindomain.RevisionProjectionPointer) error {
	if err := repository.db.WithContext(ctx).Create(revision).Error; err != nil {
		return err
	}
	if len(items) > 0 {
		if err := repository.db.WithContext(ctx).Create(&items).Error; err != nil {
			return err
		}
	}
	if len(artifacts) > 0 {
		if err := repository.db.WithContext(ctx).Create(&artifacts).Error; err != nil {
			return err
		}
	}
	if len(pointers) > 0 {
		if err := repository.db.WithContext(ctx).Create(&pointers).Error; err != nil {
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
