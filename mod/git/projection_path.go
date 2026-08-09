package git

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func (s *Service) projectionPath(projection gitservice.ImmutableProjection) (string, error) {
	id, err := uuid.Parse(projection.StorageKey)
	if err != nil || id.String() != projection.StorageKey {
		return "", errors.New("invalid projection storage key")
	}
	var directory string
	switch projection.Kind {
	case gitservice.ProjectionKindPlugin:
		directory = "plugins"
	case gitservice.ProjectionKindMarketplace:
		directory = "marketplaces"
	default:
		return "", errors.New("invalid projection kind")
	}
	path := filepath.Join(s.storageRoot, "projections", directory, projection.StorageKey[:2], projection.StorageKey+".git")
	if err := s.ensureNoSymlinkEscape(path); err != nil {
		return "", err
	}
	if err := ensurePathWithinRoot(s.storageRoot, path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) existingProjectionPath(projection gitservice.ImmutableProjection) (string, error) {
	path, err := s.projectionPath(projection)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && !info.IsDir() {
		return "", ErrRepositoryNotFound
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) quarantinePath(buildID uuid.UUID) (string, error) {
	path := filepath.Join(s.storageRoot, "quarantine", buildID.String())
	if err := s.ensureNoSymlinkEscape(path); err != nil {
		return "", err
	}
	if err := ensurePathWithinRoot(s.storageRoot, path); err != nil {
		return "", err
	}
	return path, nil
}
