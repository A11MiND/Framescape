package realtime

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/pkg/config"
)

// Opt-in because it opens 1,000 live subscriptions and needs the disposable
// Redis service. It verifies the one-subscription-per-process hub can fan an
// event out to every browser listener without dropping a healthy client.
func TestPhase7ThousandSSEListeners(t *testing.T) {
	if os.Getenv("FRAMESCAPE_LOAD_TEST") != "1" {
		t.Skip("set FRAMESCAPE_LOAD_TEST=1 to run the local SSE rehearsal")
	}
	rdb := cache.NewClient(config.RedisAddr(), config.RedisURL())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("no local Redis available: %v", err)
	}
	hub := NewHub(ctx, rdb)
	const listeners = 1000
	userID := uint64(999997001)
	channels := make([]<-chan orchestrator.Event, 0, listeners)
	stops := make([]func(), 0, listeners)
	for i := 0; i < listeners; i++ {
		ch, stop, err := hub.Subscribe(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		channels = append(channels, ch)
		stops = append(stops, stop)
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
		_ = rdb.Close()
	}()
	time.Sleep(100 * time.Millisecond)
	raw, _ := json.Marshal(orchestrator.Event{ID: 1, JobBizID: "load-job", Type: orchestrator.EventJobStatus, Payload: json.RawMessage(`{"status":"running"}`)})
	if err := rdb.Publish(ctx, orchestrator.UserChannel(userID), raw).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for i, ch := range channels {
		select {
		case event := <-ch:
			if event.ID != 1 || event.JobBizID != "load-job" {
				t.Fatalf("listener %d received %+v", i, event)
			}
		case <-deadline:
			t.Fatalf("listener %d did not receive the event", i)
		}
	}
}
