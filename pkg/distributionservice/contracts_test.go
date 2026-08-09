package distributionservice

import (
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

func TestMarketplaceGrantKeepsInternalAndPublicIdentity(t *testing.T) {
	id := uuid.New()
	publicKey, err := ParseMarketplacePublicKey("web-blackbox-a3f91c20")
	if err != nil {
		t.Fatal(err)
	}
	grant := MarketplaceGrant{DistributionID: id, PublicKey: publicKey}
	if grant.DistributionID != id || grant.PublicKey != publicKey {
		t.Fatalf("MarketplaceGrant identity = %s/%q", grant.DistributionID, grant.PublicKey.String())
	}
}

func TestDistributionGrantsUseOpaqueProjectionKeys(t *testing.T) {
	id := uuid.New()
	grant := PluginGrant{
		DistributionID: id,
		Projection: gitservice.ImmutableProjection{
			Kind:       gitservice.ProjectionKindPlugin,
			StorageKey: uuid.NewString(),
		},
	}
	if grant.DistributionID != id {
		t.Fatal("distribution identity changed")
	}
	if grant.Projection.Kind != gitservice.ProjectionKindPlugin {
		t.Fatalf("projection kind = %q, want plugin", grant.Projection.Kind)
	}
}
