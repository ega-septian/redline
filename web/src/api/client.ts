import type {
  ApiGroup,
  ApiRule,
  ApiRun,
  GroupDetail,
  ImpactResult,
  Knowledge,
  Scoreboard,
} from "./types";

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { credentials: "same-origin", ...init });
  if (response.status === 401) {
    const next = encodeURIComponent(window.location.pathname + window.location.search);
    window.location.assign(`/ui/login?next=${next}`);
    throw new ApiError(401, "unauthorized");
  }
  if (!response.ok) {
    throw new ApiError(response.status, `${path} returned ${response.status}`);
  }
  return (await response.json()) as T;
}

export type LabelInput = { label: string; note: string };
export type LabelResult = { label: string; note: string; by: string };

const CSRF_COOKIE = "redline_csrf";

function csrfToken(): string {
  const entry = document.cookie.split("; ").find((row) => row.startsWith(`${CSRF_COOKIE}=`));
  return entry ? decodeURIComponent(entry.slice(CSRF_COOKIE.length + 1)) : "";
}

export const api = {
  groups: () => request<ApiGroup[]>("/api/groups"),
  group: (fingerprint: string) =>
    request<GroupDetail>(`/api/groups/${encodeURIComponent(fingerprint)}`),
  knowledge: (fingerprint: string) =>
    request<Knowledge>(`/api/groups/${encodeURIComponent(fingerprint)}/knowledge`),
  runs: () => request<ApiRun[]>("/api/runs?limit=50"),
  impact: (kind: "file" | "endpoint", query: string) =>
    request<ImpactResult>(`/api/impact?${kind}=${encodeURIComponent(query)}`),
  rules: () => request<ApiRule[]>("/api/rules"),
  scoreboard: () => request<Scoreboard>("/api/scoreboard?limit=50"),
  label: (fingerprint: string, input: LabelInput) =>
    request<LabelResult>(`/api/groups/${encodeURIComponent(fingerprint)}/label`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken() },
      body: JSON.stringify({ ...input, by: "ui" }),
    }),
};
