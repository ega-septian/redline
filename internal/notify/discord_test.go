package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"redline/internal/notify"
)

func TestDiscordSend(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request salah: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("body bukan JSON: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := notify.NewDiscord().Send(context.Background(), server.URL, notify.Message{
		Title: "2 new failures in run #7", Description: "• TC-A · brand\n• TC-B · users",
		Color: 0xE5484D,
		Fields: []notify.Field{
			{Name: "Branch", Value: "main", Inline: true},
			{Name: "Commit", Value: "abc1234", Inline: true},
		},
		Footer: "Redline",
	})
	if err != nil {
		t.Fatal(err)
	}
	embeds, _ := got["embeds"].([]any)
	if len(embeds) != 1 {
		t.Fatalf("embeds = %#v", got)
	}
	embed, _ := embeds[0].(map[string]any)
	if embed["title"] != "2 new failures in run #7" || embed["color"] != float64(0xE5484D) {
		t.Errorf("embed salah: %#v", embed)
	}
	if embed["description"] != "• TC-A · brand\n• TC-B · users" {
		t.Errorf("description salah: %#v", embed["description"])
	}
	fields, _ := embed["fields"].([]any)
	if len(fields) != 2 {
		t.Errorf("fields = %#v", embed["fields"])
	}
}

func TestDiscordSendError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid webhook", http.StatusBadRequest)
	}))
	defer server.Close()

	err := notify.NewDiscord().Send(context.Background(), server.URL, notify.Message{Title: "x"})
	if err == nil {
		t.Fatal("HTTP 400 harus error")
	}
}

func TestDiscordSendDoesNotLeakWebhook(t *testing.T) {
	err := notify.NewDiscord().Send(context.Background(), "http://127.0.0.1:1/webhook-secret-token", notify.Message{Title: "x"})
	if err == nil {
		t.Fatal("host mati harus error")
	}
	if strings.Contains(err.Error(), "webhook-secret-token") {
		t.Errorf("error membocorkan token: %v", err)
	}
}
