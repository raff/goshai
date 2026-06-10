package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

// multiFlag supports repeatable -f flags.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// getEnvArg get the value of -e/--env from args without parsing all flags
// (to select the right config environment before flag.Parse).
func getEnvArg(args []string) string {
	var envName string

	efs := flag.NewFlagSet("early", flag.ContinueOnError)
	efs.SetOutput(io.Discard) // suppress error output
	efs.StringVar(&envName, "e", "", "environment to use (default: first in config)")
	efs.StringVar(&envName, "env", "", "environment to use (default: first in config)")
	_ = efs.Parse(args)

	return envName
}

// fuzzyMatchModel returns the best match for input against the server model list,
// using a local cache keyed by serverURL to avoid a network call on every run.
// Returns ("", false) when no candidate is found.
func fuzzyMatchModel(ctx context.Context, client *Client, serverURL, input string) (string, bool) {
	cache := LoadModelsCache()
	ids, cached := cache.CachedModels(serverURL)
	if !cached {
		infos, err := client.ListModels(ctx)
		if err != nil {
			return "", false
		}
		ids = make([]string, 0, len(infos))
		for _, m := range infos {
			ids = append(ids, m.ID)
		}
		if cache == nil {
			cache = ModelsCache{}
		}
		cache[serverURL] = ModelsCacheEntry{Models: ids, UpdatedAt: time.Now()}
		SaveModelsCache(cache)
	}

	inputLower := strings.ToLower(input)
	var prefixMatches, containsMatches []string

	for _, id := range ids {
		idLower := strings.ToLower(id)
		if idLower == inputLower {
			return id, true
		}
		if strings.HasPrefix(idLower, inputLower) {
			prefixMatches = append(prefixMatches, id)
		} else if strings.Contains(idLower, inputLower) {
			containsMatches = append(containsMatches, id)
		}
	}

	candidates := prefixMatches
	if len(candidates) == 0 {
		candidates = containsMatches
	}
	if len(candidates) == 0 {
		return "", false
	}

	sort.Strings(candidates)
	if len(candidates) > 1 {
		fmt.Fprintf(os.Stderr, "warning: %q matches multiple models: %s; using %q\n",
			input, strings.Join(candidates, ", "), candidates[0])
	}
	return candidates[0], true
}

func main() {
	var (
		files          multiFlag
		promptName     string
		model          string
		serverURL      string
		token          string
		listPrompts    bool
		listModels     bool
		listEnvs       bool
		listAliases    bool
		setAlias       string
		writeConfig    bool
		readConfig     bool
		noStream       bool
		thinking       bool
		thinkingBudget int
		sessionName    string
		renameTo       string
		listSessions   bool
		genPrompt      bool
		envName        string
		verbose        bool
	)

	// Pre-scan for -e so LoadConfig can select the right environment
	// before flag.Parse (needed so flag.Usage shows the correct settings).
	earlyEnv := getEnvArg(os.Args[1:])

	// Load config before flag.Parse so flag.Usage can show current settings.
	cfg, err := LoadConfig(earlyEnv)
	var earlyConfigErr error
	if err != nil {
		if !isEnvNotFound(err) {
			log.Fatal("config error: ", err)
		}
		earlyConfigErr = err
	}
	dir, _ := configDir() // best-effort; empty string if unavailable

	flag.Var(&files, "f", "file to include as context (repeatable)")
	flag.Var(&files, "file", "file to include as context (repeatable)")
	flag.StringVar(&promptName, "p", "", "named system prompt")
	flag.StringVar(&promptName, "prompt", "", "named system prompt")
	flag.StringVar(&model, "m", "", "model name override (alias or prefix supported)")
	flag.StringVar(&model, "model", "", "model name override (alias or prefix supported)")
	flag.StringVar(&serverURL, "u", "", "server URL override")
	flag.StringVar(&serverURL, "url", "", "server URL override")
	flag.StringVar(&token, "t", "", "auth token override")
	flag.StringVar(&token, "token", "", "auth token override")
	flag.BoolVar(&listPrompts, "P", false, "list available named prompts")
	flag.BoolVar(&listPrompts, "prompts", false, "list available named prompts")
	flag.BoolVar(&listModels, "M", false, "list available models (requires server URL)")
	flag.BoolVar(&listModels, "models", false, "list available models (requires server URL)")
	flag.BoolVar(&listAliases, "A", false, "list model aliases")
	flag.BoolVar(&listAliases, "aliases", false, "list model aliases")
	flag.StringVar(&setAlias, "a", "", "set a model alias (format: alias=model-name)")
	flag.StringVar(&setAlias, "alias", "", "set a model alias (format: alias=model-name)")
	flag.BoolVar(&noStream, "n", false, "disable streaming (non-streaming mode)")
	flag.BoolVar(&noStream, "no-stream", false, "disable streaming (non-streaming mode)")
	flag.BoolVar(&thinking, "thinking", false, "enable extended thinking / reasoning")
	flag.IntVar(&thinkingBudget, "thinking-budget", 0, "token budget for thinking (default 10000 when thinking is enabled)")
	flag.BoolVar(&writeConfig, "W", false, "write current configuration to config.yaml and create default prompts.yaml")
	flag.BoolVar(&writeConfig, "write-config", false, "write current configuration to config.yaml and create default prompts.yaml")
	flag.BoolVar(&readConfig, "R", false, "print full configuration for the selected environment (token included)")
	flag.BoolVar(&readConfig, "read-config", false, "print full configuration for the selected environment (token included)")
	flag.StringVar(&sessionName, "s", "", "session name to continue (creates if new); omit to start fresh and save to 'last'")
	flag.StringVar(&sessionName, "session", "", "session name to continue (creates if new); omit to start fresh and save to 'last'")
	flag.StringVar(&renameTo, "r", "", "rename the 'last' session to a new name and exit")
	flag.StringVar(&renameTo, "rename", "", "rename the 'last' session to a new name and exit")
	flag.BoolVar(&listSessions, "S", false, "list available sessions")
	flag.BoolVar(&listSessions, "sessions", false, "list available sessions")
	flag.BoolVar(&genPrompt, "G", false, "generate reusable prompt from session history")
	flag.BoolVar(&genPrompt, "gen-prompt", false, "generate reusable prompt from session history")
	flag.StringVar(&envName, "e", "", "environment to use (default: first in config)")
	flag.StringVar(&envName, "env", "", "environment to use (default: first in config)")
	flag.BoolVar(&listEnvs, "E", false, "list configured environments")
	flag.BoolVar(&listEnvs, "envs", false, "list configured environments")
	flag.BoolVar(&verbose, "v", false, "verbose: log HTTP requests and responses to stderr")
	flag.BoolVar(&verbose, "verbose", false, "verbose: log HTTP requests and responses to stderr")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: goshai [flags] [prompt...]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  -f, -file <path>    file to include as context (repeatable)\n")
		fmt.Fprintf(os.Stderr, "  -p, -prompt <name>  named system prompt\n")
		fmt.Fprintf(os.Stderr, "  -m, -model <name>   model name, alias, or prefix\n")
		fmt.Fprintf(os.Stderr, "  -u, -url <url>      server URL override\n")
		fmt.Fprintf(os.Stderr, "  -t, -token <tok>    auth token override\n")
		fmt.Fprintf(os.Stderr, "  -n, -no-stream      disable streaming\n")
		fmt.Fprintf(os.Stderr, "  -thinking           enable extended thinking / reasoning\n")
		fmt.Fprintf(os.Stderr, "  -thinking-budget N  token budget for thinking (default %d)\n", defaultThinkingBudget)
		fmt.Fprintf(os.Stderr, "  -e, -env <name>     select named environment from config\n")
		fmt.Fprintf(os.Stderr, "  -E, -envs           list configured environments\n")
		fmt.Fprintf(os.Stderr, "  -v, -verbose        log HTTP requests and responses to stderr\n")
		fmt.Fprintf(os.Stderr, "  -P, -prompts        list available named prompts\n")
		fmt.Fprintf(os.Stderr, "  -M, -models         list available models (requires server URL)\n")
		fmt.Fprintf(os.Stderr, "  -A, -aliases        list model aliases\n")
		fmt.Fprintf(os.Stderr, "  -a, -alias <k=v>    set a model alias (e.g. -a mini=gpt-4o-mini)\n")
		fmt.Fprintf(os.Stderr, "  -W, -write-config   save config and create default prompts.yaml if missing\n")
		fmt.Fprintf(os.Stderr, "  -R, -read-config    print full configuration for the selected environment (token included)\n")
		fmt.Fprintf(os.Stderr, "  -s, -session <name> continue named session (default: save to 'last')\n")
		fmt.Fprintf(os.Stderr, "  -r, -rename <name>  rename 'last' session to a new name\n")
		fmt.Fprintf(os.Stderr, "  -S, -sessions       list available sessions\n")
		fmt.Fprintf(os.Stderr, "  -G, -gen-prompt     generate reusable prompt from session history\n")
		fmt.Fprintf(os.Stderr, "\nCurrent configuration:\n")
		fmt.Fprintf(os.Stderr, "  config:  %s\n", configFilePath(dir, "config.yaml"))
		fmt.Fprintf(os.Stderr, "  prompts: %s\n", configFilePath(dir, "prompts.yaml"))
		if earlyEnv != "" {
			fmt.Fprintf(os.Stderr, "  env:     %s\n", earlyEnv)
		}
		fmt.Fprintf(os.Stderr, "  url:     %s\n", strOrDefault(cfg.URL, "(not set)"))
		fmt.Fprintf(os.Stderr, "  model:   %s\n", strOrDefault(cfg.Model, "(not set)"))
		fmt.Fprintf(os.Stderr, "  stream:  %v\n", !cfg.NoStream)
		fmt.Fprintf(os.Stderr, "  think:   %v\n", cfg.Think)
	}

	flag.Parse()

	if earlyConfigErr != nil && !writeConfig && !readConfig && !listEnvs {
		log.Fatal("config error: ", earlyConfigErr)
	}

	if listEnvs {
		envs, err := ListConfigs()
		if err != nil {
			log.Fatal("config error: ", err)
		}
		if len(envs) == 0 {
			fmt.Println("(no environments configured)")
			return
		}
		for i, e := range envs {
			name := e.Name
			if name == "" {
				name = "(default)"
			}
			marker := "  "
			if i == 0 {
				marker = "* "
			}
			fmt.Printf("%s%-20s  %-40s  %s\n", marker, name, strOrDefault(e.URL, "(no url)"), strOrDefault(e.Model, "(no model)"))
		}
		return
	}

	// If -e was specified after flag.Parse but differs from the pre-scanned value,
	// reload the config for the correct environment.
	if envName != earlyEnv {
		cfg, err = LoadConfig(envName)
		if err != nil {
			if (writeConfig || readConfig) && isEnvNotFound(err) {
				cfg = Config{Name: envName}
			} else {
				log.Fatal("config error: ", err)
			}
		}
	} else if earlyConfigErr != nil && (writeConfig || readConfig) {
		cfg = Config{Name: envName}
	}

	if readConfig {
		name := cfg.Name
		if name == "" {
			name = "(default)"
		}
		fmt.Printf("env:            %s\n", name)
		fmt.Printf("url:            %s\n", strOrDefault(cfg.URL, "(not set)"))
		fmt.Printf("token:          %s\n", strOrDefault(cfg.Token, "(not set)"))
		fmt.Printf("model:          %s\n", strOrDefault(cfg.Model, "(not set)"))
		fmt.Printf("prompt:         %s\n", strOrDefault(cfg.Prompt, "(not set)"))
		fmt.Printf("stream:         %v\n", !cfg.NoStream)
		fmt.Printf("think:          %v\n", cfg.Think)
		if cfg.ThinkingBudget != 0 {
			fmt.Printf("thinking-budget: %d\n", cfg.ThinkingBudget)
		}
		return
	}

	prompts, err := LoadPrompts()
	if err != nil {
		log.Fatal("prompts error: ", err)
	}

	if listSessions {
		sessions, err := ListSessions()
		if err != nil {
			log.Fatal("sessions error: ", err)
		}
		if len(sessions) == 0 {
			fmt.Println("(no sessions saved)")
			return
		}
		for _, s := range sessions {
			fmt.Printf("  %-20s  %3d messages  %s\n", s.Name, s.Messages, s.Modified.Format("2006-01-02 15:04"))
		}
		return
	}

	if renameTo != "" {
		if err := RenameSession(defaultSessionName, renameTo); err != nil {
			log.Fatal("rename error: ", err)
		}
		fmt.Printf("renamed '%s' → '%s'\n", defaultSessionName, renameTo)
		return
	}

	if listPrompts {
		if len(prompts) == 0 {
			fmt.Println("(no prompts configured)")
			return
		}
		names := make([]string, 0, len(prompts))
		for k := range prompts {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("  %-20s %s\n", name, prompts[name])
		}
		return
	}

	if listAliases {
		aliases, err := LoadAliases()
		if err != nil {
			log.Fatal("aliases error: ", err)
		}
		if len(aliases) == 0 {
			fmt.Println("(no aliases configured)")
			return
		}
		names := make([]string, 0, len(aliases))
		for k := range aliases {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("  %-20s %s\n", name, aliases[name])
		}
		return
	}

	if setAlias != "" {
		parts := strings.SplitN(setAlias, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			log.Fatal("alias format must be alias=model-name (e.g. -a mini=gpt-4o-mini)")
		}
		aliases, err := LoadAliases()
		if err != nil {
			log.Fatal("aliases error: ", err)
		}
		aliases[parts[0]] = parts[1]
		if err := SaveAliases(aliases); err != nil {
			log.Fatal("save aliases error: ", err)
		}
		fmt.Printf("alias %q → %q saved\n", parts[0], parts[1])
		return
	}

	// Merge: flag > config > default
	modelFlag := model // remember whether -m was explicitly given
	if serverURL == "" {
		serverURL = cfg.URL
	}
	if token == "" {
		token = cfg.Token
	}
	if model == "" {
		model = cfg.Model
	}
	if promptName == "" {
		promptName = cfg.Prompt
	}
	if promptName == "" {
		promptName = "default"
	}
	if !noStream {
		noStream = cfg.NoStream
	}
	if !thinking {
		thinking = cfg.Think
	}
	if thinkingBudget == 0 {
		thinkingBudget = cfg.ThinkingBudget
	}

	if writeConfig {
		effective := Config{
			Name:           envName,
			URL:            serverURL,
			Token:          token,
			Model:          model,
			Prompt:         promptName,
			NoStream:       noStream,
			Think:          thinking,
			ThinkingBudget: thinkingBudget,
		}
		if err := SaveConfig(effective); err != nil {
			log.Fatal("write config error: ", err)
		}
		if err := SaveDefaultPrompts(); err != nil {
			log.Fatal("write prompts error: ", err)
		}
		return
	}

	if serverURL == "" {
		log.Fatal("no server URL configured; use -u flag or set url in config.yaml")
	}

	client := NewClient(serverURL, token, verbose)

	if listModels {
		modelInfos, err := client.ListModels(context.Background())
		if err != nil {
			log.Fatal("models error: ", err)
		}
		// Build reverse map: full model ID → alias name
		reverseAliases := map[string]string{}
		if aliases, err := LoadAliases(); err == nil {
			for alias, full := range aliases {
				reverseAliases[full] = alias
			}
		}
		ids := make([]string, 0, len(modelInfos))
		for _, m := range modelInfos {
			ids = append(ids, m.ID)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if alias, ok := reverseAliases[id]; ok {
				fmt.Printf("  %s (%s)\n", id, alias)
			} else {
				fmt.Println(" ", id)
			}
		}
		return
	}

	// Resolve model: alias lookup first, then fuzzy match against server model list
	// if -m was explicitly given and the alias didn't resolve it.
	aliasResolved := false
	if model != "" {
		if aliases, err := LoadAliases(); err == nil {
			if full, ok := aliases[model]; ok {
				model = full
				aliasResolved = true
			}
		}
	}
	if modelFlag != "" && !aliasResolved {
		if resolved, ok := fuzzyMatchModel(context.Background(), client, serverURL, model); ok {
			model = resolved
		}
	}

	if model == "" {
		log.Fatal("no model configured; use -m flag or set model in config.yaml")
	}

	if genPrompt {
		name := defaultSessionName
		if sessionName != "" {
			name = sessionName
		}
		genMsgs, err := LoadSession(name)
		if err != nil {
			log.Fatal("session error: ", err)
		}
		if len(genMsgs) == 0 {
			log.Fatalf("session %q is empty or does not exist", name)
		}
		cleaned := stripFileBlocks(genMsgs)
		metaMessages := []Message{
			{Role: RoleSystem, Content: "You are an expert at distilling conversations into clear, reusable prompts."},
			{Role: RoleUser, Content: buildGenPromptRequest(cleaned)},
		}
		content, err := client.ChatCompletion(context.Background(), model, metaMessages, ChatOptions{})
		if err != nil {
			log.Fatal("API error: ", err)
		}
		fmt.Println(content)
		return
	}

	// Resolve user prompt: positional args take precedence; fall back to stdin if piped.
	var userPrompt string
	if args := flag.Args(); len(args) > 0 {
		userPrompt = strings.Join(args, " ")
	} else {
		// Check if stdin is a pipe/redirect (not an interactive terminal).
		info, err := os.Stdin.Stat()
		if err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				log.Fatal("stdin read error: ", err)
			}
			userPrompt = strings.TrimRight(string(data), "\n")
		}
	}

	if len(files) == 0 && userPrompt == "" {
		flag.Usage()
		os.Exit(1)
	}

	systemPrompt := prompts[promptName]
	if promptName != "default" && systemPrompt == "" {
		fmt.Fprintf(os.Stderr, "warning: prompt %q not found, proceeding without system prompt\n", promptName)
	}

	// Determine which session to save to; load history if continuing a named session.
	saveAs := defaultSessionName
	var messages []Message

	if sessionName != "" {
		saveAs = sessionName
		messages, err = LoadSession(sessionName)
		if err != nil {
			log.Fatal("session error: ", err)
		}
		// New named session: prepend system prompt if one was resolved.
		if len(messages) == 0 && systemPrompt != "" {
			messages = append(messages, Message{Role: RoleSystem, Content: systemPrompt})
		}
		userMsg, err := buildUserMessage(files, userPrompt)
		if err != nil {
			log.Fatal(err)
		}
		messages = append(messages, userMsg)
	} else {
		// No session flag: start fresh, will be saved to "last".
		messages, err = BuildMessages(systemPrompt, files, userPrompt)
		if err != nil {
			log.Fatal(err)
		}
	}

	opts := ChatOptions{Think: thinking, ThinkingBudget: thinkingBudget}

	if noStream {
		content, err := client.ChatCompletion(context.Background(), model, messages, opts)
		if err != nil {
			log.Fatal("API error: ", err)
		}
		fmt.Println(content)
		messages = append(messages, Message{Role: RoleAssistant, Content: content})
		if err := SaveSession(saveAs, messages); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
		}
		return
	}

	stream, err := client.ChatCompletionStream(context.Background(), model, messages, opts)
	if err != nil {
		log.Fatal("API error: ", err)
	}
	defer stream.Close()

	var sb strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal("stream error: ", err)
		}
		fmt.Print(chunk)
		sb.WriteString(chunk)
	}
	fmt.Println()

	messages = append(messages, Message{Role: RoleAssistant, Content: sb.String()})
	if err := SaveSession(saveAs, messages); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
	}
}
