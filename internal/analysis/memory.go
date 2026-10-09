package analysis

import (
	"context"

	"redline/internal/embed"
	"redline/internal/store"
	"redline/internal/triage"
)

// Embedder membuat embedding teks (Voyage AI). nil = pencarian kasus mirip mati.
type Embedder interface {
	Embed(ctx context.Context, texts []string) (*embed.Result, error)
}

// Batas kemiripan kasus mirip. Embedding membuat skor teks yang tidak berhubungan pun cukup tinggi
// (uji voyage-4-lite: sekitar 0.50), jadi batasnya harus jauh di atas itu.
const (
	minSimilarity = 0.70
	maxSimilar    = 3
	embedBatch    = 100
	maxEmbedRunes = 4000
)

// EmbedPending membuat embedding untuk kelompok yang belum punya, dalam satu request.
// Dipanggil setelah ingest supaya analisis berikutnya tidak perlu menunggu. Mengembalikan jumlah kelompok.
func (a *Analyzer) EmbedPending(ctx context.Context) (int, error) {
	if a.Embed == nil {
		return 0, nil
	}
	// Pemanggil kedua menunggu, lalu biasanya tidak menemukan apa-apa lagi untuk di-embed.
	a.embedMu.Lock()
	defer a.embedMu.Unlock()
	pending, err := a.Store.PendingEmbeddings(ctx, a.EmbedModel, embedBatch)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	ids := make([]string, len(pending))
	texts := make([]string, len(pending))
	for i, g := range pending {
		ids[i], texts[i] = g.GroupID, embedText(g.Error)
	}
	res, err := a.Embed.Embed(ctx, texts)
	if err != nil {
		return 0, err
	}
	if err := a.Store.SaveEmbeddings(ctx, a.EmbedModel, ids, res.Vectors); err != nil {
		return 0, err
	}
	a.logger().Info("embedding dibuat", "kelompok", len(ids), "token", res.Tokens, "model", a.EmbedModel)
	return len(ids), nil
}

// embedText: pesan error yang dinormalisasi (id, waktu, angka panjang diganti), supaya yang dibandingkan
// adalah jenis kesalahannya, bukan datanya.
func embedText(msg string) string {
	text := triage.Normalize(msg)
	if r := []rune(text); len(r) > maxEmbedRunes {
		text = string(r[:maxEmbedRunes])
	}
	return text
}

// facts mengumpulkan fakta dari database, ditambah kasus mirip kalau embedding aktif.
// Kegagalan Voyage tidak menggagalkan analisis: kasus mirip hanya pelengkap.
func (a *Analyzer) facts(ctx context.Context, fingerprint string) (*store.Facts, error) {
	f, err := a.Store.Facts(ctx, fingerprint)
	if err != nil || a.Embed == nil {
		return f, err
	}
	if _, err := a.EmbedPending(ctx); err != nil {
		a.logger().Warn("embedding gagal, analisis tanpa kasus mirip", "err", err)
		return f, nil
	}
	similar, err := a.Store.SimilarCases(ctx, fingerprint, a.EmbedModel, minSimilarity, maxSimilar)
	if err != nil {
		a.logger().Warn("cari kasus mirip gagal", "err", err)
		return f, nil
	}
	f.Similar = similar
	return f, nil
}
