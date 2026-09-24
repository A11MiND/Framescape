package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleOpenAIImage serves both /images/generations (JSON) and
// /images/edits (multipart with image[] parts), returning b64_json images
// and token usage the way the Images API does.
func (s *server) handleOpenAIImage(w http.ResponseWriter, r *http.Request) outcome {
	var prompt, size string
	n, refs := 1, 0
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(128 << 20); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_multipart")
			return outcomeError
		}
		prompt, size = r.FormValue("prompt"), r.FormValue("size")
		if v, err := strconv.Atoi(r.FormValue("n")); err == nil {
			n = v
		}
		refs = len(r.MultipartForm.File["image[]"])
	} else {
		var req struct {
			Prompt string `json:"prompt"`
			N      int    `json:"n"`
			Size   string `json:"size"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_json")
			return outcomeError
		}
		prompt, size, n = req.Prompt, req.Size, req.N
	}
	if n <= 0 {
		n = 1
	}

	b := s.cfg.get()
	if !sleep(r, s.sample(b.ImageLatency)) {
		return outcomeOK
	}
	switch s.roll(b) {
	case outcomeRateLimited:
		writeOpenAIError(w, http.StatusTooManyRequests, "requests", "rate_limit_exceeded")
		return outcomeRateLimited
	case outcomeError:
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "server_error")
		return outcomeError
	}

	width, height := 512, 512
	if wStr, hStr, ok := strings.Cut(size, "x"); ok {
		if wv, err := strconv.Atoi(wStr); err == nil {
			if hv, err := strconv.Atoi(hStr); err == nil {
				width, height = wv/2, hv/2
			}
		}
	}
	data := make([]map[string]string, 0, n)
	for i := 0; i < n; i++ {
		img, err := solidPNG(width, height, prompt+"#openai#"+strconv.Itoa(i))
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_size")
			return outcomeError
		}
		data = append(data, map[string]string{"b64_json": base64.StdEncoding.EncodeToString(img)})
	}
	textTokens := len(prompt) / 4
	imageTokens := refs * 1000
	writeJSON(w, http.StatusOK, map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
		"usage": map[string]any{
			"input_tokens":         textTokens + imageTokens,
			"output_tokens":        n * 4000,
			"total_tokens":         textTokens + imageTokens + n*4000,
			"input_tokens_details": map[string]int{"text_tokens": textTokens, "image_tokens": imageTokens},
		},
	})
	return outcomeOK
}

func writeOpenAIError(w http.ResponseWriter, status int, typ, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": typ, "code": code, "message": code}})
}
