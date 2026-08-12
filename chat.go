package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// RunChat runs the plain chat loop (no tool calling). When repl is true, it reads
// additional turns from stdin after each response, REPL-style; otherwise it answers
// the pending turn once and returns.
//
// messages seeds the conversation (e.g. a system prompt, and optionally a trailing
// unanswered user message when an initial prompt or piped stdin was supplied). saveAs,
// when non-empty, persists the session after each completed turn.
func RunChat(ctx context.Context, client *Client, model string, messages []Message, opts ChatOptions, saveAs string, repl bool, noStream bool) error {
	scanner := bufio.NewScanner(os.Stdin)
	pending := len(messages) > 0 && messages[len(messages)-1].Role == RoleUser
	if repl && !pending {
		fmt.Print("> ")
	}

	var total Usage
	var requests int
	rc := &replCommands{ctx: ctx, client: client, model: &model, opts: &opts, messages: &messages, total: &total, requests: &requests, saveAs: &saveAs}

	for pending || (repl && scanner.Scan()) {
		if !pending {
			input := scanner.Text()
			if strings.TrimSpace(input) == "" {
				fmt.Print("> ")
				continue
			}
			if isCommand(input) {
				if rc.dispatch(input) {
					return nil
				}
				if saveAs != "" {
					if err := SaveSession(saveAs, messages); err != nil {
						fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
					}
				}
				fmt.Print("> ")
				continue
			}
			userMsg, err := buildUserMessage(nil, input)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				fmt.Print("> ")
				continue
			}
			messages = append(messages, userMsg)
		}
		pending = false

		content, usage, err := runChatTurn(ctx, client, model, messages, opts, noStream)
		if err != nil {
			return err
		}
		messages = append(messages, Message{Role: RoleAssistant, Content: content})

		total.Add(usage)
		requests++
		if opts.Stats && repl {
			printTotalStats(os.Stderr, total, requests)
		}

		if saveAs != "" {
			if err := SaveSession(saveAs, messages); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
			}
		}

		if !repl {
			return nil
		}

		fmt.Print("> ")
	}

	return scanner.Err()
}

// runChatTurn sends messages to the model and prints the reply, returning its content and usage.
func runChatTurn(ctx context.Context, client *Client, model string, messages []Message, opts ChatOptions, noStream bool) (string, Usage, error) {
	start := time.Now()

	if noStream {
		content, usage, err := client.ChatCompletion(ctx, model, messages, opts)
		if err != nil {
			return "", Usage{}, fmt.Errorf("API error: %w", err)
		}
		fmt.Println(content)
		if opts.Stats {
			printStats(os.Stderr, usage, time.Since(start), 0)
		}
		return content, usage, nil
	}

	stream, err := client.ChatCompletionStream(ctx, model, messages, opts)
	if err != nil {
		return "", Usage{}, fmt.Errorf("API error: %w", err)
	}
	defer stream.Close()

	var sb strings.Builder
	var ttft time.Duration
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", Usage{}, fmt.Errorf("stream error: %w", err)
		}
		if ttft == 0 && chunk != "" {
			ttft = time.Since(start)
		}
		fmt.Print(chunk)
		sb.WriteString(chunk)
	}
	fmt.Println()

	if opts.Stats {
		printStats(os.Stderr, stream.Usage, time.Since(start), ttft)
	}
	return sb.String(), stream.Usage, nil
}
