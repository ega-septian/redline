import { Link } from "@tanstack/react-router";

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

const triageTone = {
  open: "neutral",
  acknowledged: "brand",
  in_progress: "env",
  muted: "neutral",
  wont_fix: "backend",
} as const;

const categoryDot = {
  backend_bug: "bg-danger",
  test_bug: "bg-violet",
  environment: "bg-warn",
  flaky: "bg-ok",
  unclear: "bg-faint",
} as const;

export function FailureTable({
  items,
  selected,
  onSelect,
}: {
  items: Failure[];
  selected: string;
  onSelect: (fingerprint: string) => void;
}) {
  return (
    <div className="overflow-hidden rounded-[10px] border border-line bg-surface">
      <div className="grid grid-cols-[104px_84px_minmax(0,1fr)_104px_84px_120px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2.5 text-[10.5px] uppercase tracking-[0.7px] text-faint max-lg:hidden">
        <span>Status</span>
        <span>TC ID</span>
        <span>Test</span>
        <span>Cause</span>
        <span>Source</span>
        <span>Last seen</span>
      </div>

      {items.length === 0 && (
        <div className="px-4 py-10 text-center text-sm text-muted">
          No failure matches this filter.
        </div>
      )}

      {items.map((failure) => (
        <div
          key={failure.fingerprint}
          role="button"
          tabIndex={0}
          onClick={() => onSelect(failure.fingerprint)}
          onKeyDown={(event) => {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              onSelect(failure.fingerprint);
            }
          }}
          className={cn(
            "grid w-full cursor-pointer grid-cols-1 gap-2 border-b border-line-soft px-3.5 py-3 text-left transition-colors last:border-b-0 hover:bg-[#1b222b] lg:grid-cols-[104px_84px_minmax(0,1fr)_104px_84px_120px] lg:items-center lg:gap-3",
            selected === failure.fingerprint &&
              "bg-[#1b2430] shadow-[inset_3px_0_0_var(--color-brand)]",
          )}
        >
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
      ))}
    </div>
  );
}
