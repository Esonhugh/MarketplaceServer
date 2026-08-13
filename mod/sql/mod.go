package sql

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ kernel.Module = (*Mod)(nil)

type gormOpener func(gorm.Dialector, *gorm.Config) (*gorm.DB, error)

func defaultGORMOpener(dialector gorm.Dialector, config *gorm.Config) (*gorm.DB, error) {
	return gorm.Open(dialector, config)
}

type Config struct {
	Driver          string        `yaml:"driver" mapstructure:"driver"`
	DSN             string        `yaml:"dsn" mapstructure:"dsn"`
	Debug           bool          `yaml:"debug" mapstructure:"debug"`
	MaxIdleConns    int           `yaml:"maxIdleConns" mapstructure:"maxIdleConns"`
	MaxOpenConns    int           `yaml:"maxOpenConns" mapstructure:"maxOpenConns"`
	ConnMaxLifetime time.Duration `yaml:"connMaxLifetime" mapstructure:"connMaxLifetime"`
}

type Mod struct {
	kernel.UnimplementedModule

	config Config
	db     *gorm.DB
	opener gormOpener
}

func (m *Mod) Name() string { return "sql" }

func (m *Mod) Config() any { return &m.config }

func (m *Mod) PreInit(hub *kernel.Hub) error {
	if m.config.DSN == "" {
		return errors.New("sql.dsn is required")
	}

	dialector, err := m.dialector()
	if err != nil {
		return err
	}
	opener := m.opener
	if opener == nil {
		opener = defaultGORMOpener
	}
	logLevel := logger.Silent
	if m.config.Debug {
		logLevel = logger.Info
	}
	gormLogger := logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
		LogLevel:             logLevel,
		ParameterizedQueries: true,
	})
	m.db, err = opener(dialector, &gorm.Config{Logger: gormLogger})
	if err != nil {
		return errors.New("open sql database: failed")
	}

	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("load sql connection pool: %w", err)
	}
	if m.config.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(m.config.MaxIdleConns)
	}
	if m.config.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(m.config.MaxOpenConns)
	}
	if m.config.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(m.config.ConnMaxLifetime)
	}

	hub.Map(&m.db)
	return nil
}

func (m *Mod) Init(_ *kernel.Hub) error {
	if m.db == nil {
		return errors.New("sql database is not initialized; PreInit must complete first")
	}

	sqlDB, err := m.db.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

func (m *Mod) Stop(wg *sync.WaitGroup, _ context.Context) error {
	defer wg.Done()
	if m.db == nil {
		return nil
	}
	sqlDB, err := m.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (m *Mod) dialector() (gorm.Dialector, error) {
	driver := strings.ToLower(strings.TrimSpace(m.config.Driver))
	switch driver {
	case "", "mysql":
		return mysql.Open(m.config.DSN), nil
	case "postgres", "postgresql":
		return postgres.Open(m.config.DSN), nil
	default:
		return nil, fmt.Errorf("unsupported sql driver %q", m.config.Driver)
	}
}
