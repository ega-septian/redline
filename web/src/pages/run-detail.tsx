import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { ArrowLeft, Check, Copy, ExternalLink, Search, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type { ApiRunResult } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { categoryLabel, type Category } from "@/lib/failure";
import { cn } from "@/lib/utils";

const filters = ["all", "failed", "flaky", "skipped", "passed"] as const;
type Filter = (typeof filters)[number];

const categoryTone = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
} as const;

function duration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
}

function when(iso: string | null | undefined): string {
  if (!iso) return "—";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

function parts(testKey: string): { file: string; title: string } {
  const [, file = "", ...rest] = testKey.split(" › ");
  return { file, title: rest.join(" › ") || testKey };
}

function escapeRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function categoryOf(result: ApiRunResult): Category {
  const raw = result.human_label ?? result.category ?? "";
  return raw in categoryLabel ? (raw as Category) : "unclear";
}

export function RunDetailPage() {
  const { runId } = useParams({ from: "/runs/$runId" });
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string>("");
  const [copied, setCopied] = useState(false);

  const runQuery = useQuery({
    queryKey: ["run", runId],
    queryFn: () => api.run(Number(runId)),
    retry: false,
  });

  const run = runQuery.data?.run;
  const tests = runQuery.data?.tests ?? [];
  const base = useMemo(() => tests, [tests]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return base.filter((test) => {
      if (filter !== "all" && test.status !== filter) return false;
      if (!needle) return true;
      return [test.test, test.test_case_id ?? "", test.error ?? ""]
        .join(" ")
        .toLowerCase()
        .includes(needle);
    });
  }, [base, filter, query]);

  const active =
    filtered.find((test) => test.test === selected) ??
    filtered.find((test) => test.status === "failed") ??
    filtered[0];
  const passRate = run && run.total > 0 ? Math.round((run.passed / run.total) * 100) : 0;
  const failedTests = base.filter((test) => test.status === "failed");

  async function copyCommand() {
    if (failedTests.length === 0) return;
    // TC IDs are short and stable; fall back to the test title when a test has none.
    const patterns = failedTests.map((test) =>
      test.test_case_id ? `${test.test_case_id}\\b` : escapeRegex(parts(test.test).title),
    );
    const command = `npx playwright test --grep "${patterns.join("|")}"`;
    await navigator.clipboard.writeText(command);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  if (runQuery.isPending) {
    return <State>Loading run…</State>;
  }
  if (runQuery.isError || !run) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load run #{runId}</div>
        <div className="mt-1 text-xs">
          {runQuery.error instanceof Error ? runQuery.error.message : "Run not found."}
        </div>
        <Link to="/runs" className="mt-3 inline-block text-xs text-brand hover:underline">
          ← Back to runs
        </Link>
      </State>
    );
  }

  return (
    <>
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <Link to="/runs" className="inline-flex items-center gap-1 text-[11px] text-faint hover:text-ink">
            <ArrowLeft className="h-3 w-3" /> Runs
          </Link>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <h1 className="text-lg font-semibold">Run #{run.id}</h1>
            <Badge tone={run.source === "ci" ? "brand" : "neutral"}>{run.source || "local"}</Badge>
            {run.failed > 0 ? (
              <Badge tone="backend">FAILED</Badge>
            ) : run.flaky > 0 ? (
              <Badge tone="env">FLAKY</Badge>
            ) : (
              <Badge tone="flaky">PASSED</Badge>
            )}
          </div>
          <div className="mt-1 text-[12px] text-muted">
            {when(run.created_at)} · {duration(run.duration_ms)} · triggered by{" "}
            {run.triggered_by || "—"}
          </div>
          <div className="mt-1 break-all font-mono text-[11px] text-faint">
            {run.branch || "—"} · {run.commit ? run.commit.slice(0, 10) : "no commit"} ·{" "}
            {run.app_version || "no app version"} · Playwright {run.playwright_version || "—"}
          </div>
        </div>
        <div className="flex flex-wrap gap-1.5">
          {run.ci_url && (
            <Button asChild>
              <a href={run.ci_url} target="_blank" rel="noreferrer">
                Open CI <ExternalLink className="h-3 w-3" />
              </a>
            </Button>
          )}
          <Button
            variant="default"
            onClick={copyCommand}
            disabled={failedTests.length === 0}
            title={
              failedTests.length === 0
                ? "This run has no failed test"
                : "Copy a Playwright command that reruns the failed tests"
            }
          >
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            {copied
              ? "Copied"
              : failedTests.length === 0
                ? "No failed tests"
                : "Copy failed command"}
          </Button>
        </div>
      </div>

      <section className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-6">
        <Kpi label="Total" value={String(run.total)} hint="tests" />
        <Kpi label="Passed" value={String(run.passed)} hint={`${passRate}% pass rate`} tone="ok" />
        <Kpi label="Failed" value={String(run.failed)} hint="test results" tone="danger" />
        <Kpi label="Flaky" value={String(run.flaky)} hint="passed on retry" tone="warn" />
        <Kpi label="Skipped" value={String(run.skipped)} hint="not run" />
        <Kpi label="Duration" value={duration(run.duration_ms)} hint="whole run" />
      </section>

      {run.report_errors.length > 0 && (
        <div className="rounded-[10px] border border-danger/40 bg-danger/10 p-3 text-xs text-[#ff7b72]">
          <div className="font-semibold">Report errors ({run.report_errors.length})</div>
          {run.report_errors.map((error) => (
            <div key={error} className="mt-1 font-mono text-[11px]">
              {error}
            </div>
          ))}
        </div>
      )}

      <section className="flex flex-wrap items-center gap-2 rounded-[10px] border border-line bg-surface px-2.5 py-2">
        {filters.map((item) => {
          const count =
            item === "all" ? base.length : base.filter((test) => test.status === item).length;
          return (
            <button
              key={item}
              type="button"
              onClick={() => setFilter(item)}
              className={cn(
                "rounded-full border px-2.5 py-1 text-xs capitalize whitespace-nowrap transition-colors",
                filter === item
                  ? "border-[#1f6feb66] bg-[#1f6feb22] text-[#79c0ff]"
                  : "border-line bg-[#0f141a] text-muted hover:text-ink",
              )}
            >
              {item} <span className="ml-1 text-faint">{count}</span>
            </button>
          );
        })}
        <div className="flex w-full min-w-0 items-center gap-1.5 rounded-md border border-line bg-[#0f141a] px-2.5 py-1 text-faint sm:ml-auto sm:w-auto sm:min-w-[220px]">
          <Search className="h-3.5 w-3.5" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search test or TC ID…"
            className="h-6 border-0 bg-transparent px-0 focus-visible:ring-0"
            aria-label="Search tests"
          />
        </div>
      </section>

      <div className="grid items-start gap-3.5 lg:grid-cols-[minmax(0,1fr)_390px]">
        <div className="overflow-hidden rounded-[10px] border border-line bg-surface">
          <div className="grid grid-cols-[80px_92px_minmax(0,1fr)_80px_60px_110px] gap-2 border-b border-line bg-[#12171e] px-3 py-2.5 text-[10px] uppercase tracking-[0.7px] text-faint max-lg:hidden">
            <span>Status</span>
            <span>TC ID</span>
            <span>Test</span>
            <span>Duration</span>
            <span>Retry</span>
            <span>Category</span>
          </div>

          {filtered.length === 0 && (
            <div className="px-4 py-10 text-center text-sm text-muted">
              No test matches this filter.
            </div>
          )}

          {filtered.map((test) => {
            const info = parts(test.test);
            const category = categoryOf(test);
            return (
              <button
                key={test.test}
                type="button"
                onClick={() => setSelected(test.test)}
                className={cn(
                  "grid w-full grid-cols-1 gap-1.5 border-b border-line-soft px-3 py-2.5 text-left text-xs transition-colors last:border-b-0 hover:bg-[#1b222b] lg:grid-cols-[80px_92px_minmax(0,1fr)_80px_60px_110px] lg:items-center lg:gap-2",
                  active?.test === test.test &&
                    "bg-[#1b2430] shadow-[inset_3px_0_0_var(--color-brand)]",
                )}
              >
                <Status status={test.status} />
                <span className="truncate font-mono text-[11px] text-muted">
                  {test.test_case_id || "—"}
                </span>
                <span className="min-w-0">
                  <span className="block truncate text-[13px] text-ink">{info.title}</span>
                  <span className="block truncate font-mono text-[10.5px] text-faint">
                    {info.file}
                  </span>
                </span>
                <span className="font-mono text-[11px] text-muted">{duration(test.duration_ms)}</span>
                <span className="font-mono text-[11px] text-faint">{test.retries}</span>
                <Badge tone={categoryTone[category]}>
                  {category === "unclear" ? "—" : categoryLabel[category]}
                </Badge>
              </button>
            );
          })}
        </div>

        {active ? (
          <TestDetail result={active} />
        ) : (
          <aside className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">
            No test selected.
          </aside>
        )}
      </div>

      <p className="text-[11px] text-faint">
        <span className="font-mono">GET /api/runs/{run.id}</span>. Trace and screenshots are paths
        from the runner, not served by Redline.
      </p>
    </>
  );
}

function TestDetail({ result }: { result: ApiRunResult }) {
  const category = categoryOf(result);
  const info = parts(result.test);
  const [copied, setCopied] = useState(false);

  async function copyError() {
    await navigator.clipboard.writeText(result.error || result.error_snippet || "");
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  return (
    <aside className="overflow-hidden rounded-[10px] border border-line bg-surface lg:sticky lg:top-4">
      <div className="rounded-t-[10px] border-b border-line bg-[#12171e] p-4">
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <Status status={result.status} />
          {result.test_case_id && <Badge tone="brand">{result.test_case_id}</Badge>}
          <Badge tone={categoryTone[category]}>
            {category === "unclear" ? "UNANALYZED" : categoryLabel[category]}
          </Badge>
        </div>
        <h2 className="text-sm font-semibold leading-snug">{info.title}</h2>
        <div className="mt-1 break-words font-mono text-[11px] text-faint">
          {info.file} · {duration(result.duration_ms)} · retries {result.retries}
        </div>
        <div className="mt-3 flex flex-wrap gap-1.5">
          <Button variant="default" onClick={copyError}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            {copied ? "Copied" : "Copy error"}
          </Button>
          {result.fingerprint && (
            <Button asChild>
              <a href={`/ui/?failure=${encodeURIComponent(result.fingerprint)}`}>
                Failure history <ExternalLink className="h-3 w-3" />
              </a>
            </Button>
          )}
        </div>
      </div>

      <div className="flex flex-col gap-3.5 p-4">
        <div>
          <BlockLabel>Error</BlockLabel>
          <pre className="max-h-64 overflow-auto rounded-md border border-line-soft bg-[#0b0f14] p-2 font-mono text-[10.5px] leading-relaxed whitespace-pre-wrap text-[#adbac7]">
            {result.error || result.error_snippet || "No error stored for this test."}
          </pre>
        </div>
        <div className="flex flex-col gap-1">
          <BlockLabel>Details</BlockLabel>
          <Fact label="Fingerprint" value={result.fingerprint || "—"} />
          <Fact label="Location" value={result.error_location || "—"} />
          <Fact label="Trace" value={result.trace_path || "—"} />
          <Fact label="Screenshots" value={String(result.screenshots.length)} />
          <Fact label="Confidence" value={result.confidence || "—"} />
        </div>
      </div>
    </aside>
  );
}

function Status({ status }: { status: string }) {
  const style =
    status === "passed"
      ? "text-ok"
      : status === "failed"
        ? "text-[#ff7b72]"
        : status === "flaky"
          ? "text-warn"
          : "text-faint";
  return (
    <span className={cn("inline-flex items-center gap-1.5 text-[10.5px] font-bold tracking-wider", style)}>
      {status === "passed" ? (
        <Check className="h-3 w-3" />
      ) : status === "failed" ? (
        <X className="h-3 w-3" />
      ) : (
        <span className="h-1.5 w-1.5 rounded-full bg-current" />
      )}
      {status.toUpperCase()}
    </span>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline gap-2 text-[11px]">
      <span className="w-[76px] shrink-0 text-faint">{label}</span>
      <span className="min-w-0 break-all font-mono text-[10.5px] text-[#cdd9e5]">{value}</span>
    </div>
  );
}

function BlockLabel({ children }: { children: ReactNode }) {
  return <div className="mb-1.5 text-[10px] uppercase tracking-[0.8px] text-faint">{children}</div>;
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
    <Card className="px-3 py-2.5">
      <div className="text-[11px] text-muted">{label}</div>
      <div
        className={cn(
          "mt-0.5 text-xl font-semibold tracking-tight",
          tone === "ok" && "text-[#56d364]",
          tone === "danger" && "text-[#ff7b72]",
          tone === "warn" && "text-warn",
        )}
      >
        {value}
      </div>
      <div className="text-[10.5px] text-faint">{hint}</div>
    </Card>
  );
}

function State({ children }: { children: ReactNode }) {
  return <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>;
}
