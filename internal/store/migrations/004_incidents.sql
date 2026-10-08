-- Insiden: kegagalan banyak test dengan penyebab yang sama (server mati, endpoint 5xx yang sama).
-- Status tetap dilacak per kelompok; insiden hanya pengelompokan di atasnya.
ALTER TABLE failure_groups
    ADD COLUMN incident_key   TEXT NOT NULL DEFAULT '',
    ADD COLUMN incident_label TEXT NOT NULL DEFAULT '';

ALTER TABLE test_results
    ADD COLUMN incident_key TEXT NOT NULL DEFAULT '';
CREATE INDEX test_results_incident_idx ON test_results (incident_key, run_id DESC) WHERE incident_key <> '';
