import { Users, X } from "lucide-react";
import { useMemo } from "react";

import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import type { Failure } from "@/lib/failure";
import { cn } from "@/lib/utils";

export type AgeBucket = "fresh" | "week" | "stale";
export type AgeFilter = "all" | AgeBucket;
export const UNASSIGNED = "__unassigned";

const DAY_MS = 86_400_000;
const buckets: Array<{ id: AgeBucket; label: string; hint: string }> = [
  { id: "fresh", label: "0–1d", hint: "new" },
  { id: "week", label: "2–7d", hint: "this week" },
  { id: "stale", label: ">7d", hint: "needs attention" },
];

export function ageDays(iso: string): number {
  const started = new Date(iso).getTime();
  if (Number.isNaN(started)) return 0;
  return Math.max(0, Math.floor((Date.now() - started) / DAY_MS));
}

export function ageBucketOf(iso: string): AgeBucket {
  const days = ageDays(iso);
  if (days <= 1) return "fresh";
  if (days <= 7) return "week";
  return "stale";
}

export function ownerKey(owner: string): string {
  return owner.trim() || UNASSIGNED;
}

export function ownerLabel(owner: string): string {
  return owner.trim() || "Unassigned";
}

export function TriageHealth({
  failures,
  age,
  owner,
  onAge,
  onOwner,
  onClear,
}: {
  failures: Failure[];
  age: AgeFilter;
  owner: string;
  onAge: (bucket: AgeBucket) => void;
  onOwner: (owner: string) => void;
  onClear: () => void;
}) {
  const counts = useMemo(() => {
    const next: Record<AgeBucket, number> = { fresh: 0, week: 0, stale: 0 };
    for (const failure of failures) next[ageBucketOf(failure.firstSeenAt)] += 1;
    return next;
  }, [failures]);

  const owners = useMemo(() => {
    const groups = new Map<
      string,
      { key: string; label: string; count: number; oldest: number; needsAI: number }
    >();
    for (const failure of failures) {
      const key = ownerKey(failure.triageOwner);
      const current = groups.get(key) ?? {
        key,
        label: ownerLabel(failure.triageOwner),
        count: 0,
        oldest: 0,
        needsAI: 0,
      };
      current.count += 1;
      current.oldest = Math.max(current.oldest, ageDays(failure.firstSeenAt));
      if (failure.needsAI) current.needsAI += 1;
      groups.set(key, current);
    }
    return [...groups.values()].sort(
      (left, right) =>
        right.count - left.count ||
        right.oldest - left.oldest ||
        left.label.localeCompare(right.label),
    );
  }, [failures]);

  const active = age !== "all" || owner !== "all";
  const total = failures.length;

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <Users className="h-3.5 w-3.5 text-brand" />
        <span className="text-[12.5px] font-semibold">Triage health</span>
        <Badge tone="neutral">{total} active</Badge>
        <span className="hidden text-[10.5px] text-faint md:inline">
          age and ownership · excludes muted and won’t fix
        </span>
        {active && (
          <button
            type="button"
            onClick={onClear}
            className="ml-auto inline-flex items-center gap-1 rounded-full border border-line bg-canvas px-2 py-0.5 text-[10px] text-muted hover:text-ink"
          >
            Clear filters <X className="h-3 w-3" />
          </button>
        )}
      </div>

      <div className="grid gap-4 p-3.5 lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
        <section>
          <div className="mb-1.5 flex items-center justify-between text-[10px] uppercase tracking-[0.7px] text-faint">
            <span>Aging</span>
            <span className="normal-case tracking-normal">first seen</span>
          </div>
          <div className="grid grid-cols-3 gap-2">
            {buckets.map((bucket) => {
              const count = counts[bucket.id];
              const pct = total === 0 ? 0 : Math.round((count / total) * 100);
              return (
                <button
                  key={bucket.id}
                  type="button"
                  onClick={() => onAge(bucket.id)}
                  aria-pressed={age === bucket.id}
                  className={cn(
                    "rounded-md border p-2.5 text-left transition-colors",
                    age === bucket.id
                      ? "border-brand/40 bg-brand/10"
                      : "border-line-soft bg-canvas hover:border-line hover:bg-[#12171e]",
                  )}
                >
                  <div className="flex items-baseline justify-between gap-1">
                    <span className="text-[20px] font-semibold leading-none text-ink">{count}</span>
                    <span className="font-mono text-[9.5px] text-faint">{bucket.label}</span>
                  </div>
                  <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-[#21262d]">
                    <div
                      className={cn(
                        "h-1 rounded-full",
                        bucket.id === "stale" ? "bg-danger" : bucket.id === "week" ? "bg-warn" : "bg-brand",
                      )}
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                  <div className="mt-1 truncate text-[9.5px] text-faint">{bucket.hint}</div>
                </button>
              );
            })}
          </div>
        </section>

        <section className="min-w-0">
          <div className="mb-1.5 flex items-center justify-between text-[10px] uppercase tracking-[0.7px] text-faint">
            <span>Owners</span>
            <span className="normal-case tracking-normal">count · oldest</span>
          </div>
          {owners.length === 0 ? (
            <p className="rounded-md border border-line-soft bg-canvas px-2.5 py-2 text-[10.5px] text-muted">
              No active failures.
            </p>
          ) : (
            <div className="flex max-h-[132px] flex-col gap-0.5 overflow-auto pr-1">
              {owners.map((item) => (
                <button
                  key={item.key}
                  type="button"
                  onClick={() => onOwner(item.key)}
                  aria-pressed={owner === item.key}
                  className={cn(
                    "flex items-center gap-2 rounded border px-2 py-1.5 text-left transition-colors",
                    owner === item.key
                      ? "border-brand/40 bg-brand/10"
                      : "border-transparent hover:border-line-soft hover:bg-[#12171e]",
                  )}
                >
                  <span
                    className={cn(
                      "h-1.5 w-1.5 shrink-0 rounded-full",
                      item.key === UNASSIGNED ? "bg-warn" : "bg-brand",
                    )}
                  />
                  <span className="min-w-0 flex-1 truncate text-[11px] text-ink">{item.label}</span>
                  {item.needsAI > 0 && (
                    <Badge tone="env" className="px-1.5 py-0 text-[9px]">
                      AI {item.needsAI}
                    </Badge>
                  )}
                  <span className="font-mono text-[10.5px] text-muted">{item.count}</span>
                  <span className="w-9 text-right font-mono text-[9.5px] text-faint">
                    {item.oldest === 0 ? "today" : `${item.oldest}d`}
                  </span>
                </button>
              ))}
            </div>
          )}
        </section>
      </div>
    </Card>
  );
}
