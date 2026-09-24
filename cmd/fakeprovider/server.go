package main

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type server struct {
	cfg   *config
	media *mediaStore

	rngMu sync.Mutex
	rng   *rand.Rand

	nextID atomic.Int64

	statsMu sync.Mutex
	stats   map[string]*endpointStats

	tasksMu sync.Mutex
	tasks   map[string]*asyncTask

	stopOnce sync.Once
	stop     chan struct{}
}

type endpointStats struct {
	Calls       int64 `json:"calls"`
	InFlight    int64 `json:"in_flight"`
	Errors      int64 `json:"errors"`
	RateLimited int64 `json:"rate_limited"`
}

func newServer(cfg *config) *server {
	return &server{
		cfg:   cfg,
		media: newMediaStore(),
		rng:   rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15)),
		stats: map[string]*endpointStats{},
		tasks: map[string]*asyncTask{},
		stop:  make(chan struct{}),
	}
}

func (s *server) close() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.media.cleanup()
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/image_generation", s.track("minimax.image_generation", s.handleMiniMaxImage))
	mux.HandleFunc("POST /v1/files/upload", s.track("minimax.files_upload", s.handleMiniMaxUpload))
	mux.HandleFunc("POST /v1/chat/completions", s.track("minimax.chat_completions", s.handleMiniMaxChat))
	mux.HandleFunc("POST /v2/video_generation", s.track("minimax.video_generation", s.handleMiniMaxVideoCreate))
	mux.HandleFunc("POST /v2/h3_context_ir", s.track("minimax.h3_context_ir", s.handleMiniMaxContextIRCreate))
	mux.HandleFunc("GET /v2/query/video_generation/{id}", s.track("minimax.query_task", s.handleMiniMaxTaskQuery))

	mux.HandleFunc("POST /v1/images/generations", s.track("openai.images_generations", s.handleOpenAIImage))
	mux.HandleFunc("POST /v1/images/edits", s.track("openai.images_edits", s.handleOpenAIImage))

	mux.HandleFunc("GET /files/{name}", s.handleFile)

	mux.HandleFunc("GET /_fake/stats", s.handleStats)
	mux.HandleFunc("POST /_fake/stats/reset", s.handleStatsReset)
	mux.HandleFunc("GET /_fake/config", s.handleGetConfig)
	mux.HandleFunc("POST /_fake/config", s.handleSetConfig)
	mux.HandleFunc("GET /_fake/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

// track counts calls and in-flight requests per logical endpoint. Handlers
// report provider-side failures through the returned outcome so stats
// separate real work from injected faults.
func (s *server) track(name string, h func(http.ResponseWriter, *http.Request) outcome) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := s.endpoint(name)
		s.statsMu.Lock()
		st.Calls++
		st.InFlight++
		s.statsMu.Unlock()

		result := h(w, r)

		s.statsMu.Lock()
		st.InFlight--
		switch result {
		case outcomeError:
			st.Errors++
		case outcomeRateLimited:
			st.RateLimited++
		}
		s.statsMu.Unlock()
	}
}

type outcome int

const (
	outcomeOK outcome = iota
	outcomeError
	outcomeRateLimited
)

func (s *server) endpoint(name string) *endpointStats {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	st, ok := s.stats[name]
	if !ok {
		st = &endpointStats{}
		s.stats[name] = st
	}
	return st
}

// roll decides whether this call is rate limited or fails, using the
// runtime behavior. Rate limiting is checked first so both probabilities
// stay independent of each other in expectation.
func (s *server) roll(b behavior) outcome {
	s.rngMu.Lock()
	defer s.rngMu.Unlock()
	if b.RateLimitRate > 0 && s.rng.Float64() < b.RateLimitRate {
		return outcomeRateLimited
	}
	if b.ErrorRate > 0 && s.rng.Float64() < b.ErrorRate {
		return outcomeError
	}
	return outcomeOK
}

func (s *server) sample(l latencyRange) time.Duration {
	s.rngMu.Lock()
	defer s.rngMu.Unlock()
	return l.sample(s.rng)
}

func (s *server) intn(n int) int {
	s.rngMu.Lock()
	defer s.rngMu.Unlock()
	return s.rng.IntN(n)
}

// sleep waits for d or until the client goes away, so a canceled upstream
// request frees the fake immediately just like a real provider connection.
func sleep(r *http.Request, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) handleStats(w http.ResponseWriter, _ *http.Request) {
	s.statsMu.Lock()
	out := make(map[string]endpointStats, len(s.stats))
	names := make([]string, 0, len(s.stats))
	for name, st := range s.stats {
		out[name] = *st
		names = append(names, name)
	}
	s.statsMu.Unlock()
	sort.Strings(names)

	s.tasksMu.Lock()
	pending := 0
	for _, t := range s.tasks {
		if !t.terminal() {
			pending++
		}
	}
	s.tasksMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"endpoints": out, "pending_tasks": pending})
}

func (s *server) handleStatsReset(w http.ResponseWriter, _ *http.Request) {
	s.statsMu.Lock()
	s.stats = map[string]*endpointStats{}
	s.statsMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.get())
}

func (s *server) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	b := s.cfg.get()
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.cfg.set(b)
	writeJSON(w, http.StatusOK, b)
}

func (s *server) handleFile(w http.ResponseWriter, r *http.Request) {
	data, mime, ok := s.media.get(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
}
