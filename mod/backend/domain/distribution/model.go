package distribution

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	StatusActive   = "active"
	StatusBuilding = "building"
	StatusRevoked  = "revoked"
)

type Namespace struct {
	ID          string    `gorm:"type:char(36);primaryKey"`
	Slug        string    `gorm:"size:128;not null;uniqueIndex"`
	DisplayName string    `gorm:"size:255;not null"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (Namespace) TableName() string { return "namespaces" }

type Repository struct {
	ID            string    `gorm:"type:char(36);primaryKey"`
	NamespaceID   string    `gorm:"type:char(36);not null;uniqueIndex:uidx_repository_namespace_slug"`
	Slug          string    `gorm:"size:128;not null;uniqueIndex:uidx_repository_namespace_slug"`
	Visibility    string    `gorm:"size:32;not null"`
	Status        string    `gorm:"size:32;not null"`
	StorageKey    string    `gorm:"size:255;not null;uniqueIndex"`
	DefaultBranch string    `gorm:"size:255"`
	CreatedAt     time.Time `gorm:"not null"`
	UpdatedAt     time.Time `gorm:"not null"`
	Namespace     Namespace `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (Repository) TableName() string { return "repositories" }

type Plugin struct {
	ID           string     `gorm:"type:char(36);primaryKey"`
	NamespaceID  string     `gorm:"type:char(36);not null;uniqueIndex:uidx_plugin_namespace_slug"`
	RepositoryID string     `gorm:"type:char(36);not null;uniqueIndex"`
	Slug         string     `gorm:"size:128;not null;uniqueIndex:uidx_plugin_namespace_slug"`
	Name         string     `gorm:"size:255;not null"`
	Description  string     `gorm:"type:text"`
	Visibility   string     `gorm:"size:32;not null"`
	Status       string     `gorm:"size:32;not null"`
	CreatedAt    time.Time  `gorm:"not null"`
	UpdatedAt    time.Time  `gorm:"not null"`
	Namespace    Namespace  `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Repository   Repository `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (Plugin) TableName() string { return "plugins" }

type PluginVersion struct {
	ID               string    `gorm:"type:char(36);primaryKey"`
	PluginID         string    `gorm:"type:char(36);not null;uniqueIndex:uidx_plugin_version;uniqueIndex:uidx_plugin_tag"`
	Version          string    `gorm:"size:128;not null;uniqueIndex:uidx_plugin_version"`
	TagName          string    `gorm:"size:255;not null;uniqueIndex:uidx_plugin_tag"`
	CommitSHA        string    `gorm:"type:char(40);not null"`
	ManifestDigest   string    `gorm:"type:char(64);not null"`
	ManifestSnapshot []byte    `gorm:"type:json;not null"`
	Status           string    `gorm:"size:32;not null"`
	PublishedAt      time.Time `gorm:"not null"`
	CreatedAt        time.Time `gorm:"not null"`
	UpdatedAt        time.Time `gorm:"not null"`
	Plugin           Plugin    `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (PluginVersion) TableName() string { return "plugin_versions" }

type MarketplaceTemplate struct {
	ID                  string    `gorm:"type:char(36);primaryKey"`
	NamespaceID         string    `gorm:"type:char(36);not null;uniqueIndex:uidx_marketplace_namespace_slug"`
	Slug                string    `gorm:"size:128;not null;uniqueIndex:uidx_marketplace_namespace_slug"`
	Name                string    `gorm:"size:255;not null"`
	Description         string    `gorm:"type:text"`
	Visibility          string    `gorm:"size:32;not null"`
	Status              string    `gorm:"size:32;not null"`
	PublishedRevisionID *string   `gorm:"type:char(36);index"`
	CreatedAt           time.Time `gorm:"not null"`
	UpdatedAt           time.Time `gorm:"not null"`
	Namespace           Namespace `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (MarketplaceTemplate) TableName() string { return "marketplace_templates" }

type MarketplaceRevision struct {
	ID            string              `gorm:"type:char(36);primaryKey"`
	TemplateID    string              `gorm:"type:char(36);not null;uniqueIndex:uidx_marketplace_revision"`
	Revision      uint64              `gorm:"not null;uniqueIndex:uidx_marketplace_revision"`
	ContentJSON   []byte              `gorm:"type:json;not null"`
	ContentDigest string              `gorm:"type:char(64);not null"`
	Status        string              `gorm:"size:32;not null"`
	PublishedAt   time.Time           `gorm:"not null"`
	CreatedAt     time.Time           `gorm:"not null"`
	Template      MarketplaceTemplate `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (MarketplaceRevision) TableName() string { return "marketplace_revisions" }

func (*MarketplaceRevision) BeforeUpdate(*gorm.DB) error {
	return errors.New("marketplace revisions are immutable")
}

type MarketplaceRevisionItem struct {
	ID                   string              `gorm:"type:char(36);primaryKey"`
	RevisionID           string              `gorm:"type:char(36);not null;uniqueIndex:uidx_revision_plugin"`
	PluginID             string              `gorm:"type:char(36);not null;uniqueIndex:uidx_revision_plugin"`
	PluginVersionID      string              `gorm:"type:char(36);not null"`
	PluginDistributionID string              `gorm:"type:char(36);not null"`
	SourceURL            string              `gorm:"type:text;not null"`
	DistributionSHA      string              `gorm:"type:char(40);not null"`
	Position             int                 `gorm:"not null"`
	CreatedAt            time.Time           `gorm:"not null"`
	Revision             MarketplaceRevision `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Plugin               Plugin              `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	PluginVersion        PluginVersion       `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (MarketplaceRevisionItem) TableName() string { return "marketplace_revision_items" }

func (*MarketplaceRevisionItem) BeforeUpdate(*gorm.DB) error {
	return errors.New("marketplace revision items are immutable")
}

type MarketplaceDistribution struct {
	ID                  string                             `gorm:"type:char(36);primaryKey"`
	TemplateID          string                             `gorm:"type:char(36);not null;uniqueIndex"`
	PublicKey           string                             `gorm:"size:128;not null;uniqueIndex:uidx_marketplace_distribution_public_key"`
	CurrentProjectionID *string                            `gorm:"type:char(36);index"`
	Status              string                             `gorm:"size:32;not null;index"`
	CreatedAt           time.Time                          `gorm:"not null"`
	UpdatedAt           time.Time                          `gorm:"not null"`
	RevokedAt           *time.Time                         `gorm:"index"`
	Template            MarketplaceTemplate                `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	CurrentProjection   *MarketplaceDistributionProjection `gorm:"foreignKey:CurrentProjectionID,ID;references:ID,MarketplaceDistributionID;constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (MarketplaceDistribution) TableName() string { return "marketplace_distributions" }

func (distribution *MarketplaceDistribution) BeforeUpdate(tx *gorm.DB) error {
	for _, field := range []string{"ID", "TemplateID", "PublicKey", "CreatedAt"} {
		if tx.Statement.Changed(field) {
			return errors.New("marketplace distribution identity is immutable")
		}
	}
	return nil
}

type MarketplaceDistributionProjection struct {
	ID                        string                  `gorm:"type:char(36);primaryKey;uniqueIndex:uidx_projection_distribution_pointer"`
	MarketplaceDistributionID string                  `gorm:"type:char(36);not null;uniqueIndex:uidx_distribution_revision;uniqueIndex:uidx_projection_distribution_pointer"`
	RevisionID                string                  `gorm:"type:char(36);not null;uniqueIndex:uidx_distribution_revision"`
	StorageKey                string                  `gorm:"size:255;not null;uniqueIndex"`
	DistributionSHA           string                  `gorm:"type:char(40);not null"`
	ContentDigest             string                  `gorm:"type:char(64);not null"`
	Status                    string                  `gorm:"size:32;not null;index"`
	CreatedAt                 time.Time               `gorm:"not null"`
	MarketplaceDistribution   MarketplaceDistribution `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Revision                  MarketplaceRevision     `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (MarketplaceDistributionProjection) TableName() string {
	return "marketplace_distribution_projections"
}

func (*MarketplaceDistributionProjection) BeforeUpdate(*gorm.DB) error {
	return errors.New("marketplace distribution projections are immutable")
}

type PluginDistribution struct {
	ID                string              `gorm:"type:char(36);primaryKey"`
	TemplateID        string              `gorm:"type:char(36);not null;uniqueIndex:uidx_plugin_distribution"`
	PluginID          string              `gorm:"type:char(36);not null;uniqueIndex:uidx_plugin_distribution"`
	PluginVersionID   string              `gorm:"type:char(36);not null;uniqueIndex:uidx_plugin_distribution"`
	RepositoryID      string              `gorm:"type:char(36);not null"`
	TagName           string              `gorm:"size:255;not null"`
	SourceTagType     string              `gorm:"size:32;not null"`
	SourceTagObjectID string              `gorm:"type:char(40)"`
	SourceCommitSHA   string              `gorm:"type:char(40);not null"`
	SourceTreeSHA     string              `gorm:"type:char(40);not null"`
	DistributionSHA   string              `gorm:"type:char(40);not null"`
	StorageKey        string              `gorm:"size:255;not null;uniqueIndex"`
	ContentDigest     string              `gorm:"type:char(64);not null"`
	Status            string              `gorm:"size:32;not null;index"`
	CreatedAt         time.Time           `gorm:"not null"`
	UpdatedAt         time.Time           `gorm:"not null"`
	RevokedAt         *time.Time          `gorm:"index"`
	Template          MarketplaceTemplate `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Plugin            Plugin              `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	PluginVersion     PluginVersion       `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
	Repository        Repository          `gorm:"constraint:OnUpdate:RESTRICT,OnDelete:RESTRICT"`
}

func (PluginDistribution) TableName() string { return "plugin_distributions" }

func (distribution *PluginDistribution) BeforeUpdate(tx *gorm.DB) error {
	for _, field := range []string{
		"ID", "TemplateID", "PluginID", "PluginVersionID", "RepositoryID", "TagName",
		"SourceTagType", "SourceTagObjectID", "SourceCommitSHA", "SourceTreeSHA",
		"DistributionSHA", "StorageKey", "ContentDigest", "CreatedAt",
	} {
		if tx.Statement.Changed(field) {
			return errors.New("plugin distribution identity and projection are immutable")
		}
	}
	return nil
}
