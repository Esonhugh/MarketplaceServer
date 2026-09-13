package distribution

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const postgresTestDSNEnvironment = "MARKETPLACE_TEST_POSTGRES_DSN"

func TestPostgresDistributionConstraints(t *testing.T) {
	db := newPostgresMigrationDB(t)
	if err := identitydao.Migrate(db); err != nil {
		t.Fatalf("migrate identity dependencies: %v", err)
	}
	if err := plugindomain.Migrate(db); err != nil {
		t.Fatalf("migrate Plugin lifecycle dependencies: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	fixture := insertConstraintFixture(t, db)
	first := PluginDistribution{
		ID: uuid.NewString(), TemplateID: fixture.templateID, PluginID: fixture.pluginID,
		PluginTag: "v1.0.0", RepositoryID: fixture.repositoryID,
		TagName: "v1.0.0", SourceTagType: "lightweight",
		SourceCommitSHA: strings.Repeat("1", 40), SourceTreeSHA: strings.Repeat("2", 40),
		DistributionSHA: strings.Repeat("3", 40), StorageKey: uuid.NewString(),
		ContentDigest: strings.Repeat("4", 64), Status: StatusActive,
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first plugin distribution: %v", err)
	}
	duplicate := first
	duplicate.ID = uuid.NewString()
	duplicate.StorageKey = uuid.NewString()
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate (template, plugin, version) distribution was accepted")
	}

	distributionA := MarketplaceDistribution{ID: uuid.NewString(), TemplateID: fixture.templateID, PublicKey: "web-a3f91c20", Status: StatusActive}
	if err := db.Create(&distributionA).Error; err != nil {
		t.Fatalf("create marketplace distribution A: %v", err)
	}
	projection := MarketplaceDistributionProjection{
		ID: uuid.NewString(), MarketplaceDistributionID: distributionA.ID, RevisionID: fixture.revisionID,
		StorageKey: uuid.NewString(), DistributionSHA: strings.Repeat("5", 40),
		ContentDigest: strings.Repeat("6", 64), Status: StatusActive,
	}
	if err := db.Create(&projection).Error; err != nil {
		t.Fatalf("create marketplace projection: %v", err)
	}
	if err := db.Model(&distributionA).Update("current_projection_id", projection.ID).Error; err != nil {
		t.Fatalf("activate own marketplace projection: %v", err)
	}

	otherTemplateID := uuid.NewString()
	if err := db.Create(&MarketplaceTemplate{
		ID: otherTemplateID, NamespaceID: fixture.namespaceID, Slug: "other", Name: "Other",
		Visibility: "public", Status: StatusActive,
	}).Error; err != nil {
		t.Fatalf("create other template: %v", err)
	}
	duplicatePublicKey := MarketplaceDistribution{ID: uuid.NewString(), TemplateID: otherTemplateID, PublicKey: distributionA.PublicKey, Status: StatusActive}
	if err := db.Create(&duplicatePublicKey).Error; err == nil {
		t.Fatal("duplicate Marketplace public key was accepted")
	}
	distributionB := MarketplaceDistribution{ID: uuid.NewString(), TemplateID: otherTemplateID, PublicKey: "other-b4e82d31", Status: StatusActive}
	if err := db.Create(&distributionB).Error; err != nil {
		t.Fatalf("create marketplace distribution B: %v", err)
	}
	if err := db.Model(&distributionB).Update("current_projection_id", projection.ID).Error; err == nil {
		t.Fatal("marketplace distribution accepted another distribution's projection pointer")
	}

	repository, err := NewGORMRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActivePlugin(context.Background(), uuid.MustParse(first.ID)); !errors.Is(err, distributionservice.ErrUnavailable) {
		t.Fatalf("legacy Plugin distribution without a revision projection pointer error = %v, want unavailable", err)
	}
	publicKey, err := distributionservice.ParseMarketplacePublicKey(distributionA.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActiveMarketplace(context.Background(), publicKey); err != nil {
		t.Fatalf("public active Marketplace distribution was unavailable: %v", err)
	}
	if err := db.Exec("UPDATE marketplace_distribution_projections SET content_digest = ? WHERE id = ?", strings.Repeat("7", 64), projection.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActiveMarketplace(context.Background(), publicKey); !errors.Is(err, distributionservice.ErrUnavailable) {
		t.Fatalf("Marketplace digest mismatch error = %v, want unavailable", err)
	}
	if err := db.Exec("UPDATE marketplace_distribution_projections SET content_digest = ? WHERE id = ?", projection.ContentDigest, projection.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&Plugin{}).Where("id = ?", fixture.pluginID).Update("visibility", "private").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActivePlugin(context.Background(), uuid.MustParse(first.ID)); !errors.Is(err, distributionservice.ErrNotFound) {
		t.Fatalf("private parent Plugin distribution error = %v, want not found", err)
	}
	if err := db.Model(&MarketplaceTemplate{}).Where("id = ?", fixture.templateID).Update("visibility", "private").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.FindActiveMarketplace(context.Background(), publicKey); !errors.Is(err, distributionservice.ErrNotFound) {
		t.Fatalf("private parent Marketplace distribution error = %v, want not found", err)
	}
}

type constraintFixture struct {
	namespaceID  string
	repositoryID string
	pluginID     string
	versionID    string
	templateID   string
	revisionID   string
}

func insertConstraintFixture(t *testing.T, db *gorm.DB) constraintFixture {
	t.Helper()
	fixture := constraintFixture{
		namespaceID: uuid.NewString(), pluginID: uuid.NewString(),
		versionID: uuid.NewString(), templateID: uuid.NewString(), revisionID: uuid.NewString(),
	}
	fixture.repositoryID = fixture.pluginID
	now := time.Now().UTC()
	commitSHA := strings.Repeat("a", 40)
	manifestDigest := strings.Repeat("b", 64)
	values := []any{
		&identitymodel.Namespace{ID: fixture.namespaceID, Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security"},
		&Plugin{ID: fixture.pluginID, NamespaceID: fixture.namespaceID, Slug: "scanner", Visibility: "public", Status: plugindomain.PluginStatusActive},
		&Repository{ID: fixture.repositoryID, Status: RepositoryStatusReady, StorageKey: uuid.NewString()},
		&PluginVersion{ID: fixture.versionID, PluginID: fixture.pluginID, Tag: "v1.0.0", RawTagObjectID: &commitSHA, CommitSHA: &commitSHA, ManifestDigest: &manifestDigest, ManifestSnapshot: []byte(`{}`), Status: plugindomain.VersionStatusAvailable, PublishedAt: now},
		&MarketplaceTemplate{ID: fixture.templateID, NamespaceID: fixture.namespaceID, Slug: "web", Name: "Web", Visibility: "public", Status: StatusActive},
		&MarketplaceRevision{ID: fixture.revisionID, TemplateID: fixture.templateID, Revision: 1, ContentJSON: []byte(`{"name":"web"}`), ContentDigest: strings.Repeat("6", 64), Status: StatusActive, PublishedAt: now},
	}
	for _, value := range values {
		if err := db.Create(value).Error; err != nil {
			t.Fatalf("create fixture %T: %v", value, err)
		}
	}
	return fixture
}

func postgresDSNWithSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return fmt.Sprintf("%s search_path=%s", dsn, schema)
}

func newPostgresMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(postgresTestDSNEnvironment)
	if dsn == "" {
		t.Skip(postgresTestDSNEnvironment + " is not configured; skipping PostgreSQL constraint integration test")
	}

	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })
	schema := "marketplace_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error; err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})

	db, err := gorm.Open(postgres.Open(postgresDSNWithSearchPath(t, dsn, schema)), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration schema: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestPostgresDistributionConcurrentMigrate(t *testing.T) {
	db := newPostgresMigrationDB(t)
	if err := identitydao.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := plugindomain.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	for _, phase := range []string{"fresh", "repeat"} {
		t.Run(phase, func(t *testing.T) {
			start := make(chan struct{})
			results := make(chan error, 4)
			for range 4 {
				go func() { <-start; results <- Migrate(db) }()
			}
			close(start)
			for range 4 {
				if err := <-results; err != nil {
					t.Errorf("concurrent migration: %v", err)
				}
			}
		})
	}
	fixture := insertConstraintFixture(t, db)
	if err := db.Model(&MarketplaceRevision{}).Where("id = ?", fixture.revisionID).Update("template_id", uuid.NewString()).Error; err == nil {
		t.Fatal("migration lost revision template foreign key")
	}
}

func TestPostgresDistributionMigrationRollsBackDDL(t *testing.T) {
	db := newPostgresMigrationDB(t)
	if err := identitydao.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := plugindomain.Migrate(db); err != nil {
		t.Fatal(err)
	}
	// Force a late DDL failure after earlier distribution tables were created.
	if err := db.Exec("CREATE VIEW plugin_distributions AS SELECT 1 AS sentinel").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err == nil {
		t.Fatal("migration accepted conflicting view")
	}
	if db.Migrator().HasTable(&MarketplaceTemplate{}) {
		t.Fatal("failed migration left partial distribution DDL")
	}
	if err := db.Exec("DROP VIEW plugin_distributions").Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("retry migration: %v", err)
	}
	insertConstraintFixture(t, db)
}
