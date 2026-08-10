package distribution

import "context"

// UserMarketplaceRepository only reads the owner, personal namespace, and
// currently readable plugin candidates needed to render a user marketplace.
// It deliberately has no projection or mutation methods.
type UserMarketplaceRepository interface {
	FindUserMarketplace(ctx context.Context, username string) (UserMarketplace, error)
}

type UserMarketplace struct {
	UserID        string
	Username      string
	DisplayName   string
	Status        string
	NamespaceID   string
	NamespaceSlug string
	Candidates    []UserMarketplaceCandidate
}

type UserMarketplaceCandidate struct {
	NamespaceID       string
	NamespaceSlug     string
	PluginID          string
	PluginSlug        string
	PluginDescription string
	PluginStatus      string
	RepositoryID      string
	RepositorySlug    string
	RepositoryStatus  string
	VersionID         string
	Version           string
	VersionStatus     string
	TagName           string
	CommitSHA         string
}
