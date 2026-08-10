package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	authorizationdomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/authorization"
	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	distributionhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/distribution"
	identityhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/identity"
	"github.com/Esonhugh/MarketplaceServer/pkg/api"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
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
	userMarketplaceService *distributiondomain.UserMarketplaceService
	tokenService           *identitydomain.TokenService
	basicAuthenticator     auth.BasicAuthenticator
	environment            identitydomain.Environment
	initializeIdentity     func(context.Context, *gorm.DB, identitydomain.Environment) (identitydomain.Services, error)
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
		migrate = Migrate
	}
	if err := migrate(db); err != nil {
		return fmt.Errorf("backend database migration failed: %w", err)
	}
	distributionRepository, err := distributiondomain.NewGORMRepository(db)
	if err != nil {
		return fmt.Errorf("assemble distribution repository: %w", err)
	}
	environment := m.environment
	if environment == nil {
		environment = identitydomain.OSEnvironment{}
	}
	initializeIdentity := m.initializeIdentity
	if initializeIdentity == nil {
		initializeIdentity = identitydomain.Initialize
	}
	identityServices, err := initializeIdentity(context.Background(), db, environment)
	if err != nil {
		return fmt.Errorf("assemble identity services: %w", err)
	}
	if identityServices.Repository == nil {
		return fmt.Errorf("assemble identity services: nil repository")
	}
	if isNil(identityServices.Authenticator) {
		return fmt.Errorf("assemble identity services: nil basic authenticator")
	}
	stateReader, err := authorizationdomain.NewGORMIdentityStateReader(db, identityServices.Repository)
	if err != nil {
		return fmt.Errorf("assemble authorization state reader: %w", err)
	}
	authorizer := authorizationdomain.NewPolicy(stateReader)
	pepper, err := identitydomain.APIKeyPepper(environment)
	if err != nil {
		return fmt.Errorf("load API key pepper for token service: %w", err)
	}
	tokenService, err := identitydomain.NewTokenService(identityServices.Repository, authorizer, pepper)
	if err != nil {
		return fmt.Errorf("assemble token service: %w", err)
	}

	accessService := distributiondomain.NewAccessService(distributionRepository)
	publicationService := distributiondomain.NewPublicationService(distributionRepository, projectionBuilder)
	userMarketplaceRepository, err := distributiondomain.NewGORMUserMarketplaceRepository(db)
	if err != nil {
		return fmt.Errorf("assemble user marketplace repository: %w", err)
	}
	userMarketplaceService := distributiondomain.NewUserMarketplaceService(userMarketplaceRepository, authorizer)

	m.jin = engine
	m.db = db
	m.git = repoService
	m.projectionBuilder = projectionBuilder
	m.distributionRepository = distributionRepository
	m.publicationService = publicationService
	m.accessService = accessService
	m.userMarketplaceService = userMarketplaceService
	m.tokenService = tokenService
	m.basicAuthenticator = identityServices.Authenticator
	var repositoryResolver gitservice.RepositoryResolver = m
	var distributionResolver distributionservice.Resolver = accessService
	var basicAuthenticator auth.BasicAuthenticator = identityServices.Authenticator
	var policyAuthorizer auth.Authorizer = authorizer
	hub.Map(&repositoryResolver, &distributionResolver, &basicAuthenticator, &policyAuthorizer)
	return nil
}

func (m *Mod) Resolve(ctx context.Context, namespace, repository string) (gitservice.Repository, error) {
	if m.db == nil || m.db.Config == nil || isNil(m.git) || namespace == "" || repository == "" {
		return gitservice.Repository{}, gitservice.ErrRepositoryUnavailable
	}
	return m.resolveRepository(ctx, namespace, repository)
}

func (m *Mod) resolveRepository(ctx context.Context, namespaceSlug, repositorySlug string) (gitservice.Repository, error) {
	// Resolve through the namespace relation rather than by repository slug alone.
	// Visibility and status intentionally remain unfiltered here: transport policy
	// must decide whether this resolved resource is readable or writable.
	var row struct {
		ID          string
		NamespaceID string
		OwnerUserID *string
		Visibility  string
		Status      string
	}
	err := m.db.WithContext(ctx).
		Table("repositories").
		Select("repositories.id, repositories.namespace_id, namespaces.owner_user_id, repositories.visibility, repositories.status").
		Joins("JOIN namespaces ON namespaces.id = repositories.namespace_id").
		Where("namespaces.slug = ? AND repositories.slug = ?", namespaceSlug, repositorySlug).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return gitservice.Repository{}, gitservice.ErrRepositoryNotFound
	}
	if err != nil {
		return gitservice.Repository{}, fmt.Errorf("%w: resolve development repository metadata: %v", gitservice.ErrRepositoryUnavailable, err)
	}
	ownerUserID := ""
	if row.OwnerUserID != nil {
		ownerUserID = *row.OwnerUserID
	}
	return gitservice.Repository{
		ID:          row.ID,
		NamespaceID: row.NamespaceID,
		OwnerUserID: ownerUserID,
		Visibility:  row.Visibility,
		Status:      row.Status,
	}, nil
}

func (m *Mod) Load(_ *kernel.Hub) error {
	if m.jin == nil || m.db == nil || isNil(m.git) || m.tokenService == nil || m.userMarketplaceService == nil || isNil(m.basicAuthenticator) {
		return fmt.Errorf("backend dependencies are not assembled; call PostInit after jin, sql, and git dependencies are mapped")
	}

	m.loadOnce.Do(func() {
		m.jin.GET("/api/v1/health", m.handleHealth)
		distributionhandler.NewMarketplaceJSONHandler(m.accessService).Register(m.jin)
		distributionhandler.NewUserMarketplaceJSONHandler(m.basicAuthenticator, m.userMarketplaceService).Register(m.jin)
		identityhandler.NewTokenHandler(m.tokenService, m.basicAuthenticator).Register(m.jin)
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
