package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
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
	saveAs   string // default session name for /save with no argument
}

// commandHelp lists the recognized commands and their descriptions, in display order.
var commandHelp = []struct {
	name string
	help string
}{
	{"model", "show current model; /model <name> switches it; /model list shows available models"},
	{"system", "show the system prompt; /system <text> replaces it"},
	{"thinking", "show thinking status; /thinking on|off|<budget> toggles extended reasoning"},
	{"stats", "print running session token totals; /stats on|off toggles per-request stats"},
	{"history", "print the full conversation history (alias: /messages)"},
	{"undo", "remove the last user turn and its reply"},
	{"save", "save the session; /save <name> saves under a name (default: active session)"},
	{"reset", "clear conversation history back to just the system prompt"},
	{"help", "list available commands"},
	{"exit", "leave the REPL (alias: /quit)"},
}

// commandAliases maps alternate command spellings to their canonical name.
var commandAliases = map[string]string{
	"stat":     "stats",
	"messages": "history",
	"quit":     "exit",
}

// canonicalCommand resolves a possibly-aliased command name to its canonical form.
func canonicalCommand(name string) string {
	if canon, ok := commandAliases[name]; ok {
		return canon
	}
	return name
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
	name = canonicalCommand(name)
	for _, c := range commandHelp {
		if name == c.name {
			return true
		}
	}
	return false
}

// dispatch executes a recognized slash command. Callers should check isCommand
// first; input for any other command name is a no-op. Returns true when the
// REPL should terminate (i.e. /exit or /quit).
func (r *replCommands) dispatch(input string) (quit bool) {
	name, rest := splitCommand(input)
	switch canonicalCommand(name) {
	case "model":
		r.cmdModel(rest)
	case "system":
		r.cmdSystem(rest)
	case "thinking":
		r.cmdThinking(rest)
	case "stats":
		r.cmdStats(rest)
	case "history":
		r.cmdHistory()
	case "undo":
		r.cmdUndo()
	case "save":
		r.cmdSave(rest)
	case "reset":
		r.cmdReset()
	case "help":
		r.cmdHelp()
	case "exit":
		r.cmdExit()
		return true
	}
	return false
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

func (r *replCommands) cmdThinking(rest string) {
	switch rest {
	case "":
		if r.opts.Think {
			budget := r.opts.ThinkingBudget
			if budget == 0 {
				budget = defaultThinkingBudget
			}
			fmt.Fprintf(os.Stderr, "thinking: on (budget=%d)\n", budget)
		} else {
			fmt.Fprintln(os.Stderr, "thinking: off")
		}
	case "on":
		r.opts.Think = true
		fmt.Fprintln(os.Stderr, "thinking: on")
	case "off":
		r.opts.Think = false
		fmt.Fprintln(os.Stderr, "thinking: off")
	default:
		n, err := strconv.Atoi(rest)
		if err != nil || n <= 0 {
			fmt.Fprintln(os.Stderr, "usage: /thinking [on|off|<budget>]")
			return
		}
		r.opts.Think = true
		r.opts.ThinkingBudget = n
		fmt.Fprintf(os.Stderr, "thinking: on (budget=%d)\n", n)
	}
}

func (r *replCommands) cmdHistory() {
	messages := *r.messages
	if len(messages) == 0 {
		fmt.Fprintln(os.Stderr, "(empty)")
		return
	}
	for i, m := range messages {
		content := m.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		fmt.Fprintf(os.Stderr, "[%d] %s: %s\n", i, m.Role, content)
		if len(m.ToolCalls) > 0 {
			fmt.Fprintf(os.Stderr, "     (%d tool call(s))\n", len(m.ToolCalls))
		}
	}
}

func (r *replCommands) cmdUndo() {
	messages := *r.messages
	lastUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			lastUser = i
			break
		}
	}
	if lastUser == -1 {
		fmt.Fprintln(os.Stderr, "nothing to undo")
		return
	}
	removed := len(messages) - lastUser
	*r.messages = messages[:lastUser]
	fmt.Fprintf(os.Stderr, "removed last turn (%d message(s))\n", removed)
}

func (r *replCommands) cmdSave(rest string) {
	name := rest
	if name == "" {
		name = r.saveAs
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "usage: /save <name> (no active session to save to)")
		return
	}
	if err := SaveSession(name, *r.messages); err != nil {
		fmt.Fprintf(os.Stderr, "save error: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "session saved as %q\n", name)
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

func (r *replCommands) cmdExit() {
	fmt.Fprintln(os.Stderr, "bye")
}
