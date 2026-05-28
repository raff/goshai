package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"

	PartTypeText     = "text"
	PartTypeImageURL = "image_url"
)

// ImageURL holds the URL for an image content part.
type ImageURL struct {
	URL string `json:"url"`
}

// MessagePart is a single content part in a multi-part message.
type MessagePart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// Message is a chat message. Content holds plain-text messages; MultiContent
// holds mixed text/image messages. Only one should be set.
// The content field is marshaled as a string or array accordingly.
type Message struct {
	Role         string        `json:"role"`
	Content      string        `json:"-"`
	MultiContent []MessagePart `json:"-"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	type wire struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	w := wire{Role: m.Role}
	var err error
	if len(m.MultiContent) > 0 {
		w.Content, err = json.Marshal(m.MultiContent)
	} else {
		w.Content, err = json.Marshal(m.Content)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type wire struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Role = w.Role
	if len(w.Content) == 0 {
		return nil
	}
	if w.Content[0] == '[' {
		return json.Unmarshal(w.Content, &m.MultiContent)
	}
	return json.Unmarshal(w.Content, &m.Content)
}

// Client is a minimal OpenAI-compatible HTTP client.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTPClient: &http.Client{},
	}
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	return req, nil
}

func apiError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// ModelInfo holds the ID of a model returned by the /models endpoint.
type ModelInfo struct {
	ID string `json:"id"`
}

// ListModels fetches the list of available models from the server.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := c.newRequest(ctx, "GET", "/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}
	var result struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ChatOptions holds optional per-request parameters.
type ChatOptions struct {
	Think          bool
	ThinkingBudget int
}

const defaultThinkingBudget = 10000

type thinkingParams struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type chatRequest struct {
	Model    string          `json:"model"`
	Messages []Message       `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
	Thinking *thinkingParams `json:"thinking,omitempty"`
}

func buildChatRequest(model string, messages []Message, opts ChatOptions, stream bool) chatRequest {
	r := chatRequest{Model: model, Messages: messages, Stream: stream}
	if opts.Think {
		budget := opts.ThinkingBudget
		if budget == 0 {
			budget = defaultThinkingBudget
		}
		r.Thinking = &thinkingParams{Type: "enabled", BudgetTokens: budget}
	}
	return r
}

// ChatCompletion sends a non-streaming chat request and returns the response content.
func (c *Client) ChatCompletion(ctx context.Context, model string, messages []Message, opts ChatOptions) (string, error) {
	payload, err := json.Marshal(buildChatRequest(model, messages, opts, false))
	if err != nil {
		return "", err
	}
	req, err := c.newRequest(ctx, "POST", "/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp)
	}

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", nil
	}
	return result.Choices[0].Message.Content, nil
}

// ChatStream reads a server-sent-events streaming response.
type ChatStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
}

func (s *ChatStream) Close() { s.body.Close() }

// Recv returns the next content chunk, or io.EOF when the stream ends.
func (s *ChatStream) Recv() (string, error) {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return "", io.EOF
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return "", err
		}
		if len(chunk.Choices) > 0 {
			return chunk.Choices[0].Delta.Content, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

// ChatCompletionStream opens a streaming chat request and returns a ChatStream.
func (c *Client) ChatCompletionStream(ctx context.Context, model string, messages []Message, opts ChatOptions) (*ChatStream, error) {
	payload, err := json.Marshal(buildChatRequest(model, messages, opts, true))
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, "POST", "/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, apiError(resp)
	}
	return &ChatStream{body: resp.Body, scanner: bufio.NewScanner(resp.Body)}, nil
}
