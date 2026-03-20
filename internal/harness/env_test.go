package harness

import (
	"strings"
	"testing"
)

func TestMergeEnvIncludesProcessEnvAndOverrides(t *testing.T) {
	t.Setenv("MAESTRO_BASE_ENV", "base")

	env := MergeEnv(map[string]string{
		"MAESTRO_EXTRA_ENV": "extra",
		"MAESTRO_BASE_ENV":  "override",
	})

	if !containsEnv(env, "MAESTRO_EXTRA_ENV=extra") {
		t.Fatalf("env = %v, want MAESTRO_EXTRA_ENV=extra", env)
	}
	if !containsEnv(env, "MAESTRO_BASE_ENV=base") {
		t.Fatalf("env = %v, want inherited MAESTRO_BASE_ENV=base", env)
	}
	if !containsEnv(env, "MAESTRO_BASE_ENV=override") {
		t.Fatalf("env = %v, want override MAESTRO_BASE_ENV=override", env)
	}
}

func containsEnv(env []string, want string) bool {
	for _, entry := range env {
		if strings.TrimSpace(entry) == want {
			return true
		}
	}
	return false
}
