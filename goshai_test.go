package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestBuildMessages_noFiles(t *testing.T) {
	msgs, err := BuildMessages("", nil, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != openai.ChatMessageRoleUser {
		t.Errorf("want user role, got %s", msgs[0].Role)
	}
	if msgs[0].Content != "hello world" {
		t.Errorf("unexpected content: %q", msgs[0].Content)
	}
}

func TestBuildMessages_withSystemPrompt(t *testing.T) {
	msgs, err := BuildMessages("Be helpful.", nil, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != openai.ChatMessageRoleSystem {
		t.Errorf("want system role first, got %s", msgs[0].Role)
	}
	if msgs[0].Content != "Be helpful." {
		t.Errorf("unexpected system content: %q", msgs[0].Content)
	}
}

func TestBuildMessages_emptySystemPrompt(t *testing.T) {
	msgs, err := BuildMessages("", nil, "hi")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message (no system), got %d", len(msgs))
	}
	if msgs[0].Role != openai.ChatMessageRoleUser {
		t.Errorf("want user role, got %s", msgs[0].Role)
	}
}

func TestBuildMessages_withFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := BuildMessages("", []string{path}, "review this")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	content := msgs[0].Content
	if !strings.Contains(content, "File: hello.go") {
		t.Errorf("expected file header in content, got:\n%s", content)
	}
	if !strings.Contains(content, "```go") {
		t.Errorf("expected go fence in content, got:\n%s", content)
	}
	if !strings.Contains(content, "package main") {
		t.Errorf("expected file body in content, got:\n%s", content)
	}
	if !strings.Contains(content, "review this") {
		t.Errorf("expected user prompt in content, got:\n%s", content)
	}
}

func TestBuildMessages_multipleFiles(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.py")
	bPath := filepath.Join(dir, "b.js")
	os.WriteFile(aPath, []byte("print('a')"), 0o644)
	os.WriteFile(bPath, []byte("console.log('b')"), 0o644)

	msgs, err := BuildMessages("", []string{aPath, bPath}, "")
	if err != nil {
		t.Fatal(err)
	}
	content := msgs[0].Content
	if !strings.Contains(content, "```python") {
		t.Errorf("expected python fence, got:\n%s", content)
	}
	if !strings.Contains(content, "```javascript") {
		t.Errorf("expected javascript fence, got:\n%s", content)
	}
}

func TestBuildMessages_unknownExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.xyz")
	os.WriteFile(path, []byte("some data"), 0o644)

	msgs, err := BuildMessages("", []string{path}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Unknown extension maps to empty lang hint — fence opens as ``` (no lang).
	content := msgs[0].Content
	if !strings.Contains(content, "File: data.xyz") {
		t.Errorf("expected file header, got:\n%s", content)
	}
}

func TestBuildMessages_missingFile(t *testing.T) {
	_, err := BuildMessages("", []string{"/nonexistent/path/file.go"}, "question")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoadConfig_missingFile(t *testing.T) {
	// Point configDir to a temp dir with no config.yaml.
	orig := os.Getenv("HOME")
	tmp := t.TempDir()
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal("expected no error for missing config, got:", err)
	}
	if cfg.URL != "" || cfg.Model != "" {
		t.Errorf("expected zero Config, got %+v", cfg)
	}
}

func TestLoadPrompts_missingFile(t *testing.T) {
	orig := os.Getenv("HOME")
	tmp := t.TempDir()
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	p, err := LoadPrompts()
	if err != nil {
		t.Fatal("expected no error for missing prompts, got:", err)
	}
	if len(p) != 0 {
		t.Errorf("expected empty Prompts, got %v", p)
	}
}
