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
// rate limiting.
func NewClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr})
}

// AsynqRedisOpt builds the connection option asynq's Client/Server/Inspector
// need, from the same address so both libraries agree on which Redis they
// talk to.
func AsynqRedisOpt(addr string) asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: addr}
}
