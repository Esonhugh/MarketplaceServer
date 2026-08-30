package backend

import (
	"reflect"
	"testing"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
)

func TestAggregateMigrationDependenciesAreIdentityFirst(t *testing.T) {
	identityNamespace := reflect.TypeOf(&identitymodel.Namespace{})
	distributionModels := distributiondomain.MigrationModels()
	for _, model := range distributionModels {
		if reflect.TypeOf(model) == identityNamespace {
			t.Fatal("distribution migration models duplicate canonical identity Namespace")
		}
	}
	identityModels := identitydao.MigrationModels()
	if got := reflect.TypeOf(identityModels[0]); got != reflect.TypeOf(&identitymodel.User{}) {
		t.Fatalf("first identity model = %v", got)
	}
	pluginModels := plugindomain.MigrationModels()
	if got := reflect.TypeOf(pluginModels[0]); got != reflect.TypeOf(&plugindomain.Plugin{}) {
		t.Fatalf("first Plugin lifecycle model = %v", got)
	}
	if got := reflect.TypeOf(distributionModels[0]); got != reflect.TypeOf(&distributiondomain.MarketplaceTemplate{}) {
		t.Fatalf("first dependent distribution model = %v", got)
	}
}
