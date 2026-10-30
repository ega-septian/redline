export type ApiGroup = {
  fingerprint: string;
  test: string;
  test_case_id?: string;
  project: string;
  file: string;
  title: string;
  status: string;
  summary: string;
  sample_error?: string;
  cause?: string;
  occurrences: number;
  regressions: number;
  first_seen_run: number;
  last_seen_run: number;
  first_seen_at: string;
  last_seen_at: string;
  resolved_at?: string | null;
  human_label?: string;
  human_note?: string;
  labeled_by?: string;
  local_only: boolean;
  category?: string;
  confidence?: string;
  analysis_source?: string;
  analysis_model?: string;
  analysis_summary?: string;
  analyzed_at?: string;
};

export type ApiAnalysis = {
  fingerprint: string;
  source: string;
  model?: string;
  category: string;
  confidence: string;
  summary: string;
  evidence: string[];
  next_step: string;
  cost_usd: number;
  created_at: string;
};

export type ApiOccurrence = {
  run_id: number;
  run_created_at: string;
  source: string;
  branch: string;
  commit: string;
  app_version: string;
  status: string;
  retries: number;
  error_location: string;
  error_snippet: string;
  trace_path: string;
  screenshots: string[];
};

export type GroupDetail = {
  group: ApiGroup;
  occurrences: ApiOccurrence[];
  analysis: ApiAnalysis | null;
  experiments: unknown[];
};

export type TestSplit = { passed: string[]; failed: string[] };

export type Knowledge = {
  run_id: number;
  pass_run_id: number;
  compared: boolean;
  files: Array<{
    path: string;
    direct: boolean;
    changed: boolean;
    others: TestSplit;
  }>;
  endpoints: Array<{
    key: string;
    seen_passing: boolean;
    with_changed: TestSplit;
    without: TestSplit;
  }>;
};

export type ApiRun = {
  id: number;
  created_at: string;
  started_at: string;
  duration_ms: number;
  source: string;
  triggered_by: string;
  ci_url: string;
  branch: string;
  commit: string;
  app_version: string;
  playwright_version: string;
  total: number;
  passed: number;
  failed: number;
  flaky: number;
  skipped: number;
  report_errors: string[];
};

export type ApiRunResult = {
  test: string;
  test_case_id?: string;
  status: string;
  duration_ms: number;
  retries: number;
  error?: string;
  error_snippet?: string;
  error_location?: string;
  trace_path?: string;
  screenshots: string[];
  fingerprint?: string;
  category?: string;
  confidence?: string;
  human_label?: string;
};

export type RunDetail = {
  run: ApiRun;
  tests: ApiRunResult[];
};

export type ApiImpactedTest = {
  test: string;
  test_case_id?: string;
  run_id: number;
};

export type ImpactResult = {
  file: string;
  endpoint: string;
  tests: ApiImpactedTest[];
};

export type ApiRule = {
  id: number;
  pattern: string;
  category: string;
  summary: string;
  next_step: string;
  status: string;
  learned_from: string[];
  hits: number;
  also_matches: number;
  cost_usd: number;
  created_at: string;
  decided_by?: string;
  decided_at?: string | null;
};

export type Score = {
  source: string;
  graded: number;
  correct: number;
  unknown: number;
  accuracy: number;
};

export type ScoreEntry = {
  group_id: string;
  test: string;
  source: string;
  predicted: string;
  confidence: string;
  truth: string;
  truth_by: string;
  correct: boolean;
  proven_at: string | null;
};

export type Scoreboard = {
  sources: Score[];
  entries: ScoreEntry[];
};
