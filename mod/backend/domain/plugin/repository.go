package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

var (
	ErrInvalidScope        = errors.New("plugin repository requires tenant scope")
	ErrNotFound            = errors.New("plugin aggregate not found")
	ErrAlreadyExists       = errors.New("plugin aggregate already exists")
	ErrVersionNotAvailable = errors.New("plugin version is not available")
)

type Aggregate struct {
	Plugin     Plugin
	Repository Repository
}

type aggregateRecord struct {
	ID                  string
	NamespaceID         string
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

type RepositoryStore struct {
	db *gorm.DB
}

func NewRepository(db *gorm.DB) (*RepositoryStore, error) {
	if db == nil {
		return nil, errors.New("plugin repository requires database")
	}
	return &RepositoryStore{db: db}, nil
}

func (repository *RepositoryStore) Create(ctx context.Context, aggregate Aggregate) error {
	if repository == nil || repository.db == nil || strings.TrimSpace(aggregate.Plugin.NamespaceID) == "" ||
		strings.TrimSpace(aggregate.Plugin.ID) == "" || strings.TrimSpace(aggregate.Plugin.Slug) == "" ||
		aggregate.Repository.ID != aggregate.Plugin.ID || strings.TrimSpace(aggregate.Repository.StorageKey) == "" {
		return ErrInvalidScope
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&aggregate.Plugin).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrAlreadyExists
			}
			return fmt.Errorf("create scoped Plugin: %w", err)
		}
		if err := tx.Create(&aggregate.Repository).Error; err != nil {
			if isUniqueConstraintError(err) {
				return ErrAlreadyExists
			}
			return fmt.Errorf("create shared-ID Repository: %w", err)
		}
		return nil
	})
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key")
}

func SetDefaultVersion(ctx context.Context, db *gorm.DB, namespaceID, pluginID, tag string) error {
	if db == nil || strings.TrimSpace(namespaceID) == "" || strings.TrimSpace(pluginID) == "" || strings.TrimSpace(tag) == "" {
		return ErrInvalidScope
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		plugin, err := loadDefaultMutationPlugin(tx, namespaceID, pluginID)
		if err != nil {
			return fmt.Errorf("read scoped Plugin for default Version: %w", err)
		}
		if plugin.Status == PluginStatusArchived {
			return ErrVersionNotAvailable
		}
		var version PluginVersion
		if err := tx.Select("id").Where("plugin_id = ? AND tag = ? AND status = ?", pluginID, tag, VersionStatusAvailable).Take(&version).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrVersionNotAvailable
			}
			return fmt.Errorf("verify scoped default Version: %w", err)
		}
		if plugin.DefaultVersionTag != nil && *plugin.DefaultVersionTag == tag {
			return nil
		}
		result := tx.Model(&Plugin{}).
			Where("namespace_id = ? AND id = ? AND status = ?", namespaceID, pluginID, plugin.Status).
			Updates(map[string]any{"default_version_tag": tag, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return fmt.Errorf("set scoped default Version: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrVersionNotAvailable
		}
		return nil
	})
}

func loadDefaultMutationPlugin(tx *gorm.DB, namespaceID, pluginID string) (Plugin, error) {
	var plugin Plugin
	if err := tx.Select("id", "status", "default_version_tag").
		Where("namespace_id = ? AND id = ?", namespaceID, pluginID).
		Take(&plugin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Plugin{}, ErrNotFound
		}
		return Plugin{}, err
	}
	return plugin, nil
}

func (repository *RepositoryStore) List(ctx context.Context, namespaceID string, page, size int) ([]Aggregate, int64, error) {
	if strings.TrimSpace(namespaceID) == "" || page < 1 || size < 1 || size > 100 {
		return nil, 0, ErrInvalidScope
	}
	var total int64
	if err := repository.db.WithContext(ctx).Model(&Plugin{}).Where("namespace_id = ?", namespaceID).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count scoped Plugins: %w", err)
	}
	var records []aggregateRecord
	if err := repository.db.WithContext(ctx).
		Table("plugins AS p").
		Select(`p.id, p.namespace_id, p.slug, p.description, p.visibility,
			p.status AS plugin_status, p.archived_from, p.default_version_tag,
			p.created_at AS plugin_created_at, p.updated_at AS plugin_updated_at,
			r.storage_key, r.status AS repository_status,
			r.created_at AS repository_created_at, r.updated_at AS repository_updated_at`).
		Joins("JOIN repositories AS r ON r.id = p.id").
		Where("p.namespace_id = ?", namespaceID).
		Order("p.slug ASC").Order("p.id ASC").
		Offset((page - 1) * size).Limit(size).
		Scan(&records).Error; err != nil {
		return nil, 0, fmt.Errorf("list scoped Plugin aggregates: %w", err)
	}
	items := make([]Aggregate, 0, len(records))
	for _, record := range records {
		items = append(items, aggregateFromRecord(record))
	}
	return items, total, nil
}

func (repository *RepositoryStore) ListVersions(ctx context.Context, pluginID string, page, size int) ([]PluginVersion, int64, error) {
	if strings.TrimSpace(pluginID) == "" || page < 1 || size < 1 || size > 100 {
		return nil, 0, ErrInvalidScope
	}
	query := repository.db.WithContext(ctx).Model(&PluginVersion{}).
		Where("plugin_id = ? AND status = ?", pluginID, VersionStatusAvailable)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count scoped Plugin Versions: %w", err)
	}
	var versions []PluginVersion
	if err := repository.db.WithContext(ctx).
		Where("plugin_id = ? AND status = ?", pluginID, VersionStatusAvailable).
		Order("published_at DESC").Order("tag ASC").
		Offset((page - 1) * size).Limit(size).
		Find(&versions).Error; err != nil {
		return nil, 0, fmt.Errorf("list scoped Plugin Versions: %w", err)
	}
	return versions, total, nil
}

func (repository *RepositoryStore) FindVersion(ctx context.Context, pluginID, tag string) (PluginVersion, error) {
	if strings.TrimSpace(pluginID) == "" || strings.TrimSpace(tag) == "" {
		return PluginVersion{}, ErrInvalidScope
	}
	var version PluginVersion
	if err := repository.db.WithContext(ctx).
		Where("plugin_id = ? AND tag = ?", pluginID, tag).
		Take(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PluginVersion{}, ErrNotFound
		}
		return PluginVersion{}, fmt.Errorf("find scoped Plugin Version: %w", err)
	}
	return version, nil
}

func (repository *RepositoryStore) ClearDefaultVersion(ctx context.Context, namespaceID, pluginID string, updatedAt time.Time) error {
	if strings.TrimSpace(namespaceID) == "" || strings.TrimSpace(pluginID) == "" {
		return ErrInvalidScope
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		plugin, err := loadDefaultMutationPlugin(tx, namespaceID, pluginID)
		if err != nil {
			return fmt.Errorf("read scoped Plugin for clear default: %w", err)
		}
		if plugin.Status == PluginStatusArchived {
			return ErrVersionNotAvailable
		}
		if plugin.DefaultVersionTag == nil {
			return nil
		}
		result := tx.Model(&Plugin{}).
			Where("namespace_id = ? AND id = ? AND status = ?", namespaceID, pluginID, plugin.Status).
			Updates(map[string]any{"default_version_tag": nil, "updated_at": updatedAt})
		if result.Error != nil {
			return fmt.Errorf("clear scoped default Version: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return ErrVersionNotAvailable
		}
		return nil
	})
}

func (repository *RepositoryStore) FindBySlug(ctx context.Context, namespaceID, slug string) (Aggregate, error) {
	if strings.TrimSpace(namespaceID) == "" || strings.TrimSpace(slug) == "" {
		return Aggregate{}, ErrInvalidScope
	}
	var record aggregateRecord
	err := repository.db.WithContext(ctx).
		Table("plugins AS p").
		Select(`p.id, p.namespace_id, p.slug, p.description, p.visibility,
			p.status AS plugin_status, p.archived_from, p.default_version_tag,
			p.created_at AS plugin_created_at, p.updated_at AS plugin_updated_at,
			r.storage_key, r.status AS repository_status,
			r.created_at AS repository_created_at, r.updated_at AS repository_updated_at`).
		Joins("JOIN repositories AS r ON r.id = p.id").
		Where("p.namespace_id = ? AND p.slug = ?", namespaceID, slug).
		Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Aggregate{}, ErrNotFound
	}
	if err != nil {
		return Aggregate{}, fmt.Errorf("find scoped Plugin aggregate: %w", err)
	}
	return aggregateFromRecord(record), nil
}

func aggregateFromRecord(record aggregateRecord) Aggregate {
	return Aggregate{
		Plugin: Plugin{
			ID: record.ID, NamespaceID: record.NamespaceID, Slug: record.Slug, Description: record.Description,
			Visibility: record.Visibility, Status: record.PluginStatus, ArchivedFrom: record.ArchivedFrom,
			DefaultVersionTag: record.DefaultVersionTag, CreatedAt: record.PluginCreatedAt, UpdatedAt: record.PluginUpdatedAt,
		},
		Repository: Repository{
			ID: record.ID, StorageKey: record.StorageKey, Status: record.RepositoryStatus,
			CreatedAt: record.RepositoryCreatedAt, UpdatedAt: record.RepositoryUpdatedAt,
		},
	}
}
