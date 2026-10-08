-- Satu baris per eksekusi `npx playwright test` yang diupload.
CREATE TABLE runs (
    id                 BIGSERIAL PRIMARY KEY,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at         TIMESTAMPTZ,
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    source             TEXT NOT NULL DEFAULT 'local',   -- local / ci
    branch             TEXT NOT NULL DEFAULT '',
    commit_sha         TEXT NOT NULL DEFAULT '',
    app_version        TEXT NOT NULL DEFAULT '',        -- versi backend yang dites (opsional)
    playwright_version TEXT NOT NULL DEFAULT '',
    total              INTEGER NOT NULL DEFAULT 0,
    passed             INTEGER NOT NULL DEFAULT 0,
    failed             INTEGER NOT NULL DEFAULT 0,
    flaky              INTEGER NOT NULL DEFAULT 0,
    skipped            INTEGER NOT NULL DEFAULT 0,
    report_errors      TEXT[] NOT NULL DEFAULT '{}'      -- error di luar test, sudah diredaksi
);

-- Kegagalan yang dikelompokkan berdasarkan fingerprint (test + error ternormalisasi).
-- Status: open -> resolved (test lulus lagi) -> regressed (gagal lagi dengan error yang sama).
CREATE TABLE failure_groups (
    fingerprint      TEXT PRIMARY KEY,
    test_key         TEXT NOT NULL,
    project          TEXT NOT NULL,
    file             TEXT NOT NULL,
    title            TEXT NOT NULL,
    normalized_error TEXT NOT NULL,
    sample_error     TEXT NOT NULL,                     -- contoh pesan asli (sudah diredaksi)
    status           TEXT NOT NULL CHECK (status IN ('open', 'resolved', 'regressed')),
    occurrences      INTEGER NOT NULL DEFAULT 1,
    regressions      INTEGER NOT NULL DEFAULT 0,
    first_seen_run   BIGINT NOT NULL REFERENCES runs(id),
    last_seen_run    BIGINT NOT NULL REFERENCES runs(id),
    resolved_run     BIGINT REFERENCES runs(id),
    first_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at      TIMESTAMPTZ
);
CREATE INDEX failure_groups_status_idx ON failure_groups (status, last_seen_at DESC);
CREATE INDEX failure_groups_test_idx ON failure_groups (test_key);

-- Hasil tiap test di tiap run.
CREATE TABLE test_results (
    id             BIGSERIAL PRIMARY KEY,
    run_id         BIGINT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    test_key       TEXT NOT NULL,
    project        TEXT NOT NULL,
    file           TEXT NOT NULL,
    title          TEXT NOT NULL,
    line           INTEGER NOT NULL DEFAULT 0,
    status         TEXT NOT NULL CHECK (status IN ('passed', 'failed', 'flaky', 'skipped')),
    retries        INTEGER NOT NULL DEFAULT 0,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    error_message  TEXT NOT NULL DEFAULT '',            -- sudah diredaksi
    error_snippet  TEXT NOT NULL DEFAULT '',            -- potongan kode test, sudah diredaksi
    error_location TEXT NOT NULL DEFAULT '',
    fingerprint    TEXT NOT NULL DEFAULT '',            -- kosong kalau test lulus
    trace_path     TEXT NOT NULL DEFAULT '',            -- path di mesin yang menjalankan test
    screenshots    TEXT[] NOT NULL DEFAULT '{}'
);
CREATE INDEX test_results_run_idx ON test_results (run_id);
CREATE INDEX test_results_key_idx ON test_results (test_key, run_id DESC);
CREATE INDEX test_results_fp_idx ON test_results (fingerprint) WHERE fingerprint <> '';
