package minimax

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestImageDimensions(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 12, 7))
	img.Set(0, 0, color.White)
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}

	w, h := imageDimensions(buf.Bytes())
	if w != 12 || h != 7 {
		t.Errorf("dimensions = %dx%d, want 12x7", w, h)
	}
}

func TestImageDimensionsGarbage(t *testing.T) {
	w, h := imageDimensions([]byte("not an image"))
	if w != 0 || h != 0 {
		t.Errorf("dimensions = %dx%d, want 0x0 for undecodable data", w, h)
	}
}

func TestParseUserID(t *testing.T) {
	if got := parseUserID("42"); got != 42 {
		t.Errorf("parseUserID(\"42\") = %d, want 42", got)
	}
	if got := parseUserID("not-a-number"); got != 0 {
		t.Errorf("parseUserID(garbage) = %d, want 0", got)
	}
	if got := parseUserID(""); got != 0 {
		t.Errorf("parseUserID(\"\") = %d, want 0", got)
	}
}

func TestExtForContentType(t *testing.T) {
	cases := map[string]string{
		"image/png":                 "png",
		"image/webp":                "webp",
		"image/gif":                 "gif",
		"image/jpeg":                "jpg",
		"image/jpeg; charset=UTF-8": "jpg",
		"application/octet-stream":  "jpg",
		"":                          "jpg",
	}
	for ct, want := range cases {
		if got := extForContentType(ct); got != want {
			t.Errorf("extForContentType(%q) = %q, want %q", ct, got, want)
		}
	}
}
