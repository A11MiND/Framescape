package local

import (
	"image"
	"image/color"
	"testing"
)

func TestLayoutRects_GridEqualAndUnknown_ReturnsNil(t *testing.T) {
	for _, id := range []string{"", "grid-equal", "some-made-up-layout"} {
		if rects := layoutRects(id, 4); rects != nil {
			t.Errorf("layoutRects(%q, 4) = %v, want nil (fall back to composeGrid)", id, rects)
		}
	}
}

func TestLayoutRects_UnknownLayoutButZeroTiles_ReturnsNil(t *testing.T) {
	if rects := layoutRects("feature-last", 0); rects != nil {
		t.Errorf("layoutRects with n=0 should return nil, got %v", rects)
	}
}

func TestStripRects_Vertical(t *testing.T) {
	rects := stripRects(4, true)
	if len(rects) != 4 {
		t.Fatalf("got %d rects, want 4", len(rects))
	}
	want := []layoutRect{
		{X: 0, Y: 0, W: 1, H: 0.25},
		{X: 0, Y: 0.25, W: 1, H: 0.25},
		{X: 0, Y: 0.5, W: 1, H: 0.25},
		{X: 0, Y: 0.75, W: 1, H: 0.25},
	}
	for i := range want {
		if rects[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, rects[i], want[i])
		}
	}
}

func TestStripRects_Horizontal(t *testing.T) {
	rects := stripRects(2, false)
	want := []layoutRect{
		{X: 0, Y: 0, W: 0.5, H: 1},
		{X: 0.5, Y: 0, W: 0.5, H: 1},
	}
	for i := range want {
		if rects[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, rects[i], want[i])
		}
	}
}

func TestFeatureRects_Last_FourPanels(t *testing.T) {
	rects := featureRects(4, true)
	if len(rects) != 4 {
		t.Fatalf("got %d rects, want 4", len(rects))
	}
	// Panels 0-2 share the top half evenly; panel 3 (last) takes the
	// full-width bottom half.
	for i := 0; i < 3; i++ {
		if rects[i].Y != 0 || rects[i].H != 0.5 {
			t.Errorf("panel %d should be in the top half, got %+v", i, rects[i])
		}
	}
	last := rects[3]
	if last.X != 0 || last.Y != 0.5 || last.W != 1 || last.H != 0.5 {
		t.Errorf("last panel should span the full-width bottom half, got %+v", last)
	}
}

func TestFeatureRects_First_FourPanels(t *testing.T) {
	rects := featureRects(4, false)
	first := rects[0]
	if first.X != 0 || first.Y != 0 || first.W != 1 || first.H != 0.5 {
		t.Errorf("first panel should span the full-width top half, got %+v", first)
	}
	for i := 1; i < 4; i++ {
		if rects[i].Y != 0.5 || rects[i].H != 0.5 {
			t.Errorf("panel %d should be in the bottom half, got %+v", i, rects[i])
		}
	}
}

func TestFeatureRects_SinglePanel_FullCanvas(t *testing.T) {
	rects := featureRects(1, true)
	if len(rects) != 1 || rects[0] != (layoutRect{X: 0, Y: 0, W: 1, H: 1}) {
		t.Errorf("got %+v, want a single full-canvas rect", rects)
	}
}

func solidImage(c color.Color, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// TestComposeCustomLayout_PlacesTilesInTheirOwnRects renders 4 solid-color
// tiles through a feature-last layout and samples one pixel from the middle
// of each expected region — a placement bug (wrong rect math, off-by-one in
// pixel scaling) would show up as the wrong color at that sample point.
func TestComposeCustomLayout_PlacesTilesInTheirOwnRects(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	green := color.RGBA{0, 255, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}
	yellow := color.RGBA{255, 255, 0, 255}
	tiles := []image.Image{
		solidImage(red, 100, 100),
		solidImage(green, 100, 100),
		solidImage(blue, 100, 100),
		solidImage(yellow, 100, 100),
	}
	rects := featureRects(4, true) // panel 3 (yellow) is the full-width bottom half
	canvas := composeCustomLayout(tiles, rects)

	sample := func(fx, fy float64) color.Color {
		x := int(fx * layoutCanvasSize)
		y := int(fy * layoutCanvasSize)
		return canvas.At(x, y)
	}
	checks := []struct {
		name   string
		fx, fy float64
		want   color.RGBA
	}{
		{"panel 0 (red, top-left third)", 0.1, 0.1, red},
		{"panel 1 (green, top-middle third)", 0.45, 0.1, green},
		{"panel 2 (blue, top-right third)", 0.9, 0.1, blue},
		{"panel 3 (yellow, full bottom half)", 0.5, 0.9, yellow},
	}
	for _, c := range checks {
		got := sample(c.fx, c.fy)
		r, g, b, a := got.RGBA()
		wr, wg, wb, wa := c.want.RGBA()
		if r != wr || g != wg || b != wb || a != wa {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
