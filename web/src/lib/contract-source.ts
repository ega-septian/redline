import type { ApiContractSource } from "@/api/types";

const STALE_MS = 7 * 24 * 60 * 60 * 1000;

export function isStale(source: ApiContractSource): boolean {
  if (!source.last_checked_at) return false;
  const at = new Date(source.last_checked_at).getTime();
  return Number.isFinite(at) && Date.now() - at > STALE_MS;
}

export type ContractSourceHealth = {
  valid: number;
  invalid: number;
  stale: number;
  checked: number;
  latest?: string;
};

export function contractSourceHealth(sources: ApiContractSource[]): ContractSourceHealth {
  const health: ContractSourceHealth = { valid: 0, invalid: 0, stale: 0, checked: 0 };
  for (const source of sources) {
    if (source.last_status === "valid") health.valid += 1;
    if (source.last_status === "invalid") health.invalid += 1;
    if (!source.last_checked_at) continue;
    const at = new Date(source.last_checked_at).getTime();
    if (!Number.isFinite(at)) continue;
    health.checked += 1;
    if (isStale(source)) health.stale += 1;
    if (!health.latest || at > new Date(health.latest).getTime()) {
      health.latest = source.last_checked_at;
    }
  }
  return health;
}
