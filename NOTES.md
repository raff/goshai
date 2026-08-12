# Implementation Notes

## Project structure

```
goshai/
├── go.mod       — module definition
├── main.go      — flag parsing, config merging, API call dispatch
├── client.go    — custom OpenAI-compatible HTTP client (chat completions + models list)
├── config.go    — Config and Prompts types, YAML loading
├── prompt.go    — BuildMessages: assembles API messages; handles text files, images, and @filename inline refs
├── session.go   — session load/save/list/rename, stored in ~/.config/goshai/sessions/
├── chat.go      — plain chat REPL / one-shot turn (no tools)
└── harness.go   — harness mode: same REPL/one-shot shape, but the model can call a shell tool
```

### `config.go`

Defines `Config` (URL, token, model, prompt name, `NoStream`, `Think`, `ThinkingBudget`), `Prompts` (name → system prompt string), and `Aliases` (short name → full model ID). Provides `LoadConfig`, `LoadPrompts`, `LoadAliases` (missing file or absent block = zero value, not an error), `SaveConfig`, `SaveAliases`, and `SaveDefaultPrompts`. After YAML parsing, `LoadConfig` expands environment variables in all string fields via `os.ExpandEnv`, so values like `${OPENAI_API_KEY}` are resolved at load time.

**Aliases** are stored as a top-level `aliases:` key in `config.yaml` alongside named environments. `parseConfigFile` treats this key specially — it decodes the value as `map[string]string` rather than a `Config` and excludes it from the returned environment list, so `"aliases"` never appears as a phantom environment. `marshalMultiEnv` writes the aliases block first (sorted) when non-empty, then the environment entries. All save paths (`SaveConfig`, `SaveAliases`) round-trip the aliases through to `marshalMultiEnv` so the block is never silently dropped.

Named configs (`cfg.Name != ""`) are always written in multi-env format. A new config file becomes a one-entry multi-env file, an existing multi-env file is updated or appended, and an existing legacy single-env file is preserved as a `default` environment before appending the named env.

### `client.go`

Implements a minimal OpenAI-compatible HTTP client with no external dependencies. Defines the shared message types:

- `Message` — a single chat message with `Role`, `Content` (string), and `MultiContent` (`[]MessagePart`). Custom `MarshalJSON`/`UnmarshalJSON` handle the dual-type `content` field: a string when text-only, a JSON array when multi-part. This matches the wire format expected by all OpenAI-compatible servers and keeps session JSON files compatible.
- `MessagePart` — a single content part with `Type`, `Text`, and `ImageURL`.
- `ImageURL` — wraps the `url` field of an `image_url` part.

Role constants (`RoleSystem`, `RoleUser`, `RoleAssistant`) and part-type constants (`PartTypeText`, `PartTypeImageURL`) are defined here.

`Client` holds `BaseURL`, `Token`, and an `*http.Client`. Three methods:

- `ListModels(ctx)` — `GET /models`, returns `[]ModelInfo`.
- `ChatCompletion(ctx, model, messages, opts)` — `POST /chat/completions` (non-streaming), returns the first choice's content string.
- `ChatCompletionStream(ctx, model, messages, opts)` — `POST /chat/completions` with `"stream": true`, returns a `*ChatStream`. `ChatStream.Recv()` reads the SSE response line-by-line, skipping non-`data:` lines, returning each delta content string, and returning `io.EOF` on the `[DONE]` sentinel.

`ChatOptions` carries per-request options currently passed to both call methods. `buildChatRequest` translates `ChatOptions` into the appropriate JSON fields before marshaling.

`ChatCompletionRaw(ctx, model, messages, opts)` is the non-streaming call underlying `ChatCompletion`; it returns the full response `Message` (content and/or `ToolCalls`) rather than just a content string, which is what harness mode needs.

**Tool calling** (function-calling): `Message` carries `ToolCalls []ToolCall` (assistant messages that invoke tools), `ToolCallID` and `Name` (tool-role messages responding to a call). `MarshalJSON`/`UnmarshalJSON` were extended to (de)serialize `tool_calls`, `tool_call_id`, and `name` alongside `content` — an assistant message with `ToolCalls` set and empty `Content` omits the `content` field entirely, matching what most OpenAI-compatible servers expect. `ToolDef`/`ToolFunctionDef` describe a callable function for the request's `tools` array; `ChatOptions.Tools` carries them through `buildChatRequest`.

### `prompt.go`

`BuildMessages(systemPrompt, files, userPrompt)` builds the `[]Message` slice:

1. Optional system message if `systemPrompt` is non-empty
2. User message via `buildUserMessage` (see below)

`buildUserMessage(files, userPrompt)` assembles the user `Message`. It handles two paths:

- **Text-only** (all files are source/text): produces a `Content` string with files as fenced code blocks, followed by the prompt — fully backward-compatible with any OpenAI-compatible server.
- **Mixed or image-only** (any file has an image extension): produces a `MultiContent []MessagePart` message. Text files become `text` parts (same fenced-block format), image files (`.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`) become `image_url` parts with a `data:<mime>;base64,<data>` URI. This is compatible with any vision-capable server.

**`@filename` inline syntax:** `buildUserMessage` calls `parseInlineRefs(userPrompt)` which scans the prompt for `@path` and `@"quoted path"` tokens. Each token is tested with `os.Stat`; tokens that don't resolve to an existing file are left as literal text. Valid file references are substituted inline at their position in the prompt (text files as fenced blocks, images as image parts). Unquoted refs end at whitespace; if the exact token doesn't exist, common trailing punctuation is trimmed and preserved as prompt text when the trimmed path resolves. The `-f` files are always prepended first.

Text file blocks use a Markdown backtick fence long enough to exceed the longest backtick run in the file content. This keeps Markdown files containing triple-backtick code fences from prematurely closing the wrapper fence.

**PDF files** return an explicit error. The `"file"` content part type for inline PDFs exists in OpenAI's Chat Completions spec but is not universally supported by local/compatible servers.

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
9. Computes `hasInitialPrompt` (files or a resolved user prompt) and `interactiveStdin` (`os.Stdin.Stat()` reports a char device). With neither a prompt nor a terminal there's nothing to do, so it prints usage and exits. `repl := !hasInitialPrompt && interactiveStdin` — the REPL only runs when nothing was supplied up front and more input can be read from a terminal; a prompt given via args or piped stdin always means exactly one answer
10. Assembles messages: loads named session history if `-s` given; with no session and no initial prompt, seeds only the system message (leaving the trailing user message for the REPL loop to fill in); otherwise builds fresh via `BuildMessages`
11. Creates a `Client` with `NewClient(serverURL, token)` and a `ChatOptions` from the merged `thinking` / `thinkingBudget` values
12. If `-H`/`-harness`: calls `RunHarness(ctx, client, model, messages, opts, saveAs, repl)`
13. Otherwise: calls `RunChat(ctx, client, model, messages, opts, saveAs, repl, noStream)`

Both `RunChat` and `RunHarness` handle streaming/non-streaming, printing, and session saving themselves — `main.go` no longer does the response loop inline.

### `chat.go`

`RunChat(ctx, client, model, messages, opts, saveAs, repl, noStream)` implements plain chat (no tools):

1. If `messages` already ends with an unanswered user message (initial prompt or piped stdin from `main.go`), processes that turn immediately; otherwise, when `repl` is true, prints `"> "` and reads a line from stdin as the next user message (blank lines ignored)
2. Calls `runChatTurn`, which uses `client.ChatCompletion` (non-streaming) or `client.ChatCompletionStream`, printing the reply as it arrives in the streaming case, and prints stats via `printStats` when requested
3. Appends the assistant reply, saves the session (if `saveAs` is set)
4. If `repl` is false, returns immediately after that one turn; otherwise prints `"> "` and loops back to read the next line

### `harness.go`

`RunHarness(ctx, client, model, messages, opts, saveAs, repl)` implements the harness loop, structured the same way as `RunChat` above but with tool support:

1. Sets `opts.Tools = []ToolDef{shellTool}` — a single function tool named `sh` that takes a `command` string parameter
2. If `messages` already ends with an unanswered user message, processes that turn before reading from stdin; otherwise, when `repl` is true, prints `"> "` and waits
3. When `repl` is true, reads a line from stdin as the next user message (blank lines are ignored)
4. Inner loop: calls `client.ChatCompletionRaw`, appends the assistant reply to history
   - If the reply has no `ToolCalls`, prints the content, optionally prints stats via the same `printStats` helper `main.go` uses, and breaks out
   - Otherwise, for each tool call: parses `{"command": "..."}` from the arguments JSON, runs it with `exec.Command("/bin/sh", "-c", command)`, prints `$ <command>` and the combined output, and appends a `tool`-role message with `exit <code>\n<output>` as content before looping back to call the model again
5. Saves the session (if `saveAs` is set) after each tool-call round and after each completed turn
6. If `repl` is false, returns immediately after that one turn's inner loop completes; otherwise prints `"> "` and loops back to read the next line

Non-streaming only: tool calls need a complete response to parse, so harness mode does not use `ChatCompletionStream` regardless of the `-n` flag.

**Security note:** the `sh` tool executes whatever command the model requests with no confirmation step and the permissions of the invoking user — this is intentional (matches the harness pattern of e.g. Claude Code's own bash tool) but is only ever active when `-H`/`-harness` is explicitly passed.

## OpenAI API notes

### File context: inline embedding vs. file IDs

goshai embeds file content directly in the user message as fenced code blocks. This is the universal approach and works across all OpenAI-compatible servers (Ollama, LM Studio, vLLM, etc.).

The alternative — uploading a file to `/v1/files` to get a reusable file ID — exists in OpenAI's ecosystem but is more limited than it appears:

- **Chat Completions API** (`/v1/chat/completions`): OpenAI added a `"type": "file"` content part for PDF uploads, but local/compatible servers don't implement it.
- **Responses API** (`/v1/responses`): OpenAI's newer stateful API (2025) uses `"type": "input_file"` and `"type": "input_text"` content parts with `file_id` references. Neither local-server alternatives nor the current client implement this.
- **Batch API**: `input_file_id` refers to a JSONL input file for batch jobs — unrelated to chat context.

**Conclusion:** inline embedding is the right default. File IDs for chat context are an OpenAI-only feature, require a different (newer) API surface, and are only useful when the same large file would be sent many times (to avoid retransmitting tokens). For a general-purpose CLI targeting any compatible server, there is no portable way to use file IDs as chat input.

### Chat message content parts

goshai uses two content part types, matching the OpenAI Chat Completions spec:

| Constant | JSON value | Use |
|---|---|---|
| `PartTypeText` | `"text"` | plain text segment |
| `PartTypeImageURL` | `"image_url"` | image via URL or base64 data URI |

Images are sent as `image_url` parts with a `data:image/<mime>;base64,<data>` URI. A `"file"` part type exists in OpenAI's spec (for PDFs) but is not universally supported; goshai returns an error for PDF inputs rather than sending a part type that most servers would reject.

### Extending the HTTP client

Because goshai owns its own HTTP client (`client.go`), provider-specific request fields can be added directly to `chatRequest` without any third-party library constraint.

**Thinking / extended reasoning** is the first example. `ChatOptions` carries `Think bool` and `ThinkingBudget int`; `buildChatRequest` translates these into a `"thinking": {"type": "enabled", "budget_tokens": N}` field on the request (defaulting to 10 000 tokens when the budget is 0). Enabled via `-thinking` / `-thinking-budget` flags at the CLI or `think: true` / `thinking-budget: N` per environment in `config.yaml`.
