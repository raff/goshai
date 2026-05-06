package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

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

	var sb strings.Builder
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		lang := extToLang[strings.ToLower(filepath.Ext(path))]
		fmt.Fprintf(&sb, "File: %s\n```%s\n%s\n```\n\n", filepath.Base(path), lang, string(data))
	}
	if userPrompt != "" {
		sb.WriteString(userPrompt)
	}

	msgs = append(msgs, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: sb.String(),
	})

	return msgs, nil
}
