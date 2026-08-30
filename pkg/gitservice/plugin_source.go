package gitservice

import "context"

// PluginSourceInspection is the immutable source fact obtained from a current
// canonical tag. RawTagObjectID is the ref value used for compare-and-swap;
// CommitObjectID is the fully peeled commit used for validation and publication.
// ManifestSnapshot contains the exact plugin.json bytes that were inspected.
type PluginSourceInspection struct {
	RawTagObjectID   string
	CommitObjectID   string
	ManifestDigest   string
	ManifestSnapshot []byte
}

// PluginSourceInspector is the narrow Git-storage capability used by Plugin
// lifecycle publication. All inputs are opaque or validated domain values; it
// neither accepts nor exposes filesystem paths or validator process output.
type PluginSourceInspector interface {
	InspectPluginSource(ctx context.Context, repositoryID, canonicalTag, expectedPluginSlug string) (PluginSourceInspection, error)
}
