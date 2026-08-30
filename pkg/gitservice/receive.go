package gitservice

import "context"

// ReceivePlugin is the opaque aggregate identity needed by protected Git receive.
// Name is used only for the exact, case-sensitive Plugin manifest check.
type ReceivePlugin struct {
	ID   string
	Name string
}

// ReceiveRefCommand preserves the raw ref object IDs supplied by receive-pack.
// These IDs, rather than peeled commit IDs, are the authority for ref CAS.
type ReceiveRefCommand struct {
	OldObjectID string
	NewObjectID string
	RefName     string
}

type ReceiveTagOperation string

const (
	ReceiveTagCreate ReceiveTagOperation = "create"
	ReceiveTagMove   ReceiveTagOperation = "move"
	ReceiveTagDelete ReceiveTagOperation = "delete"
)

// ReceiveTagCommand separates raw tag-ref object IDs from peeled commit IDs.
// A zero raw object ID is represented by the repository-format all-zero value;
// an absent peeled commit is represented by the empty string.
type ReceiveTagCommand struct {
	Tag                 string
	RefName             string
	Operation           ReceiveTagOperation
	OldObjectID         string
	NewObjectID         string
	OldCommitObjectID   string
	NewCommitObjectID   string
	NewManifestDigest   string
	NewManifestSnapshot []byte
}

// ReceiveBatch is the complete command set for one receive-pack session. The
// coordinator may persist only effectful available-Version transitions, but it
// must classify all CanonicalTags while holding the Plugin effect lock.
type ReceiveBatch struct {
	SessionID     string
	Plugin        ReceivePlugin
	Commands      []ReceiveRefCommand
	CanonicalTags []ReceiveTagCommand
}

// PreparedReceiveBatch identifies durable state already committed by the
// coordinator. An empty ID with no transitions means the receive is candidate-
// only and intentionally has no inert durable business state.
type PreparedReceiveBatch struct {
	ID          string
	Transitions []PreparedReceiveTransition
}

// PreparedReceiveTransition binds one durable available-Version mutation to
// the raw ref CAS facts that Git will apply.
type PreparedReceiveTransition struct {
	Tag                 string
	RefName             string
	ExpectedOldObjectID string
	ProposedNewObjectID string
}

type ReceiveDisposition string

const (
	ReceiveCompleted      ReceiveDisposition = "completed"
	ReceiveAborted        ReceiveDisposition = "aborted"
	ReceiveManualRequired ReceiveDisposition = "manual_required"
)

type ObservedReceiveRef struct {
	RefName  string
	ObjectID string
	Exists   bool
}

// PluginRefReader re-reads a validated ref using an opaque repository identity.
// It has no mutation capability and must not expose filesystem paths.
type PluginRefReader interface {
	ReadPluginRef(ctx context.Context, repositoryID, refName string) (ObservedReceiveRef, error)
}

type ReceiveResolution struct {
	Disposition ReceiveDisposition
	Observed    []ObservedReceiveRef
}

// ReceiveCoordinator is implemented by the backend Plugin lifecycle domain.
// Open obtains the Plugin-scoped effect lock and returns an idempotent session
// keyed by the server-generated receive session ID.
type ReceiveCoordinator interface {
	Open(ctx context.Context, pluginID, sessionID string) (ReceiveCoordination, error)
}

// ReceiveCoordination owns a held Plugin effect lock. Prepare atomically writes
// any durable batch/intents/artifact and pointer facts before it returns.
// Resolve idempotently finalizes, aborts, or marks that durable batch for manual
// recovery after Git has re-read the actual refs.
type ReceiveCoordination interface {
	Prepare(ctx context.Context, batch ReceiveBatch) (PreparedReceiveBatch, error)
	Resolve(ctx context.Context, prepared PreparedReceiveBatch, resolution ReceiveResolution) error
	Close() error
}
