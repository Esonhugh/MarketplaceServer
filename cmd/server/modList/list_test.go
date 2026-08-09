package modList

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/frontend"
	gitmod "github.com/Esonhugh/MarketplaceServer/mod/git"
	"github.com/Esonhugh/MarketplaceServer/mod/jin"
	"github.com/Esonhugh/MarketplaceServer/mod/sql"
	"github.com/spf13/viper"
)

func TestModListRegistersOnlyMarketplaceRuntimeModulesInOrder(t *testing.T) {
	want := []string{"jin", "sql", "git", "backend", "frontend"}

	got := make([]string, 0, len(ModList))
	seen := make(map[string]struct{}, len(ModList))
	for _, mod := range ModList {
		name := mod.Name()
		if _, exists := seen[name]; exists {
			t.Fatalf("ModList contains duplicate module %q", name)
		}
		seen[name] = struct{}{}
		got = append(got, name)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ModList modules = %v, want exactly %v", got, want)
	}
}

func TestConfigExampleDecodesRuntimeModuleConfigs(t *testing.T) {
	config := readExampleConfig(t)

	var jinConfig jin.Config
	var sqlConfig sql.Config
	var gitConfig gitmod.Config
	var frontendConfig frontend.Config
	for _, target := range []struct {
		name string
		into any
	}{
		{"jin", &jinConfig},
		{"sql", &sqlConfig},
		{"git", &gitConfig},
		{"frontend", &frontendConfig},
	} {
		decodeRuntimeModuleConfig(t, config, target.name, target.into)
	}

	if jinConfig.ReadHeaderTimeout != 5*time.Second ||
		jinConfig.ReadTimeout != 30*time.Second ||
		jinConfig.WriteTimeout != 30*time.Second ||
		jinConfig.IdleTimeout != time.Minute ||
		jinConfig.ShutdownTimeout != 10*time.Second {
		t.Fatalf("jin durations = %+v, want explicit example values", jinConfig)
	}
	if sqlConfig.ConnMaxLifetime != 30*time.Minute {
		t.Fatalf("sql.connMaxLifetime = %s, want 30m", sqlConfig.ConnMaxLifetime)
	}
	if gitConfig.StorageRoot != "./var/marketplace/git" {
		t.Fatalf("git.storageRoot = %q, want example storage root", gitConfig.StorageRoot)
	}
	if frontendConfig.Disabled || frontendConfig.BasePath != "/" {
		t.Fatalf("frontend config = %+v, want enabled at /", frontendConfig)
	}
}

func TestConfigExampleContainsOnlyRuntimeModuleSections(t *testing.T) {
	config := readExampleConfig(t)
	allowed := map[string]struct{}{
		"mode": {}, "port": {}, "log": {}, "sentrydsn": {},
		"jin": {}, "sql": {}, "git": {}, "backend": {}, "frontend": {},
	}
	legacyModules := map[string]struct{}{
		"b2": {}, "b2x": {}, "mysql": {}, "pyroscope": {}, "redis": {}, "uptrace": {},
		"jinx": {}, "mydb": {}, "pgsql": {}, "rds": {}, "grpcgateway": {}, "jinpprof": {}, "example": {},
	}

	for _, key := range config.AllKeys() {
		topLevel := strings.ToLower(strings.Split(key, ".")[0])
		if _, isLegacy := legacyModules[topLevel]; isLegacy {
			t.Fatalf("config.example.yaml contains legacy runtime module section %q", topLevel)
		}
		if _, ok := allowed[topLevel]; !ok {
			t.Fatalf("config.example.yaml contains unsupported top-level configuration key %q", topLevel)
		}
	}
}

func TestConfigExampleDoesNotContainCredentialsOrAdvancedGitSettings(t *testing.T) {
	config := readExampleConfig(t)

	if dsn := config.GetString("sql.dsn"); dsn != "" {
		t.Fatalf("sql.dsn = %q, want empty example value", dsn)
	}
	for _, key := range config.AllKeys() {
		if isSecretConfigurationKey(key) {
			if value := config.GetString(key); value != "" {
				t.Fatalf("secret config %q = %q, want empty example value", key, value)
			}
		}
	}
	for _, key := range []string{"git.gitBinary", "git.anonymousRead", "git.maxRequestBytes", "git.advertiseTimeout", "git.serviceTimeout"} {
		if config.IsSet(key) {
			t.Fatalf("config.example.yaml exposes unnecessary Git setting %q", key)
		}
	}
}

func isSecretConfigurationKey(key string) bool {
	segments := strings.Split(strings.ToLower(key), ".")
	field := segments[len(segments)-1]
	return strings.Contains(field, "secret") ||
		strings.Contains(field, "password") ||
		strings.Contains(field, "token") ||
		strings.Contains(field, "apikey") ||
		strings.Contains(field, "accesskey")
}

func readExampleConfig(t *testing.T) *viper.Viper {
	t.Helper()

	config := viper.New()
	config.SetConfigFile("../../../config.example.yaml")
	if err := config.ReadInConfig(); err != nil {
		t.Fatalf("read config.example.yaml: %v", err)
	}
	return config
}

func decodeRuntimeModuleConfig(t *testing.T, config *viper.Viper, name string, target any) {
	t.Helper()

	// Match kernel.Engine: create a one-field dynamic struct whose mapstructure
	// tag is the runtime module name, then decode the full document into it.
	targetType := reflect.TypeOf(target)
	if targetType.Kind() != reflect.Pointer {
		t.Fatalf("module %s config target must be a pointer, got %T", name, target)
	}
	wrapperType := reflect.StructOf([]reflect.StructField{{
		Name: "Config",
		Type: targetType,
		Tag:  reflect.StructTag(`mapstructure:"` + name + `"`),
	}})
	wrapper := reflect.New(wrapperType)
	wrapper.Elem().Field(0).Set(reflect.ValueOf(target))
	if err := config.Unmarshal(wrapper.Interface()); err != nil {
		t.Fatalf("decode %s config: %v", name, err)
	}
}
