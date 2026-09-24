package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// AsynqDispatcher enqueues node work on Redis via asynq.
type AsynqDispatcher struct {
	client *asynq.Client
}

func NewAsynqDispatcher(client *asynq.Client) *AsynqDispatcher {
	return &AsynqDispatcher{client: client}
}

type nodePayload struct {
	NodeID uint64 `json:"node_id"`
	Seq    int    `json:"seq"`
}

func (d *AsynqDispatcher) Enqueue(ctx context.Context, t Task) error {
	payload := t.Payload
	if t.Kind == TaskRun || t.Kind == TaskPoll {
		payload, _ = json.Marshal(nodePayload{NodeID: t.NodeID, Seq: t.Seq})
	}
	opts := []asynq.Option{asynq.Queue(t.Queue), asynq.MaxRetry(5)}
	if id := t.ID(); id != "" {
		opts = append(opts, asynq.TaskID(id))
	}
	if !t.ProcessAt.IsZero() && t.ProcessAt.After(time.Now()) {
		opts = append(opts, asynq.ProcessAt(t.ProcessAt))
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	opts = append(opts, asynq.Timeout(timeout+2*time.Minute))
	_, err := d.client.EnqueueContext(ctx, asynq.NewTask(t.Kind, payload), opts...)
	if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// Handlers returns asynq handlers for node work.
func (o *Orchestrator) Handlers() map[string]asynq.HandlerFunc {
	decode := func(t *asynq.Task) (nodePayload, error) {
		var p nodePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return p, fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		return p, nil
	}
	return map[string]asynq.HandlerFunc{
		TaskRun: func(ctx context.Context, t *asynq.Task) error {
			p, err := decode(t)
			if err != nil {
				return err
			}
			return o.RunNode(ctx, p.NodeID, p.Seq)
		},
		TaskPoll: func(ctx context.Context, t *asynq.Task) error {
			p, err := decode(t)
			if err != nil {
				return err
			}
			return o.PollNode(ctx, p.NodeID, p.Seq)
		},
	}
}

// RedisPublisher fans events out per user and broadcasts cancel signals.
type RedisPublisher struct {
	rdb *redis.Client
}

func NewRedisPublisher(rdb *redis.Client) *RedisPublisher { return &RedisPublisher{rdb: rdb} }

// UserChannel is the Redis channel carrying one user's events.
func UserChannel(userID uint64) string { return "ev:u:" + strconv.FormatUint(userID, 10) }

// CancelChannel carries node ids whose execution must stop.
const CancelChannel = "orch:cancel"

func (p *RedisPublisher) PublishEvents(ctx context.Context, events []Event) error {
	pipe := p.rdb.Pipeline()
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		pipe.Publish(ctx, UserChannel(e.UserID), raw)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (p *RedisPublisher) PublishCancel(ctx context.Context, nodeIDs []uint64) error {
	parts := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		parts[i] = strconv.FormatUint(id, 10)
	}
	return p.rdb.Publish(ctx, CancelChannel, strings.Join(parts, ",")).Err()
}

// ListenCancel delivers cancel signals to local executions until ctx ends.
func (o *Orchestrator) ListenCancel(ctx context.Context, rdb *redis.Client) {
	sub := rdb.Subscribe(ctx, CancelChannel)
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var ids []uint64
			for _, s := range strings.Split(msg.Payload, ",") {
				if id, err := strconv.ParseUint(s, 10, 64); err == nil {
					ids = append(ids, id)
				}
			}
			o.CancelLocal(ids)
		}
	}
}

// RunSweeper sweeps on an interval until ctx ends.
func (o *Orchestrator) RunSweeper(ctx context.Context, interval time.Duration, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := o.Sweep(ctx); err != nil && onError != nil && ctx.Err() == nil {
				onError(err)
			}
		}
	}
}
