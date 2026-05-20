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

Defines `Config` (URL, token, model, prompt name), `Prompts` (name → system prompt string), and `Aliases` (short name → full model ID). Provides `LoadConfig`, `LoadPrompts`, `LoadAliases` (missing file or absent block = zero value, not an error), `SaveConfig`, `SaveAliases`, and `SaveDefaultPrompts`. After YAML parsing, `LoadConfig` expands environment variables in all string fields via `os.ExpandEnv`, so values like `${OPENAI_API_KEY}` are resolved at load time.

**Aliases** are stored as a top-level `aliases:` key in `config.yaml` alongside named environments. `parseConfigFile` treats this key specially — it decodes the value as `map[string]string` rather than a `Config` and excludes it from the returned environment list, so `"aliases"` never appears as a phantom environment. `marshalMultiEnv` writes the aliases block first (sorted) when non-empty, then the environment entries. All save paths (`SaveConfig`, `SaveAliases`) round-trip the aliases through to `marshalMultiEnv` so the block is never silently dropped.

Named configs (`cfg.Name != ""`) are always written in multi-env format. A new config file becomes a one-entry multi-env file, an existing multi-env file is updated or appended, and an existing legacy single-env file is preserved as a `default` environment before appending the named env.

### `prompt.go`

`BuildMessages(systemPrompt, files, userPrompt)` builds the `[]ChatCompletionMessage` slice:

1. Optional system message if `systemPrompt` is non-empty
2. User message via `buildUserMessage` (see below)

`buildUserMessage(files, userPrompt)` assembles the user `ChatCompletionMessage`. It handles two paths:

- **Text-only** (all files are source/text): produces a `Content` string with files as fenced code blocks, followed by the prompt — fully backward-compatible with any OpenAI-compatible server.
- **Mixed or image-only** (any file has an image extension): produces a `MultiContent []ChatMessagePart` message. Text files become `text` parts (same fenced-block format), image files (`.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`) become `image_url` parts with a `data:<mime>;base64,<data>` URI. This is compatible with any vision-capable server.

**`@filename` inline syntax:** `buildUserMessage` calls `parseInlineRefs(userPrompt)` which scans the prompt for `@path` and `@"quoted path"` tokens. Each token is tested with `os.Stat`; tokens that don't resolve to an existing file are left as literal text. Valid file references are substituted inline at their position in the prompt (text files as fenced blocks, images as image parts). Unquoted refs end at whitespace; if the exact token doesn't exist, common trailing punctuation is trimmed and preserved as prompt text when the trimmed path resolves. The `-f` files are always prepended first.

Text file blocks use a Markdown backtick fence long enough to exceed the longest backtick run in the file content. This keeps Markdown files containing triple-backtick code fences from prematurely closing the wrapper fence.

**PDF files** return an explicit error: `"PDF files require go-openai file-part support (not yet in v1.36.1)"`. The `"file"` content part type for inline PDFs exists in OpenAI's Chat Completions spec but is not exposed in go-openai v1.36.1.

**Prompt generation (`-G`) helpers:**

`stripFileContent` removes file blocks with a small parser rather than a regex. It looks for `File: ...` headers, reads the following backtick fence marker, and removes content through the matching closing fence line. This supports dynamically sized fences and avoids breaking on Markdown files that contain inner triple-backtick fences.

`stripFileBlocks` handles both `Content` string messages (parser-strip) and `MultiContent` messages (drop `image_url` parts, strip file blocks from `text` parts, collapse surviving text to a `Content` string). This ensures `-G` works on sessions that included images.

`buildGenPromptRequest` formats the cleaned conversation as labeled `SYSTEM:` / `USER:` / `ASSISTANT:` turns and appends a fixed instruction asking the model to output *only* a reusable prompt template. Explicit role labels are used rather than sending actual role-structured messages so the model treats the whole history as passive context, not as live dialogue it is continuing.

### `session.go`

Manages conversation history as JSON files under the `sessions/` subdirectory of the platform config directory:

- `LoadSession(name)` — reads history; returns nil for a new session
- `SaveSession(name, messages)` — writes history with 0o600 permissions
- `ListSessions()` — returns name, message count, and modification time for each session
- `RenameSession(from, to)` — renames a session file; errors if the target already exists

Session names are validated before any path is built. Empty names, `.` / `..`, NUL bytes, and path separators are rejected, which prevents path traversal through values such as `../name` without breaking ordinary names that contain spaces.

Session files intentionally store the full API conversation so follow-up turns can keep prior file context. That includes embedded text files and base64 image data from `-f` and `@filename`, so docs warn users to treat session JSON as sensitive.

### `main.go`

1. Loads config early (before `flag.Parse`) so `flag.Usage` can show the current URL, model, and config file paths
2. Parses flags — each registered with both short and long form (e.g. `-u` / `-url`)
3. Merges values: CLI flag > config file > built-in default
4. If `-W`: writes effective config to `config.yaml`, creates `prompts.yaml` if missing, exits
5. If `-S`: lists sessions and exits; if `-r`: renames `last` and exits; if `-P`: lists prompts and exits; if `-M`: lists models via API and exits; if `-A`: lists aliases and exits; if `-a alias=model`: updates alias in `config.yaml` and exits
6. **Model resolution** (when server URL is known):
   a. Alias lookup — if the model string matches a key in `aliases`, the full model ID is substituted
   b. Fuzzy match fallback — only when `-m` was explicitly passed *and* alias lookup did not resolve it: creates a temporary client, calls `ListModels`, and finds the best match (exact → prefix → substring, case-insensitive, sorted; warns if ambiguous). Skipped when model came from config default to avoid a network call on every invocation.
7. If `-G`: loads session (named or `last`), strips file blocks, calls the model non-streaming with a meta-prompt, prints generated prompt and exits
8. Resolves user prompt: positional args → joined string; no args + piped stdin → `io.ReadAll(os.Stdin)`
9. Assembles messages: loads named session history if `-s` given, otherwise builds fresh via `BuildMessages`
10. Creates an `openai.Client` with a custom `BaseURL`
11. If `-n`/`-no-stream` (or `nostream: true` in config): calls `CreateChatCompletion` and prints the full response
12. Otherwise: streams the response via `CreateChatCompletionStream`, printing each delta to stdout
13. Appends the assistant reply and saves the session (to the named session or `last`)

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
