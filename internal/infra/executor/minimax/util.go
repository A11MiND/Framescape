package minimax

import (
	"bytes"
	"image"
	_ "image/gif" // decoder registration for image.DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strconv"
	"strings"
)

// imageDimensions reads just enough of the file to get width/height without
// a full decode. Returns (0, 0) on any failure (e.g. webp — Go's stdlib
// doesn't decode it; not worth a dependency just for a display hint).
func imageDimensions(data []byte) (width, height int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func parseUserID(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func extForContentType(contentType string) string {
	ct := strings.ToLower(strings.SplitN(contentType, ";", 2)[0])
	switch ct {
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:
		return "jpg"
	}
}
