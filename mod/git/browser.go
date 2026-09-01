package git

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
)

const browseTimeout = 10 * time.Second

func (s *Service) ListRefs(ctx context.Context, repositoryID string) (gitservice.RepositoryRefs, error) {
	ctx, cancel := context.WithTimeout(ctx, browseTimeout)
	defer cancel()
	path, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return gitservice.RepositoryRefs{}, err
	}
	output, err := s.gitOutput(ctx, nil, "--git-dir="+path, "for-each-ref", "--format=%(refname)%00%(objectname)", "refs/heads", "refs/tags")
	if err != nil {
		return gitservice.RepositoryRefs{}, gitservice.ErrRepositoryUnavailable
	}
	result := gitservice.RepositoryRefs{Branches: []gitservice.RepositoryRef{}, Tags: []gitservice.RepositoryRef{}}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 2)
		if len(parts) != 2 || !validGitObjectID(parts[1]) {
			return gitservice.RepositoryRefs{}, gitservice.ErrRepositoryUnavailable
		}
		commit, resolveErr := s.gitOutput(ctx, nil, "--git-dir="+path, "rev-parse", "--verify", "--end-of-options", parts[0]+"^{commit}")
		if resolveErr != nil {
			continue
		}
		commit = strings.TrimSpace(commit)
		if !validGitObjectID(commit) {
			return gitservice.RepositoryRefs{}, gitservice.ErrRepositoryUnavailable
		}
		ref := gitservice.RepositoryRef{Name: strings.TrimPrefix(strings.TrimPrefix(parts[0], "refs/heads/"), "refs/tags/"), CommitSHA: commit}
		if strings.HasPrefix(parts[0], "refs/heads/") {
			result.Branches = append(result.Branches, ref)
		} else if strings.HasPrefix(parts[0], "refs/tags/") {
			result.Tags = append(result.Tags, ref)
		}
	}
	if head, err := s.gitOutput(ctx, nil, "--git-dir="+path, "symbolic-ref", "--short", "HEAD"); err == nil {
		result.DefaultRef = strings.TrimSpace(head)
	}
	if result.DefaultRef == "" && len(result.Branches) > 0 {
		result.DefaultRef = result.Branches[0].Name
	}
	return result, nil
}

func (s *Service) ReadTree(ctx context.Context, repositoryID, revision, browsePath string) (gitservice.RepositoryTree, error) {
	ctx, cancel := context.WithTimeout(ctx, browseTimeout)
	defer cancel()
	path, commit, cleanPath, err := s.resolveBrowse(ctx, repositoryID, revision, browsePath)
	if err != nil {
		return gitservice.RepositoryTree{}, err
	}
	treeish := commit
	if cleanPath != "" {
		treeish += ":" + cleanPath
	}
	output, err := s.gitOutput(ctx, nil, "--git-dir="+path, "ls-tree", "-z", "-l", treeish)
	if err != nil {
		return gitservice.RepositoryTree{}, gitservice.ErrPathNotFound
	}
	entries := []gitservice.RepositoryTreeEntry{}
	for _, record := range strings.Split(output, "\x00") {
		if record == "" {
			continue
		}
		metaName := strings.SplitN(record, "\t", 2)
		if len(metaName) != 2 {
			return gitservice.RepositoryTree{}, gitservice.ErrRepositoryUnavailable
		}
		fields := strings.Fields(metaName[0])
		if len(fields) != 4 {
			return gitservice.RepositoryTree{}, gitservice.ErrRepositoryUnavailable
		}
		entry := gitservice.RepositoryTreeEntry{Name: metaName[1]}
		switch fields[1] {
		case "tree":
			entry.Type = "tree"
		case "blob":
			entry.Type = "blob"
			if fields[3] != "-" {
				size, parseErr := strconv.ParseInt(fields[3], 10, 64)
				if parseErr != nil {
					return gitservice.RepositoryTree{}, gitservice.ErrRepositoryUnavailable
				}
				entry.Size = &size
			}
		default:
			continue
		}
		entries = append(entries, entry)
	}
	return gitservice.RepositoryTree{Ref: revision, CommitSHA: commit, Path: cleanPath, Entries: entries}, nil
}

func (s *Service) ReadBlob(ctx context.Context, repositoryID, revision, browsePath string) (gitservice.RepositoryBlob, error) {
	ctx, cancel := context.WithTimeout(ctx, browseTimeout)
	defer cancel()
	path, commit, cleanPath, err := s.resolveBrowse(ctx, repositoryID, revision, browsePath)
	if err != nil {
		return gitservice.RepositoryBlob{}, err
	}
	if cleanPath == "" {
		return gitservice.RepositoryBlob{}, gitservice.ErrPathNotFound
	}
	object, err := s.gitOutput(ctx, nil, "--git-dir="+path, "rev-parse", "--verify", commit+":"+cleanPath)
	if err != nil {
		return gitservice.RepositoryBlob{}, gitservice.ErrPathNotFound
	}
	object = strings.TrimSpace(object)
	if !validGitObjectID(object) {
		return gitservice.RepositoryBlob{}, gitservice.ErrRepositoryUnavailable
	}
	typeName, err := s.gitOutput(ctx, nil, "--git-dir="+path, "cat-file", "-t", object)
	if err != nil || strings.TrimSpace(typeName) != "blob" {
		return gitservice.RepositoryBlob{}, gitservice.ErrPathNotText
	}
	sizeText, err := s.gitOutput(ctx, nil, "--git-dir="+path, "cat-file", "-s", object)
	if err != nil {
		return gitservice.RepositoryBlob{}, gitservice.ErrRepositoryUnavailable
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeText), 10, 64)
	if err != nil {
		return gitservice.RepositoryBlob{}, gitservice.ErrRepositoryUnavailable
	}
	if size > gitservice.RepositoryBlobPreviewLimit {
		return gitservice.RepositoryBlob{}, gitservice.ErrBlobTooLarge
	}
	content, err := s.gitOutput(ctx, nil, "--git-dir="+path, "cat-file", "blob", object)
	if err != nil {
		return gitservice.RepositoryBlob{}, gitservice.ErrRepositoryUnavailable
	}
	if strings.IndexByte(content, 0) >= 0 || !utf8.ValidString(content) {
		return gitservice.RepositoryBlob{}, gitservice.ErrPathNotText
	}
	return gitservice.RepositoryBlob{Ref: revision, CommitSHA: commit, Path: cleanPath, Size: size, Content: content}, nil
}

func (s *Service) ListCommits(ctx context.Context, repositoryID, revision, browsePath string, page, size int) (gitservice.RepositoryCommitPage, error) {
	if page < 1 || size < 1 || size > 100 {
		return gitservice.RepositoryCommitPage{}, gitservice.ErrInvalidBrowseInput
	}
	ctx, cancel := context.WithTimeout(ctx, browseTimeout)
	defer cancel()
	path, commit, cleanPath, err := s.resolveBrowse(ctx, repositoryID, revision, browsePath)
	if err != nil {
		return gitservice.RepositoryCommitPage{}, err
	}
	args := []string{"--git-dir=" + path, "log", "--format=%H%x00%s%x00%an%x00%ae%x00%cI", "--skip=" + strconv.Itoa((page-1)*size), "--max-count=" + strconv.Itoa(size), commit}
	if cleanPath != "" {
		args = append(args, "--", cleanPath)
	}
	output, err := s.gitOutput(ctx, nil, args...)
	if err != nil {
		return gitservice.RepositoryCommitPage{}, gitservice.ErrRepositoryUnavailable
	}
	countArgs := []string{"--git-dir=" + path, "rev-list", "--count", commit}
	if cleanPath != "" {
		countArgs = append(countArgs, "--", cleanPath)
	}
	countText, err := s.gitOutput(ctx, nil, countArgs...)
	if err != nil {
		return gitservice.RepositoryCommitPage{}, gitservice.ErrRepositoryUnavailable
	}
	total, err := strconv.ParseInt(strings.TrimSpace(countText), 10, 64)
	if err != nil {
		return gitservice.RepositoryCommitPage{}, gitservice.ErrRepositoryUnavailable
	}
	items := []gitservice.RepositoryCommit{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x00")
		if len(fields) != 5 || !validGitObjectID(fields[0]) {
			return gitservice.RepositoryCommitPage{}, gitservice.ErrRepositoryUnavailable
		}
		committed, parseErr := time.Parse(time.RFC3339, fields[4])
		if parseErr != nil {
			return gitservice.RepositoryCommitPage{}, gitservice.ErrRepositoryUnavailable
		}
		items = append(items, gitservice.RepositoryCommit{SHA: fields[0], Subject: fields[1], AuthorName: fields[2], AuthorEmail: fields[3], CommittedAt: committed.UTC()})
	}
	return gitservice.RepositoryCommitPage{Items: items, Page: page, Size: size, Total: total}, nil
}

func (s *Service) resolveBrowse(ctx context.Context, repositoryID, revision, browsePath string) (string, string, string, error) {
	if revision == "" || strings.HasPrefix(revision, "-") || strings.ContainsAny(revision, "\x00\r\n") {
		return "", "", "", gitservice.ErrInvalidBrowseInput
	}
	cleanPath, err := cleanBrowsePath(browsePath)
	if err != nil {
		return "", "", "", err
	}
	path, err := s.existingRepositoryPath(repositoryID)
	if err != nil {
		return "", "", "", err
	}
	commit, err := s.gitOutput(ctx, nil, "--git-dir="+path, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", "", "", gitservice.ErrRevisionNotFound
	}
	commit = strings.TrimSpace(commit)
	if !validGitObjectID(commit) {
		return "", "", "", gitservice.ErrRepositoryUnavailable
	}
	return path, commit, cleanPath, nil
}

func cleanBrowsePath(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\x00\r\n\\") {
		return "", gitservice.ErrInvalidBrowseInput
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", gitservice.ErrInvalidBrowseInput
		}
	}
	clean := strings.Join(parts, "/")
	if len(clean) > 4096 {
		return "", gitservice.ErrInvalidBrowseInput
	}
	return clean, nil
}

var _ gitservice.RepositoryBrowser = (*Service)(nil)
