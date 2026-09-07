// Package cache wires Redis for everything that needs it: asynq's queue
// backend (§8.2), Pub/Sub for SSE fan-out (§8.1), and later the supplier
// token bucket (§11.2②) and idempotency keys (§11.5). One shared connection
// config, several distinct uses.
package cache

import (
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// NewClient builds a go-redis client for Pub/Sub and (later) Lua-scripted
// rate limiting. url (e.g. Railway's managed-Redis REDIS_URL,
// "redis://default:password@host:port") wins when non-empty — it carries
// auth that a bare "host:port" addr can't express, needed the moment Redis
// sits behind a password (every managed Redis provider; the previous
// addr-only signature had no way to authenticate at all). addr stays the
// fallback for local dev's unauthenticated Redis.
func NewClient(addr, url string) *redis.Client {
	if url != "" {
		if opt, err := redis.ParseURL(url); err == nil {
			return redis.NewClient(opt)
		}
	}
	return redis.NewClient(&redis.Options{Addr: addr})
}

// AsynqRedisOpt builds the connection option asynq's Client/Server/Inspector
// need — same addr/url precedence as NewClient, so both libraries always
// agree on which Redis they talk to and how they authenticate. Returns the
// concrete asynq.RedisClientOpt (not the broader RedisConnOpt interface)
// since every caller in this codebase is typed against it; the type
// assertion below is safe because asynq.ParseRedisURI's "redis"/"rediss"
// scheme branch — the only scheme a REDIS_URL ever uses — always returns a
// RedisClientOpt value under that interface, never RedisFailoverClientOpt
// or *RedisClusterClientOpt (those are redis-sentinel:-only).
func AsynqRedisOpt(addr, url string) asynq.RedisClientOpt {
	if url != "" {
		if opt, err := asynq.ParseRedisURI(url); err == nil {
			if clientOpt, ok := opt.(asynq.RedisClientOpt); ok {
				return clientOpt
			}
		}
	}
	return asynq.RedisClientOpt{Addr: addr}
}
