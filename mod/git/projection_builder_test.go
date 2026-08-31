package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func TestBuildPluginProjectionSanitizesAnnotatedAndLightweightTags(t *testing.T) {
	for _, annotated := range []bool{false, true} {
		name := "lightweight"
		if annotated {
			name = "annotated"
		}
		t.Run(name, func(t *testing.T) {
			service, sourcePath, sourceCommit, unrelatedCommit := newProjectionFixture(t, annotated)
			projectionID := uuid.New()
			result, err := service.BuildPluginProjection(context.Background(), gitservice.BuildPluginProjectionCommand{
				ProjectionID: projectionID, RepositoryID: testRepositoryID, TagName: "v1.0.0",
				PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
			})
			if err != nil {
				t.Fatalf("BuildPluginProjection() error = %v", err)
			}
			wantType := gitservice.SourceTagLightweight
			if annotated {
				wantType = gitservice.SourceTagAnnotated
			}
			if result.SourceTagType != wantType {
				t.Fatalf("source tag type = %q, want %q", result.SourceTagType, wantType)
			}
			if result.SourceCommitSHA != sourceCommit || result.DistributionSHA == sourceCommit {
				t.Fatalf("source/distribution SHA = %s/%s, want distinct snapshot of %s", result.SourceCommitSHA, result.DistributionSHA, sourceCommit)
			}
			if annotated && result.SourceTagObjectID == "" {
				t.Fatal("annotated source tag object ID is empty")
			}
			if !annotated && result.SourceTagObjectID != "" {
				t.Fatalf("lightweight source tag object ID = %q, want empty", result.SourceTagObjectID)
			}

			projectionPath, err := service.existingProjectionPath(result.Projection)
			if err != nil {
				t.Fatalf("existingProjectionPath() error = %v", err)
			}
			refs := runGitOutput(t, "--git-dir="+projectionPath, "for-each-ref", "--format=%(refname) %(objectname) %(objecttype)")
			wantRef := "refs/tags/v1.0.0 " + result.DistributionSHA + " commit"
			if strings.TrimSpace(refs) != wantRef {
				t.Fatalf("projection refs = %q, want %q", strings.TrimSpace(refs), wantRef)
			}
			parents := strings.Fields(runGitOutput(t, "--git-dir="+projectionPath, "rev-list", "--parents", "-n", "1", result.DistributionSHA))
			if len(parents) != 1 {
				t.Fatalf("distribution commit parents = %v, want none", parents[1:])
			}
			if err := exec.Command("git", "--git-dir="+projectionPath, "cat-file", "-e", sourceCommit+"^{commit}").Run(); err == nil {
				t.Fatal("projection contains source commit history")
			}
			if err := exec.Command("git", "--git-dir="+projectionPath, "cat-file", "-e", unrelatedCommit+"^{commit}").Run(); err == nil {
				t.Fatal("projection contains unrelated source commit")
			}
			if err := exec.Command("git", "--git-dir="+sourcePath, "show-ref", "--verify", "refs/tags/unrelated").Run(); err != nil {
				t.Fatal("source fixture unexpectedly lost unrelated tag")
			}
			if err := service.VerifyProjection(context.Background(), result.Projection, result.ContentDigest); err != nil {
				t.Fatalf("VerifyProjection() error = %v", err)
			}
		})
	}
}

func TestBuildMarketplaceProjectionContainsOnlyCanonicalJSON(t *testing.T) {
	service, err := NewService(Config{StorageRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	content := []byte(`{"name":"ctf-reverse","owner":{"name":"Security"},"plugins":[]}`)
	result, err := service.BuildMarketplaceProjection(context.Background(), gitservice.BuildMarketplaceProjectionCommand{
		ProjectionID: uuid.New(), ContentJSON: content, PublishedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("BuildMarketplaceProjection() error = %v", err)
	}
	path, err := service.existingProjectionPath(result.Projection)
	if err != nil {
		t.Fatal(err)
	}
	refs := strings.TrimSpace(runGitOutput(t, "--git-dir="+path, "for-each-ref", "--format=%(refname)"))
	if refs != "refs/heads/main" {
		t.Fatalf("marketplace refs = %q, want refs/heads/main", refs)
	}
	paths := strings.TrimSpace(runGitOutput(t, "--git-dir="+path, "ls-tree", "-r", "--name-only", "refs/heads/main"))
	if paths != ".claude-plugin/marketplace.json" {
		t.Fatalf("marketplace paths = %q", paths)
	}
	got := runGitOutput(t, "--git-dir="+path, "show", "refs/heads/main:.claude-plugin/marketplace.json")
	if got != string(content) {
		t.Fatalf("marketplace content = %q, want %q", got, content)
	}
	sum := sha256.Sum256(content)
	if result.ContentDigest != hex.EncodeToString(sum[:]) {
		t.Fatalf("content digest = %q, want SHA-256", result.ContentDigest)
	}
	if err := service.VerifyProjection(context.Background(), result.Projection, result.ContentDigest); err != nil {
		t.Fatalf("VerifyProjection() error = %v", err)
	}
}

func TestDistributionReaderUsesOnlyUploadPack(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	service, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatal(err)
	}
	projection := gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindPlugin, StorageKey: uuid.NewString()}
	path, err := service.projectionPath(projection)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := service.AdvertiseDistribution(context.Background(), projection, &stdout, io.Discard); err != nil {
		t.Fatalf("AdvertiseDistribution() error = %v", err)
	}
	if err := service.UploadDistribution(context.Background(), projection, strings.NewReader("request"), &stdout, io.Discard); err != nil {
		t.Fatalf("UploadDistribution() error = %v", err)
	}
	log := fake.readLog(t)
	if strings.Contains(log, "receive-pack") || strings.Contains(log, "update-ref") || strings.Contains(log, "init") {
		t.Fatalf("distribution reader used write-capable Git command: %q", log)
	}
	fake.assertInvocation(t, []string{"upload-pack", "--stateless-rpc", "--advertise-refs", path})
	fake.assertInvocation(t, []string{"upload-pack", "--stateless-rpc", path})
}

func newProjectionFixture(t *testing.T, annotated bool) (*Service, string, string, string) {
	t.Helper()
	root := t.TempDir()
	service, err := NewService(Config{StorageRoot: root})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := service.InitBareRepository(context.Background(), testRepositoryID); err != nil {
		t.Fatalf("InitBareRepository() error = %v", err)
	}
	sourcePath, err := service.existingRepositoryPath(testRepositoryID)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "clone", sourcePath, work)
	runGitIn(t, work, "config", "user.name", "Test")
	runGitIn(t, work, "config", "user.email", "test@example.com")
	runGitIn(t, work, "config", "commit.gpgSign", "false")
	runGitIn(t, work, "config", "tag.gpgSign", "false")
	if err := os.WriteFile(filepath.Join(work, "plugin.txt"), []byte("version one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, work, "add", "plugin.txt")
	runGitIn(t, work, "commit", "-m", "version one")
	sourceCommit := strings.TrimSpace(runGitInOutput(t, work, "rev-parse", "HEAD"))
	if annotated {
		runGitIn(t, work, "tag", "-a", "v1.0.0", "-m", "release")
	} else {
		runGitIn(t, work, "tag", "v1.0.0")
	}
	if err := os.WriteFile(filepath.Join(work, "plugin.txt"), []byte("unrelated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitIn(t, work, "commit", "-am", "unrelated")
	unrelatedCommit := strings.TrimSpace(runGitInOutput(t, work, "rev-parse", "HEAD"))
	runGitIn(t, work, "tag", "unrelated")
	runGitIn(t, work, "push", "origin", "HEAD", "--tags")
	return service, sourcePath, sourceCommit, unrelatedCommit
}

func runGit(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git -C %s %v: %v: %s", dir, args, err, output)
	}
}

func runGitOutput(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = gitEnv()
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

func runGitInOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git -C %s %v: %v", dir, args, err)
	}
	return string(output)
}
