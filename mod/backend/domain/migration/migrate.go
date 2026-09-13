// Package migration limits schema changes to the models owned by a domain.
package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// WithBackendLock serializes the entire PostgreSQL migration sequence per schema.
// Lock order is backend session lock -> domain transaction lock -> domain DDL.
// The distinct domain keys remain necessary for standalone domain migrations.
// The callback must use the supplied handle; its transactions share the reserved
// connection, but commit independently (there is deliberately no outer transaction).
func WithBackendLock(db *gorm.DB, migrate func(*gorm.DB) error) (err error) {
	if db.Dialector.Name() != "postgres" {
		return migrate(db)
	}
	if db.Error != nil {
		return db.Error
	}
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
		return errors.New("backend migration requires a non-transactional database")
	}
	pool, err := db.DB()
	if err != nil {
		return err
	}
	conn, err := pool.Conn(db.Statement.Context)
	if err != nil {
		return fmt.Errorf("reserve backend migration connection: %w", err)
	}
	var key int64
	locked := false
	// Even an interrupted lock acquisition can have reached the server. Never
	// return that session to the pool unless release is confirmed.
	defer func() {
		if locked {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var released bool
			unlockErr := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, key).Scan(&released)
			if unlockErr == nil && !released {
				unlockErr = errors.New("backend migration lock was not held")
			}
			if unlockErr != nil {
				err = errors.Join(err, fmt.Errorf("unlock backend migration: %w", unlockErr))
				locked = false
			}
		}
		if !locked {
			// ErrBadConn tells database/sql to discard, not recycle, the session.
			if discardErr := conn.Raw(func(any) error { return driver.ErrBadConn }); discardErr != nil && !errors.Is(discardErr, driver.ErrBadConn) && !errors.Is(discardErr, sql.ErrConnDone) {
				err = errors.Join(err, fmt.Errorf("discard backend migration connection: %w", discardErr))
			}
		}
		if closeErr := conn.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
			err = errors.Join(err, fmt.Errorf("close backend migration connection: %w", closeErr))
		}
	}()
	if err = conn.QueryRowContext(db.Statement.Context, `SELECT hashtextextended(current_schema() || '.backend_migration', 0)`).Scan(&key); err != nil {
		return fmt.Errorf("resolve backend migration lock: %w", err)
	}
	if _, err = conn.ExecContext(db.Statement.Context, `SELECT pg_advisory_lock($1)`, key); err != nil {
		return fmt.Errorf("lock backend migration: %w", err)
	}
	locked = true
	// Clone the statement before replacing ConnPool, as GORM's Connection does.
	// Binding the callback also avoids pool starvation with MaxOpenConns(1).
	scoped := db.Session(&gorm.Session{NewDB: true, Context: db.Statement.Context})
	scoped.Statement.ConnPool = conn
	return migrate(scoped)
}

// AutoMigrate does not recursively alter tables owned by other domains. Foreign
// keys are installed after all owned tables exist, using the original config.
func AutoMigrate(db *gorm.DB, models ...any) error {
	if db.Dialector.Name() != "postgres" {
		return db.AutoMigrate(models...)
	}
	scoped := db.Session(&gorm.Session{})
	scoped.IgnoreRelationshipsWhenMigrating = true
	if err := scoped.AutoMigrate(models...); err != nil {
		return err
	}
	if db.DisableForeignKeyConstraintWhenMigrating || db.IgnoreRelationshipsWhenMigrating {
		return nil
	}
	for _, model := range models {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			return err
		}
		for _, relation := range stmt.Schema.Relationships.Relations {
			if relation.Field.IgnoreMigration {
				continue
			}
			if constraint := relation.ParseConstraint(); constraint != nil && constraint.Schema == stmt.Schema {
				if !db.Migrator().HasConstraint(model, constraint.Name) {
					if err := db.Migrator().CreateConstraint(model, constraint.Name); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// TriggerGuard describes a PostgreSQL trigger owned by one domain. Identifiers
// and SQL must be static application definitions, never request input.
type TriggerGuard struct {
	Table     string
	Name      string
	CreateSQL string
}

func DropTriggerGuards(db *gorm.DB, guards []TriggerGuard) error {
	for _, guard := range guards {
		if db.Migrator().HasTable(guard.Table) {
			if err := db.Exec("DROP TRIGGER IF EXISTS " + guard.Name + " ON " + guard.Table).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func InstallTriggerGuards(db *gorm.DB, guards []TriggerGuard) error {
	for _, guard := range guards {
		if err := db.Exec(guard.CreateSQL).Error; err != nil {
			return err
		}
	}
	return nil
}
