package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
)

// mediaStore keeps generated files in memory for GET /files/{name} and
// caches synthesized videos per (width, height, duration) since encoding is
// the expensive part.
type mediaStore struct {
	mu     sync.RWMutex
	files  map[string]mediaFile
	videos map[string][]byte
	tmpDir string
}

type mediaFile struct {
	data []byte
	mime string
}

func newMediaStore() *mediaStore {
	return &mediaStore{files: map[string]mediaFile{}, videos: map[string][]byte{}}
}

func (m *mediaStore) put(name string, data []byte, mime string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name] = mediaFile{data: data, mime: mime}
}

func (m *mediaStore) get(name string) ([]byte, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	f, ok := m.files[name]
	return f.data, f.mime, ok
}

func (m *mediaStore) cleanup() {
	if m.tmpDir != "" {
		_ = os.RemoveAll(m.tmpDir)
	}
}

// solidPNG renders a flat image whose color is derived from seed, so the
// same prompt always yields the same picture and different prompts are
// visually distinguishable in the UI.
func solidPNG(width, height int, seed string) ([]byte, error) {
	if width <= 0 || height <= 0 || width > 4096 || height > 4096 {
		return nil, fmt.Errorf("unsupported size %dx%d", width, height)
	}
	sum := sha256.Sum256([]byte(seed))
	base := color.RGBA{R: sum[0]/2 + 64, G: sum[1]/2 + 64, B: sum[2]/2 + 64, A: 255}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	band := height / 8
	for y := 0; y < height; y++ {
		c := base
		if band > 0 && (y/band)%2 == 1 {
			c = color.RGBA{R: base.R / 2, G: base.G / 2, B: base.B / 2, A: 255}
		}
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// video synthesizes an H.264 MP4 of the requested geometry and duration via
// ffmpeg. Frame sizes are deliberately small: downstream steps (ffprobe,
// frame extraction, concat) only need a valid, correctly-timed file.
func (m *mediaStore) video(width, height, seconds int) ([]byte, error) {
	key := strconv.Itoa(width) + "x" + strconv.Itoa(height) + "x" + strconv.Itoa(seconds)
	m.mu.RLock()
	cached, ok := m.videos[key]
	m.mu.RUnlock()
	if ok {
		return cached, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if cached, ok := m.videos[key]; ok {
		return cached, nil
	}
	if m.tmpDir == "" {
		dir, err := os.MkdirTemp("", "fakeprovider-")
		if err != nil {
			return nil, err
		}
		m.tmpDir = dir
	}
	out := filepath.Join(m.tmpDir, key+".mp4")
	// LGPL-only ffmpeg builds (the kind this project targets, see
	// local/ffmpeg_concat.go) ship libopenh264 but not libx264.
	var lastErr error
	for _, encoder := range []string{"libopenh264", "libx264", "mpeg4"} {
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=24:duration=%d", width, height, seconds),
			"-f", "lavfi", "-i", fmt.Sprintf("anullsrc=channel_layout=stereo:sample_rate=44100:duration=%d", seconds),
			"-shortest", "-c:v", encoder, "-pix_fmt", "yuv420p", "-c:a", "aac", "-movflags", "+faststart", out)
		msg, err := cmd.CombinedOutput()
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = fmt.Errorf("ffmpeg (%s): %v: %s", encoder, err, msg)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, err
	}
	m.videos[key] = data
	return data, nil
}

// videoGeometry maps a MiniMax resolution/ratio pair onto a small frame size
// with the same aspect ratio; 2K is rendered larger than 768P so tests can
// tell upgraded shots apart.
func videoGeometry(resolution, ratio string) (int, int) {
	scale := 144
	if resolution == "2K" {
		scale = 216
	}
	switch ratio {
	case "9:16":
		return scale * 9 / 16 &^ 1, scale
	case "1:1":
		return scale, scale
	case "4:3":
		return scale * 4 / 3 &^ 1, scale
	case "3:4":
		return scale * 3 / 4 &^ 1, scale
	case "21:9":
		return scale * 21 / 9 &^ 1, scale
	default:
		return scale * 16 / 9 &^ 1, scale
	}
}
