-- Skema Redline.
--
-- Istilah:
--   kelompok kegagalan  test yang sama yang gagal dengan error yang sama. id-nya adalah hash
--                       (kode pendek yang selalu sama untuk teks yang sama) dari nama test + error.
--   penyebab (cause)    kegagalan beberapa test yang penyebabnya sama, misalnya server mati atau
--                       endpoint yang sama membalas 5xx. cause_id adalah hash dari penyebab itu.

-- Satu kali eksekusi `npx playwright test` yang dikirim ke Redline.
CREATE TABLE runs (
    id                 BIGSERIAL PRIMARY KEY,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),  -- waktu diterima Redline
    started_at         TIMESTAMPTZ,                         -- waktu test mulai
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    source             TEXT NOT NULL DEFAULT 'local',       -- local / ci
    triggered_by       TEXT NOT NULL DEFAULT '',            -- siapa yang menjalankan: user, atau ci:<actor>
    ci_url             TEXT NOT NULL DEFAULT '',            -- halaman run CI: log + artifact trace/screenshot
    branch             TEXT NOT NULL DEFAULT '',
    commit_sha         TEXT NOT NULL DEFAULT '',
    app_version        TEXT NOT NULL DEFAULT '',            -- versi backend yang dites (opsional)
    playwright_version TEXT NOT NULL DEFAULT '',
    total              INTEGER NOT NULL DEFAULT 0,
    passed             INTEGER NOT NULL DEFAULT 0,
    failed             INTEGER NOT NULL DEFAULT 0,
    flaky              INTEGER NOT NULL DEFAULT 0,
    skipped            INTEGER NOT NULL DEFAULT 0,
    report_errors      TEXT[] NOT NULL DEFAULT '{}'         -- error di luar test, sudah disamarkan
);
CREATE INDEX runs_created_at_idx ON runs (created_at);

-- Kelompok kegagalan. Status: open -> resolved (lulus lagi) -> regressed (gagal lagi dengan error yang sama).
CREATE TABLE failure_groups (
    id              TEXT PRIMARY KEY,                       -- hash dari nama test + error
    project         TEXT NOT NULL,
    file            TEXT NOT NULL,
    title           TEXT NOT NULL,                          -- "describe › nama test"
    last_error      TEXT NOT NULL,                          -- pesan error terakhir, sudah disamarkan
    status          TEXT NOT NULL CHECK (status IN ('open', 'resolved', 'regressed')),
    occurrences     INTEGER NOT NULL DEFAULT 1,             -- berapa kali gagal
    regressions     INTEGER NOT NULL DEFAULT 0,             -- berapa kali gagal lagi setelah beres
    first_seen_run  BIGINT NOT NULL REFERENCES runs(id),
    last_seen_run   BIGINT NOT NULL REFERENCES runs(id),
    resolved_run    BIGINT REFERENCES runs(id),
    cause_id        TEXT NOT NULL DEFAULT '',               -- hash penyebab, sama untuk test lain dengan penyebab sama
    cause           TEXT NOT NULL DEFAULT '',               -- penyebab yang bisa dibaca, misalnya "POST /users/login → 500"
    manual_category TEXT CHECK (manual_category IN ('backend_bug', 'test_bug', 'environment', 'flaky', 'unknown')),
    manual_note     TEXT NOT NULL DEFAULT '',
    labeled_by      TEXT NOT NULL DEFAULT '',               -- siapa yang memberi label manual
    local_only      BOOLEAN NOT NULL DEFAULT false          -- baru terlihat di run lokal; tidak masuk daftar tim
);
CREATE INDEX failure_groups_test_idx ON failure_groups (project, file, title);
CREATE INDEX failure_groups_status_idx ON failure_groups (status, last_seen_run DESC);

-- Bentuk response API yang unik (nama field dan tipenya, tanpa isi data). Disimpan sekali.
CREATE TABLE response_shapes (
    id    TEXT PRIMARY KEY,                                 -- md5(shape::text)
    shape JSONB NOT NULL
);

-- Hasil tiap test di tiap run. Tabel terbesar; dibersihkan oleh retensi (RETENTION_DAYS).
CREATE TABLE test_results (
    id                BIGSERIAL PRIMARY KEY,
    run_id            BIGINT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    test_key          TEXT NOT NULL,                        -- "project › file › describe › nama test"
    status            TEXT NOT NULL CHECK (status IN ('passed', 'failed', 'flaky', 'skipped')),
    retries           INTEGER NOT NULL DEFAULT 0,
    duration_ms       INTEGER NOT NULL DEFAULT 0,
    error_message     TEXT NOT NULL DEFAULT '',             -- sudah disamarkan
    error_snippet     TEXT NOT NULL DEFAULT '',             -- potongan kode test, sudah disamarkan
    error_location    TEXT NOT NULL DEFAULT '',             -- file:baris:kolom
    group_id          TEXT NOT NULL DEFAULT '',             -- kelompok kegagalan; kosong kalau lulus
    cause_id          TEXT NOT NULL DEFAULT '',             -- penyebab; kosong kalau lulus
    test_code_hash    TEXT NOT NULL DEFAULT '',             -- untuk tahu apakah kode test berubah
    response_shape_id TEXT NOT NULL DEFAULT '',             -- bentuk response API (response_shapes.id)
    trace_path        TEXT NOT NULL DEFAULT '',             -- relatif terhadap project, misalnya test-results/.../trace.zip
    screenshots       TEXT[] NOT NULL DEFAULT '{}'          -- relatif terhadap project
);
CREATE INDEX test_results_run_idx ON test_results (run_id);
CREATE INDEX test_results_key_idx ON test_results (test_key, run_id DESC);
CREATE INDEX test_results_group_idx ON test_results (group_id) WHERE group_id <> '';
CREATE INDEX test_results_cause_idx ON test_results (cause_id, run_id DESC) WHERE cause_id <> '';
CREATE INDEX test_results_shape_idx ON test_results (response_shape_id) WHERE response_shape_id <> '';

-- Hasil analisis penyebab per kelompok. Di-cache per versi prompt: kelompok yang sama tidak
-- dianalisis ulang kecuali diminta (force) atau prompt-nya berubah.
CREATE TABLE analyses (
    group_id       TEXT NOT NULL REFERENCES failure_groups(id) ON DELETE CASCADE,
    prompt_version TEXT NOT NULL,
    source         TEXT NOT NULL CHECK (source IN ('rule', 'ai')),  -- aturan tetap atau AI
    model          TEXT NOT NULL DEFAULT '',
    category       TEXT NOT NULL CHECK (category IN ('backend_bug', 'test_bug', 'environment', 'flaky', 'unknown')),
    confidence     TEXT NOT NULL CHECK (confidence IN ('low', 'medium', 'high')),
    summary        TEXT NOT NULL,
    evidence       TEXT[] NOT NULL DEFAULT '{}',
    next_step      TEXT NOT NULL DEFAULT '',
    cost_usd       NUMERIC(12, 6) NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, prompt_version)
);
