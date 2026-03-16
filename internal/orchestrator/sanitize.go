package orchestrator

import (
	"fmt"

	"github.com/tjohnson/maestro/internal/redact"
)

func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", redact.String(err.Error()))
}

func sanitizeOutput(raw string) string {
	return redact.String(raw)
}
