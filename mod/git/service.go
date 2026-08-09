package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
)

var (
	_ gitservice.RepositoryService  = (*Service)(nil)
	_ gitservice.DistributionReader = (*Service)(nil)
	_ gitservice.ProjectionBuilder  = (*Service)(nil)
)

var (
	slugPattern         = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	repositoryIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{1,127}$`)
)

var (
	ErrInvalidSlug         = errors.New("invalid git slug")
	ErrInvalidRepositoryID = errors.New("invalid repository id")
	ErrRepositoryNotFound  = errors.New("repository not found")
	ErrUnsupportedService  = errors.New("unsupported git service")
)

type Service struct {
	storageRoot      string
	gitBinary        string
	advertiseTimeout time.Duration
	serviceTimeout   time.Duration
}

func NewService(config Config) (*Service, error) {
	if config.StorageRoot == "" {
		return nil, errors.New("git storageRoot is required")
	}
	if config.gitBinary == "" {
		config.gitBinary = defaultGitBinary
	}
	gitBinary, err := resolveGitBinary(config.gitBinary)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(config.StorageRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Service{
		storageRoot:      root,
		gitBinary:        gitBinary,
		advertiseTimeout: config.advertiseTimeout,
		serviceTimeout:   config.serviceTimeout,
	}, nil
}

func (s *Service) InitBareRepository(ctx context.Context, repositoryID string) error {
	path, err := s.repositoryPath(repositoryID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return errors.New("repository already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return s.runGit(ctx, nil, io.Discard, io.Discard, "init", "--bare", path)
}

func (s *Service) AdvertiseRefs(ctx context.Context, service, repositoryID string, stdout, stderr io.Writer) error {
	gitArg, err := gitServiceArgument(service)
	if err != nil {
		return err
	}
	path, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return err
	}
	if s.advertiseTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.advertiseTimeout)
		defer cancel()
	}
	var advertisement bytes.Buffer
	if _, err := fmt.Fprintf(&advertisement, "%04x# service=%s\n", len("# service="+service+"\n")+4, service); err != nil {
		return err
	}
	if _, err := io.WriteString(&advertisement, "0000"); err != nil {
		return err
	}
	if err := s.runGit(ctx, nil, &advertisement, stderr, gitArg, "--stateless-rpc", "--advertise-refs", path); err != nil {
		return err
	}
	_, err = advertisement.WriteTo(stdout)
	return err
}

func (s *Service) UploadPack(ctx context.Context, repositoryID string, stdin io.Reader, stdout, stderr io.Writer) error {
	return s.runService(ctx, "git-upload-pack", repositoryID, stdin, stdout, stderr)
}

func (s *Service) ReceivePack(ctx context.Context, repositoryID string, stdin io.Reader, stdout, stderr io.Writer) error {
	return s.runService(ctx, "git-receive-pack", repositoryID, stdin, stdout, stderr)
}

func (s *Service) runService(ctx context.Context, service, repositoryID string, stdin io.Reader, stdout, stderr io.Writer) error {
	gitArg, err := gitServiceArgument(service)
	if err != nil {
		return err
	}
	path, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return err
	}
	if s.serviceTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.serviceTimeout)
		defer cancel()
	}
	return s.runGit(ctx, stdin, stdout, stderr, gitArg, "--stateless-rpc", path)
}

func (s *Service) runGit(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, s.gitBinary, args...)
	cmd.Env = gitEnv()
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (s *Service) existingRepositoryPath(repositoryID string) (string, error) {
	path, err := s.repositoryPath(repositoryID)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrRepositoryNotFound
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", ErrRepositoryNotFound
	}
	return path, nil
}

func (s *Service) repositoryPath(repositoryID string) (string, error) {
	if err := validateRepositoryID(repositoryID); err != nil {
		return "", err
	}
	shard := strings.ToLower(repositoryID[:2])
	path := filepath.Join(s.storageRoot, "repositories", shard, repositoryID+".git")
	if err := s.ensureNoSymlinkEscape(path); err != nil {
		return "", err
	}
	if err := ensurePathWithinRoot(s.storageRoot, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) ensureNoSymlinkEscape(path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(s.storageRoot)
	if err != nil {
		return err
	}
	current := s.storageRoot
	rel, err := filepath.Rel(s.storageRoot, path)
	if err != nil {
		return err
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		resolved, err := filepath.EvalSymlinks(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ensurePathWithinRoot(resolvedRoot, resolved); err != nil {
			return err
		}
	}
	return nil
}

func ensurePathWithinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errors.New("repository path escapes storage root")
	}
	return nil
}

func validateSlug(slug string) error {
	if !slugPattern.MatchString(slug) {
		return ErrInvalidSlug
	}
	return nil
}

func validateRepositoryID(repositoryID string) error {
	if strings.HasSuffix(repositoryID, ".git") || !repositoryIDPattern.MatchString(repositoryID) {
		return ErrInvalidRepositoryID
	}
	return nil
}

func gitServiceArgument(service string) (string, error) {
	switch service {
	case "git-upload-pack":
		return "upload-pack", nil
	case "git-receive-pack":
		return "receive-pack", nil
	default:
		return "", ErrUnsupportedService
	}
}

func resolveGitBinary(gitBinary string) (string, error) {
	if strings.Contains(gitBinary, string(os.PathSeparator)) || filepath.IsAbs(gitBinary) {
		info, err := os.Stat(gitBinary)
		if err != nil {
			return "", err
		}
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("git binary %q is not executable", gitBinary)
		}
		return gitBinary, nil
	}
	path, err := exec.LookPath(gitBinary)
	if err != nil {
		return "", err
	}
	return path, nil
}

func gitEnv() []string {
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"LANG=C",
	}
	if path := os.Getenv("PATH"); path != "" {
		env = append(env, "PATH="+path)
	}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}
	return env
}

type SSHGitCommand struct {
	Service    string
	Namespace  string
	Repository string
}

func ParseSSHGitCommand(raw string) (SSHGitCommand, error) {
	parts := strings.Split(raw, " ")
	if len(parts) != 2 {
		return SSHGitCommand{}, errors.New("invalid ssh git command")
	}
	if parts[0] != "git-upload-pack" && parts[0] != "git-receive-pack" {
		return SSHGitCommand{}, ErrUnsupportedService
	}
	path := parts[1]
	if !strings.HasPrefix(path, "'") || !strings.HasSuffix(path, "'") {
		return SSHGitCommand{}, errors.New("git repository path must be single quoted")
	}
	path = strings.TrimSuffix(strings.TrimPrefix(path, "'"), "'")
	if strings.HasPrefix(path, "/") || strings.Contains(path, "//") || strings.Contains(path, "\\") {
		return SSHGitCommand{}, errors.New("invalid repository path")
	}
	if !strings.HasSuffix(path, ".git") {
		return SSHGitCommand{}, errors.New("repository path must end with .git")
	}
	path = strings.TrimSuffix(path, ".git")
	segments := strings.Split(path, "/")
	if len(segments) != 2 {
		return SSHGitCommand{}, errors.New("repository path must be namespace/repository.git")
	}
	if err := validateSlug(segments[0]); err != nil {
		return SSHGitCommand{}, err
	}
	if err := validateSlug(segments[1]); err != nil {
		return SSHGitCommand{}, err
	}
	return SSHGitCommand{Service: parts[0], Namespace: segments[0], Repository: segments[1]}, nil
}
