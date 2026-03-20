package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type AgentPackConfig struct {
	Name           string            `yaml:"name"`
	Description    string            `yaml:"description"`
	InstanceName   string            `yaml:"instance_name"`
	Harness        string            `yaml:"harness"`
	Workspace      string            `yaml:"workspace"`
	Prompt         string            `yaml:"prompt"`
	ApprovalPolicy string            `yaml:"approval_policy"`
	Communication  string            `yaml:"communication"`
	MaxConcurrent  int               `yaml:"max_concurrent"`
	Codex          *CodexConfig      `yaml:"codex"`
	Claude         *ClaudeConfig     `yaml:"claude"`
	Env            map[string]string `yaml:"env"`
	Tools          []string          `yaml:"tools"`
	Skills         []string          `yaml:"skills"`
	ContextFiles   []string          `yaml:"context_files"`

	Path      string `yaml:"-"`
	ClaudeDir string `yaml:"-"`
	CodexDir  string `yaml:"-"`
	Context   string `yaml:"-"`
}

const defaultRepoPackPath = ".maestro"

func resolveAgentPacks(cfg *Config) error {
	for i := range cfg.AgentTypes {
		if err := resolveAgentPack(cfg, &cfg.AgentTypes[i]); err != nil {
			return err
		}
	}
	return nil
}

func resolveAgentPack(cfg *Config, agent *AgentTypeConfig) error {
	if strings.TrimSpace(agent.AgentPack) != "" {
		if repoPath, ok := ParseRepoPackRef(agent.AgentPack); ok {
			agent.RepoPackPath = repoPath
			return nil
		}
		pack, err := loadAgentPack(cfg, agent.AgentPack)
		if err != nil {
			return fmt.Errorf("load agent pack %q: %w", agent.AgentPack, err)
		}
		mergeAgentPack(agent, pack)
		agent.PackPath = pack.Path
		agent.PackClaudeDir = pack.ClaudeDir
		agent.PackCodexDir = pack.CodexDir
	}
	return nil
}

func resolveAgentContexts(cfg *Config) error {
	for i := range cfg.AgentTypes {
		context, err := loadAgentContext(cfg.AgentTypes[i].ContextFiles)
		if err != nil {
			return fmt.Errorf("load context for agent %q: %w", cfg.AgentTypes[i].Name, err)
		}
		cfg.AgentTypes[i].Context = context
	}
	return nil
}

func loadAgentPack(cfg *Config, ref string) (*AgentPackConfig, error) {
	path, err := resolveAgentPackPath(cfg, ref)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	pack := &AgentPackConfig{}
	if err := yaml.Unmarshal(raw, pack); err != nil {
		return nil, fmt.Errorf("decode agent pack: %w", err)
	}

	pack.Path = path
	if err := resolveLocalPackAssets(pack, filepath.Dir(path)); err != nil {
		return nil, err
	}
	return pack, nil
}

func ResolveRepoPack(workspacePath string, repoPackPath string) (*AgentPackConfig, error) {
	packDir, err := resolveRepoPackDir(workspacePath, repoPackPath)
	if err != nil {
		return nil, err
	}
	promptPath := filepath.Join(packDir, "prompt.md")
	if _, err := os.Stat(promptPath); err != nil {
		return nil, fmt.Errorf("repo pack prompt %q: %w", promptPath, err)
	}
	contextFiles, err := collectPackContextFiles(filepath.Join(packDir, "context"))
	if err != nil {
		return nil, err
	}
	context, err := loadAgentContext(contextFiles)
	if err != nil {
		return nil, err
	}
	claudeDir, err := resolveOptionalPackDir(packDir, "claude")
	if err != nil {
		return nil, err
	}
	codexDir, err := resolveOptionalPackDir(packDir, "codex")
	if err != nil {
		return nil, err
	}
	pack := &AgentPackConfig{
		Path:         filepath.Join(packDir, "agent.yaml"),
		Prompt:       filepath.Clean(promptPath),
		ContextFiles: contextFiles,
		ClaudeDir:    claudeDir,
		CodexDir:     codexDir,
		Context:      context,
	}
	return pack, nil
}

func resolveAgentPackPath(cfg *Config, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty agent pack reference")
	}

	candidates := []string{}
	isPathLike := strings.ContainsRune(ref, os.PathSeparator) || strings.HasPrefix(ref, ".") || strings.HasSuffix(ref, ".yaml") || strings.HasSuffix(ref, ".yml")
	if isPathLike {
		candidate := ref
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cfg.ConfigDir, candidate)
		}
		candidates = append(candidates, candidate)
	} else {
		base := cfg.AgentPacksDir
		if !filepath.IsAbs(base) {
			base = filepath.Join(cfg.ConfigDir, base)
		}
		candidates = append(candidates, filepath.Join(base, ref))
	}

	for _, candidate := range candidates {
		resolved, err := expandPath(candidate)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(resolved) {
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
		}
		info, err := os.Stat(resolved)
		if err == nil && info.IsDir() {
			resolved = filepath.Join(resolved, "agent.yaml")
		} else if err == nil {
			return filepath.Clean(resolved), nil
		}

		if _, err := os.Stat(resolved); err == nil {
			return filepath.Clean(resolved), nil
		}
	}

	return "", fmt.Errorf("no agent pack found for %q", ref)
}

func ParseRepoPackRef(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "repo:") {
		return "", false
	}
	path := strings.TrimSpace(strings.TrimPrefix(ref, "repo:"))
	if path == "" {
		path = defaultRepoPackPath
	}
	path = filepath.Clean(path)
	if path == "." {
		path = defaultRepoPackPath
	}
	return path, true
}

func mergeAgentPack(agent *AgentTypeConfig, pack *AgentPackConfig) {
	if agent.Name == "" {
		agent.Name = pack.Name
	}
	if agent.Description == "" {
		agent.Description = pack.Description
	}
	if agent.InstanceName == "" {
		agent.InstanceName = pack.InstanceName
	}
	if agent.Harness == "" {
		agent.Harness = pack.Harness
	}
	if agent.Workspace == "" {
		agent.Workspace = pack.Workspace
	}
	if agent.Prompt == "" {
		agent.Prompt = pack.Prompt
	}
	if agent.ApprovalPolicy == "" {
		agent.ApprovalPolicy = pack.ApprovalPolicy
	}
	if agent.Communication == "" {
		agent.Communication = pack.Communication
	}
	if agent.MaxConcurrent == 0 {
		agent.MaxConcurrent = pack.MaxConcurrent
	}
	agent.Codex = mergeCodexConfig(pack.Codex, agent.Codex)
	agent.Claude = mergeClaudeConfig(pack.Claude, agent.Claude)
	agent.Tools = appendUnique(pack.Tools, agent.Tools)
	agent.Skills = appendUnique(pack.Skills, agent.Skills)
	agent.ContextFiles = appendUnique(pack.ContextFiles, agent.ContextFiles)
	agent.Env = mergeStringMap(pack.Env, agent.Env)
}

func resolveOptionalPackDir(packDir string, name string) (string, error) {
	path := filepath.Join(packDir, name)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat pack %s dir: %w", name, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("pack %s path %q must be a directory", name, path)
	}
	return filepath.Clean(path), nil
}

func resolveLocalPackAssets(pack *AgentPackConfig, packDir string) error {
	if pack.Prompt != "" && !filepath.IsAbs(pack.Prompt) {
		pack.Prompt = filepath.Join(packDir, pack.Prompt)
	}
	if pack.Prompt != "" {
		pack.Prompt = filepath.Clean(pack.Prompt)
	}

	for i := range pack.ContextFiles {
		if filepath.IsAbs(pack.ContextFiles[i]) {
			pack.ContextFiles[i] = filepath.Clean(pack.ContextFiles[i])
			continue
		}
		pack.ContextFiles[i] = filepath.Join(packDir, pack.ContextFiles[i])
		pack.ContextFiles[i] = filepath.Clean(pack.ContextFiles[i])
	}

	claudeDir, err := resolveOptionalPackDir(packDir, "claude")
	if err != nil {
		return err
	}
	pack.ClaudeDir = claudeDir

	codexDir, err := resolveOptionalPackDir(packDir, "codex")
	if err != nil {
		return err
	}
	pack.CodexDir = codexDir
	return nil
}

func resolveRepoPackDir(workspacePath string, repoPackPath string) (string, error) {
	relative := strings.TrimSpace(repoPackPath)
	if relative == "" {
		relative = defaultRepoPackPath
	}
	relative = filepath.Clean(relative)
	if relative == "." {
		relative = defaultRepoPackPath
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("repo pack path %q must be relative", relative)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repo pack path %q must stay within the workspace", relative)
	}
	path := filepath.Join(workspacePath, relative)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("repo pack dir %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo pack path %q must be a directory", path)
	}
	return filepath.Clean(path), nil
}

func collectPackContextFiles(contextDir string) ([]string, error) {
	info, err := os.Stat(contextDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat pack context dir %q: %w", contextDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("pack context path %q must be a directory", contextDir)
	}
	files := []string{}
	err = filepath.WalkDir(contextDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("pack context path %q must not be a symlink", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("pack context path %q has unsupported file type", path)
		}
		files = append(files, filepath.Clean(path))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func mergeStringMap(base map[string]string, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	merged := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

func appendUnique(base []string, override []string) []string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}

	combined := make([]string, 0, len(base)+len(override))
	for _, value := range append(append([]string{}, base...), override...) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || slices.Contains(combined, trimmed) {
			continue
		}
		combined = append(combined, trimmed)
	}
	return combined
}

func loadAgentContext(paths []string) (string, error) {
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
