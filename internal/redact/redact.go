package redact

import "regexp"

var replacements = []struct {
	re *regexp.Regexp
	to string
}{
	{re: regexp.MustCompile(`https?://([^/\s:@]+):([^@\s/]+)@`), to: `https://${1}:REDACTED@`},
	{re: regexp.MustCompile(`(?i)(authorization:\s*(?:basic|bearer)\s+)[^\s"']+`), to: `${1}REDACTED`},
	{re: regexp.MustCompile(`(?i)(private-token:\s*)[^\s"']+`), to: `${1}REDACTED`},
	{re: regexp.MustCompile(`(?i)([?&](?:access[_-]?token|private[_-]?token|token)=)[^&\s]+`), to: `${1}REDACTED`},
	{re: regexp.MustCompile(`\bglpat-[A-Za-z0-9._-]+\b`), to: `glpat-REDACTED`},
	{re: regexp.MustCompile(`\blin_api_[A-Za-z0-9._-]+\b`), to: `lin_api_REDACTED`},
	{re: regexp.MustCompile(`\bxoxb-[A-Za-z0-9._-]+\b`), to: `xoxb-REDACTED`},
	{re: regexp.MustCompile(`\bxapp-[A-Za-z0-9._-]+\b`), to: `xapp-REDACTED`},
}

func String(raw string) string {
	out := raw
	for _, replacement := range replacements {
		out = replacement.re.ReplaceAllString(out, replacement.to)
	}
	return out
}
