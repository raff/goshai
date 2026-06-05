package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
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

// debugTransport logs outgoing requests and incoming responses to stderr.
type debugTransport struct {
	rt http.RoundTripper
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	dump, _ := httputil.DumpRequestOut(req, true)
	fmt.Fprintf(os.Stderr, "--- request ---\n%s\n", dump)

	resp, err := d.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// For SSE streaming responses, only dump headers to avoid consuming the body.
	isStream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	dump, _ = httputil.DumpResponse(resp, !isStream)
	fmt.Fprintf(os.Stderr, "--- response ---\n%s\n", dump)

	return resp, nil
}

// Client is a minimal OpenAI-compatible HTTP client.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewClient(baseURL, token string, verbose bool) *Client {
	c := &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      token,
		HTTPClient: &http.Client{},
	}
	if verbose {
		c.HTTPClient.Transport = &debugTransport{rt: http.DefaultTransport}
	}
	return c
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

// Usage holds token consumption reported by the API.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatOptions holds optional per-request parameters.
type ChatOptions struct {
	Think          bool
	ThinkingBudget int
	Stats          bool
}

const defaultThinkingBudget = 10000

type thinkingParams struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	Stream        bool            `json:"stream,omitempty"`
	Thinking      *thinkingParams `json:"thinking,omitempty"`
	StreamOptions *streamOptions  `json:"stream_options,omitempty"`
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
	if stream && opts.Stats {
		r.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return r
}

// ChatCompletion sends a non-streaming chat request and returns the response content and usage.
func (c *Client) ChatCompletion(ctx context.Context, model string, messages []Message, opts ChatOptions) (string, Usage, error) {
	payload, err := json.Marshal(buildChatRequest(model, messages, opts, false))
	if err != nil {
		return "", Usage{}, err
	}
	req, err := c.newRequest(ctx, "POST", "/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, apiError(resp)
	}

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", Usage{}, err
	}
	if len(result.Choices) == 0 {
		return "", result.Usage, nil
	}
	return result.Choices[0].Message.Content, result.Usage, nil
}

// ChatStream reads a server-sent-events streaming response.
type ChatStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	Usage   Usage
}

func (s *ChatStream) Close() { s.body.Close() }

// Recv returns the next content chunk, or io.EOF when the stream ends.
// After io.EOF, s.Usage holds the token counts if stream_options.include_usage was set.
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
			Usage *Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return "", err
		}
		if chunk.Usage != nil {
			s.Usage = *chunk.Usage
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
