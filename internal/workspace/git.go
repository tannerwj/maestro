package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/tjohnson/maestro/internal/redact"
)

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"git %s: %w: %s",
			redact.String(strings.Join(args, " ")),
			err,
			redact.String(strings.TrimSpace(string(output))),
		)
	}

	return nil
}
