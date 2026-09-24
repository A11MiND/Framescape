package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// latencyRange is a uniform [Min, Max] delay. JSON/env form is "15s-30s" or a
// single duration.
type latencyRange struct {
	Min time.Duration
	Max time.Duration
}

func parseLatency(s string) (latencyRange, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return latencyRange{}, nil
	}
	lo, hi, found := strings.Cut(s, "-")
	minD, err := time.ParseDuration(strings.TrimSpace(lo))
	if err != nil {
		return latencyRange{}, fmt.Errorf("latency %q: %w", s, err)
	}
	maxD := minD
	if found {
		if maxD, err = time.ParseDuration(strings.TrimSpace(hi)); err != nil {
			return latencyRange{}, fmt.Errorf("latency %q: %w", s, err)
		}
	}
	if maxD < minD {
		minD, maxD = maxD, minD
	}
	return latencyRange{Min: minD, Max: maxD}, nil
}

func (l latencyRange) sample(r *rand.Rand) time.Duration {
	if l.Max <= l.Min {
		return l.Min
	}
	return l.Min + time.Duration(r.Int64N(int64(l.Max-l.Min)))
}

func (l latencyRange) String() string {
	if l.Max == l.Min {
		return l.Min.String()
	}
	return l.Min.String() + "-" + l.Max.String()
}

func (l latencyRange) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

func (l *latencyRange) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := parseLatency(s)
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}

// behavior is the runtime-mutable part of the configuration.
type behavior struct {
	ImageLatency latencyRange `json:"image_latency"`
	VideoLatency latencyRange `json:"video_latency"`
	TextLatency  latencyRange `json:"text_latency"`
	// ErrorRate is the probability that a call fails with a provider-side
	// error (5xx for HTTP-status providers, a business error code for
	// MiniMax's always-200 image endpoint, a failed task for video).
	ErrorRate float64 `json:"error_rate"`
	// RateLimitRate is the probability that a call is rejected as rate
	// limited (HTTP 429, or MiniMax status_code 1002 on images).
	RateLimitRate float64 `json:"rate_limit_rate"`
}

type config struct {
	Addr      string
	PublicURL string
	Seed      uint64

	mu       sync.RWMutex
	behavior behavior
}

func configFromEnv() *config {
	addr := getenv("FAKE_ADDR", "127.0.0.1:18090")
	c := &config{
		Addr:      addr,
		PublicURL: strings.TrimRight(getenv("FAKE_PUBLIC_URL", "http://"+addr), "/"),
		Seed:      uint64(getenvInt("FAKE_SEED", int(time.Now().UnixNano()&0x7fffffff))),
	}
	b := behavior{
		ImageLatency:  mustLatency(getenv("FAKE_IMAGE_LATENCY", "2s-4s")),
		VideoLatency:  mustLatency(getenv("FAKE_VIDEO_LATENCY", "20s-40s")),
		TextLatency:   mustLatency(getenv("FAKE_TEXT_LATENCY", "300ms-800ms")),
		ErrorRate:     getenvFloat("FAKE_ERROR_RATE", 0),
		RateLimitRate: getenvFloat("FAKE_RATE_LIMIT_RATE", 0),
	}
	c.behavior = b
	return c
}

func (c *config) get() behavior {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.behavior
}

func (c *config) set(b behavior) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.behavior = b
}

func mustLatency(s string) latencyRange {
	l, err := parseLatency(s)
	if err != nil {
		panic(err)
	}
	return l
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func getenvFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}
