// Package ai provides the OpenRouter chat client used by the roleplay bot.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is OpenRouter's OpenAI-compatible API root.
	DefaultBaseURL = "https://openrouter.ai/api/v1"

	defaultTimeout     = 60 * time.Second
	defaultMaxResponse = 1 << 20
	maxErrorBodyChars  = 400
)

// Message is one chat turn in an OpenAI-compatible request.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string

	// Images are attached to a user turn as multimodal content parts. They are
	// ignored on other roles.
	Images []Image
}

// Image is one image URL sent to a vision-capable model.
type Image struct {
	Label  string
	URL    string
	Detail string
}

// Usage reports token usage for a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Reply is a completed chat request.
type Reply struct {
	Text      string
	Usage     Usage
	ToolCalls []ToolCall
}

// Tool describes a function the model may call.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the callable part of a Tool.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name and JSON arguments of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Options configures a Client.
type Options struct {
	// APIKey is the OpenRouter key. It is required.
	APIKey string

	// BaseURL overrides the API root. It is primarily for tests.
	BaseURL string

	// Model is the OpenRouter model slug. It is required.
	Model string

	// AppName and AppURL are optional OpenRouter attribution headers.
	AppName string
	AppURL  string

	// MaxTokens caps the completion. Zero leaves the choice to the model.
	MaxTokens int

	// Timeout caps a single request. Zero uses 60 seconds.
	Timeout time.Duration

	// HTTPClient overrides the HTTP client.
	HTTPClient *http.Client
}

// Client calls the OpenRouter chat completions endpoint.
type Client struct {
	apiKey    string
	baseURL   string
	model     string
	appName   string
	appURL    string
	maxTokens int
	http      *http.Client
}

// New builds a chat client.
func New(opts Options) (*Client, error) {
	apiKey := strings.TrimSpace(opts.APIKey)
	if apiKey == "" {
		return nil, errors.New("ai: API key is empty")
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		return nil, errors.New("ai: model is empty")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if !strings.HasPrefix(baseURL, "https://") && !strings.HasPrefix(baseURL, "http://") {
		return nil, fmt.Errorf("ai: base URL %q must be absolute", baseURL)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	return &Client{
		apiKey:    apiKey,
		baseURL:   baseURL,
		model:     model,
		appName:   strings.TrimSpace(opts.AppName),
		appURL:    strings.TrimSpace(opts.AppURL),
		maxTokens: opts.MaxTokens,
		http:      httpClient,
	}, nil
}

// Model returns the configured model slug.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

type requestMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// contentPart is one multimodal content entry. OpenRouter's OpenAI-compatible
// API accepts a plain string or an array of these parts.
type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// messageContent keeps text-only turns as a string and switches to content
// parts when images are attached.
func messageContent(text string, images []Image) any {
	usable := make([]Image, 0, len(images))
	for _, image := range images {
		if strings.TrimSpace(image.URL) != "" {
			usable = append(usable, image)
		}
	}
	if len(usable) == 0 {
		return text
	}

	parts := make([]contentPart, 0, len(usable)*2+1)
	if text != "" {
		parts = append(parts, contentPart{Type: "text", Text: text})
	}
	for _, image := range usable {
		if label := strings.TrimSpace(image.Label); label != "" {
			parts = append(parts, contentPart{Type: "text", Text: label})
		}
		parts = append(parts, contentPart{
			Type: "image_url",
			ImageURL: &imageURL{
				URL:    image.URL,
				Detail: strings.TrimSpace(image.Detail),
			},
		})
	}
	return parts
}

type chatRequest struct {
	Model     string           `json:"model"`
	Messages  []requestMessage `json:"messages"`
	Tools     []Tool           `json:"tools,omitempty"`
	MaxTokens int              `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Chat sends a system prompt and conversation to the model. An empty system
// prompt is allowed.
func (c *Client) Chat(ctx context.Context, system string, messages []Message) (Reply, error) {
	return c.ChatWithTools(ctx, system, messages, nil)
}

// ChatWithTools is Chat with optional function tools. The model may answer
// with tool calls instead of text; the caller runs them and sends the results
// back as a tool message.
func (c *Client) ChatWithTools(ctx context.Context, system string, messages []Message, tools []Tool) (Reply, error) {
	if c == nil || c.http == nil {
		return Reply{}, errors.New("ai: client is disabled")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	wire := make([]requestMessage, 0, len(messages)+1)
	if text := strings.TrimSpace(system); text != "" {
		wire = append(wire, requestMessage{Role: "system", Content: text})
	}
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		content := strings.TrimSpace(message.Content)
		if role == "" {
			continue
		}
		if content == "" && len(message.ToolCalls) == 0 && message.ToolCallID == "" && len(message.Images) == 0 {
			continue
		}
		wire = append(wire, requestMessage{
			Role:       role,
			Content:    messageContent(content, message.Images),
			ToolCalls:  message.ToolCalls,
			ToolCallID: message.ToolCallID,
		})
	}
	if len(wire) == 0 {
		return Reply{}, errors.New("ai: no messages to send")
	}

	body, err := json.Marshal(chatRequest{
		Model:     c.model,
		Messages:  wire,
		Tools:     tools,
		MaxTokens: c.maxTokens,
	})
	if err != nil {
		return Reply{}, fmt.Errorf("ai: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Reply{}, fmt.Errorf("ai: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.appURL != "" {
		req.Header.Set("HTTP-Referer", c.appURL)
	}
	if c.appName != "" {
		req.Header.Set("X-Title", c.appName)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Reply{}, fmt.Errorf("ai: request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, defaultMaxResponse))
	if err != nil {
		return Reply{}, fmt.Errorf("ai: reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Reply{}, fmt.Errorf("ai: status %d: %s", resp.StatusCode, summarizeError(data))
	}

	var decoded chatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Reply{}, fmt.Errorf("ai: decoding response: %w", err)
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return Reply{}, fmt.Errorf("ai: %s", strings.TrimSpace(decoded.Error.Message))
	}
	if len(decoded.Choices) == 0 {
		return Reply{}, errors.New("ai: model returned no choices")
	}

	text := strings.TrimSpace(decoded.Choices[0].Message.Content)
	toolCalls := decoded.Choices[0].Message.ToolCalls
	if text == "" && len(toolCalls) == 0 {
		return Reply{}, errors.New("ai: model returned an empty reply")
	}
	return Reply{Text: text, Usage: decoded.Usage, ToolCalls: toolCalls}, nil
}

func summarizeError(data []byte) string {
	text := strings.Join(strings.Fields(string(data)), " ")
	if text == "" {
		return "empty error body"
	}
	runes := []rune(text)
	if len(runes) > maxErrorBodyChars {
		text = string(runes[:maxErrorBodyChars]) + "..."
	}
	return text
}
