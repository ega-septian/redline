export type ApiProject = {
  project: string;
  tests: number;
  runs: number;
  open_failures: number;
  last_run_at?: string | null;
};

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
  cause_id?: string;
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
  triage_status: string;
  triage_owner?: string;
  triage_note?: string;
  triage_updated_by?: string;
  triage_updated_at?: string | null;
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

export type AnalyzeResult = {
  analysis: ApiAnalysis;
  cached: boolean;
  needs_ai?: boolean;
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
  artifacts?: ApiArtifact[];
};

export type ApiArtifact = {
  id: number;
  run_id: number;
  test: string;
  kind: "trace" | "screenshot" | string;
  name: string;
  content_type?: string;
  size: number;
  created_at: string;
};

export type RunDetail = {
  run: ApiRun;
  tests: ApiRunResult[];
};

export type RunChange = "new" | "fixed" | "still_failing" | "changed_cause" | "flaky" | "removed";

export type ComparedTest = {
  test: string;
  test_case_id?: string;
  status: string;
  previous_status?: string;
  fingerprint?: string;
  previous_fingerprint?: string;
  change: RunChange;
};

export type RunComparison = {
  current: ApiRun;
  previous: ApiRun | null;
  summary: Record<string, number>;
  tests: ComparedTest[];
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

export type ApiRuleCandidate = {
  id: number;
  fingerprint: string;
  test: string;
  pattern: string;
  category: string;
  summary: string;
  next_step: string;
  status: string;
  source: string;
  model?: string;
  prompt_version?: string;
  confidence?: string;
  support: number;
  also_matches: number;
  backtest_reason?: string;
  promoted_rule_id?: number | null;
  created_at: string;
  proven_at?: string | null;
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

export type TestHealthSummary = {
  days: number;
  project?: string;
  runs: number;
  results: number;
  passed: number;
  failed: number;
  flaky: number;
  skipped: number;
  pass_rate: number;
  flake_rate: number;
  open_failures: number;
  new_failures: number;
  resolved_failures: number;
  mttr_hours: number;
};

export type TestHealthRow = {
  key: string;
  test: string;
  test_case_id?: string;
  project?: string;
  total: number;
  passed: number;
  failed: number;
  flaky: number;
  skipped: number;
  pass_rate: number;
  flake_rate: number;
  flips: number;
  avg_duration_ms: number;
  last_status: string;
  last_run_at?: string | null;
  last_failure_at?: string | null;
  triage_owner?: string;
  triage_status?: string;
  quarantined: boolean;
  quarantine_owner?: string;
  quarantine_reason?: string;
  quarantine_until?: string | null;
};

export type ApiQuarantineSuggestion = {
  key: string;
  test: string;
  test_case_id?: string;
  project?: string;
  score: "high" | "medium";
  reason: string;
  owner?: string;
  suggested_until: string;
  days: number;
  executed: number;
  flaky: number;
  flake_rate: number;
  flips: number;
  last_failure_at?: string | null;
};

export type ApiQuarantine = {
  key: string;
  test?: string;
  test_case_id?: string;
  project?: string;
  owner: string;
  reason: string;
  by: string;
  created_at: string;
  until: string;
  released_at?: string | null;
  released_by?: string;
};

export type TestRunPoint = {
  run_id: number;
  at: string;
  branch?: string;
  commit?: string;
  status: string;
  duration_ms: number;
  retries: number;
  error?: string;
  fingerprint?: string;
};

export type TestHealth = {
  summary: TestHealthSummary;
  tests: TestHealthRow[];
};

export type TestHistory = {
  summary: TestHealthRow;
  runs: TestRunPoint[];
};

export type ApiCoverageEndpoint = {
  method: string;
  path: string;
  summary?: string;
  status_codes: string[];
  observed_status_codes: string[];
  covered: boolean;
  tests: number;
  last_tested_at?: string | null;
};

export type ApiCoverageChange = {
  kind: "added" | "changed" | "removed" | string;
  method: string;
  path: string;
  detail: string;
};

export type ApiCoverageSnapshot = {
  run_id: number;
  at: string;
  contract_id: string;
  source_url?: string;
};

export type ApiCoveragePoint = {
  run_id: number;
  at: string;
  documented: number;
  covered: number;
  status_codes_documented: number;
  status_codes_seen: number;
  changes: number;
};

export type ApiCoverage = {
  available: boolean;
  project: string;
  message?: string;
  snapshot?: ApiCoverageSnapshot;
  previous?: ApiCoverageSnapshot;
  summary: {
    documented: number;
    covered: number;
    uncovered: number;
    status_codes_documented: number;
    status_codes_seen: number;
    changes: number;
  };
  endpoints: ApiCoverageEndpoint[];
  changes: ApiCoverageChange[];
  history?: ApiCoveragePoint[];
};

export type ApiContractSource = {
  project: string;
  url: string;
  enabled: boolean;
  by?: string;
  updated_at?: string | null;
  last_checked_at?: string | null;
  last_status?: "valid" | "invalid" | "unknown" | string;
  last_error?: string;
  last_version?: string;
  last_endpoints?: number;
  last_duration_ms?: number;
};

export type ApiSourceCheck = {
  ok: boolean;
  url: string;
  kind?: string;
  status_code?: number;
  content_type?: string;
  title?: string;
  openapi?: string;
  endpoints: number;
  error?: string;
  hint?: string;
  candidates?: string[];
  checked_at: string;
  duration_ms: number;
};

export type ApiNotificationDelivery = {
  at: string;
  event: string;
  run_id?: number;
  ok: boolean;
  status_code?: number;
  attempts: number;
  error?: string;
};

export type ApiNotifications = {
  enabled: boolean;
  provider?: string;
  webhook_url: string;
  webhook_set: boolean;
  events: string[];
  available_events: string[];
  by?: string;
  updated_at?: string | null;
  delivery: boolean;
  last_delivery?: ApiNotificationDelivery | null;
};

export type ApiConfig = {
  contracts: ApiContractSource[];
  notifications: ApiNotifications;
};
