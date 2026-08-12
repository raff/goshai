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
		{"/undo", true},
		{"/session list", true},
		{"/sessions load foo", true},
		{"/thinking on", true},
		{"/history", true},
		{"/messages", true},
		{"/exit", true},
		{"/quit", true},
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
	saveAs := ""
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
		saveAs:   &saveAs,
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

func TestCmdThinking(t *testing.T) {
	opts := ChatOptions{}
	messages := []Message{}
	rc := newTestReplCommands(&messages, &opts)

	rc.dispatch("/thinking on")
	if !opts.Think {
		t.Fatal("/thinking on should enable opts.Think")
	}
	rc.dispatch("/thinking off")
	if opts.Think {
		t.Fatal("/thinking off should disable opts.Think")
	}
	rc.dispatch("/thinking 5000")
	if !opts.Think || opts.ThinkingBudget != 5000 {
		t.Fatalf("/thinking 5000 should enable thinking with budget 5000, got Think=%v Budget=%d", opts.Think, opts.ThinkingBudget)
	}
	rc.dispatch("/thinking bogus")
	if opts.ThinkingBudget != 5000 {
		t.Fatalf("invalid /thinking argument should leave budget unchanged, got %d", opts.ThinkingBudget)
	}
}

func TestCmdUndo(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "hello"},
		{Role: RoleUser, Content: "again"},
		{Role: RoleAssistant, Content: "hi again"},
	}
	rc := newTestReplCommands(&messages, &ChatOptions{})

	rc.dispatch("/undo")
	if len(messages) != 3 {
		t.Fatalf("expected last turn removed, got %+v", messages)
	}

	rc.dispatch("/undo")
	if len(messages) != 1 || messages[0].Role != RoleSystem {
		t.Fatalf("expected only system message left, got %+v", messages)
	}

	// nothing left to undo
	rc.dispatch("/undo")
	if len(messages) != 1 {
		t.Fatalf("expected /undo with no user turns to be a no-op, got %+v", messages)
	}
}

func TestCmdSessionSave(t *testing.T) {
	setTempConfigHome(t)
	messages := []Message{{Role: RoleUser, Content: "hi"}, {Role: RoleAssistant, Content: "hello"}}
	rc := newTestReplCommands(&messages, &ChatOptions{})
	*rc.saveAs = "default-session"

	rc.dispatch("/session save")
	saved, err := LoadSession("default-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("expected /session save with no args to save to the active session, got %+v", saved)
	}

	rc.dispatch("/session save named-session")
	saved, err = LoadSession("named-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("expected /session save <name> to save under that name, got %+v", saved)
	}
	if *rc.saveAs != "named-session" {
		t.Fatalf("expected /session save <name> to switch the active session, got %q", *rc.saveAs)
	}
}

func TestCmdSessionLoad(t *testing.T) {
	setTempConfigHome(t)
	if err := SaveSession("other-session", []Message{{Role: RoleUser, Content: "prior"}, {Role: RoleAssistant, Content: "reply"}}); err != nil {
		t.Fatal(err)
	}

	messages := []Message{{Role: RoleSystem, Content: "sys"}}
	rc := newTestReplCommands(&messages, &ChatOptions{})
	*rc.saveAs = "default-session"

	rc.dispatch("/session load other-session")
	if len(messages) != 2 || messages[0].Content != "prior" {
		t.Fatalf("expected messages replaced with loaded session, got %+v", messages)
	}
	if *rc.saveAs != "other-session" {
		t.Fatalf("expected /session load to switch the active session, got %q", *rc.saveAs)
	}

	// loading a nonexistent session should leave state untouched.
	rc.dispatch("/session load does-not-exist")
	if len(messages) != 2 || *rc.saveAs != "other-session" {
		t.Fatalf("expected no change after loading a missing session, got messages=%+v saveAs=%q", messages, *rc.saveAs)
	}
}

func TestCmdSessionList(t *testing.T) {
	setTempConfigHome(t)
	messages := []Message{}
	rc := newTestReplCommands(&messages, &ChatOptions{})

	// Should not error even with no sessions saved yet.
	rc.dispatch("/session list")
	rc.dispatch("/sessions list")
}

func TestDispatchExit(t *testing.T) {
	messages := []Message{}
	rc := newTestReplCommands(&messages, &ChatOptions{})

	if quit := rc.dispatch("/help"); quit {
		t.Fatal("/help should not signal quit")
	}
	if quit := rc.dispatch("/exit"); !quit {
		t.Fatal("/exit should signal quit")
	}
	if quit := rc.dispatch("/quit"); !quit {
		t.Fatal("/quit should signal quit")
	}
}
