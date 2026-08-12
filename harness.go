package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// shellTool is the single tool offered to the model in harness mode: run a shell
// command on the local machine and get back its combined output and exit code.
var shellTool = ToolDef{
	Type: "function",
	Function: ToolFunctionDef{
		Name:        "sh",
		Description: "Execute a shell command on the local machine and return its combined stdout/stderr and exit code.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"command": {
					"type": "string",
					"description": "The shell command to run via /bin/sh -c."
				}
			},
			"required": ["command"]
		}`),
	},
}

// shellArgs is the expected JSON shape of the "sh" tool call arguments.
type shellArgs struct {
	Command string `json:"command"`
}

// runShellTool executes a shell command and formats the result the way the model expects it back.
func runShellTool(command string) string {
	cmd := exec.Command("/bin/sh", "-c", command)
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return fmt.Sprintf("exit %d\n%s", exitCode, output)
}

// RunHarness runs the harness loop, in which the model can call the "sh" tool to
// execute shell commands on the local machine, observe their output, and continue
// reasoning until it produces a plain-text reply. When repl is true, it then reads
// additional turns from stdin, REPL-style; otherwise it answers the pending turn
// once and returns.
//
// messages seeds the conversation (e.g. a system prompt, and optionally a trailing
// unanswered user message when an initial prompt or piped stdin was supplied). saveAs,
// when non-empty, persists the session after each completed turn.
//
// WARNING: the model can execute arbitrary shell commands with no confirmation step.
// Only use harness mode with a trusted model/server.
func RunHarness(ctx context.Context, client *Client, model string, messages []Message, opts ChatOptions, saveAs string, repl bool) error {
	opts.Tools = []ToolDef{shellTool}

	if repl {
		fmt.Fprintln(os.Stderr, "harness mode: the model can execute shell commands on this machine without confirmation.")
	}

	scanner := bufio.NewScanner(os.Stdin)
	pending := len(messages) > 0 && messages[len(messages)-1].Role == RoleUser
	if repl && !pending {
		fmt.Print("> ")
	}

	for pending || (repl && scanner.Scan()) {
		if !pending {
			input := scanner.Text()
			if strings.TrimSpace(input) == "" {
				fmt.Print("> ")
				continue
			}
			messages = append(messages, Message{Role: RoleUser, Content: input})
		}
		pending = false

		for {
			start := time.Now()
			reply, usage, err := client.ChatCompletionRaw(ctx, model, messages, opts)
			elapsed := time.Since(start)
			if err != nil {
				return fmt.Errorf("API error: %w", err)
			}
			messages = append(messages, reply)

			if len(reply.ToolCalls) == 0 {
				fmt.Println(reply.Content)
				if opts.Stats {
					printStats(os.Stderr, usage, elapsed, 0)
				}
				break
			}

			for _, tc := range reply.ToolCalls {
				var args shellArgs
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					messages = append(messages, Message{
						Role:       RoleTool,
						ToolCallID: tc.ID,
						Name:       tc.Function.Name,
						Content:    fmt.Sprintf("error: invalid arguments: %v", err),
					})
					continue
				}
				fmt.Printf("$ %s\n", args.Command)
				result := runShellTool(args.Command)
				fmt.Print(result)
				messages = append(messages, Message{
					Role:       RoleTool,
					ToolCallID: tc.ID,
					Name:       tc.Function.Name,
					Content:    result,
				})
			}

			if saveAs != "" {
				if err := SaveSession(saveAs, messages); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not save session: %v\n", err)
				}
			}
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
