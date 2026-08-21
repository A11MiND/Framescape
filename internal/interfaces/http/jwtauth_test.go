package httpapi

import (
	"testing"
	"time"
)

func TestSignAndParseTokenRoundTrip(t *testing.T) {
	raw, err := signToken("secret-a", 42, tokenAccess, time.Hour)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}
	cl, err := parseToken("secret-a", raw, tokenAccess)
	if err != nil {
		t.Fatalf("parseToken: %v", err)
	}
	if cl.UserID != 42 {
		t.Errorf("UserID = %d, want 42", cl.UserID)
	}
	if cl.Type != tokenAccess {
		t.Errorf("Type = %q, want %q", cl.Type, tokenAccess)
	}
}

// TestParseTokenWrongSecret is the whole point of signing tokens at all —
// a token minted with one secret must never validate against another.
func TestParseTokenWrongSecret(t *testing.T) {
	raw, err := signToken("secret-a", 1, tokenAccess, time.Hour)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}
	if _, err := parseToken("secret-b", raw, tokenAccess); err == nil {
		t.Fatal("expected parseToken to reject a token signed with a different secret")
	}
}

// TestParseTokenWrongType is what stops a leaked/expired refresh token from
// being replayed as an access token (and vice versa) — see jwtauth.go's own
// doc on tokenType.
func TestParseTokenWrongType(t *testing.T) {
	refresh, err := signToken("secret-a", 1, tokenRefresh, time.Hour)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}
	if _, err := parseToken("secret-a", refresh, tokenAccess); err == nil {
		t.Fatal("expected parseToken to reject a refresh token presented as an access token")
	}
}

func TestParseTokenExpired(t *testing.T) {
	raw, err := signToken("secret-a", 1, tokenAccess, -time.Minute)
	if err != nil {
		t.Fatalf("signToken: %v", err)
	}
	if _, err := parseToken("secret-a", raw, tokenAccess); err == nil {
		t.Fatal("expected parseToken to reject an already-expired token")
	}
}

func TestParseTokenGarbage(t *testing.T) {
	if _, err := parseToken("secret-a", "not-a-jwt", tokenAccess); err == nil {
		t.Fatal("expected parseToken to reject a non-JWT string")
	}
}
