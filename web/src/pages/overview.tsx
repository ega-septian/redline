import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Bell,
  GitBranch,
  Layers,
  ShieldCheck,
  Sparkles,
  type LucideIcon,
} from "lucide-react";

import { api } from "@/api/client";
import type { ApiQuarantineSuggestion, ApiRun } from "@/api/types";
import { CoverageTrend } from "@/components/coverage-trend";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { contractSourceHealth } from "@/lib/contract-source";
import { useProject } from "@/lib/project";
import {
  categoryLabel,
  statusLabel,
  toFailure,
  type Category,
  type FailureStatus,
} from "@/lib/failure";
import { cn } from "@/lib/utils";

const WINDOW_DAYS = 30;

type SectionLink = "/" | "/health" | "/runs" | "/config" | "/coverage";

const categoryTone: Record<Category, "backend" | "test" | "env" | "flaky" | "neutral"> = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
};

const statusTone: Record<FailureStatus, "brand" | "env" | "backend"> = {
  new: "brand",
  recurring: "env",
  regressed: "backend",
};

export function OverviewPage() {
  const { project } = useProject();
  const groupsQuery = useQuery({
    queryKey: ["groups", project],
    queryFn: () => api.groups(project),
  });
  const healthQuery = useQuery({
    queryKey: ["test-health", WINDOW_DAYS, project],
    queryFn: () => api.testHealth(WINDOW_DAYS, project),
  });
  const runsQuery = useQuery({
    queryKey: ["runs", project],
    queryFn: () => api.runs(project),
  });
  const coverageQuery = useQuery({
    queryKey: ["coverage", project],
    queryFn: () => api.coverage(project),
    enabled: project !== "",
  });
  const suggestionsQuery = useQuery({
    queryKey: ["quarantine-suggestions", WINDOW_DAYS, project],
    queryFn: () => api.quarantineSuggestions(WINDOW_DAYS, project),
  });
  const configQuery = useQuery({ queryKey: ["config"], queryFn: api.config });

  if (project === "") {
    return <State>Select a project in the sidebar to see its overview.</State>;
  }
  const loading = groupsQuery.isPending || healthQuery.isPending || runsQuery.isPending;
  const error = groupsQuery.error ?? healthQuery.error ?? runsQuery.error;

  if (loading) {
    return <State>Loading overview…</State>;
  }
  if (error) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load the overview</div>
        <div className="mt-1 text-xs">
          {error instanceof Error ? error.message : "The request failed."}
        </div>
        <button
          type="button"
          onClick={() => {
            void groupsQuery.refetch();
            void healthQuery.refetch();
            void runsQuery.refetch();
          }}
          className="mt-3 text-[11px] text-brand hover:underline"
        >
          Retry
        </button>
      </State>
    );
  }

  const groups = groupsQuery.data ?? [];
  const health = healthQuery.data;
  const summary = health?.summary;
  const runs = runsQuery.data ?? [];
  const coverage = coverageQuery.data;
  const suggestions = suggestionsQuery.data ?? [];
  const config = configQuery.data;
  const projectSources = config?.contracts.filter((source) => source.project === project) ?? [];
  const contractsHealth = contractSourceHealth(projectSources);
  const failures = [...groups]
    .sort(
      (left, right) =>
        right.occurrences - left.occurrences ||
        new Date(right.last_seen_at).getTime() - new Date(left.last_seen_at).getTime(),
    )
    .slice(0, 3)
    .map((group) => toFailure(group));
  const recentRuns = runs.slice(0, 5);
  const coverageRatio =
    coverage?.available && coverage.summary.documented > 0
      ? Math.round((coverage.summary.covered / coverage.summary.documented) * 100)
      : null;

  return (
    <>
      <section className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
        <Kpi
          label="Open failures"
          value={String(groups.length)}
          hint={
            groups.length === 0
              ? "all clear"
              : `${summary?.new_failures ?? 0} new in ${WINDOW_DAYS}d`
          }
          tone={groups.length > 0 ? "danger" : "ok"}
        />
        <Kpi
          label="Flake rate"
          value={summary ? pct(summary.flake_rate) : "—"}
          hint={`${summary?.flaky ?? 0} flaky ${(summary?.flaky ?? 0) === 1 ? "result" : "results"}`}
          tone="warn"
        />
        <Kpi label="Runs" value={String(summary?.runs ?? 0)} hint={`last ${WINDOW_DAYS} days`} />
        <Kpi
          label="Coverage"
          value={
            coverage?.available ? `${coverage.summary.covered}/${coverage.summary.documented}` : "—"
          }
          hint={coverageRatio === null ? "no contract snapshot" : `${coverageRatio}% of contract`}
          tone={coverageRatio !== null && coverageRatio >= 50 ? "ok" : "warn"}
        />
        <Kpi
          label="MTTR"
          value={summary && summary.mttr_hours > 0 ? `${summary.mttr_hours.toFixed(1)} h` : "—"}
          hint="mean time to resolve"
        />
      </section>

      <section className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card className="overflow-hidden">
          <SectionHeader
            icon={AlertTriangle}
            title="Needs attention"
            meta={`${groups.length} open`}
            to="/"
            action="View failures"
          />
          {failures.length === 0 ? (
            <Empty
              icon={ShieldCheck}
              title="No open failures"
              hint="The latest runs are clean."
              to="/runs"
              action="View runs"
            />
          ) : (
            failures.map((failure) => (
              <a
                key={failure.fingerprint}
                href={`/?failure=${encodeURIComponent(failure.fingerprint)}`}
                className="flex items-center gap-2.5 border-b border-line-soft px-3.5 py-2.5 text-[11.5px] last:border-b-0 hover:bg-[#1b222b]"
              >
                <Badge tone={statusTone[failure.status]}>{statusLabel[failure.status]}</Badge>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[12px] text-ink">{shortTitle(failure.test)}</div>
                  <div className="mt-0.5 flex flex-wrap gap-2 text-[10.5px] text-faint">
                    {failure.testCaseId && <span className="font-mono">{failure.testCaseId}</span>}
                    <span>{failure.occurrences}×</span>
                    <span>{ago(failure.lastSeenAt)}</span>
                    {failure.triageOwner && <span>owner {failure.triageOwner}</span>}
                  </div>
                </div>
                <Badge tone={categoryTone[failure.category]}>
                  {categoryLabel[failure.category]}
                </Badge>
              </a>
            ))
          )}
        </Card>

        <Card className="overflow-hidden">
          <SectionHeader
            icon={Sparkles}
            title="Suggested quarantines"
            meta={`${suggestions.length} candidate${suggestions.length === 1 ? "" : "s"}`}
            to="/health"
            action="View health"
          />
          {suggestions.length === 0 ? (
            <Empty
              icon={ShieldCheck}
              title="No quarantine suggestions"
              hint="Recent runs are stable enough."
              to="/health"
              action="Open Health"
            />
          ) : (
            suggestions
              .slice(0, 3)
              .map((suggestion) => <SuggestionRow key={suggestion.key} suggestion={suggestion} />)
          )}
        </Card>
      </section>

      <section className="grid grid-cols-1 items-start gap-3 xl:grid-cols-[minmax(0,1.35fr)_minmax(0,1fr)]">
        <div className="flex min-w-0 flex-col gap-3">
          {coverage?.available && (coverage.history?.length ?? 0) > 0 ? (
            <CoverageTrend points={coverage.history ?? []} />
          ) : (
            <Card className="overflow-hidden">
              <SectionHeader
                icon={Layers}
                title="Coverage trend"
                to="/coverage"
                action="Open Coverage"
              />
              <Empty
                icon={Layers}
                title={coverage?.available ? "Not enough runs for a trend" : "No contract snapshot"}
                hint={
                  coverage?.available
                    ? "Send another run to compare coverage."
                    : "Add a contract source in Config, then send a run."
                }
                to="/config"
                action="Open Config"
              />
            </Card>
          )}

          <Card className="overflow-hidden">
            <SectionHeader
              icon={ShieldCheck}
              title="System status"
              to="/config"
              action="Open Config"
            />
            <div className="grid gap-3 p-3.5 sm:grid-cols-3">
              <Status
                icon={Bell}
                label="Discord"
                value={
                  config?.notifications.enabled
                    ? "enabled"
                    : config?.notifications.webhook_set
                      ? "webhook set, disabled"
                      : "not configured"
                }
                hint={
                  config?.notifications.last_delivery
                    ? lastDeliveryLabel(config.notifications.last_delivery)
                    : "no delivery yet"
                }
              />
              <Status
                icon={Layers}
                label="Contract sources"
                value={
                  projectSources.length === 0
                    ? "none"
                    : `${contractsHealth.valid}/${projectSources.length} valid`
                }
                hint={
                  projectSources.length === 0
                    ? "add a source in Config"
                    : contractsHealth.invalid > 0
                      ? `${contractsHealth.invalid} invalid`
                      : contractsHealth.stale > 0
                        ? `${contractsHealth.stale} stale`
                        : contractsHealth.latest
                          ? `last checked ${ago(contractsHealth.latest)}`
                          : "no checks yet"
                }
                tone={
                  contractsHealth.invalid > 0
                    ? "danger"
                    : contractsHealth.stale > 0
                      ? "warn"
                      : undefined
                }
              />
              <Status
                icon={Activity}
                label="Coverage"
                value={
                  coverage?.available
                    ? `${coverage.summary.covered}/${coverage.summary.documented}`
                    : "no snapshot"
                }
                hint={
                  coverage?.snapshot
                    ? `latest ${when(coverage.snapshot.at)}`
                    : "add a contract source"
                }
              />
            </div>
          </Card>
        </div>

        <Card className="overflow-hidden">
          <SectionHeader icon={GitBranch} title="Recent runs" to="/runs" action="View runs" />
          {recentRuns.length === 0 ? (
            <Empty
              icon={GitBranch}
              title="No runs yet"
              hint="Send your first Playwright run to fill the dashboard."
              to="/runs"
              action="Open Runs"
            />
          ) : (
            recentRuns.map((run) => <RunRow key={run.id} run={run} />)
          )}
        </Card>
      </section>
    </>
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
  tone?: "ok" | "warn" | "danger";
}) {
  return (
    <Card className="px-3.5 py-3">
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

function SectionHeader({
  icon: Icon,
  title,
  meta,
  to,
  action,
}: {
  icon: LucideIcon;
  title: string;
  meta?: string;
  to: SectionLink;
  action: string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
      <Icon className="h-3.5 w-3.5 text-brand" />
      <span className="text-[12.5px] font-semibold">{title}</span>
      {meta && <Badge tone="neutral">{meta}</Badge>}
      <Link to={to} className="ml-auto text-[11px] text-brand hover:underline">
        {action} <ArrowRight className="inline h-3 w-3" />
      </Link>
    </div>
  );
}

function SuggestionRow({ suggestion }: { suggestion: ApiQuarantineSuggestion }) {
  return (
    <Link
      to="/health"
      className="flex items-center gap-2.5 border-b border-line-soft px-3.5 py-2.5 last:border-b-0 hover:bg-[#1b222b]"
    >
      <Badge tone={suggestion.score === "high" ? "backend" : "env"}>{suggestion.score}</Badge>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-[12px] text-ink">{shortTitle(suggestion.test)}</span>
          {suggestion.test_case_id && (
            <span className="shrink-0 font-mono text-[10px] text-faint">
              {suggestion.test_case_id}
            </span>
          )}
        </div>
        <div className="mt-0.5 truncate text-[10.5px] text-faint" title={suggestion.reason}>
          {suggestion.reason}
        </div>
      </div>
      <ArrowRight className="h-3.5 w-3.5 shrink-0 text-faint" />
    </Link>
  );
}

function RunRow({ run }: { run: ApiRun }) {
  const tone = run.failed > 0 ? "bg-danger" : run.flaky > 0 ? "bg-warn" : "bg-ok";
  return (
    <Link
      to="/runs/$runId"
      params={{ runId: String(run.id) }}
      className="flex items-center gap-2.5 border-b border-line-soft px-3.5 py-2.5 text-[11px] last:border-b-0 hover:bg-[#1b222b]"
    >
      <span className={cn("h-[7px] w-[7px] shrink-0 rounded-full", tone)} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="font-mono text-[11px] text-ink">#{run.id}</span>
          <span className="truncate text-[10.5px] text-faint">
            {run.branch || "—"} · {run.source || "local"}
          </span>
        </div>
        <div className="mt-0.5 text-[10px] text-faint">{when(run.created_at)}</div>
      </div>
      <div
        className="flex shrink-0 gap-2 font-mono text-[10px]"
        title={`${run.passed} passed · ${run.failed} failed · ${run.flaky} flaky`}
        aria-label={`${run.passed} passed, ${run.failed} failed, ${run.flaky} flaky`}
      >
        <span className="text-ok">{run.passed}p</span>
        <span className={run.failed > 0 ? "text-danger" : "text-faint"}>{run.failed}f</span>
        <span className={run.flaky > 0 ? "text-warn" : "text-faint"}>{run.flaky}fl</span>
      </div>
    </Link>
  );
}

function Status({
  icon: Icon,
  label,
  value,
  hint,
  tone,
}: {
  icon: LucideIcon;
  label: string;
  value: string;
  hint: string;
  tone?: "warn" | "danger";
}) {
  return (
    <div className="flex items-start gap-2">
      <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0 text-faint" />
      <div className="min-w-0">
        <div className="text-[10px] uppercase tracking-[0.7px] text-faint">{label}</div>
        <div className="mt-0.5 truncate text-[12px] text-ink">{value}</div>
        <div
          className={cn(
            "mt-0.5 truncate text-[10.5px]",
            tone === "danger" ? "text-danger" : tone === "warn" ? "text-warn" : "text-faint",
          )}
        >
          {hint}
        </div>
      </div>
    </div>
  );
}

function Empty({
  icon: Icon,
  title,
  hint,
  to,
  action,
}: {
  icon: LucideIcon;
  title: string;
  hint: string;
  to: SectionLink;
  action: string;
}) {
  return (
    <div className="flex flex-col items-start gap-1 px-3.5 py-5">
      <Icon className="h-4 w-4 text-faint" />
      <div className="text-[12px] text-ink">{title}</div>
      <div className="text-[10.5px] text-faint">{hint}</div>
      <Link to={to} className="mt-1 text-[10.5px] text-brand hover:underline">
        {action} →
      </Link>
    </div>
  );
}

function State({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">
      {children}
    </div>
  );
}

function shortTitle(test: string): string {
  return test.split(" › ").slice(2).join(" › ") || test;
}

function pct(value: number): string {
  return `${Math.round(value * 100)}%`;
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

function ago(iso: string): string {
  const date = new Date(iso).getTime();
  if (Number.isNaN(date)) return "—";
  const minutes = Math.floor((Date.now() - date) / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function lastDeliveryLabel(delivery: {
  at: string;
  ok: boolean;
  attempts: number;
  status_code?: number;
}): string {
  const status = delivery.ok ? "sent" : "failed";
  const attempts = delivery.attempts > 1 ? ` · ${delivery.attempts} attempts` : "";
  const code = !delivery.ok && delivery.status_code ? ` · HTTP ${delivery.status_code}` : "";
  return `${status} ${ago(delivery.at)}${attempts}${code}`;
}
