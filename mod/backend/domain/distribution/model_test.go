package distribution

import (
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestMigrationModelsFollowDependencyOrder(t *testing.T) {
	want := []reflect.Type{
		reflect.TypeOf(&Namespace{}),
		reflect.TypeOf(&Repository{}),
		reflect.TypeOf(&Plugin{}),
		reflect.TypeOf(&PluginVersion{}),
		reflect.TypeOf(&MarketplaceTemplate{}),
		reflect.TypeOf(&MarketplaceRevision{}),
		reflect.TypeOf(&MarketplaceRevisionItem{}),
		reflect.TypeOf(&MarketplaceDistribution{}),
		reflect.TypeOf(&MarketplaceDistributionProjection{}),
		reflect.TypeOf(&PluginDistribution{}),
	}
	if len(migrationModels) != len(want) {
		t.Fatalf("migration model count = %d, want %d", len(migrationModels), len(want))
	}
	for i := range want {
		if got := reflect.TypeOf(migrationModels[i]); got != want[i] {
			t.Fatalf("migration model %d = %v, want %v", i, got, want[i])
		}
	}
}

func TestImmutableModelsRejectUpdates(t *testing.T) {
	for name, hook := range map[string]func(*gorm.DB) error{
		"revision":   (&MarketplaceRevision{}).BeforeUpdate,
		"item":       (&MarketplaceRevisionItem{}).BeforeUpdate,
		"projection": (&MarketplaceDistributionProjection{}).BeforeUpdate,
	} {
		t.Run(name, func(t *testing.T) {
			if err := hook(&gorm.DB{}); err == nil {
				t.Fatal("immutable model accepted update")
			}
		})
	}
}

func TestMarketplaceDistributionPublicKeyIsGloballyUnique(t *testing.T) {
	field, ok := reflect.TypeOf(MarketplaceDistribution{}).FieldByName("PublicKey")
	if !ok {
		t.Fatal("MarketplaceDistribution.PublicKey is missing")
	}
	tag := field.Tag.Get("gorm")
	if !strings.Contains(tag, "not null") || !strings.Contains(tag, "uniqueIndex:uidx_marketplace_distribution_public_key") {
		t.Fatalf("PublicKey gorm tag = %q", tag)
	}
}

func TestDistributionTableNamesAreStable(t *testing.T) {
	tests := map[string]string{
		(MarketplaceDistribution{}).TableName():           "marketplace_distributions",
		(MarketplaceDistributionProjection{}).TableName(): "marketplace_distribution_projections",
		(PluginDistribution{}).TableName():                "plugin_distributions",
	}
	for got, want := range tests {
		if got != want {
			t.Fatalf("TableName() = %q, want %q", got, want)
		}
	}
}
