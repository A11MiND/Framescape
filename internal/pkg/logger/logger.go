// Package logger provides the process-wide structured logger (zap), with
// trace_id/job_id/task_run_id carried via context so every log line in a
// request or task's lifecycle can be correlated (PRD §15.1 requires this
// from the first line of code, not bolted on later).
package logger

import (
	"context"

	"go.uber.org/zap"
)

var base *zap.Logger

type ctxKey struct{}

// Init builds the process-wide logger. env is "dev" (console, debug) or
// anything else (JSON, info).
func Init(env string) {
	var l *zap.Logger
	var err error
	if env == "dev" {
		cfg := zap.NewDevelopmentConfig()
		l, err = cfg.Build()
	} else {
		cfg := zap.NewProductionConfig()
		l, err = cfg.Build()
	}
	if err != nil {
		panic(err)
	}
	base = l
}

// L returns the base logger. Init must be called first.
func L() *zap.Logger {
	if base == nil {
		Init("dev")
	}
	return base
}

// With returns a logger carrying the given fields, stashed on the context so
// downstream code can retrieve it via From without re-threading fields.
func With(ctx context.Context, fields ...zap.Field) context.Context {
	return context.WithValue(ctx, ctxKey{}, L().With(fields...))
}

// From returns the contextual logger, falling back to the base logger.
func From(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok {
		return l
	}
	return L()
}
