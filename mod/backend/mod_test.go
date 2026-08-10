package backend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/juanjiTech/inject/v2"
	"github.com/juanjiTech/jin"
	"go.uber.org/zap"
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

func TestConfigIsAbsentUntilBackendHasConfiguration(t *testing.T) {
	if got := (&Mod{}).Config(); got != nil {
		t.Fatalf("Config() = %#v, want nil for the configuration-free skeleton", got)
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

func TestPostInitStopsBeforeAssemblyWhenMigrationFails(t *testing.T) {
	hub := newTestHub()
	engine := jin.New()
	db := &gorm.DB{}
	gitSvc := &fakeRepositoryService{}
	repoSvc := gitservice.RepositoryService(gitSvc)
	projectionBuilder := gitservice.ProjectionBuilder(&fakeProjectionBuilder{})
	hub.Map(&engine, &db, &repoSvc, &projectionBuilder)

	m := &Mod{migrate: func(*gorm.DB) error { return errors.New("migration failed") }}
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
	hub.Map(&engine, &db, &repoSvc, &projectionBuilder)

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
	hub.Map(&engine, &db, &repoSvc, &projectionBuilder)

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
	if got, want := w.Body.String(), `{"status":"ok","checks":[{"name":"backend","status":"ok"}]}`; got != want {
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
		hub.Map(&engine, &db, &repoSvc, &projectionBuilder)

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
		hub.Map(&engine, &db, &repoSvc, &projectionBuilder)

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

func testMod() *Mod {
	return &Mod{
		environment: identitydomain.MapEnvironment{identitydomain.APIKeyPepperEnvironment: "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="},
		migrate:     func(*gorm.DB) error { return nil },
		initializeIdentity: func(context.Context, *gorm.DB, identitydomain.Environment) (identitydomain.Services, error) {
			repository, _ := identitydomain.NewRepository(&gorm.DB{})
			return identitydomain.Services{Repository: repository, Authenticator: &fakeBasicAuthenticator{}}, nil
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

type fakeBasicAuthenticator struct{}

func (*fakeBasicAuthenticator) AuthenticateBasic(context.Context, string, string) (auth.Principal, error) {
	return auth.Principal{}, errors.New("not implemented in fake")
}

var _ auth.BasicAuthenticator = (*fakeBasicAuthenticator)(nil)

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
