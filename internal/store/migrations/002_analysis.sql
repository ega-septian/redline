-- Label manual dari manusia: kebenaran akhir, menimpa hasil aturan maupun AI.
ALTER TABLE failure_groups
    ADD COLUMN human_label TEXT CHECK (human_label IN ('backend_bug', 'test_bug', 'environment', 'flaky', 'unknown')),
    ADD COLUMN human_note  TEXT NOT NULL DEFAULT '',
    ADD COLUMN labeled_at  TIMESTAMPTZ;

-- Hasil analisis per kelompok. Di-cache per (fingerprint, prompt_version):
-- kegagalan yang sama tidak dianalisis ulang kecuali diminta (force) atau prompt-nya berubah.
CREATE TABLE analyses (
    fingerprint    TEXT NOT NULL REFERENCES failure_groups(fingerprint) ON DELETE CASCADE,
    prompt_version TEXT NOT NULL,
    source         TEXT NOT NULL CHECK (source IN ('rule', 'ai')),
    model          TEXT NOT NULL DEFAULT '',
    category       TEXT NOT NULL CHECK (category IN ('backend_bug', 'test_bug', 'environment', 'flaky', 'unknown')),
    confidence     TEXT NOT NULL CHECK (confidence IN ('low', 'medium', 'high')),
    summary        TEXT NOT NULL,
    evidence       TEXT[] NOT NULL DEFAULT '{}',
    next_step      TEXT NOT NULL DEFAULT '',
    input_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens  INTEGER NOT NULL DEFAULT 0,
    cost_usd       NUMERIC(12, 6) NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (fingerprint, prompt_version)
);
