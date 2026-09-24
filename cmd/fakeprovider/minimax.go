package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type baseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

var okResp = baseResp{StatusCode: 0, StatusMsg: "success"}

// MiniMax image_generation reports business errors in base_resp with HTTP
// 200; 1002 is its rate-limit code.
func (s *server) handleMiniMaxImage(w http.ResponseWriter, r *http.Request) outcome {
	var req struct {
		Prompt      string `json:"prompt"`
		N           int    `json:"n"`
		AspectRatio string `json:"aspect_ratio"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"base_resp": baseResp{StatusCode: 2013, StatusMsg: "invalid params"}})
		return outcomeError
	}
	b := s.cfg.get()
	if !sleep(r, s.sample(b.ImageLatency)) {
		return outcomeOK
	}
	switch s.roll(b) {
	case outcomeRateLimited:
		writeJSON(w, http.StatusOK, map[string]any{"base_resp": baseResp{StatusCode: 1002, StatusMsg: "rate limit exceeded"}})
		return outcomeRateLimited
	case outcomeError:
		writeJSON(w, http.StatusOK, map[string]any{"base_resp": baseResp{StatusCode: 1000, StatusMsg: "unknown error"}})
		return outcomeError
	}

	n := req.N
	if n <= 0 {
		n = 1
	}
	width, height := req.Width, req.Height
	if width <= 0 || height <= 0 {
		width, height = imageGeometry(req.AspectRatio)
	}
	urls := make([]string, 0, n)
	for i := 0; i < n; i++ {
		data, err := solidPNG(width, height, req.Prompt+"#"+strconv.Itoa(i))
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"base_resp": baseResp{StatusCode: 2013, StatusMsg: err.Error()}})
			return outcomeError
		}
		name := fmt.Sprintf("img-%d.png", s.nextID.Add(1))
		s.media.put(name, data, "image/png")
		urls = append(urls, s.cfg.PublicURL+"/files/"+name)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        fmt.Sprintf("fake-%d", s.nextID.Add(1)),
		"data":      map[string]any{"image_urls": urls},
		"metadata":  map[string]string{"success_count": strconv.Itoa(n), "failed_count": "0"},
		"base_resp": okResp,
	})
	return outcomeOK
}

// imageGeometry uses half of MiniMax's nominal output sizes; callers only
// read the geometry back, they never depend on the exact pixel count.
func imageGeometry(ratio string) (int, int) {
	switch ratio {
	case "16:9":
		return 640, 360
	case "9:16":
		return 360, 640
	case "4:3":
		return 576, 432
	case "3:4":
		return 432, 576
	case "3:2":
		return 624, 416
	case "2:3":
		return 416, 624
	case "21:9":
		return 672, 288
	default:
		return 512, 512
	}
}

func (s *server) handleMiniMaxUpload(w http.ResponseWriter, r *http.Request) outcome {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"base_resp": baseResp{StatusCode: 2013, StatusMsg: "invalid multipart"}})
		return outcomeError
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"base_resp": baseResp{StatusCode: 2013, StatusMsg: "missing file"}})
		return outcomeError
	}
	defer file.Close()
	size, _ := io.Copy(io.Discard, file)
	writeJSON(w, http.StatusOK, map[string]any{
		"file": map[string]any{
			"file_id":  s.nextID.Add(1),
			"bytes":    size,
			"filename": header.Filename,
			"purpose":  r.FormValue("purpose"),
		},
		"base_resp": okResp,
	})
	return outcomeOK
}

var (
	panelCountPattern = regexp.MustCompile(`恰好\s*(\d+)\s*格|exactly\s+(\d+)\s+panels`)
	jsonPlanMarker    = "reference_strategy"
)

// handleMiniMaxChat answers the three prompt shapes the platform sends:
// the comic planner (expects one JSON object with a panels array), the
// story splitter (expects N numbered lines) and free-form rewrites.
func (s *server) handleMiniMaxChat(w http.ResponseWriter, r *http.Request) outcome {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"base_resp": baseResp{StatusCode: 2013, StatusMsg: "invalid params"}})
		return outcomeError
	}
	b := s.cfg.get()
	if !sleep(r, s.sample(b.TextLatency)) {
		return outcomeOK
	}
	switch s.roll(b) {
	case outcomeRateLimited:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"base_resp": baseResp{StatusCode: 1002, StatusMsg: "rate limit exceeded"}})
		return outcomeRateLimited
	case outcomeError:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"base_resp": baseResp{StatusCode: 1000, StatusMsg: "unknown error"}})
		return outcomeError
	}

	var text strings.Builder
	for _, m := range req.Messages {
		if str, ok := m.Content.(string); ok {
			text.WriteString(str)
			text.WriteByte('\n')
		}
	}
	prompt := text.String()
	count := 4
	if m := panelCountPattern.FindStringSubmatch(prompt); m != nil {
		for _, g := range m[1:] {
			if v, err := strconv.Atoi(g); err == nil && v > 0 && v <= 12 {
				count = v
			}
		}
	}

	var content string
	switch {
	case strings.Contains(prompt, jsonPlanMarker):
		panels := make([]map[string]string, count)
		for i := range panels {
			panels[i] = map[string]string{
				"scene": fmt.Sprintf("scene %d", i+1), "action": fmt.Sprintf("action %d", i+1),
				"expression": "calm", "details": "fake planner output", "dialogue": "", "character_slot": "",
			}
		}
		raw, _ := json.Marshal(map[string]any{"reference_strategy": "character", "layout_id": "grid-2x2", "style": "flat illustration", "panels": panels})
		content = string(raw)
	case strings.Contains(prompt, "格") || strings.Contains(strings.ToLower(prompt), "panel"):
		var lines bytes.Buffer
		for i := 1; i <= count; i++ {
			fmt.Fprintf(&lines, "%d. fake panel line %d\n", i, i)
		}
		content = lines.String()
	default:
		content = "fake rewritten prompt: " + strings.TrimSpace(lastLine(prompt))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": fmt.Sprintf("chat-%d", s.nextID.Add(1)),
		"choices": []map[string]any{{
			"message":       map[string]string{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage":     map[string]int{"prompt_tokens": len(prompt) / 3, "completion_tokens": len(content) / 3, "total_tokens": (len(prompt) + len(content)) / 3},
		"base_resp": okResp,
	})
	return outcomeOK
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// asyncTask models MiniMax's H3 task family (video and h3_context_ir share
// the query endpoint). Status is derived from wall-clock time so a crashed
// and restarted poller observes exactly what a real provider would.
type asyncTask struct {
	ID         string
	Kind       string // "video" | "context_ir"
	CreatedAt  time.Time
	ReadyAt    time.Time
	WillFail   bool
	Cancelled  bool
	Resolution string
	Ratio      string
	Duration   int
	Prompt     string
	Callback   string
}

func (t *asyncTask) status(now time.Time) string {
	switch {
	case t.Cancelled:
		return "cancelled"
	case now.Before(t.CreatedAt.Add(t.ReadyAt.Sub(t.CreatedAt) / 10)):
		return "queued"
	case now.Before(t.ReadyAt):
		return "running"
	case t.WillFail:
		return "failed"
	default:
		return "succeeded"
	}
}

func (t *asyncTask) terminal() bool {
	s := t.status(time.Now())
	return s == "succeeded" || s == "failed" || s == "cancelled"
}

type videoContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Video errors use real HTTP status codes, unlike image_generation.
func (s *server) handleMiniMaxVideoCreate(w http.ResponseWriter, r *http.Request) outcome {
	var req struct {
		Content     []videoContentItem `json:"content"`
		Resolution  string             `json:"resolution"`
		Duration    int                `json:"duration"`
		Ratio       string             `json:"ratio"`
		CallbackURL string             `json:"callback_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_params"}})
		return outcomeError
	}
	return s.createTask(w, "video", req.Content, req.Resolution, req.Ratio, req.Duration, req.CallbackURL)
}

func (s *server) handleMiniMaxContextIRCreate(w http.ResponseWriter, r *http.Request) outcome {
	var req struct {
		Content  []videoContentItem `json:"content"`
		Duration int                `json:"duration"`
		Ratio    string             `json:"ratio"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_params"}})
		return outcomeError
	}
	return s.createTask(w, "context_ir", req.Content, "", req.Ratio, req.Duration, "")
}

func (s *server) createTask(w http.ResponseWriter, kind string, content []videoContentItem, resolution, ratio string, duration int, callback string) outcome {
	b := s.cfg.get()
	switch s.roll(b) {
	case outcomeRateLimited:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]string{"code": "rate_limit_exceeded", "message": "rate limit exceeded"}})
		return outcomeRateLimited
	case outcomeError:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "internal_error", "message": "internal error"}})
		return outcomeError
	}
	if duration <= 0 {
		duration = 5
	}
	if resolution == "" {
		resolution = "768P"
	}
	var prompt strings.Builder
	for _, c := range content {
		if c.Type == "text" {
			prompt.WriteString(c.Text)
		}
	}
	latency := s.sample(b.VideoLatency)
	if kind == "context_ir" {
		latency = s.sample(b.TextLatency) * 4
	}
	now := time.Now()
	t := &asyncTask{
		ID:         fmt.Sprintf("fv-%d", s.nextID.Add(1)),
		Kind:       kind,
		CreatedAt:  now,
		ReadyAt:    now.Add(latency),
		WillFail:   s.roll(behavior{ErrorRate: b.ErrorRate}) == outcomeError,
		Resolution: resolution,
		Ratio:      ratio,
		Duration:   duration,
		Prompt:     prompt.String(),
		Callback:   callback,
	}
	s.tasksMu.Lock()
	s.tasks[t.ID] = t
	s.tasksMu.Unlock()
	if callback != "" {
		go s.fireCallback(t)
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_id": t.ID, "base_resp": okResp})
	return outcomeOK
}

func (s *server) fireCallback(t *asyncTask) {
	timer := time.NewTimer(time.Until(t.ReadyAt))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-s.stop:
		return
	}
	s.tasksMu.Lock()
	status := t.status(time.Now())
	s.tasksMu.Unlock()
	body, _ := json.Marshal(map[string]any{"task": map[string]string{"id": t.ID, "status": status}})
	resp, err := http.Post(t.Callback, "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}

func (s *server) handleMiniMaxTaskQuery(w http.ResponseWriter, r *http.Request) outcome {
	s.tasksMu.Lock()
	t, ok := s.tasks[r.PathValue("id")]
	s.tasksMu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "task_not_found"}})
		return outcomeError
	}
	status := t.status(time.Now())
	task := map[string]any{"id": t.ID, "model": "fake", "status": status, "resolution": t.Resolution, "duration": t.Duration, "ratio": t.Ratio}
	switch status {
	case "succeeded":
		if t.Kind == "context_ir" {
			task["content"] = map[string]string{"prompt": "enhanced: " + t.Prompt}
			task["usage"] = map[string]int{"prompt_tokens": 200, "completion_tokens": 400, "total_tokens": 600}
			break
		}
		name := t.ID + ".mp4"
		if _, _, ok := s.media.get(name); !ok {
			width, height := videoGeometry(t.Resolution, t.Ratio)
			data, err := s.media.video(width, height, t.Duration)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "fake_render_failed", "message": err.Error()}})
				return outcomeError
			}
			s.media.put(name, data, "video/mp4")
		}
		task["content"] = map[string]string{"url": s.cfg.PublicURL + "/files/" + name}
		task["usage"] = map[string]int{"total_seconds": t.Duration, "output_seconds": t.Duration}
	case "failed":
		task["error"] = map[string]string{"code": "generation_failed", "message": "fake provider failure"}
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
	return outcomeOK
}
