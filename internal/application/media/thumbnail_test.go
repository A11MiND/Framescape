package media

import (
	"image"
	"testing"
)

func TestResizeKeepsAspectAndSmallImages(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{2048, 1024, 512, 256},
		{1024, 2048, 256, 512},
		{300, 200, 300, 200},
		{4000, 3, 512, 1},
	}
	for _, c := range cases {
		got := Resize(image.NewRGBA(image.Rect(0, 0, c.w, c.h)), ThumbEdge).Bounds()
		if got.Dx() != c.wantW || got.Dy() != c.wantH {
			t.Errorf("%dx%d -> %dx%d, want %dx%d", c.w, c.h, got.Dx(), got.Dy(), c.wantW, c.wantH)
		}
	}
}
