package git

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"github.com/juanjiTech/jin"
)

func TestRealGitPublicPluginDistributionCloneInteroperability(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary is not available")
	}
	service, _, _, _ := newProjectionFixture(t, true)
	result, err := service.BuildPluginProjection(context.Background(), gitservice.BuildPluginProjectionCommand{
		ProjectionID: uuid.New(), RepositoryID: testRepositoryID, TagName: "v1.0.0",
		PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("BuildPluginProjection() error = %v", err)
	}
	distributionID := uuid.New()
	resolver := &routeDistributionResolver{plugin: distributionservice.PluginGrant{DistributionID: distributionID, Projection: result.Projection}}
	engine := jin.New()
	mod := &Mod{distributionResolver: resolver, distributionReader: service, config: Config{maxRequestBytes: defaultMaxRequestBytes}}
	mod.registerDistributionRoutes(engine)
	server := httptest.NewServer(engine)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitCommand(t, gitBinary, "", "clone", "--branch", "v1.0.0", server.URL+"/distribution/plugins/"+distributionID.String()+".git", cloneDir)
	content, err := os.ReadFile(filepath.Join(cloneDir, "plugin.txt"))
	if err != nil {
		t.Fatalf("read cloned plugin file: %v", err)
	}
	if string(content) != "version one\n" {
		t.Fatalf("cloned plugin content = %q", content)
	}
	parents := runGitInOutput(t, cloneDir, "rev-list", "--parents", "-n", "1", "HEAD")
	if fields := len(strings.Fields(parents)); fields != 1 {
		t.Fatalf("cloned distribution commit has %d fields, want SHA only", fields)
	}
}

func TestRealGitPublicMarketplaceDistributionCloneInteroperability(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary is not available")
	}
	service, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: gitBinary})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"name":"ctf-reverse","owner":{"name":"Security"},"plugins":[]}`)
	result, err := service.BuildMarketplaceProjection(context.Background(), gitservice.BuildMarketplaceProjectionCommand{
		ProjectionID: uuid.New(), ContentJSON: content, PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	distributionID := uuid.New()
	publicKey, err := distributionservice.ParseMarketplacePublicKey("ctf-reverse-a3f91c20")
	if err != nil {
		t.Fatal(err)
	}
	resolver := &routeDistributionResolver{marketplace: distributionservice.MarketplaceGrant{DistributionID: distributionID, PublicKey: publicKey, RevisionID: uuid.New(), Projection: result.Projection}}
	engine := jin.New()
	mod := &Mod{distributionResolver: resolver, distributionReader: service, config: Config{maxRequestBytes: defaultMaxRequestBytes}}
	mod.registerDistributionRoutes(engine)
	server := httptest.NewServer(engine)
	defer server.Close()

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitCommand(t, gitBinary, "", "clone", server.URL+"/distribution/marketplaces/"+publicKey.String()+".git", cloneDir)
	got, err := os.ReadFile(filepath.Join(cloneDir, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("cloned marketplace = %q, want %q", got, content)
	}
}
