package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/tjohnson/maestro/internal/domain"
)

func (s *Service) runHook(ctx context.Context, script string, workdir string, run *domain.AgentRun, stage string) error {
	if strings.TrimSpace(script) == "" || strings.TrimSpace(workdir) == "" {
		return nil
	}

	hookCtx, cancel := context.WithTimeout(ctx, s.cfg.Hooks.Timeout.Duration)
	defer cancel()

	shell, args := shellCommand(script)
	cmd := exec.CommandContext(hookCtx, shell, args...)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(), hookEnv(run, stage)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hook %s failed: %w output=%s", stage, err, sanitizeOutput(strings.TrimSpace(string(output))))
	}
	return nil
}

func (s *Service) runHookBestEffort(ctx context.Context, script string, workdir string, run *domain.AgentRun, stage string) {
	if err := s.runHook(ctx, script, workdir, run, stage); err != nil {
		s.recordEvent("warn", "%v", err)
	} else if strings.TrimSpace(script) != "" && strings.TrimSpace(workdir) != "" {
		s.recordEvent("info", "hook %s completed for %s", stage, run.Issue.Identifier)
	}
}

func hookEnv(run *domain.AgentRun, stage string) []string {
	return []string{
		"MAESTRO_RUN_ID=" + run.ID,
		"MAESTRO_ISSUE_ID=" + run.Issue.ID,
		"MAESTRO_ISSUE_IDENTIFIER=" + run.Issue.Identifier,
		"MAESTRO_AGENT_NAME=" + run.AgentName,
		"MAESTRO_AGENT_TYPE=" + run.AgentType,
		"MAESTRO_RUN_STAGE=" + stage,
		"MAESTRO_RUN_STATUS=" + string(run.Status),
		"MAESTRO_WORKSPACE_PATH=" + run.WorkspacePath,
	}
}

func shellCommand(script string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", script}
	}
	return "sh", []string{"-lc", script}
}
