# Implementation Notes

## Project structure

```
goshai/
├── go.mod       — module definition
├── main.go      — flag parsing, config merging, streaming API call
├── config.go    — Config and Prompts types, YAML loading
├── prompt.go    — BuildMessages: assembles API messages; handles text files, images, and @filename inline refs
└── session.go   — session load/save/list/rename, stored in ~/.config/goshai/sessions/
```

### `config.go`

Defines `Config` (URL, token, model, prompt name) and `Prompts` (name → system prompt string). Provides `LoadConfig`, `LoadPrompts` (missing file = zero value, not an error), `SaveConfig` (writes the effective config), and `SaveDefaultPrompts` (creates `prompts.yaml` only if it does not already exist). After YAML parsing, `LoadConfig` expands environment variables in all string fields via `os.ExpandEnv`, so values like `${OPENAI_API_KEY}` are resolved at load time.

### `prompt.go`

`BuildMessages(systemPrompt, files, userPrompt)` builds the `[]ChatCompletionMessage` slice:

1. Optional system message if `systemPrompt` is non-empty
2. User message via `buildUserMessage` (see below)

`buildUserMessage(files, userPrompt)` assembles the user `ChatCompletionMessage`. It handles two paths:

- **Text-only** (all files are source/text): produces a `Content` string with files as fenced code blocks, followed by the prompt — fully backward-compatible with any OpenAI-compatible server.
- **Mixed or image-only** (any file has an image extension): produces a `MultiContent []ChatMessagePart` message. Text files become `text` parts (same fenced-block format), image files (`.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`) become `image_url` parts with a `data:<mime>;base64,<data>` URI. This is compatible with any vision-capable server.

**`@filename` inline syntax:** `buildUserMessage` calls `parseInlineRefs(userPrompt)` which scans the prompt for `@path` and `@"quoted path"` tokens. Each token is tested with `os.Stat`; tokens that don't resolve to an existing file are left as literal text. Valid file references are substituted inline at their position in the prompt (text files as fenced blocks, images as image parts). The `-f` files are always prepended first.

**PDF files** return an explicit error: `"PDF files require go-openai file-part support (not yet in v1.36.1)"`. The `"file"` content part type for inline PDFs exists in OpenAI's Chat Completions spec but is not exposed in go-openai v1.36.1.

**Prompt generation (`-G`) helpers:**

`stripFileContent` removes file blocks from a message using:
```
(?s)File: [^\n]+\n```[^\n]*\n.*?```\n\n
```
The `(?s)` flag (dotall) is required because file contents span multiple lines — without it `.` would not cross newline boundaries and the match would fail. The non-greedy `.*?` prevents one file block from swallowing subsequent ones.

`stripFileBlocks` now handles both `Content` string messages (regex-strip) and `MultiContent` messages (drop `image_url` parts, strip file blocks from `text` parts, collapse surviving text to a `Content` string). This ensures `-G` works on sessions that included images.

`buildGenPromptRequest` formats the cleaned conversation as labeled `SYSTEM:` / `USER:` / `ASSISTANT:` turns and appends a fixed instruction asking the model to output *only* a reusable prompt template. Explicit role labels are used rather than sending actual role-structured messages so the model treats the whole history as passive context, not as live dialogue it is continuing.

### `session.go`

Manages conversation history as JSON files under `~/.config/goshai/sessions/`:

- `LoadSession(name)` — reads history; returns nil for a new session
- `SaveSession(name, messages)` — writes history with 0o600 permissions
- `ListSessions()` — returns name, message count, and modification time for each session
- `RenameSession(from, to)` — renames a session file; errors if the target already exists

### `main.go`

1. Loads config early (before `flag.Parse`) so `flag.Usage` can show the current URL, model, and config file paths
2. Parses flags — each registered with both short and long form (e.g. `-u` / `-url`)
3. Merges values: CLI flag > config file > built-in default
4. If `-W`: writes effective config to `config.yaml`, creates `prompts.yaml` if missing, exits
5. If `-S`: lists sessions and exits; if `-r`: renames `last` and exits; if `-P`: lists prompts and exits; if `-M`: lists models via API and exits
5. If `-G`: loads session (named or `last`), strips file blocks, calls the model non-streaming with a meta-prompt, prints generated prompt and exits — positioned *after* URL/model validation but *before* the user-prompt/file requirement check, since `-G` needs neither
6. Resolves user prompt: positional args → joined string; no args + piped stdin → `io.ReadAll(os.Stdin)`
7. Assembles messages: loads named session history if `-s` given, otherwise builds fresh via `BuildMessages`
8. Creates an `openai.Client` with a custom `BaseURL`
9. If `-n`/`-no-stream` (or `nostream: true` in config): calls `CreateChatCompletion` and prints the full response
10. Otherwise: streams the response via `CreateChatCompletionStream`, printing each delta to stdout
11. Appends the assistant reply and saves the session (to the named session or `last`)

## OpenAI API notes

### File context: inline embedding vs. file IDs

goshai embeds file content directly in the user message as fenced code blocks. This is the universal approach and works across all OpenAI-compatible servers (Ollama, LM Studio, vLLM, etc.).

The alternative — uploading a file to `/v1/files` to get a reusable file ID — exists in OpenAI's ecosystem but is more limited than it appears:

- **Chat Completions API** (`/v1/chat/completions`): OpenAI added a `"type": "file"` content part for PDF uploads, but go-openai (v1.36.1) does not expose this yet, and local/compatible servers don't implement it.
- **Responses API** (`/v1/responses`): OpenAI's newer stateful API (2025) uses `"type": "input_file"` and `"type": "input_text"` content parts, where a `file_id` from `/v1/files` can be referenced. go-openai doesn't implement the Responses API at all, and neither do local-server alternatives.
- **Batch API**: `input_file_id` appears in go-openai's batch structs, but that refers to a JSONL input file for batch jobs — unrelated to chat context.

**Conclusion:** inline embedding is the right default. File IDs for chat context are an OpenAI-only feature, require a different (newer) API surface, and are only useful when the same large file would be sent many times (to avoid retransmitting tokens). For a general-purpose CLI targeting any compatible server, there is no portable way to use file IDs as chat input.

### go-openai chat message content parts (v1.36.1)

The library defines two content part types for `ChatCompletionMessage`:

| Constant | JSON value | Use |
|---|---|---|
| `ChatMessagePartTypeText` | `"text"` | plain text segment |
| `ChatMessagePartTypeImageURL` | `"image_url"` | image via URL or base64 data URI |

A `"file"` part type exists in OpenAI's Chat Completions spec (for PDFs) but is not yet in go-openai v1.36.1. Images work today via `image_url` with a `data:image/<mime>;base64,<data>` URI — goshai uses this for all image extensions.
