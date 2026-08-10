package distribution

import (
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestMigrationModelsFollowDependencyOrder(t *testing.T) {
	want := []reflect.Type{
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
	if len(MigrationModels()) != len(want) {
		t.Fatalf("migration model count = %d, want %d", len(MigrationModels()), len(want))
	}
	for i := range want {
		if got := reflect.TypeOf(MigrationModels()[i]); got != want[i] {
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

func TestRepositoryStatusContract(t *testing.T) {
	want := []string{
		RepositoryStatusProvisioning,
		RepositoryStatusReady,
		RepositoryStatusReadOnly,
		RepositoryStatusError,
		RepositoryStatusDeleting,
		RepositoryStatusDeleted,
	}
	if got := strings.Join(want, ","); got != "provisioning,ready,readOnly,error,deleting,deleted" {
		t.Fatalf("repository statuses = %q", got)
	}
	for _, status := range want {
		if !validRepositoryStatus(status) {
			t.Errorf("validRepositoryStatus(%q) = false", status)
		}
	}
	for _, status := range []string{"", StatusActive, "READY", "unknown"} {
		if validRepositoryStatus(status) {
			t.Errorf("validRepositoryStatus(%q) = true", status)
		}
	}
	for _, status := range []string{RepositoryStatusReady, RepositoryStatusReadOnly} {
		if !repositoryAllowsRead(status) {
			t.Errorf("repositoryAllowsRead(%q) = false", status)
		}
	}
	for _, status := range []string{RepositoryStatusProvisioning, RepositoryStatusError, RepositoryStatusDeleting, RepositoryStatusDeleted, StatusActive} {
		if repositoryAllowsRead(status) {
			t.Errorf("repositoryAllowsRead(%q) = true", status)
		}
	}

	field, ok := reflect.TypeOf(Repository{}).FieldByName("Status")
	if !ok {
		t.Fatal("Repository.Status is missing")
	}
	tag := field.Tag.Get("gorm")
	for _, value := range want {
		if !strings.Contains(tag, "'"+value+"'") {
			t.Errorf("Repository.Status gorm tag %q does not constrain %q", tag, value)
		}
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
