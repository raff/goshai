package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds settings from ~/.config/goshai/config.yaml.
// Name is the environment key (map key in the YAML); it is not written as a field.
type Config struct {
	Name     string `yaml:"-"`
	URL      string `yaml:"url"`
	Token    string `yaml:"token"`
	Model    string `yaml:"model"`
	Prompt   string `yaml:"prompt"`
	NoStream bool   `yaml:"nostream,omitempty"`
}

// Prompts maps named system prompts from ~/.config/goshai/prompts.yaml.
type Prompts map[string]string

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "goshai"), nil
}

// configFilePath returns dir/name, or "(unavailable)" when dir is empty.
func configFilePath(dir, name string) string {
	if dir == "" {
		return "(unavailable)"
	}
	return filepath.Join(dir, name)
}

// strOrDefault returns s if non-empty, otherwise fallback.
func strOrDefault(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func expandConfig(cfg *Config) {
	cfg.URL = os.ExpandEnv(cfg.URL)
	cfg.Token = os.ExpandEnv(cfg.Token)
	cfg.Model = os.ExpandEnv(cfg.Model)
	cfg.Prompt = os.ExpandEnv(cfg.Prompt)
}

// parseConfigFile returns all configs from data, preserving map insertion order.
//
// Multi-env format: top-level YAML mapping where each key is an environment name
// and the value is a Config mapping.
//
//	local:
//	  url: "http://localhost:11434/v1"
//	  model: "llama3.2"
//	remote:
//	  url: "https://api.openai.com/v1"
//	  token: "$OPENAI_API_KEY"
//	  model: "gpt-4o"
//
// Legacy single-env format: top-level Config fields directly.
//
//	url: "http://localhost:11434/v1"
//	model: "llama3.2"
func parseConfigFile(data []byte) ([]Config, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return nil, nil
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode || len(root.Content) < 2 {
		return nil, nil
	}

	// Detect format: if the first value is itself a mapping → multi-env.
	if root.Content[1].Kind == yaml.MappingNode {
		var configs []Config
		for i := 0; i+1 < len(root.Content); i += 2 {
			envName := root.Content[i].Value
			var cfg Config
			if err := root.Content[i+1].Decode(&cfg); err != nil {
				return nil, fmt.Errorf("environment %q: %w", envName, err)
			}
			cfg.Name = envName
			configs = append(configs, cfg)
		}
		return configs, nil
	}

	// Legacy single-env: top-level scalars are Config fields.
	var cfg Config
	if err := root.Decode(&cfg); err != nil {
		return nil, err
	}
	return []Config{cfg}, nil
}

// marshalMultiEnv encodes configs as an ordered YAML map of envName → Config.
func marshalMultiEnv(configs []Config) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, cfg := range configs {
		name := cfg.Name
		cfg.Name = ""
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
		var valDoc yaml.Node
		if err := valDoc.Encode(cfg); err != nil {
			return nil, err
		}
		root.Content = append(root.Content, keyNode, valDoc.Content[0])
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	return yaml.Marshal(doc)
}

// ListConfigs returns all environments from config.yaml without expanding env vars.
// Returns nil, nil if the file does not exist.
func ListConfigs() ([]Config, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseConfigFile(data)
}

// LoadConfig reads config.yaml and returns the selected environment.
// envName selects by name; if empty the first entry is used.
// A missing file returns a zero-value Config.
func LoadConfig(envName string) (Config, error) {
	dir, err := configDir()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	configs, err := parseConfigFile(data)
	if err != nil {
		return Config{}, err
	}
	if len(configs) == 0 {
		return Config{}, nil
	}
	if envName == "" {
		cfg := configs[0]
		expandConfig(&cfg)
		return cfg, nil
	}
	for _, cfg := range configs {
		if cfg.Name == envName {
			expandConfig(&cfg)
			return cfg, nil
		}
	}
	return Config{}, fmt.Errorf("environment %q not found in config", envName)
}

// SaveConfig writes the effective configuration to ~/.config/goshai/config.yaml.
// If the existing file uses multi-env format and cfg.Name is set, that named
// environment is updated (or appended if new). Otherwise saves in single-env format.
func SaveConfig(cfg Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")

	// If named env and existing file uses multi-env format, update in place.
	if cfg.Name != "" {
		if existing, err := os.ReadFile(path); err == nil {
			if configs, err := parseConfigFile(existing); err == nil && len(configs) > 0 && configs[0].Name != "" {
				found := false
				for i, c := range configs {
					if c.Name == cfg.Name {
						configs[i] = cfg
						found = true
						break
					}
				}
				if !found {
					configs = append(configs, cfg)
				}
				data, err := marshalMultiEnv(configs)
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					return err
				}
				fmt.Println("wrote", path)
				return nil
			}
		}
	}

	// Single-env (legacy) format.
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

// defaultPromptsContent is written when no prompts.yaml exists yet.
const defaultPromptsContent = `# goshai named system prompts
# Select a prompt with the -p flag: goshai -p coder "your question"
#
default: "You are a helpful assistant."
coder: "You are an expert software engineer. Be concise and precise."
reviewer: "You are a senior code reviewer. Focus on bugs, edge cases, and improvements."
explainer: "You are a patient teacher. Explain concepts clearly with examples."
`

// SaveDefaultPrompts writes a starter prompts.yaml only if one does not exist.
func SaveDefaultPrompts() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "prompts.yaml")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		fmt.Println("skipped", path, "(already exists)")
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprint(f, defaultPromptsContent); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

// LoadPrompts reads prompts.yaml; a missing file returns an empty map.
func LoadPrompts() (Prompts, error) {
	dir, err := configDir()
	if err != nil {
		return Prompts{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "prompts.yaml"))
	if os.IsNotExist(err) {
		return Prompts{}, nil
	}
	if err != nil {
		return Prompts{}, err
	}
	var p Prompts
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Prompts{}, err
	}
	return p, nil
}
