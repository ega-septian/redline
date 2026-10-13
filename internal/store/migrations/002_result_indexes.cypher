// Index untuk mencari hasil test per kelompok kegagalan (riwayat kemunculan) dan per test (pembanding).
CREATE INDEX result_group_id IF NOT EXISTS FOR (n:Result) ON (n.groupId);
CREATE INDEX result_test_key IF NOT EXISTS FOR (n:Result) ON (n.testKey);
