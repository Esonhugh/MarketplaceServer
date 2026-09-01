package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func TestRepositoryBrowserReadsRefsTreeBlobAndHistory(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	service, err := NewService(Config{StorageRoot: root, gitBinary: gitBinary})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := service.InitBareRepository(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	runBrowserGit(t, gitBinary, work, "init", "-b", "main")
	runBrowserGit(t, gitBinary, work, "config", "user.name", "Browser Test")
	runBrowserGit(t, gitBinary, work, "config", "user.email", "browser@example.test")
	if err := os.Mkdir(filepath.Join(work, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", "main.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "binary.dat"), []byte{0x00, 0xff, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "invalid-utf8.txt"), []byte{0xff, 0xfe}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "large.txt"), []byte(strings.Repeat("x", int(gitservice.RepositoryBlobPreviewLimit)+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	runBrowserGit(t, gitBinary, work, "add", ".")
	runBrowserGit(t, gitBinary, work, "commit", "-m", "initial Plugin")
	runBrowserGit(t, gitBinary, work, "tag", "-a", "v1.0.0", "-m", "release")
	if err := os.WriteFile(filepath.Join(work, "src", "main.txt"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runBrowserGit(t, gitBinary, work, "add", "src/main.txt")
	runBrowserGit(t, gitBinary, work, "commit", "-m", "update source")
	bare, err := service.existingRepositoryPath(id)
	if err != nil {
		t.Fatal(err)
	}
	runBrowserGit(t, gitBinary, work, "remote", "add", "origin", bare)
	runBrowserGit(t, gitBinary, work, "push", "origin", "main", "v1.0.0")
	runBrowserGit(t, gitBinary, bare, "symbolic-ref", "HEAD", "refs/heads/main")

	refs, err := service.ListRefs(context.Background(), id)
	if err != nil || refs.DefaultRef != "main" || len(refs.Branches) != 1 || len(refs.Tags) != 1 || !validGitObjectID(refs.Tags[0].CommitSHA) {
		t.Fatalf("refs=%#v err=%v", refs, err)
	}
	tree, err := service.ReadTree(context.Background(), id, "main", "")
	if err != nil || len(tree.Entries) != 5 {
		t.Fatalf("tree=%#v err=%v", tree, err)
	}
	blob, err := service.ReadBlob(context.Background(), id, "v1.0.0", "README.md")
	if err != nil || blob.Content != "# Demo\n" {
		t.Fatalf("blob=%#v err=%v", blob, err)
	}
	history, err := service.ListCommits(context.Background(), id, "main", "src/main.txt", 1, 1)
	if err != nil || history.Total != 2 || len(history.Items) != 1 || history.Items[0].Subject != "update source" || history.Page != 1 || history.Size != 1 {
		t.Fatalf("first history page=%#v err=%v", history, err)
	}
	history, err = service.ListCommits(context.Background(), id, "main", "src/main.txt", 2, 1)
	if err != nil || history.Total != 2 || len(history.Items) != 1 || history.Items[0].Subject != "initial Plugin" || history.Page != 2 || history.Size != 1 {
		t.Fatalf("second history page=%#v err=%v", history, err)
	}

	for name, path := range map[string]string{"binary": "binary.dat", "invalid UTF-8": "invalid-utf8.txt"} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.ReadBlob(context.Background(), id, "main", path); !errors.Is(err, gitservice.ErrPathNotText) {
				t.Fatalf("ReadBlob(%q) error=%v, want ErrPathNotText", path, err)
			}
		})
	}
	if _, err := service.ReadBlob(context.Background(), id, "main", "large.txt"); !errors.Is(err, gitservice.ErrBlobTooLarge) {
		t.Fatalf("large blob error=%v, want ErrBlobTooLarge", err)
	}
}

func TestRepositoryBrowserMapsMissingRevisionAndPath(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	service, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: gitBinary})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := service.InitBareRepository(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	for name, browse := range map[string]func() error{
		"tree missing revision": func() error { _, err := service.ReadTree(context.Background(), id, "missing", ""); return err },
		"blob missing revision": func() error { _, err := service.ReadBlob(context.Background(), id, "missing", "README.md"); return err },
		"history missing revision": func() error {
			_, err := service.ListCommits(context.Background(), id, "missing", "", 1, 20)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := browse(); !errors.Is(err, gitservice.ErrRevisionNotFound) {
				t.Fatalf("error=%v, want ErrRevisionNotFound", err)
			}
		})
	}
	if _, err := service.ListCommits(context.Background(), id, "main", "", 0, 20); !errors.Is(err, gitservice.ErrInvalidBrowseInput) {
		t.Fatalf("invalid pagination error=%v, want ErrInvalidBrowseInput", err)
	}
}

func TestRepositoryBrowserRejectsUnsafeAndUnpreviewableInput(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	service, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: gitBinary})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := service.InitBareRepository(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadTree(context.Background(), id, "--help", ""); !errors.Is(err, gitservice.ErrInvalidBrowseInput) {
		t.Fatalf("unsafe revision error=%v", err)
	}
	if _, err := service.ReadTree(context.Background(), id, "main", "../secret"); !errors.Is(err, gitservice.ErrInvalidBrowseInput) {
		t.Fatalf("unsafe path error=%v", err)
	}
}

func runBrowserGit(t *testing.T, binary, directory string, args ...string) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = directory
	command.Env = append(gitEnv(), "HOME="+t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
