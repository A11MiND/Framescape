package httpapi

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"aigc-platform/internal/application/communitysvc"
	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/jobsvc"
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
// fine for auth_test.go/auth_oauth_test.go, whose handlers only ever touch
// s.db/s.jwtSecret/s.redis. Anything that needs a real job/credits/asset
// stack (jobs_test.go, assets_test.go) uses newFullTestServer instead.
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

// newFullTestServer wires jobs/credits/community the same way cmd/api/
// main.go does — real *creditsvc.Service/*communitysvc.Service against the
// same MySQL, a fakeEngine standing in for the real Aether rpc.Client, and
// nil minimax/objects (out of scope: no route this package currently tests
// needs either — see server.go's own field docs for what actually depends
// on them). Use this over the plain newTestServer whenever a test needs to
// create/read/cancel/delete a job or touch an asset's publish flag.
func newFullTestServer(t *testing.T) (*Server, *fakeEngine) {
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
	eng := newFakeEngine()
	credits := creditsvc.New(sqlDB)
	community := communitysvc.New(sqlDB, credits)
	jobs := jobsvc.New(db, eng, credits, nil)
	return NewServer(db, jobs, credits, community, testRedisClient(t), testJWTSecret, nil, nil), eng
}

// registerAndFund creates a fresh account through the real /auth/register
// endpoint (so it's exactly what a real signup produces, not a hand-rolled
// row) and, if credits > 0, tops up its balance through creditsvc.Recharge —
// never a raw SQL balance write, same reasoning as creditsvc_test.go's own
// seeding: every balance change must be ledger-backed or the
// balance+held==SUM(credit_ledger.amount) invariant this app relies on
// elsewhere wouldn't mean anything here.
func registerAndFund(t *testing.T, s *Server, credits int) (accessToken string, uid uint64) {
	t.Helper()
	r := s.Router()
	email := uniqueEmail(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", registerRequest{Email: email, Password: "correct-horse"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("register: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	tp := decodeTokens(t, rec)
	cl, err := parseToken(testJWTSecret, tp.AccessToken, tokenAccess)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if credits > 0 {
		if err := s.credits.Recharge(context.Background(), cl.UserID, "test:recharge:"+email, credits, "test seed"); err != nil {
			t.Fatalf("recharge: %v", err)
		}
	}
	return tp.AccessToken, cl.UserID
}

// makeAdmin flips a test user's is_admin flag directly via GORM — unlike
// registerAndFund's ledger-backed credit seeding, there's no HTTP path that
// can grant the very first admin (handleAdminSetAdmin itself requires an
// existing admin caller), so a direct DB write is the only option here,
// same as this migration's own doc on how the real first admin gets
// bootstrapped in production.
func makeAdmin(t *testing.T, s *Server, uid uint64) {
	t.Helper()
	if err := s.db.Model(&persistence.User{}).Where("id = ?", uid).Update("is_admin", true).Error; err != nil {
		t.Fatalf("make admin: %v", err)
	}
}

// testRedisClient returns a live client only if Redis actually answers —
// checkVerifyRateLimit already treats a nil s.redis as "no rate limiting"
// (fail-open by design), so most tests work fine either way; a test that
// specifically exercises rate limiting must skip itself when this is nil.
func testRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	c := cache.NewClient(config.RedisAddr(), config.RedisURL())
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
			// No FK on any of these user_id/owner_user_id columns — every row
			// jobs_test.go/assets_test.go/characters_test.go/presets_test.go/
			// projects_test.go create alongside a test user (credit account,
			// ledger entries, jobs, assets, characters, projects, saved
			// presets) would otherwise outlive it forever. Captured before the
			// users themselves are deleted, and scoped to exactly those IDs
			// rather than a blanket "delete every orphan" sweep that could
			// also catch rows unrelated to this run.
			var testUserIDs []uint64
			db.Model(&persistence.User{}).Where("email LIKE ?", "httptest-%").Pluck("id", &testUserIDs)
			if len(testUserIDs) > 0 {
				db.Where("user_id IN ?", testUserIDs).Delete(&persistence.CreditAccount{})
				db.Where("user_id IN ?", testUserIDs).Delete(&persistence.CreditLedger{})
				// DeletedAt here is a plain *time.Time, not gorm.DeletedAt — GORM
				// has no soft-delete scope on these models, so this is already
				// a real DELETE, not the no-op Unscoped() would exist to bypass.
				db.Where("user_id IN ?", testUserIDs).Delete(&persistence.Job{})
				db.Where("user_id IN ?", testUserIDs).Delete(&persistence.Asset{})
				db.Where("user_id IN ?", testUserIDs).Delete(&persistence.Character{})
				db.Where("user_id IN ?", testUserIDs).Delete(&persistence.Project{})
				db.Where("owner_user_id IN ?", testUserIDs).Delete(&persistence.Preset{})
			}
			// presets_test.go's system-preset fixtures have owner_user_id NULL
			// (that's the whole point — they stand in for a seeded system
			// preset), so they can't be swept via any test user's ID above.
			db.Where("name = ?", "httptest-system-preset-fixture").Delete(&persistence.Preset{})
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
