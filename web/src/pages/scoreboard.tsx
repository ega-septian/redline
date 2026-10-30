import { useQuery } from "@tanstack/react-query";
import { Check, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type { ScoreEntry } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

const sources = ["all", "rule", "ai"] as const;
type SourceFilter = (typeof sources)[number];

function date(iso: string | null): string {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

export function ScoreboardPage() {
  const [source, setSource] = useState<SourceFilter>("all");
  const boardQuery = useQuery({ queryKey: ["scoreboard"], queryFn: api.scoreboard });
  const board = boardQuery.data;

  const entries = useMemo(() => {
    const list = board?.entries ?? [];
    return source === "all" ? list : list.filter((entry) => entry.source === source);
  }, [board, source]);

  if (boardQuery.isPending) return <State>Loading scoreboard…</State>;
  if (boardQuery.isError) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load the scoreboard</div>
        <div className="mt-1 text-xs">
          {boardQuery.error instanceof Error ? boardQuery.error.message : "The API request failed."}
        </div>
        <Button className="mt-3" onClick={() => boardQuery.refetch()}>
          Retry
        </Button>
      </State>
    );
  }

  return (
    <>
      <section className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {(board?.sources ?? []).map((score) => (
          <Card key={score.source} className="px-3.5 py-3">
            <div className="flex items-center justify-between">
              <span className="text-[11.5px] text-muted">{score.source.toUpperCase()} accuracy</span>
              <span className="text-[11px] text-faint">
                {score.correct}/{score.graded} correct
              </span>
            </div>
            <div className="mt-0.5 text-2xl font-semibold tracking-tight">
              {score.graded === 0 ? "—" : `${Math.round(score.accuracy * 100)}%`}
            </div>
            <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-line">
              <div
                className="h-full rounded-full bg-ok"
                style={{ width: `${Math.round(score.accuracy * 100)}%` }}
              />
            </div>
            <div className="mt-1.5 text-[11px] text-faint">
              {score.unknown} unknown · guesses are graded only when proven
            </div>
          </Card>
        ))}
        {(board?.sources ?? []).length === 0 && (
          <State>No prediction has been graded yet. Run `npx redline verify` and label failures.</State>
        )}
      </section>

      <section className="flex flex-wrap items-center gap-2 rounded-[10px] border border-line bg-surface px-2.5 py-2">
        {sources.map((item) => (
          <button
            key={item}
            type="button"
            onClick={() => setSource(item)}
            className={cn(
              "rounded-full border px-2.5 py-1 text-xs uppercase whitespace-nowrap transition-colors",
              source === item
                ? "border-[#1f6feb66] bg-[#1f6feb22] text-[#79c0ff]"
                : "border-line bg-[#0f141a] text-muted hover:text-ink",
            )}
          >
            {item}
          </button>
        ))}
        <span className="ml-auto text-[11px] text-faint">
          {entries.length} entr{entries.length === 1 ? "y" : "ies"}
        </span>
      </section>

      <div className="overflow-hidden rounded-[10px] border border-line bg-surface">
        <div className="grid grid-cols-[minmax(0,1fr)_70px_110px_150px_100px_100px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2.5 text-[10.5px] uppercase tracking-[0.7px] text-faint max-lg:hidden">
          <span>Test</span>
          <span>Source</span>
          <span>Predicted</span>
          <span>Truth</span>
          <span>Result</span>
          <span>Proven</span>
        </div>

        {entries.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-muted">
            No graded prediction for this filter yet.
          </div>
        ) : (
          entries.map((entry) => <ScoreRow key={`${entry.group_id}-${entry.source}`} entry={entry} />)
        )}
      </div>

      <p className="text-[11px] text-faint">
        Accuracy is measured only against proven cases (labels or experiments). “Unknown” means Redline
        did not guess.
      </p>
    </>
  );
}

function ScoreRow({ entry }: { entry: ScoreEntry }) {
  return (
    <div className="grid grid-cols-1 gap-2 border-b border-line-soft px-3.5 py-3 text-xs last:border-b-0 hover:bg-[#1b222b] lg:grid-cols-[minmax(0,1fr)_70px_110px_150px_100px_100px] lg:items-center lg:gap-3">
      <div className="min-w-0">
        <div className="truncate text-[13px] text-ink">{entry.test}</div>
        <div className="truncate font-mono text-[11px] text-faint">{entry.group_id}</div>
      </div>
      <Badge tone={entry.source === "ai" ? "test" : "neutral"}>{entry.source}</Badge>
      <span className="truncate font-mono text-[11px] text-muted" title={entry.predicted}>
        {entry.predicted}
      </span>
      <span className="min-w-0 truncate font-mono text-[11px] text-[#cdd9e5]" title={`${entry.truth} (${entry.truth_by})`}>
        {entry.truth}
        <span className="ml-1 text-faint">({entry.truth_by})</span>
      </span>
      <span
        className={cn(
          "inline-flex items-center gap-1 whitespace-nowrap text-[11px]",
          entry.correct ? "text-ok" : "text-danger",
        )}
      >
        {entry.correct ? <Check className="h-3.5 w-3.5" /> : <X className="h-3.5 w-3.5" />}
        {entry.correct ? "correct" : "wrong"}
      </span>
      <span className="text-faint">{date(entry.proven_at)}</span>
    </div>
  );
}

function State({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>
  );
}
