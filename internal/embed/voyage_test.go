package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoyage_Embed(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer kunci" || r.URL.Path != "/v1/embeddings" {
			t.Errorf("request salah: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			Model     string   `json:"model"`
			Input     []string `json:"input"`
			InputType string   `json:"input_type"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "voyage-4-lite" || len(body.Input) != 2 || body.InputType != "document" {
			t.Errorf("body salah: %+v", body)
		}
		// Urutan dibalik: hasil harus disusun ulang berdasarkan index.
		_, _ = w.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}],"usage":{"total_tokens":12}}`))
	}))
	defer srv.Close()

	res, err := NewVoyage("kunci", "voyage-4-lite", srv.URL+"/").Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tokens != 12 || res.Vectors[0][0] != 1 || res.Vectors[1][1] != 1 || calls != 1 {
		t.Fatalf("hasil salah: %+v", res)
	}
	if empty, err := NewVoyage("kunci", "m", srv.URL).Embed(context.Background(), nil); err != nil || len(empty.Vectors) != 0 || calls != 1 {
		t.Errorf("tanpa teks tidak boleh memanggil API: %+v %v", empty, err)
	}
}

func TestVoyage_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid key"}`))
	}))
	defer srv.Close()
	_, err := NewVoyage("salah", "m", srv.URL).Embed(context.Background(), []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("mau error 401, dapat %v", err)
	}
}
