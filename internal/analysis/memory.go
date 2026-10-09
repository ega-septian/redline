package analysis

import (
	"context"
	"errors"

	"redline/internal/contract"
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

// Contracts mengambil dokumen kontrak API (OpenAPI) per project. url kosong = project tanpa kontrak.
type Contracts interface {
	Fetch(ctx context.Context, project string) (url string, document []byte, err error)
}

// SnapshotContracts memotret kontrak setiap project di run ini, supaya analisis memakai kontrak yang
// berlaku saat itu (kontrak bisa berubah antar versi aplikasi). Gagal memotret tidak menggagalkan ingest.
func (a *Analyzer) SnapshotContracts(ctx context.Context, runID int64, projects []string) {
	if a.Contracts == nil {
		return
	}
	for _, project := range projects {
		url, doc, err := a.Contracts.Fetch(ctx, project)
		if url == "" {
			continue
		}
		if err == nil {
			_, err = a.Store.SaveRunContract(ctx, runID, project, url, string(doc))
		}
		if err != nil {
			a.logger().Warn("potret kontrak gagal; run ini dianalisis tanpa kontrak", "run", runID, "project", project, "err", err)
		}
	}
}

// facts mengumpulkan fakta dari database, ditambah kontrak API dan kasus mirip kalau aktif.
// Kegagalan membaca kontrak atau embedding tidak menggagalkan analisis: keduanya pelengkap.
func (a *Analyzer) facts(ctx context.Context, fingerprint string) (*store.Facts, error) {
	f, err := a.Store.Facts(ctx, fingerprint)
	if err != nil {
		return f, err
	}
	if err := a.addContract(ctx, f); err != nil {
		a.logger().Warn("kontrak API tidak bisa dibaca, analisis tanpa kontrak", "project", f.Group.Project, "err", err)
	}
	if a.Embed == nil {
		return f, nil
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

// addContract mengisi kontrak saat test gagal, pelanggaran response terhadap kontrak itu, dan apakah kontrak
// untuk endpoint yang dipanggil berubah dibanding run lulus terakhir.
func (a *Analyzer) addContract(ctx context.Context, f *store.Facts) error {
	if f.FailRunID == 0 || len(f.CurrentCalls) == 0 {
		return nil
	}
	doc, err := a.runContract(ctx, f.FailRunID, f.Group.Project)
	if doc == nil {
		return err
	}
	f.Contract = doc.Describe(f.CurrentCalls)
	f.ContractViolations = doc.Violations(f.CurrentCalls)
	f.ContractChanged = "tidak diketahui"
	if f.LastPass == nil {
		return nil
	}
	before, err := a.runContract(ctx, f.LastPass.RunID, f.Group.Project)
	if before == nil {
		return err
	}
	if prev := before.Describe(f.CurrentCalls); prev == f.Contract {
		f.ContractChanged = "tidak"
	} else {
		f.ContractChanged, f.ContractBefore = "ya", prev
	}
	return nil
}

// runContract: potret kontrak project pada run itu, atau nil kalau tidak ada.
func (a *Analyzer) runContract(ctx context.Context, runID int64, project string) (*contract.Document, error) {
	id, document, err := a.Store.RunContract(ctx, runID, project)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return contract.ParseCached(id, []byte(document))
}
