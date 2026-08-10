package kernel

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type lifecycleModule struct {
	UnimplementedModule
	name   string
	record func(string)
}

func (m *lifecycleModule) Name() string {
	return m.name
}

func (m *lifecycleModule) Config() any {
	m.record("config:" + m.name)
	return nil
}

func (m *lifecycleModule) PreInit(*Hub) error {
	m.record("pre-init:" + m.name)
	return nil
}

func (m *lifecycleModule) Init(*Hub) error {
	m.record("init:" + m.name)
	return nil
}

func (m *lifecycleModule) PostInit(*Hub) error {
	m.record("post-init:" + m.name)
	return nil
}

func (m *lifecycleModule) Load(*Hub) error {
	m.record("load:" + m.name)
	return nil
}

func (m *lifecycleModule) Stop(wg *sync.WaitGroup, _ context.Context) error {
	defer wg.Done()
	m.record("stop:" + m.name)
	return nil
}

func TestStartModuleRunsAssemblyLifecycleInRegistrationOrder(t *testing.T) {
	var got []string
	record := func(event string) {
		got = append(got, event)
	}

	engine := New(Config{})
	names := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"}
	mods := make([]Module, 0, len(names))
	for _, name := range names {
		mods = append(mods, &lifecycleModule{name: name, record: record})
	}
	engine.RegMod(mods...)

	if err := engine.StartModule(); err != nil {
		t.Fatalf("StartModule() error = %v", err)
	}

	var want []string
	for _, phase := range []string{"config", "pre-init", "init", "post-init", "load"} {
		for _, name := range names {
			want = append(want, phase+":"+name)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("assembly lifecycle order mismatch\ngot:  %v\nwant: %v", got, want)
	}
}

func TestDuplicateRegistrationPanicsWithoutChangingLifecycleOrder(t *testing.T) {
	var got []string
	record := func(event string) {
		got = append(got, event)
	}

	engine := New(Config{})
	engine.RegMod(
		&lifecycleModule{name: "alpha", record: record},
		&lifecycleModule{name: "bravo", record: record},
	)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("RegMod() did not panic for a duplicate module name")
			}
		}()
		engine.RegMod(&lifecycleModule{name: "alpha", record: record})
	}()

	if err := engine.StartModule(); err != nil {
		t.Fatalf("StartModule() error = %v", err)
	}

	want := []string{
		"config:alpha", "config:bravo",
		"pre-init:alpha", "pre-init:bravo",
		"init:alpha", "init:bravo",
		"post-init:alpha", "post-init:bravo",
		"load:alpha", "load:bravo",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle order after duplicate registration = %v, want %v", got, want)
	}
}

type blockingStartModule struct {
	UnimplementedModule
	name    string
	started chan<- string
	release <-chan struct{}
}

func (m *blockingStartModule) Name() string {
	return m.name
}

func (m *blockingStartModule) Start(*Hub) error {
	m.started <- m.name
	<-m.release
	return nil
}

func TestStartModuleInvokesEveryStartConcurrently(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	defer close(release)

	engine := New(Config{})
	engine.RegMod(
		&blockingStartModule{name: "alpha", started: started, release: release},
		&blockingStartModule{name: "bravo", started: started, release: release},
		&blockingStartModule{name: "charlie", started: started, release: release},
	)

	if err := engine.StartModule(); err != nil {
		t.Fatalf("StartModule() error = %v", err)
	}

	seen := make(map[string]bool)
	for range 3 {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(time.Second):
			t.Fatalf("StartModule() did not invoke every Start concurrently; invoked %v", seen)
		}
	}
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if !seen[name] {
			t.Errorf("Start() was not invoked for %s", name)
		}
	}
}

func TestStopDrainsIngressThenStopsDependenciesInReverseOrder(t *testing.T) {
	var got []string
	record := func(event string) {
		got = append(got, event)
	}

	engine := New(Config{})
	engine.RegMod(
		&lifecycleModule{name: "jin", record: record},
		&lifecycleModule{name: "sql", record: record},
		&lifecycleModule{name: "git", record: record},
		&lifecycleModule{name: "backend", record: record},
		&lifecycleModule{name: "frontend", record: record},
	)

	if err := engine.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	want := []string{"stop:jin", "stop:frontend", "stop:backend", "stop:git", "stop:sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stop() order mismatch: got %v, want %v", got, want)
	}
}

type stopModule struct {
	UnimplementedModule
	name string
	err  error
	call func(string)
}

func (m *stopModule) Name() string {
	return m.name
}

func (m *stopModule) Stop(wg *sync.WaitGroup, _ context.Context) error {
	defer wg.Done()
	m.call(m.name)
	return m.err
}

func TestStopWaitsForEachModuleBeforeStoppingDependencies(t *testing.T) {
	release := make(chan struct{})
	firstDone := make(chan struct{})
	var got []string
	var mutex sync.Mutex
	record := func(name string) {
		mutex.Lock()
		got = append(got, name)
		mutex.Unlock()
	}
	first := &blockingStopModule{name: "jin", release: release, done: firstDone, record: record}
	second := &stopModule{name: "sql", call: record}
	engine := New(Config{})
	engine.RegMod(first, second)

	stopped := make(chan error, 1)
	go func() { stopped <- engine.Stop() }()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first module Stop was not called")
	}
	mutex.Lock()
	if !reflect.DeepEqual(got, []string{"jin"}) {
		t.Fatalf("Stop advanced before jin drained: %v", got)
	}
	mutex.Unlock()
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if !reflect.DeepEqual(got, []string{"jin", "sql"}) {
		t.Fatalf("Stop order = %v", got)
	}
}

type blockingStopModule struct {
	UnimplementedModule
	name    string
	release <-chan struct{}
	done    chan<- struct{}
	record  func(string)
}

func (module *blockingStopModule) Name() string { return module.name }
func (module *blockingStopModule) Stop(wg *sync.WaitGroup, _ context.Context) error {
	defer wg.Done()
	module.record(module.name)
	close(module.done)
	<-module.release
	return nil
}

func TestStopCallsEveryModuleWhenOneReturnsError(t *testing.T) {
	stopErr := errors.New("charlie stop failed")
	laterErr := errors.New("bravo stop failed")
	var got []string
	call := func(name string) {
		got = append(got, name)
	}

	engine := New(Config{})
	engine.RegMod(
		&stopModule{name: "alpha", call: call},
		&stopModule{name: "bravo", err: laterErr, call: call},
		&stopModule{name: "charlie", err: stopErr, call: call},
	)

	done := make(chan error, 1)
	go func() {
		done <- engine.Stop()
	}()

	select {
	case err := <-done:
		if !errors.Is(err, stopErr) {
			t.Fatalf("Stop() error = %v, want %v", err, stopErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not return after a module error")
	}

	want := []string{"alpha", "charlie", "bravo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stop() calls = %v, want %v", got, want)
	}
}
