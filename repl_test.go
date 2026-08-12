package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
