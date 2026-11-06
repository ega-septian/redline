import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Layers, Search, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type { ApiCoverage, ApiCoverageChange, ApiCoverageEndpoint } from "@/api/types";
import { CoverageTrend } from "@/components/coverage-trend";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useProject } from "@/lib/project";
import { cn } from "@/lib/utils";

type CoverageFilter = "all" | "covered" | "uncovered";

export function CoveragePage() {
  const { project } = useProject();
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<CoverageFilter>("all");
  const [selected, setSelected] = useState<string | null>(null);

  const coverageQuery = useQuery({
    queryKey: ["coverage", project],
    queryFn: () => api.coverage(project),
    enabled: project !== "",
  });
  const coverage = coverageQuery.data;

  const endpoints = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return (coverage?.endpoints ?? []).filter((endpoint) => {
      if (filter === "covered" && !endpoint.covered) return false;
      if (filter === "uncovered" && endpoint.covered) return false;
      if (!needle) return true;
      return `${endpoint.method} ${endpoint.path} ${endpoint.summary ?? ""}`.toLowerCase().includes(needle);
    });
  }, [coverage, filter, search]);

  const selectedEndpoint = useMemo(
    () => endpoints.find((endpoint) => `${endpoint.method} ${endpoint.path}` === selected) ?? null,
    [endpoints, selected],
  );

  if (project === "") {
    return <State>Select a project in the sidebar to see its contract coverage.</State>;
  }
  if (coverageQuery.isPending) return <State>Loading contract coverage…</State>;
  if (coverageQuery.isError) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load contract coverage</div>
        <div className="mt-1 text-xs">
          {coverageQuery.error instanceof Error ? coverageQuery.error.message : "The API request failed."}
        </div>
        <Button className="mt-3" onClick={() => coverageQuery.refetch()}>
          Retry
        </Button>
      </State>
    );
  }
  if (!coverage?.available) {
    return (
      <>
        <State>
          <div className="font-semibold text-ink">No contract snapshot for {coverage?.project || project}</div>
          <p className="mt-1 text-xs leading-relaxed">
            {coverage?.message ||
              "Add a contract source in Config, then send a run so Redline can store the contract."}
          </p>
          <p className="mt-2 text-[11px] text-faint">
            The URL must be reachable from the Redline server, not only from your browser. For Docker,
            use <span className="font-mono">host.docker.internal</span> for a service on the host.
          </p>
          <Link to="/config" className="mt-3 inline-block text-[11px] text-brand hover:underline">
            Open Config →
          </Link>
        </State>
      </>
    );
  }

  const summary = coverage.summary;
  const documentedRatio = summary.documented === 0 ? "—" : `${summary.covered}/${summary.documented}`;
  const statusRatio =
    summary.status_codes_documented === 0
      ? "—"
      : `${summary.status_codes_seen}/${summary.status_codes_documented}`;

  return (
    <>
      <div className="flex flex-wrap items-center gap-2 rounded-[10px] border border-line bg-surface px-2.5 py-2">
        <Layers className="h-3.5 w-3.5 text-brand" />
        <span className="text-[11px] text-faint">
          Latest snapshot <b className="text-muted">run #{coverage.snapshot?.run_id}</b>
          {coverage.previous && (
            <>
              {" "}· compared with <b className="text-muted">run #{coverage.previous.run_id}</b>
            </>
          )}
        </span>
        <select
          value={filter}
          onChange={(event) => setFilter(event.target.value as CoverageFilter)}
          aria-label="Coverage filter"
          className="h-7 rounded-md border border-line bg-canvas px-2 text-[11px] text-ink outline-none focus-visible:border-brand/60"
        >
          <option value="all">All endpoints</option>
          <option value="covered">Covered</option>
          <option value="uncovered">Uncovered</option>
        </select>
        <div className="ml-auto flex min-w-[180px] flex-1 items-center gap-1.5 rounded-md border border-line bg-canvas px-2.5 text-faint sm:max-w-xs">
          <Search className="h-3.5 w-3.5" />
          <Input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search path or method…"
            aria-label="Search endpoints"
            className="h-7 border-0 bg-transparent px-0 text-[11px] focus-visible:ring-0"
          />
        </div>
      </div>

      <section className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
        <Kpi label="Documented endpoints" value={String(summary.documented)} hint="from the latest contract" />
        <Kpi
          label="Covered"
          value={documentedRatio}
          hint={summary.documented ? `${Math.round((summary.covered / summary.documented) * 100)}% of contract` : "—"}
          tone="ok"
        />
        <Kpi label="Uncovered" value={String(summary.uncovered)} hint="never called by a test" tone="danger" />
        <Kpi
          label="Status codes"
          value={statusRatio}
          hint="documented responses seen"
          tone={summary.status_codes_seen === summary.status_codes_documented ? "ok" : "warn"}
        />
        <Kpi label="Contract changes" value={String(summary.changes)} hint="since the previous snapshot" tone="warn" />
      </section>

      <CoverageTrend points={coverage.history ?? []} />

      <div className="grid grid-cols-1 gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
        <div className="min-w-0 overflow-hidden rounded-[10px] border border-line bg-surface">
          <div className="flex items-center justify-between border-b border-line bg-[#12171e] px-3.5 py-2.5">
            <span className="text-[10.5px] uppercase tracking-[0.7px] text-faint">
              Endpoints · {endpoints.length} shown
            </span>
            <span className="text-[10.5px] text-faint">
              {summary.documented} documented · {summary.uncovered} uncovered
            </span>
          </div>
          <div className="overflow-x-auto">
            <div className="min-w-[720px]">
              <div className="grid grid-cols-[minmax(220px,1fr)_82px_92px_210px_54px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2 text-[9.5px] uppercase tracking-[0.7px] text-faint">
                <span>Endpoint</span>
                <span>Covered</span>
                <span>Last tested</span>
                <span>Status codes</span>
                <span className="text-right">Tests</span>
              </div>
              {endpoints.length === 0 ? (
                <div className="px-4 py-10 text-center text-sm text-muted">
                  No endpoint matches this filter.
                </div>
              ) : (
                endpoints.map((endpoint) => {
                  const key = `${endpoint.method} ${endpoint.path}`;
                  return (
                    <EndpointRow
                      key={key}
                      endpoint={endpoint}
                      active={selected === key}
                      onSelect={() => setSelected(selected === key ? null : key)}
                    />
                  );
                })
              )}
            </div>
          </div>
        </div>

        {selectedEndpoint ? (
          <EndpointDetail endpoint={selectedEndpoint} onClose={() => setSelected(null)} />
        ) : (
          <ContractChanges coverage={coverage} />
        )}
      </div>

      <p className="text-[11px] text-faint">
        Coverage uses the latest contract snapshot and the calls recorded by the Redline fixture. A documented
        status code with no observation is gray; an undocumented call is not counted. Retention limits how far
        back coverage can see.
      </p>
    </>
  );
}

function EndpointRow({
  endpoint,
  active,
  onSelect,
}: {
  endpoint: ApiCoverageEndpoint;
  active: boolean;
  onSelect: () => void;
}) {
  const observed = new Set(endpoint.observed_status_codes);
  return (
    <button
      type="button"
      onClick={onSelect}
      title="Show endpoint details"
      className={cn(
        "grid w-full grid-cols-[minmax(220px,1fr)_82px_92px_210px_54px] gap-3 border-b border-line-soft px-3.5 py-2.5 text-left text-[11px] last:border-b-0 hover:bg-[#1b222b]",
        active && "bg-[#1f6feb14]",
      )}
    >
      <div className="flex min-w-0 items-center gap-2">
        <span className={cn("w-11 shrink-0 font-mono text-[10px] font-bold", methodClass(endpoint.method))}>
          {endpoint.method}
        </span>
        <span className="truncate font-mono text-[11px] text-[#cdd9e5]">{endpoint.path}</span>
      </div>
      <span className={cn("inline-flex items-center gap-1.5", endpoint.covered ? "text-ok" : "text-danger")}>
        <i className={cn("h-1.5 w-1.5 rounded-full", endpoint.covered ? "bg-ok" : "bg-danger")} />
        {endpoint.covered ? "covered" : "uncovered"}
      </span>
      <span className="text-[10.5px] text-faint">{endpoint.last_tested_at ? when(endpoint.last_tested_at) : "—"}</span>
      <span className="flex flex-wrap gap-1">
        {endpoint.status_codes.length === 0 ? (
          <span className="text-faint">—</span>
        ) : (
          endpoint.status_codes.map((code) => (
            <span
              key={code}
              className={cn(
                "rounded border px-1 py-0.5 font-mono text-[9.5px]",
                observed.has(code)
                  ? "border-ok/40 bg-ok/10 text-ok"
                  : "border-line-soft bg-canvas text-faint",
              )}
              title={observed.has(code) ? "seen in a stored run" : "documented but not seen"}
            >
              {code}
            </span>
          ))
        )}
      </span>
      <span className="text-right font-mono text-muted">{endpoint.tests}</span>
    </button>
  );
}

function EndpointDetail({ endpoint, onClose }: { endpoint: ApiCoverageEndpoint; onClose: () => void }) {
  const observed = new Set(endpoint.observed_status_codes);
  const tests = endpoint.test_keys ?? [];
  return (
    <Card className="overflow-hidden xl:sticky xl:top-4 xl:self-start">
      <div className="flex items-start gap-2 border-b border-line bg-[#12171e] p-3.5">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <span className={cn("shrink-0 font-mono text-[11px] font-bold", methodClass(endpoint.method))}>
              {endpoint.method}
            </span>
            <span className="break-all font-mono text-[11px] text-ink">{endpoint.path}</span>
          </div>
          {endpoint.summary && <p className="mt-1 text-[11px] text-muted">{endpoint.summary}</p>}
        </div>
        <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close endpoint detail">
          <X className="h-4 w-4" />
        </Button>
      </div>

      <div className="p-3.5">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={endpoint.covered ? "flaky" : "backend"}>
            {endpoint.covered ? "covered" : "uncovered"}
          </Badge>
          <span className="text-[10.5px] text-faint">
            {endpoint.tests} test{endpoint.tests === 1 ? "" : "s"}
          </span>
          <span className="text-[10.5px] text-faint">
            last {endpoint.last_tested_at ? when(endpoint.last_tested_at) : "never"}
          </span>
        </div>

        <div className="mt-3">
          <div className="text-[10px] uppercase tracking-[0.8px] text-faint">Status codes</div>
          <div className="mt-1.5 flex flex-wrap gap-1">
            {endpoint.status_codes.length === 0 ? (
              <span className="text-[10.5px] text-faint">None documented.</span>
            ) : (
              endpoint.status_codes.map((code) => (
                <span
                  key={code}
                  className={cn(
                    "rounded border px-1 py-0.5 font-mono text-[9.5px]",
                    observed.has(code)
                      ? "border-ok/40 bg-ok/10 text-ok"
                      : "border-line-soft bg-canvas text-faint",
                  )}
                  title={observed.has(code) ? "seen in a stored run" : "documented but not seen"}
                >
                  {code}
                </span>
              ))
            )}
          </div>
        </div>

        <div className="mt-3">
          <div className="text-[10px] uppercase tracking-[0.8px] text-faint">Tests that called it</div>
          {tests.length === 0 ? (
            <p className="mt-1.5 text-[10.5px] text-faint">
              No recorded call yet. The contract lists this endpoint but no stored run called it.
            </p>
          ) : (
            <div className="mt-1.5 flex flex-col gap-1">
              {tests.map((test) => (
                <div key={test} className="break-all font-mono text-[10px] leading-relaxed text-muted">
                  {test}
                </div>
              ))}
            </div>
          )}
          {endpoint.tests > tests.length && (
            <p className="mt-1.5 text-[10px] text-faint">Showing the first {tests.length} of {endpoint.tests}.</p>
          )}
        </div>
      </div>
    </Card>
  );
}

function ContractChanges({ coverage }: { coverage: ApiCoverage }) {
  return (
    <Card className="overflow-hidden xl:sticky xl:top-4 xl:self-start">
      <div className="flex items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <span className="text-[12.5px] font-semibold">Contract changes</span>
        <span className="ml-auto text-[10.5px] text-faint">
          {coverage.previous ? `run #${coverage.snapshot?.run_id} vs #${coverage.previous.run_id}` : "latest"}
        </span>
      </div>
      <div className="border-b border-line bg-[#0f141a] px-3.5 py-2.5 text-[10.5px] leading-relaxed text-faint">
        <div>
          <b className="text-muted">Latest</b> · run #{coverage.snapshot?.run_id} · {coverage.snapshot ? when(coverage.snapshot.at) : "—"}
        </div>
        {coverage.previous && (
          <div>
            <b className="text-muted">Compared with</b> · run #{coverage.previous.run_id} · {when(coverage.previous.at)}
          </div>
        )}
      </div>
      {coverage.changes.length === 0 ? (
        <div className="px-3.5 py-6 text-xs text-muted">
          {coverage.previous
            ? "No endpoint or status-code changes between these snapshots."
            : "No previous contract snapshot to compare."}
        </div>
      ) : (
        <div>
          {coverage.changes.map((change, index) => (
            <ChangeRow key={`${change.kind}-${change.method}-${change.path}-${change.detail}-${index}`} change={change} />
          ))}
        </div>
      )}
      <div className="flex flex-wrap gap-2 border-t border-line px-3.5 py-2.5">
        {coverage.snapshot && (
          <Link
            to="/runs/$runId"
            params={{ runId: String(coverage.snapshot.run_id) }}
            className="text-[11px] text-brand hover:underline"
          >
            View run #{coverage.snapshot.run_id}
          </Link>
        )}
        {coverage.previous && (
          <Link
            to="/runs/$runId"
            params={{ runId: String(coverage.previous.run_id) }}
            className="text-[11px] text-faint hover:text-ink"
          >
            Previous run
          </Link>
        )}
      </div>
    </Card>
  );
}

function ChangeRow({ change }: { change: ApiCoverageChange }) {
  const tone = change.kind === "added" ? "added" : change.kind === "removed" ? "removed" : "changed";
  return (
    <div className="flex gap-2 border-b border-line-soft px-3.5 py-2.5 last:border-b-0">
      <span className={cn("mt-0.5 h-[18px] shrink-0 rounded border px-1.5 text-[9px] font-bold uppercase", changeTone[tone])}>
        {change.kind}
      </span>
      <div className="min-w-0">
        <div className="break-all font-mono text-[10.5px] text-ink">
          {change.method} {change.path}
        </div>
        <div className="mt-0.5 text-[10.5px] text-faint">{change.detail}</div>
      </div>
    </div>
  );
}

const changeTone = {
  added: "border-ok/40 bg-ok/10 text-ok",
  changed: "border-warn/40 bg-warn/10 text-warn",
  removed: "border-danger/40 bg-danger/10 text-danger",
} as const;

function Kpi({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: string;
  hint: string;
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
      <div className="mt-0.5 truncate text-[10px] text-faint">{hint}</div>
    </Card>
  );
}

function State({ children }: { children: ReactNode }) {
  return <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>;
}

function methodClass(method: string): string {
  switch (method) {
    case "GET":
      return "text-[#79c0ff]";
    case "POST":
      return "text-ok";
    case "PUT":
      return "text-warn";
    case "PATCH":
      return "text-violet";
    case "DELETE":
      return "text-[#ff7b72]";
    default:
      return "text-muted";
  }
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
