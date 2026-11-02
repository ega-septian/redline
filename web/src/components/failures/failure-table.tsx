import { Link } from "@tanstack/react-router";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import {
  categoryLabel,
  sourceLabel,
  statusLabel,
  triageLabel,
  type Failure,
} from "@/lib/failure";
import { cn } from "@/lib/utils";

const categoryTone = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
} as const;

const sourceTone = {
  rule: "brand",
  ai: "test",
  experiment: "flaky",
  human: "flaky",
  none: "env",
} as const;

const statusStyle = {
  new: "text-[#ff7b72]",
  regressed: "text-warn",
  recurring: "text-[#79c0ff]",
} as const;

const categoryDot = {
  backend_bug: "bg-danger",
  test_bug: "bg-violet",
  environment: "bg-warn",
  flaky: "bg-ok",
  unclear: "bg-faint",
} as const;

const triageTone = {
  open: "neutral",
  acknowledged: "brand",
  in_progress: "env",
  muted: "neutral",
  wont_fix: "backend",
} as const;

function causeLabel(failure: Failure): string {
  const cause = failure.cause.trim();
  if (cause.includes(" → ") || /^[A-Z]+ \//.test(cause)) return cause;
  return failure.title || cause || "Unknown cause";
}

type Cause = {
  key: string;
  label: string;
  items: Failure[];
};

const headerColumns =
  "grid-cols-[28px_104px_84px_minmax(0,1fr)_104px_84px_120px]";
const rowColumns =
  "lg:grid-cols-[28px_104px_84px_minmax(0,1fr)_104px_84px_120px]";

export function FailureTable({
  items,
  selected,
  onSelect,
  selection,
  onToggle,
  onToggleMany,
  grouped,
}: {
  items: Failure[];
  selected: string;
  onSelect: (fingerprint: string) => void;
  selection: Set<string>;
  onToggle: (fingerprint: string) => void;
  onToggleMany: (fingerprints: string[]) => void;
  grouped: boolean;
}) {
  const [open, setOpen] = useState<Set<string>>(new Set());

  const causes = useMemo<Cause[]>(() => {
    if (!grouped) return [{ key: "", label: "", items }];
    const byCause = new Map<string, Cause>();
    for (const failure of items) {
      const key = failure.causeId || failure.fingerprint;
      const existing = byCause.get(key);
      if (existing) {
        existing.items.push(failure);
      } else {
        byCause.set(key, {
          key,
          label: causeLabel(failure),
          items: [failure],
        });
      }
    }
    return [...byCause.values()];
  }, [items, grouped]);

  function toggleCause(key: string) {
    setOpen((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  return (
    <div className="overflow-hidden rounded-[10px] border border-line bg-surface">
      <div
        className={cn(
          "grid gap-3 border-b border-line bg-[#12171e] px-3.5 py-2.5 text-[10.5px] uppercase tracking-[0.7px] text-faint max-lg:hidden",
          headerColumns,
        )}
      >
        <span />
        <span>Status</span>
        <span>TC ID</span>
        <span>Test</span>
        <span>Category</span>
        <span>Source</span>
        <span>Last seen</span>
      </div>

      {items.length === 0 && (
        <div className="px-4 py-10 text-center text-sm text-muted">
          No failure matches this filter.
        </div>
      )}

      {causes.map((cause) => {
        const causeOpen = !grouped || open.has(cause.key);
        const allSelected = cause.items.every((failure) => selection.has(failure.fingerprint));
        const someSelected = cause.items.some((failure) => selection.has(failure.fingerprint));
        const owners = [...new Set(cause.items.map((failure) => failure.triageOwner).filter(Boolean))];
        const ownerLabel =
          owners.length === 1 ? ` · owner ${owners[0]}` : owners.length > 1 ? " · mixed owners" : "";
        return (
          <div key={cause.key || "flat"}>
            {grouped && (
              <div className="flex items-center gap-2 border-b border-line-soft bg-[#0f141a] px-3 py-2">
                <input
                  type="checkbox"
                  checked={allSelected}
                  ref={(input) => {
                    if (input) input.indeterminate = someSelected && !allSelected;
                  }}
                  onChange={() => onToggleMany(cause.items.map((failure) => failure.fingerprint))}
                  aria-label={`Select all tests in ${cause.label}`}
                  className="h-3.5 w-3.5 accent-[var(--color-brand)]"
                />
                <button
                  type="button"
                  onClick={() => toggleCause(cause.key)}
                  aria-expanded={causeOpen}
                  className="grid h-6 w-6 shrink-0 place-items-center rounded text-faint outline-none transition-colors hover:bg-surface-2 hover:text-ink focus-visible:ring-2 focus-visible:ring-brand/60"
                  aria-label={causeOpen ? "Collapse cause" : "Expand cause"}
                >
                  {causeOpen ? (
                    <ChevronDown className="h-3.5 w-3.5" />
                  ) : (
                    <ChevronRight className="h-3.5 w-3.5" />
                  )}
                </button>
                <button
                  type="button"
                  onClick={() => toggleCause(cause.key)}
                  className="min-w-0 flex-1 text-left outline-none focus-visible:ring-2 focus-visible:ring-brand/60"
                >
                  <div className="truncate text-[12px] font-semibold text-ink">{cause.label}</div>
                  <div className="mt-0.5 truncate text-[10.5px] text-faint">
                    {cause.items.length} {cause.items.length === 1 ? "test" : "tests"} ·{" "}
                    {cause.items[0].lastSeen}
                    {ownerLabel}
                  </div>
                </button>
                <div className="hidden items-center gap-1 sm:flex">
                  {cause.items.length > 1 && (
                    <span className="font-mono text-[10px] text-faint">
                      {cause.items.reduce((n, failure) => n + failure.occurrences, 0)}x
                    </span>
                  )}
                </div>
              </div>
            )}

            {causeOpen &&
              cause.items.map((failure) => (
                <FailureRow
                  key={failure.fingerprint}
                  failure={failure}
                  selected={selected === failure.fingerprint}
                  checked={selection.has(failure.fingerprint)}
                  onSelect={() => onSelect(failure.fingerprint)}
                  onToggle={() => onToggle(failure.fingerprint)}
                />
              ))}
          </div>
        );
      })}
    </div>
  );
}

function FailureRow({
  failure,
  selected,
  checked,
  onSelect,
  onToggle,
}: {
  failure: Failure;
  selected: boolean;
  checked: boolean;
  onSelect: () => void;
  onToggle: () => void;
}) {
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onSelect}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onSelect();
        }
      }}
      className={cn(
        "grid w-full cursor-pointer grid-cols-1 gap-2 border-b border-line-soft px-3.5 py-3 text-left transition-colors last:border-b-0 hover:bg-[#1b222b] lg:items-center lg:gap-3",
        rowColumns,
        selected && "bg-[#1b2430] shadow-[inset_3px_0_0_var(--color-brand)]",
      )}
    >
      <input
        type="checkbox"
        checked={checked}
        onClick={(event) => event.stopPropagation()}
        onChange={onToggle}
        aria-label={`Select ${failure.title}`}
        className="h-3.5 w-3.5 accent-[var(--color-brand)]"
      />

      <div className="flex flex-col items-start gap-1">
        <span
          className={cn(
            "inline-flex items-center gap-1.5 text-[10.5px] font-bold tracking-wider",
            statusStyle[failure.status],
          )}
        >
          <span className="h-1.5 w-1.5 rounded-sm bg-current" />
          {statusLabel[failure.status]}
        </span>
        {failure.triageStatus !== "open" && (
          <Badge tone={triageTone[failure.triageStatus]}>
            {triageLabel[failure.triageStatus]}
          </Badge>
        )}
      </div>

      <span
        className={cn(
          "truncate font-mono text-[11px] text-muted",
          !failure.testCaseId && "hidden lg:block",
        )}
      >
        <span className="mr-1 text-faint lg:hidden">TC</span>
        {failure.testCaseId || "—"}
      </span>

      <div className="min-w-0">
        <div className="truncate text-[13px] font-medium">{failure.title}</div>
        <div className="mt-0.5 truncate font-mono text-[11px] text-faint">
          {failure.file} › {failure.test}
          {failure.triageOwner && ` · owner ${failure.triageOwner}`}
        </div>
      </div>

      <span className="flex items-center gap-1.5 text-[11px]">
        <span className={cn("h-[7px] w-[7px] rounded-full", categoryDot[failure.category])} />
        <Badge tone={categoryTone[failure.category]}>{categoryLabel[failure.category]}</Badge>
      </span>

      <Badge tone={sourceTone[failure.source]}>{sourceLabel[failure.source]}</Badge>

      <span className="flex min-w-0 flex-wrap items-baseline gap-x-1.5 text-[11px] text-muted">
        <span className="truncate">{failure.lastSeen}</span>
        {failure.lastSeenRun > 0 && (
          <Link
            to="/runs/$runId"
            params={{ runId: String(failure.lastSeenRun) }}
            onClick={(event) => event.stopPropagation()}
            className="font-mono text-[10.5px] text-brand hover:underline"
          >
            #{failure.lastSeenRun}
          </Link>
        )}
        <span className="font-mono text-[10.5px] text-faint">· {failure.occurrences}x</span>
      </span>
    </div>
  );
}
