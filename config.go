package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds settings from ~/.config/goshai/config.yaml.
type Config struct {
	URL      string `yaml:"url"`
	Token    string `yaml:"token"`
	Model    string `yaml:"model"`
	Prompt   string `yaml:"prompt"`
	NoStream bool   `yaml:"no_stream,omitempty"`
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

// LoadConfig reads config.yaml; a missing file returns a zero-value Config.
func LoadConfig() (Config, error) {
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
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// SaveConfig writes the effective configuration to ~/.config/goshai/config.yaml,
// creating the directory if needed.
func SaveConfig(cfg Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")
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
