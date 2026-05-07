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
- Session history: conversations are saved automatically and can be continued by name
- Generate a reusable prompt from any session (`-G`)

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
token: ""                          # auth token (empty = no auth, e.g. local Ollama)
model: "llama3.2"                  # default model
prompt: "default"                  # default named system prompt
nostream: false                    # set true for servers that don't support streaming
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
  -P, -prompts        list available named prompts
  -M, -models         list available models (requires server URL)
  -W, -write-config   save config and create default prompts.yaml if missing
  -s, -session <name> continue named session (default: save to 'last')
  -r, -rename <name>  rename 'last' session to a new name
  -S, -sessions       list available sessions
  -G, -gen-prompt     generate reusable prompt from session history

Current configuration:
  config:  /path/to/config.yaml
  prompts: /path/to/prompts.yaml
  url:     http://localhost:11434/v1
  model:   llama3.2
  stream:  true
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
    goshai -P

    # List available models
    goshai -M
    
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

## Sessions

Every conversation is automatically saved to `~/.config/goshai/sessions/last.json` when it completes. You can name, continue, and manage sessions with a few flags.

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

Session files are plain JSON in `~/.config/goshai/sessions/` and can be deleted manually when no longer needed.

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

## Implementation notes

See [NOTES.md](NOTES.md) for internals: module structure, per-file design notes, and API research (file context strategies, go-openai content part types).

## Dependencies

- [`github.com/sashabaranov/go-openai`](https://github.com/sashabaranov/go-openai) — OpenAI-compatible API client
- [`gopkg.in/yaml.v3`](https://pkg.go.dev/gopkg.in/yaml.v3) — YAML config parsing
