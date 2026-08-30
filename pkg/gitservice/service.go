package gitservice

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrRepositoryNotFound       = errors.New("repository not found")
	ErrRepositoryUnavailable    = errors.New("repository unavailable")
	ErrRepositoryNotProvisioned = errors.New("repository was not provisioned by this operation")
)

// RepositoryIdentity carries the opaque identifiers that bind one hidden Git
// repository to its Plugin aggregate. Neither identifier is a filesystem path.
type RepositoryIdentity struct {
	ID         string
	StorageKey string
}

// ProvisionedRepository is returned by ProvisionRepository. It intentionally
// contains no path. A non-empty receipt authorizes only its creating Git
// service instance to compensate a failed SQL transaction.
type ProvisionedRepository struct {
	RepositoryIdentity

	receipt string
}

func (r ProvisionedRepository) Created() bool {
	return r.receipt != ""
}

// NewProvisionedRepository is for Git storage implementations. Consumers
// receive its opaque result from ProvisionRepository and must not construct it.
func NewProvisionedRepository(repository RepositoryIdentity, receipt string) ProvisionedRepository {
	return ProvisionedRepository{RepositoryIdentity: repository, receipt: receipt}
}

// ConsumeReceipt supplies the opaque receipt to the issuing provisioner and
// clears it from the returned value. It lets the backend pass a compensation
// result back without learning or logging the receipt itself.
func (r ProvisionedRepository) ConsumeReceipt(consume func(string) bool) bool {
	return r.Created() && consume(r.receipt)
}

// RepositoryOrphanCandidate gives backend recovery an opaque aged storage
// identity. Backend decides database absence and any later quarantine/cleanup;
// no filesystem path is exposed across this boundary.
type RepositoryOrphanCandidate struct {
	StorageKey string
	CreatedAt  time.Time
}

// RepositoryOrphanCleaner removes a candidate already proven absent from every
// Plugin aggregate. StorageKey is opaque and never a filesystem path.
type RepositoryOrphanCleaner interface {
	RemoveOrphanRepository(ctx context.Context, storageKey string) error
}

// RepositoryProvisioner is the narrow filesystem capability needed by the
// backend's shared-ID Plugin/Repository aggregate orchestration.
type RepositoryProvisioner interface {
	ProvisionRepository(ctx context.Context, repository RepositoryIdentity) (ProvisionedRepository, error)
	RemoveProvisionedRepository(ctx context.Context, provisioned ProvisionedRepository) error
	ListRepositoryOrphanCandidates(ctx context.Context, minimumAge time.Duration) ([]RepositoryOrphanCandidate, error)
}

type Repository struct {
	ID          string
	NamespaceID string
	OwnerUserID string
	Slug        string
	Visibility  string
	Status      string
}

const (
	VisibilityPublic = "public"
	StatusReady      = "ready"
	StatusReadOnly   = "readOnly"
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
