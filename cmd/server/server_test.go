package server

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestRunReturnsConfigurationError(t *testing.T) {
	previousPath := configPath
	configPath = filepath.Join(t.TempDir(), "missing.yaml")
	t.Cleanup(func() {
		configPath = previousPath
		viper.Reset()
	})

	err := run()
	if err == nil {
		t.Fatal("run() succeeded with missing configuration")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Fatalf("run() error = %q, want safe configuration context", err)
	}
}
