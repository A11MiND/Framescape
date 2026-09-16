package gemini

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// leaseTTL bounds how long a concurrency token can be held before it's
// treated as leaked (a worker process crashing mid-generation must not
// permanently shrink the account's usable concurrency) — same reasoning and
// value as minimax's own VideoLimiter (ratelimit.go).
const leaseTTL = 30 * time.Minute

// maxAcquireWait bounds how long Acquire polls for a free slot before
// giving up — found live that a fixed "try once, fail immediately" gate
// (this file's own earlier version) meant an Aether-level retry re-hit the
// same still-exhausted quota window seconds later, since Aether has no
// built-in retry backoff (checked: third_party/aether has no
// backoff/delay concept at all, only retry.limit's bare count) — so pacing
// has to happen inside this call, not between separate Execute invocations.
// Left well under gen-one-panel-gemini's own 3m task timeout so a real
// GenerateImage call afterward still has room to run.
const maxAcquireWait = 120 * time.Second

// pollInterval is how often Acquire retries the combined gate while waiting.
const pollInterval = 2 * time.Second

// acquireScript combines two independent gates in one atomic check:
//  1. a ZSET-backed concurrency semaphore (expiring members so a crashed
//     worker can never leak a token forever — minimax.ratelimit.go's own
//     acquireScript, ported rather than shared, see this package's doc),
//  2. a minimum-spacing gate between successive dispatches.
//
// Found live (comic4_client.py's own 9-panel test) that (1) alone wasn't
// enough: Vertex AI Express Mode's 429 RESOURCE_EXHAUSTED kept recurring
// even with concurrency capped at 2, including two calls that started
// simultaneously (concurrency check passed for both) and both still got
// rejected — strong evidence the real ceiling is a requests-PER-TIME-WINDOW
// quota (Express Mode's documented "limited quotas, no billing enabled"
// posture), not a concurrent-connections cap. (2) directly targets that:
// no new dispatch is allowed until minIntervalMs has passed since the last
// one *started*, regardless of how many concurrency slots are free.
var acquireScript = redis.NewScript(`
local concurrencyKey = KEYS[1]
local lastDispatchKey = KEYS[2]
local now = tonumber(ARGV[1])
local leaseTTLMs = tonumber(ARGV[2])
local maxConcurrency = tonumber(ARGV[3])
local token = ARGV[4]
local minIntervalMs = tonumber(ARGV[5])

if minIntervalMs > 0 then
  local last = tonumber(redis.call('GET', lastDispatchKey) or "0")
  if (now - last) < minIntervalMs then
    return 0
  end
end

redis.call('ZREMRANGEBYSCORE', concurrencyKey, 0, now - leaseTTLMs)
if redis.call('ZCARD', concurrencyKey) < maxConcurrency then
  redis.call('ZADD', concurrencyKey, now, token)
  redis.call('PEXPIRE', concurrencyKey, leaseTTLMs)
  if minIntervalMs > 0 then
    redis.call('SET', lastDispatchKey, now, 'PX', math.max(leaseTTLMs, minIntervalMs + 1000))
  end
  return 1
end
return 0
`)

// Limiter caps concurrent Gemini/Vertex AI calls and paces how often new
// ones start — added after live testing found comic4's anchor-mode DAG
// (every panel's generation is an independent parallel branch, by design,
// for speed) reliably tripped Vertex AI Express Mode's low per-project
// quota. A nil *Limiter (every gemini.image test, and any deployment
// leaving both GEMINI_VERTEX_CONCURRENCY and GEMINI_VERTEX_MIN_INTERVAL_MS
// unset) is a no-op — unbounded, immediate — matching
// minimax.VideoLimiter's own "0 = unbounded" default posture exactly.
type Limiter struct {
	redis          *redis.Client
	accountID      string
	maxConcurrency int
	minInterval    time.Duration
	// MaxWait/PollInterval override maxAcquireWait/pollInterval — exported
	// purely so tests can shrink them; every real caller leaves both zero,
	// which applies the package defaults inside Acquire.
	MaxWait      time.Duration
	PollInterval time.Duration
}

// NewLimiter's maxConcurrency and minInterval should reflect this GCP
// project's actual granted Vertex AI quota (config, not discovered — Vertex
// AI doesn't expose per-model QPM through this SDK). accountID distinguishes
// multiple Vertex AI projects sharing one Redis instance; "default" is fine
// for a single-project deployment.
func NewLimiter(redisClient *redis.Client, accountID string, maxConcurrency int, minInterval time.Duration) *Limiter {
	return &Limiter{redis: redisClient, accountID: accountID, maxConcurrency: maxConcurrency, minInterval: minInterval}
}

// Acquire blocks (polling every pollInterval, ctx-aware) until a slot that
// satisfies both the concurrency cap and the minimum dispatch spacing is
// free, or maxAcquireWait elapses. Returns ok=false only once that whole
// budget is exhausted — the caller then returns ExecCodeError so Aether's
// own retry/backoff (bare retry.limit, no delay of its own — this file's own
// doc) gets a fresh attempt, itself starting its own wait here rather than
// hitting the API immediately. A nil receiver, a nil Redis client, or both
// limits disabled (<=0) always grants immediately — the only behavior that
// existed before this type did.
func (l *Limiter) Acquire(ctx context.Context, taskRunID string) (release func(), ok bool, err error) {
	if l == nil || l.redis == nil || (l.maxConcurrency <= 0 && l.minInterval <= 0) {
		return func() {}, true, nil
	}
	maxConcurrency := l.maxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 1 << 30 // effectively unbounded — only the interval gate applies
	}
	maxWait := l.MaxWait
	if maxWait <= 0 {
		maxWait = maxAcquireWait
	}
	pollEvery := l.PollInterval
	if pollEvery <= 0 {
		pollEvery = pollInterval
	}
	concurrencyKey, dispatchKey := l.keys()
	deadline := time.Now().Add(maxWait)
	for {
		now := float64(time.Now().UnixMilli())
		res, scriptErr := acquireScript.Run(ctx, l.redis, []string{concurrencyKey, dispatchKey},
			now, leaseTTL.Milliseconds(), maxConcurrency, taskRunID, l.minInterval.Milliseconds()).Int()
		if scriptErr != nil {
			return nil, false, fmt.Errorf("acquire gemini image token: %w", scriptErr)
		}
		if res == 1 {
			release = func() {
				l.redis.ZRem(context.Background(), concurrencyKey, taskRunID)
			}
			return release, true, nil
		}
		if time.Now().After(deadline) {
			return nil, false, nil
		}
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

func (l *Limiter) keys() (concurrencyKey, dispatchKey string) {
	return "sem:gemini:image:" + l.accountID, "sem:gemini:image:lastdispatch:" + l.accountID
}
