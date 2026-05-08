package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// fileBlockRe matches "File: ...\n```lang\n...content...\n```\n\n" blocks embedded in user messages.
var fileBlockRe = regexp.MustCompile("(?s)File: [^\n]+\n```[^\n]*\n.*?```\n\n")

// inlineRefRe matches @"quoted path" or @unquoted-token in prompt text.
var inlineRefRe = regexp.MustCompile(`@"([^"]+)"|@(\S+)`)

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

var imageExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

func imageMediaType(path string) string {
	return imageExts[strings.ToLower(filepath.Ext(path))]
}

func buildImagePart(path, mime string) (openai.ChatMessagePart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return openai.ChatMessagePart{}, err
	}
	uri := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	return openai.ChatMessagePart{
		Type:     openai.ChatMessagePartTypeImageURL,
		ImageURL: &openai.ChatMessageImageURL{URL: uri},
	}, nil
}

func textFileBlock(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lang := extToLang[strings.ToLower(filepath.Ext(path))]
	return fmt.Sprintf("File: %s\n```%s\n%s\n```\n\n", filepath.Base(path), lang, string(data)), nil
}

// promptSegment is a piece of the user prompt: either literal text or a resolved file reference.
type promptSegment struct {
	text     string
	filePath string
}

// parseInlineRefs splits prompt into text/file-ref segments. @tokens that don't resolve to an
// existing file are kept as literal text.
func parseInlineRefs(prompt string) []promptSegment {
	if prompt == "" {
		return nil
	}
	var segs []promptSegment
	lastEnd := 0
	for _, m := range inlineRefRe.FindAllStringSubmatchIndex(prompt, -1) {
		var filePath string
		if m[2] >= 0 {
			filePath = prompt[m[2]:m[3]] // quoted form
		} else {
			filePath = prompt[m[4]:m[5]] // unquoted form
		}
		if _, err := os.Stat(filePath); err != nil {
			// File doesn't exist — treat the whole token as literal text.
			continue
		}
		if m[0] > lastEnd {
			segs = append(segs, promptSegment{text: prompt[lastEnd:m[0]]})
		}
		segs = append(segs, promptSegment{filePath: filePath})
		lastEnd = m[1]
	}
	if lastEnd < len(prompt) {
		segs = append(segs, promptSegment{text: prompt[lastEnd:]})
	}
	return segs
}

// appendTextPart appends text to the last part if it is a text part, otherwise adds a new text part.
func appendTextPart(parts []openai.ChatMessagePart, text string) []openai.ChatMessagePart {
	if text == "" {
		return parts
	}
	if len(parts) > 0 && parts[len(parts)-1].Type == openai.ChatMessagePartTypeText {
		parts[len(parts)-1].Text += text
		return parts
	}
	return append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: text})
}

// buildUserMessage assembles the user ChatCompletionMessage.
// -f files are prepended; @filename tokens in userPrompt are substituted inline.
// Uses Content string for text-only; switches to MultiContent when any file is an image.
func buildUserMessage(files []string, userPrompt string) (openai.ChatCompletionMessage, error) {
	segs := parseInlineRefs(userPrompt)

	// Detect whether any file (from -f or @) is an image or PDF.
	hasImage := false
	for _, path := range files {
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".pdf" {
			return openai.ChatCompletionMessage{}, fmt.Errorf(
				"PDF files require go-openai file-part support (not yet in v1.36.1): %s", path)
		}
		if imageMediaType(path) != "" {
			hasImage = true
		}
	}
	for _, seg := range segs {
		if seg.filePath == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(seg.filePath))
		if ext == ".pdf" {
			return openai.ChatCompletionMessage{}, fmt.Errorf(
				"PDF files require go-openai file-part support (not yet in v1.36.1): %s", seg.filePath)
		}
		if imageMediaType(seg.filePath) != "" {
			hasImage = true
		}
	}

	if !hasImage {
		// Text-only path: build Content string (backward-compatible).
		var sb strings.Builder
		for _, path := range files {
			block, err := textFileBlock(path)
			if err != nil {
				return openai.ChatCompletionMessage{}, err
			}
			sb.WriteString(block)
		}
		for _, seg := range segs {
			if seg.text != "" {
				sb.WriteString(seg.text)
			} else {
				block, err := textFileBlock(seg.filePath)
				if err != nil {
					return openai.ChatCompletionMessage{}, err
				}
				sb.WriteString(block)
			}
		}
		// If there were no @refs, segs is nil and we need to write the raw prompt.
		if segs == nil && userPrompt != "" {
			sb.WriteString(userPrompt)
		}
		return openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: sb.String(),
		}, nil
	}

	// Mixed or image-only path: build MultiContent.
	var parts []openai.ChatMessagePart

	for _, path := range files {
		if mime := imageMediaType(path); mime != "" {
			part, err := buildImagePart(path, mime)
			if err != nil {
				return openai.ChatCompletionMessage{}, err
			}
			parts = append(parts, part)
		} else {
			block, err := textFileBlock(path)
			if err != nil {
				return openai.ChatCompletionMessage{}, err
			}
			parts = appendTextPart(parts, block)
		}
	}

	for _, seg := range segs {
		if seg.text != "" {
			parts = appendTextPart(parts, seg.text)
		} else if mime := imageMediaType(seg.filePath); mime != "" {
			part, err := buildImagePart(seg.filePath, mime)
			if err != nil {
				return openai.ChatCompletionMessage{}, err
			}
			parts = append(parts, part)
		} else {
			block, err := textFileBlock(seg.filePath)
			if err != nil {
				return openai.ChatCompletionMessage{}, err
			}
			parts = appendTextPart(parts, block)
		}
	}
	// If there were no @refs, segs is nil and we need to append the raw prompt.
	if segs == nil && userPrompt != "" {
		parts = appendTextPart(parts, userPrompt)
	}

	return openai.ChatCompletionMessage{
		Role:         openai.ChatMessageRoleUser,
		MultiContent: parts,
	}, nil
}

// stripFileContent removes embedded file blocks from a single message content string.
func stripFileContent(content string) string {
	return strings.TrimSpace(fileBlockRe.ReplaceAllString(content, ""))
}

// stripFileBlocks returns a copy of the message slice with file blocks and images removed from
// user messages. MultiContent messages are collapsed to a Content string after stripping.
func stripFileBlocks(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, len(messages))
	copy(result, messages)
	for i, msg := range result {
		if msg.Role != openai.ChatMessageRoleUser {
			continue
		}
		if msg.MultiContent != nil {
			var sb strings.Builder
			for _, part := range msg.MultiContent {
				if part.Type == openai.ChatMessagePartTypeText {
					if stripped := stripFileContent(part.Text); stripped != "" {
						if sb.Len() > 0 {
							sb.WriteString(" ")
						}
						sb.WriteString(stripped)
					}
				}
				// image_url parts are dropped
			}
			result[i].MultiContent = nil
			result[i].Content = sb.String()
		} else {
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
func BuildMessages(systemPrompt string, files []string, userPrompt string) ([]openai.ChatCompletionMessage, error) {
	var msgs []openai.ChatCompletionMessage

	if systemPrompt != "" {
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: systemPrompt,
		})
	}

	userMsg, err := buildUserMessage(files, userPrompt)
	if err != nil {
		return nil, err
	}
	msgs = append(msgs, userMsg)

	return msgs, nil
}
