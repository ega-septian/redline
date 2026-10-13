-- ID test case dari tag @TC-... (misalnya TC-BRD-001), supaya mudah dicari dan nanti menjadi penghubung
-- ke ReportPortal (testCaseId). Hanya label: identitas test tetap test_key / (project, file, title), karena tag
-- bisa belum ada, dobel, atau diganti. Kosong untuk test tanpa tag dan untuk data sebelum migrasi ini.
ALTER TABLE test_results ADD COLUMN test_case_id TEXT NOT NULL DEFAULT '';
ALTER TABLE failure_groups ADD COLUMN test_case_id TEXT NOT NULL DEFAULT '';
CREATE INDEX test_results_case_idx ON test_results (test_case_id) WHERE test_case_id <> '';
CREATE INDEX failure_groups_case_idx ON failure_groups (test_case_id) WHERE test_case_id <> '';
