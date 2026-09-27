// Package comic owns the versioned, resolution-independent editable page format.
package comic

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

type Reference struct {
	AssetID string `json:"asset_id"`
	Label   string `json:"label"`
}
type Layer struct {
	FillOpacity *float64 `json:"fill_opacity,omitempty"` // nil preserves opaque legacy documents
	ID          string   `json:"id"`
	Kind        string   `json:"kind"` // bubble | text | logo
	X           float64  `json:"x"`
	Y           float64  `json:"y"`
	W           float64  `json:"w"`
	H           float64  `json:"h"`
	Text        string   `json:"text"`
	AssetID     string   `json:"asset_id,omitempty"`
	FontSize    int      `json:"font_size"` // pixels in the canonical 1536x1024 canvas
	Color       string   `json:"color"`
	Fill        string   `json:"fill"`
	Tail        string   `json:"tail"` // none | left | right
	Locked      bool     `json:"locked"`
	// Hidden layers stay in the document but are neither shown nor exported.
	Hidden bool `json:"hidden,omitempty"`
}
type Pending struct {
	JobID string `json:"job_id"`
	Panel int    `json:"panel"`
}
type Document struct {
	SchemaVersion int         `json:"schema_version"`
	Title         string      `json:"title"`
	Mode          string      `json:"mode"`
	Brief         string      `json:"brief"`
	Background    string      `json:"background"`
	Context       string      `json:"context"`
	References    []Reference `json:"references"`
	PageAssetID   string      `json:"page_asset_id"`
	PanelAssetIDs []string    `json:"panel_asset_ids"`
	// PageSource says where the page came from: generated or imported by the
	// user; empty in documents saved before it existed.
	PageSource string   `json:"page_source,omitempty"`
	Layers     []Layer  `json:"layers"` // array order is z-order; IDs are stable
	Pending    *Pending `json:"pending,omitempty"`
}

// MaxReferences is the user-attached character/style reference cap. OpenAI's
// edits endpoint takes at most 16 images; a single-panel redraw also sends
// the current page as one more reference, so users get 16-1.
const MaxReferences = 15

// Text limits, in characters (runes). The generation text is the brief plus
// one label line per reference, so a client must budget the brief against
// MaxComposedChars minus its labels rather than against MaxBriefChars.
const (
	MaxTitleChars          = 128
	MaxBriefChars          = 20000  // stored in the document
	MaxComposedChars       = 20000  // brief plus reference labels, sent to generation
	MaxReferenceLabelChars = 200    // one reference's usage note
	MaxContextChars        = 8000   // reviewed source excerpts sent along
	MaxBackgroundChars     = 200000 // raw imported source kept in the document
	MaxCompiledChars       = 32000  // final prompt after appending contracts
	MaxLayers              = 64
)

// ImageMime is the set of image types the comic editor stores and sends to
// OpenAI (its edits endpoint accepts PNG, JPEG and WebP).
// ReferenceMimes are the image types accepted as OpenAI references.
var ReferenceMimes = []string{"image/png", "image/jpeg", "image/webp"}

func ImageMime(mime string) bool { return slices.Contains(ReferenceMimes, mime) }

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (d Document) Validate() error {
	if d.SchemaVersion != 1 {
		return fmt.Errorf("unsupported comic document version")
	}
	if strings.TrimSpace(d.Title) == "" || utf8.RuneCountInString(d.Title) > MaxTitleChars {
		return fmt.Errorf("title must contain 1..128 characters")
	}
	if d.Mode != "direct" && d.Mode != "editable" {
		return fmt.Errorf("invalid comic mode")
	}
	if d.PageSource != "" && d.PageSource != "generated" && d.PageSource != "imported" {
		return fmt.Errorf("invalid page source")
	}
	if utf8.RuneCountInString(d.Brief) > MaxBriefChars || utf8.RuneCountInString(d.Background) > MaxBackgroundChars || utf8.RuneCountInString(d.Context) > MaxContextChars {
		return fmt.Errorf("comic source or brief exceeds the character limit")
	}
	if len(d.References) > MaxReferences || len(d.PanelAssetIDs) != 4 || len(d.Layers) > MaxLayers {
		return fmt.Errorf("invalid reference, panel or layer count")
	}
	for _, ref := range d.References {
		if ref.AssetID == "" || utf8.RuneCountInString(ref.Label) > 200 {
			return fmt.Errorf("invalid reference")
		}
	}
	if d.Pending != nil && (d.Pending.JobID == "" || d.Pending.Panel < 0 || d.Pending.Panel > 4) {
		return fmt.Errorf("invalid pending generation")
	}
	ids := map[string]bool{}
	for _, l := range d.Layers {
		if l.ID == "" || len(l.ID) > 80 || ids[l.ID] {
			return fmt.Errorf("duplicate or invalid layer ID")
		}
		ids[l.ID] = true
		if l.Kind != "bubble" && l.Kind != "text" && l.Kind != "logo" {
			return fmt.Errorf("unknown layer kind")
		}
		for _, v := range []float64{l.X, l.Y, l.W, l.H} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
				return fmt.Errorf("invalid layer geometry")
			}
		}
		if l.W < 0.02 || l.H < 0.02 || l.X+l.W > 1.000001 || l.Y+l.H > 1.000001 {
			return fmt.Errorf("layer extends outside canvas")
		}
		if l.FontSize < 12 || l.FontSize > 96 || !hexColor.MatchString(l.Color) || !hexColor.MatchString(l.Fill) || utf8.RuneCountInString(l.Text) > 2000 {
			return fmt.Errorf("invalid layer text/style")
		}
		if l.FillOpacity != nil && (math.IsNaN(*l.FillOpacity) || math.IsInf(*l.FillOpacity, 0) || *l.FillOpacity < 0 || *l.FillOpacity > 1) {
			return fmt.Errorf("invalid bubble opacity")
		}
		if l.Tail != "none" && l.Tail != "left" && l.Tail != "right" {
			return fmt.Errorf("invalid bubble tail")
		}
		if l.Kind == "logo" && l.AssetID == "" {
			return fmt.Errorf("logo requires an asset")
		}
	}
	return nil
}
func (d Document) AssetIDs() []string {
	seen := map[string]bool{}
	ids := []string{}
	add := func(id string) {
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	add(d.PageAssetID)
	for _, id := range d.PanelAssetIDs {
		add(id)
	}
	for _, r := range d.References {
		add(r.AssetID)
	}
	for _, l := range d.Layers {
		add(l.AssetID)
	}
	return ids
}
