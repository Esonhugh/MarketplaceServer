// Package plugin implements management-plane HTTP adapters for Plugin lifecycle
// operations. It deliberately depends only on local API value types and a
// narrow lifecycle service contract; wiring to a domain implementation remains
// outside this package.
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	managementhandler "github.com/Esonhugh/MarketplaceServer/mod/backend/handler/management"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/juanjiTech/jin"
)

var (
	ErrForbidden        = errors.New("plugin handler: forbidden")
	ErrNotFound         = errors.New("plugin handler: not found")
	ErrConflict         = errors.New("plugin handler: conflict")
	ErrGone             = errors.New("plugin handler: gone")
	ErrInvalidInput     = errors.New("plugin handler: invalid input")
	slugPattern         = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	canonicalTagPattern = regexp.MustCompile(`^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
)

// Authenticator accepts management JWTs only. The common management helpers
// additionally enforce that the resolved principal represents a JWT user.
type Authenticator interface {
	AuthenticateBearer(context.Context, string) (auth.Principal, error)
}

// Lifecycle contains handler-facing value contracts only. Implementations own
// authorization, namespace lookup, aggregate state, persistence, and Git
// orchestration. Handler request and response values intentionally do not map
// to GORM records.
type Lifecycle interface {
	Create(context.Context, auth.Principal, CreateInput) (Plugin, error)
	List(context.Context, auth.Principal, string, int, int) (PluginPage, error)
	Get(context.Context, auth.Principal, string, string) (Plugin, error)
	Archive(context.Context, auth.Principal, string, string) error
	Restore(context.Context, auth.Principal, string, string) error
	SetVisibility(context.Context, auth.Principal, string, string, string) error
	ListVersions(context.Context, auth.Principal, string, string, int, int) (VersionPage, error)
	GetVersion(context.Context, auth.Principal, string, string, string) (Version, error)
	Publish(context.Context, auth.Principal, string, string, PublishInput) (Version, error)
	SetDefaultVersion(context.Context, auth.Principal, string, string, string) error
	ClearDefaultVersion(context.Context, auth.Principal, string, string) error
}

type repositoryLifecycle interface {
	ListRepositoryRefs(context.Context, auth.Principal, string, string) (gitservice.RepositoryRefs, error)
	ReadRepositoryTree(context.Context, auth.Principal, string, string, string, string) (gitservice.RepositoryTree, error)
	ReadRepositoryBlob(context.Context, auth.Principal, string, string, string, string) (gitservice.RepositoryBlob, error)
	ListRepositoryCommits(context.Context, auth.Principal, string, string, string, string, int, int) (gitservice.RepositoryCommitPage, error)
}

// Plugin contains only approved management API facts. It must not gain IDs,
// storage keys, paths, repository recovery state, or persistence models.
type Plugin struct {
	Namespace        string    `json:"namespace"`
	Name             string    `json:"name"`
	Status           string    `json:"status"`
	Visibility       string    `json:"visibility"`
	RepositoryStatus string    `json:"repositoryStatus"`
	CloneURL         string    `json:"cloneUrl"`
	DefaultVersion   *string   `json:"defaultVersion"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// Version contains only approved management API facts.
type Version struct {
	Tag         string    `json:"tag"`
	Status      string    `json:"status"`
	CommitSHA   string    `json:"commitSha"`
	PublishedAt time.Time `json:"publishedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type PluginPage struct {
	Items []Plugin `json:"items"`
	Page  int      `json:"page"`
	Size  int      `json:"size"`
	Total int64    `json:"total"`
}

type VersionPage struct {
	Items []Version `json:"items"`
	Page  int       `json:"page"`
	Size  int       `json:"size"`
	Total int64     `json:"total"`
}

type CreateInput struct {
	Namespace  string
	Name       string
	Visibility string
}

type PublishInput struct {
	Tag         string
	MakeDefault bool
}

type Handler struct {
	service       Lifecycle
	authenticator Authenticator
}

func NewHandler(service Lifecycle, authenticator Authenticator) *Handler {
	return &Handler{service: service, authenticator: authenticator}
}

// Register adds only Plugin lifecycle routes. Runtime wiring is deliberately
// outside this handler package.
//
// Jin treats ':' as a parameter marker inside a segment. Action routes are
// consequently whole-segment dispatchers. Their dispatchers validate both the
// accepted suffix and the plugin/tag prefix before service delegation.
func (handler *Handler) Register(engine *jin.Engine) {
	engine.GET("/api/v1/namespaces/:namespace/plugins", handler.List)
	engine.POST("/api/v1/namespaces/:namespace/plugins", handler.Create)
	engine.GET("/api/v1/namespaces/:namespace/plugins/:plugin", handler.Get)
	engine.GET("/api/v1/namespaces/:namespace/plugins/:plugin/versions", handler.ListVersions)
	engine.GET("/api/v1/namespaces/:namespace/plugins/:plugin/repository/refs", handler.RepositoryRefs)
	engine.GET("/api/v1/namespaces/:namespace/plugins/:plugin/repository/tree", handler.RepositoryTree)
	engine.GET("/api/v1/namespaces/:namespace/plugins/:plugin/repository/blob", handler.RepositoryBlob)
	engine.GET("/api/v1/namespaces/:namespace/plugins/:plugin/repository/commits", handler.RepositoryCommits)
	engine.GET("/api/v1/namespaces/:namespace/plugins/:plugin/versions/:tag", handler.GetVersion)
	engine.DELETE("/api/v1/namespaces/:namespace/plugins/:plugin/default-version", handler.ClearDefaultVersion)
	// Jin cannot register two wildcard names at the Plugin action segment. One
	// whole-segment dispatcher recognizes every action suffix itself, avoiding
	// service calls with a malformed slug.
	engine.POST("/api/v1/namespaces/:namespace/plugins/:plugin", handler.PluginPost)
	engine.POST("/api/v1/namespaces/:namespace/plugins/:plugin/:versionAction", handler.PluginPost)
	engine.POST("/api/v1/namespaces/:namespace/plugins/:plugin/versions/:tagAction", handler.PluginPost)
}

type createRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
}

type visibilityRequest struct {
	Visibility string `json:"visibility"`
}

type publishRequest struct {
	Tag         string `json:"tag"`
	MakeDefault bool   `json:"makeDefault"`
}

type requestFields map[string]json.RawMessage

type contextKey uint8

const requestFieldsKey contextKey = iota

func (handler *Handler) Create(c *jin.Context) {
	principal, ok := handler.authenticateRequired(c)
	if !ok {
		return
	}
	namespace, ok := pathSlug(c, "namespace", "namespace")
	if !ok {
		return
	}
	var request createRequest
	if !decodeBody(c, &request) {
		return
	}
	if !validSlug(request.Name) || (request.Visibility != "" && !validVisibility(request.Visibility)) {
		renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid")
		return
	}
	if request.Visibility == "" && jsonObjectFieldPresent(c, "visibility") {
		renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid")
		return
	}
	visibility := request.Visibility
	if visibility == "" {
		visibility = "public"
	}
	created, err := handler.service.Create(c.Request.Context(), principal, CreateInput{Namespace: namespace, Name: request.Name, Visibility: visibility})
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	renderSuccess(c, http.StatusCreated, created)
}

func (handler *Handler) List(c *jin.Context) {
	principal, ok := handler.authenticateRequired(c)
	if !ok {
		return
	}
	namespace, ok := pathSlug(c, "namespace", "namespace")
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	result, err := handler.service.List(c.Request.Context(), principal, namespace, page, size)
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	result.Items = nonNilPlugins(result.Items)
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) Get(c *jin.Context) {
	principal, ok := handler.authenticateOptional(c)
	if !ok {
		return
	}
	namespace, plugin, ok := pluginPath(c)
	if !ok {
		return
	}
	result, err := handler.service.Get(c.Request.Context(), principal, namespace, plugin)
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) PluginPost(c *jin.Context) {
	principal, ok := handler.authenticateRequired(c)
	if !ok {
		return
	}
	namespace, ok := pathSlug(c, "namespace", "namespace")
	if !ok {
		return
	}
	plugin, action, ok := pluginPostPath(c)
	if !ok {
		renderNotFound(c)
		return
	}
	switch action {
	case "archive":
		handler.runPluginStateCommand(c, handler.service.Archive, principal, namespace, plugin)
	case "restore":
		handler.runPluginStateCommand(c, handler.service.Restore, principal, namespace, plugin)
	case "set-visibility":
		var request visibilityRequest
		if !decodeBody(c, &request) {
			return
		}
		if !validVisibility(request.Visibility) || !jsonObjectHasKey(c, "visibility") {
			renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid")
			return
		}
		if err := handler.service.SetVisibility(c.Request.Context(), principal, namespace, plugin, request.Visibility); err != nil {
			handler.renderServiceError(c, err)
			return
		}
		noContent(c)
	case "publish":
		var request publishRequest
		if !decodeBody(c, &request) {
			return
		}
		if !validTag(request.Tag) || !jsonObjectHasKey(c, "tag") || jsonObjectFieldPresent(c, "makeDefault") && !jsonObjectHasKey(c, "makeDefault") {
			renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid")
			return
		}
		result, err := handler.service.Publish(c.Request.Context(), principal, namespace, plugin, PublishInput{Tag: request.Tag, MakeDefault: request.MakeDefault})
		if err != nil {
			handler.renderServiceError(c, err)
			return
		}
		renderSuccess(c, http.StatusCreated, result)
	case "set-default":
		tag, _, _ := splitOneColon(c.Params.ByName("tagAction"))
		if err := handler.service.SetDefaultVersion(c.Request.Context(), principal, namespace, plugin, tag); err != nil {
			handler.renderServiceError(c, err)
			return
		}
		noContent(c)
	}
}

func pluginPostPath(c *jin.Context) (string, string, bool) {
	if action := c.Params.ByName("versionAction"); action != "" {
		plugin := c.Params.ByName("plugin")
		if validSlug(plugin) && action == "versions:publish" {
			return plugin, "publish", true
		}
		return "", "", false
	}
	if tagAction := c.Params.ByName("tagAction"); tagAction != "" {
		plugin := c.Params.ByName("plugin")
		tag, action, found := splitOneColon(tagAction)
		if validSlug(plugin) && found && validTag(tag) && action == "set-default" {
			return plugin, "set-default", true
		}
		return "", "", false
	}
	return splitPluginAction(c.Params.ByName("plugin"))
}

func (handler *Handler) runPluginStateCommand(c *jin.Context, command func(context.Context, auth.Principal, string, string) error, principal auth.Principal, namespace, plugin string) {
	if err := command(c.Request.Context(), principal, namespace, plugin); err != nil {
		handler.renderServiceError(c, err)
		return
	}
	noContent(c)
}

func (handler *Handler) ListVersions(c *jin.Context) {
	principal, ok := handler.authenticateRequired(c)
	if !ok {
		return
	}
	namespace, plugin, ok := pluginPath(c)
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	result, err := handler.service.ListVersions(c.Request.Context(), principal, namespace, plugin, page, size)
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	result.Items = nonNilVersions(result.Items)
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) GetVersion(c *jin.Context) {
	principal, ok := handler.authenticateOptional(c)
	if !ok {
		return
	}
	namespace, plugin, ok := pluginPath(c)
	if !ok {
		return
	}
	tag, ok := pathTag(c, "tag")
	if !ok {
		return
	}
	result, err := handler.service.GetVersion(c.Request.Context(), principal, namespace, plugin, tag)
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) RepositoryRefs(c *jin.Context) {
	principal, namespace, plugin, ok := handler.repositoryRequest(c)
	if !ok {
		return
	}
	service, ok := handler.repositoryLifecycle(c)
	if !ok {
		return
	}
	result, err := service.ListRepositoryRefs(c.Request.Context(), principal, namespace, plugin)
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	if result.Branches == nil {
		result.Branches = []gitservice.RepositoryRef{}
	}
	if result.Tags == nil {
		result.Tags = []gitservice.RepositoryRef{}
	}
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) RepositoryTree(c *jin.Context) {
	principal, namespace, plugin, ok := handler.repositoryRequest(c)
	if !ok {
		return
	}
	revision := c.Request.URL.Query().Get("ref")
	service, ok := handler.repositoryLifecycle(c)
	if !ok {
		return
	}
	result, err := service.ReadRepositoryTree(c.Request.Context(), principal, namespace, plugin, revision, c.Request.URL.Query().Get("path"))
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	if result.Entries == nil {
		result.Entries = []gitservice.RepositoryTreeEntry{}
	}
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) RepositoryBlob(c *jin.Context) {
	principal, namespace, plugin, ok := handler.repositoryRequest(c)
	if !ok {
		return
	}
	service, ok := handler.repositoryLifecycle(c)
	if !ok {
		return
	}
	result, err := service.ReadRepositoryBlob(c.Request.Context(), principal, namespace, plugin, c.Request.URL.Query().Get("ref"), c.Request.URL.Query().Get("path"))
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) RepositoryCommits(c *jin.Context) {
	principal, namespace, plugin, ok := handler.repositoryRequest(c)
	if !ok {
		return
	}
	page, size, ok := managementhandler.ParsePagination(c)
	if !ok {
		return
	}
	service, ok := handler.repositoryLifecycle(c)
	if !ok {
		return
	}
	result, err := service.ListRepositoryCommits(c.Request.Context(), principal, namespace, plugin, c.Request.URL.Query().Get("ref"), c.Request.URL.Query().Get("path"), page, size)
	if err != nil {
		handler.renderServiceError(c, err)
		return
	}
	if result.Items == nil {
		result.Items = []gitservice.RepositoryCommit{}
	}
	renderSuccess(c, http.StatusOK, result)
}

func (handler *Handler) repositoryLifecycle(c *jin.Context) (repositoryLifecycle, bool) {
	service, ok := handler.service.(repositoryLifecycle)
	if !ok || service == nil {
		renderError(c, http.StatusNotImplemented, "not_implemented", "repository browsing is unavailable")
		return nil, false
	}
	return service, true
}

func (handler *Handler) repositoryRequest(c *jin.Context) (auth.Principal, string, string, bool) {
	principal, ok := handler.authenticateRequired(c)
	if !ok {
		return auth.Principal{}, "", "", false
	}
	namespace, plugin, ok := pluginPath(c)
	return principal, namespace, plugin, ok
}

func (handler *Handler) ClearDefaultVersion(c *jin.Context) {
	principal, ok := handler.authenticateRequired(c)
	if !ok {
		return
	}
	namespace, plugin, ok := pluginPath(c)
	if !ok {
		return
	}
	if err := handler.service.ClearDefaultVersion(c.Request.Context(), principal, namespace, plugin); err != nil {
		handler.renderServiceError(c, err)
		return
	}
	noContent(c)
}

func (handler *Handler) authenticateRequired(c *jin.Context) (auth.Principal, bool) {
	return managementhandler.AuthenticateRequired(c, handler.authenticator)
}

func (handler *Handler) authenticateOptional(c *jin.Context) (auth.Principal, bool) {
	return managementhandler.AuthenticateOptional(c, handler.authenticator)
}

func (handler *Handler) renderServiceError(c *jin.Context, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		renderError(c, http.StatusForbidden, "forbidden", "permission denied")
	case errors.Is(err, ErrNotFound):
		renderNotFound(c)
	case errors.Is(err, ErrConflict):
		renderError(c, http.StatusConflict, "conflict", "request conflicts with current state")
	case errors.Is(err, ErrGone):
		renderError(c, http.StatusGone, "gone", "version is no longer available")
	case errors.Is(err, ErrInvalidInput), errors.Is(err, gitservice.ErrInvalidBrowseInput):
		renderError(c, http.StatusUnprocessableEntity, "validation_failed", "request is invalid")
	case errors.Is(err, gitservice.ErrRevisionNotFound), errors.Is(err, gitservice.ErrPathNotFound):
		renderError(c, http.StatusNotFound, "not_found", "repository revision or path not found")
	case errors.Is(err, gitservice.ErrPathNotText):
		renderError(c, http.StatusConflict, "not_text", "repository path is not a previewable text file")
	case errors.Is(err, gitservice.ErrBlobTooLarge):
		renderError(c, http.StatusRequestEntityTooLarge, "preview_too_large", "repository file exceeds the preview limit")
	default:
		renderError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func decodeBody(c *jin.Context, destination any) bool {
	var raw json.RawMessage
	if err := managementhandler.DecodeJSONBody(c, &raw); err != nil {
		renderError(c, http.StatusBadRequest, "bad_request", "request body is invalid")
		return false
	}
	var fields requestFields
	if err := json.Unmarshal(raw, &fields); err != nil {
		renderError(c, http.StatusBadRequest, "bad_request", "request body is invalid")
		return false
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		renderError(c, http.StatusBadRequest, "bad_request", "request body is invalid")
		return false
	}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), requestFieldsKey, fields))
	return true
}

func jsonObjectHasKey(c *jin.Context, key string) bool {
	fields, _ := c.Request.Context().Value(requestFieldsKey).(requestFields)
	raw, ok := fields[key]
	return ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func jsonObjectFieldPresent(c *jin.Context, key string) bool {
	fields, _ := c.Request.Context().Value(requestFieldsKey).(requestFields)
	_, ok := fields[key]
	return ok
}

func renderSuccess(c *jin.Context, status int, data any) {
	managementhandler.RenderSuccess(c, status, data)
}

func renderError(c *jin.Context, status int, code, message string) {
	managementhandler.RenderError(c, status, code, message)
}

func renderNotFound(c *jin.Context) {
	renderError(c, http.StatusNotFound, "not_found", "plugin not found")
}

func noContent(c *jin.Context) {
	c.Writer.WriteHeader(http.StatusNoContent)
}

func pluginPath(c *jin.Context) (string, string, bool) {
	namespace, ok := pathSlug(c, "namespace", "namespace")
	if !ok {
		return "", "", false
	}
	plugin, ok := pathSlug(c, "plugin", "plugin")
	if !ok {
		return "", "", false
	}
	return namespace, plugin, true
}

func pathSlug(c *jin.Context, name, resource string) (string, bool) {
	value := c.Params.ByName(name)
	if !validSlug(value) {
		renderError(c, http.StatusUnprocessableEntity, "validation_failed", resource+" is invalid")
		return "", false
	}
	return value, true
}

func pathTag(c *jin.Context, name string) (string, bool) {
	value := c.Params.ByName(name)
	if !validTag(value) {
		renderNotFound(c)
		return "", false
	}
	return value, true
}

func pluginActionPath(c *jin.Context) (string, string, bool) {
	return splitPluginAction(c.Params.ByName("pluginAction"))
}

func splitPluginAction(value string) (string, string, bool) {
	plugin, action, found := splitOneColon(value)
	if !found || !validSlug(plugin) || !validPluginAction(action) {
		return "", "", false
	}
	return plugin, action, true
}

func splitOneColon(value string) (string, string, bool) {
	separator := -1
	for index, character := range value {
		if character == ':' {
			if separator != -1 {
				return "", "", false
			}
			separator = index
		}
	}
	if separator <= 0 || separator == len(value)-1 {
		return "", "", false
	}
	return value[:separator], value[separator+1:], true
}

func validPluginAction(value string) bool {
	switch value {
	case "archive", "restore", "set-visibility", "versions:publish":
		return true
	default:
		return false
	}
}

func validSlug(value string) bool {
	return len(value) >= 1 && len(value) <= 63 && slugPattern.MatchString(value)
}

func validTag(value string) bool {
	return canonicalTagPattern.MatchString(value)
}

func validVisibility(value string) bool {
	return value == "public" || value == "private"
}

func nonNilPlugins(items []Plugin) []Plugin {
	if items == nil {
		return []Plugin{}
	}
	return items
}

func nonNilVersions(items []Version) []Version {
	if items == nil {
		return []Version{}
	}
	return items
}
