package backend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	pluginhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"github.com/juanjiTech/inject/v2"
	"github.com/juanjiTech/jin"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestModName(t *testing.T) {
	m := testMod()

	if got, want := m.Name(), "backend"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}

	if _, ok := any(m).(kernel.Module); !ok {
		t.Fatalf("Mod must implement kernel.Module")
	}
}

func TestConfigExposesJWTSecret(t *testing.T) {
	mod := &Mod{}
	config, ok := mod.Config().(*Config)
	if !ok || config != &mod.config {
		t.Fatalf("Config() = %#v, want backend config pointer", mod.Config())
	}
}

func TestPostInitFailsFastWhenDependenciesMissing(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*kernel.Hub)
		wantErr string
	}{
		{
			name:    "missing jin engine",
			arrange: func(*kernel.Hub) {},
			wantErr: "backend dependency *jin.Engine",
		},
		{
			name: "missing gorm db",
			arrange: func(h *kernel.Hub) {
				engine := jin.New()
				h.Map(&engine)
			},
			wantErr: "backend dependency *gorm.DB",
		},
		{
			name: "missing git repository service",
			arrange: func(h *kernel.Hub) {
				engine := jin.New()
				db := &gorm.DB{}
				h.Map(&engine, &db)
			},
			wantErr: "backend dependency gitservice.RepositoryService",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := newTestHub()
			tt.arrange(hub)

			err := (&Mod{}).PostInit(hub)
			if err == nil {
				t.Fatalf("PostInit() error = nil, want diagnostic containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("PostInit() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestPostInitRejectsTypedNilRepositoryService(t *testing.T) {
	hub := newTestHub()
	engine := jin.New()
	db := &gorm.DB{}
	var gitSvc *fakeRepositoryService
	var repoSvc gitservice.RepositoryService = gitSvc
	hub.Map(&engine, &db, &repoSvc)

	err := (&Mod{}).PostInit(hub)
	if err == nil {
		t.Fatal("PostInit() error = nil, want typed-nil dependency rejection")
	}
	if !strings.Contains(err.Error(), "gitservice.RepositoryService") || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("PostInit() error = %q, want typed-nil git dependency diagnostic", err.Error())
	}
}

func TestPostInitRequiresPluginLifecycleGitDependencies(t *testing.T) {
	base := func(hub *kernel.Hub) {
		engine := jin.New()
		db := &gorm.DB{}
		repoSvc := gitservice.RepositoryService(&fakeRepositoryService{})
		projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
		hub.Map(&engine, &db, &repoSvc, &projectionBuilder)
	}
	for _, test := range []struct {
		name    string
		arrange func(*kernel.Hub)
		wantErr string
	}{
		{
			name:    "missing repository provisioner",
			arrange: base,
			wantErr: "backend dependency gitservice.RepositoryProvisioner",
		},
		{
			name: "missing plugin source inspector",
			arrange: func(hub *kernel.Hub) {
				base(hub)
				provisioner := gitservice.RepositoryProvisioner(&fakeRepositoryProvisioner{})
				hub.Map(&provisioner)
			},
			wantErr: "backend dependency gitservice.PluginSourceInspector",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := newTestHub()
			test.arrange(hub)
			if err := testMod().PostInit(hub); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("PostInit() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestPostInitStopsBeforeAssemblyWhenMigrationFails(t *testing.T) {
	hub := newTestHub()
	engine := jin.New()
	db := &gorm.DB{}
	gitSvc := &fakeRepositoryService{}
	repoSvc := gitservice.RepositoryService(gitSvc)
	projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
	repositoryProvisioner := gitservice.RepositoryProvisioner(&fakeRepositoryProvisioner{})
	pluginSourceInspector := gitservice.PluginSourceInspector(&fakePluginSourceInspector{})
	hub.Map(&engine, &db, &repoSvc, &projectionBuilder, &repositoryProvisioner, &pluginSourceInspector)

	m := &Mod{config: Config{JWTSecret: "test JWT secret"}, migrate: func(*gorm.DB) error { return errors.New("migration failed") }}
	err := m.PostInit(hub)
	if err == nil || !strings.Contains(err.Error(), "database migration failed") {
		t.Fatalf("PostInit() error = %v, want migration failure", err)
	}
	if m.jin != nil || m.db != nil || m.git != nil || m.distributionRepository != nil {
		t.Fatal("PostInit() retained partial assembly after migration failure")
	}
	var resolver gitservice.RepositoryResolver
	if err := hub.Load(&resolver); err == nil {
		t.Fatal("PostInit() mapped resolver after migration failure")
	}
}

func TestPostInitAssemblesDependencies(t *testing.T) {
	hub := newTestHub()
	engine := jin.New()
	db := &gorm.DB{}
	gitSvc := &fakeRepositoryService{}
	repoSvc := gitservice.RepositoryService(gitSvc)
	projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
	repositoryProvisioner := gitservice.RepositoryProvisioner(&fakeRepositoryProvisioner{})
	pluginSourceInspector := gitservice.PluginSourceInspector(&fakePluginSourceInspector{})
	hub.Map(&engine, &db, &repoSvc, &projectionBuilder, &repositoryProvisioner, &pluginSourceInspector)

	m := testMod()
	if err := m.PostInit(hub); err != nil {
		t.Fatalf("PostInit() error = %v", err)
	}

	if m.jin != engine {
		t.Fatalf("PostInit() did not store jin dependency")
	}
	if m.db != db {
		t.Fatalf("PostInit() did not store db dependency")
	}
	if m.git != gitSvc {
		t.Fatalf("PostInit() did not store git service dependency")
	}

	var resolver gitservice.RepositoryResolver
	if err := hub.Load(&resolver); err != nil {
		t.Fatalf("hub.Load(gitservice.RepositoryResolver) error = %v", err)
	}
	if resolver == nil {
		t.Fatal("mapped RepositoryResolver is nil")
	}
	if repository, err := resolver.Resolve(context.Background(), "team-a", "plugin-one"); !errors.Is(err, gitservice.ErrRepositoryUnavailable) || repository != (gitservice.Repository{}) {
		t.Fatalf("fail-closed resolver Resolve() = (%#v, %v), want zero repository and unavailable", repository, err)
	}
}

func TestResolveUsesSharedIDPluginAggregateAndTenantScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&identitymodel.User{}, &identitymodel.Namespace{}); err != nil {
		t.Fatal(err)
	}
	if err := plugindomain.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, namespace := range []identitymodel.Namespace{
		{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "team-a", DisplayName: "Team A", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "team-b", DisplayName: "Team B", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&namespace).Error; err != nil {
			t.Fatal(err)
		}
		pluginID := uuid.NewString()
		if err := db.Create(&plugindomain.Plugin{ID: pluginID, NamespaceID: namespace.ID, Slug: "scanner", Visibility: plugindomain.VisibilityPublic, Status: plugindomain.PluginStatusActive, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&plugindomain.Repository{ID: pluginID, StorageKey: pluginID, Status: plugindomain.RepositoryStatusReady, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	m := &Mod{db: db, git: &fakeRepositoryService{}}
	resolved, err := m.Resolve(context.Background(), "team-b", "scanner")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.NamespaceID == "" || resolved.Status != plugindomain.RepositoryStatusReady || resolved.Visibility != plugindomain.VisibilityPublic {
		t.Fatalf("Resolve() = %#v", resolved)
	}
	var repository plugindomain.Repository
	if err := db.Where("id = ?", resolved.ID).Take(&repository).Error; err != nil {
		t.Fatal(err)
	}
	if repository.ID != resolved.ID || repository.StorageKey != resolved.ID {
		t.Fatalf("shared aggregate = %#v/%#v", resolved, repository)
	}
	if _, err := m.Resolve(context.Background(), "team-c", "scanner"); !errors.Is(err, gitservice.ErrRepositoryNotFound) {
		t.Fatalf("cross-tenant Resolve() error = %v, want not found", err)
	}
}

func TestResolveFailsClosedForIncompleteOrUnassembledBackend(t *testing.T) {
	tests := []struct {
		name       string
		mod        *Mod
		namespace  string
		repository string
	}{
		{name: "unassembled", mod: &Mod{}, namespace: "team-a", repository: "plugin-one"},
		{name: "blank namespace", mod: assembledMod(), repository: "plugin-one"},
		{name: "blank repository", mod: assembledMod(), namespace: "team-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository, err := tt.mod.Resolve(context.Background(), tt.namespace, tt.repository)
			if !errors.Is(err, gitservice.ErrRepositoryUnavailable) {
				t.Fatalf("Resolve() error = %v, want ErrRepositoryUnavailable", err)
			}
			if repository != (gitservice.Repository{}) {
				t.Fatalf("Resolve() repository = %#v, want zero value", repository)
			}
		})
	}
}

func TestLoadRegistersBackendHealthEndpoint(t *testing.T) {
	hub := newTestHub()
	engine := jin.New()
	db := &gorm.DB{}
	gitSvc := &fakeRepositoryService{}
	repoSvc := gitservice.RepositoryService(gitSvc)
	projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
	repositoryProvisioner := gitservice.RepositoryProvisioner(&fakeRepositoryProvisioner{})
	pluginSourceInspector := gitservice.PluginSourceInspector(&fakePluginSourceInspector{})
	hub.Map(&engine, &db, &repoSvc, &projectionBuilder, &repositoryProvisioner, &pluginSourceInspector)

	m := testMod()
	if err := m.PostInit(hub); err != nil {
		t.Fatalf("PostInit() error = %v", err)
	}
	if err := m.Load(hub); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	engine.ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d; body=%s", got, want, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got, want := w.Body.String(), `{"data":{"status":"ok","checks":[{"name":"backend","status":"ok"}]}}`; got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestLoadFailsFastWithoutAssemblyAndIsIdempotent(t *testing.T) {
	t.Run("fails before dependencies assembled", func(t *testing.T) {
		hub := newTestHub()
		engine := jin.New()
		db := &gorm.DB{}
		gitSvc := &fakeRepositoryService{}
		repoSvc := gitservice.RepositoryService(gitSvc)
		projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
		hub.Map(&engine, &db, &repoSvc, &projectionBuilder)

		err := (&Mod{}).Load(hub)
		if err == nil {
			t.Fatalf("Load() error = nil, want diagnostic")
		}
		if !strings.Contains(err.Error(), "backend dependencies are not assembled") {
			t.Fatalf("Load() error = %q, want assembly diagnostic", err.Error())
		}
	})

	t.Run("concurrent loads register the route once", func(t *testing.T) {
		hub := newTestHub()
		engine := jin.New()
		db := &gorm.DB{}
		gitSvc := &fakeRepositoryService{}
		repoSvc := gitservice.RepositoryService(gitSvc)
		projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
		repositoryProvisioner := gitservice.RepositoryProvisioner(&fakeRepositoryProvisioner{})
		pluginSourceInspector := gitservice.PluginSourceInspector(&fakePluginSourceInspector{})
		hub.Map(&engine, &db, &repoSvc, &projectionBuilder, &repositoryProvisioner, &pluginSourceInspector)

		m := testMod()
		if err := m.PostInit(hub); err != nil {
			t.Fatalf("PostInit() error = %v", err)
		}

		const callers = 32
		start := make(chan struct{})
		errs := make(chan error, callers)
		for range callers {
			go func() {
				<-start
				errs <- m.Load(hub)
			}()
		}
		close(start)
		for range callers {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent Load() error = %v", err)
			}
		}

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		engine.ServeHTTP(w, req)
		if got, want := w.Code, http.StatusOK; got != want {
			t.Fatalf("status after concurrent loads = %d, want %d", got, want)
		}
	})

	t.Run("second load does not register duplicate route", func(t *testing.T) {
		hub := newTestHub()
		engine := jin.New()
		db := &gorm.DB{}
		gitSvc := &fakeRepositoryService{}
		repoSvc := gitservice.RepositoryService(gitSvc)
		projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
		repositoryProvisioner := gitservice.RepositoryProvisioner(&fakeRepositoryProvisioner{})
		pluginSourceInspector := gitservice.PluginSourceInspector(&fakePluginSourceInspector{})
		hub.Map(&engine, &db, &repoSvc, &projectionBuilder, &repositoryProvisioner, &pluginSourceInspector)

		m := testMod()
		if err := m.PostInit(hub); err != nil {
			t.Fatalf("PostInit() error = %v", err)
		}
		if err := m.Load(hub); err != nil {
			t.Fatalf("first Load() error = %v", err)
		}
		if err := m.Load(hub); err != nil {
			t.Fatalf("second Load() error = %v", err)
		}

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		engine.ServeHTTP(w, req)
		if got, want := w.Code, http.StatusOK; got != want {
			t.Fatalf("status after idempotent load = %d, want %d", got, want)
		}
	})
}

func TestLoadRegistersPluginLifecycleRoutesAndRejectsInvalidBearer(t *testing.T) {
	hub := newTestHub()
	engine := jin.New()
	db := &gorm.DB{}
	repoSvc := gitservice.RepositoryService(&fakeRepositoryService{})
	projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
	repositoryProvisioner := gitservice.RepositoryProvisioner(&fakeRepositoryProvisioner{})
	pluginSourceInspector := gitservice.PluginSourceInspector(&fakePluginSourceInspector{})
	hub.Map(&engine, &db, &repoSvc, &projectionBuilder, &repositoryProvisioner, &pluginSourceInspector)

	mod := testMod()
	mod.pluginLifecycle = &runtimePluginLifecycle{}
	if err := mod.PostInit(hub); err != nil {
		t.Fatalf("PostInit() error = %v", err)
	}
	if err := mod.Load(hub); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	public := httptest.NewRecorder()
	engine.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/research/plugins/scanner", nil))
	if public.Code != http.StatusOK {
		t.Fatalf("anonymous exact Plugin status = %d, body=%s", public.Code, public.Body.String())
	}

	invalid := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/research/plugins/scanner", nil)
	request.Header.Set("Authorization", "Bearer invalid-token")
	engine.ServeHTTP(invalid, request)
	if invalid.Code != http.StatusUnauthorized || invalid.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("invalid Bearer status/challenge = %d/%q, body=%s", invalid.Code, invalid.Header().Get("WWW-Authenticate"), invalid.Body.String())
	}
}

func testMod() *Mod {
	return &Mod{
		config:             Config{JWTSecret: "test JWT secret"},
		environment:        identitymodel.MapEnvironment{identitymodel.APIKeyPepperEnvironment: "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="},
		migrate:            func(*gorm.DB) error { return nil },
		pluginLifecycle:    &runtimePluginLifecycle{},
		receiveCoordinator: &fakeReceiveCoordinator{},
		initializeIdentity: func(_ context.Context, _ *gorm.DB, _ identitymodel.Environment, jwtSecret string) (identityservice.Services, error) {
			repository, _ := identitydao.NewRepository(&gorm.DB{})
			account, _ := identityservice.NewAccountService(repository)
			jwtService, _ := identityservice.NewJWTService(jwtSecret)
			pat := &fakePATAuthenticator{}
			return identityservice.Services{
				Repository:      repository,
				Account:         account,
				Login:           identityservice.NewLoginService(account, jwtService),
				JWT:             jwtService,
				GitPAT:          pat,
				SubscriptionPAT: pat,
			}, nil
		},
	}
}

func assembledMod() *Mod {
	return &Mod{
		jin: jin.New(),
		db:  &gorm.DB{},
		git: &fakeRepositoryService{},
	}
}

type runtimePluginLifecycle struct{}

func (*runtimePluginLifecycle) Create(context.Context, auth.Principal, pluginhandler.CreateInput) (pluginhandler.Plugin, error) {
	return pluginhandler.Plugin{}, errors.New("not implemented")
}
func (*runtimePluginLifecycle) List(context.Context, auth.Principal, string, int, int) (pluginhandler.PluginPage, error) {
	return pluginhandler.PluginPage{}, errors.New("not implemented")
}
func (*runtimePluginLifecycle) Get(_ context.Context, principal auth.Principal, namespace, name string) (pluginhandler.Plugin, error) {
	if !principal.IsAnonymous() || namespace != "research" || name != "scanner" {
		return pluginhandler.Plugin{}, pluginhandler.ErrNotFound
	}
	return pluginhandler.Plugin{Namespace: namespace, Name: name, Status: "active", Visibility: "public", RepositoryStatus: "ready", CloneURL: "/git/research/scanner.git"}, nil
}
func (*runtimePluginLifecycle) Archive(context.Context, auth.Principal, string, string) error {
	return errors.New("not implemented")
}
func (*runtimePluginLifecycle) Restore(context.Context, auth.Principal, string, string) error {
	return errors.New("not implemented")
}
func (*runtimePluginLifecycle) SetVisibility(context.Context, auth.Principal, string, string, string) error {
	return errors.New("not implemented")
}
func (*runtimePluginLifecycle) ListVersions(context.Context, auth.Principal, string, string, int, int) (pluginhandler.VersionPage, error) {
	return pluginhandler.VersionPage{}, errors.New("not implemented")
}
func (*runtimePluginLifecycle) GetVersion(context.Context, auth.Principal, string, string, string) (pluginhandler.Version, error) {
	return pluginhandler.Version{}, errors.New("not implemented")
}
func (*runtimePluginLifecycle) Publish(context.Context, auth.Principal, string, string, pluginhandler.PublishInput) (pluginhandler.Version, error) {
	return pluginhandler.Version{}, errors.New("not implemented")
}
func (*runtimePluginLifecycle) SetDefaultVersion(context.Context, auth.Principal, string, string, string) error {
	return errors.New("not implemented")
}
func (*runtimePluginLifecycle) ClearDefaultVersion(context.Context, auth.Principal, string, string) error {
	return errors.New("not implemented")
}

var _ pluginhandler.Lifecycle = (*runtimePluginLifecycle)(nil)

type fakeReceiveCoordinator struct{}

func (*fakeReceiveCoordinator) Open(context.Context, string, string) (gitservice.ReceiveCoordination, error) {
	return nil, errors.New("not implemented in fake")
}

var _ gitservice.ReceiveCoordinator = (*fakeReceiveCoordinator)(nil)

func newTestHub() *kernel.Hub {
	return &kernel.Hub{
		Injector: inject.New(),
		Log:      zap.NewNop().Sugar(),
	}
}

type fakeProjectionBuilder struct{}

func (*fakeProjectionBuilder) BuildPluginProjection(context.Context, gitservice.BuildPluginProjectionCommand) (gitservice.PluginProjectionResult, error) {
	return gitservice.PluginProjectionResult{}, errors.New("not implemented in fake")
}
func (*fakeProjectionBuilder) BuildMarketplaceProjection(context.Context, gitservice.BuildMarketplaceProjectionCommand) (gitservice.MarketplaceProjectionResult, error) {
	return gitservice.MarketplaceProjectionResult{}, errors.New("not implemented in fake")
}
func (*fakeProjectionBuilder) RemoveProjection(context.Context, gitservice.ImmutableProjection) error {
	return errors.New("not implemented in fake")
}
func (*fakeProjectionBuilder) VerifyProjection(context.Context, gitservice.ImmutableProjection, string) error {
	return errors.New("not implemented in fake")
}

var _ gitservice.ProjectionBuilder = (*fakeProjectionBuilder)(nil)

type fakePATAuthenticator struct{}

func (*fakePATAuthenticator) AuthenticateGitPAT(context.Context, string, string, auth.GitOperation) (auth.Principal, error) {
	return auth.Principal{}, errors.New("not implemented in fake")
}

func (*fakePATAuthenticator) AuthenticateSubscriptionPAT(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{}, errors.New("not implemented in fake")
}

var (
	_ auth.GitPATAuthenticator          = (*fakePATAuthenticator)(nil)
	_ auth.SubscriptionPATAuthenticator = (*fakePATAuthenticator)(nil)
)

type fakeRepositoryProvisioner struct{}

func (*fakeRepositoryProvisioner) ProvisionRepository(_ context.Context, identity gitservice.RepositoryIdentity) (gitservice.ProvisionedRepository, error) {
	return gitservice.NewProvisionedRepository(identity, "test-receipt"), nil
}

func (*fakeRepositoryProvisioner) RemoveProvisionedRepository(context.Context, gitservice.ProvisionedRepository) error {
	return nil
}

func (*fakeRepositoryProvisioner) ListRepositoryOrphanCandidates(context.Context, time.Duration) ([]gitservice.RepositoryOrphanCandidate, error) {
	return nil, nil
}

type fakePluginSourceInspector struct{}

func (*fakePluginSourceInspector) InspectPluginSource(context.Context, string, string, string) (gitservice.PluginSourceInspection, error) {
	return gitservice.PluginSourceInspection{}, errors.New("not implemented in fake")
}

var (
	_ gitservice.RepositoryProvisioner = (*fakeRepositoryProvisioner)(nil)
	_ gitservice.PluginSourceInspector = (*fakePluginSourceInspector)(nil)
)

type fakeRepositoryService struct{}

func (*fakeRepositoryService) InitBareRepository(context.Context, string) error {
	return errors.New("not implemented in fake")
}

func (*fakeRepositoryService) AdvertiseRefs(context.Context, string, string, io.Writer, io.Writer) error {
	return errors.New("not implemented in fake")
}

func (*fakeRepositoryService) UploadPack(context.Context, string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("not implemented in fake")
}

func (*fakeRepositoryService) ReceivePack(context.Context, string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("not implemented in fake")
}

var _ gitservice.RepositoryService = (*fakeRepositoryService)(nil)
