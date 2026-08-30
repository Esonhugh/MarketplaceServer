package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func requireGit(t *testing.T) string {
	t.Helper()
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary is not available")
	}
	return gitBinary
}

func TestProvisionRepositoryCreatesBareRepositoryWithMainHEAD(t *testing.T) {
	gitBinary := requireGit(t)
	root := t.TempDir()
	svc, err := NewService(Config{StorageRoot: root, gitBinary: gitBinary})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	identity := gitservice.RepositoryIdentity{ID: uuid.NewString(), StorageKey: uuid.NewString()}
	provisioned, err := svc.ProvisionRepository(context.Background(), identity)
	if err != nil {
		t.Fatalf("ProvisionRepository() error = %v", err)
	}
	if !provisioned.Created() {
		t.Fatal("ProvisionRepository() did not report a newly created repository")
	}

	path, err := svc.provisioningRepositoryPath(identity)
	if err != nil {
		t.Fatalf("provisioningRepositoryPath() error = %v", err)
	}
	head, err := os.ReadFile(filepath.Join(path, "HEAD"))
	if err != nil {
		t.Fatalf("read HEAD: %v", err)
	}
	if got, want := string(head), "ref: refs/heads/main\n"; got != want {
		t.Fatalf("HEAD = %q, want %q", got, want)
	}
}

func TestProvisionRepositoryIsIdempotentForSameIdentity(t *testing.T) {
	fake := newFakeGit(t)
	svc, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	repositoryID := uuid.NewString()
	identity := gitservice.RepositoryIdentity{ID: repositoryID, StorageKey: repositoryID}
	first, err := svc.ProvisionRepository(context.Background(), identity)
	if err != nil {
		t.Fatalf("first ProvisionRepository() error = %v", err)
	}
	if !first.Created() {
		t.Fatal("first ProvisionRepository() did not report creation")
	}
	second, err := svc.ProvisionRepository(context.Background(), identity)
	if err != nil {
		t.Fatalf("second ProvisionRepository() error = %v", err)
	}
	if second.Created() {
		t.Fatal("second ProvisionRepository() reported a new repository")
	}

	path, err := svc.provisioningRepositoryPath(identity)
	if err != nil {
		t.Fatalf("provisioningRepositoryPath() error = %v", err)
	}
	fake.assertInvocation(t, []string{"init", "--bare", path})
	if got := strings.Count(fake.readLog(t), "ARG:init\n"); got != 1 {
		t.Fatalf("init invocation count = %d, want 1; log:\n%s", got, fake.readLog(t))
	}
}

func TestProvisionRepositoryRejectsInvalidOpaqueIdentityWithoutExecutingGit(t *testing.T) {
	fake := newFakeGit(t)
	svc, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	valid := uuid.NewString()
	for _, identity := range []gitservice.RepositoryIdentity{
		{},
		{ID: valid},
		{StorageKey: valid},
		{ID: "not-a-uuid", StorageKey: valid},
		{ID: valid, StorageKey: "not-a-uuid"},
		{ID: strings.ToUpper(valid), StorageKey: valid},
		{ID: valid, StorageKey: strings.ToUpper(valid)},
	} {
		if _, err := svc.ProvisionRepository(context.Background(), identity); err == nil {
			t.Fatalf("ProvisionRepository(%#v) succeeded; want error", identity)
		}
	}
	if got := fake.readLog(t); got != "" {
		t.Fatalf("invalid identity executed git: %q", got)
	}
}

func TestProvisionRepositoryRejectsSymlinkEscape(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "repositories")); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	repositoryID := uuid.NewString()
	identity := gitservice.RepositoryIdentity{ID: repositoryID, StorageKey: repositoryID}
	if _, err := svc.ProvisionRepository(context.Background(), identity); err == nil {
		t.Fatal("ProvisionRepository() succeeded through symlink; want error")
	}
	if got := fake.readLog(t); got != "" {
		t.Fatalf("symlink escape executed git: %q", got)
	}
}

func TestRemoveProvisionedRepositoryOnlyRemovesMatchingNewRepository(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	repositoryID := uuid.NewString()
	identity := gitservice.RepositoryIdentity{ID: repositoryID, StorageKey: repositoryID}
	provisioned, err := svc.ProvisionRepository(context.Background(), identity)
	if err != nil {
		t.Fatalf("ProvisionRepository() error = %v", err)
	}
	if err := svc.RemoveProvisionedRepository(context.Background(), provisioned); err != nil {
		t.Fatalf("RemoveProvisionedRepository() error = %v", err)
	}
	path, err := svc.provisioningRepositoryPath(identity)
	if err != nil {
		t.Fatalf("provisioningRepositoryPath() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository remains after removal: %v", err)
	}
	if err := svc.RemoveProvisionedRepository(context.Background(), provisioned); err != nil {
		t.Fatalf("second RemoveProvisionedRepository() error = %v", err)
	}
}

func TestRemoveProvisionedRepositoryRefusesRepositoryWithoutProvisioningReceipt(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	repositoryID := uuid.NewString()
	identity := gitservice.RepositoryIdentity{ID: repositoryID, StorageKey: repositoryID}
	path, err := svc.provisioningRepositoryPath(identity)
	if err != nil {
		t.Fatalf("provisioningRepositoryPath() error = %v", err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveProvisionedRepository(context.Background(), gitservice.ProvisionedRepository{RepositoryIdentity: identity}); !errors.Is(err, gitservice.ErrRepositoryNotProvisioned) {
		t.Fatalf("RemoveProvisionedRepository() error = %v, want ErrRepositoryNotProvisioned", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unowned repository was removed: %v", err)
	}
}

func TestListRepositoryOrphanCandidatesReturnsOnlyMinimumAgeUUIDDirectories(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	oldID := uuid.NewString()
	oldIdentity := gitservice.RepositoryIdentity{ID: oldID, StorageKey: oldID}
	oldPath, err := svc.provisioningRepositoryPath(oldIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldPath, 0o700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	recentID := uuid.NewString()
	recentIdentity := gitservice.RepositoryIdentity{ID: recentID, StorageKey: recentID}
	recentPath, err := svc.provisioningRepositoryPath(recentIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(recentPath, 0o700); err != nil {
		t.Fatal(err)
	}

	nonUUIDPath := filepath.Join(root, "repositories", "bad", "not-a-uuid.git")
	if err := os.MkdirAll(nonUUIDPath, 0o700); err != nil {
		t.Fatal(err)
	}

	candidates, err := svc.ListRepositoryOrphanCandidates(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("ListRepositoryOrphanCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].StorageKey != oldID {
		t.Fatalf("candidates = %#v, want only %q", candidates, oldID)
	}
	if candidates[0].CreatedAt.After(time.Now().Add(-time.Hour)) {
		t.Fatalf("candidate timestamp = %v, want at least one hour old", candidates[0].CreatedAt)
	}
}

func TestListRepositoryOrphanCandidatesRejectsNonPositiveMinimumAge(t *testing.T) {
	fake := newFakeGit(t)
	svc, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := svc.ListRepositoryOrphanCandidates(context.Background(), 0); err == nil {
		t.Fatal("ListRepositoryOrphanCandidates() with zero age succeeded; want error")
	}
	if _, err := svc.ListRepositoryOrphanCandidates(context.Background(), -time.Second); err == nil {
		t.Fatal("ListRepositoryOrphanCandidates() with negative age succeeded; want error")
	}
}
