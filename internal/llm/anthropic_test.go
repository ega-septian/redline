package llm

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateMessage_ToolChoice(t *testing.T) {
	var got map[string]any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests) // harus di-retry
			return
		}
		if r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("anthropic-version") == "" || r.URL.Path != "/v1/messages" {
			t.Errorf("header/path salah: %v %s", r.Header, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got)
		w.Write([]byte(`{"model":"claude-haiku-5-5","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5},
			"content":[{"type":"tool_use","id":"t1","name":"report_verdict","input":{"category":"flaky"}}]}`))
	}))
	defer srv.Close()

	c := NewClient("sk-test", srv.URL+"/")
	resp, err := c.CreateMessage(context.Background(), Request{
		Model: "m", MaxTokens: 10, Messages: []Message{{Role: "user", Content: "hai"}},
		Tools:      []Tool{{Name: "report_verdict", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &ToolChoice{Type: "tool", Name: "report_verdict"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("429 harus di-retry, calls=%d", calls)
	}
	tc := got["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "report_verdict" {
		t.Errorf("tool_choice salah: %v", tc)
	}
	input, ok := resp.ToolInput("report_verdict")
	if !ok || !strings.Contains(string(input), "flaky") {
		t.Errorf("tool input tidak terbaca: %s", input)
	}
	if c := (Pricing{InputPerMTok: 0.10, OutputPerMTok: 0.50}).Cost(resp.Usage); math.Abs(c-3.5e-6) > 1e-12 {
		t.Error("perhitungan biaya salah")
	}
}

func TestCreateMessage_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()
	_, err := NewClient("salah", srv.URL).CreateMessage(context.Background(), Request{})
	if apiErr, ok := err.(*APIError); !ok || apiErr.StatusCode != 401 {
		t.Errorf("mau APIError 401, dapat %v", err)
	}
}
