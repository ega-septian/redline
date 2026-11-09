import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Activity, AlertTriangle, Check, Clock, Copy, Search, Sparkles, Timer, TrendingUp, X } from "lucide-react";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";

import { api } from "@/api/client";
import type { ApiQuarantineSuggestion, TestHealthRow, TestRunPoint } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useProject } from "@/lib/project";
import { cn } from "@/lib/utils";

const windows = [7, 30, 90] as const;
type WindowDays = (typeof windows)[number];
const REGRESSION_PCT = 30;
const REGRESSION_MS = 100;

export function HealthPage() {
  const { project } = useProject();
  const [days, setDays] = useState<WindowDays>(30);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string | null>(null);

  const healthQuery = useQuery({
    queryKey: ["test-health", days, project],
    queryFn: () => api.testHealth(days, project),
  });
  const suggestionsQuery = useQuery({
    queryKey: ["quarantine-suggestions", days, project],
    queryFn: () => api.quarantineSuggestions(days, project),
  });
  const suggestions = suggestionsQuery.data ?? [];
  const summary = healthQuery.data?.summary;
  const tests = healthQuery.data?.tests ?? [];
  const quarantined = tests.filter((test) => test.quarantined).length;

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
  const slowest = useMemo(
    () =>
      [...tests]
        .filter((test) => test.avg_duration_ms > 0)
        .sort((a, b) => b.avg_duration_ms - a.avg_duration_ms)
        .slice(0, 5),
    [tests],
  );
  const regressions = useMemo(
    () =>
      [...tests]
        .filter(
          (test) =>
            (test.previous_duration_ms ?? 0) > 0 &&
            (test.last_duration_ms ?? 0) > 0 &&
            (test.duration_delta_pct ?? 0) >= REGRESSION_PCT &&
            (test.last_duration_ms ?? 0) - (test.previous_duration_ms ?? 0) >= REGRESSION_MS,
        )
        .sort((a, b) => (b.duration_delta_pct ?? 0) - (a.duration_delta_pct ?? 0))
        .slice(0, 5),
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

      <section className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
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
          label="Quarantined"
          value={String(quarantined)}
          hint="active exceptions"
          tone="warn"
        />
        <Kpi
          label="MTTR"
          value={summary && summary.mttr_hours > 0 ? `${summary.mttr_hours.toFixed(1)} h` : "—"}
          hint="mean time to resolve"
        />
      </section>

      {suggestions.length > 0 && (
        <SuggestionsPanel suggestions={suggestions} onSelect={setSelected} />
      )}

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

      <section className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <TopList
          title="Slowest tests"
          icon={<Timer className="h-3.5 w-3.5 text-brand" />}
          tests={slowest}
          metric={(test) => `${duration(test.avg_duration_ms)} avg · ${executions(test)} runs`}
          empty="No duration data in this window."
          onSelect={setSelected}
        />
        <TopList
          title="Duration regressions"
          icon={<TrendingUp className="h-3.5 w-3.5 text-warn" />}
          tests={regressions}
          metric={(test) =>
            `+${Math.round(test.duration_delta_pct ?? 0)}% · ${duration(test.previous_duration_ms ?? 0)} → ${duration(test.last_duration_ms ?? 0)}`
          }
          empty="No test is ≥30% and ≥100 ms slower than its previous run."
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
              {project ? `project ${project}` : "no project"} · {days}d
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
          <TestDetail key={selectedTest.key} row={selectedTest} days={days} onClose={() => setSelected(null)} />
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
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-[12.5px] text-ink">{shortTitle(test.test)}</span>
          {test.quarantined && (
            <Badge tone="env" className="shrink-0">
              Q
            </Badge>
          )}
        </div>
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
        <div className="mt-1 flex flex-wrap justify-between gap-x-3 gap-y-0.5 text-[10.5px] text-faint">
          <span>last {row.last_duration_ms ? duration(row.last_duration_ms) : "—"}</span>
          <span>prev {row.previous_duration_ms ? duration(row.previous_duration_ms) : "—"}</span>
          {(row.duration_delta_pct ?? 0) !== 0 && (
            <span className={cn((row.duration_delta_pct ?? 0) > 0 ? "text-warn" : "text-ok")}>
              {(row.duration_delta_pct ?? 0) > 0 ? "+" : ""}
              {Math.round(row.duration_delta_pct ?? 0)}%
            </span>
          )}
        </div>
      </div>

      <div className="p-4">
        <QuarantinePanel row={row} />
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

function QuarantinePanel({ row }: { row: TestHealthRow }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [owner, setOwner] = useState(row.triage_owner ?? "");
  const [reason, setReason] = useState("");
  const [days, setDays] = useState("7");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["test-health"] }),
      queryClient.invalidateQueries({ queryKey: ["test-history"] }),
      queryClient.invalidateQueries({ queryKey: ["quarantine-suggestions"] }),
    ]);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!owner.trim() || !reason.trim()) return;
    setBusy(true);
    setError("");
    try {
      await api.quarantine({
        test: row.key,
        owner: owner.trim(),
        reason: reason.trim(),
        days: Number(days),
      });
      setOpen(false);
      setReason("");
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Quarantine failed.");
    } finally {
      setBusy(false);
    }
  }

  async function release() {
    setBusy(true);
    setError("");
    try {
      await api.releaseQuarantine(row.key);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Release failed.");
    } finally {
      setBusy(false);
    }
  }

  if (row.quarantined) {
    return (
      <div className="mb-3 rounded-md border border-warn/30 bg-warn/5 px-2.5 py-2 text-[11px]">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone="env">quarantined</Badge>
          <span className="text-faint">
            until {row.quarantine_until ? when(row.quarantine_until) : "—"}
          </span>
          <Button
            size="sm"
            variant="outline"
            className="ml-auto"
            onClick={release}
            disabled={busy}
          >
            {busy ? "Releasing…" : "Release"}
          </Button>
        </div>
        <div className="mt-1 text-muted">
          {row.quarantine_owner || "—"} · {row.quarantine_reason || "no reason"}
        </div>
        {error && <div className="mt-1 text-danger">{error}</div>}
      </div>
    );
  }

  return (
    <div className="mb-3">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="outline" onClick={() => setOpen((value) => !value)}>
          {open ? "Cancel" : "Quarantine"}
        </Button>
        <span className="text-[10.5px] text-faint">Known failure, reviewed and time-boxed.</span>
      </div>
      {open && (
        <form
          onSubmit={submit}
          className="mt-2 flex flex-col gap-1.5 rounded-md border border-line-soft bg-canvas p-2"
        >
          <Input
            value={owner}
            onChange={(event) => setOwner(event.target.value)}
            placeholder="owner, e.g. ega"
            aria-label="Quarantine owner"
            className="h-7 text-[11px]"
          />
          <Input
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            placeholder="reason or issue link"
            aria-label="Quarantine reason"
            className="h-7 text-[11px]"
          />
          <div className="flex items-center gap-1.5">
            <select
              value={days}
              onChange={(event) => setDays(event.target.value)}
              aria-label="Quarantine days"
              className="h-7 rounded-md border border-line bg-canvas px-1.5 text-[11px] text-ink outline-none focus-visible:border-brand/60"
            >
              <option value="7">7 days</option>
              <option value="14">14 days</option>
              <option value="30">30 days</option>
            </select>
            <Button type="submit" size="sm" disabled={busy || !owner.trim() || !reason.trim()}>
              {busy ? "Saving…" : "Save"}
            </Button>
          </div>
        </form>
      )}
      {error && <div className="mt-1 text-[11px] text-danger">{error}</div>}
    </div>
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

function executions(test: TestHealthRow): number {
  return test.passed + test.failed + test.flaky;
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

function SuggestionsPanel({
  suggestions,
  onSelect,
}: {
  suggestions: ApiQuarantineSuggestion[];
  onSelect: (key: string) => void;
}) {
  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <Sparkles className="h-3.5 w-3.5 text-violet" />
        <span className="text-[12.5px] font-semibold">Suggested quarantines</span>
        <Badge tone="test">{suggestions.length}</Badge>
        <span className="ml-auto text-[10.5px] text-faint">
          Evidence only — nothing is quarantined automatically
        </span>
      </div>
      {suggestions.slice(0, 5).map((suggestion) => (
        <SuggestionRow key={suggestion.key} suggestion={suggestion} onSelect={onSelect} />
      ))}
      {suggestions.length > 5 && (
        <div className="border-t border-line px-3.5 py-2 text-[10.5px] text-faint">
          {suggestions.length - 5} more in the table below.
        </div>
      )}
    </Card>
  );
}

function SuggestionRow({
  suggestion,
  onSelect,
}: {
  suggestion: ApiQuarantineSuggestion;
  onSelect: (key: string) => void;
}) {
  const queryClient = useQueryClient();
  const [owner, setOwner] = useState(suggestion.owner ?? "");
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");

  const test = suggestion.test_case_id || suggestion.key;
  const canAct = owner.trim() !== "";
  const json = JSON.stringify(
    {
      test,
      owner: owner.trim(),
      reason: suggestion.reason,
      until: suggestion.suggested_until,
    },
    null,
    2,
  );

  async function copy() {
    if (!canAct) return;
    setError("");
    try {
      await navigator.clipboard.writeText(json);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      window.prompt("Copy quarantine JSON", json);
    }
  }

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["test-health"] }),
      queryClient.invalidateQueries({ queryKey: ["test-history"] }),
      queryClient.invalidateQueries({ queryKey: ["quarantine-suggestions"] }),
    ]);
  }

  async function quarantine() {
    if (!canAct) return;
    setBusy(true);
    setError("");
    try {
      await api.quarantine({
        test: suggestion.key,
        owner: owner.trim(),
        reason: suggestion.reason,
        days: suggestion.days,
      });
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Quarantine failed.");
    } finally {
      setBusy(false);
    }
  }

  async function dismiss() {
    setBusy(true);
    setError("");
    try {
      await api.dismissQuarantineSuggestion(suggestion.key, 7);
      await queryClient.invalidateQueries({ queryKey: ["quarantine-suggestions"] });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Dismiss failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-line-soft px-3.5 py-2.5 last:border-b-0">
      <Badge tone={suggestion.score === "high" ? "backend" : "env"}>{suggestion.score}</Badge>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate text-[12px] text-ink">{shortTitle(suggestion.test)}</span>
          {suggestion.test_case_id && (
            <span className="shrink-0 font-mono text-[10px] text-faint">{suggestion.test_case_id}</span>
          )}
        </div>
        <div className="mt-0.5 truncate text-[10.5px] text-faint" title={suggestion.reason}>
          {suggestion.reason}
        </div>
      </div>
      {!suggestion.owner && (
        <Input
          value={owner}
          onChange={(event) => setOwner(event.target.value)}
          placeholder="owner"
          aria-label="Quarantine owner"
          className="h-7 w-24 text-[11px]"
        />
      )}
      <Button size="sm" variant="default" onClick={copy} disabled={!canAct || busy}>
        {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
        {copied ? "Copied" : "Copy JSON"}
      </Button>
      <Button
        size="sm"
        variant="outline"
        onClick={quarantine}
        disabled={!canAct || busy}
        title="Write a 7-day quarantine to this server. For PR CI, copy the JSON into redline-quarantine.json."
      >
        Quarantine on server
      </Button>
      <Button size="sm" variant="ghost" onClick={() => onSelect(suggestion.key)}>
        Review
      </Button>
      <Button size="sm" variant="ghost" onClick={dismiss} disabled={busy}>
        Dismiss
      </Button>
      {error && <div className="w-full text-[10.5px] text-danger">{error}</div>}
    </div>
  );
}
