import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Activity, AlertTriangle, Clock, Search, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type { TestHealthRow, TestRunPoint } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const windows = [7, 30, 90] as const;
type WindowDays = (typeof windows)[number];

export function HealthPage() {
  const [days, setDays] = useState<WindowDays>(30);
  const [project, setProject] = useState("");
  const [appliedProject, setAppliedProject] = useState("");
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string | null>(null);

  const healthQuery = useQuery({
    queryKey: ["test-health", days, appliedProject],
    queryFn: () => api.testHealth(days, appliedProject),
  });
  const summary = healthQuery.data?.summary;
  const tests = healthQuery.data?.tests ?? [];

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return tests;
    return tests.filter((test) =>
      `${test.test} ${test.test_case_id ?? ""} ${test.project ?? ""}`.toLowerCase().includes(needle),
    );
  }, [tests, search]);

  const flakiest = useMemo(
    () =>
      [...tests]
        .filter((test) => test.flaky > 0)
        .sort((a, b) => b.flake_rate - a.flake_rate || b.flaky - a.flaky)
        .slice(0, 5),
    [tests],
  );
  const failing = useMemo(
    () => [...tests].filter((test) => test.failed > 0).sort((a, b) => b.failed - a.failed).slice(0, 5),
    [tests],
  );
  const selectedTest = useMemo(
    () => tests.find((test) => test.key === selected) ?? null,
    [tests, selected],
  );

  if (healthQuery.isPending) return <State>Loading test health…</State>;
  if (healthQuery.isError) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load test health</div>
        <div className="mt-1 text-xs">
          {healthQuery.error instanceof Error ? healthQuery.error.message : "The API request failed."}
        </div>
        <Button className="mt-3" onClick={() => healthQuery.refetch()}>
          Retry
        </Button>
      </State>
    );
  }

  return (
    <>
      <section className="flex flex-wrap items-center gap-2 rounded-[10px] border border-line bg-surface px-2.5 py-2">
        <Activity className="h-3.5 w-3.5 text-brand" />
        <span className="mr-1 text-[11px] uppercase tracking-[0.7px] text-faint">Window</span>
        {windows.map((window) => (
          <button
            key={window}
            type="button"
            onClick={() => setDays(window)}
            className={cn(
              "rounded-full border px-2.5 py-1 text-xs transition-colors",
              days === window
                ? "border-[#1f6feb66] bg-[#1f6feb22] text-[#79c0ff]"
                : "border-line bg-[#0f141a] text-muted hover:text-ink",
            )}
          >
            {window}d
          </button>
        ))}
        <form
          className="ml-2 flex items-center gap-1.5"
          onSubmit={(event) => {
            event.preventDefault();
            setAppliedProject(project.trim());
          }}
        >
          <Input
            value={project}
            onChange={(event) => setProject(event.target.value)}
            placeholder="project (all)"
            aria-label="Project filter"
            className="h-7 w-32 text-[11px]"
          />
          <Button type="submit" size="sm" variant="outline">
            Apply
          </Button>
        </form>
        <div className="ml-auto flex min-w-[180px] flex-1 items-center gap-1.5 rounded-md border border-line bg-canvas px-2.5 text-faint sm:max-w-xs">
          <Search className="h-3.5 w-3.5" />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search test or TC ID…"
            aria-label="Search tests"
            className="h-7 border-0 bg-transparent px-0 text-[11px] focus-visible:ring-0"
          />
        </div>
      </section>

      <section className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-7">
        <Kpi label="Runs" value={String(summary?.runs ?? 0)} hint={`${summary?.results ?? 0} results`} />
        <Kpi
          label="Pass rate"
          value={summary && summary.results > 0 ? pct(summary.pass_rate) : "—"}
          hint={`${summary?.passed ?? 0} passed`}
          tone="ok"
        />
        <Kpi
          label="Flake rate"
          value={summary && summary.results > 0 ? pct(summary.flake_rate) : "—"}
          hint={`${summary?.flaky ?? 0} flaky`}
          tone="warn"
        />
        <Kpi label="Open failures" value={String(summary?.open_failures ?? 0)} hint="open or regressed" tone="danger" />
        <Kpi label="New failures" value={String(summary?.new_failures ?? 0)} hint={`in the last ${days}d`} />
        <Kpi label="Resolved" value={String(summary?.resolved_failures ?? 0)} hint={`in the last ${days}d`} tone="ok" />
        <Kpi
          label="MTTR"
          value={summary && summary.mttr_hours > 0 ? `${summary.mttr_hours.toFixed(1)} h` : "—"}
          hint="mean time to resolve"
        />
      </section>

      <section className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <TopList
          title="Flakiest tests"
          icon={<Activity className="h-3.5 w-3.5 text-warn" />}
          tests={flakiest}
          metric={(test) => `${pct(test.flake_rate)} · ${test.flaky} flaky`}
          empty="No flaky test in this window."
          onSelect={setSelected}
        />
        <TopList
          title="Most failed"
          icon={<AlertTriangle className="h-3.5 w-3.5 text-danger" />}
          tests={failing}
          metric={(test) => `${test.failed} failed · ${pct(test.pass_rate)} pass`}
          empty="No failed test in this window."
          onSelect={setSelected}
        />
      </section>

      <div
        className={cn(
          "grid grid-cols-1 gap-3",
          selectedTest && "xl:grid-cols-[minmax(0,1fr)_390px]",
        )}
      >
        <div className="min-w-0 overflow-hidden rounded-[10px] border border-line bg-surface">
          <div className="flex items-center justify-between border-b border-line bg-[#12171e] px-3.5 py-2.5">
            <span className="text-[10.5px] uppercase tracking-[0.7px] text-faint">
              {filtered.length} test{filtered.length === 1 ? "" : "s"}
            </span>
            <span className="text-[11px] text-faint">
              {appliedProject ? `project ${appliedProject}` : "all projects"} · {days}d
            </span>
          </div>
          <div className="overflow-x-auto">
            <div className="min-w-[900px]">
              <div className="grid grid-cols-[minmax(220px,1fr)_90px_54px_54px_54px_64px_64px_90px_96px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2 text-[10px] uppercase tracking-[0.7px] text-faint">
                <span>Test</span>
                <span>TC ID</span>
                <span className="text-right">Pass</span>
                <span className="text-right">Fail</span>
                <span className="text-right">Flaky</span>
                <span className="text-right">Pass %</span>
                <span className="text-right">Flake %</span>
                <span>Last</span>
                <span>Owner</span>
              </div>
              {filtered.length === 0 ? (
                <div className="px-4 py-10 text-center text-sm text-muted">
                  No test history in this window. Run the suite, then check again.
                </div>
              ) : (
                filtered.map((test) => (
                  <TestRow
                    key={test.key}
                    test={test}
                    active={test.key === selected}
                    onSelect={() => setSelected(test.key)}
                  />
                ))
              )}
            </div>
          </div>
        </div>

        {selectedTest && (
          <TestDetail row={selectedTest} days={days} onClose={() => setSelected(null)} />
        )}
      </div>
      {!selectedTest && (
        <p className="text-[11px] text-faint">
          Select a test to see its run timeline, stability and owner.
        </p>
      )}
    </>
  );
}

function TestRow({ test, active, onSelect }: { test: TestHealthRow; active: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        "grid w-full grid-cols-[minmax(220px,1fr)_90px_54px_54px_54px_64px_64px_90px_96px] gap-3 border-b border-line-soft px-3.5 py-2.5 text-left text-[11.5px] transition-colors last:border-b-0 hover:bg-[#1b222b]",
        active && "bg-[#1f6feb14]",
      )}
    >
      <div className="min-w-0">
        <div className="truncate text-[12.5px] text-ink">{shortTitle(test.test)}</div>
        <div className="truncate font-mono text-[10.5px] text-faint">{test.test}</div>
      </div>
      <span className="truncate font-mono text-[10.5px] text-muted">{test.test_case_id || "—"}</span>
      <span className="text-right font-mono text-ok">{test.passed}</span>
      <span className={cn("text-right font-mono", test.failed > 0 ? "text-danger" : "text-faint")}>
        {test.failed}
      </span>
      <span className={cn("text-right font-mono", test.flaky > 0 ? "text-warn" : "text-faint")}>
        {test.flaky}
      </span>
      <span className="text-right font-mono text-muted">{pct(test.pass_rate)}</span>
      <span className="text-right font-mono text-muted">{test.flaky > 0 ? pct(test.flake_rate) : "—"}</span>
      <StatusBadge status={test.last_status} />
      <span className="truncate text-[11px] text-faint" title={test.triage_owner || ""}>
        {test.triage_owner || "—"}
      </span>
    </button>
  );
}

function TestDetail({ row, days, onClose }: { row: TestHealthRow; days: number; onClose: () => void }) {
  const historyQuery = useQuery({
    queryKey: ["test-history", row.key, days],
    queryFn: () => api.testHistory(row.key, days),
  });
  const runs = historyQuery.data?.runs ?? [];

  return (
    <Card className="overflow-hidden xl:sticky xl:top-4 xl:self-start">
      <div className="border-b border-line bg-[#12171e] p-4">
        <div className="flex items-start gap-2">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <StatusBadge status={row.last_status} />
              {row.test_case_id && <Badge tone="brand">{row.test_case_id}</Badge>}
              {row.triage_owner && <Badge tone="neutral">owner {row.triage_owner}</Badge>}
            </div>
            <h2 className="mt-2 text-sm font-semibold leading-snug">{shortTitle(row.test)}</h2>
            <div className="mt-1 break-all font-mono text-[10.5px] text-faint">{row.test}</div>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close test detail">
            <X className="h-4 w-4" />
          </Button>
        </div>
        <div className="mt-3 grid grid-cols-4 gap-1.5 text-center">
          <Mini label="Total" value={row.total} />
          <Mini label="Pass" value={row.passed} tone="ok" />
          <Mini label="Fail" value={row.failed} tone="danger" />
          <Mini label="Flaky" value={row.flaky} tone="warn" />
        </div>
        <div className="mt-1.5 flex justify-between text-[10.5px] text-faint">
          <span>pass {pct(row.pass_rate)}</span>
          <span>flake {pct(row.flake_rate)}</span>
          <span>avg {duration(row.avg_duration_ms)}</span>
        </div>
      </div>

      <div className="p-4">
        <div className="mb-2 flex items-center justify-between">
          <span className="text-[10px] uppercase tracking-[0.8px] text-faint">Timeline</span>
          <span className="text-[10.5px] text-faint">last {days}d</span>
        </div>
        {historyQuery.isPending && <p className="text-xs text-muted">Loading history…</p>}
        {historyQuery.isError && (
          <p className="text-xs text-danger">
            {historyQuery.error instanceof Error ? historyQuery.error.message : "History failed."}
          </p>
        )}
        {historyQuery.isSuccess && runs.length === 0 && (
          <p className="text-xs text-muted">No run in this window.</p>
        )}
        <div className="flex flex-col gap-1.5">
          {runs.map((run) => (
            <RunPoint key={`${run.run_id}-${run.at}`} point={run} />
          ))}
        </div>
      </div>
    </Card>
  );
}

function RunPoint({ point }: { point: TestRunPoint }) {
  return (
    <Link
      to="/runs/$runId"
      params={{ runId: String(point.run_id) }}
      className="rounded-md border border-line-soft bg-canvas px-2.5 py-2 transition-colors hover:border-line hover:bg-[#12171e]"
    >
      <div className="flex items-center gap-2">
        <StatusBadge status={point.status} />
        <span className="font-mono text-[10.5px] text-muted">run #{point.run_id}</span>
        <span className="ml-auto text-[10.5px] text-faint">{when(point.at)}</span>
      </div>
      <div className="mt-1 flex items-center gap-2 text-[10.5px] text-faint">
        <Clock className="h-3 w-3" />
        <span>{duration(point.duration_ms)}</span>
        {point.retries > 0 && <span>· {point.retries} retries</span>}
        {point.branch && <span className="truncate">· {point.branch}</span>}
        {point.commit && <span className="font-mono">· {point.commit.slice(0, 7)}</span>}
      </div>
      {point.error && (
        <div className="mt-1 truncate font-mono text-[10px] text-[#ff7b72]" title={point.error}>
          {oneLine(point.error)}
        </div>
      )}
    </Link>
  );
}

function TopList({
  title,
  icon,
  tests,
  metric,
  empty,
  onSelect,
}: {
  title: string;
  icon: ReactNode;
  tests: TestHealthRow[];
  metric: (test: TestHealthRow) => string;
  empty: string;
  onSelect: (key: string) => void;
}) {
  return (
    <Card className="overflow-hidden">
      <div className="flex items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        {icon}
        <span className="text-[12.5px] font-semibold">{title}</span>
        <span className="ml-auto text-[10.5px] text-faint">top {tests.length}</span>
      </div>
      {tests.length === 0 ? (
        <div className="px-3.5 py-6 text-xs text-muted">{empty}</div>
      ) : (
        tests.map((test) => (
          <button
            key={test.key}
            type="button"
            onClick={() => onSelect(test.key)}
            className="flex w-full items-center gap-2 border-b border-line-soft px-3.5 py-2 text-left text-[11.5px] last:border-b-0 hover:bg-[#1b222b]"
          >
            <span className="min-w-0 flex-1 truncate text-ink">{shortTitle(test.test)}</span>
            <span className="shrink-0 font-mono text-[10.5px] text-muted">{metric(test)}</span>
          </button>
        ))
      )}
    </Card>
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
  hint?: string;
  tone?: "ok" | "warn" | "danger";
}) {
  return (
    <Card className="px-3 py-2.5">
      <div className="text-[10.5px] text-faint">{label}</div>
      <div
        className={cn(
          "mt-0.5 text-xl font-semibold tracking-tight",
          tone === "ok" && "text-ok",
          tone === "warn" && "text-warn",
          tone === "danger" && "text-danger",
        )}
      >
        {value}
      </div>
      {hint && <div className="mt-0.5 truncate text-[10px] text-faint">{hint}</div>}
    </Card>
  );
}

function Mini({ label, value, tone }: { label: string; value: number; tone?: "ok" | "danger" | "warn" }) {
  return (
    <div className="rounded border border-line-soft bg-canvas px-1 py-1.5">
      <div
        className={cn(
          "font-mono text-sm font-semibold",
          tone === "ok" && "text-ok",
          tone === "danger" && "text-danger",
          tone === "warn" && "text-warn",
        )}
      >
        {value}
      </div>
      <div className="text-[9.5px] uppercase tracking-[0.6px] text-faint">{label}</div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const tone =
    status === "passed" ? "flaky" : status === "failed" ? "backend" : status === "flaky" ? "env" : "neutral";
  return <Badge tone={tone}>{status || "unknown"}</Badge>;
}

function State({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>
  );
}

function shortTitle(test: string): string {
  return test.split(" › ").slice(2).join(" › ") || test;
}

function pct(value: number): string {
  return `${Math.round(value * 100)}%`;
}

function duration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

function when(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function oneLine(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}
