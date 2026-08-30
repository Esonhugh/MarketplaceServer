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
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	identityservice "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/service"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	pluginreceive "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin/receive"
	distributionhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/distribution"
	identityhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/identity"
	pluginhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/plugin"
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

type Config struct {
	JWTSecret string `yaml:"jwtSecret" mapstructure:"jwtSecret"`
}

type Mod struct {
	kernel.UnimplementedModule

	config                 Config
	jin                    *jin.Engine
	db                     *gorm.DB
	git                    gitservice.RepositoryService
	projectionBuilder      gitservice.ProjectionBuilder
	distributionRepository distributiondomain.RepositoryStore
	publicationService     *distributiondomain.PublicationService
	accessService          *distributiondomain.AccessService
	userMarketplaceService *distributiondomain.UserMarketplaceService
	tokenService           *identityservice.TokenService
	loginService           *identityservice.LoginService
	managementAuth         identityhandler.ManagementAuthenticator
	subscriptionPAT        auth.SubscriptionPATAuthenticator
	pluginLifecycle        pluginhandler.Lifecycle
	receiveCoordinator     gitservice.ReceiveCoordinator
	environment            identitymodel.Environment
	initializeIdentity     func(context.Context, *gorm.DB, identitymodel.Environment, string) (identityservice.Services, error)
	migrate                func(*gorm.DB) error
	loadOnce               sync.Once
}

func (m *Mod) Name() string { return "backend" }

func (m *Mod) Config() any { return &m.config }

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

	var repositoryProvisioner gitservice.RepositoryProvisioner
	if err := hub.Load(&repositoryProvisioner); err != nil {
		return fmt.Errorf("backend dependency gitservice.RepositoryProvisioner not available: %w", err)
	}
	if isNil(repositoryProvisioner) {
		return fmt.Errorf("backend dependency gitservice.RepositoryProvisioner not available: nil")
	}

	var pluginSourceInspector gitservice.PluginSourceInspector
	if err := hub.Load(&pluginSourceInspector); err != nil {
		return fmt.Errorf("backend dependency gitservice.PluginSourceInspector not available: %w", err)
	}
	if isNil(pluginSourceInspector) {
		return fmt.Errorf("backend dependency gitservice.PluginSourceInspector not available: nil")
	}

	environment := m.environment
	if environment == nil {
		environment = identitymodel.OSEnvironment{}
	}
	jwtSecret, err := identityservice.JWTSecret(environment, m.config.JWTSecret)
	if err != nil {
		return fmt.Errorf("backend JWT configuration invalid: %w", err)
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
	initializeIdentity := m.initializeIdentity
	if initializeIdentity == nil {
		initializeIdentity = identityservice.Initialize
	}
	identityServices, err := initializeIdentity(context.Background(), db, environment, jwtSecret)
	if err != nil {
		return fmt.Errorf("assemble identity services: %w", err)
	}
	if identityServices.Repository == nil || identityServices.Account == nil || identityServices.Login == nil || identityServices.JWT == nil ||
		isNil(identityServices.GitPAT) || isNil(identityServices.SubscriptionPAT) {
		return fmt.Errorf("assemble identity services: incomplete service set")
	}
	stateReader, err := authorizationdomain.NewGORMIdentityStateReader(db, identityServices.Repository)
	if err != nil {
		return fmt.Errorf("assemble authorization state reader: %w", err)
	}
	authorizer := authorizationdomain.NewPolicy(stateReader)
	pepper, err := identitymodel.APIKeyPepper(environment)
	if err != nil {
		return fmt.Errorf("load API key pepper for token service: %w", err)
	}
	tokenService, err := identityservice.NewTokenService(identityServices.Repository, authorizer, pepper)
	if err != nil {
		return fmt.Errorf("assemble token service: %w", err)
	}
	pluginService, err := plugindomain.NewService(db, authorizer, repositoryProvisioner, pluginSourceInspector)
	if err != nil {
		return fmt.Errorf("assemble plugin lifecycle service: %w", err)
	}
	pluginLifecycle := m.pluginLifecycle
	if isNil(pluginLifecycle) {
		pluginLifecycle = pluginhandler.NewDomainLifecycleAdapter(pluginService)
	}
	if isNil(pluginLifecycle) {
		return fmt.Errorf("assemble plugin lifecycle handler adapter: domain lifecycle service is incomplete")
	}
	receiveCoordinator := m.receiveCoordinator
	if isNil(receiveCoordinator) {
		coordinator, err := pluginreceive.NewCoordinator(db, projectionBuilder, pluginreceive.Options{})
		if err != nil {
			return fmt.Errorf("assemble plugin receive coordinator: %w", err)
		}
		receiveCoordinator = coordinator
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
	m.loginService = identityServices.Login
	m.managementAuth = identityhandler.NewManagementAuthenticator(identityServices.JWT, identityServices.Account)
	m.subscriptionPAT = identityServices.SubscriptionPAT
	m.pluginLifecycle = pluginLifecycle
	m.receiveCoordinator = receiveCoordinator
	var repositoryResolver gitservice.RepositoryResolver = m
	var distributionResolver distributionservice.Resolver = accessService
	var gitPATAuthenticator auth.GitPATAuthenticator = identityServices.GitPAT
	var policyAuthorizer auth.Authorizer = authorizer
	var protectedReceive gitservice.ReceiveCoordinator = receiveCoordinator
	hub.Map(&repositoryResolver, &distributionResolver, &gitPATAuthenticator, &policyAuthorizer, &protectedReceive)
	return nil
}

func (m *Mod) Resolve(ctx context.Context, namespace, repository string) (gitservice.Repository, error) {
	if m.db == nil || m.db.Config == nil || isNil(m.git) || namespace == "" || repository == "" {
		return gitservice.Repository{}, gitservice.ErrRepositoryUnavailable
	}
	return m.resolveRepository(ctx, namespace, repository)
}

func (m *Mod) resolveRepository(ctx context.Context, namespaceSlug, pluginSlug string) (gitservice.Repository, error) {
	// The Git route resolves the Plugin aggregate. The hidden Repository contributes
	// only its shared storage identity and operational status.
	var row struct {
		ID          string
		NamespaceID string
		OwnerUserID *string
		Slug        string
		Visibility  string
		Status      string
	}
	err := m.db.WithContext(ctx).
		Table("plugins").
		Select("plugins.id, plugins.namespace_id, namespaces.owner_user_id, plugins.slug, plugins.visibility, repositories.status").
		Joins("JOIN repositories ON repositories.id = plugins.id").
		Joins("JOIN namespaces ON namespaces.id = plugins.namespace_id").
		Where("namespaces.slug = ? AND plugins.slug = ?", namespaceSlug, pluginSlug).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return gitservice.Repository{}, gitservice.ErrRepositoryNotFound
	}
	if err != nil {
		return gitservice.Repository{}, fmt.Errorf("%w: resolve Plugin repository metadata: %v", gitservice.ErrRepositoryUnavailable, err)
	}
	ownerUserID := ""
	if row.OwnerUserID != nil {
		ownerUserID = *row.OwnerUserID
	}
	return gitservice.Repository{
		ID:          row.ID,
		NamespaceID: row.NamespaceID,
		OwnerUserID: ownerUserID,
		Slug:        row.Slug,
		Visibility:  row.Visibility,
		Status:      row.Status,
	}, nil
}

func (m *Mod) Load(_ *kernel.Hub) error {
	if m.jin == nil || m.db == nil || isNil(m.git) || m.tokenService == nil || m.loginService == nil ||
		m.userMarketplaceService == nil || isNil(m.managementAuth) || isNil(m.subscriptionPAT) || isNil(m.pluginLifecycle) || isNil(m.receiveCoordinator) {
		return fmt.Errorf("backend dependencies are not assembled; call PostInit after jin, sql, and git dependencies are mapped")
	}

	m.loadOnce.Do(func() {
		m.jin.GET("/api/v1/health", m.handleHealth)
		distributionhandler.NewMarketplaceJSONHandler(m.accessService).Register(m.jin)
		distributionhandler.NewUserMarketplaceJSONHandler(m.subscriptionPAT, m.userMarketplaceService).Register(m.jin)
		identityhandler.NewLoginHandler(m.loginService).Register(m.jin)
		identityhandler.NewTokenHandler(m.tokenService, m.managementAuth).Register(m.jin)
		pluginhandler.NewHandler(m.pluginLifecycle, m.managementAuth).Register(m.jin)
	})
	return nil
}

func (m *Mod) handleHealth(c *jin.Context) {
	c.Render(http.StatusOK, render.JSON{Data: api.Success(api.NewHealth(
		api.HealthStatusOK,
		api.HealthCheck{Name: "backend", Status: api.HealthStatusOK},
	))})
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
