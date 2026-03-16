package redact

import (
	"strings"
	"testing"
)

func TestStringRedactsKnownSecretShapes(t *testing.T) {
	raw := "clone https://oauth2:secret123@example.com/repo.git Authorization: Basic abc123 PRIVATE-TOKEN: xyz glpat-abcdef lin_api_token123 ?token=something"
	got := String(raw)

	for _, fragment := range []string{"secret123", "abc123", "xyz", "glpat-abcdef", "lin_api_token123", "something"} {
		if strings.Contains(got, fragment) {
			t.Fatalf("redaction leaked %q in %q", fragment, got)
		}
	}
}
