package comic

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func validDocument() Document {
	return Document{SchemaVersion: 1, Title: "港燈四格", Mode: "editable", PanelAssetIDs: []string{"", "", "", ""}, Layers: []Layer{{ID: "bubble-1", Kind: "bubble", X: .1, Y: .1, W: .3, H: .2, FontSize: 32, Text: "繁體中文、简体中文\n99.9999%", Color: "#172033", Fill: "#ffffff", Tail: "left"}}}
}
func TestDocumentValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*Document)
		valid bool
	}{
		{"valid", func(d *Document) {}, true},
		{"200k unicode", func(d *Document) { d.Background = strings.Repeat("智", 200000) }, true},
		{"too much background", func(d *Document) { d.Background = strings.Repeat("智", 200001) }, false},
		{"unknown schema", func(d *Document) { d.SchemaVersion = 2 }, false},
		{"outside canvas", func(d *Document) { d.Layers[0].X = .9 }, false},
		{"non finite", func(d *Document) { d.Layers[0].Y = math.NaN() }, false},
		{"duplicate layer", func(d *Document) { d.Layers = append(d.Layers, d.Layers[0]) }, false},
		{"injected color", func(d *Document) { d.Layers[0].Fill = "url(javascript:alert(1))" }, false},
		{"logo without asset", func(d *Document) { d.Layers[0].Kind = "logo" }, false},
		{"too many panels", func(d *Document) { d.PanelAssetIDs = append(d.PanelAssetIDs, "") }, false},
		{"invalid pending panel", func(d *Document) { d.Pending = &Pending{JobID: "a", Panel: 5} }, false},
		{"imported page, hidden layer", func(d *Document) { d.PageSource = "imported"; d.Layers[0].Hidden = true }, true},
		{"transparent bubble", func(d *Document) { opacity := 0.35; d.Layers[0].FillOpacity = &opacity }, true},
		{"opacity above one", func(d *Document) { opacity := 1.01; d.Layers[0].FillOpacity = &opacity }, false},
		{"unknown page source", func(d *Document) { d.PageSource = "gpt" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := validDocument()
			tc.edit(&d)
			if err := d.Validate(); (err == nil) != tc.valid {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}
func TestAssetIDsDeduplicated(t *testing.T) {
	d := validDocument()
	d.PageAssetID = "a"
	d.PanelAssetIDs[0] = "a"
	d.References = []Reference{{AssetID: "b"}}
	d.Layers[0].AssetID = "c"
	if ids := d.AssetIDs(); strings.Join(ids, ",") != "a,b,c" {
		t.Fatalf("IDs = %v", ids)
	}
}

func TestExtendedCanvasRoundTrip(t *testing.T) {
	d := validDocument()
	d.TopSpace = 300
	d.LeftSpace = 120
	d.RightSpace = 80
	d.BottomSpace = 90
	enabled := true
	d.AutoFit = &enabled
	d.ImageGap = 12
	d.PageWidth = 768
	d.PageHeight = 1024
	d.Layers = append(d.Layers, Layer{ID: "logo", Kind: "logo", Placement: "left", Rotation: 45, X: .2, Y: .2, W: .2, H: .3, AssetID: "brand", FontSize: 32, Color: "#172033", Fill: "#ffffff", Tail: "none"})
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var restored Document
	if err := json.Unmarshal(b, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
	if restored.LeftSpace != 120 || restored.RightSpace != 80 || restored.BottomSpace != 90 || restored.AutoFit == nil || !*restored.AutoFit || restored.ImageGap != 12 || restored.Layers[1].Placement != "left" || restored.TopSpace != 300 || restored.PageWidth != 768 || restored.PageHeight != 1024 || restored.Layers[1].Rotation != 45 {
		t.Fatalf("lost canvas metadata: %s", b)
	}
	for _, height := range []int{-1, 32769} {
		restored.TopSpace = height
		if restored.Validate() == nil {
			t.Fatalf("accepted top space %v", height)
		}
	}
	restored.TopSpace = 300
	d.PageWidth = 768
	d.PageHeight = 1024
	for _, rotation := range []float64{-181, 181, math.NaN(), math.Inf(1)} {
		restored.Layers[1].Rotation = rotation
		if restored.Validate() == nil {
			t.Fatalf("accepted rotation %v", rotation)
		}
	}
}
