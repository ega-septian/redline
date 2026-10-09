-- Potret kontrak API (OpenAPI) per run. Kontrak berubah antar versi aplikasi (misalnya sprint1 -> sprint2),
-- jadi analisis harus memakai kontrak SAAT test gagal, bukan kontrak terbaru. Dengan potret per run,
-- Redline juga bisa membedakan perubahan yang disengaja (kontrak ikut berubah) dari regresi (kontrak tetap).
CREATE TABLE contracts (
    id          TEXT PRIMARY KEY,                           -- md5 isi dokumen
    project     TEXT NOT NULL,
    source_url  TEXT NOT NULL,
    document    TEXT NOT NULL,                              -- OpenAPI 3 (JSON) apa adanya
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now()          -- pertama kali versi ini terlihat
);

-- Kontrak yang berlaku untuk setiap project di setiap run.
CREATE TABLE run_contracts (
    run_id      BIGINT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    project     TEXT NOT NULL,
    contract_id TEXT NOT NULL REFERENCES contracts(id),
    PRIMARY KEY (run_id, project)
);
CREATE INDEX run_contracts_contract_idx ON run_contracts (contract_id);
