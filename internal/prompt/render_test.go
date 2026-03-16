package prompt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/prompt"
)

func TestRenderFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "prompt.md")
	if err := os.WriteFile(path, []byte("Hello {{.AgentName}} from {{.Issue.Identifier}}"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	rendered, err := prompt.RenderFile(path, prompt.Data{
		Issue:     struct{ Identifier string }{Identifier: "team/project#42"},
		AgentName: "coder",
	})
	if err != nil {
		t.Fatalf("render prompt: %v", err)
	}

	if !strings.Contains(rendered, "Hello coder from team/project#42") {
		t.Fatalf("unexpected prompt output: %q", rendered)
	}
	if !strings.Contains(rendered, "Never print, paste, log, summarize, or quote secrets.") {
		t.Fatalf("missing global system preamble: %q", rendered)
	}
	if !strings.HasPrefix(rendered, "## System") {
		t.Fatalf("prompt should start with system preamble: %q", rendered)
	}
}

func TestCodePRPromptIncludesIssueDescription(t *testing.T) {
	path := filepath.Join("..", "..", "agents", "code-pr", "prompt.md")
	rendered, err := prompt.RenderFile(path, prompt.Data{
		Issue: struct {
			Identifier  string
			Title       string
			URL         string
			Description string
		}{
			Identifier:  "team/project#42",
			Title:       "Fix the demo walkthrough",
			URL:         "https://example.com/issues/42",
			Description: "Create the expected demo artifact and exit cleanly.",
		},
		User: struct{ Name string }{Name: "Demo User"},
		Agent: config.AgentTypeConfig{
			Name:         "code-pr",
			InstanceName: "demo-coder",
		},
	})
	if err != nil {
		t.Fatalf("render code-pr prompt: %v", err)
	}
	if !strings.Contains(rendered, "## Issue Description") {
		t.Fatalf("rendered prompt missing issue description section: %q", rendered)
	}
	if !strings.Contains(rendered, "Create the expected demo artifact and exit cleanly.") {
		t.Fatalf("rendered prompt missing issue description content: %q", rendered)
	}
}
