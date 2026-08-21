package minimax

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestParsePanelsHappyPath(t *testing.T) {
	got := parsePanels("A cat sits.\nA dog barks.\nA bird flies.", "fallback", 3)
	want := []string{"A cat sits.", "A dog barks.", "A bird flies."}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestParsePanelsStripsMarkers covers the model adding numbering/bullets
// despite being asked not to — parsePanels' whole reason for existing
// rather than a plain strings.Split.
func TestParsePanelsStripsMarkers(t *testing.T) {
	got := parsePanels("1. A cat sits.\n2、A dog barks.\n- A bird flies.\n3) One more.", "fallback", 4)
	want := []string{"A cat sits.", "A dog barks.", "A bird flies.", "One more."}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParsePanelsPadsShortfall(t *testing.T) {
	got := parsePanels("Only one line.", "the original story", 3)
	if len(got) != 3 {
		t.Fatalf("got %d panels, want exactly 3", len(got))
	}
	if got[0] != "Only one line." {
		t.Errorf("[0] = %q, want the real line", got[0])
	}
	if got[1] != "the original story" || got[2] != "the original story" {
		t.Errorf("got = %v, want the shortfall padded with the fallback story", got)
	}
}

func TestParsePanelsTruncatesExcess(t *testing.T) {
	got := parsePanels("one\ntwo\nthree\nfour\nfive", "fallback", 2)
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("got = %v, want exactly the first 2 lines", got)
	}
}

func TestParsePanelsSkipsBlankLines(t *testing.T) {
	got := parsePanels("one\n\n\ntwo", "fallback", 2)
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("got = %v, want blank lines skipped rather than counted", got)
	}
}

// TestParsePanelsStripsThinkTag covers the ThinkingConfig{Type:"disabled"}
// backstop — a truncated or leaked reasoning preamble must never leak into
// the parsed panels.
func TestParsePanelsStripsThinkTag(t *testing.T) {
	got := parsePanels("<think>reasoning about the scene</think>A cat sits.\nA dog barks.", "fallback", 2)
	if got[0] != "A cat sits." {
		t.Errorf("[0] = %q, want the think block stripped", got[0])
	}
	if got[1] != "A dog barks." {
		t.Errorf("[1] = %q, want A dog barks.", got[1])
	}
}

func TestParsePanelsUnclosedThinkTag(t *testing.T) {
	got := parsePanels("<think>reasoning that never closes because max tokens hit", "fallback story", 1)
	if got[0] != "fallback story" {
		t.Errorf("got = %v, want the fallback (everything after the unclosed <think> is discarded)", got)
	}
}

func TestSplitStory(t *testing.T) {
	var gotMaxTokens int
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		var body ChatCompletionRequest
		_ = decodeJSON(req, &body)
		gotMaxTokens = body.MaxCompletionTokens
		w.Write([]byte(`{"id":"c-1","choices":[{"message":{"content":"panel one\npanel two\npanel three"}}],"usage":{"prompt_tokens":100,"completion_tokens":50}}`))
	})
	defer closeFn()

	panels, cost, err := SplitStory(context.Background(), client, "a story about a cat", 3)
	if err != nil {
		t.Fatalf("SplitStory: %v", err)
	}
	if len(panels) != 3 {
		t.Fatalf("panels = %v, want exactly 3", panels)
	}
	if gotMaxTokens != 200*3 {
		t.Errorf("max_completion_tokens = %d, want %d (200*count)", gotMaxTokens, 200*3)
	}
	wantCost := 100.0/1_000_000*textInputYuanPerM + 50.0/1_000_000*textOutputYuanPerM
	if cost != wantCost {
		t.Errorf("cost = %v, want %v", cost, wantCost)
	}
}

func TestSplitStoryDefaultsCountToFour(t *testing.T) {
	var gotMaxTokens int
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		var body ChatCompletionRequest
		_ = decodeJSON(req, &body)
		gotMaxTokens = body.MaxCompletionTokens
		w.Write([]byte(`{"id":"c-1","choices":[{"message":{"content":"a\nb\nc\nd"}}]}`))
	})
	defer closeFn()

	panels, _, err := SplitStory(context.Background(), client, "a story", 0)
	if err != nil {
		t.Fatalf("SplitStory: %v", err)
	}
	if len(panels) != 4 {
		t.Errorf("panels = %v, want 4 (count<1 defaults to 4)", panels)
	}
	if gotMaxTokens != 200*4 {
		t.Errorf("max_completion_tokens = %d, want %d", gotMaxTokens, 200*4)
	}
}

func TestSplitStoryTruncatesLongStory(t *testing.T) {
	var gotInstruction string
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		var body ChatCompletionRequest
		_ = decodeJSON(req, &body)
		gotInstruction, _ = body.Messages[0].Content.(string)
		w.Write([]byte(`{"id":"c-1","choices":[{"message":{"content":"a\nb"}}]}`))
	})
	defer closeFn()

	longStory := strings.Repeat("x", 3000)
	if _, _, err := SplitStory(context.Background(), client, longStory, 2); err != nil {
		t.Fatalf("SplitStory: %v", err)
	}
	idx := strings.Index(gotInstruction, "剧情：")
	if idx == -1 {
		t.Fatalf("instruction never carried the expected marker, got: %q", gotInstruction)
	}
	sentStory := gotInstruction[idx+len("剧情："):]
	if got := len([]rune(sentStory)); got != 2000 {
		t.Errorf("story sent upstream = %d runes, want exactly 2000 (the truncation cap)", got)
	}
}

func TestSplitStoryNoChoices(t *testing.T) {
	client, closeFn := jsonServer(t, func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(`{"id":"c-1","choices":[]}`))
	})
	defer closeFn()

	panels, cost, err := SplitStory(context.Background(), client, "a story", 3)
	if err != nil {
		t.Fatalf("SplitStory: %v", err)
	}
	if panels != nil {
		t.Errorf("panels = %v, want nil (not an error) when the API returns zero choices", panels)
	}
	if cost != 0 {
		t.Errorf("cost = %v, want 0", cost)
	}
}
