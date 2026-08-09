package distribution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func TestMarketplacePublicKeyConflictClassification(t *testing.T) {
	for _, err := range []error{
		errors.New(`ERROR: duplicate key value violates unique constraint "uidx_marketplace_distribution_public_key"`),
		errors.New(`Duplicate entry 'web-a3f91c20' for key 'marketplace_distributions.public_key'`),
	} {
		if !isMarketplacePublicKeyConflict(err) {
			t.Fatalf("isMarketplacePublicKeyConflict(%v) = false", err)
		}
	}
	for _, err := range []error{
		nil,
		errors.New(`duplicate key value violates unique constraint "marketplace_distributions_template_id_key"`),
		errors.New(`query mentions uidx_marketplace_distribution_public_key but is unavailable`),
	} {
		if isMarketplacePublicKeyConflict(err) {
			t.Fatalf("isMarketplacePublicKeyConflict(%v) = true", err)
		}
	}
}

func TestValidateOrigin(t *testing.T) {
	for _, origin := range []string{"http://localhost:8080", "https://marketplace.example"} {
		if got, err := validateOrigin(origin); err != nil || got != origin {
			t.Fatalf("validateOrigin(%q) = (%q, %v)", origin, got, err)
		}
	}
	for _, origin := range []string{
		"", "ftp://marketplace.example", "https://", "https://user@marketplace.example",
		"https://marketplace.example/path", "https://marketplace.example?query=1", "https://marketplace.example#fragment",
	} {
		if _, err := validateOrigin(origin); err == nil {
			t.Fatalf("validateOrigin(%q) succeeded", origin)
		}
	}
}

func TestPublishBuildsPinnedMarketplaceAndSwitchesPointers(t *testing.T) {
	fixture := newPublicationFixture()
	service := NewPublicationService(fixture.repository, fixture.builder)
	result, err := service.Publish(context.Background(), PublishCommand{
		TemplateID: fixture.templateID, PluginVersionIDs: []uuid.UUID{fixture.versionID},
		Origin: "https://marketplace.example", PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.DistributionID == uuid.Nil || result.PublicKey.String() == "" || result.RevisionID == uuid.Nil || result.ProjectionID == uuid.Nil {
		t.Fatalf("Publish() returned zero identities: %#v", result)
	}
	if _, err := distributionservice.ParseMarketplacePublicKey(result.PublicKey.String()); err != nil {
		t.Fatalf("Publish() public key %q is invalid: %v", result.PublicKey.String(), err)
	}
	if fixture.repository.marketplaceDistribution == nil || fixture.repository.marketplaceDistribution.PublicKey != result.PublicKey.String() {
		t.Fatalf("persisted public key = %#v, want %q", fixture.repository.marketplaceDistribution, result.PublicKey)
	}
	body := string(result.ContentJSON)
	for _, required := range []string{
		`"name":"ctf-web"`, `"source":"url"`, `"ref":"v1.0.0"`,
		`"sha":"` + fixture.builder.pluginResult.DistributionSHA + `"`,
		"https://marketplace.example/distribution/plugins/" + fixture.repository.pluginDistribution.ID + ".git",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("marketplace JSON %s does not contain %q", body, required)
		}
	}
	if fixture.repository.switchedProjection != result.ProjectionID || fixture.repository.switchedRevision != result.RevisionID {
		t.Fatalf("pointers switched to %s/%s, want %s/%s", fixture.repository.switchedRevision, fixture.repository.switchedProjection, result.RevisionID, result.ProjectionID)
	}
	if fixture.builder.marketplaceContent != body {
		t.Fatalf("builder marketplace content = %q, want exact stored bytes %q", fixture.builder.marketplaceContent, body)
	}
}

func TestPublishReusesPersistedMarketplacePublicKey(t *testing.T) {
	fixture := newPublicationFixture()
	persistedKey := "original-name-a3f91c20"
	fixture.repository.marketplaceDistribution = &MarketplaceDistribution{
		ID: uuid.NewString(), TemplateID: fixture.templateID.String(), PublicKey: persistedKey, Status: StatusActive,
	}
	fixture.repository.input.Template.Name = "Renamed Marketplace"
	result, err := NewPublicationService(fixture.repository, fixture.builder).Publish(context.Background(), PublishCommand{
		TemplateID: fixture.templateID, PluginVersionIDs: []uuid.UUID{fixture.versionID},
		Origin: "https://marketplace.example", PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.PublicKey.String() != persistedKey {
		t.Fatalf("Publish() public key = %q, want persisted %q", result.PublicKey.String(), persistedKey)
	}
	if fixture.repository.marketplaceCreateCalls != 0 {
		t.Fatalf("existing distribution was recreated %d times", fixture.repository.marketplaceCreateCalls)
	}
}

func TestPublishRetriesOnlyMarketplacePublicKeyCollision(t *testing.T) {
	fixture := newPublicationFixture()
	fixture.repository.marketplaceCreateErrors = []error{ErrMarketplacePublicKeyConflict, nil}
	if _, err := NewPublicationService(fixture.repository, fixture.builder).Publish(context.Background(), PublishCommand{
		TemplateID: fixture.templateID, PluginVersionIDs: []uuid.UUID{fixture.versionID},
		Origin: "https://marketplace.example", PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if fixture.repository.marketplaceCreateCalls != 2 {
		t.Fatalf("marketplace create calls = %d, want 2", fixture.repository.marketplaceCreateCalls)
	}

	fixture = newPublicationFixture()
	fixture.repository.marketplaceCreateErrors = []error{errors.New("database unavailable")}
	if _, err := NewPublicationService(fixture.repository, fixture.builder).Publish(context.Background(), PublishCommand{
		TemplateID: fixture.templateID, PluginVersionIDs: []uuid.UUID{fixture.versionID},
		Origin: "https://marketplace.example", PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	}); err == nil || fixture.repository.marketplaceCreateCalls != 1 {
		t.Fatalf("non-collision error/calls = %v/%d, want error/1", err, fixture.repository.marketplaceCreateCalls)
	}
}

func TestPublishCompensatesNewArtifactsOnTransactionFailure(t *testing.T) {
	fixture := newPublicationFixture()
	fixture.repository.transactionErr = errors.New("commit failed")
	service := NewPublicationService(fixture.repository, fixture.builder)
	_, err := service.Publish(context.Background(), PublishCommand{
		TemplateID: fixture.templateID, PluginVersionIDs: []uuid.UUID{fixture.versionID},
		Origin: "https://marketplace.example", PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err == nil {
		t.Fatal("Publish() succeeded despite transaction failure")
	}
	if len(fixture.builder.removed) != 2 {
		t.Fatalf("removed projection count = %d, want plugin and marketplace", len(fixture.builder.removed))
	}
}

func TestRollbackVerifiesAndReusesHistoricalProjection(t *testing.T) {
	fixture := newPublicationFixture()
	distributionID := uuid.New()
	revisionID := uuid.New()
	projectionID := uuid.New()
	fixture.repository.marketplaceSnapshot = MarketplaceSnapshot{
		Distribution: MarketplaceDistribution{ID: distributionID.String(), TemplateID: fixture.templateID.String()},
	}
	fixture.repository.historicalProjection = MarketplaceDistributionProjection{
		ID: projectionID.String(), StorageKey: projectionID.String(), ContentDigest: strings.Repeat("d", 64), Status: StatusActive,
	}
	service := NewPublicationService(fixture.repository, fixture.builder)
	if err := service.Rollback(context.Background(), distributionID, revisionID); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if fixture.builder.verified.StorageKey != projectionID.String() {
		t.Fatalf("verified projection = %#v", fixture.builder.verified)
	}
	if fixture.builder.marketplaceBuilds != 0 || fixture.builder.pluginBuilds != 0 {
		t.Fatal("rollback rebuilt a Git projection")
	}
	if fixture.repository.switchedProjection != projectionID || fixture.repository.switchedRevision != revisionID {
		t.Fatal("rollback did not switch to historical projection")
	}
}

func TestAccessServiceReturnsImmutableGrantCopies(t *testing.T) {
	repository := &fakePublicationRepository{}
	marketplaceID := uuid.New()
	revisionID := uuid.New()
	content := []byte(`{"name":"ctf-web"}`)
	publicKey, err := distributionservice.ParseMarketplacePublicKey("ctf-web-a3f91c20")
	if err != nil {
		t.Fatal(err)
	}
	repository.marketplaceSnapshot = MarketplaceSnapshot{
		Distribution: MarketplaceDistribution{ID: marketplaceID.String(), PublicKey: publicKey.String()},
		Projection:   MarketplaceDistributionProjection{StorageKey: uuid.NewString()},
		Revision:     MarketplaceRevision{ID: revisionID.String(), ContentJSON: content, ContentDigest: strings.Repeat("a", 64)},
	}
	grant, err := NewAccessService(repository).ResolveMarketplace(context.Background(), publicKey)
	if err != nil {
		t.Fatal(err)
	}
	grant.ContentJSON[0] = 'X'
	if grant.DistributionID != marketplaceID || grant.PublicKey != publicKey {
		t.Fatalf("access grant identity = %s/%q, want %s/%q", grant.DistributionID, grant.PublicKey.String(), marketplaceID, publicKey)
	}
	if content[0] == 'X' {
		t.Fatal("access grant aliases repository content")
	}
}

type publicationFixture struct {
	templateID uuid.UUID
	versionID  uuid.UUID
	repository *fakePublicationRepository
	builder    *fakeProjectionBuilder
}

func newPublicationFixture() publicationFixture {
	templateID := uuid.New()
	versionID := uuid.New()
	pluginID := uuid.New()
	repositoryID := uuid.New()
	pluginProjectionID := uuid.New()
	repository := &fakePublicationRepository{
		input: PublicationInput{
			Template:  MarketplaceTemplate{ID: templateID.String(), Slug: "ctf-web", Name: "CTF Web"},
			Namespace: Namespace{ID: uuid.NewString(), DisplayName: "Security Team"},
			Versions: []PublicationVersion{{
				Version:    PluginVersion{ID: versionID.String(), PluginID: pluginID.String(), Version: "1.0.0", TagName: "v1.0.0"},
				Plugin:     Plugin{ID: pluginID.String(), RepositoryID: repositoryID.String(), Slug: "java-scanner", Description: "Java scanner"},
				Repository: Repository{ID: repositoryID.String()},
			}},
		},
	}
	builder := &fakeProjectionBuilder{pluginResult: gitservice.PluginProjectionResult{
		Projection: gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindPlugin, StorageKey: pluginProjectionID.String()},
		TagName:    "v1.0.0", SourceTagType: gitservice.SourceTagLightweight,
		SourceCommitSHA: strings.Repeat("1", 40), SourceTreeSHA: strings.Repeat("2", 40),
		DistributionSHA: strings.Repeat("3", 40), ContentDigest: strings.Repeat("4", 64),
	}}
	return publicationFixture{templateID: templateID, versionID: versionID, repository: repository, builder: builder}
}

type fakePublicationRepository struct {
	input                   PublicationInput
	pluginDistribution      *PluginDistribution
	marketplaceDistribution *MarketplaceDistribution
	marketplaceSnapshot     MarketplaceSnapshot
	historicalProjection    MarketplaceDistributionProjection
	transactionErr          error
	marketplaceCreateErrors []error
	marketplaceCreateCalls  int
	switchedRevision        uuid.UUID
	switchedProjection      uuid.UUID
}

func (repository *fakePublicationRepository) LoadPublicationInput(context.Context, uuid.UUID, []uuid.UUID) (PublicationInput, error) {
	return repository.input, nil
}
func (repository *fakePublicationRepository) FindPluginDistribution(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (PluginDistribution, error) {
	if repository.pluginDistribution == nil {
		return PluginDistribution{}, distributionservice.ErrNotFound
	}
	return *repository.pluginDistribution, nil
}
func (repository *fakePublicationRepository) FindMarketplaceDistribution(context.Context, uuid.UUID) (MarketplaceDistribution, error) {
	if repository.marketplaceDistribution == nil {
		return MarketplaceDistribution{}, distributionservice.ErrNotFound
	}
	return *repository.marketplaceDistribution, nil
}
func (*fakePublicationRepository) NextRevision(context.Context, uuid.UUID) (uint64, error) {
	return 1, nil
}
func (repository *fakePublicationRepository) CreatePluginDistribution(_ context.Context, record *PluginDistribution) error {
	copy := *record
	repository.pluginDistribution = &copy
	return nil
}
func (repository *fakePublicationRepository) CreateMarketplaceDistribution(_ context.Context, record *MarketplaceDistribution) error {
	repository.marketplaceCreateCalls++
	if len(repository.marketplaceCreateErrors) > 0 {
		err := repository.marketplaceCreateErrors[0]
		repository.marketplaceCreateErrors = repository.marketplaceCreateErrors[1:]
		if err != nil {
			return err
		}
	}
	copy := *record
	repository.marketplaceDistribution = &copy
	return nil
}
func (*fakePublicationRepository) CreateMarketplaceRevision(context.Context, *MarketplaceRevision, []MarketplaceRevisionItem, *MarketplaceDistributionProjection) error {
	return nil
}
func (repository *fakePublicationRepository) SwitchMarketplacePointers(_ context.Context, _ uuid.UUID, revisionID, _ uuid.UUID, projectionID uuid.UUID) error {
	repository.switchedRevision = revisionID
	repository.switchedProjection = projectionID
	return nil
}
func (repository *fakePublicationRepository) FindMarketplaceProjectionByRevision(context.Context, uuid.UUID, uuid.UUID) (MarketplaceDistributionProjection, error) {
	return repository.historicalProjection, nil
}
func (*fakePublicationRepository) FindActivePlugin(context.Context, uuid.UUID) (PluginDistribution, error) {
	return PluginDistribution{}, distributionservice.ErrNotFound
}
func (repository *fakePublicationRepository) FindActiveMarketplace(context.Context, distributionservice.MarketplacePublicKey) (MarketplaceSnapshot, error) {
	return repository.marketplaceSnapshot, nil
}
func (repository *fakePublicationRepository) FindActiveMarketplaceByID(context.Context, uuid.UUID) (MarketplaceSnapshot, error) {
	return repository.marketplaceSnapshot, nil
}
func (repository *fakePublicationRepository) Transaction(_ context.Context, fn func(RepositoryStore) error) error {
	if repository.transactionErr != nil {
		return repository.transactionErr
	}
	return fn(repository)
}

var _ PublicationRepository = (*fakePublicationRepository)(nil)

type fakeProjectionBuilder struct {
	pluginResult       gitservice.PluginProjectionResult
	removed            []gitservice.ImmutableProjection
	verified           gitservice.ImmutableProjection
	marketplaceContent string
	pluginBuilds       int
	marketplaceBuilds  int
}

func (builder *fakeProjectionBuilder) BuildPluginProjection(_ context.Context, command gitservice.BuildPluginProjectionCommand) (gitservice.PluginProjectionResult, error) {
	builder.pluginBuilds++
	result := builder.pluginResult
	result.Projection.StorageKey = command.ProjectionID.String()
	return result, nil
}
func (builder *fakeProjectionBuilder) BuildMarketplaceProjection(_ context.Context, command gitservice.BuildMarketplaceProjectionCommand) (gitservice.MarketplaceProjectionResult, error) {
	builder.marketplaceBuilds++
	builder.marketplaceContent = string(command.ContentJSON)
	return gitservice.MarketplaceProjectionResult{
		Projection:      gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindMarketplace, StorageKey: command.ProjectionID.String()},
		DistributionSHA: strings.Repeat("5", 40), ContentDigest: strings.Repeat("6", 64),
	}, nil
}
func (builder *fakeProjectionBuilder) RemoveProjection(_ context.Context, projection gitservice.ImmutableProjection) error {
	builder.removed = append(builder.removed, projection)
	return nil
}
func (builder *fakeProjectionBuilder) VerifyProjection(_ context.Context, projection gitservice.ImmutableProjection, _ string) error {
	builder.verified = projection
	return nil
}
