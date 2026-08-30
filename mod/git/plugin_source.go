package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
)

var (
	ErrInvalidCanonicalTag       = errors.New("invalid canonical tag")
	ErrInvalidExpectedPluginSlug = errors.New("invalid expected plugin slug")
	ErrPluginSourceUnavailable   = errors.New("plugin source unavailable")
	ErrPluginSourceInvalid       = errors.New("plugin source validation failed")
)

const (
	pluginManifestPath      = ".claude-plugin/plugin.json"
	maxPluginSourceEntries  = 10_000
	maxPluginSourceFileSize = 16 << 20
	maxPluginSourceBytes    = 256 << 20
	maxPluginManifestBytes  = 1 << 20
)

type pluginSourceInspector struct {
	service         *Service
	validatorBinary string
}

// NewPluginSourceInspector constructs the strict source-inspection capability.
// validatorBinary is deliberately supplied by the caller so tests can run with
// an isolated executable rather than a locally installed Claude binary.
func NewPluginSourceInspector(service *Service, validatorBinary string) (gitservice.PluginSourceInspector, error) {
	if service == nil || validatorBinary == "" {
		return nil, errors.New("plugin source inspector is not configured")
	}
	validator, err := resolveExecutable(validatorBinary)
	if err != nil {
		return nil, errors.New("plugin source inspector is not configured")
	}
	return &pluginSourceInspector{service: service, validatorBinary: validator}, nil
}

func (s *pluginSourceInspector) InspectPluginSource(ctx context.Context, repositoryID, canonicalTag, expectedPluginSlug string) (gitservice.PluginSourceInspection, error) {
	if s == nil || s.service == nil || s.validatorBinary == "" {
		return gitservice.PluginSourceInspection{}, errors.New("plugin source inspector is not configured")
	}
	if err := validateRepositoryID(repositoryID); err != nil {
		return gitservice.PluginSourceInspection{}, err
	}
	if !canonicalTagPattern.MatchString(canonicalTag) {
		return gitservice.PluginSourceInspection{}, ErrInvalidCanonicalTag
	}
	if err := validateSlug(expectedPluginSlug); err != nil {
		return gitservice.PluginSourceInspection{}, ErrInvalidExpectedPluginSlug
	}
	if err := ctx.Err(); err != nil {
		return gitservice.PluginSourceInspection{}, err
	}

	repositoryPath, err := s.service.existingRepositoryPath(repositoryID)
	if err != nil {
		return gitservice.PluginSourceInspection{}, ErrPluginSourceUnavailable
	}
	rawTagObjectID, err := s.resolveRawRefObjectID(ctx, repositoryPath, canonicalTag)
	if err != nil {
		return gitservice.PluginSourceInspection{}, err
	}
	commitObjectID, err := s.peelCommit(ctx, repositoryPath, rawTagObjectID)
	if err != nil {
		return gitservice.PluginSourceInspection{}, err
	}

	materializedRoot, err := os.MkdirTemp("", "marketplace-plugin-source-")
	if err != nil {
		return gitservice.PluginSourceInspection{}, ErrPluginSourceUnavailable
	}
	defer os.RemoveAll(materializedRoot)
	materializedDirectory := filepath.Join(materializedRoot, "source")
	if err := s.materializeCommit(ctx, repositoryPath, commitObjectID, materializedDirectory); err != nil {
		return gitservice.PluginSourceInspection{}, err
	}
	manifestSnapshot, err := readPluginManifest(materializedDirectory)
	if err != nil {
		return gitservice.PluginSourceInspection{}, err
	}
	if !manifestHasExpectedName(manifestSnapshot, expectedPluginSlug) {
		return gitservice.PluginSourceInspection{}, ErrPluginSourceInvalid
	}
	if err := s.runStrictValidator(ctx, materializedDirectory); err != nil {
		return gitservice.PluginSourceInspection{}, err
	}
	digest := sha256.Sum256(manifestSnapshot)
	return gitservice.PluginSourceInspection{
		RawTagObjectID:   rawTagObjectID,
		CommitObjectID:   commitObjectID,
		ManifestDigest:   hex.EncodeToString(digest[:]),
		ManifestSnapshot: append([]byte(nil), manifestSnapshot...),
	}, nil
}

func (s *pluginSourceInspector) resolveRawRefObjectID(ctx context.Context, repositoryPath, canonicalTag string) (string, error) {
	var output bytes.Buffer
	refName := "refs/tags/" + canonicalTag
	if err := s.service.runGit(ctx, nil, &output, io.Discard, "--git-dir="+repositoryPath, "show-ref", "--verify", "--hash", refName); err != nil {
		return "", ErrPluginSourceUnavailable
	}
	rawTagObjectID := strings.TrimSpace(output.String())
	if !validGitObjectID(rawTagObjectID) {
		return "", ErrPluginSourceUnavailable
	}
	return rawTagObjectID, nil
}

func (s *pluginSourceInspector) peelCommit(ctx context.Context, repositoryPath, rawTagObjectID string) (string, error) {
	var output bytes.Buffer
	if err := s.service.runGit(ctx, nil, &output, io.Discard, "--git-dir="+repositoryPath, "rev-parse", "--verify", rawTagObjectID+"^{commit}"); err != nil {
		return "", ErrPluginSourceUnavailable
	}
	commitObjectID := strings.TrimSpace(output.String())
	if !validGitObjectID(commitObjectID) {
		return "", ErrPluginSourceUnavailable
	}
	return commitObjectID, nil
}

func (s *pluginSourceInspector) inspectCommit(ctx context.Context, repositoryPath, rawObjectID, commitObjectID, expectedPluginSlug string) (gitservice.PluginSourceInspection, error) {
	if !validGitObjectID(rawObjectID) || !validGitObjectID(commitObjectID) || validateSlug(expectedPluginSlug) != nil {
		return gitservice.PluginSourceInspection{}, ErrPluginSourceInvalid
	}
	materializedRoot, err := os.MkdirTemp("", "marketplace-plugin-receive-")
	if err != nil {
		return gitservice.PluginSourceInspection{}, ErrPluginSourceUnavailable
	}
	defer os.RemoveAll(materializedRoot)
	materializedDirectory := filepath.Join(materializedRoot, "source")
	if err := s.materializeCommit(ctx, repositoryPath, commitObjectID, materializedDirectory); err != nil {
		return gitservice.PluginSourceInspection{}, err
	}
	manifestSnapshot, err := readPluginManifest(materializedDirectory)
	if err != nil || !manifestHasExpectedName(manifestSnapshot, expectedPluginSlug) {
		return gitservice.PluginSourceInspection{}, ErrPluginSourceInvalid
	}
	if err := s.runStrictValidator(ctx, materializedDirectory); err != nil {
		return gitservice.PluginSourceInspection{}, err
	}
	digest := sha256.Sum256(manifestSnapshot)
	return gitservice.PluginSourceInspection{
		RawTagObjectID: rawObjectID, CommitObjectID: commitObjectID,
		ManifestDigest: hex.EncodeToString(digest[:]), ManifestSnapshot: append([]byte(nil), manifestSnapshot...),
	}, nil
}

func (s *pluginSourceInspector) materializeCommit(ctx context.Context, repositoryPath, commitObjectID, directory string) error {
	if err := os.Mkdir(directory, 0o700); err != nil {
		return ErrPluginSourceUnavailable
	}
	command := exec.CommandContext(ctx, s.service.gitBinary, "--git-dir="+repositoryPath, "ls-tree", "-rz", "--full-tree", commitObjectID)
	command.Env = pluginSourceGitEnv()
	command.Stderr = io.Discard
	output, err := command.Output()
	if err != nil {
		return ErrPluginSourceUnavailable
	}
	entries, err := parsePluginSourceTree(output)
	if err != nil {
		return ErrPluginSourceInvalid
	}
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, err := pluginSourcePath(directory, entry.name)
		if err != nil {
			return ErrPluginSourceInvalid
		}
		if err := makePluginSourceDirectory(directory, filepath.Dir(path)); err != nil {
			return ErrPluginSourceInvalid
		}
		switch entry.mode {
		case "100644", "100755":
			size, err := s.objectSize(ctx, repositoryPath, entry.objectID)
			if err != nil || size > maxPluginSourceFileSize || total+size > maxPluginSourceBytes {
				return ErrPluginSourceInvalid
			}
			if err := s.materializeBlob(ctx, repositoryPath, entry.objectID, path, size); err != nil {
				return err
			}
			total += size
		case "120000":
			size, err := s.objectSize(ctx, repositoryPath, entry.objectID)
			if err != nil || size <= 0 || size > maxPluginSourceFileSize || total+size > maxPluginSourceBytes {
				return ErrPluginSourceInvalid
			}
			target, err := s.readBlob(ctx, repositoryPath, entry.objectID, size)
			if err != nil || createPluginSourceSymlink(directory, path, string(target)) != nil {
				return ErrPluginSourceInvalid
			}
			total += size
		default:
			return ErrPluginSourceInvalid
		}
	}
	return nil
}

type pluginSourceTreeEntry struct {
	mode     string
	objectID string
	name     string
}

func parsePluginSourceTree(output []byte) ([]pluginSourceTreeEntry, error) {
	records := bytes.Split(output, []byte{0})
	if len(records) == 0 || len(records[len(records)-1]) != 0 {
		return nil, errors.New("invalid tree listing")
	}
	records = records[:len(records)-1]
	if len(records) > maxPluginSourceEntries {
		return nil, errors.New("too many tree entries")
	}
	entries := make([]pluginSourceTreeEntry, 0, len(records))
	for _, record := range records {
		metadata, name, found := bytes.Cut(record, []byte{'\t'})
		fields := bytes.Fields(metadata)
		if !found || len(fields) != 3 || string(fields[1]) != "blob" || !validGitObjectID(string(fields[2])) {
			return nil, errors.New("invalid tree entry")
		}
		entries = append(entries, pluginSourceTreeEntry{mode: string(fields[0]), objectID: string(fields[2]), name: string(name)})
	}
	return entries, nil
}

func pluginSourcePath(destination, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.ContainsRune(name, 0) {
		return "", errors.New("invalid source path")
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid source path")
		}
	}
	path := filepath.Join(destination, filepath.FromSlash(name))
	if err := ensurePathWithinRoot(destination, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *pluginSourceInspector) objectSize(ctx context.Context, repositoryPath, objectID string) (int64, error) {
	var output bytes.Buffer
	command := exec.CommandContext(ctx, s.service.gitBinary, "--git-dir="+repositoryPath, "cat-file", "-s", objectID)
	command.Env = pluginSourceGitEnv()
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return 0, err
	}
	var size int64
	if _, err := fmt.Fscan(strings.NewReader(output.String()), &size); err != nil || size < 0 {
		return 0, errors.New("invalid blob size")
	}
	return size, nil
}

func (s *pluginSourceInspector) materializeBlob(ctx context.Context, repositoryPath, objectID, path string, size int64) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrPluginSourceInvalid
	}
	command := exec.CommandContext(ctx, s.service.gitBinary, "--git-dir="+repositoryPath, "cat-file", "blob", objectID)
	command.Env = pluginSourceGitEnv()
	command.Stdout = file
	command.Stderr = io.Discard
	runErr := command.Run()
	closeErr := file.Close()
	if runErr != nil || closeErr != nil {
		return ErrPluginSourceUnavailable
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != size {
		return ErrPluginSourceUnavailable
	}
	return nil
}

func (s *pluginSourceInspector) readBlob(ctx context.Context, repositoryPath, objectID string, size int64) ([]byte, error) {
	if size > maxPluginSourceFileSize {
		return nil, ErrPluginSourceInvalid
	}
	command := exec.CommandContext(ctx, s.service.gitBinary, "--git-dir="+repositoryPath, "cat-file", "blob", objectID)
	command.Env = pluginSourceGitEnv()
	command.Stderr = io.Discard
	output, err := command.Output()
	if err != nil || int64(len(output)) != size {
		return nil, ErrPluginSourceUnavailable
	}
	return output, nil
}

func makePluginSourceDirectory(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("archive directory escapes materialization")
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("archive directory is not a directory")
		}
	}
	return nil
}

func createPluginSourceSymlink(root, path, linkTarget string) error {
	if linkTarget == "" || filepath.IsAbs(linkTarget) || strings.ContainsRune(linkTarget, 0) {
		return errors.New("invalid archive symlink")
	}
	resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(linkTarget)))
	if err := ensurePathWithinRoot(root, resolvedTarget); err != nil {
		return errors.New("archive symlink escapes materialization")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("duplicate archive entry")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(linkTarget, path)
}

func (s *pluginSourceInspector) runStrictValidator(ctx context.Context, directory string) error {
	output := &boundedValidatorOutput{remaining: 1 << 20}
	command := exec.CommandContext(ctx, s.validatorBinary, "plugin", "validate", directory, "--strict")
	command.Env = pluginSourceGitEnv()
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return ErrPluginSourceInvalid
	}
	if output.overflow || validatorOutputHasDiagnostic(string(output.contents)) {
		return ErrPluginSourceInvalid
	}
	return nil
}

type boundedValidatorOutput struct {
	contents  []byte
	remaining int
	overflow  bool
}

func (output *boundedValidatorOutput) Write(data []byte) (int, error) {
	if len(data) > output.remaining {
		output.contents = append(output.contents, data[:output.remaining]...)
		output.remaining = 0
		output.overflow = true
		return len(data), nil
	}
	output.contents = append(output.contents, data...)
	output.remaining -= len(data)
	return len(data), nil
}

func readPluginManifest(directory string) ([]byte, error) {
	manifestPath := filepath.Join(directory, filepath.FromSlash(pluginManifestPath))
	if err := ensurePathWithinRoot(directory, manifestPath); err != nil {
		return nil, ErrPluginSourceInvalid
	}
	info, err := os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxPluginManifestBytes {
		return nil, ErrPluginSourceInvalid
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, ErrPluginSourceInvalid
	}
	defer file.Close()
	snapshot, err := io.ReadAll(io.LimitReader(file, maxPluginManifestBytes+1))
	if err != nil || len(snapshot) > maxPluginManifestBytes {
		return nil, ErrPluginSourceInvalid
	}
	return snapshot, nil
}

func manifestHasExpectedName(snapshot []byte, expectedPluginSlug string) bool {
	var manifest struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(snapshot, &manifest) == nil && manifest.Name == expectedPluginSlug
}

func validatorOutputHasDiagnostic(output string) bool {
	lowerOutput := strings.ToLower(output)
	return strings.Contains(lowerOutput, "warning") || strings.Contains(lowerOutput, "error")
}

func pluginSourceGitEnv() []string {
	env := []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_TERMINAL_PROMPT=0",
		"LANG=C",
	}
	if path := os.Getenv("PATH"); path != "" {
		env = append(env, "PATH="+path)
	}
	return env
}

func resolveExecutable(binary string) (string, error) {
	if strings.Contains(binary, string(os.PathSeparator)) || filepath.IsAbs(binary) {
		info, err := os.Stat(binary)
		if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			return "", errors.New("not executable")
		}
		return binary, nil
	}
	return exec.LookPath(binary)
}
