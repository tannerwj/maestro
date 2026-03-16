package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	MaxConcurrent  int               `yaml:"max_concurrent"`
	Env            map[string]string `yaml:"env"`
	Tools          []string          `yaml:"tools"`
	Skills         []string          `yaml:"skills"`
	ContextFiles   []string          `yaml:"context_files"`

	Path    string `yaml:"-"`
	Context string `yaml:"-"`
}

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
		pack, err := loadAgentPack(cfg, agent.AgentPack)
		if err != nil {
			return fmt.Errorf("load agent pack %q: %w", agent.AgentPack, err)
		}
		mergeAgentPack(agent, pack)
		agent.PackPath = pack.Path
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
	packDir := filepath.Dir(path)
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
	if agent.MaxConcurrent == 0 {
		agent.MaxConcurrent = pack.MaxConcurrent
	}
	agent.Tools = appendUnique(pack.Tools, agent.Tools)
	agent.Skills = appendUnique(pack.Skills, agent.Skills)
	agent.ContextFiles = appendUnique(pack.ContextFiles, agent.ContextFiles)
	agent.Env = mergeStringMap(pack.Env, agent.Env)
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
