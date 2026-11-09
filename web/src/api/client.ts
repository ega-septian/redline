import type {
  AnalyzeResult,
  ApiGroup,
  ApiLoadError,
  ApiProject,
  ApiQuarantine,
  ApiQuarantineSuggestion,
  ApiNotificationChannel,
  ApiNotificationDelivery,
  ApiSourceCheckEvent,
  ApiCoverage,
  ApiConfig,
  ApiContractSource,
  ApiNotifications,
  ApiSourceCheck,
  ApiRule,
  ApiRun,
  ApiRuleCandidate,
  GroupDetail,
  ImpactResult,
  Knowledge,
  RunComparison,
  RunDetail,
  Scoreboard,
  TestHealth,
  TestHealthPoint,
  TestHistory,
} from "./types";

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function requestMarkdown(path: string): Promise<string> {
  const response = await fetch(path, { credentials: "same-origin" });
  if (response.status === 401) {
    const next = encodeURIComponent(window.location.pathname + window.location.search);
    window.location.assign(`/ui/login?next=${next}`);
    throw new ApiError(401, "unauthorized");
  }
  if (!response.ok) {
    let message = `${path} returned ${response.status}`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // Non-JSON errors keep the generic message.
    }
    throw new ApiError(response.status, message);
  }
  return response.text();
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: "same-origin", ...init });
  if (response.status === 401) {
    const next = encodeURIComponent(window.location.pathname + window.location.search);
    window.location.assign(`/ui/login?next=${next}`);
    throw new ApiError(401, "unauthorized");
  }
  if (!response.ok) {
    let message = `${path} returned ${response.status}`;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // Non-JSON errors keep the generic message.
    }
    throw new ApiError(response.status, message);
  }
  return (await response.json()) as T;
}

export type LabelInput = { label: string; note: string };
export type LabelResult = { label: string; note: string; by: string };

export type TriageInput = { status: string; owner: string; note: string };
export type TriageResult = TriageInput & { by: string };
export type TriageBulkResult = { updated: number };

const CSRF_COOKIE = "redline_csrf";

function csrfToken(): string {
  const entry = document.cookie.split("; ").find((row) => row.startsWith(`${CSRF_COOKIE}=`));
  return entry ? decodeURIComponent(entry.slice(CSRF_COOKIE.length + 1)) : "";
}

export const api = {
  projects: () => request<ApiProject[]>("/api/projects"),
  groups: (project?: string) =>
    request<ApiGroup[]>(
      `/api/groups${project ? `?project=${encodeURIComponent(project)}` : ""}`,
    ),
  group: (fingerprint: string) =>
    request<GroupDetail>(`/api/groups/${encodeURIComponent(fingerprint)}`),
  knowledge: (fingerprint: string) =>
    request<Knowledge>(`/api/groups/${encodeURIComponent(fingerprint)}/knowledge`),
  knowledgeMarkdown: (fingerprint: string) =>
    requestMarkdown(`/api/groups/${encodeURIComponent(fingerprint)}/knowledge.md`),
  runs: (project?: string) =>
    request<ApiRun[]>(
      `/api/runs?limit=50${project ? `&project=${encodeURIComponent(project)}` : ""}`,
    ),
  runErrors: (limit = 20) => request<ApiRun[]>(`/api/runs/errors?limit=${limit}`),
  loadErrors: (limit = 50) => request<ApiLoadError[]>(`/api/load-errors?limit=${limit}`),
  analyzeLoadError: (id: string) =>
    request<{ category: string; confidence: string; source: string; summary: string; next_step: string }>(
      `/api/load-errors/${encodeURIComponent(id)}/analyze`,
      { method: "POST", headers: { "X-CSRF-Token": csrfToken() } },
    ),
  triageLoadError: (
    id: string,
    input: { status: string; owner: string; note: string },
  ) =>
    request<{ status: string; owner: string; note: string; by: string }>(
      `/api/load-errors/${encodeURIComponent(id)}/triage`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
        body: JSON.stringify({ ...input, by: "ui" }),
      },
    ),
  run: (id: number) => request<RunDetail>(`/api/runs/${id}`),
  compareRun: (id: number) => request<RunComparison>(`/api/runs/${id}/compare`),
  impact: (kind: "file" | "endpoint", query: string) =>
    request<ImpactResult>(`/api/impact?${kind}=${encodeURIComponent(query)}`),
  rules: () => request<ApiRule[]>("/api/rules"),
  ruleCandidates: (status?: string) =>
    request<ApiRuleCandidate[]>(
      `/api/rules/candidates${status ? `?status=${encodeURIComponent(status)}` : ""}`,
    ),
  scoreboard: () => request<Scoreboard>("/api/scoreboard?limit=50"),
  decideRule: (id: number, status: "active" | "rejected") =>
    request<ApiRule>(`/api/rules/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ status, by: "ui" }),
    }),
  label: (fingerprint: string, input: LabelInput) =>
    request<LabelResult>(`/api/groups/${encodeURIComponent(fingerprint)}/label`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ ...input, by: "ui" }),
    }),
  analyze: (fingerprint: string, force = false) =>
    request<AnalyzeResult>(
      `/api/groups/${encodeURIComponent(fingerprint)}/analyze${force ? "?force=1" : ""}`,
      {
        method: "POST",
        headers: { "X-CSRF-Token": csrfToken() },
      },
    ),
  triage: (fingerprint: string, input: TriageInput) =>
    request<TriageResult>(`/api/groups/${encodeURIComponent(fingerprint)}/triage`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ ...input, by: "ui" }),
    }),
  triageBulk: (fingerprints: string[], status: string) =>
    request<TriageBulkResult>("/api/groups/triage", {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ fingerprints, status, by: "ui" }),
    }),
  triageOwnerBulk: (fingerprints: string[], owner: string) =>
    request<TriageBulkResult>("/api/groups/triage/owner", {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ fingerprints, owner, by: "ui" }),
    }),
  testHealth: (days: number, project: string) =>
    request<TestHealth>(
      `/api/tests?days=${days}${project ? `&project=${encodeURIComponent(project)}` : ""}`,
    ),
  testTrend: (days: number, project: string, limit = 20) =>
    request<TestHealthPoint[]>(
      `/api/tests/trend?days=${days}&limit=${limit}${project ? `&project=${encodeURIComponent(project)}` : ""}`,
    ),
  testHistory: (test: string, days: number) =>
    request<TestHistory>(`/api/tests/history?test=${encodeURIComponent(test)}&days=${days}`),
  quarantines: (project = "") =>
    request<ApiQuarantine[]>(
      `/api/tests/quarantine${project ? `?project=${encodeURIComponent(project)}` : ""}`,
    ),
  quarantine: (input: { test: string; owner: string; reason: string; days: number }) =>
    request<ApiQuarantine>("/api/tests/quarantine", {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify(input),
    }),
  releaseQuarantine: (test: string) =>
    request<ApiQuarantine>(`/api/tests/quarantine?test=${encodeURIComponent(test)}`, {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrfToken() },
    }),
  quarantineSuggestions: (days: number, project: string) =>
    request<ApiQuarantineSuggestion[]>(
      `/api/tests/quarantine/suggestions?days=${days}${project ? `&project=${encodeURIComponent(project)}` : ""}`,
    ),
  dismissQuarantineSuggestion: (test: string, days = 7) =>
    request<{ dismissed: string; until: string }>("/api/tests/quarantine/suggestions/dismiss", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ test, days }),
    }),
  coverage: (project: string) =>
    request<ApiCoverage>(`/api/coverage?project=${encodeURIComponent(project)}`),
  config: () => request<ApiConfig>("/api/config"),
  sourceHistory: (project: string, limit = 20) =>
    request<ApiSourceCheckEvent[]>(
      `/api/config/contracts/history?project=${encodeURIComponent(project)}&limit=${limit}`,
    ),
  validateSource: (url: string) =>
    request<ApiSourceCheck>("/api/config/contracts/validate", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ url }),
    }),
  saveSource: (project: string, url: string) =>
    request<ApiContractSource>("/api/config/contracts", {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ project, url }),
    }),
  deleteSource: (project: string) =>
    request<{ deleted: string }>(`/api/config/contracts?project=${encodeURIComponent(project)}`, {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrfToken() },
    }),
  checkSource: (project: string) =>
    request<ApiSourceCheck>("/api/config/contracts/check", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ project }),
    }),
  saveNotifications: (input: {
    enabled: boolean;
    webhook_url: string;
    events: string[];
  }) =>
    request<ApiNotifications>("/api/config/notifications", {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify(input),
    }),
  saveNotificationChannel: (
    project: string,
    input: { enabled: boolean; webhook_url: string; events: string[] },
  ) =>
    request<ApiNotificationChannel>(
      `/api/config/notifications/projects/${encodeURIComponent(project)}`,
      {
        method: "PUT",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
        body: JSON.stringify(input),
      },
    ),
  deleteNotificationChannel: (project: string) =>
    request<{ deleted: string }>(
      `/api/config/notifications/projects/${encodeURIComponent(project)}`,
      { method: "DELETE", headers: { "X-CSRF-Token": csrfToken() } },
    ),
  testNotification: (project = "") =>
    request<{ sent: boolean; mock: boolean; message: string }>(
      `/api/config/notifications/test${project ? `?project=${encodeURIComponent(project)}` : ""}`,
      { method: "POST", headers: { "X-CSRF-Token": csrfToken() } },
    ),
  notificationDeliveries: (limit = 20) =>
    request<ApiNotificationDelivery[]>(`/api/config/notifications/deliveries?limit=${limit}`),
};
