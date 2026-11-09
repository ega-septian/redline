import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { FileWarning, RefreshCw, Search } from "lucide-react";
import { useMemo, useState, type FormEvent } from "react";

import { api } from "@/api/client";
import type { ApiLoadError } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { categoryLabel, type Category } from "@/lib/failure";
import { cn } from "@/lib/utils";

const statuses = ["open", "acknowledged", "in_progress", "muted", "wont_fix", "resolved"] as const;
type LoadStatus = (typeof statuses)[number];

const statusLabel: Record<LoadStatus, string> = {
  open: "Open",
  acknowledged: "Acknowledged",
  in_progress: "In progress",
  muted: "Muted",
  wont_fix: "Won't fix",
  resolved: "Resolved",
};

const statusTone: Record<LoadStatus, "neutral" | "brand" | "env" | "backend" | "flaky"> = {
  open: "neutral",
  acknowledged: "brand",
  in_progress: "env",
  muted: "neutral",
  wont_fix: "backend",
  resolved: "flaky",
};

const categoryTone: Record<string, "backend" | "test" | "env" | "flaky" | "neutral"> = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
};

export function LoadErrorsPage() {
  const query = useQuery({ queryKey: ["load-errors"], queryFn: () => api.loadErrors(50) });
  const [search, setSearch] = useState("");
  const items = query.data ?? [];
  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) return items;
    return items.filter((item) =>
      `${item.message} ${item.triage_owner ?? ""} ${item.triage_status}`.toLowerCase().includes(needle),
    );
  }, [items, search]);

  const open = items.filter(
    (item) => item.triage_status !== "resolved" && item.triage_status !== "wont_fix" && item.triage_status !== "muted",
  ).length;
  const unassigned = items.filter((item) => !item.triage_owner && item.triage_status !== "resolved").length;
  const occurrences = items.reduce((total, item) => total + item.occurrences, 0);

  if (query.isPending) return <State>Loading load errors…</State>;
  if (query.isError) {
    return (
      <State>
        <div className="font-semibold">Cannot load suite load errors</div>
        <p className="mt-1 text-xs text-muted">
          {query.error instanceof Error ? query.error.message : "The API request failed."}
        </p>
        <Button className="mt-3" onClick={() => query.refetch()}>
          <RefreshCw className="h-3.5 w-3.5" />
          Retry
        </Button>
      </State>
    );
  }

  return (
    <>
      <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Kpi label="Groups" value={String(items.length)} hint="distinct report errors" />
        <Kpi label="Active" value={String(open)} hint="not resolved or ignored" tone={open > 0 ? "danger" : "ok"} />
        <Kpi label="Unassigned" value={String(unassigned)} hint="no owner yet" tone={unassigned > 0 ? "warn" : "ok"} />
        <Kpi label="Occurrences" value={String(occurrences)} hint="failed runs" />
      </section>

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
          <FileWarning className="h-3.5 w-3.5 text-danger" />
          <span className="text-[12.5px] font-semibold">Suite load errors</span>
          <Badge tone="neutral">{filtered.length} shown</Badge>
          <span className="hidden text-[10.5px] text-faint md:inline">
            specs or global setup that failed before tests could run
          </span>
          <div className="ml-auto flex min-w-[200px] flex-1 items-center gap-1.5 rounded-md border border-line bg-canvas px-2.5 text-faint sm:max-w-xs">
            <Search className="h-3.5 w-3.5" />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search message, owner or status…"
              aria-label="Search load errors"
              className="h-7 border-0 bg-transparent px-0 text-[11px] focus-visible:ring-0"
            />
          </div>
        </div>

        {filtered.length === 0 ? (
          <div className="px-4 py-10 text-center text-sm text-muted">
            No suite load errors recorded. Runs where a spec or global setup fails will appear here.
          </div>
        ) : (
          filtered.map((item) => <LoadErrorRow key={item.id} item={item} />)
        )}
      </Card>

      <p className="text-[11px] text-faint">
        These errors have no test result, so they are grouped separately from failures. Resolving one is
        manual; if it appears again in a later run, it reopens automatically.
      </p>
    </>
  );
}

function LoadErrorRow({ item }: { item: ApiLoadError }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [status, setStatus] = useState<LoadStatus>(
    (statuses as readonly string[]).includes(item.triage_status)
      ? (item.triage_status as LoadStatus)
      : "open",
  );
  const [owner, setOwner] = useState(item.triage_owner ?? "");
  const [note, setNote] = useState(item.triage_note ?? "");
  const [error, setError] = useState("");

  const analyze = useMutation({
    mutationFn: () => api.analyzeLoadError(item.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["load-errors"] }),
    onError: (err) => setError(err instanceof Error ? err.message : "Analysis failed."),
  });

  const analyzeAI = useMutation({
    mutationFn: () => api.analyzeLoadError(item.id, true),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["load-errors"] }),
    onError: (err) => setError(err instanceof Error ? err.message : "AI analysis failed."),
  });

  const save = useMutation({
    mutationFn: () => api.triageLoadError(item.id, { status, owner: owner.trim(), note: note.trim() }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["load-errors"] });
      setOpen(false);
    },
    onError: (err) => setError(err instanceof Error ? err.message : "Triage failed."),
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    save.mutate();
  }

  return (
    <div className="border-b border-line-soft last:border-b-0">
      <div className="flex gap-3 px-3.5 py-3">
        <FileWarning className="mt-0.5 h-4 w-4 shrink-0 text-danger" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone={statusTone[(item.triage_status as LoadStatus) || "open"] ?? "neutral"}>
              {statusLabel[(item.triage_status as LoadStatus) || "open"] ?? item.triage_status}
            </Badge>
            {item.category ? (
              <Badge tone={categoryTone[item.category] ?? "neutral"}>
                {categoryLabel[item.category as Category] ?? item.category}
              </Badge>
            ) : (
              <Badge tone="neutral">not analyzed</Badge>
            )}
            {item.triage_owner && <Badge tone="neutral">owner {item.triage_owner}</Badge>}
            <span className="text-[10.5px] text-faint">
              {item.occurrences} occurrence{item.occurrences === 1 ? "" : "s"}
            </span>
            <Link
              to="/runs/$runId"
              params={{ runId: String(item.last_seen_run) }}
              className="text-[10.5px] text-brand hover:underline"
            >
              last run #{item.last_seen_run}
            </Link>
          </div>
          <div
            className="mt-1 break-words font-mono text-[11.5px] leading-relaxed text-[#ffb3ad]"
            title={item.message}
          >
            {item.message.split("\n")[0]}
          </div>
          {item.summary && <div className="mt-1 text-[11px] text-muted">{item.summary}</div>}
          {item.next_step && (
            <div className="mt-0.5 text-[10.5px] text-faint">→ {item.next_step}</div>
          )}
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[10px] text-faint">
            <span>first seen {when(item.first_seen_at)} · run #{item.first_seen_run}</span>
            <span>last seen {when(item.last_seen_at)}</span>
            {item.triage_note && <span className="truncate">note: {item.triage_note}</span>}
          </div>
        </div>
        <div className="flex shrink-0 items-start gap-1">
          {!item.category && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => analyze.mutate()}
              disabled={analyze.isPending}
            >
              {analyze.isPending ? "Analyzing…" : "Analyze"}
            </Button>
          )}
          {item.category === "unclear" && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => analyzeAI.mutate()}
              disabled={analyzeAI.isPending}
              title="Ask the model to classify this unclear message"
            >
              {analyzeAI.isPending ? "Asking AI…" : "Analyze with AI"}
            </Button>
          )}
          <Button size="sm" variant={open ? "default" : "outline"} onClick={() => setOpen((value) => !value)}>
            {open ? "Cancel" : "Triage"}
          </Button>
        </div>
      </div>

      {error && !open && (
        <div className="border-t border-line-soft px-3.5 py-2 text-[10.5px] text-danger">{error}</div>
      )}

      {open && (
        <form onSubmit={submit} className="border-t border-line-soft bg-[#0f141a] px-3.5 py-3">
          <div className="grid gap-2 sm:grid-cols-[150px_minmax(0,1fr)_minmax(0,1.4fr)_auto] sm:items-end">
            <label className="text-[10.5px] text-faint">
              Status
              <select
                value={status}
                onChange={(event) => setStatus(event.target.value as LoadStatus)}
                aria-label="Load error status"
                className="mt-1 h-8 w-full rounded-md border border-line bg-canvas px-2 text-[11px] text-ink outline-none focus-visible:border-brand/60"
              >
                {statuses.map((value) => (
                  <option key={value} value={value}>
                    {statusLabel[value]}
                  </option>
                ))}
              </select>
            </label>
            <label className="text-[10.5px] text-faint">
              Owner
              <Input
                value={owner}
                onChange={(event) => setOwner(event.target.value)}
                placeholder="e.g. ega"
                className="mt-1 h-8 text-[11px]"
              />
            </label>
            <label className="text-[10.5px] text-faint">
              Note
              <Input
                value={note}
                onChange={(event) => setNote(event.target.value)}
                placeholder="next action or issue link"
                className="mt-1 h-8 text-[11px]"
              />
            </label>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save"}
            </Button>
          </div>
          {error && <div className="mt-1.5 text-[10.5px] text-danger">{error}</div>}
        </form>
      )}
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
  tone?: "ok" | "warn" | "danger";
}) {
  return (
    <Card className="px-3.5 py-3">
      <div className="text-[10.5px] uppercase tracking-[0.7px] text-faint">{label}</div>
      <div
        className={cn(
          "mt-0.5 text-2xl font-semibold tracking-tight",
          tone === "ok" && "text-ok",
          tone === "warn" && "text-warn",
          tone === "danger" && "text-danger",
        )}
      >
        {value}
      </div>
      <div className="text-[10.5px] text-faint">{hint}</div>
    </Card>
  );
}

function State({ children }: { children: React.ReactNode }) {
  return <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>;
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
