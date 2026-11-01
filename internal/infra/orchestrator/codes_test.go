package orchestrator

import "testing"

func TestFailureCodes(t *testing.T) {
	for msg, want := range map[string]string{
		"sensitive_content: flagged":      CodeModeration,
		"insufficient_balance: account":   CodeProviderBalance,
		"auth_error: key":                 CodeProviderAccount,
		"account_config: model":           CodeProviderAccount,
		"spend_limit: org":                CodeProviderLimit,
		"throttled: 429":                  CodeProviderThrottled,
		"provider_unavailable: 503":       CodeProviderUnavailable,
		"invalid_request: 400":            CodeProviderRejected,
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
