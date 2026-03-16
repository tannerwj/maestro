package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/logging"
	"github.com/tjohnson/maestro/internal/ops"
	"github.com/tjohnson/maestro/internal/orchestrator"
	"github.com/tjohnson/maestro/internal/state"
	"github.com/tjohnson/maestro/internal/tui"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runCommand(nil)
		return
	}

	switch args[0] {
	case "run":
		runCommand(args[1:])
	case "inspect":
		inspectCommand(args[1:])
	case "reset":
		resetCommand(args[1:])
	case "cleanup":
		cleanupCommand(args[1:])
	case "version":
		fmt.Println(version)
	default:
		runCommand(args)
	}
}

func runCommand(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var configPath string
	var noTUI bool
	fs.StringVar(&configPath, "config", "", "path to maestro config")
	fs.BoolVar(&noTUI, "no-tui", false, "run without the terminal UI")
	_ = fs.Parse(args)

	cfg, err := config.Load(configPath)
	if err != nil {
		fatalf("load config: %v", err)
	}
	if err := config.ValidateMVP(cfg); err != nil {
		fatalf("validate config: %v", err)
	}

	logger, closeLogs, err := logging.New(cfg.Logging.Level, cfg.Logging.Dir, cfg.Logging.MaxFiles)
	if err != nil {
		fatalf("init logger: %v", err)
	}
	defer func() {
		if closeLogs != nil {
			_ = closeLogs()
		}
	}()

	runtime, err := orchestrator.NewRuntime(cfg, logger)
	if err != nil {
		fatalf("build runtime: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.Run(ctx)
	}()

	if noTUI {
		if err := waitForExit(ctx, errCh, logger); err != nil {
			fatalf("run service: %v", err)
		}
		return
	}

	model := tui.NewModel(runtime)
	if _, err := tea.NewProgram(model, tea.WithAltScreen()).Run(); err != nil {
		fatalf("run tui: %v", err)
	}
	cancel()
	if err := waitForExit(context.Background(), errCh, logger); err != nil {
		fatalf("run service: %v", err)
	}
}

func inspectCommand(args []string) {
	if len(args) == 0 {
		fatalf("inspect requires a target: config or state")
	}

	switch args[0] {
	case "config":
		inspectConfig(args[1:])
	case "state":
		inspectState(args[1:])
	case "runs":
		inspectRuns(args[1:])
	default:
		fatalf("unknown inspect target %q", args[0])
	}
}

func inspectConfig(args []string) {
	fs := flag.NewFlagSet("inspect config", flag.ExitOnError)
	var configPath string
	var asJSON bool
	fs.StringVar(&configPath, "config", "", "path to maestro config")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	_ = fs.Parse(args)

	cfg, err := config.Load(configPath)
	if err != nil {
		fatalf("load config: %v", err)
	}
	summary := ops.SummarizeConfig(cfg)
	if asJSON {
		printJSON(summary)
		return
	}

	fmt.Printf("Config: %s\n", summary.ConfigPath)
	fmt.Printf("Workspace: %s\n", summary.WorkspaceRoot)
	fmt.Printf("State dir: %s\n", summary.StateDir)
	fmt.Printf("Log dir: %s (max_files=%d)\n", summary.LogDir, summary.LogMaxFiles)
	for _, source := range summary.Sources {
		fmt.Printf("Source: %s tracker=%s poll=%s\n", source.Name, source.Tracker, source.PollInterval)
		if source.Project != "" {
			fmt.Printf("  Project: %s\n", source.Project)
		}
		if source.Group != "" {
			fmt.Printf("  Group: %s\n", source.Group)
		}
		if source.Repo != "" {
			fmt.Printf("  Repo: %s\n", source.Repo)
		}
		if source.TokenEnv != "" {
			fmt.Printf("  Token env: %s\n", source.TokenEnv)
		}
	}
	for _, agent := range summary.Agents {
		fmt.Printf("Agent: %s harness=%s approval=%s\n", agent.Name, agent.Harness, agent.ApprovalPolicy)
		if agent.AgentPack != "" {
			fmt.Printf("  Pack: %s\n", agent.AgentPack)
		}
		if len(agent.EnvKeys) > 0 {
			sort.Strings(agent.EnvKeys)
			fmt.Printf("  Env keys: %v\n", agent.EnvKeys)
		}
	}
}

func inspectState(args []string) {
	fs := flag.NewFlagSet("inspect state", flag.ExitOnError)
	var configPath string
	var stateDir string
	var asJSON bool
	fs.StringVar(&configPath, "config", "", "path to maestro config")
	fs.StringVar(&stateDir, "state-dir", "", "path to the state directory")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	_ = fs.Parse(args)

	stores, _, err := resolveStateStoresAndConfig(configPath, stateDir)
	if err != nil {
		fatalf("resolve state stores: %v", err)
	}
	if asJSON {
		summaries, err := loadStateSummaries(stores)
		if err != nil {
			fatalf("load state: %v", err)
		}
		printJSON(summaries)
		return
	}
	summaries, err := loadStateSummaries(stores)
	if err != nil {
		fatalf("load state: %v", err)
	}
	for i, summary := range summaries {
		if len(summaries) > 1 {
			fmt.Printf("[%s]\n", stores[i].Name)
		}
		fmt.Printf("Source: %s\n", summary.SourceName)
		fmt.Printf("Health: %s done=%d failed=%d retries=%d pending=%d\n", summary.Health, summary.DoneCount, summary.FailedCount, summary.RetryCount, summary.PendingCount)
		if summary.LastError != "" {
			fmt.Printf("Last error: %s\n", summary.LastError)
		}
		fmt.Printf("State file: %s\n", summary.Path)
		fmt.Printf("Version: %d\n", summary.Version)
		fmt.Printf("Finished: %d\n", summary.FinishedCount)
		fmt.Printf("Retries: %d\n", summary.RetryCount)
		fmt.Printf("Pending approvals: %d\n", summary.PendingCount)
		fmt.Printf("Approval history: %d\n", summary.ApprovalHistCount)
		if summary.ActiveRun != nil {
			fmt.Printf("Active run: %s on %s status=%s attempt=%d\n", summary.ActiveRun.RunID, summary.ActiveRun.Identifier, summary.ActiveRun.Status, summary.ActiveRun.Attempt)
		}
		for _, approval := range summary.PendingApprovals {
			fmt.Printf("Pending approval: %s %s on %s\n", approval.RequestID, approval.ToolName, approval.IssueIdentifier)
		}
		for _, retry := range summary.Retries {
			fmt.Printf("Retry: %s attempt=%d due=%s\n", retry.Identifier, retry.Attempt, retry.DueAt.Format("2006-01-02T15:04:05Z07:00"))
		}
		if i < len(summaries)-1 {
			fmt.Println()
		}
	}
}

func inspectRuns(args []string) {
	fs := flag.NewFlagSet("inspect runs", flag.ExitOnError)
	var configPath string
	var stateDir string
	var asJSON bool
	fs.StringVar(&configPath, "config", "", "path to maestro config")
	fs.StringVar(&stateDir, "state-dir", "", "path to the state directory")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	_ = fs.Parse(args)

	stores, _, err := resolveStateStoresAndConfig(configPath, stateDir)
	if err != nil {
		fatalf("resolve state stores: %v", err)
	}
	if asJSON {
		summaries, err := loadRunSummaries(stores)
		if err != nil {
			fatalf("load state: %v", err)
		}
		printJSON(summaries)
		return
	}
	summaries, err := loadRunSummaries(stores)
	if err != nil {
		fatalf("load state: %v", err)
	}
	for i, summary := range summaries {
		if len(summaries) > 1 {
			fmt.Printf("[%s]\n", stores[i].Name)
		}
		fmt.Printf("Source: %s\n", summary.SourceName)
		fmt.Printf("Health: %s active=%d retries=%d finished=%d done=%d failed=%d\n", summary.Health, summary.ActiveCount, summary.RetryCount, summary.FinishedCount, summary.DoneCount, summary.FailedCount)
		if summary.LastError != "" {
			fmt.Printf("Last error: %s\n", summary.LastError)
		}
		if summary.ActiveRun != nil {
			fmt.Printf("Active: %s run=%s status=%s attempt=%d workspace=%s\n", summary.ActiveRun.Identifier, summary.ActiveRun.RunID, summary.ActiveRun.Status, summary.ActiveRun.Attempt, summary.ActiveRun.WorkspacePath)
			if !summary.ActiveRun.LastActivityAt.IsZero() {
				fmt.Printf("  Last activity: %s\n", summary.ActiveRun.LastActivityAt.Format(time.RFC3339))
			}
		} else {
			fmt.Println("Active: none")
		}
		if len(summary.Retries) == 0 {
			fmt.Println("Retries: none")
		} else {
			fmt.Println("Retries:")
			for _, retry := range summary.Retries {
				fmt.Printf("  %s attempt=%d due=%s\n", retry.Identifier, retry.Attempt, retry.DueAt.Format(time.RFC3339))
				if retry.Error != "" {
					fmt.Printf("    Error: %s\n", retry.Error)
				}
			}
		}
		if len(summary.Finished) == 0 {
			fmt.Println("Finished: none")
		} else {
			fmt.Println("Finished:")
			for _, finished := range summary.Finished {
				fmt.Printf("  %s status=%s attempt=%d finished=%s\n", finished.Identifier, finished.Status, finished.Attempt, finished.FinishedAt.Format(time.RFC3339))
				if finished.Error != "" {
					fmt.Printf("    Error: %s\n", finished.Error)
				}
			}
		}
		if i < len(summaries)-1 {
			fmt.Println()
		}
	}
}

func resetCommand(args []string) {
	if len(args) == 0 {
		fatalf("reset requires a target: issue")
	}
	switch args[0] {
	case "issue":
		resetIssueCommand(args[1:])
	default:
		fatalf("unknown reset target %q", args[0])
	}
}

func resetIssueCommand(args []string) {
	fs := flag.NewFlagSet("reset issue", flag.ExitOnError)
	var configPath string
	var stateDir string
	var workspaceRoot string
	var keepWorkspace bool
	var asJSON bool
	fs.StringVar(&configPath, "config", "", "path to maestro config")
	fs.StringVar(&stateDir, "state-dir", "", "path to the state directory")
	fs.StringVar(&workspaceRoot, "workspace-root", "", "path to the workspace root")
	fs.BoolVar(&keepWorkspace, "keep-workspace", false, "keep the issue workspace on disk")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	_ = fs.Parse(args)

	if fs.NArg() != 1 {
		fatalf("reset issue requires one issue id or identifier")
	}
	issueRef := fs.Arg(0)

	stores, cfg, err := resolveStateStoresAndConfig(configPath, stateDir)
	if err != nil {
		fatalf("resolve state stores: %v", err)
	}
	if workspaceRoot == "" && cfg != nil {
		workspaceRoot = cfg.Workspace.Root
	}

	var (
		result   ops.ResetIssueResult
		resetErr error
	)
	for _, candidate := range stores {
		result, resetErr = ops.ResetIssue(candidate.Store, issueRef, workspaceRoot, !keepWorkspace)
		if resetErr == nil {
			break
		}
	}
	if resetErr != nil {
		fatalf("reset issue: %v", resetErr)
	}
	if asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("Reset issue: %s\n", result.IssueRef)
	if result.MatchedIdentifier != "" || result.MatchedIssueID != "" {
		fmt.Printf("Matched: %s (%s)\n", result.MatchedIdentifier, result.MatchedIssueID)
	}
	fmt.Printf("Removed finished: %t\n", result.RemovedFinished)
	fmt.Printf("Removed retry: %t\n", result.RemovedRetry)
	fmt.Printf("Removed pending approvals: %d\n", result.RemovedPendingApprovals)
	fmt.Printf("Removed approval history: %d\n", result.RemovedApprovalHistory)
	if len(result.RemovedWorkspacePaths) == 0 {
		fmt.Println("Removed workspaces: none")
	} else {
		fmt.Println("Removed workspaces:")
		for _, path := range result.RemovedWorkspacePaths {
			fmt.Printf("  %s\n", path)
		}
	}
}

func cleanupCommand(args []string) {
	if len(args) == 0 {
		fatalf("cleanup requires a target: workspaces")
	}
	switch args[0] {
	case "workspaces":
		cleanupWorkspacesCommand(args[1:])
	default:
		fatalf("unknown cleanup target %q", args[0])
	}
}

func cleanupWorkspacesCommand(args []string) {
	fs := flag.NewFlagSet("cleanup workspaces", flag.ExitOnError)
	var configPath string
	var stateDir string
	var workspaceRoot string
	var dryRun bool
	var asJSON bool
	fs.StringVar(&configPath, "config", "", "path to maestro config")
	fs.StringVar(&stateDir, "state-dir", "", "path to the state directory")
	fs.StringVar(&workspaceRoot, "workspace-root", "", "path to the workspace root")
	fs.BoolVar(&dryRun, "dry-run", false, "show which workspaces would be removed")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	_ = fs.Parse(args)

	stores, cfg, err := resolveStateStoresAndConfig(configPath, stateDir)
	if err != nil {
		fatalf("resolve state stores: %v", err)
	}
	if workspaceRoot == "" {
		if cfg == nil {
			fatalf("cleanup workspaces requires --config or --workspace-root")
		}
		workspaceRoot = cfg.Workspace.Root
	}

	protected, err := activeWorkspacePaths(stores)
	if err != nil {
		fatalf("load state: %v", err)
	}
	result, err := ops.CleanupWorkspacesWithProtected(workspaceRoot, protected, dryRun)
	if err != nil {
		fatalf("cleanup workspaces: %v", err)
	}
	if asJSON {
		printJSON(result)
		return
	}

	fmt.Printf("Workspace root: %s\n", result.Root)
	fmt.Printf("Dry run: %t\n", result.DryRun)
	if len(result.Protected) > 0 {
		fmt.Println("Protected:")
		for _, path := range result.Protected {
			fmt.Printf("  %s\n", path)
		}
	}
	if len(result.Removed) == 0 {
		fmt.Println("Removed: none")
	} else {
		fmt.Println("Removed:")
		for _, path := range result.Removed {
			fmt.Printf("  %s\n", path)
		}
	}
	if len(result.Skipped) > 0 {
		fmt.Println("Skipped:")
		for _, path := range result.Skipped {
			fmt.Printf("  %s\n", path)
		}
	}
}

func resolveStateStore(configPath string, stateDir string) (*state.Store, error) {
	stores, _, err := resolveStateStoresAndConfig(configPath, stateDir)
	if err != nil {
		return nil, err
	}
	if len(stores) == 0 {
		return nil, fmt.Errorf("no state stores configured")
	}
	return stores[0].Store, nil
}

type namedStore struct {
	Name  string
	Store *state.Store
}

func resolveStateStoresAndConfig(configPath string, stateDir string) ([]namedStore, *config.Config, error) {
	if stateDir != "" {
		return []namedStore{{Name: "state", Store: state.NewStore(stateDir)}}, nil, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, err
	}
	stores := make([]namedStore, 0, len(cfg.Sources))
	for _, source := range cfg.Sources {
		stores = append(stores, namedStore{
			Name:  source.Name,
			Store: state.NewStore(config.ScopedStateDir(cfg, source)),
		})
	}
	return stores, cfg, nil
}

func loadStateSummaries(stores []namedStore) ([]ops.StateSummary, error) {
	out := make([]ops.StateSummary, 0, len(stores))
	for _, candidate := range stores {
		snapshot, err := candidate.Store.Load()
		if err != nil {
			return nil, err
		}
		out = append(out, ops.SummarizeState(candidate.Name, candidate.Store.Path(), snapshot))
	}
	return out, nil
}

func loadRunSummaries(stores []namedStore) ([]ops.RunsSummary, error) {
	out := make([]ops.RunsSummary, 0, len(stores))
	for _, candidate := range stores {
		snapshot, err := candidate.Store.Load()
		if err != nil {
			return nil, err
		}
		out = append(out, ops.SummarizeRuns(candidate.Name, snapshot))
	}
	return out, nil
}

func activeWorkspacePaths(stores []namedStore) ([]string, error) {
	var protected []string
	for _, candidate := range stores {
		snapshot, err := candidate.Store.Load()
		if err != nil {
			return nil, err
		}
		if snapshot.ActiveRun != nil && snapshot.ActiveRun.WorkspacePath != "" {
			protected = append(protected, snapshot.ActiveRun.WorkspacePath)
		}
	}
	return protected, nil
}

func printJSON(value any) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fatalf("render json: %v", err)
	}
	fmt.Println(string(raw))
}

func waitForExit(ctx context.Context, errCh <-chan error, logger *slog.Logger) error {
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown requested")
		return <-errCh
	}
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
