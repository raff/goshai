package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// modelsCacheTTL is how long a cached model list is considered fresh.
const modelsCacheTTL = 24 * time.Hour

// ModelsCacheEntry holds a list of model IDs fetched from one server URL.
type ModelsCacheEntry struct {
	Models    []string  `yaml:"models"`
	UpdatedAt time.Time `yaml:"updated_at"`
}

// ModelsCache maps server base URL → cached model list.
type ModelsCache map[string]ModelsCacheEntry

// LoadModelsCache reads models_cache.yaml; returns an empty cache on any error.
func LoadModelsCache() ModelsCache {
	dir, err := configDir()
	if err != nil {
		return ModelsCache{}
	}
	data, err := os.ReadFile(filepath.Join(dir, "models_cache.yaml"))
	if err != nil {
		return ModelsCache{}
	}
	var c ModelsCache
	if err := yaml.Unmarshal(data, &c); err != nil || c == nil {
		return ModelsCache{}
	}
	return c
}

// SaveModelsCache writes the cache back to models_cache.yaml, ignoring errors.
func SaveModelsCache(c ModelsCache) {
	dir, err := configDir()
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	data, err := yaml.Marshal(c)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "models_cache.yaml"), data, 0o600)
}

// CachedModels returns (models, true) when a fresh entry exists for serverURL.
func (c ModelsCache) CachedModels(serverURL string) ([]string, bool) {
	entry, ok := c[serverURL]
	if !ok || time.Since(entry.UpdatedAt) > modelsCacheTTL {
		return nil, false
	}
	return entry.Models, true
}

// Config holds settings from ~/.config/goshai/config.yaml.
// Name is the environment key (map key in the YAML); it is not written as a field.
type Config struct {
	Name           string `yaml:"-"`
	URL            string `yaml:"url"`
	Token          string `yaml:"token"`
	Model          string `yaml:"model"`
	Prompt         string `yaml:"prompt"`
	NoStream       bool   `yaml:"nostream,omitempty"`
	Think          bool   `yaml:"think,omitempty"`
	ThinkingBudget int    `yaml:"thinking-budget,omitempty"`
}

// Prompts maps named system prompts from ~/.config/goshai/prompts.yaml.
type Prompts map[string]string

// Aliases maps short model names to full model IDs, stored in config.yaml
// under a top-level "aliases:" key.
type Aliases map[string]string

// EnvNotFoundError reports that a named environment was requested but absent.
type EnvNotFoundError struct {
	Name string
}

func (e EnvNotFoundError) Error() string {
	return fmt.Sprintf("environment %q not found in config", e.Name)
}

func isEnvNotFound(err error) bool {
	var notFound EnvNotFoundError
	return errors.As(err, &notFound)
}

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

// parseConfigFile returns all configs and aliases from data, preserving map insertion order.
//
// Multi-env format: top-level YAML mapping where each key is an environment name
// and the value is a Config mapping. The reserved key "aliases" is decoded as
// map[string]string and excluded from the returned configs.
//
//	aliases:
//	  mini: "gpt-4o-mini"
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
func parseConfigFile(data []byte) ([]Config, Aliases, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, nil, err
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return nil, nil, nil
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode || len(root.Content) < 2 {
		return nil, nil, nil
	}

	// Detect format: if the first value is itself a mapping → multi-env.
	if root.Content[1].Kind == yaml.MappingNode {
		var configs []Config
		var aliases Aliases
		for i := 0; i+1 < len(root.Content); i += 2 {
			key := root.Content[i].Value
			valNode := root.Content[i+1]
			if key == "aliases" {
				var a map[string]string
				if err := valNode.Decode(&a); err != nil {
					return nil, nil, fmt.Errorf("aliases: %w", err)
				}
				aliases = a
				continue
			}
			var cfg Config
			if err := valNode.Decode(&cfg); err != nil {
				return nil, nil, fmt.Errorf("environment %q: %w", key, err)
			}
			cfg.Name = key
			configs = append(configs, cfg)
		}
		return configs, aliases, nil
	}

	// Legacy single-env: top-level scalars are Config fields.
	var cfg Config
	if err := root.Decode(&cfg); err != nil {
		return nil, nil, err
	}
	return []Config{cfg}, nil, nil
}

// marshalMultiEnv encodes aliases (if any) and configs as an ordered YAML map.
// The aliases block is written first, followed by envName → Config entries.
func marshalMultiEnv(configs []Config, aliases Aliases) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	if len(aliases) > 0 {
		keys := make([]string, 0, len(aliases))
		for k := range aliases {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		aliasMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range keys {
			aliasMap.Content = append(aliasMap.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: aliases[k]},
			)
		}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "aliases"},
			aliasMap,
		)
	}

	for _, cfg := range configs {
		name := cfg.Name
		cfg.Name = ""
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
		var valDoc yaml.Node
		if err := valDoc.Encode(cfg); err != nil {
			return nil, err
		}
		root.Content = append(root.Content, keyNode, &valDoc)
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
	configs, _, err := parseConfigFile(data)
	return configs, err
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
	configs, _, err := parseConfigFile(data)
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
	return Config{}, EnvNotFoundError{Name: envName}
}

// LoadAliases reads the aliases block from config.yaml.
// Returns an empty Aliases map if none are configured.
func LoadAliases() (Aliases, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if os.IsNotExist(err) {
		return Aliases{}, nil
	}
	if err != nil {
		return nil, err
	}
	_, aliases, err := parseConfigFile(data)
	if err != nil {
		return nil, err
	}
	if aliases == nil {
		return Aliases{}, nil
	}
	return aliases, nil
}

// SaveAliases writes an updated aliases map back into config.yaml, preserving
// all environment entries. Converts a legacy single-env config to named "default"
// if necessary to support the multi-env format required by the aliases block.
func SaveAliases(aliases Aliases) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")

	var configs []Config
	if existing, err := os.ReadFile(path); err == nil {
		parsed, _, err := parseConfigFile(existing)
		if err != nil {
			return err
		}
		configs = parsed
		// Convert legacy single-env (unnamed) to "default" so we can use multi-env format.
		if len(configs) > 0 && configs[0].Name == "" {
			configs[0].Name = "default"
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	data, err := marshalMultiEnv(configs, aliases)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	fmt.Println("wrote", path)
	return nil
}

// SaveConfig writes the effective configuration to ~/.config/goshai/config.yaml.
// Named configs are saved in multi-env format and update or append that env.
// Unnamed configs are saved in legacy single-env format.
func SaveConfig(cfg Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.yaml")

	// Named environment format. Missing files create a new multi-env config; an
	// existing multi-env file is updated in place; a legacy single-env file is
	// preserved as "default" before appending the requested named environment.
	if cfg.Name != "" {
		var configs []Config
		var aliases Aliases
		if existing, err := os.ReadFile(path); err == nil {
			parsed, parsedAliases, err := parseConfigFile(existing)
			if err != nil {
				return err
			}
			configs = parsed
			aliases = parsedAliases
			if len(configs) > 0 && configs[0].Name == "" {
				legacy := configs[0]
				if cfg.Name == "default" {
					configs = nil
				} else {
					legacy.Name = "default"
					configs = []Config{legacy}
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}

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
		data, err := marshalMultiEnv(configs, aliases)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		fmt.Println("wrote", path)
		return nil
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
