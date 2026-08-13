package dao

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type queryCaptureLogger struct {
	logger.Interface
	mu      sync.Mutex
	queries []string
}

func (capture *queryCaptureLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	capture.mu.Lock()
	capture.queries = append(capture.queries, strings.ToLower(sql))
	capture.mu.Unlock()
}

func (capture *queryCaptureLogger) reset() {
	capture.mu.Lock()
	capture.queries = nil
	capture.mu.Unlock()
}

func (capture *queryCaptureLogger) joined() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return strings.Join(capture.queries, "\n")
}

func TestRepositoryRejectsInvalidPagination(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost user=test dbname=test", PreferSimpleProtocol: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		page int
		size int
	}{
		{page: 0, size: 20},
		{page: 1, size: 0},
		{page: math.MaxInt, size: 2},
	} {
		if _, _, err := repository.ListTokenMetadata(context.Background(), "user-1", testCase.page, testCase.size); !errors.Is(err, ErrInvalidPagination) {
			t.Fatalf("ListTokenMetadata(%d, %d) error = %v", testCase.page, testCase.size, err)
		}
	}
}

func TestTouchTokenUsesMonotonicActiveAtomicUpdate(t *testing.T) {
	for _, testCase := range []struct {
		name string
		open func(*queryCaptureLogger) (*gorm.DB, error)
	}{
		{
			name: "postgres",
			open: func(capture *queryCaptureLogger) (*gorm.DB, error) {
				return gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost user=test dbname=test", PreferSimpleProtocol: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: capture})
			},
		},
		{
			name: "mysql",
			open: func(capture *queryCaptureLogger) (*gorm.DB, error) {
				return gorm.Open(mysql.New(mysql.Config{DSN: "user:pass@tcp(localhost:3306)/test", SkipInitializeWithVersion: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: capture})
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			capture := &queryCaptureLogger{Interface: logger.Default.LogMode(logger.Info)}
			db, err := testCase.open(capture)
			if err != nil {
				t.Fatal(err)
			}
			repository, err := NewRepository(db)
			if err != nil {
				t.Fatal(err)
			}

			usedAt := time.Date(2026, time.August, 12, 10, 11, 12, 0, time.FixedZone("offset", 8*60*60)).UTC()
			_ = touchTokenUpdate(repository.db.WithContext(context.Background()), "token-1", usedAt)
			_, _ = validateTouchedToken(repository.db.WithContext(context.Background()), "token-1", usedAt)
			touchSQL := capture.joined()
			for _, fragment := range []string{
				"update ",
				"id = 'token-1'",
				"revoked_at is null",
				"(expires_at is null or expires_at > '2026-08-12 02:11:12')",
				"(last_used_at is null or last_used_at < '2026-08-12 02:11:12')",
				"select ",
				"last_used_at >= '2026-08-12 02:11:12'",
				"for update",
			} {
				if !strings.Contains(touchSQL, fragment) {
					t.Fatalf("TouchToken SQL does not contain %q:\n%s", fragment, touchSQL)
				}
			}
		})
	}
}

func TestRepositorySecretProjections(t *testing.T) {
	capture := &queryCaptureLogger{Interface: logger.Default.LogMode(logger.Info)}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost user=test dbname=test", PreferSimpleProtocol: true}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: capture})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = repository.ListTokenMetadata(context.Background(), "user-1", 2, 20)
	listSQL := capture.joined()
	if strings.Contains(listSQL, "secret_plaintext") || strings.Contains(listSQL, "secret_hmac") || !strings.Contains(listSQL, "user_id = 'user-1'") || !strings.Contains(listSQL, "limit 20 offset 20") {
		t.Fatalf("metadata SQL = %s", listSQL)
	}

	capture.reset()
	_, _ = repository.FindTokenCredentialBySecretHMAC(context.Background(), "hmac")
	lookupSQL := capture.joined()
	if strings.Contains(lookupSQL, "secret_plaintext") || !strings.Contains(lookupSQL, "secret_hmac = 'hmac'") {
		t.Fatalf("credential SQL = %s", lookupSQL)
	}

	capture.reset()
	_, _ = repository.FindTokenRevealByIDAndUserID(context.Background(), "token-1", "user-1")
	revealSQL := capture.joined()
	if !strings.Contains(revealSQL, "secret_plaintext") || strings.Contains(revealSQL, "secret_hmac") || !strings.Contains(revealSQL, "id = 'token-1' and user_id = 'user-1'") {
		t.Fatalf("reveal SQL = %s", revealSQL)
	}
}
