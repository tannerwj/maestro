package testutil

import (
	"os"
	"strings"
	"testing"
)

// RequireEnv skips the test unless all env vars are non-empty.
func RequireEnv(t *testing.T, names ...string) map[string]string {
	t.Helper()

	values := make(map[string]string, len(names))
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			t.Skipf("skipping live test; %s is not set", name)
		}
		values[name] = value
	}

	return values
}

// RequireFlag skips the test unless the env flag is set to "1".
func RequireFlag(t *testing.T, name string) {
	t.Helper()

	if strings.TrimSpace(os.Getenv(name)) != "1" {
		t.Skipf("skipping live test; %s=1 is required", name)
	}
}
