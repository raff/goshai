package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// fileBlockRe matches "File: ...\n```lang\n...content...\n```\n\n" blocks embedded in user messages.
var fileBlockRe = regexp.MustCompile("(?s)File: [^\n]+\n```[^\n]*\n.*?```\n\n")

var extToLang = map[string]string{
	".go":   "go",
	".py":   "python",
	".js":   "javascript",
	".ts":   "typescript",
	".sh":   "bash",
	".bash": "bash",
	".yaml": "yaml",
	".yml":  "yaml",
	".json": "json",
	".md":   "markdown",
	".c":    "c",
	".cpp":  "cpp",
	".h":    "c",
	".rs":   "rust",
	".rb":   "ruby",
	".java": "java",
	".kt":   "kotlin",
	".sql":  "sql",
	".html": "html",
	".css":  "css",
	".xml":  "xml",
	".toml": "toml",
}

// buildUserContent assembles the user message content: file blocks followed by the prompt.
func buildUserContent(files []string, userPrompt string) (string, error) {
	var sb strings.Builder
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		lang := extToLang[strings.ToLower(filepath.Ext(path))]
		fmt.Fprintf(&sb, "File: %s\n```%s\n%s\n```\n\n", filepath.Base(path), lang, string(data))
	}
	if userPrompt != "" {
		sb.WriteString(userPrompt)
	}
	return sb.String(), nil
}

// stripFileContent removes embedded file blocks from a single message content string.
func stripFileContent(content string) string {
	return strings.TrimSpace(fileBlockRe.ReplaceAllString(content, ""))
}

// stripFileBlocks returns a copy of the message slice with file blocks removed from user messages.
func stripFileBlocks(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, len(messages))
	copy(result, messages)
	for i, msg := range result {
		if msg.Role == openai.ChatMessageRoleUser {
			result[i].Content = stripFileContent(msg.Content)
		}
	}
	return result
}

// buildGenPromptRequest formats a cleaned conversation into a meta-prompt that asks the model
// to produce a reusable prompt capturing the original task intent.
func buildGenPromptRequest(messages []openai.ChatCompletionMessage) string {
	var sb strings.Builder
	sb.WriteString("Here is a conversation history (file contents have been removed):\n\n")
	for _, msg := range messages {
		switch msg.Role {
		case openai.ChatMessageRoleSystem:
			fmt.Fprintf(&sb, "SYSTEM: %s\n\n", msg.Content)
		case openai.ChatMessageRoleUser:
			fmt.Fprintf(&sb, "USER: %s\n\n", msg.Content)
		case openai.ChatMessageRoleAssistant:
			fmt.Fprintf(&sb, "ASSISTANT: %s\n\n", msg.Content)
		}
	}
	sb.WriteString("Based on this conversation, write a concise, reusable prompt that captures the task and intent. " +
		"The prompt should be a template that works on different input files — " +
		"do not reference specific file names or their contents. " +
		"Output ONLY the prompt text, with no preamble or explanation.")
	return sb.String()
}

// BuildMessages assembles the messages slice for the API call.
// File contents are prepended to userPrompt as fenced code blocks.
func BuildMessages(systemPrompt string, files []string, userPrompt string) ([]openai.ChatCompletionMessage, error) {
	var msgs []openai.ChatCompletionMessage

	if systemPrompt != "" {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	content, err := buildUserContent(files, userPrompt)
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: content,
	})

	return msgs, nil
}
