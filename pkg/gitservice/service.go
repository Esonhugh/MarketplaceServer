package gitservice

import (
	"context"
	"errors"
	"io"
)

var (
	ErrRepositoryNotFound    = errors.New("repository not found")
	ErrRepositoryUnavailable = errors.New("repository unavailable")
)

type Repository struct {
	ID         string
	Visibility string
	Status     string
}

const (
	VisibilityPublic = "public"
	StatusReady      = "ready"
)

type RepositoryResolver interface {
	Resolve(ctx context.Context, namespace, repository string) (Repository, error)
}

type RepositoryService interface {
	InitBareRepository(ctx context.Context, repositoryID string) error
	AdvertiseRefs(ctx context.Context, service, repositoryID string, stdout, stderr io.Writer) error
	UploadPack(ctx context.Context, repositoryID string, stdin io.Reader, stdout, stderr io.Writer) error
	ReceivePack(ctx context.Context, repositoryID string, stdin io.Reader, stdout, stderr io.Writer) error
}
