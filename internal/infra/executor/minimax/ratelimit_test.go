package minimax

import (
	"context"
	"testing"

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

func TestVideoLimiterAcquire(t *testing.T) {
	rc := testRedisClient(t)
	accountID := "httptest-limiter-" + t.Name()
	t.Cleanup(func() { rc.Del(context.Background(), "sem:minimax:video:"+accountID) })

	limiter := NewVideoLimiter(rc, accountID, 2)
	ctx := context.Background()

	release1, ok, err := limiter.Acquire(ctx, "task-1")
	if err != nil || !ok {
		t.Fatalf("acquire 1: ok=%v err=%v, want ok=true", ok, err)
	}
	release2, ok, err := limiter.Acquire(ctx, "task-2")
	if err != nil || !ok {
		t.Fatalf("acquire 2: ok=%v err=%v, want ok=true", ok, err)
	}

	// Concurrency is exhausted at 2 — a third acquire must be refused, not
	// block or error.
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

// TestVideoLimiterDisabled covers the documented "limiter disabled:
// unbounded (dev default)" path — maxConcurrency<=0 must never block.
func TestVideoLimiterDisabled(t *testing.T) {
	limiter := NewVideoLimiter(nil, "unused", 0)
	release, ok, err := limiter.Acquire(context.Background(), "task-1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v, want ok=true for a disabled limiter", ok, err)
	}
	release() // must not panic on a nil redis client
}
