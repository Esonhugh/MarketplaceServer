package distribution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

func TestUserMarketplaceServiceFiltersAndRendersDeterministically(t *testing.T) {
	principal := testMarketplacePrincipal(t, "user-1", "alice", auth.UnrestrictedScopes())
	repository := &userMarketplaceRepositoryStub{marketplace: UserMarketplace{
		UserID: "user-1", Username: "alice", DisplayName: "", Status: "active", NamespaceID: "namespace-1", NamespaceSlug: "alice",
		Candidates: []UserMarketplaceCandidate{
			candidate("plugin-z", "zulu", "repo-z", "2.0.0", "published", "v2.0.0", "b"),
			candidate("plugin-z", "zulu", "repo-z", "10.0.0", "published", "v10.0.0", "c"),
			candidate("plugin-a", "alpha", "repo-a", "1.0.0", "published", "v1.0.0", "a"),
			readOnlyCandidate("plugin-r", "readonly", "repo-r", "5.0.0", "published", "v5.0.0", "e"),
			candidate("plugin-y", "yank", "repo-y", "99.0.0", "yanked", "v99.0.0", "d"),
			candidate("plugin-bad", "bad", "repo-bad", "1.0.0", "published", "bad..ref", "e"),
			candidate("plugin-pre", "pre", "repo-pre", "3.0.0-rc.1", "published", "v3.0.0-rc.1", "f"),
			candidate("plugin-short", "short", "repo-short", "2", "published", "v2", "1"),
			candidateInNamespace("plugin-team", "team-one", "shared", "repo-team", "4.0.0", "published", "v4.0.0", "d"),
		},
	}}
	service := NewUserMarketplaceService(repository, userMarketplaceAuthorizerStub{})
	first, err := service.Render(context.Background(), principal, "alice", "https://market.example")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Render(context.Background(), principal, "alice", "https://market.example")
	if err != nil {
		t.Fatal(err)
	}
	if string(first.ContentJSON) != string(second.ContentJSON) || first.ETag != second.ETag {
		t.Fatalf("render is not deterministic: %q / %q", first.ContentJSON, second.ContentJSON)
	}
	want := `{"name":"alice","owner":{"name":"alice"},"plugins":[{"name":"alice-alpha","source":{"source":"url","url":"https://market.example/git/alice/repo-a.git","ref":"v1.0.0","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},{"name":"alice-readonly","source":{"source":"url","url":"https://market.example/git/alice/repo-r.git","ref":"v5.0.0","sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}},{"name":"alice-zulu","source":{"source":"url","url":"https://market.example/git/alice/repo-z.git","ref":"v10.0.0","sha":"cccccccccccccccccccccccccccccccccccccccc"}},{"name":"team-one-shared","source":{"source":"url","url":"https://market.example/git/team-one/repo-team.git","ref":"v4.0.0","sha":"dddddddddddddddddddddddddddddddddddddddd"}}]}`
	if got := string(first.ContentJSON); got != want {
		t.Fatalf("content = %s\nwant = %s", got, want)
	}
	if repository.calls != 1+1 {
		t.Fatalf("repository calls = %d, want 2", repository.calls)
	}
}

func TestUserMarketplaceServiceRequiresExactSelfAndFiltersAuthorization(t *testing.T) {
	principal := testMarketplacePrincipal(t, "user-1", "alice", auth.RestrictedScopes(auth.ActionMarketplaceRead))
	repository := &userMarketplaceRepositoryStub{marketplace: UserMarketplace{
		UserID: "user-1", Username: "alice", Status: "active", NamespaceID: "namespace-1", NamespaceSlug: "alice",
		Candidates: []UserMarketplaceCandidate{candidate("plugin-1", "one", "repo-1", "1.0.0", "published", "v1.0.0", "a")},
	}}
	service := NewUserMarketplaceService(repository, userMarketplaceAuthorizerStub{denyPlugin: true})
	result, err := service.Render(context.Background(), principal, "alice", "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.ContentJSON), `"plugins":[]`) {
		t.Fatalf("plugins must be an array when all candidates are denied: %s", result.ContentJSON)
	}
	if _, err := service.Render(context.Background(), principal, "ALICE", "http://localhost:8080"); !errors.Is(err, ErrUserMarketplaceNotFound) {
		t.Fatalf("noncanonical username error = %v", err)
	}
	if _, err := service.Render(context.Background(), testMarketplacePrincipal(t, "user-1", "mallory", auth.UnrestrictedScopes()), "alice", "http://localhost:8080"); !errors.Is(err, ErrUserMarketplaceNotFound) {
		t.Fatalf("principal mismatch error = %v", err)
	}
}

func candidate(pluginID, pluginSlug, repositorySlug, version, status, tag, shaChar string) UserMarketplaceCandidate {
	return candidateInNamespace(pluginID, "alice", pluginSlug, repositorySlug, version, status, tag, shaChar)
}

func candidateInNamespace(pluginID, namespaceSlug, pluginSlug, repositorySlug, version, status, tag, shaChar string) UserMarketplaceCandidate {
	return UserMarketplaceCandidate{NamespaceID: namespaceSlug + "-namespace", NamespaceSlug: namespaceSlug, PluginID: pluginID, PluginSlug: pluginSlug, PluginStatus: StatusActive, RepositoryID: pluginID + "-repository", RepositorySlug: repositorySlug, RepositoryStatus: RepositoryStatusReady, Version: version, VersionStatus: status, TagName: tag, CommitSHA: strings.Repeat(shaChar, 40)}
}

func readOnlyCandidate(pluginID, pluginSlug, repositorySlug, version, status, tag, shaChar string) UserMarketplaceCandidate {
	value := candidate(pluginID, pluginSlug, repositorySlug, version, status, tag, shaChar)
	value.RepositoryStatus = RepositoryStatusReadOnly
	return value
}

type userMarketplaceRepositoryStub struct {
	marketplace UserMarketplace
	err         error
	calls       int
}

func (stub *userMarketplaceRepositoryStub) FindUserMarketplace(context.Context, string) (UserMarketplace, error) {
	stub.calls++
	return stub.marketplace, stub.err
}

type userMarketplaceAuthorizerStub struct {
	denyMarketplace bool
	denyPlugin      bool
	denyRepository  bool
}

func (stub userMarketplaceAuthorizerStub) Authorize(_ context.Context, _ auth.Principal, action auth.Action, _ auth.ResourceRef) error {
	if stub.denyMarketplace && action == auth.ActionMarketplaceRead || stub.denyPlugin && action == auth.ActionPluginRead || stub.denyRepository && action == auth.ActionRepositoryRead {
		return errors.New("denied")
	}
	return nil
}

func testMarketplacePrincipal(t *testing.T, id, username string, scopes auth.ScopeSet) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal(id, username, auth.CredentialPAT, scopes)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}
