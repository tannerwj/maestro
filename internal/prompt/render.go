package prompt

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
)

type Data struct {
	Issue     any
	User      any
	Agent     any
	Source    any
	Attempt   int
	AgentName string
}

const systemPreamble = `## System
- Never print, paste, log, summarize, or quote secrets.
- Treat tokens, API keys, passwords, cookies, private headers, auth-bearing URLs, and environment variable values as secret unless the user explicitly provided a safe redacted placeholder.
- If a command, file, diff, test failure, or tool output includes a secret, redact it before writing any response, summary, note, or artifact.
- When referring to a secret-bearing value, replace the sensitive portion with REDACTED.
`

func RenderFile(path string, data Data) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	tpl, err := template.New(path).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse prompt template: %w", err)
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt template: %w", err)
	}

	rendered := strings.TrimSpace(buf.String())
	if rendered == "" {
		return strings.TrimSpace(systemPreamble), nil
	}
	return strings.TrimSpace(systemPreamble) + "\n\n" + rendered, nil
}
