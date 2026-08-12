package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// replCommands dispatches "/"-prefixed REPL lines. It holds pointers into the
// caller's loop state so commands can inspect or mutate the live session
// (current model, chat options, message history, running token totals).
type replCommands struct {
	ctx      context.Context
	client   *Client
	model    *string
	opts     *ChatOptions
	messages *[]Message
	total    *Usage
	requests *int
}

// commandHelp lists the recognized commands and their descriptions, in display order.
var commandHelp = []struct {
	name string
	help string
}{
	{"model", "show current model; /model <name> switches it; /model list shows available models"},
	{"stats", "print running session token totals; /stats on|off toggles per-request stats"},
	{"system", "show the system prompt; /system <text> replaces it"},
	{"reset", "clear conversation history back to just the system prompt"},
	{"help", "list available commands"},
}

// splitCommand parses a "/name rest..." line into its command name (without the
// leading slash) and the remaining trimmed argument text. Returns ("", "") if
// input isn't slash-prefixed.
func splitCommand(input string) (name, rest string) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return "", ""
	}
	parts := strings.SplitN(input[1:], " ", 2)
	name = parts[0]
	if len(parts) > 1 {
		rest = strings.TrimSpace(parts[1])
	}
	return name, rest
}

// isCommand reports whether input's command name matches a known REPL command.
func isCommand(input string) bool {
	name, _ := splitCommand(input)
	if name == "" {
		return false
	}
	if name == "stat" {
		return true
	}
	for _, c := range commandHelp {
		if name == c.name {
			return true
		}
	}
	return false
}

// dispatch executes a recognized slash command. Callers should check isCommand
// first; input for any other command name is a no-op.
func (r *replCommands) dispatch(input string) {
	name, rest := splitCommand(input)
	switch name {
	case "model":
		r.cmdModel(rest)
	case "stats", "stat":
		r.cmdStats(rest)
	case "system":
		r.cmdSystem(rest)
	case "reset":
		r.cmdReset()
	case "help":
		r.cmdHelp()
	}
}

func (r *replCommands) cmdModel(rest string) {
	switch rest {
	case "":
		fmt.Fprintf(os.Stderr, "current model: %s\n", *r.model)
	case "list":
		if err := printModelList(r.ctx, r.client, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "models error: %v\n", err)
		}
	default:
		*r.model = resolveModelName(r.ctx, r.client, rest)
		fmt.Fprintf(os.Stderr, "model set to: %s\n", *r.model)
	}
}

func (r *replCommands) cmdStats(rest string) {
	switch rest {
	case "":
		printTotalStats(os.Stderr, *r.total, *r.requests)
	case "on":
		r.opts.Stats = true
		fmt.Fprintln(os.Stderr, "stats: on")
	case "off":
		r.opts.Stats = false
		fmt.Fprintln(os.Stderr, "stats: off")
	default:
		fmt.Fprintln(os.Stderr, "usage: /stats [on|off]")
	}
}

func (r *replCommands) cmdSystem(rest string) {
	messages := *r.messages
	hasSystem := len(messages) > 0 && messages[0].Role == RoleSystem

	if rest == "" {
		if hasSystem {
			fmt.Fprintln(os.Stderr, messages[0].Content)
		} else {
			fmt.Fprintln(os.Stderr, "(no system prompt set)")
		}
		return
	}

	if hasSystem {
		messages[0].Content = rest
	} else {
		messages = append([]Message{{Role: RoleSystem, Content: rest}}, messages...)
	}
	*r.messages = messages
	fmt.Fprintln(os.Stderr, "system prompt updated")
}

func (r *replCommands) cmdReset() {
	messages := *r.messages
	if len(messages) > 0 && messages[0].Role == RoleSystem {
		*r.messages = messages[:1]
	} else {
		*r.messages = nil
	}
	fmt.Fprintln(os.Stderr, "session reset (history cleared)")
}

func (r *replCommands) cmdHelp() {
	fmt.Fprintln(os.Stderr, "available commands:")
	for _, c := range commandHelp {
		fmt.Fprintf(os.Stderr, "  /%-8s %s\n", c.name, c.help)
	}
}
