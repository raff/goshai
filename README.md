# goshai

A command-line client for interacting with OpenAI-compatible LLM servers (Ollama, LM Studio, vLLM, OpenAI, etc.).

## Features

- Ask questions from the terminal with a one-shot prompt
- Pipe a prompt from stdin: `echo "question" | goshai -f file.go`
- Include one or more files as context (`-f`): source files embedded as fenced blocks, images as base64 data URIs
- Embed files inline at any position in your prompt using `@filename` syntax
- Select named system prompts from a YAML file (`-p`)
- Server URL, auth token, and model stored in a config file
- Streams the response to stdout as tokens arrive
- Non-streaming mode (`-n`) for servers that don't support streaming
- Extended thinking / reasoning support (`-thinking`) for models that expose it
- Session history: conversations are saved automatically and can be continued by name
- Interactive REPL: run `goshai` with no prompt on the command line (and no piped stdin) to chat turn-by-turn; giving a prompt via args or stdin always answers once and exits
- Harness mode (`-H`): the same REPL, except the model can execute shell commands via a tool call, observe the output, and keep going until it produces a plain-text reply
- Generate a reusable prompt from any session (`-G`)
- Named environments in a single config file for switching between servers (`-e`, `-E`)
- Model aliases: define short names for long model IDs and manage them from the CLI (`-a`, `-A`)
- Fuzzy model matching: pass a prefix or substring to `-m` and goshai resolves the full model name from the server's list

## Installation

```bash
go install github.com/raff/goshai@latest
```

Or build from source:

```bash
git clone https://github.com/raff/goshai
cd goshai
go build -o goshai .
```

## Configuration

Config files are stored in the platform config directory (`~/Library/Application Support/goshai/` on macOS, `~/.config/goshai/` on Linux).

The easiest way to create them is to run `-write-config` with any flag overrides you want to save:

```bash
# Save URL, model, and token to config.yaml; create a starter prompts.yaml
goshai -u http://localhost:11434/v1 -m llama3.2 -W
```

### `config.yaml`

The config file supports two formats.

**Single environment (legacy):**

```yaml
url: "http://localhost:11434/v1"   # OpenAI-compatible server base URL
token: ""                          # auth token (empty = no auth, e.g. local Ollama)
model: "llama3.2"                  # default model
prompt: "default"                  # default named system prompt
nostream: false                    # set true for servers that don't support streaming
# think: true                      # uncomment to enable extended thinking / reasoning
# thinking-budget: 16000           # token budget for thinking (default 10000)
```

**Multiple named environments:**

```yaml
default: remote

local:
  url: "http://localhost:11434/v1"
  model: "llama3.2"

remote:
  url: "https://api.openai.com/v1"
  token: "${OPENAI_API_KEY}"
  model: "gpt-4o"

work:
  url: "${WORK_LLM_URL}"
  token: "${WORK_LLM_TOKEN}"
  model: "claude-sonnet-4-5"
  think: true
  thinking-budget: 16000
```

When using named environments, select one with `-e <name>`. If `-e` is omitted, the environment named by the top-level `default:` key is used; if no `default:` key is set, the first entry in the file is used. Use `-E` to list all configured environments and `-D <name>` to set the default.

String values (`url`, `token`, `model`, `prompt`) support environment variable expansion using `$VAR` or `${VAR}` syntax, useful for keeping secrets out of the config file.

**Model aliases** can be added as a top-level `aliases:` block in `config.yaml`. Aliases are global — they apply regardless of which environment is selected:

```yaml
aliases:
  llama: "llama3.2:latest"
  mini: "gpt-4o-mini"
  o1: "o1-preview"

local:
  url: "http://localhost:11434/v1"
  model: "llama3.2:latest"

remote:
  url: "https://api.openai.com/v1"
  token: "${OPENAI_API_KEY}"
  model: "gpt-4o"
```

Use `-a alias=model-name` to add or update an alias without editing the file directly, and `-A` to list all configured aliases.

### `prompts.yaml`

A map of named system prompts. Running `-W` creates this file automatically if it does not exist yet:

```yaml
# goshai named system prompts
# Select a prompt with the -p flag: goshai -p coder "your question"
#
default: "You are a helpful assistant."
coder: "You are an expert software engineer. Be concise and precise."
reviewer: "You are a senior code reviewer. Focus on bugs, edge cases, and improvements."
explainer: "You are a patient teacher. Explain concepts clearly with examples."
```

Both files are optional. Missing files are silently ignored.

## Usage

```
goshai [flags] [prompt...]

Flags:
  -f, -file <path>    file to include as context (repeatable)
  -p, -prompt <name>  named system prompt
  -m, -model <name>   model name, alias, or prefix (fuzzy-matched against server list)
  -u, -url <url>      server URL override
  -t, -token <tok>    auth token override
  -n, -no-stream      disable streaming (non-streaming mode)
  -thinking           enable extended thinking / reasoning
  -thinking-budget N  token budget for thinking (default 10000)
  -e, -env <name>     select named environment from config
  -E, -envs           list configured environments
  -D, -set-default <name>  set the default environment
  -P, -prompts        list available named prompts
  -M, -models         list available models (requires server URL)
  -A, -aliases        list model aliases
  -a, -alias <k=v>    set a model alias (e.g. -a mini=gpt-4o-mini)
  -W, -write-config   save config and create default prompts.yaml if missing
  -R, -read-config    print full configuration for the selected environment (token included)
  -s, -session <name> continue named session (default: save to 'last')
  -r, -rename <name>  rename 'last' session to a new name
  -S, -sessions       list available sessions
  -G, -gen-prompt     generate reusable prompt from session history
  -H, -harness        harness mode: interactive REPL, model can run shell commands (DANGEROUS)

Current configuration:
  config:  /path/to/config.yaml
  prompts: /path/to/prompts.yaml
  url:     http://localhost:11434/v1
  model:   llama3.2
  stream:  true
  think:   false
```

Running `goshai` with no arguments (or `-help`) always prints the current configuration block so you can verify which server and model are active.

The user prompt can also be supplied via stdin (pipe):

```bash
    echo "Explain this code" | goshai -f main.go
    cat question.txt | goshai
```

### Examples

```bash
    # Simple question
    goshai "What is the capital of France?"
    
    # Ask about a file
    goshai -f main.go "What does this file do?"
    
    # Multiple files
    goshai -f main.go -f config.go "How do these two files relate?"
    
    # Ask about an image (vision-capable model required)
    goshai -f screenshot.png "What error is shown in this screenshot?"

    # Mix image and code context
    goshai -f diagram.png -f main.go "Does the code match the architecture diagram?"

    # Inline @filename — embed files at a specific position in the prompt
    goshai "The error appears in @screenshot.png, here is the source @main.go — what's wrong?"

    # Use a named system prompt
    goshai -p coder -f main.go "Review this code"
    
    # Override server and model at runtime
    goshai -u http://localhost:11434/v1 -m llama3.2 "Explain recursion"
    
    # List available named prompts
    goshai -P

    # List available models
    goshai -M
    
    # Pipe a prompt via stdin
    echo "Explain this code" | goshai -f main.go
    
    # Non-streaming mode (for servers that don't support streaming)
    goshai -n "What is 2+2?"
    
    # First-time setup: save config and create starter prompts file
    goshai -u http://localhost:11434/v1 -m llama3.2 -W

    # List configured environments
    goshai -E

    # Use a named environment
    goshai -e remote "What is the capital of France?"

    # Save current flags as a named environment
    goshai -e local -u http://localhost:11434/v1 -m llama3.2 -W

    # Add a model alias
    goshai -a mini=gpt-4o-mini
    goshai -a llama=llama3.2:latest

    # List configured aliases
    goshai -A

    # Use an alias with -m
    goshai -m mini "Summarize this"

    # Fuzzy model match — resolves to the first model whose name starts with "llama"
    goshai -m llama "What is the capital of France?"

    # Enable extended thinking (model must support it)
    goshai -thinking "Solve this step by step: ..."

    # Enable thinking with a custom token budget
    goshai -thinking -thinking-budget 20000 -f problem.go "Find all edge cases"

    # Save a thinking-enabled environment
    goshai -e work -u https://api.example.com/v1 -m claude-sonnet-4-5 -thinking -W
```

### File context

**`-f` flag** — files are prepended before the user prompt. Text files become fenced code blocks; image files (`.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`) are base64-encoded and sent as `image_url` parts, which vision-capable models can read:

```bash
    # Ask about an image
    goshai -f screenshot.png "What error is shown?"

    # Mix text and image context
    goshai -f main.go -f diagram.png "Does the code match the diagram?"
```

**`@filename` syntax** — embed a file exactly where it appears in your prompt, instead of prepending it with `-f`. This is useful when order matters, such as comparing two files or pointing from a screenshot to the relevant source:

```bash
    goshai "Look at @screenshot.png — the relevant code is @main.go. What's wrong?"
    goshai "Compare @old.go with @new.go and summarize the changes"
    goshai "Review @cmd/server.go, then check @cmd/routes.go"
    goshai 'Review @"docs/design draft.md"'
```

Resolution rules:

- `@path` is resolved against the current working directory unless it is an absolute path.
- Unquoted refs end at whitespace. Common trailing punctuation such as `,` or `.` is kept as prompt text when the file resolves without it.
- Use `@"path with spaces.txt"` for paths containing spaces, or whenever you want the path boundary to be explicit.
- If the path does not resolve to an existing file, the original `@token` is left as literal prompt text.
- Text files become fenced code blocks; image files become `image_url` parts, the same as with `-f`.

Text files passed via `-f` are embedded as fenced code blocks:

````text
File: main.go
```go
package main
...
```

[your question]
````

goshai chooses a fence long enough for the file content, so Markdown files that already contain triple backticks remain intact.

## Sessions

Every conversation is automatically saved to `sessions/last.json` under the platform config directory when it completes. You can name, continue, and manage sessions with a few flags.

Session files contain the full conversation history sent to the model, including text file contents and base64 image data added with `-f` or `@filename`. The session directory is private to your user account and session JSON files are written with owner-only permissions, but you should still treat them as sensitive and delete old sessions when they are no longer needed.

Session names cannot be empty and cannot contain path separators (`/` or `\`). Quote names with spaces in your shell, for example `goshai -s "code review" "next question"`.

### Continuing a conversation

```bash
    # First question — saved to 'last' automatically
    goshai -f main.go "What does this file do?"

    # Follow-up — the file content is already in the history, no need to re-pass -f
    goshai -s last "Can you suggest a better name for the main function?"

    # Or name the session up front and use it throughout
    goshai -s review -f main.go "What does this file do?"
    goshai -s review "Can you suggest a better name for the main function?"
    goshai -s review "What about error handling?"
```

### Recovering a forgotten session

```bash
    # You forgot to pass -s but want to continue the last conversation:
    goshai -s last "Actually, one more question..."

    # Or rename 'last' to keep it before starting something new:
    goshai -r myreview
    goshai "Completely unrelated question"   # saved to 'last' again
```

### Managing sessions

```bash
    # List all saved sessions
    goshai -S

    # Output:
    #   last                   4 messages  2026-05-06 14:30
    #   myreview               6 messages  2026-05-06 12:15
```

Session files are plain JSON in the `sessions/` subdirectory of the platform config directory and can be deleted manually when no longer needed.

### Generating a reusable prompt from a session

After a conversation, you can ask the model to distill it into a prompt you can reuse on other files:

```bash
    # Generate a prompt from the last session
    goshai -G

    # Generate a prompt from a named session
    goshai -G -s review
```

The model receives the conversation history with all file contents stripped out, then produces a concise prompt that captures the task and intent — ready to paste back in as your next starting point.

```bash
    # Example workflow
    goshai -f main.go "Review this for correctness, error handling, and Go idioms"
    # ... conversation ...

    goshai -G > my_review_prompt.txt
    # Produces something like:
    # "Review the provided code for correctness, error handling, and adherence
    #  to Go idioms. Highlight bugs, missing error checks, and suggest idiomatic
    #  rewrites where appropriate."

    # Reuse it later
    goshai -f other.go "$(cat my_review_prompt.txt)"
```

## Interactive REPL vs. one-shot

Both plain chat and harness mode (`-H`) share the same rule for whether to start an interactive REPL or just answer once and exit:

- **No prompt on the command line, and stdin is a terminal:** drops into a REPL — prints `> `, reads a line, answers, repeats until EOF (Ctrl-D) or the process is killed.
- **A prompt given as command-line arguments, or piped via stdin:** answers that single prompt and exits — no REPL, regardless of terminal.

```bash
    # Interactive chat REPL (no args, run from a terminal)
    goshai

    # One-shot: answers and exits
    goshai "what's the capital of France?"
    echo "what's the capital of France?" | goshai
```

### REPL commands

While in a REPL (either mode), a line starting with `/` is treated as a command instead of being sent to the model:

| Command | Effect |
| --- | --- |
| `/model` | show the current model |
| `/model <name>` | switch models for subsequent turns (alias or fuzzy match, same as `-m`) |
| `/model list` | list models available on the server |
| `/system` | show the current system prompt |
| `/system <text>` | replace the system prompt for the rest of the session |
| `/thinking` | show whether extended thinking is on and its token budget |
| `/thinking on` / `/thinking off` | toggle extended thinking mid-session |
| `/thinking <N>` | enable extended thinking with a specific token budget |
| `/stats` | print running session token totals |
| `/stats on` / `/stats off` | toggle per-request stats mid-session |
| `/history` (alias `/messages`) | print the full conversation history |
| `/undo` | remove the last user turn and its reply |
| `/session list` (alias `/sessions`) | list saved sessions |
| `/session load <name>` | replace the current conversation with a saved session, and make it the active session |
| `/session save` | save the session under its active name |
| `/session save <name>` | save the session under a given name, and make it the active session |
| `/reset` | clear conversation history back to just the system prompt |
| `/help` | list available commands |
| `/exit` (alias `/quit`) | leave the REPL |

## Harness mode

`-H`/`-harness` uses the same REPL/one-shot rule above, except the model can call a `sh` tool to run shell commands on your machine, see their output, and keep reasoning — looping through as many commands as it wants — before printing a plain-text reply.

```bash
    # Start a fresh harness REPL
    goshai -H

    # One-shot: run a task, print the final reply, and exit
    goshai -H "list the go files in this repo and summarize what each one does"

    # Use a system prompt tailored for agentic/tool-using tasks
    goshai -H -p coder

    # Continue (or start) a named session in harness mode
    goshai -H -s deploy-task

    # Print token usage after each turn (in REPL mode, also a running session total)
    goshai -H -stats
```

**⚠️ Warning:** the model executes shell commands with no confirmation step and the full permissions of your user account. Only use harness mode with a model/server you trust, and be mindful of what it has access to (working directory, credentials, network). There is no sandboxing — treat it like giving the model a real terminal.

Harness mode uses the OpenAI Chat Completions "tools" (function-calling) mechanism — `tool_calls` on the assistant message and `tool` role messages carrying the result — rather than a separate stateful API, so it works with any OpenAI-compatible server that supports function calling. It always uses non-streaming requests internally so tool calls can be parsed from a complete response.

## Implementation notes

See [NOTES.md](NOTES.md) for internals: module structure, per-file design notes, and API notes (file context strategies, content part types).

## Dependencies

- [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3) — YAML config parsing
