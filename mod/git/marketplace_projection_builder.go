package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func (s *Service) BuildMarketplaceProjection(ctx context.Context, cmd gitservice.BuildMarketplaceProjectionCommand) (result gitservice.MarketplaceProjectionResult, err error) {
	if cmd.ProjectionID == uuid.Nil {
		return result, errors.New("marketplace projection ID is required")
	}
	if len(cmd.ContentJSON) == 0 {
		return result, errors.New("marketplace JSON is required")
	}
	if cmd.PublishedAt.IsZero() {
		return result, errors.New("marketplace publication time is required")
	}
	projection := gitservice.ImmutableProjection{Kind: gitservice.ProjectionKindMarketplace, StorageKey: cmd.ProjectionID.String()}
	finalPath, err := s.projectionPath(projection)
	if err != nil {
		return result, err
	}
	if _, err := os.Stat(finalPath); err == nil {
		return result, errors.New("marketplace projection already exists")
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
	blobSHA, err := s.gitOutput(ctx, strings.NewReader(string(cmd.ContentJSON)), "--git-dir="+quarantinePath, "hash-object", "-w", "--stdin")
	if err != nil {
		return result, err
	}
	blobSHA = strings.TrimSpace(blobSHA)
	if !objectIDPattern.MatchString(blobSHA) {
		return result, errors.New("invalid marketplace JSON blob SHA")
	}
	fileTreeInput := "100644 blob " + blobSHA + "\tmarketplace.json\n"
	fileTreeSHA, err := s.gitOutput(ctx, strings.NewReader(fileTreeInput), "--git-dir="+quarantinePath, "mktree")
	if err != nil {
		return result, err
	}
	fileTreeSHA = strings.TrimSpace(fileTreeSHA)
	rootTreeInput := "040000 tree " + fileTreeSHA + "\t.claude-plugin\n"
	rootTreeSHA, err := s.gitOutput(ctx, strings.NewReader(rootTreeInput), "--git-dir="+quarantinePath, "mktree")
	if err != nil {
		return result, err
	}
	rootTreeSHA = strings.TrimSpace(rootTreeSHA)
	commitSHA, err := s.createSnapshotCommit(ctx, quarantinePath, rootTreeSHA, cmd.PublishedAt.UTC(), cmd.ProjectionID)
	if err != nil {
		return result, err
	}
	if err = s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+quarantinePath, "update-ref", "refs/heads/main", commitSHA); err != nil {
		return result, err
	}
	if err = s.verifyMarketplaceProjection(ctx, quarantinePath, commitSHA, cmd.ContentJSON); err != nil {
		return result, err
	}
	contentSum := sha256.Sum256(cmd.ContentJSON)
	contentDigest := hex.EncodeToString(contentSum[:])
	if err = activateProjection(quarantinePath, finalPath); err != nil {
		return result, err
	}
	return gitservice.MarketplaceProjectionResult{
		Projection: projection, DistributionSHA: commitSHA, ContentDigest: contentDigest,
	}, nil
}

func (s *Service) verifyMarketplaceProjection(ctx context.Context, path, commitSHA string, content []byte) error {
	refs, err := s.gitOutput(ctx, nil, "--git-dir="+path, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil || strings.TrimSpace(refs) != "refs/heads/main "+commitSHA {
		return errors.New("marketplace projection contains unexpected refs")
	}
	parents, err := s.gitOutput(ctx, nil, "--git-dir="+path, "rev-list", "--parents", "-n", "1", commitSHA)
	if err != nil || strings.TrimSpace(parents) != commitSHA {
		return errors.New("marketplace projection commit has parents")
	}
	paths, err := s.gitOutput(ctx, nil, "--git-dir="+path, "ls-tree", "-r", "--name-only", commitSHA)
	if err != nil || strings.TrimSpace(paths) != ".claude-plugin/marketplace.json" {
		return errors.New("marketplace projection contains unexpected paths")
	}
	actualContent, err := s.gitOutput(ctx, nil, "--git-dir="+path, "show", commitSHA+":.claude-plugin/marketplace.json")
	if err != nil || actualContent != string(content) {
		return errors.New("marketplace projection content mismatch")
	}
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+path, "fsck", "--full", "--no-dangling"); err != nil {
		return errors.New("marketplace projection failed integrity check")
	}
	return verifyNoAlternatesOrSymlinks(path)
}

func (s *Service) RemoveProjection(_ context.Context, projection gitservice.ImmutableProjection) error {
	path, err := s.projectionPath(projection)
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func (s *Service) VerifyProjection(ctx context.Context, projection gitservice.ImmutableProjection, contentDigest string) error {
	path, err := s.existingProjectionPath(projection)
	if err != nil {
		return err
	}
	if err := verifyNoAlternatesOrSymlinks(path); err != nil {
		return err
	}
	if err := s.runGit(ctx, nil, io.Discard, io.Discard, "--git-dir="+path, "fsck", "--full", "--no-dangling"); err != nil {
		return errors.New("projection failed integrity check")
	}
	if projection.Kind == gitservice.ProjectionKindMarketplace {
		content, err := s.gitOutput(ctx, nil, "--git-dir="+path, "show", "refs/heads/main:.claude-plugin/marketplace.json")
		if err != nil {
			return errors.New("read marketplace projection content")
		}
		sum := sha256.Sum256([]byte(content))
		if hex.EncodeToString(sum[:]) != contentDigest {
			return errors.New("marketplace projection digest mismatch")
		}
		return nil
	}
	digest, err := s.projectionDigest(ctx, path)
	if err != nil {
		return err
	}
	if digest != contentDigest {
		return errors.New("plugin projection digest mismatch")
	}
	return nil
}
