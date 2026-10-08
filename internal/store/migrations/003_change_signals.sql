-- Sinyal perubahan tanpa git: sidik jari kode test dan bentuk response API (tanpa isi data).
ALTER TABLE test_results
    ADD COLUMN source_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN http_calls  JSONB NOT NULL DEFAULT '[]';
