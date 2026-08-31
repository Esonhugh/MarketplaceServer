package plugin

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

type EffectLocker interface {
	LockPlugin(context.Context, string) (func() error, error)
}

type DatabaseEffectLocker struct {
	db            *gorm.DB
	lockTimeout   time.Duration
	retryInterval time.Duration
}

func NewDatabaseEffectLocker(db *gorm.DB, lockTimeout, retryInterval time.Duration) (*DatabaseEffectLocker, error) {
	if db == nil {
		return nil, errors.New("plugin effect lock requires database")
	}
	driver := strings.ToLower(strings.TrimSpace(db.Dialector.Name()))
	if driver != "sqlite" && driver != "postgres" {
		return nil, errors.New("plugin effect lock requires PostgreSQL or SQLite")
	}
	if lockTimeout <= 0 {
		lockTimeout = 5 * time.Second
	}
	if retryInterval <= 0 {
		retryInterval = 25 * time.Millisecond
	}
	return &DatabaseEffectLocker{db: db, lockTimeout: lockTimeout, retryInterval: retryInterval}, nil
}

func (locker *DatabaseEffectLocker) LockPlugin(ctx context.Context, pluginID string) (func() error, error) {
	if locker == nil || pluginID == "" {
		return nil, errors.New("invalid plugin effect lock")
	}
	if strings.EqualFold(locker.db.Dialector.Name(), "sqlite") {
		unlock, err := sqliteEffectLocks.lock(ctx, pluginID)
		if err != nil {
			return nil, err
		}
		return func() error { unlock(); return nil }, nil
	}
	return locker.lockPostgres(ctx, pluginID)
}

func (locker *DatabaseEffectLocker) lockPostgres(ctx context.Context, pluginID string) (func() error, error) {
	database, err := locker.db.DB()
	if err != nil {
		return nil, err
	}
	lockCtx, cancel := context.WithTimeout(ctx, locker.lockTimeout)
	defer cancel()
	connection, err := database.Conn(lockCtx)
	if err != nil {
		return nil, err
	}
	locked := false
	defer func() {
		if !locked {
			_ = connection.Close()
		}
	}()
	ticker := time.NewTicker(locker.retryInterval)
	defer ticker.Stop()
	for {
		if err := connection.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", pluginID).Scan(&locked); err != nil {
			return nil, err
		}
		if locked {
			break
		}
		select {
		case <-lockCtx.Done():
			return nil, lockCtx.Err()
		case <-ticker.C:
		}
	}
	return postgresUnlock(connection, pluginID, locker.lockTimeout), nil
}

func postgresUnlock(connection *sql.Conn, pluginID string, timeout time.Duration) func() error {
	return func() error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var unlocked bool
		err := connection.QueryRowContext(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", pluginID).Scan(&unlocked)
		closeErr := connection.Close()
		if err != nil {
			return err
		}
		if !unlocked {
			return errors.New("plugin effect lock was not held")
		}
		return closeErr
	}
}

var sqliteEffectLocks = newEffectKeyedLocks()

type effectKeyedLocks struct {
	mu    sync.Mutex
	locks map[string]*effectKeyedLock
}

type effectKeyedLock struct {
	semaphore chan struct{}
	users     int
}

func newEffectKeyedLocks() *effectKeyedLocks {
	return &effectKeyedLocks{locks: make(map[string]*effectKeyedLock)}
}

func (locks *effectKeyedLocks) lock(ctx context.Context, key string) (func(), error) {
	locks.mu.Lock()
	entry := locks.locks[key]
	if entry == nil {
		entry = &effectKeyedLock{semaphore: make(chan struct{}, 1)}
		entry.semaphore <- struct{}{}
		locks.locks[key] = entry
	}
	entry.users++
	locks.mu.Unlock()
	select {
	case <-ctx.Done():
		locks.releaseReference(key, entry)
		return nil, ctx.Err()
	case <-entry.semaphore:
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.semaphore <- struct{}{}
			locks.releaseReference(key, entry)
		})
	}, nil
}

func (locks *effectKeyedLocks) releaseReference(key string, entry *effectKeyedLock) {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	entry.users--
	if entry.users == 0 {
		delete(locks.locks, key)
	}
}
