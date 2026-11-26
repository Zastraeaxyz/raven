package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatSendsSystemAndMessages(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Fatalf("authorization = %q", auth)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"content":"  hello there  "}}],
			"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}
		}`)
	}))
	defer server.Close()

	client, err := New(Options{
		APIKey:    "test-key",
		BaseURL:   server.URL,
		Model:     "test/model",
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reply, err := client.Chat(context.Background(), "be brief", []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
		{Role: "", Content: "ignored"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Text != "hello there" {
		t.Fatalf("text = %q", reply.Text)
	}
	if reply.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", reply.Usage)
	}

	if got["model"] != "test/model" {
		t.Fatalf("model = %v", got["model"])
	}
	if got["max_tokens"] != float64(64) {
		t.Fatalf("max_tokens = %v", got["max_tokens"])
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	first := messages[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be brief" {
		t.Fatalf("first message = %#v", first)
	}
}

func TestChatWithTools(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"chastity_status","arguments":"{}"}}]}}]}`)
	}))
	defer server.Close()

	client, err := New(Options{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reply, err := client.ChatWithTools(context.Background(), "sys", []Message{
		{Role: "user", Content: "am I locked?"},
	}, []Tool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "chastity_status",
			Description: "Read the caller's lock status.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}})
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}
	if reply.Text != "" {
		t.Fatalf("text = %q", reply.Text)
	}
	if len(reply.ToolCalls) != 1 || reply.ToolCalls[0].Function.Name != "chastity_status" {
		t.Fatalf("tool calls = %+v", reply.ToolCalls)
	}

	tools, ok := got["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", got["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("tool = %#v", tool)
	}
}

func TestChatSendsToolResults(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"yes, you are locked"}}]}`)
	}))
	defer server.Close()

	client, err := New(Options{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reply, err := client.Chat(context.Background(), "", []Message{
		{Role: "user", Content: "am I locked?"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: ToolCallFunction{Name: "chastity_status", Arguments: "{}"},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "1 day remaining"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if reply.Text != "yes, you are locked" {
		t.Fatalf("text = %q", reply.Text)
	}

	messages := got["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	assistant := messages[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Fatalf("assistant = %#v", assistant)
	}
	calls, ok := assistant["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls = %#v", assistant["tool_calls"])
	}
	toolMessage := messages[2].(map[string]any)
	if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_1" {
		t.Fatalf("tool message = %#v", toolMessage)
	}
}

func TestChatErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"bad status": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"no key"}}`)
		},
		"empty choices": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"choices":[]}`)
		},
		"empty text": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"   "}}]}`)
		},
		"error member": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"error":{"message":"rate limited"}}`)
		},
	}

	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()

			client, err := New(Options{APIKey: "k", BaseURL: server.URL, Model: "m"})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := client.Chat(context.Background(), "", []Message{{Role: "user", Content: "hi"}}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestNewRejectsMissingConfiguration(t *testing.T) {
	if _, err := New(Options{Model: "m"}); err == nil {
		t.Fatal("expected an error for a missing key")
	}
	if _, err := New(Options{APIKey: "k"}); err == nil {
		t.Fatal("expected an error for a missing model")
	}
}

func TestChatSkipsEmptyMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "system") {
			t.Fatalf("request should not include an empty system prompt: %s", body)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	client, err := New(Options{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.Chat(context.Background(), "   ", []Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, err := client.Chat(context.Background(), "", nil); err == nil {
		t.Fatal("expected an error with no messages")
	}
}

func TestChatSendsImagesAsContentParts(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"a cat"}}]}`)
	}))
	defer server.Close()

	client, err := New(Options{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), "", []Message{{
		Role:    "user",
		Content: "what is this?",
		Images: []Image{
			{Label: "Image on the current message: cat.png", URL: "https://cdn.discordapp.com/cat.png", Detail: "auto"},
			{URL: "  "},
		},
	}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	parts, ok := messages[0].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("content = %#v", messages[0].(map[string]any)["content"])
	}
	if parts[0].(map[string]any)["type"] != "text" || parts[0].(map[string]any)["text"] != "what is this?" {
		t.Fatalf("text part = %#v", parts[0])
	}
	if parts[1].(map[string]any)["type"] != "text" || parts[1].(map[string]any)["text"] != "Image on the current message: cat.png" {
		t.Fatalf("label part = %#v", parts[1])
	}
	imagePart := parts[2].(map[string]any)
	if imagePart["type"] != "image_url" {
		t.Fatalf("image part = %#v", imagePart)
	}
	imageURL := imagePart["image_url"].(map[string]any)
	if imageURL["url"] != "https://cdn.discordapp.com/cat.png" || imageURL["detail"] != "auto" {
		t.Fatalf("image_url = %#v", imageURL)
	}
}

func TestChatKeepsImageOnlyMessages(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"a cat"}}]}`)
	}))
	defer server.Close()

	client, err := New(Options{APIKey: "k", BaseURL: server.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), "", []Message{{
		Role:   "user",
		Images: []Image{{URL: "https://cdn.discordapp.com/cat.png"}},
	}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", got["messages"])
	}
	parts, ok := messages[0].(map[string]any)["content"].([]any)
	if !ok || len(parts) != 1 || parts[0].(map[string]any)["type"] != "image_url" {
		t.Fatalf("content = %#v", messages[0].(map[string]any)["content"])
	}
}
