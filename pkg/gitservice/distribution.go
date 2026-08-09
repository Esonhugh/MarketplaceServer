package gitservice

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"
)

type ProjectionKind string

const (
	ProjectionKindPlugin      ProjectionKind = "plugin"
	ProjectionKindMarketplace ProjectionKind = "marketplace"
)

type ImmutableProjection struct {
	Kind       ProjectionKind
	StorageKey string
}

// DistributionReader is the complete Git capability available to distribution
// request handlers. It intentionally has no repository or ref mutation methods.
type DistributionReader interface {
	AdvertiseDistribution(ctx context.Context, projection ImmutableProjection, stdout, stderr io.Writer) error
	UploadDistribution(ctx context.Context, projection ImmutableProjection, stdin io.Reader, stdout, stderr io.Writer) error
}

type SourceTagType string

const (
	SourceTagLightweight SourceTagType = "lightweight"
	SourceTagAnnotated   SourceTagType = "annotated"
)

type BuildPluginProjectionCommand struct {
	ProjectionID uuid.UUID
	RepositoryID string
	TagName      string
	PublishedAt  time.Time
}

type PluginProjectionResult struct {
	Projection        ImmutableProjection
	TagName           string
	SourceTagType     SourceTagType
	SourceTagObjectID string
	SourceCommitSHA   string
	SourceTreeSHA     string
	DistributionSHA   string
	ContentDigest     string
}

type BuildMarketplaceProjectionCommand struct {
	ProjectionID uuid.UUID
	ContentJSON  []byte
	PublishedAt  time.Time
}

type MarketplaceProjectionResult struct {
	Projection      ImmutableProjection
	DistributionSHA string
	ContentDigest   string
}

// ProjectionBuilder is a write-side publication capability. Request handlers
// must receive DistributionReader instead of this interface.
type ProjectionBuilder interface {
	BuildPluginProjection(ctx context.Context, cmd BuildPluginProjectionCommand) (PluginProjectionResult, error)
	BuildMarketplaceProjection(ctx context.Context, cmd BuildMarketplaceProjectionCommand) (MarketplaceProjectionResult, error)
	RemoveProjection(ctx context.Context, projection ImmutableProjection) error
	VerifyProjection(ctx context.Context, projection ImmutableProjection, contentDigest string) error
}
