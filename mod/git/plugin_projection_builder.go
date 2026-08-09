package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/google/uuid"
)

var (
	tagNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/+\-]{0,254}$`)
	objectIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func (s *Service) BuildPluginProjection(ctx context.Context, cmd gitservice.BuildPluginProjectionCommand) (result gitservice.PluginProjectionResult, err error) {
	if cmd.ProjectionID == uuid.Nil {
		return result, errors.New("plugin projection ID is required")
	}
	if !tagNamePattern.MatchString(cmd.TagName) || strings.Contains(cmd.TagName, "..") || strings.HasSuffix(cmd.TagName, ".") || strings.Contains(cmd.TagName, "@{") {
		return result, errors.New("invalid plugin distribution tag")
	}
	sourcePath, err := s.existingRepositoryPath(cmd.RepositoryID)
	if err != nil {
		return result, err
	}
	tagRef := "refs/tags/" + cmd.TagName
	sourceTagType, sourceTagObjectID, sourceCommitSHA, sourceTreeSHA, err := s.resolveSourceTag(ctx, sourcePath, tagRef)
	if err != nil {
		return result, err
	}

	projection := gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindPlugin, StorageKey: cmd.ProjectionID.String()}
	finalPath, err := s.projectionPath(projection)
	if err != nil {
		return result, err
	}
	if _, err := os.Stat(finalPath); err == nil {
		return result, errors.New("plugin projection already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	quarantinePath, err := s.quarantinePath(uuid.New())
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(quarantinePath)
		}
	}()
	if err = os.MkdirAll(filepath.Dir(quarantinePath), 0o700); err != nil {
		return result, err
	}
	if err = s.runGit(ctx, nil, io.Discard, io.Discard, "init", "--bare", quarantinePath); err != nil {
		return result, err
	}
	if err = s.copyTreeClosure(ctx, sourcePath, quarantinePath, sourceTreeSHA); err != nil {
		return result, err
	}
	publishedAt := cmd.PublishedAt.UTC()
	if publishedAt.IsZero() {
		return result, errors.New("plugin publication time is required")
	}
	distributionSHA, err := s.createSnapshotCommit(ctx, quarantinePath, sourceTreeSHA, publishedAt, cmd.ProjectionID)
	if err != nil {
		return result, err
	}
	if err = s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+quarantinePath, "update-ref", tagRef, distributionSHA); err != nil {
		return result, err
	}
	if err = s.verifyPluginProjection(ctx, quarantinePath, tagRef, distributionSHA, sourceTreeSHA); err != nil {
		return result, err
	}
	contentDigest, err := s.projectionDigest(ctx, quarantinePath)
	if err != nil {
		return result, err
	}
	if err = activateProjection(quarantinePath, finalPath); err != nil {
		return result, err
	}
	return gitservice.PluginProjectionResult{
		Projection: projection, TagName: cmd.TagName, SourceTagType: sourceTagType,
		SourceTagObjectID: sourceTagObjectID, SourceCommitSHA: sourceCommitSHA,
		SourceTreeSHA: sourceTreeSHA, DistributionSHA: distributionSHA, ContentDigest: contentDigest,
	}, nil
}

func (s *Service) resolveSourceTag(ctx context.Context, sourcePath, tagRef string) (gitservice.SourceTagType, string, string, string, error) {
	objectType, err := s.gitOutput(ctx, nil, "--git-dir="+sourcePath, "cat-file", "-t", tagRef)
	if err != nil {
		return "", "", "", "", errors.New("resolve source tag")
	}
	objectType = strings.TrimSpace(objectType)
	var tagType gitservice.SourceTagType
	var tagObjectID string
	switch objectType {
	case "tag":
		tagType = gitservice.SourceTagAnnotated
		tagObjectID, err = s.gitOutput(ctx, nil, "--git-dir="+sourcePath, "rev-parse", "--verify", tagRef)
	case "commit":
		tagType = gitservice.SourceTagLightweight
	default:
		return "", "", "", "", fmt.Errorf("source tag points to unsupported object type %q", objectType)
	}
	if err != nil {
		return "", "", "", "", errors.New("resolve source tag object")
	}
	tagObjectID = strings.TrimSpace(tagObjectID)
	commitSHA, err := s.gitOutput(ctx, nil, "--git-dir="+sourcePath, "rev-parse", "--verify", tagRef+"^{commit}")
	if err != nil {
		return "", "", "", "", errors.New("resolve source tag commit")
	}
	commitSHA = strings.TrimSpace(commitSHA)
	treeSHA, err := s.gitOutput(ctx, nil, "--git-dir="+sourcePath, "rev-parse", "--verify", tagRef+"^{tree}")
	if err != nil {
		return "", "", "", "", errors.New("resolve source tag tree")
	}
	treeSHA = strings.TrimSpace(treeSHA)
	for name, value := range map[string]string{"tag object": tagObjectID, "commit": commitSHA, "tree": treeSHA} {
		if value != "" && !objectIDPattern.MatchString(value) {
			return "", "", "", "", fmt.Errorf("invalid source %s ID", name)
		}
	}
	return tagType, tagObjectID, commitSHA, treeSHA, nil
}

func (s *Service) copyTreeClosure(ctx context.Context, sourcePath, destinationPath, treeSHA string) error {
	var pack bytes.Buffer
	input := strings.NewReader(treeSHA + "\n")
	if err := s.runGit(ctx, input, &pack, io.Discard, "--git-dir="+sourcePath, "pack-objects", "--stdout", "--revs"); err != nil {
		return fmt.Errorf("pack plugin source tree: %w", err)
	}
	if err := s.runGit(ctx, &pack, io.Discard, io.Discard, "--git-dir="+destinationPath, "index-pack", "--stdin"); err != nil {
		return fmt.Errorf("index plugin source tree: %w", err)
	}
	return nil
}

func (s *Service) createSnapshotCommit(ctx context.Context, repositoryPath, treeSHA string, publishedAt time.Time, projectionID uuid.UUID) (string, error) {
	identityDate := publishedAt.Format(time.RFC3339)
	env := append(gitEnv(),
		"GIT_AUTHOR_NAME=MarketplaceServer", "GIT_AUTHOR_EMAIL=distribution@localhost",
		"GIT_AUTHOR_DATE="+identityDate, "GIT_COMMITTER_NAME=MarketplaceServer",
		"GIT_COMMITTER_EMAIL=distribution@localhost", "GIT_COMMITTER_DATE="+identityDate,
	)
	message := "MarketplaceServer distribution " + projectionID.String() + "\n"
	output, err := s.gitOutputWithEnv(ctx, env, strings.NewReader(message), "--git-dir="+repositoryPath, "commit-tree", treeSHA)
	if err != nil {
		return "", fmt.Errorf("create distribution commit: %w", err)
	}
	commitSHA := strings.TrimSpace(output)
	if !objectIDPattern.MatchString(commitSHA) {
		return "", errors.New("invalid distribution commit SHA")
	}
	return commitSHA, nil
}

func (s *Service) verifyPluginProjection(ctx context.Context, path, tagRef, commitSHA, treeSHA string) error {
	refs, err := s.gitOutput(ctx, nil, "--git-dir="+path, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil || strings.TrimSpace(refs) != tagRef+" "+commitSHA {
		return errors.New("plugin projection contains unexpected refs")
	}
	parents, err := s.gitOutput(ctx, nil, "--git-dir="+path, "rev-list", "--parents", "-n", "1", commitSHA)
	if err != nil || strings.TrimSpace(parents) != commitSHA {
		return errors.New("plugin projection commit has parents")
	}
	actualTree, err := s.gitOutput(ctx, nil, "--git-dir="+path, "rev-parse", commitSHA+"^{tree}")
	if err != nil || strings.TrimSpace(actualTree) != treeSHA {
		return errors.New("plugin projection tree mismatch")
	}
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+path, "fsck", "--full", "--no-dangling"); err != nil {
		return errors.New("plugin projection failed integrity check")
	}
	if err := verifyNoAlternatesOrSymlinks(path); err != nil {
		return err
	}
	return nil
}

func (s *Service) gitOutput(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	return s.gitOutputWithEnv(ctx, gitEnv(), stdin, args...)
}

func (s *Service) gitOutputWithEnv(ctx context.Context, env []string, stdin io.Reader, args ...string) (string, error) {
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, s.gitBinary, args...)
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func (s *Service) projectionDigest(ctx context.Context, path string) (string, error) {
	refs, err := s.gitOutput(ctx, nil, "--git-dir="+path, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(refs))
	return hex.EncodeToString(sum[:]), nil
}

func verifyNoAlternatesOrSymlinks(root string) error {
	if _, err := os.Lstat(filepath.Join(root, "objects", "info", "alternates")); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("projection uses alternates")
		}
		return err
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("projection contains symlink")
		}
		return nil
	})
}

func activateProjection(quarantinePath, finalPath string) error {
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(finalPath); err == nil {
		return errors.New("projection already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(quarantinePath, finalPath)
}
