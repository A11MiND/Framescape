package orchestrator

import "testing"

func TestFailureCodes(t *testing.T) {
	for msg, want := range map[string]string{
		"sensitive_content: flagged":      CodeModeration,
		"insufficient_balance: account":   CodeProviderAccount,
		"rate_limited: 429":               CodeProviderBusy,
		"bad_params: ratio":               CodeBadParams,
		"wait_timeout: poll":              CodeTimeout,
		"something else entirely":         CodeProviderRejected,
		"mutual_exclusion: frames + refs": CodeBadParams,
	} {
		if got := failureCode(msg, CodeProviderRejected); got != want {
			t.Errorf("%q -> %s, want %s", msg, got, want)
		}
	}
	for _, p := range failurePrefixes {
		if _, ok := FailureCodes[p.code]; !ok {
			t.Errorf("%s is not documented", p.code)
		}
	}
}
