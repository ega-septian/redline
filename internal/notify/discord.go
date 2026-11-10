// Package notify delivers Redline summaries to chat webhooks. Discord is the first provider; the
// message shape is small enough to add Slack or Teams later without touching the ingest path.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Sender sends one already-built message to a webhook URL.
type Sender interface {
	Send(ctx context.Context, webhookURL string, message Message) error
}

// DeliveryError carries the HTTP status and the delay Discord asks for on a rate limit. The caller
// uses StatusCode to decide whether a retry is useful.
type DeliveryError struct {
	StatusCode int
	RetryAfter time.Duration
	Message    string
}

func (e *DeliveryError) Error() string { return e.Message }

// Message is a provider-neutral summary. Discord renders it as one embed.
type Message struct {
	Title       string
	Description string
	Color       int
	Fields      []Field
	Footer      string
}

// Field is one inline embed field.
type Field struct {
	Name   string
	Value  string
	Inline bool
}

// Discord posts a webhook message with one embed. It is safe to reuse: the HTTP client owns the
// connection pool and every Send is independent.
type Discord struct {
	HTTP *http.Client
}

func NewDiscord() *Discord {
	return &Discord{HTTP: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to scheme %q is not allowed", req.URL.Scheme)
			}
			return nil
		},
	}}
}

type discordEmbed struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color,omitempty"`
	Fields      []discordField `json:"fields,omitempty"`
	Footer      *discordFooter `json:"footer,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordFooter struct {
	Text string `json:"text"`
}

func (d *Discord) Send(ctx context.Context, webhookURL string, message Message) error {
	if !strings.HasPrefix(webhookURL, "http://") && !strings.HasPrefix(webhookURL, "https://") {
		return fmt.Errorf("webhook must be http(s)://")
	}
	embed := discordEmbed{
		Title:       clip(message.Title, 256),
		Description: clip(message.Description, 4096),
		Color:       message.Color,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
	for _, field := range message.Fields {
		if len(embed.Fields) == 25 {
			break
		}
		embed.Fields = append(embed.Fields, discordField{
			Name: clip(field.Name, 256), Value: clip(field.Value, 1024), Inline: field.Inline,
		})
	}
	if message.Footer != "" {
		embed.Footer = &discordFooter{Text: clip(message.Footer, 2048)}
	}
	payload := map[string]any{
		"username": "Redline",
		"embeds":   []discordEmbed{embed},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := d.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// url.Error includes the full URL, and a Discord webhook URL carries the secret token.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("Discord request failed: %v", urlErr.Err)
		}
		return fmt.Errorf("Discord request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		message := strings.TrimSpace(string(detail))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return &DeliveryError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter(resp.Header.Get("Retry-After"), detail),
			Message:    fmt.Sprintf("discord HTTP %d: %s", resp.StatusCode, message),
		}
	}
	return nil
}

// retryAfter reads Discord's JSON retry_after or the standard Retry-After header. It is deliberately
// capped by the caller, not here: the sender reports what Discord asked for.
func retryAfter(header string, body []byte) time.Duration {
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil && seconds > 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	var payload struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.RetryAfter > 0 {
		return time.Duration(payload.RetryAfter * float64(time.Second))
	}
	return 0
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
