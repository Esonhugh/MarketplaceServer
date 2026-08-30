package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/marketplacejson"
	"github.com/Masterminds/semver"
)

var (
	ErrUserMarketplaceNotFound = errors.New("user marketplace not found")
	userMarketplaceSlug        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	userMarketplaceSHA         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	userMarketplaceRef         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+\-]{0,254}$`)
)

type UserMarketplaceService struct {
	repository UserMarketplaceRepository
	authorizer auth.Authorizer
}

func NewUserMarketplaceService(repository UserMarketplaceRepository, authorizer auth.Authorizer) *UserMarketplaceService {
	return &UserMarketplaceService{repository: repository, authorizer: authorizer}
}

type UserMarketplaceResult struct {
	ContentJSON []byte
	ETag        string
}

// Render authenticates no identity itself. It verifies all access decisions
// immediately before generating a deterministic, direct Git marketplace.
func (service *UserMarketplaceService) Render(ctx context.Context, principal auth.Principal, username, origin string) (UserMarketplaceResult, error) {
	if service.repository == nil || service.authorizer == nil || !validSlug(username) || !principal.IsUser() || principal.Username() != username {
		return UserMarketplaceResult{}, ErrUserMarketplaceNotFound
	}
	if err := service.authorizer.Authorize(ctx, principal, auth.ActionMarketplaceRead, auth.ResourceRef{Type: "user", ID: principal.UserID()}); err != nil {
		return UserMarketplaceResult{}, ErrUserMarketplaceNotFound
	}
	marketplace, err := service.repository.FindUserMarketplace(ctx, username)
	if err != nil || marketplace.Username != username || marketplace.UserID != principal.UserID() || marketplace.Status != "active" || !validSlug(marketplace.NamespaceSlug) || marketplace.NamespaceID == "" {
		return UserMarketplaceResult{}, ErrUserMarketplaceNotFound
	}
	if err := validateUserMarketplaceOrigin(origin); err != nil {
		return UserMarketplaceResult{}, fmt.Errorf("render user marketplace: %w", err)
	}
	plugins, err := service.plugins(ctx, principal, origin, marketplace)
	if err != nil {
		return UserMarketplaceResult{}, err
	}
	owner := marketplace.DisplayName
	if strings.TrimSpace(owner) == "" {
		owner = username
	}
	content, err := marketplacejson.Marshal(marketplacejson.Marketplace{
		Name:    username,
		Owner:   marketplacejson.Owner{Name: owner},
		Plugins: plugins,
	})
	if err != nil {
		return UserMarketplaceResult{}, fmt.Errorf("render user marketplace: %w", err)
	}
	sum := sha256.Sum256(content)
	return UserMarketplaceResult{ContentJSON: content, ETag: `"` + hex.EncodeToString(sum[:]) + `"`}, nil
}

func (service *UserMarketplaceService) plugins(ctx context.Context, principal auth.Principal, origin string, marketplace UserMarketplace) ([]marketplacejson.Plugin, error) {
	best := make(map[string]userMarketplaceVersion)
	for _, candidate := range marketplace.Candidates {
		selected, ok := candidateVersion(candidate, origin)
		if !ok {
			continue
		}
		if current, exists := best[selected.pluginID]; !exists || selected.version.GreaterThan(current.version) {
			best[selected.pluginID] = selected
		}
	}
	plugins := make([]marketplacejson.Plugin, 0, len(best))
	for _, selected := range best {
		if service.authorizer.Authorize(ctx, principal, auth.ActionPluginRead, auth.ResourceRef{Type: auth.ResourcePlugin, ID: selected.pluginID, NamespaceID: selected.namespaceID}) != nil {
			continue
		}
		plugins = append(plugins, marketplacejson.Plugin{
			Name:        selected.name,
			Description: selected.description,
			Source:      marketplacejson.URLSource{Source: marketplacejson.URLSourceType, URL: selected.url, Ref: selected.tag, SHA: selected.sha},
		})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	return plugins, nil
}

type userMarketplaceVersion struct {
	namespaceID, pluginID, repositoryID, name, description, url, tag, sha string
	version                                                               *semver.Version
}

func availableCandidateStatus(status string) bool {
	return status == plugindomain.VersionStatusAvailable || status == "published"
}

func candidatePluginReadable(status string) bool {
	return status == plugindomain.PluginStatusActive || status == plugindomain.PluginStatusArchived
}

func candidateVersion(candidate UserMarketplaceCandidate, origin string) (userMarketplaceVersion, bool) {
	if !candidatePluginReadable(candidate.PluginStatus) || !repositoryAllowsRead(candidate.RepositoryStatus) || !availableCandidateStatus(candidate.VersionStatus) ||
		candidate.NamespaceID == "" || !validSlug(candidate.NamespaceSlug) || candidate.PluginID == "" || candidate.RepositoryID == "" ||
		!validSlug(candidate.PluginSlug) || !validSlug(candidate.RepositorySlug) || !validRef(candidate.TagName) ||
		!userMarketplaceSHA.MatchString(candidate.CommitSHA) {
		return userMarketplaceVersion{}, false
	}
	version, err := semver.NewVersion(strings.TrimPrefix(candidate.Version, "v"))
	if err != nil || version.Prerelease() != "" || version.Original() != version.String() {
		return userMarketplaceVersion{}, false
	}
	return userMarketplaceVersion{
		namespaceID: candidate.NamespaceID, pluginID: candidate.PluginID, repositoryID: candidate.RepositoryID,
		name: candidate.NamespaceSlug + "-" + candidate.PluginSlug, description: candidate.PluginDescription,
		url: origin + "/git/" + candidate.NamespaceSlug + "/" + candidate.RepositorySlug + ".git",
		tag: candidate.TagName, sha: candidate.CommitSHA, version: version,
	}, true
}

func validateUserMarketplaceOrigin(origin string) error {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid origin")
	}
	host := parsed.Hostname()
	if host == "" || !validOriginHostname(host) {
		return errors.New("invalid origin")
	}
	if port := parsed.Port(); port != "" {
		value := 0
		for _, character := range port {
			if character < '0' || character > '9' {
				return errors.New("invalid origin")
			}
			value = value*10 + int(character-'0')
		}
		if value < 1 || value > 65535 {
			return errors.New("invalid origin")
		}
	}
	return nil
}

func validOriginHostname(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	return true
}

func validSlug(value string) bool { return userMarketplaceSlug.MatchString(value) }
func validRef(value string) bool {
	return userMarketplaceRef.MatchString(value) && !strings.Contains(value, "..") && !strings.Contains(value, "@{") && !strings.HasSuffix(value, ".") && !strings.HasSuffix(value, "/")
}
