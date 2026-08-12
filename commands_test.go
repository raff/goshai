package main

import (
	"context"
	"testing"
)

func TestIsCommand(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"hello there", false},
		{"/etc/passwd tell me about this", false},
		{"/system  ", true},
		{"/stat", true},
		{"/stats on", true},
		{"/model list", true},
		{"/reset", true},
		{"/help", true},
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		if got := isCommand(c.input); got != c.want {
			t.Errorf("isCommand(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func newTestReplCommands(messages *[]Message, opts *ChatOptions) *replCommands {
	model := "gpt-4o"
	var total Usage
	var requests int
	return &replCommands{
		ctx:      context.Background(),
		client:   nil,
		model:    &model,
		opts:     opts,
		messages: messages,
		total:    &total,
		requests: &requests,
	}
}

func TestCmdSystem(t *testing.T) {
	messages := []Message{{Role: RoleSystem, Content: "old prompt"}, {Role: RoleUser, Content: "hi"}}
	rc := newTestReplCommands(&messages, &ChatOptions{})

	rc.dispatch("/system new prompt")
	if len(messages) != 2 || messages[0].Content != "new prompt" {
		t.Fatalf("expected system message replaced in place, got %+v", messages)
	}

	// No existing system message: /system should prepend one.
	messages = []Message{{Role: RoleUser, Content: "hi"}}
	rc = newTestReplCommands(&messages, &ChatOptions{})
	rc.dispatch("/system prepended")
	if len(messages) != 2 || messages[0].Role != RoleSystem || messages[0].Content != "prepended" {
		t.Fatalf("expected system message prepended, got %+v", messages)
	}
}

func TestCmdReset(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
	}
	rc := newTestReplCommands(&messages, &ChatOptions{})

	rc.dispatch("/reset")
	if len(messages) != 1 || messages[0].Role != RoleSystem {
		t.Fatalf("expected only the system message to survive /reset, got %+v", messages)
	}

	// No system message: /reset should clear everything.
	messages = []Message{{Role: RoleUser, Content: "hi"}, {Role: RoleAssistant, Content: "hello"}}
	rc = newTestReplCommands(&messages, &ChatOptions{})
	rc.dispatch("/reset")
	if len(messages) != 0 {
		t.Fatalf("expected empty history after /reset with no system message, got %+v", messages)
	}
}

func TestCmdStatsToggle(t *testing.T) {
	opts := ChatOptions{}
	messages := []Message{}
	rc := newTestReplCommands(&messages, &opts)

	rc.dispatch("/stats on")
	if !opts.Stats {
		t.Fatal("/stats on should enable opts.Stats")
	}
	rc.dispatch("/stats off")
	if opts.Stats {
		t.Fatal("/stats off should disable opts.Stats")
	}
}
