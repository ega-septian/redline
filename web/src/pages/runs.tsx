import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ExternalLink, Search } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type { ApiRun } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const sources = ["all", "ci", "local"] as const;
type SourceFilter = (typeof sources)[number];

function duration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
}

function when(iso: string): string {
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

export function RunsPage() {
  const [source, setSource] = useState<SourceFilter>("all");
  const [query, setQuery] = useState("");
  const runsQuery = useQuery({ queryKey: ["runs"], queryFn: api.runs });
  const runs = runsQuery.data ?? [];

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return runs.filter((run) => {
      if (source !== "all" && run.source !== source) return false;
      if (!needle) return true;
      return [run.branch, run.commit, run.triggered_by, run.app_version, String(run.id)]
        .join(" ")
        .toLowerCase()
        .includes(needle);
    });
  }, [runs, source, query]);

  const totals = useMemo(
    () => ({
      runs: runs.length,
      passed: runs.reduce((total, run) => total + run.passed, 0),
      failed: runs.reduce((total, run) => total + run.failed, 0),
      flaky: runs.reduce((total, run) => total + run.flaky, 0),
    }),
    [runs],
  );

  if (runsQuery.isPending) {
    return <State>Loading runs…</State>;
  }
  if (runsQuery.isError) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load runs</div>
        <div className="mt-1 text-xs">
          {runsQuery.error instanceof Error ? runsQuery.error.message : "The API request failed."}
        </div>
        <Button className="mt-3" onClick={() => runsQuery.refetch()}>
          Retry
        </Button>
      </State>
    );
  }

  return (
    <>
      <section className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Kpi label="Runs" value={String(totals.runs)} hint="last 50" />
        <Kpi label="Passed" value={String(totals.passed)} hint="test results" tone="ok" />
        <Kpi label="Failed" value={String(totals.failed)} hint="test results" tone="danger" />
        <Kpi label="Flaky" value={String(totals.flaky)} hint="passed on retry" tone="warn" />
      </section>

      <section className="flex flex-wrap items-center gap-2 rounded-[10px] border border-line bg-surface px-2.5 py-2">
        {sources.map((item) => (
          <button
            key={item}
            type="button"
            onClick={() => setSource(item)}
            className={cn(
              "rounded-full border px-2.5 py-1 text-xs capitalize whitespace-nowrap transition-colors",
              source === item
                ? "border-[#1f6feb66] bg-[#1f6feb22] text-[#79c0ff]"
                : "border-line bg-[#0f141a] text-muted hover:text-ink",
            )}
          >
            {item}
          </button>
        ))}
        <div className="flex w-full min-w-0 items-center gap-1.5 rounded-md border border-line bg-[#0f141a] px-2.5 py-1 text-faint sm:ml-auto sm:w-auto sm:min-w-[210px]">
          <Search className="h-3.5 w-3.5" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search branch, commit, run…"
            className="h-6 border-0 bg-transparent px-0 focus-visible:ring-0"
            aria-label="Search runs"
          />
        </div>
      </section>

      <div className="overflow-hidden rounded-[10px] border border-line bg-surface">
        <div className="grid grid-cols-[70px_minmax(0,1fr)_100px_150px_130px_90px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2.5 text-[10.5px] uppercase tracking-[0.7px] text-faint max-lg:hidden">
          <span>Run</span>
          <span>Branch / commit</span>
          <span>Source</span>
          <span>Results</span>
          <span>Triggered by</span>
          <span>Duration</span>
        </div>

        {filtered.length === 0 && (
          <div className="px-4 py-10 text-center text-sm text-muted">No run matches this filter.</div>
        )}

        {filtered.map((run) => (
          <RunRow key={run.id} run={run} />
        ))}
      </div>

      <p className="text-[11px] text-faint">
        <span className="font-mono">GET /api/runs?limit=50</span>. Select a run to see every test,
        its TC ID, duration, retries and failure details.
      </p>
    </>
  );
}

function RunRow({ run }: { run: ApiRun }) {
  const navigate = useNavigate();
  const results: Array<[string, number, string]> = [
    ["passed", run.passed, "text-ok"],
    ["failed", run.failed, "text-danger"],
    ["flaky", run.flaky, "text-warn"],
    ["skipped", run.skipped, "text-faint"],
  ];

  return (
    <div
      onClick={() => navigate({ to: "/runs/$runId", params: { runId: String(run.id) } })}
      className="grid cursor-pointer grid-cols-1 gap-2 border-b border-line-soft px-3.5 py-3 text-xs last:border-b-0 hover:bg-[#1b222b] lg:grid-cols-[70px_minmax(0,1fr)_100px_150px_130px_90px] lg:items-center lg:gap-3"
    >
      <div className="flex items-center gap-2">
        <Link
          to="/runs/$runId"
          params={{ runId: String(run.id) }}
          onClick={(event) => event.stopPropagation()}
          className="font-mono text-muted hover:text-brand hover:underline"
        >
          #{run.id}
        </Link>
        {run.ci_url && (
          <a
            href={run.ci_url}
            target="_blank"
            rel="noreferrer"
            onClick={(event) => event.stopPropagation()}
            className="text-brand hover:underline"
            aria-label={`Open run ${run.id} in CI`}
          >
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>

      <div className="min-w-0">
        <div className="truncate text-[13px] text-ink">{run.branch || "—"}</div>
        <div className="truncate font-mono text-[11px] text-faint">
          {run.commit ? run.commit.slice(0, 10) : "no commit"} · {when(run.created_at)}
        </div>
      </div>

      <div>
        <Badge tone={run.source === "ci" ? "brand" : "neutral"}>{run.source || "local"}</Badge>
      </div>

      <div className="flex flex-wrap gap-2">
        {results
          .filter(([, value]) => value > 0)
          .map(([label, value, tone]) => (
            <span key={label} className={cn("font-mono", tone)}>
              {value} {label}
            </span>
          ))}
        {run.report_errors.length > 0 && (
          <span className="text-danger" title={run.report_errors.join("\n")}>
            report error
          </span>
        )}
      </div>

      <div className="truncate text-muted">{run.triggered_by || "—"}</div>
      <div className="font-mono text-faint">{duration(run.duration_ms)}</div>
    </div>
  );
}

function Kpi({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: string;
  hint: string;
  tone?: "ok" | "danger" | "warn";
}) {
  return (
    <Card className="px-3.5 py-3">
      <div className="text-[11.5px] text-muted">{label}</div>
      <div
        className={cn(
          "mt-0.5 text-2xl font-semibold tracking-tight",
          tone === "ok" && "text-[#56d364]",
          tone === "danger" && "text-[#ff7b72]",
          tone === "warn" && "text-warn",
        )}
      >
        {value}
      </div>
      <div className="text-[11px] text-faint">{hint}</div>
    </Card>
  );
}

function State({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>
  );
}
