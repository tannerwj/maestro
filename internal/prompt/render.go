package prompt

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
)

var defaultFuncMap = template.FuncMap{
	"default": func(dflt any, val any) any {
		if val == nil {
			return dflt
		}
		if s, ok := val.(string); ok && s == "" {
			return dflt
		}
		return val
	},
	"join":      strings.Join,
	"lower":     strings.ToLower,
	"upper":     strings.ToUpper,
	"trim":      strings.TrimSpace,
	"contains":  strings.Contains,
	"hasPrefix": strings.HasPrefix,
	"indent": func(spaces int, s string) string {
		pad := strings.Repeat(" ", spaces)
		lines := strings.Split(s, "\n")
		for i, line := range lines {
			if line != "" {
				lines[i] = pad + line
			}
		}
		return strings.Join(lines, "\n")
	},
}

type Data struct {
	Issue               any
	User                any
	Agent               any
	Source              any
	Attempt             int
	AgentName           string
	OperatorInstruction string
}

const systemPreamble = `## System
- Never print, paste, log, summarize, or quote secrets.
- Treat tokens, API keys, passwords, cookies, private headers, auth-bearing URLs, and environment variable values as secret unless the user explicitly provided a safe redacted placeholder.
- If a command, file, diff, test failure, or tool output includes a secret, redact it before writing any response, summary, note, or artifact.
- When referring to a secret-bearing value, replace the sensitive portion with REDACTED.
`

func SystemPreamble() string {
	return strings.TrimSpace(systemPreamble)
}

func ParseFile(path string) (*template.Template, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	tpl, err := template.New(path).Funcs(defaultFuncMap).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse prompt template: %w", err)
	}
	return tpl, nil
}

func RenderFile(path string, data Data) (string, error) {
	tpl, err := ParseFile(path)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render prompt template: %w", err)
	}

	rendered := strings.TrimSpace(buf.String())
	if strings.TrimSpace(data.OperatorInstruction) != "" {
		operatorSection := strings.TrimSpace(fmt.Sprintf("## Operator Guidance\n%s", data.OperatorInstruction))
		if rendered == "" {
			rendered = operatorSection
		} else {
			rendered = rendered + "\n\n" + operatorSection
		}
	}
	if rendered == "" {
		return strings.TrimSpace(systemPreamble), nil
	}
	return strings.TrimSpace(systemPreamble) + "\n\n" + rendered, nil
}
