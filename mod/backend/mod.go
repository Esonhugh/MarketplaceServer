package backend

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	distributionhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/distribution"
	"github.com/Esonhugh/MarketplaceServer/pkg/api"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/juanjiTech/jin"
	"github.com/juanjiTech/jin/render"
	"gorm.io/gorm"
)

var (
	_ kernel.Module                 = (*Mod)(nil)
	_ gitservice.RepositoryResolver = (*Mod)(nil)
)

type Mod struct {
	kernel.UnimplementedModule

	jin                    *jin.Engine
	db                     *gorm.DB
	git                    gitservice.RepositoryService
	projectionBuilder      gitservice.ProjectionBuilder
	distributionRepository distributiondomain.RepositoryStore
	publicationService     *distributiondomain.PublicationService
	accessService          *distributiondomain.AccessService
	migrate                func(*gorm.DB) error
	loadOnce               sync.Once
}

func (m *Mod) Name() string { return "backend" }

func (m *Mod) Config() any { return nil }

func (m *Mod) PostInit(hub *kernel.Hub) error {
	var engine *jin.Engine
	if err := hub.Load(&engine); err != nil {
		return fmt.Errorf("backend dependency *jin.Engine not available: %w", err)
	}
	if engine == nil {
		return fmt.Errorf("backend dependency *jin.Engine not available: nil")
	}

	var db *gorm.DB
	if err := hub.Load(&db); err != nil {
		return fmt.Errorf("backend dependency *gorm.DB not available: %w", err)
	}
	if db == nil {
		return fmt.Errorf("backend dependency *gorm.DB not available: nil")
	}

	var repoService gitservice.RepositoryService
	if err := hub.Load(&repoService); err != nil {
		return fmt.Errorf("backend dependency gitservice.RepositoryService not available: %w", err)
	}
	if isNil(repoService) {
		return fmt.Errorf("backend dependency gitservice.RepositoryService not available: nil")
	}

	var projectionBuilder gitservice.ProjectionBuilder
	if err := hub.Load(&projectionBuilder); err != nil {
		return fmt.Errorf("backend dependency gitservice.ProjectionBuilder not available: %w", err)
	}
	if isNil(projectionBuilder) {
		return fmt.Errorf("backend dependency gitservice.ProjectionBuilder not available: nil")
	}

	migrate := m.migrate
	if migrate == nil {
		migrate = distributiondomain.Migrate
	}
	if err := migrate(db); err != nil {
		return fmt.Errorf("backend database migration failed: %w", err)
	}
	distributionRepository, err := distributiondomain.NewGORMRepository(db)
	if err != nil {
		return fmt.Errorf("assemble distribution repository: %w", err)
	}

	accessService := distributiondomain.NewAccessService(distributionRepository)
	publicationService := distributiondomain.NewPublicationService(distributionRepository, projectionBuilder)

	m.jin = engine
	m.db = db
	m.git = repoService
	m.projectionBuilder = projectionBuilder
	m.distributionRepository = distributionRepository
	m.publicationService = publicationService
	m.accessService = accessService
	var repositoryResolver gitservice.RepositoryResolver = m
	var distributionResolver distributionservice.Resolver = accessService
	hub.Map(&repositoryResolver, &distributionResolver)
	return nil
}

func (m *Mod) Resolve(ctx context.Context, namespace, repository string) (gitservice.Repository, error) {
	if m.db == nil || isNil(m.git) || namespace == "" || repository == "" {
		return gitservice.Repository{}, gitservice.ErrRepositoryUnavailable
	}
	return m.resolveRepository(ctx, namespace, repository)
}

func (m *Mod) resolveRepository(context.Context, string, string) (gitservice.Repository, error) {
	// TODO: resolve repository metadata through the backend repository DAO and authorization service.
	return gitservice.Repository{}, gitservice.ErrRepositoryUnavailable
}

func (m *Mod) Load(_ *kernel.Hub) error {
	if m.jin == nil || m.db == nil || isNil(m.git) {
		return fmt.Errorf("backend dependencies are not assembled; call PostInit after jin, sql, and git dependencies are mapped")
	}

	m.loadOnce.Do(func() {
		m.jin.GET("/api/v1/health", m.handleHealth)
		distributionhandler.NewMarketplaceJSONHandler(m.accessService).Register(m.jin)
	})
	return nil
}

func (m *Mod) handleHealth(c *jin.Context) {
	c.Render(http.StatusOK, render.JSON{Data: api.NewHealth(
		api.HealthStatusOK,
		api.HealthCheck{Name: "backend", Status: api.HealthStatusOK},
	)})
}

func isNil(value any) bool {
	if value == nil {
		return true
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
