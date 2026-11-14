package token

import (
	"strings"
	"testing"
)

func TestNewAndMatches(t *testing.T) {
	tok, hash := New()
	if !strings.HasPrefix(tok, Prefix) || len(tok) < 40 || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("tok=%q hash=%q", tok, hash)
	}
	if !Matches(tok, hash) || Matches(tok+"x", hash) {
		t.Error("match mismatch")
	}
	if other, _ := New(); other == tok {
		t.Error("tokens must be random")
	}
}
