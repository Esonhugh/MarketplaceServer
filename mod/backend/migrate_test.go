package backend

import (
	"reflect"
	"testing"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
)

func TestAggregateMigrationDependenciesAreIdentityFirst(t *testing.T) {
	identityNamespace := reflect.TypeOf(&identitydomain.Namespace{})
	distributionModels := distributiondomain.MigrationModels()
	for _, model := range distributionModels {
		if reflect.TypeOf(model) == identityNamespace {
			t.Fatal("distribution migration models duplicate canonical identity Namespace")
		}
	}
	identityModels := identitydomain.MigrationModels()
	if got := reflect.TypeOf(identityModels[0]); got != reflect.TypeOf(&identitydomain.User{}) {
		t.Fatalf("first identity model = %v", got)
	}
	if got := reflect.TypeOf(distributionModels[0]); got != reflect.TypeOf(&distributiondomain.Repository{}) {
		t.Fatalf("first dependent distribution model = %v", got)
	}
}
