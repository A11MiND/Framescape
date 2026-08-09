// ffmpeg_concat.go is local.ffmpeg.concat (DEV_PLAN.md §10): the final step
// of video.sequence, joining N shots (a mix of kept-768P / redone-768P /
// upgraded-2K segments, per the preview gate's decision) into one mp4 at a
// single uniform resolution — segments can't be stream-copied together
// as-is since an upgraded shot is a different resolution than its
// untouched neighbors, so this always re-encodes through a scale+concat
// filter graph.
package local

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/BabySid/aether/executor"
	"github.com/BabySid/aether/model"

	"aigc-platform/internal/infra/executor/assetstore"
)

// ConcatConfig's three (shot-index, asset-id) pairs come from three
// different sources with three different shapes (gate's direct pass-through
// for kept shots; a Loop `aggregate`'s parallel output arrays for redone/
// upgraded shots — Aether's aggregate mechanism produces one array per
// listed output field name, not an array of objects, so index and asset-id
// arrive as two same-length arrays rather than paired structs). Execute
// zips each pair back together itself.
type ConcatConfig struct {
	KeepShotIndex     []string `json:"keep-shot-index"`
	KeepAssetID       []string `json:"keep-asset-id"`
	RedoneShotIndex   []string `json:"redone-shot-index"`
	RedoneAssetID     []string `json:"redone-asset-id"`
	UpgradedShotIndex []string `json:"upgraded-shot-index"`
	UpgradedAssetID   []string `json:"upgraded-asset-id"`
	TotalShots        string   `json:"total-shots"`
	UserID            string   `json:"user-id"`
}

type ConcatPlugin struct {
	reader assetstore.Reader
	sink   assetstore.Sink
	http   *http.Client
}

func NewConcatPlugin(reader assetstore.Reader, sink assetstore.Sink) *ConcatPlugin {
	return &ConcatPlugin{reader: reader, sink: sink, http: &http.Client{Timeout: 60 * time.Second}}
}

func (p *ConcatPlugin) Type() string { return "local.ffmpeg.concat" }

func (p *ConcatPlugin) Schema() model.ExecutorSchema {
	return executor.SchemaOf[ConcatConfig, executor.DynamicOutputs](
		"local.ffmpeg.concat", "1.0", "Concatenate a video.sequence's shots (mixed 768P/2K) into one uniform-resolution mp4",
	)
}

func (p *ConcatPlugin) Execute(ctx context.Context, req *executor.ExecuteRequest) (*model.ExecOutputs, error) {
	var cfg ConcatConfig
	if err := executor.BindInputs(req.Inputs, &cfg); err != nil {
		return nil, fmt.Errorf("bind local.ffmpeg.concat inputs: %w", err)
	}

	total, err := strconv.Atoi(cfg.TotalShots)
	if err != nil || total <= 0 {
		return &model.ExecOutputs{Code: model.ExecCodeFailed, Message: fmt.Sprintf("bad total-shots %q", cfg.TotalShots)}, nil
	}

	byIndex := map[int]string{}
	zip := func(indexes, assetIDs []string) error {
		if len(indexes) != len(assetIDs) {
			return fmt.Errorf("mismatched shot-index/asset-id array lengths (%d vs %d)", len(indexes), len(assetIDs))
		}
		for i, idxStr := range indexes {
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return fmt.Errorf("bad shot-index %q: %w", idxStr, err)
			}
			byIndex[idx] = assetIDs[i]
		}
		return nil
	}
	for _, err := range []error{
		zip(cfg.KeepShotIndex, cfg.KeepAssetID),
		zip(cfg.RedoneShotIndex, cfg.RedoneAssetID),
		zip(cfg.UpgradedShotIndex, cfg.UpgradedAssetID),
	} {
		if err != nil {
			return &model.ExecOutputs{Code: model.ExecCodeFailed, Message: err.Error()}, nil
		}
	}

	orderedAssetIDs := make([]string, total)
	for i := 1; i <= total; i++ {
		assetID, ok := byIndex[i]
		if !ok {
			return &model.ExecOutputs{Code: model.ExecCodeFailed, Message: fmt.Sprintf("no asset resolved for shot %d of %d", i, total)}, nil
		}
		orderedAssetIDs[i-1] = assetID
	}

	tmpDir, err := os.MkdirTemp("", "concat-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPaths := make([]string, 0, total)
	durations := make([]float64, 0, total)
	maxW, maxH := 0, 0
	for i, assetID := range orderedAssetIDs {
		url, err := p.reader.PublicURL(ctx, assetID)
		if err != nil {
			return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("look up shot %d asset %s: %v", i+1, assetID, err)}, nil
		}
		path := fmt.Sprintf("%s/shot-%03d.mp4", tmpDir, i+1)
		if err := p.downloadTo(ctx, url, path); err != nil {
			return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("download shot %d: %v", i+1, err)}, nil
		}
		w, h, err := ffprobeDimensions(ctx, path)
		if err != nil {
			return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("probe shot %d: %v", i+1, err)}, nil
		}
		if w*h > maxW*maxH {
			maxW, maxH = w, h
		}
		dur, err := ffprobeDuration(ctx, path)
		if err != nil {
			return &model.ExecOutputs{Code: model.ExecCodeError, Message: fmt.Sprintf("probe duration shot %d: %v", i+1, err)}, nil
		}
		inputPaths = append(inputPaths, path)
		durations = append(durations, dur)
	}
	if maxW == 0 || maxH == 0 {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: "could not determine target resolution"}, nil
	}

	outPath := tmpDir + "/concat-output.mp4"
	if err := runConcatFilter(ctx, inputPaths, durations, outPath, maxW, maxH); err != nil {
		return &model.ExecOutputs{Code: model.ExecCodeError, Message: "ffmpeg concat: " + err.Error()}, nil
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read concat output: %w", err)
	}
	durationS, err := ffprobeDuration(ctx, outPath)
	if err != nil {
		durationS = 0 // non-fatal — the file itself is still valid
	}

	resolutionTag := "768P"
	if maxW >= 1920 || maxH >= 1920 {
		resolutionTag = "2K"
	}

	assetID, err := p.sink.MaterializeBytes(ctx, assetstore.NewAssetBytes{
		UserID:        parseUserID(cfg.UserID),
		Type:          "video",
		Source:        "derived",
		FromTaskRunID: req.TaskRunID,
		Body:          bytes.NewReader(data),
		SizeBytes:     int64(len(data)),
		Ext:           "mp4",
		Mime:          "video/mp4",
		Width:         maxW,
		Height:        maxH,
		DurationMs:    int(durationS * 1000),
		ResolutionTag: resolutionTag,
		Meta: map[string]any{
			"concat_from_shots": orderedAssetIDs,
			"shot_count":        total,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("materialize concatenated video: %w", err)
	}

	return executor.OutputFrom(struct {
		AssetID    string `json:"asset-id"`
		Resolution string `json:"resolution"`
	}{AssetID: assetID, Resolution: resolutionTag})
}

func (p *ConcatPlugin) downloadTo(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func ffprobeDimensions(ctx context.Context, path string) (width, height int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=s=x:p=0", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected ffprobe output %q", string(out))
	}
	width, err1 := strconv.Atoi(parts[0])
	height, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("parse ffprobe dimensions %q", string(out))
	}
	return width, height, nil
}

// crossfadeSeconds is PRD §5.4's "段间 0.2s 交叉淡化" — a fixed constant
// rather than a tunable parameter since the PRD only ever specifies this one
// value.
const crossfadeSeconds = 0.2

// runConcatFilter re-encodes every input to targetW x targetH (letterboxed,
// aspect preserved — segments won't all share an aspect ratio since t2va
// shots pick their own `ratio` while i2va/r2va shots are forced adaptive)
// and joins them via a chained xfade/acrossfade filter_complex graph (not
// the concat demuxer's stream-copy mode, since mixed 768P/2K segments can't
// be copy-concatenated, and not the plain `concat` filter either, since that
// only supports hard cuts). A single input skips the crossfade chain
// entirely — there's nothing to transition between.
func runConcatFilter(ctx context.Context, inputPaths []string, durations []float64, outPath string, targetW, targetH int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	args := make([]string, 0, len(inputPaths)*2+8)
	for _, p := range inputPaths {
		args = append(args, "-i", p)
	}

	var filter strings.Builder
	for i := range inputPaths {
		fmt.Fprintf(&filter, "[%d:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1[v%d];",
			i, targetW, targetH, targetW, targetH, i)
	}

	if len(inputPaths) == 1 {
		filter.WriteString("[v0]copy[outv];[0:a]acopy[outa]")
	} else {
		// Chained xfade: each transition's `offset` is where in the *running
		// chain so far* (not the original clip) the next clip starts
		// overlapping — chainDur tracks that running length, shrinking by
		// crossfadeSeconds after each transition since the overlap eats into
		// what would otherwise be the concatenated total duration. Every shot
		// is generated at 4-15s (§3.2), comfortably longer than a 0.2s
		// overlap, so no clamping against a too-short clip is needed here.
		chainDur := durations[0]
		prevV, prevA := "v0", "0:a"
		for i := 1; i < len(inputPaths); i++ {
			offset := chainDur - crossfadeSeconds
			outV := fmt.Sprintf("vx%d", i)
			outA := fmt.Sprintf("ax%d", i)
			fmt.Fprintf(&filter, "[%s][v%d]xfade=transition=fade:duration=%.3f:offset=%.3f[%s];",
				prevV, i, crossfadeSeconds, offset, outV)
			fmt.Fprintf(&filter, "[%s][%d:a]acrossfade=d=%.3f:c1=tri:c2=tri[%s];",
				prevA, i, crossfadeSeconds, outA)
			prevV, prevA = outV, outA
			chainDur = chainDur + durations[i] - crossfadeSeconds
		}
		fmt.Fprintf(&filter, "[%s]copy[outv];[%s]acopy[outa]", prevV, prevA)
	}

	// libopenh264, not libx264: this project's target ffmpeg builds (this
	// dev machine's, and any non-GPL Linux distro build) are commonly built
	// with --disable-gpl, which excludes libx264 (GPL-licensed). Confirmed
	// directly — the exact hang/silent-failure chain this caused is
	// documented on truncateOutput's doc comment. libopenh264 (Cisco's BSD/
	// MIT-licensed encoder) is available in both this dev build and typical
	// distro ffmpeg packages without needing a GPL build.
	args = append(args, "-filter_complex", filter.String(), "-map", "[outv]", "-map", "[outa]",
		"-c:v", "libopenh264", "-c:a", "aac", "-y", outPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncateOutput(out))
	}
	return nil
}

// truncateOutput keeps ffmpeg's raw stderr well under aether_task_runs.message's
// VARCHAR(1024) cap. This isn't cosmetic: exceeding it errors under MySQL's
// strict_trans_tables mode, and the engine's CompletionHandler callback has
// no error return (broker.CompletionHandler is fire-and-forget by design) —
// so a too-long message doesn't surface as an error anywhere, it just makes
// the UpdateTaskRun call silently fail and the task sits "Running" forever
// until the timeout watchdog kills it. Confirmed directly: ffmpeg's verbose
// startup banner (codec config, per-stream metadata — real MiniMax output
// files embed a sizeable AIGC provenance JSON blob per stream) blew past
// 1024 bytes on its own, well before this project's own "ffmpeg concat: "
// prefix and exec.ExitError text were added on top.
func truncateOutput(b []byte) string {
	const max = 500
	if len(b) > max {
		b = b[len(b)-max:]
	}
	return string(b)
}

var _ executor.Plugin = (*ConcatPlugin)(nil)
