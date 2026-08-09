package minimax

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// videoLeaseTTL bounds how long a token can be held before it's treated as
// leaked (a worker process crashing mid-generation must not permanently
// shrink the account's usable concurrency, §11.2②).
const videoLeaseTTL = 30 * time.Minute

// acquireScript is §11.2②'s Lua script verbatim: a ZSET-backed semaphore
// with expiring members so a crashed Worker can never leak a token forever.
var acquireScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local leaseTTL = tonumber(ARGV[2])
local maxConcurrency = tonumber(ARGV[3])
local token = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now - leaseTTL)
if redis.call('ZCARD', key) < maxConcurrency then
  redis.call('ZADD', key, now, token)
  redis.call('EXPIRE', key, leaseTTL)
  return 1
end
return 0
`)

// VideoLimiter is the account-level supplier concurrency guard (PRD §11.2:
// "Worker 池规格 = 供应商额度" is the first line of defense; this ZSET
// semaphore is the second, for when multiple Worker instances share one
// MiniMax account). Acquiring returns ExecCodeError (not Failed) on the
// caller's behalf when no slot is free — Aether's retry/backoff handles the
// rest, this isn't a business failure.
type VideoLimiter struct {
	redis          *redis.Client
	accountID      string
	maxConcurrency int
}

// NewVideoLimiter's maxConcurrency should match MiniMax's actual granted
// video concurrency quota (config, not discovered — MiniMax doesn't expose
// it via API). accountID distinguishes multiple MiniMax accounts sharing one
// Redis instance; "default" is fine for a single-account POC deployment.
func NewVideoLimiter(redisClient *redis.Client, accountID string, maxConcurrency int) *VideoLimiter {
	return &VideoLimiter{redis: redisClient, accountID: accountID, maxConcurrency: maxConcurrency}
}

// Acquire blocks the caller's own decision, not the Redis call: it makes one
// attempt and returns ok=false immediately if no slot is free (the executor
// then returns ExecCodeError so Aether's own retry/backoff spaces out the
// next attempt, rather than this method spin-waiting inside a Worker slot).
func (l *VideoLimiter) Acquire(ctx context.Context, taskRunID string) (release func(), ok bool, err error) {
	if l.redis == nil || l.maxConcurrency <= 0 {
		return func() {}, true, nil // limiter disabled: unbounded (dev default)
	}
	key := l.key()
	now := float64(time.Now().Unix())
	res, err := acquireScript.Run(ctx, l.redis, []string{key}, now, videoLeaseTTL.Seconds(), l.maxConcurrency, taskRunID).Int()
	if err != nil {
		return nil, false, fmt.Errorf("acquire video token: %w", err)
	}
	if res == 0 {
		return nil, false, nil
	}
	release = func() {
		l.redis.ZRem(context.Background(), key, taskRunID)
	}
	return release, true, nil
}

func (l *VideoLimiter) key() string { return "sem:minimax:video:" + l.accountID }
