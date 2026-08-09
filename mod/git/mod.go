package git

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	jinengine "github.com/juanjiTech/jin"
	"go.uber.org/zap"
)

var _ kernel.Module = (*Mod)(nil)

const (
	defaultGitBinary        = "git"
	defaultMaxRequestBytes  = int64(100 << 20)
	defaultAdvertiseTimeout = 15 * time.Second
	defaultServiceTimeout   = 5 * time.Minute
)

type Config struct {
	StorageRoot string `yaml:"storageRoot" mapstructure:"storageRoot"`

	gitBinary        string
	maxRequestBytes  int64
	advertiseTimeout time.Duration
	serviceTimeout   time.Duration
}

type Mod struct {
	kernel.UnimplementedModule

	config               Config
	service              *Service
	repositoryService    gitservice.RepositoryService
	distributionReader   gitservice.DistributionReader
	projectionBuilder    gitservice.ProjectionBuilder
	distributionResolver distributionservice.Resolver
	resolver             gitservice.RepositoryResolver
	log                  *zap.SugaredLogger
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
	m.service = svc
	m.repositoryService = svc
	m.distributionReader = svc
	m.projectionBuilder = svc
	hub.Map(&m.repositoryService, &m.distributionReader, &m.projectionBuilder)
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
	if resolver == nil {
		return errors.New("gitservice.RepositoryResolver from kernel is nil")
	}
	var distributionResolver distributionservice.Resolver
	if err := hub.Load(&distributionResolver); err != nil {
		return errors.New("can't load distributionservice.Resolver from kernel")
	}
	if distributionResolver == nil {
		return errors.New("distributionservice.Resolver from kernel is nil")
	}
	m.resolver = resolver
	m.distributionResolver = distributionResolver
	m.registerSmartHTTPRoutes(engine)
	m.registerDistributionRoutes(engine)
	return nil
}

func (m *Mod) applyDefaults() {
	if m.config.gitBinary == "" {
		m.config.gitBinary = defaultGitBinary
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
	switch service {
	case "git-upload-pack":
	case "git-receive-pack":
		writePlain(c, http.StatusForbidden, "git receive-pack is disabled until authorization is available\n")
		return
	default:
		writePlain(c, http.StatusNotFound, "unsupported git service\n")
		return
	}

	repository, ok := m.resolveAnonymousRepository(c.Request.Context(), namespace, repositorySlug)
	if !ok {
		writePlain(c, http.StatusNotFound, "repository not found\n")
		return
	}
	setGitHeaders(c, "application/x-git-upload-pack-advertisement")
	if err := m.service.AdvertiseRefs(c.Request.Context(), service, repository.ID, c.Writer, io.Discard); err != nil {
		m.handleGitError(c, err)
		return
	}
}

func (m *Mod) handleUploadPack(c *jinengine.Context) {
	namespace, repositorySlug, ok := routeRepository(c)
	if !ok {
		writePlain(c, http.StatusNotFound, "repository not found\n")
		return
	}
	repository, ok := m.resolveAnonymousRepository(c.Request.Context(), namespace, repositorySlug)
	if !ok {
		writePlain(c, http.StatusNotFound, "repository not found\n")
		return
	}
	if !contentLengthWithinLimit(c, m.config.maxRequestBytes) {
		return
	}
	setGitHeaders(c, "application/x-git-upload-pack-result")
	body := c.Request.Body
	if m.config.maxRequestBytes > 0 {
		body = http.MaxBytesReader(c.Writer, c.Request.Body, m.config.maxRequestBytes)
	}
	if err := m.service.UploadPack(c.Request.Context(), repository.ID, body, c.Writer, io.Discard); err != nil {
		m.handleGitError(c, err)
		return
	}
}

func (m *Mod) handleReceivePack(c *jinengine.Context) {
	writePlain(c, http.StatusForbidden, "git receive-pack is disabled until authorization is available\n")
}

func (m *Mod) resolveAnonymousRepository(ctx context.Context, namespace, repository string) (gitservice.Repository, bool) {
	resolved, err := m.resolver.Resolve(ctx, namespace, repository)
	if err != nil || resolved.Visibility != gitservice.VisibilityPublic || resolved.Status != gitservice.StatusReady {
		return gitservice.Repository{}, false
	}
	if err := validateRepositoryID(resolved.ID); err != nil {
		return gitservice.Repository{}, false
	}
	return resolved, true
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
