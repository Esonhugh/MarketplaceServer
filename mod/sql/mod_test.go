package sql

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/juanjiTech/inject/v2"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestModName(t *testing.T) {
	m := &Mod{}
	if got := m.Name(); got != "sql" {
		t.Fatalf("Name() = %q, want %q", got, "sql")
	}
}

func TestConfigFieldsHaveYAMLAndMapstructureTags(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	for _, fieldName := range []string{
		"Driver",
		"DSN",
		"Debug",
		"MaxIdleConns",
		"MaxOpenConns",
		"ConnMaxLifetime",
	} {
		field, ok := cfgType.FieldByName(fieldName)
		if !ok {
			t.Fatalf("Config.%s field is missing", fieldName)
		}
		if got := field.Tag.Get("yaml"); got == "" {
			t.Fatalf("Config.%s is missing yaml tag", fieldName)
		}
		if got := field.Tag.Get("mapstructure"); got == "" {
			t.Fatalf("Config.%s is missing mapstructure tag", fieldName)
		}
	}
}

func TestDialectorSelectsSupportedDrivers(t *testing.T) {
	tests := []struct {
		name       string
		driverName string
		want       string
	}{
		{name: "default mysql", driverName: "", want: "mysql"},
		{name: "mysql", driverName: "mysql", want: "mysql"},
		{name: "postgres", driverName: "postgres", want: "postgres"},
		{name: "postgresql alias", driverName: "postgresql", want: "postgres"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Mod{config: Config{Driver: tt.driverName, DSN: "test-dsn"}}
			dialector, err := m.dialector()
			if err != nil {
				t.Fatalf("dialector() returned error: %v", err)
			}
			if got := dialector.Name(); got != tt.want {
				t.Fatalf("dialector().Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDialectorRejectsUnknownDriver(t *testing.T) {
	m := &Mod{config: Config{Driver: "sqlite", DSN: "test-dsn"}}
	_, err := m.dialector()
	if err == nil {
		t.Fatal("dialector() succeeded for unsupported driver, want error")
	}
	if !strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), m.config.DSN) {
		t.Fatalf("dialector() error = %q, want unsupported driver error without DSN", err.Error())
	}
}

func TestPreInitSanitizesDatabaseOpenErrors(t *testing.T) {
	const sensitiveDSN = "postgres://db-user:db-password@db.example.invalid/marketplace?secret=top-secret"
	m := &Mod{
		config: Config{Driver: "postgres", DSN: sensitiveDSN},
		opener: func(gorm.Dialector, *gorm.Config) (*gorm.DB, error) {
			return nil, fmt.Errorf("connect to %s failed for db-user with db-password and top-secret", sensitiveDSN)
		},
	}

	err := m.PreInit(newTestHub())
	if err == nil {
		t.Fatal("PreInit() succeeded, want database open error")
	}
	if got, want := err.Error(), "open sql database: failed"; got != want {
		t.Fatalf("PreInit() error = %q, want %q", got, want)
	}
	for _, secret := range []string{sensitiveDSN, "db-user", "db-password", "top-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("PreInit() error leaked sensitive value %q: %q", secret, err.Error())
		}
	}
}

func TestPreInitRequiresDSNBeforeOpeningDatabase(t *testing.T) {
	called := false
	m := &Mod{
		config: Config{Driver: "mysql"},
		opener: func(gorm.Dialector, *gorm.Config) (*gorm.DB, error) {
			called = true
			return nil, fmt.Errorf("open should not be called without a DSN")
		},
	}
	err := m.PreInit(newTestHub())
	if err == nil {
		t.Fatal("PreInit() succeeded without DSN, want error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "dsn") {
		t.Fatalf("PreInit() error = %q, want DSN validation error", err.Error())
	}
	if called {
		t.Fatal("PreInit() called GORM open despite missing DSN")
	}
}

func TestPreInitMapsGormDBAndAppliesStablePoolSettings(t *testing.T) {
	fakeDB, _, tracker := newTrackedGormDB(t)
	var openedDialector string
	m := &Mod{
		config: Config{
			Driver:          "postgresql",
			DSN:             "postgres://user:secret@example.invalid/marketplace",
			MaxIdleConns:    3,
			MaxOpenConns:    7,
			ConnMaxLifetime: time.Minute,
		},
		opener: func(dialector gorm.Dialector, cfg *gorm.Config) (*gorm.DB, error) {
			openedDialector = dialector.Name()
			if cfg == nil {
				t.Fatal("PreInit() passed nil gorm.Config")
			}
			return fakeDB, nil
		},
	}
	hub := newTestHub()

	if err := m.PreInit(hub); err != nil {
		t.Fatalf("PreInit() returned error: %v", err)
	}
	if openedDialector != "postgres" {
		t.Fatalf("GORM opened with dialector %q, want %q", openedDialector, "postgres")
	}

	var loaded *gorm.DB
	if err := hub.Load(&loaded); err != nil {
		t.Fatalf("hub.Load(&loaded) returned error: %v", err)
	}
	if loaded != fakeDB {
		t.Fatal("PreInit() did not map the opened *gorm.DB into DI")
	}

	sqlDB, err := loaded.DB()
	if err != nil {
		t.Fatalf("loaded.DB() returned error: %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}
	if got := tracker.pings.Load(); got != 0 {
		t.Fatalf("PreInit() pinged database %d times, want Init() to own ping", got)
	}
}

func TestInitRequiresPreInit(t *testing.T) {
	err := (&Mod{}).Init(newTestHub())
	if err == nil {
		t.Fatal("Init() succeeded before PreInit(), want diagnostic error")
	}
	if got, want := err.Error(), "sql database is not initialized; PreInit must complete first"; got != want {
		t.Fatalf("Init() error = %q, want %q", got, want)
	}
}

func TestInitPingsDatabase(t *testing.T) {
	fakeDB, _, tracker := newTrackedGormDB(t)
	m := &Mod{db: fakeDB}

	if err := m.Init(newTestHub()); err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}
	if got := tracker.pings.Load(); got != 1 {
		t.Fatalf("Init() ping count = %d, want 1", got)
	}
}

func TestStopWithNilDatabaseMarksWaitGroupDone(t *testing.T) {
	m := &Mod{}
	var wg sync.WaitGroup
	wg.Add(1)

	if err := m.Stop(&wg, context.Background()); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}

	waitForWaitGroup(t, &wg)
}

func TestStopClosesDatabaseAndMarksWaitGroupDone(t *testing.T) {
	fakeDB, _, tracker := newTrackedGormDB(t)
	m := &Mod{db: fakeDB}
	if err := m.Init(newTestHub()); err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	if err := m.Stop(&wg, context.Background()); err != nil {
		t.Fatalf("Stop() returned error: %v", err)
	}

	waitForWaitGroup(t, &wg)
	if got := tracker.closes.Load(); got == 0 {
		t.Fatal("Stop() did not close the underlying database connection")
	}
}

func waitForWaitGroup(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not call wg.Done()")
	}
}

func newTestHub() *kernel.Hub {
	return &kernel.Hub{Injector: inject.New()}
}

func newTrackedGormDB(t *testing.T) (*gorm.DB, *stdsql.DB, *trackedDriverState) {
	t.Helper()
	rawDB, tracker := newTrackedStdDB(t)
	gormDB, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      rawDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("create tracked gorm DB: %v", err)
	}
	return gormDB, rawDB, tracker
}

var trackedDriverSeq atomic.Uint64

func newTrackedStdDB(t *testing.T) (*stdsql.DB, *trackedDriverState) {
	t.Helper()
	state := &trackedDriverState{}
	name := fmt.Sprintf("marketplace_sql_test_%d", trackedDriverSeq.Add(1))
	stdsql.Register(name, trackedDriver{state: state})
	db, err := stdsql.Open(name, "")
	if err != nil {
		t.Fatalf("open tracked database/sql DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, state
}

type trackedDriverState struct {
	pings  atomic.Int64
	closes atomic.Int64
}

type trackedDriver struct {
	state *trackedDriverState
}

func (d trackedDriver) Open(string) (driver.Conn, error) {
	return &trackedConn{state: d.state}, nil
}

type trackedConn struct {
	state *trackedDriverState
}

func (c *trackedConn) Prepare(string) (driver.Stmt, error) { return trackedStmt{}, nil }
func (c *trackedConn) Close() error {
	c.state.closes.Add(1)
	return nil
}
func (c *trackedConn) Begin() (driver.Tx, error) { return trackedTx{}, nil }
func (c *trackedConn) Ping(context.Context) error {
	c.state.pings.Add(1)
	return nil
}

type trackedStmt struct{}

func (trackedStmt) Close() error                               { return nil }
func (trackedStmt) NumInput() int                              { return -1 }
func (trackedStmt) Exec([]driver.Value) (driver.Result, error) { return trackedResult(0), nil }
func (trackedStmt) Query([]driver.Value) (driver.Rows, error)  { return trackedRows{}, nil }

type trackedTx struct{}

func (trackedTx) Commit() error   { return nil }
func (trackedTx) Rollback() error { return nil }

type trackedResult int64

func (r trackedResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r trackedResult) RowsAffected() (int64, error) { return int64(r), nil }

type trackedRows struct{}

func (trackedRows) Columns() []string         { return nil }
func (trackedRows) Close() error              { return nil }
func (trackedRows) Next([]driver.Value) error { return driver.ErrBadConn }
