package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestServiceCreateProvisionsAuthorizedSharedIDAggregate(t *testing.T) {
	db := newServiceTestDatabase(t)
	namespace := createServiceTestNamespace(t, db, "security")
	authorizer := &serviceTestAuthorizer{}
	provisioner := &serviceTestProvisioner{}
	service := newServiceForTest(t, db, authorizer, provisioner, nil)
	principal := serviceTestPrincipal(t)

	created, err := service.Create(t.Context(), principal, CreateInput{Namespace: namespace.Slug, Name: "scanner"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Namespace != namespace.Slug || created.Name != "scanner" || created.Status != PluginStatusDraft || created.Visibility != VisibilityPublic || created.RepositoryStatus != RepositoryStatusReady {
		t.Fatalf("Create() = %#v", created)
	}
	if created.CloneURL != "/git/security/scanner.git" || created.DefaultVersion != nil || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("Create() public fields = %#v", created)
	}
	if provisioner.provisioned.ID == "" || provisioner.provisioned.ID != provisioner.provisioned.StorageKey {
		t.Fatalf("provisioned identity = %#v", provisioner.provisioned)
	}
	if got := authorizer.last(); got.action != auth.ActionPluginCreate || got.resource != (auth.ResourceRef{Type: auth.ResourceNamespace, ID: namespace.ID}) {
		t.Fatalf("authorization = %#v", got)
	}

	stored, err := service.repository.FindBySlug(t.Context(), namespace.ID, "scanner")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Plugin.ID != provisioner.provisioned.ID || stored.Repository.ID != stored.Plugin.ID || stored.Repository.StorageKey != provisioner.provisioned.StorageKey {
		t.Fatalf("stored aggregate = %#v", stored)
	}
}

func TestServiceCreateRejectsInvalidDuplicateAndDeniedRequestsSafely(t *testing.T) {
	db := newServiceTestDatabase(t)
	createServiceTestNamespace(t, db, "security")
	principal := serviceTestPrincipal(t)

	for _, input := range []CreateInput{
		{Namespace: "security", Name: "Scanner"},
		{Namespace: "security", Name: "scanner-"},
		{Namespace: "security", Name: strings.Repeat("a", 64)},
		{Namespace: "security", Name: "scanner", Visibility: "internal"},
	} {
		service := newServiceForTest(t, db, &serviceTestAuthorizer{}, &serviceTestProvisioner{}, nil)
		if _, err := service.Create(t.Context(), principal, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Create(%#v) error = %v, want ErrInvalidInput", input, err)
		}
	}

	denied := newServiceForTest(t, db, &serviceTestAuthorizer{deny: true}, &serviceTestProvisioner{}, nil)
	if _, err := denied.Create(t.Context(), principal, CreateInput{Namespace: "security", Name: "scanner"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("denied Create() error = %v, want ErrForbidden", err)
	}

	provisioner := &serviceTestProvisioner{}
	service := newServiceForTest(t, db, &serviceTestAuthorizer{}, provisioner, nil)
	if _, err := service.Create(t.Context(), principal, CreateInput{Namespace: "security", Name: "scanner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(t.Context(), principal, CreateInput{Namespace: "security", Name: "scanner"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate Create() error = %v, want ErrConflict", err)
	}
}

func TestServiceCreateCompensatesFailedAggregatePersistence(t *testing.T) {
	db := newServiceTestDatabase(t)
	createServiceTestNamespace(t, db, "security")
	if err := db.Exec(`CREATE TRIGGER reject_repository BEFORE INSERT ON repositories BEGIN SELECT RAISE(ABORT, 'fixture persistence failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	provisioner := &serviceTestProvisioner{}
	service := newServiceForTest(t, db, &serviceTestAuthorizer{}, provisioner, nil)

	if _, err := service.Create(t.Context(), serviceTestPrincipal(t), CreateInput{Namespace: "security", Name: "scanner"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Create() error = %v, want ErrUnavailable", err)
	}
	if provisioner.removed != 1 {
		t.Fatalf("compensation removals = %d, want 1", provisioner.removed)
	}
	var plugins int64
	if err := db.Model(&Plugin{}).Count(&plugins).Error; err != nil {
		t.Fatal(err)
	}
	if plugins != 0 {
		t.Fatalf("persisted Plugins = %d, want 0", plugins)
	}
}

func TestServiceListAndGetAreAuthorizedAndTenantScoped(t *testing.T) {
	db := newServiceTestDatabase(t)
	alpha := createServiceTestNamespace(t, db, "alpha")
	bravo := createServiceTestNamespace(t, db, "bravo")
	now := time.Now().UTC()
	createServiceTestAggregate(t, db, alpha.ID, "zeta", PluginStatusDraft, VisibilityPrivate, RepositoryStatusReady, now)
	alphaPlugin := createServiceTestAggregate(t, db, alpha.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusReadOnly, now.Add(time.Second))
	createServiceTestAggregate(t, db, bravo.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusReady, now)
	authorizer := &serviceTestAuthorizer{}
	service := newServiceForTest(t, db, authorizer, &serviceTestProvisioner{}, nil)
	principal := serviceTestPrincipal(t)

	page, err := service.List(t.Context(), principal, alpha.Slug, 1, 1)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.Total != 2 || page.Page != 1 || page.Size != 1 || len(page.Items) != 1 || page.Items[0].Name != "scanner" || page.Items[0].Namespace != alpha.Slug {
		t.Fatalf("List() = %#v", page)
	}
	if got := authorizer.last(); got.action != auth.ActionPluginList || got.resource != (auth.ResourceRef{Type: auth.ResourceNamespace, ID: alpha.ID}) {
		t.Fatalf("list authorization = %#v", got)
	}

	got, err := service.Get(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "scanner" || got.Namespace != "alpha" || got.RepositoryStatus != RepositoryStatusReadOnly {
		t.Fatalf("Get() = %#v", got)
	}
	if authorization := authorizer.last(); authorization.action != auth.ActionPluginRead || authorization.resource != (auth.ResourceRef{Type: auth.ResourcePlugin, ID: alphaPlugin.Plugin.ID, NamespaceID: alpha.ID}) {
		t.Fatalf("get authorization = %#v", authorization)
	}

	if _, err := service.Get(t.Context(), principal, "alpha", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Get() error = %v, want ErrNotFound", err)
	}
	denied := newServiceForTest(t, db, &serviceTestAuthorizer{deny: true}, &serviceTestProvisioner{}, nil)
	if _, err := denied.Get(t.Context(), principal, "alpha", "scanner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("denied Get() error = %v, want nondisclosing ErrNotFound", err)
	}
	if _, err := service.List(t.Context(), principal, "alpha", 0, 10); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid List() error = %v, want ErrInvalidInput", err)
	}
}

func TestServiceArchiveRestoreAndVisibilityEnforceLifecycle(t *testing.T) {
	db := newServiceTestDatabase(t)
	namespace := createServiceTestNamespace(t, db, "security")
	active := createServiceTestAggregate(t, db, namespace.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusError, time.Now().UTC())
	authorizer := &serviceTestAuthorizer{}
	service := newServiceForTest(t, db, authorizer, &serviceTestProvisioner{}, nil)
	principal := serviceTestPrincipal(t)

	if err := service.Archive(t.Context(), principal, namespace.Slug, active.Plugin.Slug); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	if err := service.Archive(t.Context(), principal, namespace.Slug, active.Plugin.Slug); err != nil {
		t.Fatalf("idempotent Archive() error = %v", err)
	}
	archived := loadServiceTestAggregate(t, service, namespace.ID, active.Plugin.Slug)
	if archived.Plugin.Status != PluginStatusArchived || archived.Plugin.ArchivedFrom == nil || *archived.Plugin.ArchivedFrom != PluginStatusActive {
		t.Fatalf("archived aggregate = %#v", archived.Plugin)
	}
	if got := authorizer.last(); got.action != auth.ActionPluginArchive {
		t.Fatalf("archive authorization = %#v", got)
	}
	if err := service.SetVisibility(t.Context(), principal, namespace.Slug, active.Plugin.Slug, VisibilityPrivate); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived SetVisibility() error = %v, want ErrConflict", err)
	}
	if err := service.Restore(t.Context(), principal, namespace.Slug, active.Plugin.Slug); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	restored := loadServiceTestAggregate(t, service, namespace.ID, active.Plugin.Slug)
	if restored.Plugin.Status != PluginStatusActive || restored.Plugin.ArchivedFrom != nil {
		t.Fatalf("restored aggregate = %#v", restored.Plugin)
	}
	if err := service.Restore(t.Context(), principal, namespace.Slug, active.Plugin.Slug); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-archived Restore() error = %v, want ErrConflict", err)
	}
	if err := service.SetVisibility(t.Context(), principal, namespace.Slug, active.Plugin.Slug, VisibilityPrivate); err != nil {
		t.Fatalf("SetVisibility() error = %v", err)
	}
	if err := service.SetVisibility(t.Context(), principal, namespace.Slug, active.Plugin.Slug, VisibilityPrivate); err != nil {
		t.Fatalf("idempotent SetVisibility() error = %v", err)
	}
	updated := loadServiceTestAggregate(t, service, namespace.ID, active.Plugin.Slug)
	if updated.Plugin.Visibility != VisibilityPrivate {
		t.Fatalf("visibility = %q, want private", updated.Plugin.Visibility)
	}
	if got := authorizer.last(); got.action != auth.ActionPluginArchive {
		t.Fatalf("visibility authorization = %#v", got)
	}
}

func newServiceTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.User{}, &identitymodel.Namespace{}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func createServiceTestNamespace(t *testing.T, db *gorm.DB, slug string) identitymodel.Namespace {
	t.Helper()
	namespace := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: slug, DisplayName: slug}
	if err := db.Create(&namespace).Error; err != nil {
		t.Fatal(err)
	}
	return namespace
}

func newServiceForTest(t *testing.T, db *gorm.DB, authorizer auth.Authorizer, provisioner gitservice.RepositoryProvisioner, inspector gitservice.PluginSourceInspector) *Service {
	t.Helper()
	service, err := NewService(db, authorizer, provisioner, inspector)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func createServiceTestAggregate(t *testing.T, db *gorm.DB, namespaceID, slug, status, visibility, repositoryStatus string, createdAt time.Time) Aggregate {
	t.Helper()
	pluginID := uuid.NewString()
	aggregate := Aggregate{
		Plugin: Plugin{
			ID: pluginID, NamespaceID: namespaceID, Slug: slug, Status: status, Visibility: visibility,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		},
		Repository: Repository{
			ID: pluginID, StorageKey: uuid.NewString(), Status: repositoryStatus,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		},
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&aggregate.Plugin).Error; err != nil {
			return err
		}
		return tx.Create(&aggregate.Repository).Error
	}); err != nil {
		t.Fatal(err)
	}
	return aggregate
}

func loadServiceTestAggregate(t *testing.T, service *Service, namespaceID, slug string) Aggregate {
	t.Helper()
	aggregate, err := service.repository.FindBySlug(t.Context(), namespaceID, slug)
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}

func serviceTestPrincipal(t *testing.T) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal(uuid.NewString(), "owner", auth.CredentialJWT, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

type serviceTestAuthorization struct {
	action   auth.Action
	resource auth.ResourceRef
}

type serviceTestAuthorizer struct {
	deny   bool
	denied map[auth.Action]bool
	calls  []serviceTestAuthorization
}

func (authorizer *serviceTestAuthorizer) Authorize(_ context.Context, _ auth.Principal, action auth.Action, resource auth.ResourceRef) error {
	authorizer.calls = append(authorizer.calls, serviceTestAuthorization{action: action, resource: resource})
	if authorizer.deny || authorizer.denied[action] {
		return errors.New("fixture denied")
	}
	return nil
}

func (authorizer *serviceTestAuthorizer) last() serviceTestAuthorization {
	if len(authorizer.calls) == 0 {
		return serviceTestAuthorization{}
	}
	return authorizer.calls[len(authorizer.calls)-1]
}

type serviceTestProvisioner struct {
	provisioned gitservice.RepositoryIdentity
	removed     int
	removeErr   error
}

func (provisioner *serviceTestProvisioner) ProvisionRepository(_ context.Context, identity gitservice.RepositoryIdentity) (gitservice.ProvisionedRepository, error) {
	provisioner.provisioned = identity
	return gitservice.NewProvisionedRepository(identity, uuid.NewString()), nil
}

func (provisioner *serviceTestProvisioner) RemoveProvisionedRepository(_ context.Context, provisioned gitservice.ProvisionedRepository) error {
	if !provisioned.Created() {
		return gitservice.ErrRepositoryNotProvisioned
	}
	provisioner.removed++
	return provisioner.removeErr
}

func (*serviceTestProvisioner) ListRepositoryOrphanCandidates(context.Context, time.Duration) ([]gitservice.RepositoryOrphanCandidate, error) {
	return nil, nil
}
