// Package embed adalah client minimal untuk Voyage AI Embeddings API (tanpa SDK).
// Embedding mengubah teks menjadi deretan angka; teks yang maknanya mirip menghasilkan angka yang berdekatan.
package embed

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

type Voyage struct {
	APIKey     string
	Model      string
	BaseURL    string
	HTTP       *http.Client
	MaxRetries int
}

func NewVoyage(apiKey, model, baseURL string) *Voyage {
	return &Voyage{
		APIKey:     apiKey,
		Model:      model,
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		MaxRetries: 3,
	}
}

// Result adalah embedding untuk setiap teks (urutan sama dengan input) dan jumlah token yang dipakai.
type Result struct {
	Vectors [][]float32
	Tokens  int
}

// Embed membuat embedding untuk banyak teks dalam satu request. 429/5xx di-retry dengan backoff:
// akun tanpa metode pembayaran punya rate limit yang sangat rendah.
func (v *Voyage) Embed(ctx context.Context, texts []string) (*Result, error) {
	if len(texts) == 0 {
		return &Result{}, nil
	}
	payload, err := json.Marshal(map[string]any{
		"model":      v.Model,
		"input":      texts,
		"input_type": "document", // pesan error dibandingkan dengan pesan error: simetris
	})
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.BaseURL+"/v1/embeddings", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+v.APIKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := v.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if retryable && attempt < v.MaxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(5*(attempt+1)) * time.Second):
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			msg := string(body)
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return nil, fmt.Errorf("voyage API %d: %s", resp.StatusCode, msg)
		}
		var out struct {
			Data []struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			} `json:"data"`
			Usage struct {
				TotalTokens int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("decode response voyage: %w", err)
		}
		if len(out.Data) != len(texts) {
			return nil, fmt.Errorf("voyage mengembalikan %d embedding untuk %d teks", len(out.Data), len(texts))
		}
		res := &Result{Vectors: make([][]float32, len(texts)), Tokens: out.Usage.TotalTokens}
		for _, d := range out.Data {
			if d.Index < 0 || d.Index >= len(texts) {
				return nil, fmt.Errorf("index embedding %d di luar jangkauan", d.Index)
			}
			res.Vectors[d.Index] = d.Embedding
		}
		return res, nil
	}
}
