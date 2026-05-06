# goshai

A command-line client for interacting with OpenAI-compatible LLM servers (Ollama, LM Studio, vLLM, OpenAI, etc.).

## Features

- Ask questions from the terminal with a one-shot prompt
- Pipe a prompt from stdin: `echo "question" | goshai -f file.go`
- Include one or more files as context (`-f`)
- Select named system prompts from a YAML file (`-p`)
- Server URL, auth token, and model stored in a config file
- Streams the response to stdout as tokens arrive
- Non-streaming mode (`-n`) for servers that don't support streaming

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

```yaml
url: "http://localhost:11434/v1"   # OpenAI-compatible server base URL
token: ""                           # auth token (empty = no auth, e.g. local Ollama)
model: "llama3.2"                   # default model
prompt: "default"                   # default named system prompt
no_stream: false                    # set true for servers that don't support streaming
```

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
  -m, -model <name>   model name override
  -u, -url <url>      server URL override
  -t, -token <tok>    auth token override
  -n, -no-stream      disable streaming (non-streaming mode)
  -l, -list           list available named prompts
  -W, -write-config   save config and create default prompts.yaml if missing

Current configuration:
  config:  /path/to/config.yaml
  prompts: /path/to/prompts.yaml
  url:     http://localhost:11434/v1
  model:   llama3.2
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

# Use a named system prompt
goshai -p coder -f main.go "Review this code"

# Override server and model at runtime
goshai -u http://localhost:11434/v1 -m llama3.2 "Explain recursion"

# List available named prompts
goshai -l

# Pipe a prompt via stdin
echo "Explain this code" | goshai -f main.go

# Non-streaming mode (for servers that don't support streaming)
goshai -n "What is 2+2?"

# First-time setup: save config and create starter prompts file
goshai -u http://localhost:11434/v1 -m llama3.2 -W
```

When files are passed, their content is prepended to the user message as fenced code blocks:

```
File: main.go
```go
package main
...
```

[your question]
```

## Project structure

```
goshai/
├── go.mod       — module definition
├── main.go      — flag parsing, config merging, streaming API call
├── config.go    — Config and Prompts types, YAML loading
└── prompt.go    — BuildMessages: assembles API message array from system prompt + files + user text
```

### `config.go`

Defines `Config` (URL, token, model, prompt name) and `Prompts` (name → system prompt string). Provides `LoadConfig`, `LoadPrompts` (missing file = zero value, not an error), `SaveConfig` (writes the effective config), and `SaveDefaultPrompts` (creates `prompts.yaml` only if it does not already exist).

### `prompt.go`

`BuildMessages(systemPrompt, files, userPrompt)` builds the `[]ChatCompletionMessage` slice:

1. Optional system message if `systemPrompt` is non-empty
2. User message with each file rendered as a fenced code block (language hint from extension), followed by the user's question

### `main.go`

1. Loads config early (before `flag.Parse`) so `flag.Usage` can show the current URL, model, and config file paths
2. Parses flags — each registered with both short and long form (e.g. `-u` / `-url`)
3. Merges values: CLI flag > config file > built-in default
4. If `-W`: writes effective config to `config.yaml`, creates `prompts.yaml` if missing, exits
5. Resolves user prompt: positional args → joined string; no args + piped stdin → `io.ReadAll(os.Stdin)`
6. Calls `BuildMessages`
7. Creates an `openai.Client` with a custom `BaseURL`
8. If `-n`/`-no-stream` (or `no_stream: true` in config): calls `CreateChatCompletion` and prints the full response
9. Otherwise: streams the response via `CreateChatCompletionStream`, printing each delta to stdout

## Dependencies

- [`github.com/sashabaranov/go-openai`](https://github.com/sashabaranov/go-openai) — OpenAI-compatible API client
- [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3) — YAML config parsing
