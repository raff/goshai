package main

import (
	"encoding/base64"
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

func TestBuildMessages_imageFile(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "shot.png")
	imgData := []byte("fakepngbytes")
	if err := os.WriteFile(imgPath, imgData, 0o644); err != nil {
		t.Fatal(err)
	}

	msgs, err := BuildMessages("", []string{imgPath}, "what do you see?")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.Content != "" {
		t.Errorf("expected empty Content when image present, got %q", msg.Content)
	}
	if len(msg.MultiContent) == 0 {
		t.Fatal("expected MultiContent parts, got none")
	}
	var foundImage, foundText bool
	for _, part := range msg.MultiContent {
		if part.Type == openai.ChatMessagePartTypeImageURL {
			foundImage = true
			want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgData)
			if part.ImageURL == nil || part.ImageURL.URL != want {
				t.Errorf("unexpected image URL: %v", part.ImageURL)
			}
		}
		if part.Type == openai.ChatMessagePartTypeText && strings.Contains(part.Text, "what do you see?") {
			foundText = true
		}
	}
	if !foundImage {
		t.Error("expected image_url part")
	}
	if !foundText {
		t.Error("expected text part with user prompt")
	}
}

func TestBuildMessages_mixedTextAndImage(t *testing.T) {
	dir := t.TempDir()
	goPath := filepath.Join(dir, "main.go")
	imgPath := filepath.Join(dir, "shot.png")
	os.WriteFile(goPath, []byte("package main"), 0o644)
	os.WriteFile(imgPath, []byte("fakepng"), 0o644)

	msgs, err := BuildMessages("", []string{goPath, imgPath}, "explain")
	if err != nil {
		t.Fatal(err)
	}
	msg := msgs[0]
	if len(msg.MultiContent) == 0 {
		t.Fatal("expected MultiContent for mixed files")
	}
	var textParts, imageParts int
	for _, part := range msg.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			textParts++
		case openai.ChatMessagePartTypeImageURL:
			imageParts++
		}
	}
	if textParts == 0 {
		t.Error("expected at least one text part")
	}
	if imageParts != 1 {
		t.Errorf("expected 1 image part, got %d", imageParts)
	}
}

func TestBuildMessages_pdfError(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "doc.pdf")
	os.WriteFile(pdfPath, []byte("%PDF-1.4"), 0o644)

	_, err := BuildMessages("", []string{pdfPath}, "summarize")
	if err == nil {
		t.Error("expected error for PDF file, got nil")
	}
	if !strings.Contains(err.Error(), "PDF") {
		t.Errorf("expected PDF error message, got: %v", err)
	}
}

func TestParseInlineRefs_noRefs(t *testing.T) {
	segs := parseInlineRefs("hello world")
	if len(segs) != 1 || segs[0].text != "hello world" {
		t.Errorf("unexpected segments: %+v", segs)
	}
}

func TestParseInlineRefs_nonExistentRef(t *testing.T) {
	segs := parseInlineRefs("please @mention me")
	if len(segs) != 1 || segs[0].text != "please @mention me" {
		t.Errorf("non-existent @ref should be kept as literal text, got: %+v", segs)
	}
}

func TestParseInlineRefs_existingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("package main"), 0o644)

	prompt := "review " + path + " please"
	// Use @path form
	prompt = "review @" + path + " please"
	segs := parseInlineRefs(prompt)

	var found bool
	for _, seg := range segs {
		if seg.filePath == path {
			found = true
		}
	}
	if !found {
		t.Errorf("expected file segment for %s, got: %+v", path, segs)
	}
}

func TestBuildMessages_inlineTextRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "util.go")
	os.WriteFile(path, []byte("package util"), 0o644)

	msgs, err := BuildMessages("", nil, "check @"+path+" for bugs")
	if err != nil {
		t.Fatal(err)
	}
	msg := msgs[0]
	// Text-only: should use Content string, not MultiContent
	if msg.MultiContent != nil {
		t.Error("expected Content string for text-only inline ref, got MultiContent")
	}
	if !strings.Contains(msg.Content, "File: util.go") {
		t.Errorf("expected file block in content, got: %s", msg.Content)
	}
	if !strings.Contains(msg.Content, "for bugs") {
		t.Errorf("expected trailing text in content, got: %s", msg.Content)
	}
}

func TestBuildMessages_inlineImageRef(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "err.png")
	imgData := []byte("pngdata")
	os.WriteFile(imgPath, imgData, 0o644)

	msgs, err := BuildMessages("", nil, "look at @"+imgPath+" what's wrong?")
	if err != nil {
		t.Fatal(err)
	}
	msg := msgs[0]
	if len(msg.MultiContent) == 0 {
		t.Fatal("expected MultiContent for inline image ref")
	}
	var foundImage bool
	for _, part := range msg.MultiContent {
		if part.Type == openai.ChatMessagePartTypeImageURL {
			foundImage = true
		}
	}
	if !foundImage {
		t.Error("expected image_url part for @image.png")
	}
}

func TestStripFileBlocks_multiContent(t *testing.T) {
	parts := []openai.ChatMessagePart{
		{Type: openai.ChatMessagePartTypeText, Text: "File: x.go\n```go\npackage x\n```\n\nsome question"},
		{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,abc"}},
	}
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, MultiContent: parts},
	}
	stripped := stripFileBlocks(msgs)
	if stripped[0].MultiContent != nil {
		t.Error("MultiContent should be collapsed after stripping")
	}
	if stripped[0].Content != "some question" {
		t.Errorf("unexpected stripped content: %q", stripped[0].Content)
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
