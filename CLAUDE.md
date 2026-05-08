# goshai

A Go CLI tool that provides a conversational interface to AI models (OpenAI-compatible APIs).

## Workflow

Default task completion includes all of the following steps — skip a step only if explicitly told to:

1. Implement the change
2. Update relevant docs (README.md, DESIGN.md, NOTES.md) if behavior or APIs changed
3. Run `go build ./... && go test ./...` and fix any failures
4. Commit with a concise descriptive message

## CGo Conventions

- Never compare C pointer types (CGImageRef, CGDataProviderRef, etc.) directly to `nil` in Go — write a small C helper that returns a bool, or compare via `unsafe.Pointer`
- Split non-trivial Objective-C/CGo code into `.h`/`.m` files rather than inlining in Go strings

## Bug Fixes

- Before applying a fix to a parser, extractor, or renderer, enumerate 2–3 adjacent cases the affected code path handles (e.g., wrapped lines, same-X continuations, sibling sections) and verify the fix doesn't regress them
- Prefer minimal, well-guarded fixes over broad heuristics that may regress unrelated inputs
- Identify the root cause, not just the symptom — state it before writing code
