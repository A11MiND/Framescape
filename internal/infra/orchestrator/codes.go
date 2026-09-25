package orchestrator

import "strings"

// Failure codes stored in job_nodes.error_code and jobs.error_code. Clients
// localize by code; the stored message is English detail.
const (
	CodeCancelled           = "cancelled"
	CodeTimeout             = "timeout"
	CodeWorkerLost          = "worker_lost"
	CodeDeadlineExceeded    = "deadline_exceeded"
	CodeGateExpired         = "gate_expired"
	CodeModeration          = "moderation"
	CodeBadParams           = "bad_params"
	CodeProviderRejected    = "provider_rejected"
	CodeProviderBusy        = "provider_busy"
	CodeProviderAccount     = "provider_account"
	CodeIncompleteOutput    = "incomplete_output"
	CodePollFailed          = "poll_failed"
	CodeExecutorError       = "executor_error"
	CodeExecutorUnavailable = "executor_unavailable"
	CodeInputUnresolved     = "input_unresolved"
	CodeProviderRefInvalid  = "provider_ref_invalid"
	CodeUnexpectedResult    = "unexpected_result"
)

// FailureCodes documents every failure code for clients (cli error-codes).
var FailureCodes = map[string]string{
	CodeCancelled:           "cancelled by the user",
	CodeTimeout:             "the step exceeded its time limit",
	CodeWorkerLost:          "the worker running the step stopped; retried while attempts remain",
	CodeDeadlineExceeded:    "the job exceeded its time limit",
	CodeGateExpired:         "the preview was not confirmed in time",
	CodeModeration:          "the provider blocked the content; the message may quote it and must not be shown",
	CodeBadParams:           "the provider rejected the parameters",
	CodeProviderRejected:    "the provider rejected the request",
	CodeProviderBusy:        "the provider was over capacity after all retries",
	CodeProviderAccount:     "the platform's provider account needs attention (balance or credentials); not the user's fault",
	CodeIncompleteOutput:    "fewer results than requested",
	CodePollFailed:          "the remote task status could not be read",
	CodeExecutorError:       "the step failed unexpectedly",
	CodeExecutorUnavailable: "no worker can run this step",
	CodeInputUnresolved:     "an upstream result the step needs is missing",
	CodeProviderRefInvalid:  "the remote task reference was lost",
	CodeUnexpectedResult:    "the step returned an unknown result",
}

// inputChangeCodes are failures that repeat for the same request, so they
// are fixed by changing the request rather than by retrying it.
var inputChangeCodes = map[string]bool{CodeModeration: true, CodeBadParams: true}

// NeedsInputChange reports whether a failure with this code can only be
// resolved by changing the request.
func NeedsInputChange(code string) bool { return inputChangeCodes[code] }

// failurePrefixes maps the executors' message prefixes to codes.
var failurePrefixes = []struct{ prefix, code string }{
	{"sensitive_content:", CodeModeration},
	{"insufficient_balance:", CodeProviderAccount},
	{"auth_error:", CodeProviderAccount},
	{"rate_limited:", CodeProviderBusy},
	{"concurrency_limited:", CodeProviderBusy},
	{"no_capacity:", CodeProviderBusy},
	{"bad_params:", CodeBadParams},
	{"mutual_exclusion:", CodeBadParams},
	{"wait_timeout:", CodeTimeout},
}

// failureCode classifies an executor's failure message, falling back to
// fallback when no prefix matches.
func failureCode(message, fallback string) string {
	for _, p := range failurePrefixes {
		if strings.HasPrefix(message, p.prefix) {
			return p.code
		}
	}
	return fallback
}
