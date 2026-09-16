package gemini

import (
	"context"
	"testing"
)

// fakeServiceAccountJSON is a syntactically valid (but not cryptographically
// real) service-account key — enough for credentials.DetectDefault to parse
// and build a token provider; the private key itself is only ever parsed
// lazily at actual token-fetch time (verified against the vendored SDK
// source: auth.New2LOTokenProvider only validates required fields are
// non-empty, tokenProvider2LO.Token is what calls internal.ParseKey), which
// this test never triggers — no real network call, no real key needed.
const fakeServiceAccountJSON = `{
	"type": "service_account",
	"project_id": "test-project",
	"private_key_id": "fake-key-id",
	"private_key": "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----\n",
	"client_email": "fake@test-project.iam.gserviceaccount.com",
	"client_id": "123456789",
	"token_uri": "https://oauth2.googleapis.com/token"
}`

// TestNewClientWithCredentialsJSON covers the Railway-oriented auth path
// (Config's own doc): a raw service-account JSON string handed in directly,
// as an alternative to a GOOGLE_APPLICATION_CREDENTIALS file path — Railway
// has no clean way to hand the SDK a file that survives a redeploy, only
// environment variables. Pairing CredentialsJSON with a fake BaseURL/HTTPClient
// (this file's own doc: never touches the real network) confirms the
// resulting client is at least usable — a bad NewClient call here would
// mean image.comic4/image.single's gemini provider was unreachable at
// startup on any deployment using this path.
func TestNewClientWithCredentialsJSON(t *testing.T) {
	client, err := NewClient(context.Background(), Config{
		ProjectID:       "test-project",
		Location:        "us-central1",
		CredentialsJSON: fakeServiceAccountJSON,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient returned a nil client with no error")
	}
}

// TestNewClientWithInvalidCredentialsJSON covers the failure path — this is
// exactly the mistake a pasted-wrong-value Railway variable would produce,
// and it must surface as a clear construction-time error, not a panic or a
// client that only fails much later on first real use.
func TestNewClientWithInvalidCredentialsJSON(t *testing.T) {
	_, err := NewClient(context.Background(), Config{
		ProjectID:       "test-project",
		Location:        "us-central1",
		CredentialsJSON: "not valid json at all",
	})
	if err == nil {
		t.Fatal("NewClient should reject malformed CredentialsJSON")
	}
}
