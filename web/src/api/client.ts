import type { ApiGroup, GroupDetail, Knowledge } from "./types";

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

export const api = {
  groups: () => request<ApiGroup[]>("/api/groups"),
  group: (fingerprint: string) =>
    request<GroupDetail>(`/api/groups/${encodeURIComponent(fingerprint)}`),
  knowledge: (fingerprint: string) =>
    request<Knowledge>(`/api/groups/${encodeURIComponent(fingerprint)}/knowledge`),
};
