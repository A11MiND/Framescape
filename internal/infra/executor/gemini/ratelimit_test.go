package gemini

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"aigc-platform/internal/pkg/config"
)

func testRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: config.RedisAddr()})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skip("no local Redis available, skipping")
	}
	return c
}

// fastLimiter shrinks MaxWait/PollInterval so tests exercising the "still
// blocked" path finish in milliseconds instead of waiting out the real
// maxAcquireWait (120s) — see ratelimit.go's own doc on why Acquire polls
// at all rather than failing fast like this file's earlier version did.
func fastLimiter(rc *redis.Client, accountID string, maxConcurrency int, minInterval time.Duration) *Limiter {
	l := NewLimiter(rc, accountID, maxConcurrency, minInterval)
	l.MaxWait = 200 * time.Millisecond
	l.PollInterval = 20 * time.Millisecond
	return l
}

// TestLimiterAcquire mirrors minimax's own TestVideoLimiterAcquire — same
// ZSET concurrency semaphore at its core (ratelimit.go's own doc), with the
// interval gate disabled (minInterval=0) so this test exercises concurrency
// alone.
func TestLimiterAcquire(t *testing.T) {
	rc := testRedisClient(t)
	accountID := "httptest-limiter-" + t.Name()
	t.Cleanup(func() {
		rc.Del(context.Background(), "sem:gemini:image:"+accountID, "sem:gemini:image:lastdispatch:"+accountID)
	})

	limiter := fastLimiter(rc, accountID, 2, 0)
	ctx := context.Background()

	release1, ok, err := limiter.Acquire(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("acquire 1: ok=%v err=%v, want ok=true", ok, err)
	}
	release2, ok, err := limiter.Acquire(ctx, "task-2")
	if err != nil || !ok {
		t.Fatalf("acquire 2: ok=%v err=%v, want ok=true", ok, err)
	}

	// Concurrency is exhausted at 2 — a third acquire must eventually be
	// refused (after polling out its shrunk MaxWait budget), not block
	// forever or error.
	_, ok, err = limiter.Acquire(ctx, "task-3")
	if err != nil {
		t.Fatalf("acquire 3: unexpected error %v", err)
	}
	if ok {
		t.Fatal("acquire 3: ok = true, want false (concurrency limit reached)")
	}

	// Releasing one slot frees capacity for the next caller.
	release1()
	_, ok, err = limiter.Acquire(ctx, "task-3")
	if err != nil || !ok {
		t.Fatalf("acquire after release: ok=%v err=%v, want ok=true", ok, err)
	}
	release2()
}

// TestLimiterMinInterval covers the gate added after live testing found
// concurrency alone insufficient against Vertex AI Express Mode's
// requests-per-time-window quota (ratelimit.go's own doc): a second acquire
// within minInterval of the first must be refused even though concurrency
// itself has plenty of room, and must succeed again once enough real time
// has passed.
func TestLimiterMinInterval(t *testing.T) {
	rc := testRedisClient(t)
	accountID := "httptest-limiter-" + t.Name()
	t.Cleanup(func() {
		rc.Del(context.Background(), "sem:gemini:image:"+accountID, "sem:gemini:image:lastdispatch:"+accountID)
	})

	minInterval := 150 * time.Millisecond
	limiter := fastLimiter(rc, accountID, 10, minInterval) // concurrency wide open — only the interval gate should bind
	ctx := context.Background()

	release1, ok, err := limiter.Acquire(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("acquire 1: ok=%v err=%v, want ok=true", ok, err)
	}
	release1()

	// Immediately after: still inside minInterval of task-1's own dispatch,
	// so this must be refused despite concurrency being nowhere near full.
	start := time.Now()
	_, ok, err = limiter.Acquire(ctx, "task-2")
	if err != nil {
		t.Fatalf("acquire 2 (too soon): unexpected error %v", err)
	}
	if ok {
		t.Fatal("acquire 2 (too soon): ok = true, want false (within minInterval of the previous dispatch)")
	}
	if elapsed := time.Since(start); elapsed < limiter.MaxWait {
		t.Errorf("acquire 2 gave up after %v, want it to poll out its MaxWait budget (%v)", elapsed, limiter.MaxWait)
	}

	// Enough real time has now passed (the failed poll loop above already
	// spent >= minInterval) that a fresh acquire should succeed.
	release2, ok, err := limiter.Acquire(ctx, "task-3")
	if err != nil || !ok {
		t.Fatalf("acquire after interval elapsed: ok=%v err=%v, want ok=true", ok, err)
	}
	release2()
}

// TestLimiterDisabled covers both limits disabled (<=0) and a nil *Limiter
// (every gemini.image test, and any deployment that never constructs one) —
// both must never block.
func TestLimiterDisabled(t *testing.T) {
	limiter := NewLimiter(nil, "unused", 0, 0)
	release, ok, err := limiter.Acquire(context.Background(), "task-1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want ok=true for a disabled limiter", ok, err)
	}
	release() // must not panic on a nil redis client

	var nilLimiter *Limiter
	release, ok, err = nilLimiter.Acquire(context.Background(), "task-1")
	if err != nil || !ok {
		t.Fatalf("nil limiter: ok=%v err=%v, want ok=true", ok, err)
	}
	release()
}
