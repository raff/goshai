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

func main() {
	var (
		files       multiFlag
		promptName  string
		model       string
		serverURL   string
		token       string
		listPrompts bool
		writeConfig bool
		noStream    bool
	)

	// Load config before flag.Parse so flag.Usage can show current settings.
	cfg, err := LoadConfig()
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
	flag.BoolVar(&listPrompts, "l", false, "list available named prompts")
	flag.BoolVar(&listPrompts, "list", false, "list available named prompts")
	flag.BoolVar(&noStream, "n", false, "disable streaming (non-streaming mode)")
	flag.BoolVar(&noStream, "no-stream", false, "disable streaming (non-streaming mode)")
	flag.BoolVar(&writeConfig, "W", false, "write current configuration to config.yaml and create default prompts.yaml")
	flag.BoolVar(&writeConfig, "write-config", false, "write current configuration to config.yaml and create default prompts.yaml")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: goshai [flags] [prompt...]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  -f, -file <path>    file to include as context (repeatable)\n")
		fmt.Fprintf(os.Stderr, "  -p, -prompt <name>  named system prompt\n")
		fmt.Fprintf(os.Stderr, "  -m, -model <name>   model name override\n")
		fmt.Fprintf(os.Stderr, "  -u, -url <url>      server URL override\n")
		fmt.Fprintf(os.Stderr, "  -t, -token <tok>    auth token override\n")
		fmt.Fprintf(os.Stderr, "  -n, -no-stream      disable streaming\n")
		fmt.Fprintf(os.Stderr, "  -l, -list           list available named prompts\n")
		fmt.Fprintf(os.Stderr, "  -W, -write-config   save config and create default prompts.yaml if missing\n")
		fmt.Fprintf(os.Stderr, "\nCurrent configuration:\n")
		fmt.Fprintf(os.Stderr, "  config:  %s\n", configFilePath(dir, "config.yaml"))
		fmt.Fprintf(os.Stderr, "  prompts: %s\n", configFilePath(dir, "prompts.yaml"))
		fmt.Fprintf(os.Stderr, "  url:     %s\n", strOrDefault(cfg.URL, "(not set)"))
		fmt.Fprintf(os.Stderr, "  model:   %s\n", strOrDefault(cfg.Model, "(not set)"))
	}

	flag.Parse()

	prompts, err := LoadPrompts()
	if err != nil {
		log.Fatal("prompts error: ", err)
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

	if serverURL == "" {
		log.Fatal("no server URL configured; use -u flag or set url in config.yaml")
	}
	if model == "" {
		log.Fatal("no model configured; use -m flag or set model in config.yaml")
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

	messages, err := BuildMessages(systemPrompt, files, userPrompt)
	if err != nil {
		log.Fatal(err)
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
			fmt.Println(resp.Choices[0].Message.Content)
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

	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Fatal("stream error: ", err)
		}
		if len(resp.Choices) > 0 {
			fmt.Print(resp.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
}
