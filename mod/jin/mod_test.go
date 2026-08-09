package jin

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/juanjiTech/inject/v2"
	jinengine "github.com/juanjiTech/jin"
)

func newTestHub() *kernel.Hub {
	return &kernel.Hub{Injector: inject.New()}
}

func TestName(t *testing.T) {
	m := &Mod{}
	if got := m.Name(); got != "jin" {
		t.Fatalf("Name() = %q, want %q", got, "jin")
	}
}

func TestConfigDefaultsAndTags(t *testing.T) {
	m := &Mod{}
	cfg, ok := m.Config().(*Config)
	if !ok {
		t.Fatalf("Config() type = %T, want *Config", m.Config())
	}

	wantDurations := map[string]time.Duration{
		"ReadHeaderTimeout": 5 * time.Second,
		"ReadTimeout":       30 * time.Second,
		"WriteTimeout":      30 * time.Second,
		"IdleTimeout":       60 * time.Second,
		"ShutdownTimeout":   10 * time.Second,
	}
	values := reflect.ValueOf(*cfg)
	cfgType := reflect.TypeOf(*cfg)
	for fieldName, want := range wantDurations {
		field, ok := cfgType.FieldByName(fieldName)
		if !ok {
			t.Fatalf("Config missing field %s", fieldName)
		}
		if field.Tag.Get("yaml") == "" {
			t.Fatalf("Config.%s missing yaml tag", fieldName)
		}
		if field.Tag.Get("mapstructure") == "" {
			t.Fatalf("Config.%s missing mapstructure tag", fieldName)
		}
		if got := values.FieldByName(fieldName).Interface().(time.Duration); got != want {
			t.Fatalf("Config.%s default = %s, want %s", fieldName, got, want)
		}
	}
}

func TestPreInitCreatesRecoveryEngineAndMapsIt(t *testing.T) {
	hub := newTestHub()
	m := &Mod{}
	if err := m.PreInit(hub); err != nil {
		t.Fatalf("PreInit() error = %v", err)
	}

	var engine *jinengine.Engine
	if err := hub.Load(&engine); err != nil {
		t.Fatalf("hub.Load(*jin.Engine) error = %v", err)
	}
	if engine == nil {
		t.Fatal("loaded *jin.Engine is nil")
	}
	if engine != m.engine {
		t.Fatal("loaded *jin.Engine is not the module engine")
	}

	engine.GET("/panic", func(*jinengine.Context) {
		panic("boom")
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("panic route status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestStartFailsWhenCMuxMissing(t *testing.T) {
	hub := newTestHub()
	m := &Mod{}
	if err := m.PreInit(hub); err != nil {
		t.Fatalf("PreInit() error = %v", err)
	}

	err := m.Start(hub)
	if err == nil {
		t.Fatal("Start() error = nil, want missing cmux error")
	}
	if !strings.Contains(err.Error(), "cmux.CMux") {
		t.Fatalf("Start() error = %q, want mention cmux.CMux", err.Error())
	}
}
