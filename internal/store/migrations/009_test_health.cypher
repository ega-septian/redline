// Test health aggregates results by test case across runs.
CREATE INDEX result_case_id IF NOT EXISTS FOR (n:Result) ON (n.caseId);
