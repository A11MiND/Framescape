package httpapi

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/config"
)

const testJWTSecret = "http-layer-test-secret"

// newTestServer opens a real connection to the same local MySQL every other
// package's DB-backed tests use (creditsvc_test.go's own convention: skip
// rather than fail when it's unreachable, since schema only ever comes from
// goose migrations, never AutoMigrate, so there's nothing this helper could
// stand up on its own). jobs/credits/community/minimax/objects stay nil —
// every handler covered by this package's current tests only touches
// s.db/s.jwtSecret/s.redis; job/asset/credit-topup routes need a real
// jobsvc.Service + workflow.Engine and are out of scope here.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := persistence.Open(persistence.Config{DSN: config.MySQLDSN()})
	if err != nil {
		t.Skipf("no local MySQL available, skipping: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Skipf("no local MySQL available, skipping: %v", err)
	}
	return NewServer(db, nil, nil, nil, testRedisClient(t), testJWTSecret, nil, nil)
}

// testRedisClient returns a live client only if Redis actually answers —
// checkVerifyRateLimit already treats a nil s.redis as "no rate limiting"
// (fail-open by design), so most tests work fine either way; a test that
// specifically exercises rate limiting must skip itself when this is nil.
func testRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	c := cache.NewClient(config.RedisAddr())
	if err := c.Ping(context.Background()).Err(); err != nil {
		return nil
	}
	return c
}

// TestMain sweeps every row this package's tests could have created — by
// the shared "httptest-"/"+1555" prefixes uniqueEmail/uniquePhone always
// use — once the whole suite finishes, so repeated local `go test` runs
// don't pile up rows in the dev database indefinitely. Best-effort: skipped
// entirely if MySQL isn't reachable (same condition every individual test
// already skips itself on), and errors here are swallowed since a cleanup
// failure shouldn't turn a passing test run red.
func TestMain(m *testing.M) {
	code := m.Run()
	if db, err := persistence.Open(persistence.Config{DSN: config.MySQLDSN()}); err == nil {
		if sqlDB, err := db.DB(); err == nil && sqlDB.Ping() == nil {
			// No FK on credit_accounts.user_id — the row createAccount inserts
			// alongside each test user would otherwise outlive it forever.
			// Captured before the users themselves are deleted, and scoped to
			// exactly those IDs rather than a blanket "delete every orphan"
			// sweep that could also catch rows unrelated to this test run.
			var testUserIDs []uint64
			db.Model(&persistence.User{}).Where("email LIKE ?", "httptest-%").Pluck("id", &testUserIDs)
			if len(testUserIDs) > 0 {
				db.Where("user_id IN ?", testUserIDs).Delete(&persistence.CreditAccount{})
			}
			db.Where("email LIKE ?", "httptest-%").Delete(&persistence.User{})
			db.Where("email LIKE ?", "httptest-%").Delete(&persistence.EmailVerificationCode{})
			db.Where("phone LIKE ?", "+1555%").Delete(&persistence.PhoneVerificationCode{})
		}
	}
	os.Exit(code)
}

// uniqueEmail/uniquePhone give each test its own identifier so parallel
// `go test` runs (and re-runs against the same dev DB) never collide on the
// users table's unique email/phone/google_sub keys, and so TestMain's final
// sweep can find everything this package's tests created.
func uniqueEmail(t *testing.T) string {
	t.Helper()
	safeName := strings.ToLower(strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	return fmt.Sprintf("httptest-%s-%d@example.com", safeName, rand.Int63())
}

func uniquePhone(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("+1555%07d", rand.Int63n(10_000_000))
}

// uniqueGoogleSub stays well under google_sub's VARCHAR(64) — a real Google
// `sub` is a short numeric string, nothing like an email address.
func uniqueGoogleSub(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-sub-%d", rand.Int63())
}
