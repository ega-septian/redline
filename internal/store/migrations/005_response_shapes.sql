-- Bentuk response hampir selalu sama dari run ke run. Simpan sekali per isi,
-- test_results cukup menyimpan hash-nya.
CREATE TABLE response_shapes (
    hash       TEXT PRIMARY KEY,                -- md5(calls::text)
    calls      JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE test_results ADD COLUMN calls_hash TEXT NOT NULL DEFAULT '';

-- Pindahkan data lama.
INSERT INTO response_shapes (hash, calls)
SELECT DISTINCT ON (md5(http_calls::text)) md5(http_calls::text), http_calls
FROM test_results WHERE http_calls <> '[]'::jsonb
ON CONFLICT (hash) DO NOTHING;
UPDATE test_results SET calls_hash = md5(http_calls::text) WHERE http_calls <> '[]'::jsonb;

ALTER TABLE test_results DROP COLUMN http_calls;
CREATE INDEX test_results_calls_hash_idx ON test_results (calls_hash) WHERE calls_hash <> '';

-- Untuk retensi: cari hasil lama per waktu run.
CREATE INDEX runs_created_at_idx ON runs (created_at);
