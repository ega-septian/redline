// Package llm adalah client minimal untuk Claude Messages API (tanpa SDK).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	APIKey     string
	BaseURL    string
	HTTP       *http.Client
	MaxRetries int
}

func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		APIKey:     apiKey,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTP:       &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 2,
	}
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolChoice {"type":"tool","name":"x"} memaksa model menjawab lewat tool tersebut,
// sehingga output selalu berupa JSON sesuai schema.
type ToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type Request struct {
	Model      string      `json:"model"`
	MaxTokens  int         `json:"max_tokens"`
	System     string      `json:"system,omitempty"`
	Messages   []Message   `json:"messages"`
	Tools      []Tool      `json:"tools,omitempty"`
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Response struct {
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Content    []ContentBlock `json:"content"`
	Usage      Usage          `json:"usage"`
}

// ToolInput mengembalikan input dari blok tool_use dengan nama tertentu.
func (r *Response) ToolInput(name string) (json.RawMessage, bool) {
	for _, c := range r.Content {
		if c.Type == "tool_use" && c.Name == name {
			return c.Input, true
		}
	}
	return nil, false
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string { return fmt.Sprintf("claude API %d: %s", e.StatusCode, e.Body) }

// CreateMessage memanggil POST /v1/messages. Error 429/5xx di-retry dengan backoff.
func (c *Client) CreateMessage(ctx context.Context, req Request) (*Response, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/messages", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("x-api-key", c.APIKey)
		httpReq.Header.Set("anthropic-version", "2023-06-01")
		httpReq.Header.Set("content-type", "application/json")

		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retryable && attempt < c.MaxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			msg := string(body)
			if len(msg) > 500 {
				msg = msg[:500]
			}
			return nil, &APIError{StatusCode: resp.StatusCode, Body: msg}
		}
		var out Response
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &out, nil
	}
}

// Pricing adalah harga model dalam USD per 1 juta token.
type Pricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

func (p Pricing) Cost(u Usage) float64 {
	return float64(u.InputTokens)*p.InputPerMTok/1e6 + float64(u.OutputTokens)*p.OutputPerMTok/1e6
}
