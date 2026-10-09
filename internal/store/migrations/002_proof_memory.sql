-- Bukti, ingatan, dan rapor.
--
-- Bukti        eksperimen yang menjalankan test lagi (rerun, atau patch dari AI) untuk membuktikan penyebab.
-- Ingatan      peta kode (file yang dipakai test), embedding pesan error (untuk mencari kasus mirip),
--              dan aturan yang dipelajari dari kasus terbukti.
-- Rapor        riwayat tebakan aturan dan AI, dinilai setelah ada bukti.

-- pgvector: tipe vector dan jarak cosine (<=>). Dipasang di schema public supaya tetap ada walaupun
-- search_path menunjuk schema lain (dipakai test).
CREATE EXTENSION IF NOT EXISTS vector SCHEMA public;

-- Peta kode: file lokal yang dipakai test (file spec + file yang di-import langsung), relatif terhadap project.
ALTER TABLE test_results ADD COLUMN code_files TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE failure_groups
    -- Kapan label manual diberikan; rapor hanya menilai tebakan yang dibuat SEBELUM ada jawaban.
    ADD COLUMN labeled_at      TIMESTAMPTZ,
    -- Embedding pesan error (Voyage AI) untuk mencari kasus terbukti yang mirip maknanya.
    -- Tanpa ukuran tetap supaya model bisa diganti; hanya embedding dari model yang sama yang dibandingkan.
    ADD COLUMN embedding       vector,
    ADD COLUMN embedding_model TEXT NOT NULL DEFAULT '';

-- Eksperimen: bukti yang didapat dengan MENJALANKAN test lagi, bukan menebak dari teks error.
--   rerun  test dijalankan ulang apa adanya. Lulus = tidak konsisten (flaky); gagal lagi = bisa direproduksi.
--   patch  AI mengusulkan perbaikan kode test, test dijalankan di salinan project. Lulus = terbukti salah di test.
-- Eksperimen dijalankan di mesin yang punya kode test (CLI di project Playwright), lalu hasilnya dikirim ke sini.
CREATE TABLE experiments (
    id          BIGSERIAL PRIMARY KEY,
    group_id    TEXT NOT NULL REFERENCES failure_groups(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('rerun', 'patch')),
    -- passed: test lulus. failed: tetap gagal. rejected: patch ditolak penjaga (misalnya assertion dihapus).
    -- skipped: AI tidak mengusulkan patch karena menurutnya bukan salah test.
    outcome     TEXT NOT NULL CHECK (outcome IN ('passed', 'failed', 'rejected', 'skipped')),
    category    TEXT NOT NULL DEFAULT 'unknown'
                CHECK (category IN ('backend_bug', 'test_bug', 'environment', 'flaky', 'unknown')),
    hypothesis  TEXT NOT NULL DEFAULT '',                   -- dugaan yang diuji
    patch       TEXT NOT NULL DEFAULT '',                   -- diff yang diterapkan (kind = patch)
    detail      TEXT NOT NULL DEFAULT '',                   -- error setelah eksperimen, atau alasan ditolak
    runs        INTEGER NOT NULL DEFAULT 1,                 -- berapa kali test dijalankan
    passes      INTEGER NOT NULL DEFAULT 0,                 -- berapa kali lulus
    cost_usd    NUMERIC(12, 6) NOT NULL DEFAULT 0,          -- biaya AI untuk membuat patch
    run_by      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX experiments_group_idx ON experiments (group_id, id DESC);

-- Aturan yang dipelajari dari kasus terbukti (label manual, atau patch yang lulus).
-- AI mengusulkan pola regex, Redline mengujinya ke semua kasus lama, lalu manusia menyetujui.
-- Aturan aktif dipakai sebelum AI, jadi pola yang sama berikutnya diputuskan tanpa biaya.
CREATE TABLE learned_rules (
    id             BIGSERIAL PRIMARY KEY,
    pattern        TEXT NOT NULL,                           -- regex (sintaks Go/RE2) terhadap pesan error
    category       TEXT NOT NULL CHECK (category IN ('backend_bug', 'test_bug', 'environment', 'flaky', 'unknown')),
    summary        TEXT NOT NULL,
    next_step      TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'active', 'rejected')),
    learned_from   TEXT[] NOT NULL DEFAULT '{}',            -- id kelompok kasus yang jadi contoh
    hits           INTEGER NOT NULL DEFAULT 0,              -- saat diusulkan: kasus terbukti berkategori sama yang cocok
    also_matches   INTEGER NOT NULL DEFAULT 0,              -- saat diusulkan: kelompok tanpa bukti yang ikut cocok
    cost_usd       NUMERIC(12, 6) NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_by     TEXT NOT NULL DEFAULT '',
    decided_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX learned_rules_pattern_idx ON learned_rules (pattern, category);

-- Riwayat tebakan aturan dan AI. analyses hanya menyimpan hasil terakhir per versi prompt, sedangkan
-- rapor butuh tebakan awal untuk dibandingkan dengan bukti (eksperimen atau label manual).
CREATE TABLE predictions (
    id             BIGSERIAL PRIMARY KEY,
    group_id       TEXT NOT NULL REFERENCES failure_groups(id) ON DELETE CASCADE,
    source         TEXT NOT NULL CHECK (source IN ('rule', 'ai')),
    category       TEXT NOT NULL CHECK (category IN ('backend_bug', 'test_bug', 'environment', 'flaky', 'unknown')),
    confidence     TEXT NOT NULL CHECK (confidence IN ('low', 'medium', 'high')),
    prompt_version TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX predictions_group_idx ON predictions (group_id, id DESC);
