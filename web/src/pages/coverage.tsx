import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Check, Copy, EyeOff, Info, Layers, Search, Sparkles, X } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type {
  ApiCoverage,
  ApiCoverageChange,
  ApiCoverageEndpoint,
  ApiCoverageSuggestion,
} from "@/api/types";
import { CoverageTrend } from "@/components/coverage-trend";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useProject } from "@/lib/project";
import { cn } from "@/lib/utils";

type CoverageFilter = "all" | "covered" | "uncovered" | "ignored";
type GenerateTarget = { endpoint: ApiCoverageEndpoint; status: string };
type IgnoreTarget = GenerateTarget;
type SuggestTarget = {
  endpoint: ApiCoverageEndpoint;
  status: string;
  suggestion: ApiCoverageSuggestion;
};

// The generate command lands in Phase 2. Keep the dialog code ready, but do not offer a
// button that copies a command which does not exist yet.
const GENERATOR_READY = false;

export function CoveragePage() {
  const { project } = useProject();
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState<CoverageFilter>("all");
  const [selected, setSelected] = useState<string | null>(null);
  const [generateTarget, setGenerateTarget] = useState<GenerateTarget | null>(null);
  const [ignoreTarget, setIgnoreTarget] = useState<IgnoreTarget | null>(null);
  const [suggestTarget, setSuggestTarget] = useState<SuggestTarget | null>(null);
  const queryClient = useQueryClient();

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
      if (filter === "ignored" && endpoint.ignored_status_codes.length === 0) return false;
      if (!needle) return true;
      return `${endpoint.method} ${endpoint.path} ${endpoint.summary ?? ""}`.toLowerCase().includes(needle);
    });
  }, [coverage, filter, search]);

  const selectedEndpoint = useMemo(
    () => endpoints.find((endpoint) => `${endpoint.method} ${endpoint.path}` === selected) ?? null,
    [endpoints, selected],
  );

  const unignore = useMutation({
    mutationFn: (target: IgnoreTarget) =>
      api.unignoreCoverageStatus(project, target.endpoint.method, target.endpoint.path, target.status),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["coverage", project] }),
  });

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
  const statusTotal = Math.max(0, summary.status_codes_documented - summary.status_codes_ignored);
  const statusRatio =
    statusTotal === 0 ? "—" : `${summary.status_codes_seen}/${statusTotal}`;

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
          <option value="ignored">With ignored status</option>
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
          hint={`${summary.status_codes_ignored} ignored · documented responses seen`}
          tone={summary.status_codes_seen === statusTotal ? "ok" : "warn"}
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
                      onGenerate={(status) => setGenerateTarget({ endpoint, status })}
                      onIgnore={(status) => setIgnoreTarget({ endpoint, status })}
                      onUnignore={(status) => unignore.mutate({ endpoint, status })}
                    />
                  );
                })
              )}
            </div>
          </div>
        </div>

        {selectedEndpoint ? (
          <EndpointDetail
            endpoint={selectedEndpoint}
            onClose={() => setSelected(null)}
            onGenerate={(status) => setGenerateTarget({ endpoint: selectedEndpoint, status })}
            onIgnore={(status) => setIgnoreTarget({ endpoint: selectedEndpoint, status })}
            onUnignore={(status) => unignore.mutate({ endpoint: selectedEndpoint, status })}
            onSuggest={(status, suggestion) =>
              setSuggestTarget({ endpoint: selectedEndpoint, status, suggestion })
            }
          />
        ) : (
          <ContractChanges coverage={coverage} />
        )}
      </div>

      <p className="text-[11px] text-faint">
        Coverage uses the latest contract snapshot and the calls recorded by the Redline fixture. A documented
        status code with no observation is gray; an undocumented call is not counted. Retention limits how far
        back coverage can see.
      </p>

      {generateTarget && (
        <GenerateDialog
          key={`${generateTarget.endpoint.method}-${generateTarget.endpoint.path}-${generateTarget.status}`}
          target={generateTarget}
          onClose={() => setGenerateTarget(null)}
        />
      )}

      {ignoreTarget && (
        <IgnoreDialog
          key={`${ignoreTarget.endpoint.method}-${ignoreTarget.endpoint.path}-${ignoreTarget.status}`}
          project={project}
          target={ignoreTarget}
          onClose={() => setIgnoreTarget(null)}
          onIgnored={() => {
            setIgnoreTarget(null);
            queryClient.invalidateQueries({ queryKey: ["coverage", project] });
          }}
        />
      )}

      {suggestTarget && (
        <SuggestDialog
          key={`suggest-${suggestTarget.endpoint.method}-${suggestTarget.endpoint.path}-${suggestTarget.status}`}
          target={suggestTarget}
          onClose={() => setSuggestTarget(null)}
        />
      )}
    </>
  );
}

function GenerateDialog({
  target,
  onClose,
}: {
  target: GenerateTarget;
  onClose: () => void;
}) {
  const { endpoint, status } = target;
  const scenarios = scenariosFor(status);
  const [selected, setSelected] = useState<string[]>([]);
  const [copied, setCopied] = useState(false);
  const command = generateCommand(endpoint, status, selected);

  function toggle(scenario: string) {
    setSelected((current) =>
      current.includes(scenario) ? current.filter((item) => item !== scenario) : [...current, scenario],
    );
    setCopied(false);
  }

  async function copy() {
    await navigator.clipboard.writeText(command);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1800);
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/65 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="w-full max-w-lg overflow-hidden rounded-[10px] border border-line bg-surface shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="generate-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="flex items-start gap-2 border-b border-line bg-[#12171e] px-3.5 py-3">
          <Sparkles className="mt-0.5 h-4 w-4 shrink-0 text-brand" />
          <div className="min-w-0 flex-1">
            <h2 id="generate-title" className="text-[12.5px] font-semibold text-ink">
              Generate a test
            </h2>
            <p className="mt-0.5 break-all font-mono text-[10.5px] text-faint">
              {endpoint.method} {endpoint.path}{" "}
              <span className="rounded border border-line-soft px-1 py-0.5 text-[9.5px]">{status}</span>
            </p>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close generate dialog">
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="px-3.5 py-3">
          <p className="text-[11px] leading-relaxed text-muted">
            Choose the cases to include. The command targets the Phase 2 generator; it is copied to your
            clipboard, not executed here.
          </p>

          <div className="mt-3">
            <div className="text-[10px] uppercase tracking-[0.8px] text-faint">Suggested cases</div>
            <div className="mt-1.5 flex flex-col gap-1">
              {scenarios.map((scenario) => (
                <label
                  key={scenario}
                  className="flex cursor-pointer items-center gap-2 rounded-md border border-line-soft bg-canvas px-2 py-1.5 text-[11px] text-muted hover:border-[#3d444d]"
                >
                  <input
                    type="checkbox"
                    checked={selected.includes(scenario)}
                    onChange={() => toggle(scenario)}
                    className="h-3.5 w-3.5 accent-[#58a6ff]"
                  />
                  {scenario}
                </label>
              ))}
            </div>
          </div>

          <div className="mt-3">
            <div className="text-[10px] uppercase tracking-[0.8px] text-faint">Command</div>
            <pre className="mt-1.5 overflow-x-auto rounded-md border border-line-soft bg-[#0b0f14] p-2.5 font-mono text-[10.5px] leading-relaxed text-[#cdd9e5]">
              {command}
            </pre>
          </div>

          <div className="mt-3 flex items-center gap-2">
            <Button variant="default" onClick={copy}>
              {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
              {copied ? "Copied" : "Copy command"}
            </Button>
            <Button variant="ghost" onClick={onClose}>
              Close
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function SuggestDialog({ target, onClose }: { target: SuggestTarget; onClose: () => void }) {
  const { endpoint, status, suggestion } = target;
  const command = suggestion.command ?? "";
  const [copied, setCopied] = useState(false);

  async function copy() {
    await navigator.clipboard.writeText(command);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1800);
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/65 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="w-full max-w-lg overflow-hidden rounded-[10px] border border-line bg-surface shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="suggest-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="flex items-start gap-2 border-b border-line bg-[#12171e] px-3.5 py-3">
          <Sparkles className="mt-0.5 h-4 w-4 shrink-0 text-brand" />
          <div className="min-w-0 flex-1">
            <h2 id="suggest-title" className="text-[12.5px] font-semibold text-ink">
              Suggest a test
            </h2>
            <p className="mt-0.5 break-all font-mono text-[10.5px] text-faint">
              {endpoint.method} {endpoint.path}{" "}
              <span className="rounded border border-line-soft px-1 py-0.5 text-[9.5px]">{status}</span>
            </p>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close suggest dialog">
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="px-3.5 py-3">
          <p className="text-[11px] leading-relaxed text-muted">{suggestion.reason}</p>

          <div className="mt-3 grid grid-cols-2 gap-2">
            <SuggestionFact
              label="Request"
              value={`${suggestion.request?.method ?? endpoint.method} ${suggestion.request?.path ?? endpoint.path}`}
            />
            <SuggestionFact
              label="Expected"
              value={String(suggestion.expected_status ?? status)}
            />
            <SuggestionFact label="Confidence" value={suggestion.confidence} />
            <SuggestionFact label="Kind" value={suggestion.kind} />
          </div>

          {suggestion.body_fields && Object.keys(suggestion.body_fields).length > 0 && (
            <div className="mt-3">
              <div className="text-[10px] uppercase tracking-[0.8px] text-faint">Body fields</div>
              <div className="mt-1.5 flex flex-wrap gap-1">
                {Object.entries(suggestion.body_fields).map(([field, kind]) => (
                  <span
                    key={field}
                    className="rounded border border-line-soft bg-canvas px-1.5 py-0.5 font-mono text-[9.5px] text-muted"
                  >
                    {field}: {kind}
                  </span>
                ))}
              </div>
            </div>
          )}

          <div className="mt-3">
            <div className="text-[10px] uppercase tracking-[0.8px] text-faint">Command</div>
            <pre className="mt-1.5 overflow-x-auto rounded-md border border-line-soft bg-[#0b0f14] p-2.5 font-mono text-[10.5px] leading-relaxed text-[#cdd9e5]">
              {command || "No command for this suggestion."}
            </pre>
          </div>

          <p className="mt-2 text-[10px] leading-relaxed text-faint">
            Runs a temporary test against your project. If it passes, the call is recorded and this status
            becomes observed. No test file is written.
          </p>

          <div className="mt-3 flex items-center gap-2">
            <Button variant="default" onClick={copy} disabled={!command}>
              {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
              {copied ? "Copied" : "Copy command"}
            </Button>
            <Button variant="ghost" onClick={onClose}>
              Close
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function SuggestionFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-line-soft bg-canvas px-2 py-1.5">
      <div className="text-[9px] uppercase tracking-[0.7px] text-faint">{label}</div>
      <div className="mt-0.5 break-all font-mono text-[10px] text-muted">{value}</div>
    </div>
  );
}

function IgnoreDialog({
  project,
  target,
  onClose,
  onIgnored,
}: {
  project: string;
  target: IgnoreTarget;
  onClose: () => void;
  onIgnored: () => void;
}) {
  const { endpoint, status } = target;
  const [reason, setReason] = useState(defaultIgnoreReason(status));
  const mutation = useMutation({
    mutationFn: () =>
      api.ignoreCoverageStatus(project, endpoint.method, endpoint.path, status, reason.trim()),
    onSuccess: onIgnored,
  });

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/65 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="w-full max-w-md overflow-hidden rounded-[10px] border border-line bg-surface shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-labelledby="ignore-title"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="flex items-start gap-2 border-b border-line bg-[#12171e] px-3.5 py-3">
          <EyeOff className="mt-0.5 h-4 w-4 shrink-0 text-brand" />
          <div className="min-w-0 flex-1">
            <h2 id="ignore-title" className="text-[12.5px] font-semibold text-ink">
              Ignore status code
            </h2>
            <p className="mt-0.5 break-all font-mono text-[10.5px] text-faint">
              {endpoint.method} {endpoint.path}{" "}
              <span className="rounded border border-line-soft px-1 py-0.5 text-[9.5px]">{status}</span>
            </p>
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} aria-label="Close ignore dialog">
            <X className="h-4 w-4" />
          </Button>
        </div>

        <div className="px-3.5 py-3">
          <p className="text-[11px] leading-relaxed text-muted">
            This status code stays visible but no longer counts as a coverage gap. You can undo it
            later.
          </p>
          <label className="mt-3 block text-[10px] uppercase tracking-[0.8px] text-faint">
            Reason
            <textarea
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              rows={3}
              className="mt-1.5 w-full resize-y rounded-md border border-line bg-canvas px-2 py-1.5 text-[11px] text-ink outline-none placeholder:text-faint focus-visible:border-brand/60"
              placeholder="Why is this status code not applicable?"
            />
          </label>
          {mutation.isError && (
            <p className="mt-2 text-[11px] text-danger">
              {mutation.error instanceof Error ? mutation.error.message : "Failed to ignore"}
            </p>
          )}
          <div className="mt-3 flex items-center gap-2">
            <Button
              variant="default"
              onClick={() => mutation.mutate()}
              disabled={mutation.isPending || reason.trim() === ""}
            >
              {mutation.isPending ? "Saving…" : "Ignore status"}
            </Button>
            <Button variant="ghost" onClick={onClose}>
              Cancel
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function defaultIgnoreReason(status: string): string {
  if (status === "404") return "No path parameter or lookup input can produce 404 on this endpoint.";
  if (status === "405") return "405 requires a different HTTP method; this operation cannot observe it.";
  return "This status code is documented but not applicable for this operation.";
}

function scenariosFor(status: string): string[] {
  if (status === "400" || status === "422") return ["missing required field", "wrong type", "empty body"];
  if (status === "401") return ["missing token", "invalid token"];
  if (status === "403") return ["wrong role"];
  if (status === "404") return ["unknown id"];
  if (status === "409") return ["duplicate"];
  return ["documented case"];
}

function generateCommand(endpoint: ApiCoverageEndpoint, status: string, cases: string[]): string {
  const caseArg = cases.length > 0 ? ` --cases "${cases.join(", ")}"` : "";
  return `npx redline generate --operation "${endpoint.method} ${endpoint.path}" --status ${status}${caseArg}`;
}

function EndpointRow({
  endpoint,
  active,
  onSelect,
  onGenerate,
  onIgnore,
  onUnignore,
}: {
  endpoint: ApiCoverageEndpoint;
  active: boolean;
  onSelect: () => void;
  onGenerate: (status: string) => void;
  onIgnore: (status: string) => void;
  onUnignore: (status: string) => void;
}) {
  const observed = new Set(endpoint.observed_status_codes);
  const ignored = new Set(endpoint.ignored_status_codes);
  const hints = new Map((endpoint.status_hints ?? []).map((hint) => [hint.status, hint]));
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
      title="Show endpoint details"
      className={cn(
        "grid w-full cursor-pointer grid-cols-[minmax(220px,1fr)_82px_92px_210px_54px] gap-3 border-b border-line-soft px-3.5 py-2.5 text-left text-[11px] outline-none last:border-b-0 hover:bg-[#1b222b] focus-visible:ring-2 focus-visible:ring-brand/50",
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
          endpoint.status_codes.map((code) =>
            observed.has(code) ? (
              <span
                key={code}
                className="rounded border border-ok/40 bg-ok/10 px-1 py-0.5 font-mono text-[9.5px] text-ok"
                title="seen in a stored run"
              >
                {code}
              </span>
            ) : ignored.has(code) ? (
              <button
                key={code}
                type="button"
                onClick={(event) => {
                  event.stopPropagation();
                  onUnignore(code);
                }}
                title="Ignored — click to undo"
                className="inline-flex items-center gap-1 rounded border border-dashed border-line px-1 py-0.5 font-mono text-[9.5px] text-faint hover:border-brand/50 hover:text-brand"
              >
                {code}
                <EyeOff className="h-2.5 w-2.5" />
              </button>
            ) : (
              <span key={code} className="inline-flex items-center gap-0.5">
                {GENERATOR_READY ? (
                  <button
                    type="button"
                    onClick={(event) => {
                      event.stopPropagation();
                      onGenerate(code);
                    }}
                    title={`Generate a test for ${code}`}
                    className="inline-flex items-center gap-1 rounded border border-brand/40 bg-brand/10 px-1 py-0.5 font-mono text-[9.5px] text-brand hover:border-brand hover:bg-brand/15"
                  >
                    {code}
                    <Sparkles className="h-2.5 w-2.5" />
                  </button>
                ) : (
                  <span
                    title={hints.get(code)?.reason || "documented but not seen"}
                    className={cn(
                      "inline-flex items-center gap-0.5 rounded border px-1 py-0.5 font-mono text-[9.5px]",
                      hints.get(code)?.hint === "likely_unreachable"
                        ? "border-warn/40 bg-warn/10 text-warn"
                        : "border-line-soft bg-canvas text-faint",
                    )}
                  >
                    {code}
                    {hints.get(code)?.hint === "likely_unreachable" && (
                      <AlertTriangle className="h-2.5 w-2.5" />
                    )}
                    {hints.get(code)?.hint === "reachable" && (
                      <Info className="h-2.5 w-2.5 text-muted" />
                    )}
                  </span>
                )}
                <button
                  type="button"
                  onClick={(event) => {
                    event.stopPropagation();
                    onIgnore(code);
                  }}
                  title={`Ignore ${code} for this endpoint`}
                  aria-label={`Ignore ${code}`}
                  className="rounded border border-line px-1 py-0.5 text-faint hover:border-brand/50 hover:text-brand"
                >
                  <EyeOff className="h-2.5 w-2.5" />
                </button>
              </span>
            ),
          )
        )}
      </span>
      <span className="text-right font-mono text-muted">{endpoint.tests}</span>
    </div>
  );
}

function EndpointDetail({
  endpoint,
  onClose,
  onGenerate,
  onIgnore,
  onUnignore,
  onSuggest,
}: {
  endpoint: ApiCoverageEndpoint;
  onClose: () => void;
  onGenerate: (status: string) => void;
  onIgnore: (status: string) => void;
  onUnignore: (status: string) => void;
  onSuggest: (status: string, suggestion: ApiCoverageSuggestion) => void;
}) {
  const observed = new Set(endpoint.observed_status_codes);
  const ignored = new Set(endpoint.ignored_status_codes);
  const hints = new Map((endpoint.status_hints ?? []).map((hint) => [hint.status, hint]));
  const activeHints = (endpoint.status_hints ?? []).filter(
    (hint) =>
      hint.hint !== "unknown" && !observed.has(hint.status) && !ignored.has(hint.status),
  );
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
              endpoint.status_codes.map((code) =>
                observed.has(code) ? (
                  <span
                    key={code}
                    className="rounded border border-ok/40 bg-ok/10 px-1 py-0.5 font-mono text-[9.5px] text-ok"
                    title="seen in a stored run"
                  >
                    {code}
                  </span>
                ) : ignored.has(code) ? (
                  <button
                    key={code}
                    type="button"
                    onClick={() => onUnignore(code)}
                    title="Ignored — click to undo"
                    className="inline-flex items-center gap-1 rounded border border-dashed border-line px-1 py-0.5 font-mono text-[9.5px] text-faint hover:border-brand/50 hover:text-brand"
                  >
                    {code} · ignored
                    <EyeOff className="h-2.5 w-2.5" />
                  </button>
                ) : (
                  <span key={code} className="inline-flex items-center gap-0.5">
                    {GENERATOR_READY ? (
                      <button
                        type="button"
                        onClick={() => onGenerate(code)}
                        title={`Generate a test for ${code}`}
                        className="inline-flex items-center gap-1 rounded border border-brand/40 bg-brand/10 px-1 py-0.5 font-mono text-[9.5px] text-brand hover:border-brand hover:bg-brand/15"
                      >
                        {code}
                        <Sparkles className="h-2.5 w-2.5" />
                      </button>
                    ) : (
                      <span
                        title={hints.get(code)?.reason || "documented but not seen"}
                        className={cn(
                          "inline-flex items-center gap-0.5 rounded border px-1 py-0.5 font-mono text-[9.5px]",
                          hints.get(code)?.hint === "likely_unreachable"
                            ? "border-warn/40 bg-warn/10 text-warn"
                            : "border-line-soft bg-canvas text-faint",
                        )}
                      >
                        {code}
                        {hints.get(code)?.hint === "likely_unreachable" && (
                          <AlertTriangle className="h-2.5 w-2.5" />
                        )}
                        {hints.get(code)?.hint === "reachable" && (
                          <Info className="h-2.5 w-2.5 text-muted" />
                        )}
                      </span>
                    )}
                    <button
                      type="button"
                      onClick={() => onIgnore(code)}
                      title={`Ignore ${code} for this endpoint`}
                      aria-label={`Ignore ${code}`}
                      className="rounded border border-line px-1 py-0.5 text-faint hover:border-brand/50 hover:text-brand"
                    >
                      <EyeOff className="h-2.5 w-2.5" />
                    </button>
                  </span>
                ),
              )
            )}
          </div>
          {activeHints.length > 0 && (
            <div className="mt-2 flex flex-col gap-1.5">
              {activeHints.map((hint) => {
                const suggestion = hint.suggestion;
                return (
                  <div
                    key={hint.status}
                    className="flex flex-wrap items-start gap-2 text-[10px] leading-relaxed text-faint"
                  >
                    <p className="min-w-0 flex-1">
                      <span className="font-mono text-muted">{hint.status}</span>{" "}
                      {hint.hint === "likely_unreachable" ? (
                        <span className="text-warn">likely unreachable</span>
                      ) : (
                        <span className="text-ok">reachable</span>
                      )}{" "}
                      — {hint.reason}
                    </p>
                    {suggestion?.kind === "business" && (
                      <span className="rounded border border-warn/40 bg-warn/10 px-1 py-0.5 text-[9.5px] text-warn">
                        needs context
                      </span>
                    )}
                    {suggestion?.kind === "recipe" && suggestion.command && (
                      <button
                        type="button"
                        onClick={() => onSuggest(hint.status, suggestion)}
                        className="inline-flex items-center gap-1 rounded border border-brand/40 bg-brand/10 px-1.5 py-0.5 text-[9.5px] text-brand hover:border-brand hover:bg-brand/15"
                      >
                        <Sparkles className="h-2.5 w-2.5" />
                        Suggest test
                      </button>
                    )}
                  </div>
                );
              })}
            </div>
          )}
          <p className="mt-1.5 text-[10px] text-faint">
            Gray status codes can be ignored. Ignored codes stay visible and can be undone;
            deterministic recipes can be verified with Suggest test.
          </p>
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
  const tests = change.test_keys ?? [];
  const total = change.tests ?? tests.length;
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
        {tests.length > 0 ? (
          <details className="mt-1">
            <summary className="cursor-pointer text-[10px] text-brand">
              {total} test{total === 1 ? "" : "s"} call{total === 1 ? "s" : ""} this endpoint
            </summary>
            <div className="mt-1 flex flex-col gap-0.5">
              {tests.map((test) => (
                <span key={test} className="break-all font-mono text-[9.5px] leading-relaxed text-faint">
                  {test}
                </span>
              ))}
            </div>
            {total > tests.length && (
              <div className="mt-1 text-[9.5px] text-faint">Showing the first {tests.length} of {total}.</div>
            )}
          </details>
        ) : (
          <div className="mt-1 text-[10px] text-faint">No recorded test calls this endpoint.</div>
        )}
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
