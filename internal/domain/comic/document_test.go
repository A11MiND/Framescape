package comic

import (
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
