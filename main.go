package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// multiFlag supports repeatable -f flags.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// findEnvArg scans args for the value of -e / -env before flag.Parse.
func findEnvArg(args []string) string {
	for i, a := range args {
		switch {
		case (a == "-e" || a == "-env" || a == "--env") && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "-e="):
			return a[3:]
		case strings.HasPrefix(a, "-env="):
			return a[5:]
		case strings.HasPrefix(a, "--env="):
			return a[6:]
		}
	}
	return ""
}

func main() {
	var (
		files        multiFlag
		promptName   string
		model        string
		serverURL    string
		token        string
		listPrompts  bool
		listModels   bool
		listEnvs     bool
		writeConfig  bool
		noStream     bool
		sessionName  string
		renameTo     string
		listSessions bool
		genPrompt    bool
		envName      string
	)

	// Pre-scan for -e so LoadConfig can select the right environment
	// before flag.Parse (needed so flag.Usage shows the correct settings).
	earlyEnv := findEnvArg(os.Args[1:])

	// Load config before flag.Parse so flag.Usage can show current settings.
	cfg, err := LoadConfig(earlyEnv)
	if err != nil {
		log.Fatal("config error: ", err)
	}
	dir, _ := configDir() // best-effort; empty string if unavailable

	flag.Var(&files, "f", "file to include as context (repeatable)")
	flag.Var(&files, "file", "file to include as context (repeatable)")
	flag.StringVar(&promptName, "p", "", "named system prompt")
	flag.StringVar(&promptName, "prompt", "", "named system prompt")
	flag.StringVar(&model, "m", "", "model name override")
	flag.StringVar(&model, "model", "", "model name override")
	flag.StringVar(&serverURL, "u", "", "server URL override")
	flag.StringVar(&serverURL, "url", "", "server URL override")
	flag.StringVar(&token, "t", "", "auth token override")
	flag.StringVar(&token, "token", "", "auth token override")
	flag.BoolVar(&listPrompts, "P", false, "list available named prompts")
	flag.BoolVar(&listPrompts, "prompts", false, "list available named prompts")
	flag.BoolVar(&listModels, "M", false, "list available models (requires server URL)")
	flag.BoolVar(&listModels, "models", false, "list available models (requires server URL)")
	flag.BoolVar(&noStream, "n", false, "disable streaming (non-streaming mode)")
	flag.BoolVar(&noStream, "no-stream", false, "disable streaming (non-streaming mode)")
	flag.BoolVar(&writeConfig, "W", false, "write current configuration to config.yaml and create default prompts.yaml")
	flag.BoolVar(&writeConfig, "write-config", false, "write current configuration to config.yaml and create default prompts.yaml")
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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: goshai [flags] [prompt...]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  -f, -file <path>    file to include as context (repeatable)\n")
		fmt.Fprintf(os.Stderr, "  -p, -prompt <name>  named system prompt\n")
		fmt.Fprintf(os.Stderr, "  -m, -model <name>   model name override\n")
		fmt.Fprintf(os.Stderr, "  -u, -url <url>      server URL override\n")
		fmt.Fprintf(os.Stderr, "  -t, -token <tok>    auth token override\n")
		fmt.Fprintf(os.Stderr, "  -n, -no-stream      disable streaming\n")
		fmt.Fprintf(os.Stderr, "  -e, -env <name>     select named environment from config\n")
		fmt.Fprintf(os.Stderr, "  -E, -envs           list configured environments\n")
		fmt.Fprintf(os.Stderr, "  -P, -prompts        list available named prompts\n")
		fmt.Fprintf(os.Stderr, "  -M, -models         list available models (requires server URL)\n")
		fmt.Fprintf(os.Stderr, "  -W, -write-config   save config and create default prompts.yaml if missing\n")
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
	}

	flag.Parse()

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
			log.Fatal("config error: ", err)
		}
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

	// Merge: flag > config > default
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

	if writeConfig {
		effective := Config{
			Name:     envName,
			URL:      serverURL,
			Token:    token,
			Model:    model,
			Prompt:   promptName,
			NoStream: noStream,
		}
		if err := SaveConfig(effective); err != nil {
			log.Fatal("write config error: ", err)
		}
		if err := SaveDefaultPrompts(); err != nil {
			log.Fatal("write prompts error: ", err)
		}
		return
	}

	if listModels {
		if serverURL == "" {
			log.Fatal("no server URL configured; use -u flag or set url in config.yaml")
		}
		oaiCfg := openai.DefaultConfig(token)
		oaiCfg.BaseURL = serverURL
		client := openai.NewClientWithConfig(oaiCfg)
		modelList, err := client.ListModels(context.Background())
		if err != nil {
			log.Fatal("models error: ", err)
		}
		models := make([]string, 0, len(modelList.Models))
		for _, m := range modelList.Models {
			models = append(models, m.ID)
		}
		sort.Strings(models)
		for _, id := range models {
			fmt.Println(" ", id)
		}
		return
	}

	if serverURL == "" {
		log.Fatal("no server URL configured; use -u flag or set url in config.yaml")
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
		metaMessages := []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "You are an expert at distilling conversations into clear, reusable prompts.",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: buildGenPromptRequest(cleaned),
			},
		}
		oaiCfg := openai.DefaultConfig(token)
		oaiCfg.BaseURL = serverURL
		client := openai.NewClientWithConfig(oaiCfg)
		resp, err := client.CreateChatCompletion(context.Background(), openai.ChatCompletionRequest{
			Model:    model,
			Messages: metaMessages,
		})
		if err != nil {
			log.Fatal("API error: ", err)
		}
		if len(resp.Choices) > 0 {
			fmt.Println(resp.Choices[0].Message.Content)
		}
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
	var messages []openai.ChatCompletionMessage

	if sessionName != "" {
		saveAs = sessionName
		messages, err = LoadSession(sessionName)
		if err != nil {
			log.Fatal("session error: ", err)
		}
		// New named session: prepend system prompt if one was resolved.
		if len(messages) == 0 && systemPrompt != "" {
			messages = append(messages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: systemPrompt,
			})
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

	oaiCfg := openai.DefaultConfig(token)
	oaiCfg.BaseURL = serverURL
	client := openai.NewClientWithConfig(oaiCfg)

	if noStream {
		resp, err := client.CreateChatCompletion(
			context.Background(),
			openai.ChatCompletionRequest{
				Model:    model,
				Messages: messages,
			},
		)
		if err != nil {
			log.Fatal("API error: ", err)
		}
		if len(resp.Choices) > 0 {
			content := resp.Choices[0].Message.Content
			fmt.Println(content)
			messages = append(messages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleAssistant,
				Content: content,
			})
			if err := SaveSession(saveAs, messages); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
			}
		}
		return
	}

	stream, err := client.CreateChatCompletionStream(
		context.Background(),
		openai.ChatCompletionRequest{
			Model:    model,
			Messages: messages,
			Stream:   true,
		},
	)
	if err != nil {
		log.Fatal("API error: ", err)
	}
	defer stream.Close()

	var sb strings.Builder
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Fatal("stream error: ", err)
		}
		if len(resp.Choices) > 0 {
			chunk := resp.Choices[0].Delta.Content
			fmt.Print(chunk)
			sb.WriteString(chunk)
		}
	}
	fmt.Println()

	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: sb.String(),
	})
	if err := SaveSession(saveAs, messages); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
	}
}
