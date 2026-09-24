// Package realtime fans per-user event channels out to SSE connections.
// Each process holds one Redis subscription and adds or drops user channels
// as their first listener arrives or last listener leaves, so the number of
// Redis connections does not grow with the number of browser connections.
package realtime

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"

	"aigc-platform/internal/infra/orchestrator"
)

type Hub struct {
	ps *redis.PubSub

	mu        sync.Mutex
	listeners map[uint64]map[chan orchestrator.Event]struct{}
}

// NewHub starts the shared subscription; it stops when ctx ends.
func NewHub(ctx context.Context, rdb *redis.Client) *Hub {
	h := &Hub{ps: rdb.Subscribe(ctx), listeners: map[uint64]map[chan orchestrator.Event]struct{}{}}
	go h.run(ctx)
	return h
}

func (h *Hub) run(ctx context.Context) {
	defer h.ps.Close()
	ch := h.ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			userID, err := strconv.ParseUint(strings.TrimPrefix(msg.Channel, "ev:u:"), 10, 64)
			if err != nil {
				continue
			}
			var ev orchestrator.Event
			if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
				continue
			}
			h.mu.Lock()
			for l := range h.listeners[userID] {
				select {
				case l <- ev:
				default: // a stalled client resyncs from the database on reconnect
				}
			}
			h.mu.Unlock()
		}
	}
}

// Subscribe returns a stream of the user's events and a function to stop it.
func (h *Hub) Subscribe(ctx context.Context, userID uint64) (<-chan orchestrator.Event, func(), error) {
	ch := make(chan orchestrator.Event, 64)
	h.mu.Lock()
	first := len(h.listeners[userID]) == 0
	if first {
		h.listeners[userID] = map[chan orchestrator.Event]struct{}{}
	}
	h.listeners[userID][ch] = struct{}{}
	h.mu.Unlock()
	if first {
		if err := h.ps.Subscribe(ctx, orchestrator.UserChannel(userID)); err != nil {
			h.remove(userID, ch)
			return nil, nil, err
		}
	}
	var once sync.Once
	return ch, func() { once.Do(func() { h.remove(userID, ch) }) }, nil
}

func (h *Hub) remove(userID uint64, ch chan orchestrator.Event) {
	h.mu.Lock()
	delete(h.listeners[userID], ch)
	last := len(h.listeners[userID]) == 0
	if last {
		delete(h.listeners, userID)
	}
	h.mu.Unlock()
	if last {
		_ = h.ps.Unsubscribe(context.Background(), orchestrator.UserChannel(userID))
	}
}
