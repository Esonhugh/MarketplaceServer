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
	"sync"
	"syscall"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

var (
	_ gitservice.RepositoryService       = (*Service)(nil)
	_ gitservice.RepositoryProvisioner   = (*Service)(nil)
	_ gitservice.RepositoryOrphanCleaner = (*Service)(nil)
	_ gitservice.DistributionReader      = (*Service)(nil)
	_ gitservice.ProjectionBuilder       = (*Service)(nil)
	_ gitservice.PluginRefReader         = (*Service)(nil)
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

	provisioningMu sync.Mutex
	provisioning   map[string]*provisioningReceipt
}

type provisioningReceipt struct {
	identity gitservice.RepositoryIdentity
	removed  bool
}

const (
	protectedReceiveHookMarker    = "# MarketplaceServer protected receive hook v1"
	protectedReceiveHookModeEnv   = "MARKETPLACE_RECEIVE_HOOK_MODE"
	protectedReceiveHookSocketEnv = "MARKETPLACE_RECEIVE_HOOK_SOCKET"
)

var protectedReceiveHookModes = map[string]bool{"pre-receive": true, "proc-receive": true}

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
		provisioning:     make(map[string]*provisioningReceipt),
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
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "init", "--bare", path); err != nil {
		return err
	}
	return s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+path, "symbolic-ref", "HEAD", "refs/heads/main")
}

func (s *Service) ProvisionRepository(ctx context.Context, identity gitservice.RepositoryIdentity) (gitservice.ProvisionedRepository, error) {
	s.provisioningMu.Lock()
	defer s.provisioningMu.Unlock()

	path, err := s.provisioningRepositoryPath(identity)
	if err != nil {
		return gitservice.ProvisionedRepository{}, err
	}
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return gitservice.ProvisionedRepository{}, gitservice.ErrRepositoryUnavailable
		}
		if err := s.installProtectedReceiveHooks(ctx, path); err != nil {
			return gitservice.ProvisionedRepository{}, fmt.Errorf("repair protected receive hooks: %w", err)
		}
		return gitservice.ProvisionedRepository{RepositoryIdentity: identity}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return gitservice.ProvisionedRepository{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return gitservice.ProvisionedRepository{}, err
	}
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "init", "--bare", path); err != nil {
		return gitservice.ProvisionedRepository{}, err
	}
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+path, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		if cleanupErr := s.removeRepositoryPath(path); cleanupErr != nil {
			return gitservice.ProvisionedRepository{}, fmt.Errorf("set repository HEAD: %w (cleanup: %v)", err, cleanupErr)
		}
		return gitservice.ProvisionedRepository{}, fmt.Errorf("set repository HEAD: %w", err)
	}
	if err := s.installProtectedReceiveHooks(ctx, path); err != nil {
		if cleanupErr := s.removeRepositoryPath(path); cleanupErr != nil {
			return gitservice.ProvisionedRepository{}, fmt.Errorf("install protected receive hooks: %w (cleanup: %v)", err, cleanupErr)
		}
		return gitservice.ProvisionedRepository{}, fmt.Errorf("install protected receive hooks: %w", err)
	}

	receipt := uuid.NewString()
	s.provisioning[receipt] = &provisioningReceipt{identity: identity}
	return gitservice.NewProvisionedRepository(identity, receipt), nil
}

func (s *Service) RemoveProvisionedRepository(_ context.Context, provisioned gitservice.ProvisionedRepository) error {
	if !provisioned.Created() {
		return gitservice.ErrRepositoryNotProvisioned
	}
	var receipt *provisioningReceipt
	known := provisioned.ConsumeReceipt(func(token string) bool {
		s.provisioningMu.Lock()
		defer s.provisioningMu.Unlock()
		receipt = s.provisioning[token]
		return receipt != nil && receipt.identity == provisioned.RepositoryIdentity
	})
	if !known {
		return gitservice.ErrRepositoryNotProvisioned
	}
	s.provisioningMu.Lock()
	defer s.provisioningMu.Unlock()
	if receipt.removed {
		return nil
	}

	path, err := s.provisioningRepositoryPath(provisioned.RepositoryIdentity)
	if err != nil {
		return err
	}
	if err := s.removeRepositoryPath(path); err != nil {
		return err
	}
	receipt.removed = true
	return nil
}

func (s *Service) RemoveOrphanRepository(ctx context.Context, storageKey string) error {
	identity := gitservice.RepositoryIdentity{ID: storageKey, StorageKey: storageKey}
	path, err := s.provisioningRepositoryPath(identity)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.removeRepositoryPath(path)
}

func (s *Service) ReadPluginRef(ctx context.Context, repositoryID, refName string) (gitservice.ObservedReceiveRef, error) {
	if !validReceiveRefName(refName) {
		return gitservice.ObservedReceiveRef{}, errors.New("invalid Git ref")
	}
	path, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return gitservice.ObservedReceiveRef{}, err
	}
	objectID, err := s.gitOutput(ctx, nil, "--git-dir="+path, "rev-parse", "--verify", refName)
	if err != nil {
		return gitservice.ObservedReceiveRef{RefName: refName}, nil
	}
	objectID = strings.TrimSpace(objectID)
	if !validGitObjectID(objectID) {
		return gitservice.ObservedReceiveRef{}, errors.New("invalid Git ref object")
	}
	return gitservice.ObservedReceiveRef{RefName: refName, ObjectID: objectID, Exists: true}, nil
}

func validGitObjectID(objectID string) bool {
	if len(objectID) != 40 && len(objectID) != 64 {
		return false
	}
	for _, character := range objectID {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func (s *Service) ListRepositoryOrphanCandidates(ctx context.Context, minimumAge time.Duration) ([]gitservice.RepositoryOrphanCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if minimumAge <= 0 {
		return nil, errors.New("minimum orphan age must be positive")
	}
	repositoriesRoot := filepath.Join(s.storageRoot, "repositories")
	if err := s.ensureNoSymlinkEscape(repositoriesRoot); err != nil {
		return nil, err
	}
	if err := ensurePathWithinRoot(s.storageRoot, repositoriesRoot); err != nil {
		return nil, err
	}
	shards, err := os.ReadDir(repositoriesRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-minimumAge)
	candidates := make([]gitservice.RepositoryOrphanCandidate, 0)
	for _, shard := range shards {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !shard.IsDir() || shard.Type()&os.ModeSymlink != 0 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(repositoriesRoot, shard.Name()))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".git") {
				continue
			}
			storageKey := strings.TrimSuffix(entry.Name(), ".git")
			identity := gitservice.RepositoryIdentity{ID: storageKey, StorageKey: storageKey}
			path, err := s.provisioningRepositoryPath(identity)
			if err != nil {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				return nil, err
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			candidates = append(candidates, gitservice.RepositoryOrphanCandidate{StorageKey: storageKey, CreatedAt: info.ModTime()})
		}
	}
	return candidates, nil
}

func (s *Service) removeRepositoryPath(path string) error {
	if err := s.ensureNoSymlinkEscape(path); err != nil {
		return err
	}
	if err := ensurePathWithinRoot(s.storageRoot, path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.RemoveAll(path)
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
	path, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return err
	}
	receive, protected := ctx.Value(receiveContextKey{}).(receiveContext)
	if !protected {
		return s.runService(ctx, "git-receive-pack", repositoryID, stdin, stdout, stderr)
	}
	inspector, ok := receive.inspector.(*pluginSourceInspector)
	if !ok || inspector == nil {
		return errors.New("protected receive inspector unavailable")
	}
	if s.serviceTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.serviceTimeout)
		defer cancel()
	}
	session, err := startReceiveHookSession(ctx, s, inspector, receive)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, s.gitBinary, "receive-pack", "--stateless-rpc", path)
	command.Env = append(gitEnv(), protectedReceiveHookSocketEnv+"="+session.SocketPath())
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		session.Abort()
		return err
	}
	runErr := command.Wait()
	hookErr := session.Wait(runErr)
	if runErr != nil {
		return runErr
	}
	return hookErr
}

func (s *Service) InstallProtectedReceiveHooks(ctx context.Context, repositoryID string) error {
	repositoryPath, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return err
	}
	return s.installProtectedReceiveHooks(ctx, repositoryPath)
}

func (s *Service) installProtectedReceiveHooks(ctx context.Context, repositoryPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return errors.New("protected receive executable unavailable")
	}
	hooksDirectory := filepath.Join(repositoryPath, "hooks")
	if err := ensurePathWithinRoot(s.storageRoot, hooksDirectory); err != nil {
		return err
	}
	if err := os.MkdirAll(hooksDirectory, 0o700); err != nil {
		return errors.New("create protected receive hooks")
	}
	executableCommand := shellQuoteHookArgument(executable)
	if strings.HasSuffix(executable, ".test") {
		executableCommand += " -test.run=^TestProtectedReceiveHookProcess$"
	}
	for mode := range protectedReceiveHookModes {
		hookPath := filepath.Join(hooksDirectory, mode)
		content := "#!/bin/sh\n" + protectedReceiveHookMarker + "\n" + protectedReceiveHookModeEnv + "=" + shellQuoteHookArgument(mode) + " exec " + executableCommand + "\n"
		if err := writeAtomicHook(hookPath, []byte(content)); err != nil {
			return err
		}
	}
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+repositoryPath, "config", "--replace-all", "receive.procReceiveRefs", "refs/heads/"); err != nil {
		return errors.New("configure protected receive refs")
	}
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+repositoryPath, "config", "--add", "receive.procReceiveRefs", "refs/tags/"); err != nil {
		return errors.New("configure protected receive refs")
	}
	return s.verifyProtectedReceiveHooks(repositoryPath)
}

func (s *Service) VerifyProtectedReceiveHooks(repositoryID string) error {
	repositoryPath, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return err
	}
	return s.verifyProtectedReceiveHooks(repositoryPath)
}

func (s *Service) verifyProtectedReceiveHooks(repositoryPath string) error {
	for mode := range protectedReceiveHookModes {
		hookPath := filepath.Join(repositoryPath, "hooks", mode)
		info, err := os.Lstat(hookPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("protected receive hook prerequisite missing")
		}
		content, err := os.ReadFile(hookPath)
		if err != nil || !strings.Contains(string(content), "\n"+protectedReceiveHookMarker+"\n") {
			return errors.New("protected receive hook prerequisite invalid")
		}
	}
	return nil
}

func ProtectedReceiveHookMode() (string, bool) {
	mode, ok := os.LookupEnv(protectedReceiveHookModeEnv)
	return mode, ok && protectedReceiveHookModes[mode]
}

func RunProtectedReceiveHook(ctx context.Context, input io.Reader, output io.Writer) error {
	mode, ok := ProtectedReceiveHookMode()
	if !ok {
		return errors.New("protected receive hook mode unavailable")
	}
	socketPath := os.Getenv(protectedReceiveHookSocketEnv)
	return runReceiveHookClient(ctx, mode, socketPath, input, output)
}

func shellQuoteHookArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeAtomicHook(path string, content []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".protected-receive-*")
	if err != nil {
		return errors.New("create protected receive hook")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o700); err != nil {
		_ = temporary.Close()
		return errors.New("configure protected receive hook")
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return errors.New("write protected receive hook")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errors.New("sync protected receive hook")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close protected receive hook")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return errors.New("install protected receive hook")
		}
		return errors.New("install protected receive hook")
	}
	return nil
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

func (s *Service) provisioningRepositoryPath(identity gitservice.RepositoryIdentity) (string, error) {
	if err := validateRepositoryIdentity(identity); err != nil {
		return "", err
	}
	path := filepath.Join(s.storageRoot, "repositories", identity.StorageKey[:2], identity.StorageKey+".git")
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

func validateRepositoryIdentity(identity gitservice.RepositoryIdentity) error {
	id, err := uuid.Parse(identity.ID)
	if err != nil || id.String() != identity.ID {
		return ErrInvalidRepositoryID
	}
	storageKey, err := uuid.Parse(identity.StorageKey)
	if err != nil || storageKey.String() != identity.StorageKey {
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
