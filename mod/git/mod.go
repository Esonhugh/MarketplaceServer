package git

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	jinengine "github.com/juanjiTech/jin"
	"go.uber.org/zap"
)

var _ kernel.Module = (*Mod)(nil)

const (
	defaultGitBinary        = "git"
	defaultValidatorBinary  = "claude"
	defaultMaxRequestBytes  = int64(100 << 20)
	defaultAdvertiseTimeout = 15 * time.Second
	defaultServiceTimeout   = 5 * time.Minute
)

type Config struct {
	StorageRoot     string `yaml:"storageRoot" mapstructure:"storageRoot"`
	ValidatorBinary string `yaml:"validatorBinary" mapstructure:"validatorBinary"`

	gitBinary        string
	maxRequestBytes  int64
	advertiseTimeout time.Duration
	serviceTimeout   time.Duration
}

type Mod struct {
	kernel.UnimplementedModule

	config                  Config
	service                 *Service
	repositoryService       gitservice.RepositoryService
	repositoryProvisioner   gitservice.RepositoryProvisioner
	repositoryOrphanCleaner gitservice.RepositoryOrphanCleaner
	repositoryBrowser       gitservice.RepositoryBrowser
	distributionReader      gitservice.DistributionReader
	projectionBuilder       gitservice.ProjectionBuilder
	pluginSourceInspector   gitservice.PluginSourceInspector
	pluginRefReader         gitservice.PluginRefReader
	receiveCoordinator      gitservice.ReceiveCoordinator
	distributionResolver    distributionservice.Resolver
	resolver                gitservice.RepositoryResolver
	gitPATAuthenticator     auth.GitPATAuthenticator
	authorizer              auth.Authorizer
	log                     *zap.SugaredLogger
}

func (m *Mod) Name() string { return "git" }

func (m *Mod) Config() any {
	m.applyDefaults()
	return &m.config
}

func (m *Mod) Init(hub *kernel.Hub) error {
	m.applyDefaults()
	if err := m.validateConfig(); err != nil {
		return err
	}
	m.log = hub.Log
	svc, err := NewService(m.config)
	if err != nil {
		return err
	}
	inspector, err := NewPluginSourceInspector(svc, m.config.ValidatorBinary)
	if err != nil {
		return err
	}
	m.service = svc
	m.repositoryService = svc
	m.repositoryProvisioner = svc
	m.repositoryOrphanCleaner = svc
	m.repositoryBrowser = svc
	m.distributionReader = svc
	m.projectionBuilder = svc
	m.pluginSourceInspector = inspector
	m.pluginRefReader = svc
	hub.Map(
		&m.repositoryService,
		&m.repositoryProvisioner,
		&m.repositoryOrphanCleaner,
		&m.repositoryBrowser,
		&m.distributionReader,
		&m.projectionBuilder,
		&m.pluginSourceInspector,
		&m.pluginRefReader,
	)
	return nil
}

func (m *Mod) Load(hub *kernel.Hub) error {
	var engine *jinengine.Engine
	if err := hub.Load(&engine); err != nil {
		return errors.New("can't load jin.Engine from kernel")
	}
	if m.service == nil {
		return errors.New("git service is not initialized")
	}
	var resolver gitservice.RepositoryResolver
	if err := hub.Load(&resolver); err != nil {
		return errors.New("can't load gitservice.RepositoryResolver from kernel")
	}
	if nilInterface(resolver) {
		return errors.New("gitservice.RepositoryResolver from kernel is nil")
	}
	var gitPATAuthenticator auth.GitPATAuthenticator
	if err := hub.Load(&gitPATAuthenticator); err != nil {
		return errors.New("can't load auth.GitPATAuthenticator from kernel")
	}
	if nilInterface(gitPATAuthenticator) {
		return errors.New("auth.GitPATAuthenticator from kernel is nil")
	}
	var authorizer auth.Authorizer
	if err := hub.Load(&authorizer); err != nil {
		return errors.New("can't load auth.Authorizer from kernel")
	}
	if nilInterface(authorizer) {
		return errors.New("auth.Authorizer from kernel is nil")
	}
	var distributionResolver distributionservice.Resolver
	if err := hub.Load(&distributionResolver); err != nil {
		return errors.New("can't load distributionservice.Resolver from kernel")
	}
	if nilInterface(distributionResolver) {
		return errors.New("distributionservice.Resolver from kernel is nil")
	}
	var receiveCoordinator gitservice.ReceiveCoordinator
	if err := hub.Load(&receiveCoordinator); err != nil {
		return errors.New("can't load gitservice.ReceiveCoordinator from kernel")
	}
	if nilInterface(receiveCoordinator) {
		return errors.New("gitservice.ReceiveCoordinator from kernel is nil")
	}
	m.receiveCoordinator = receiveCoordinator
	m.resolver = resolver
	m.gitPATAuthenticator = gitPATAuthenticator
	m.authorizer = authorizer
	m.distributionResolver = distributionResolver
	m.registerSmartHTTPRoutes(engine)
	m.registerDistributionRoutes(engine)
	return nil
}

func nilInterface(value any) bool {
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

func (m *Mod) applyDefaults() {
	if m.config.gitBinary == "" {
		m.config.gitBinary = defaultGitBinary
	}
	if m.config.ValidatorBinary == "" {
		m.config.ValidatorBinary = defaultValidatorBinary
	}
	if m.config.maxRequestBytes == 0 {
		m.config.maxRequestBytes = defaultMaxRequestBytes
	}
	if m.config.advertiseTimeout == 0 {
		m.config.advertiseTimeout = defaultAdvertiseTimeout
	}
	if m.config.serviceTimeout == 0 {
		m.config.serviceTimeout = defaultServiceTimeout
	}
}

func (m *Mod) validateConfig() error {
	if m.config.maxRequestBytes < 0 {
		return errors.New("git request byte limit must not be negative")
	}
	if m.config.advertiseTimeout < 0 {
		return errors.New("git advertise timeout must not be negative")
	}
	if m.config.serviceTimeout < 0 {
		return errors.New("git service timeout must not be negative")
	}
	return nil
}

func (m *Mod) registerSmartHTTPRoutes(engine *jinengine.Engine) {
	engine.GET("/git/:namespace/:repository/info/refs", m.handleInfoRefs)
	engine.POST("/git/:namespace/:repository/git-upload-pack", m.handleUploadPack)
	engine.POST("/git/:namespace/:repository/git-receive-pack", m.handleReceivePack)
}

func (m *Mod) handleInfoRefs(c *jinengine.Context) {
	namespace, repositorySlug, ok := routeRepository(c)
	if !ok {
		writePlain(c, http.StatusNotFound, "repository not found\n")
		return
	}
	service := c.Request.URL.Query().Get("service")
	var action auth.Action
	var contentType string
	switch service {
	case "git-upload-pack":
		action, contentType = auth.ActionPluginRead, "application/x-git-upload-pack-advertisement"
	case "git-receive-pack":
		action, contentType = auth.ActionPluginWrite, "application/x-git-receive-pack-advertisement"
	default:
		writePlain(c, http.StatusNotFound, "unsupported git service\n")
		return
	}
	repository, requestContext, ok := m.authorizeRepository(c, namespace, repositorySlug, action, gitOperationForAction(action))
	if !ok {
		return
	}
	setGitHeaders(c, contentType)
	if err := m.service.AdvertiseRefs(requestContext, service, repository.ID, c.Writer, io.Discard); err != nil {
		m.handleGitError(c, err)
	}
}

func (m *Mod) handleUploadPack(c *jinengine.Context) {
	m.handleServiceRPC(c, "git-upload-pack", auth.ActionPluginRead, "application/x-git-upload-pack-result", func(ctx context.Context, repositoryID string, body io.Reader, stdout io.Writer) error {
		return m.service.UploadPack(ctx, repositoryID, body, stdout, io.Discard)
	})
}

func (m *Mod) handleReceivePack(c *jinengine.Context) {
	namespace, repositorySlug, ok := routeRepository(c)
	if !ok {
		writePlain(c, http.StatusNotFound, "repository not found\n")
		return
	}
	repository, requestContext, ok := m.authorizeRepository(c, namespace, repositorySlug, auth.ActionPluginWrite, auth.GitOperationWrite)
	if !ok || !contentLengthWithinLimit(c, m.config.maxRequestBytes) {
		return
	}
	if nilInterface(m.receiveCoordinator) {
		writePlain(c, http.StatusServiceUnavailable, "protected receive unavailable\n")
		return
	}
	if err := m.service.InstallProtectedReceiveHooks(requestContext, repository.ID); err != nil {
		writePlain(c, http.StatusServiceUnavailable, "protected receive unavailable\n")
		return
	}
	setGitHeaders(c, "application/x-git-receive-pack-result")
	body := io.Reader(c.Request.Body)
	if m.config.maxRequestBytes > 0 {
		body = http.MaxBytesReader(c.Writer, c.Request.Body, m.config.maxRequestBytes)
	}
	requestContext = context.WithValue(requestContext, receiveContextKey{}, receiveContext{
		coordinator:  m.receiveCoordinator,
		inspector:    m.pluginSourceInspector,
		repositoryID: repository.ID,
		pluginName:   repositorySlug,
	})
	if err := m.service.ReceivePack(requestContext, repository.ID, body, c.Writer, io.Discard); err != nil {
		m.handleGitError(c, err)
	}
}

type receiveContextKey struct{}

type receiveContext struct {
	coordinator  gitservice.ReceiveCoordinator
	inspector    gitservice.PluginSourceInspector
	repositoryID string
	pluginName   string
}

type gitRPC func(context.Context, string, io.Reader, io.Writer) error

func (m *Mod) handleServiceRPC(c *jinengine.Context, service string, action auth.Action, contentType string, run gitRPC) {
	namespace, repositorySlug, ok := routeRepository(c)
	if !ok {
		writePlain(c, http.StatusNotFound, "repository not found\n")
		return
	}
	repository, requestContext, ok := m.authorizeRepository(c, namespace, repositorySlug, action, gitOperationForAction(action))
	if !ok || !contentLengthWithinLimit(c, m.config.maxRequestBytes) {
		return
	}
	setGitHeaders(c, contentType)
	body := io.Reader(c.Request.Body)
	if m.config.maxRequestBytes > 0 {
		body = http.MaxBytesReader(c.Writer, c.Request.Body, m.config.maxRequestBytes)
	}
	if err := run(requestContext, repository.ID, body, c.Writer); err != nil {
		m.handleGitError(c, err)
	}
}

// authorizeRepository resolves resource metadata before policy evaluation. Invalid
// supplied credentials never downgrade to anonymous access, and no subprocess is
// started until readiness and authorization both succeed.
func (m *Mod) authorizeRepository(c *jinengine.Context, namespace, repositorySlug string, action auth.Action, operation auth.GitOperation) (gitservice.Repository, context.Context, bool) {
	repository, err := m.resolver.Resolve(c.Request.Context(), namespace, repositorySlug)
	if err != nil || validateRepositoryID(repository.ID) != nil || !repositoryAllowsAction(repository, action) {
		writePlain(c, http.StatusNotFound, "repository not found\n")
		return gitservice.Repository{}, nil, false
	}
	principal, supplied, err := m.authenticateRequest(c.Request, operation)
	if err != nil {
		writeAuthenticationRequired(c)
		return gitservice.Repository{}, nil, false
	}
	if action == auth.ActionPluginWrite && !supplied {
		writeAuthenticationRequired(c)
		return gitservice.Repository{}, nil, false
	}
	if action == auth.ActionPluginRead && !supplied && repository.Visibility != gitservice.VisibilityPublic {
		writeAuthenticationRequired(c)
		return gitservice.Repository{}, nil, false
	}
	requestContext := auth.ContextWithPrincipal(c.Request.Context(), principal)
	resource := auth.ResourceRef{Type: auth.ResourcePlugin, ID: repository.ID, NamespaceID: repository.NamespaceID}
	if err := m.authorizer.Authorize(requestContext, principal, action, resource); err != nil {
		if !supplied {
			writeAuthenticationRequired(c)
		} else {
			writePlain(c, http.StatusForbidden, "repository access denied\n")
		}
		return gitservice.Repository{}, nil, false
	}
	return repository, requestContext, true
}

func gitOperationForAction(action auth.Action) auth.GitOperation {
	if action == auth.ActionPluginWrite {
		return auth.GitOperationWrite
	}
	return auth.GitOperationRead
}

func repositoryAllowsAction(repository gitservice.Repository, action auth.Action) bool {
	switch action {
	case auth.ActionPluginRead:
		return repository.Status == gitservice.StatusReady || repository.Status == gitservice.StatusReadOnly
	case auth.ActionPluginWrite:
		return repository.Status == gitservice.StatusReady
	default:
		return false
	}
}

func (m *Mod) authenticateRequest(request *http.Request, operation auth.GitOperation) (auth.Principal, bool, error) {
	header := request.Header.Get("Authorization")
	if header == "" {
		return auth.AnonymousPrincipal(), false, nil
	}
	username, plaintext, ok := request.BasicAuth()
	if !ok || username == "" || plaintext == "" {
		return auth.Principal{}, true, errors.New("invalid git credentials")
	}
	principal, err := m.gitPATAuthenticator.AuthenticateGitPAT(request.Context(), username, plaintext, operation)
	if err != nil || !principal.IsUser() || principal.CredentialKind() != auth.CredentialPAT {
		return auth.Principal{}, true, errors.New("invalid git credentials")
	}
	return principal, true, nil
}

func writeAuthenticationRequired(c *jinengine.Context) {
	c.Writer.Header().Set("WWW-Authenticate", `Basic realm="MarketplaceServer Git"`)
	writePlain(c, http.StatusUnauthorized, "authentication required\n")
}

func routeRepository(c *jinengine.Context) (string, string, bool) {
	namespace := c.Params.ByName("namespace")
	repositoryParam := c.Params.ByName("repository")
	if namespace == "" || repositoryParam == "" || !strings.HasSuffix(repositoryParam, ".git") {
		return "", "", false
	}
	repository := strings.TrimSuffix(repositoryParam, ".git")
	if repository == "" {
		return "", "", false
	}
	if err := validateSlug(namespace); err != nil {
		return "", "", false
	}
	if err := validateSlug(repository); err != nil {
		return "", "", false
	}
	return namespace, repository, true
}

func setGitHeaders(c *jinengine.Context, contentType string) {
	header := c.Writer.Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "no-cache, max-age=0, must-revalidate")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	header.Set("X-Content-Type-Options", "nosniff")
}

func writePlain(c *jinengine.Context, status int, body string) {
	header := c.Writer.Header()
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	c.Writer.WriteHeader(status)
	_, _ = c.Writer.WriteString(body)
}

func (m *Mod) handleGitError(c *jinengine.Context, err error) {
	if errors.Is(err, ErrInvalidSlug) || errors.Is(err, ErrInvalidRepositoryID) || errors.Is(err, ErrRepositoryNotFound) || errors.Is(err, ErrUnsupportedService) {
		if !c.Writer.Written() {
			writePlain(c, http.StatusNotFound, "repository not found\n")
		}
		return
	}
	if m.log != nil {
		m.log.Error("git subprocess failed")
	}
	if !c.Writer.Written() {
		writePlain(c, http.StatusInternalServerError, "git service unavailable\n")
	}
}

func contentLengthWithinLimit(c *jinengine.Context, limit int64) bool {
	if limit <= 0 || c.Request.ContentLength < 0 || c.Request.ContentLength <= limit {
		return true
	}
	writePlain(c, http.StatusRequestEntityTooLarge, "git request body too large\n")
	return false
}
