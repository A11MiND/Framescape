package jobsvc

import "testing"

func TestNodeRetryable(t *testing.T) {
	cases := []struct {
		workflow, node, phase, code string
		want                        bool
	}{
		{"video.single", "gen", "Failed", "provider_busy", true},
		{"video.single", "gen", "Timeout", "timeout", true},
		{"video.single", "gen", "Failed", "provider_rejected", true},
		{"video.single", "gen", "Failed", "moderation", false},
		{"video.single", "gen", "Failed", "bad_params", false},
		{"video.single", "gen", "Succeeded", "", false},
		{"video.single", "enhance", "Failed", "provider_busy", false},
		{"image.comic4", "panel-4", "Failed", "provider_busy", false},
	}
	for _, c := range cases {
		if got := NodeRetryable(c.workflow, c.node, c.phase, c.code); got != c.want {
			t.Errorf("NodeRetryable(%s, %s, %s, %s) = %v, want %v", c.workflow, c.node, c.phase, c.code, got, c.want)
		}
	}
}
