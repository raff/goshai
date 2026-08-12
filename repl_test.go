package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nonStreamingChatServer returns a test server that answers /chat/completions with a
// single fixed assistant message, non-streaming.
func nonStreamingChatServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, content)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunChat_oneShot verifies that with repl=false, RunChat answers the pending
// turn exactly once and returns without reading from stdin (which, in a test binary,
// is not an interactive terminal the model could get more input from anyway).
func TestRunChat_oneShot(t *testing.T) {
	setTempConfigHome(t)
	srv := nonStreamingChatServer(t, "hello there")
	client := NewClient(srv.URL, "", false)

	messages := []Message{{Role: RoleUser, Content: "hi"}}
	err := RunChat(context.Background(), client, "test-model", messages, ChatOptions{}, "test-session", false, true)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := LoadSession("test-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("want 2 saved messages (user+assistant), got %d: %+v", len(saved), saved)
	}
	if saved[1].Role != RoleAssistant || saved[1].Content != "hello there" {
		t.Fatalf("unexpected assistant message: %+v", saved[1])
	}
}

// TestRunHarness_oneShot verifies that with repl=false, RunHarness answers the
// pending turn once (with no tool calls requested here) and returns.
func TestRunHarness_oneShot(t *testing.T) {
	setTempConfigHome(t)
	srv := nonStreamingChatServer(t, "no tools needed")
	client := NewClient(srv.URL, "", false)

	messages := []Message{{Role: RoleUser, Content: "hi"}}
	err := RunHarness(context.Background(), client, "test-model", messages, ChatOptions{}, "test-session", false)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := LoadSession("test-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("want 2 saved messages (user+assistant), got %d: %+v", len(saved), saved)
	}
	if saved[1].Role != RoleAssistant || saved[1].Content != "no tools needed" {
		t.Fatalf("unexpected assistant message: %+v", saved[1])
	}
}

// withStdin temporarily replaces os.Stdin with a pipe fed by the given lines
// (newline-terminated), restoring the original on test cleanup.
func withStdin(t *testing.T, lines ...string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
		w.Close()
	}()
}

// TestRunChat_replExpandsInlineFileRef verifies that a "@file" reference typed
// as a REPL follow-up turn (not just the initial prompt) is expanded to the
// file's contents before being sent to the model.
func TestRunChat_replExpandsInlineFileRef(t *testing.T) {
	setTempConfigHome(t)
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("hello from file"), 0o644); err != nil {
		t.Fatal(err)
	}

	var lastUserContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if n := len(req.Messages); n > 0 {
			lastUserContent = req.Messages[n-1].Content
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(srv.Close)
	client := NewClient(srv.URL, "", false)

	withStdin(t, "@"+path+" summarize this")

	if err := RunChat(context.Background(), client, "test-model", nil, ChatOptions{}, "", true, true); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(lastUserContent, "hello from file") {
		t.Fatalf("expected @file reference expanded into the request, got: %q", lastUserContent)
	}
	if strings.Contains(lastUserContent, "@"+path) {
		t.Fatalf("expected @path token replaced, still present in: %q", lastUserContent)
	}
}
