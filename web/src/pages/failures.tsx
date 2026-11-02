import { useQuery } from "@tanstack/react-query";
import { ChevronDown, CircleHelp, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { api } from "@/api/client";
import { FailureDetail } from "@/components/failures/failure-detail";
import { FailureTable } from "@/components/failures/failure-table";
import { Kpis } from "@/components/failures/kpis";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tooltip } from "@/components/ui/tooltip";
import {
  categoryLabel,
  toFailure,
  triageLabel,
  type Category,
  type FailureStatus,
  type TriageStatus,
} from "@/lib/failure";
import { cn } from "@/lib/utils";

type StatusFilter = "all" | FailureStatus | "needs_ai";
type CategoryFilter = "all" | Category;
type SortMode = "recent" | "oldest" | "occurrences";
type TriageFilter = "active" | "all" | TriageStatus;

const sorts: Array<{ id: SortMode; label: string }> = [
  { id: "recent", label: "Recent first" },
  { id: "oldest", label: "Oldest first" },
  { id: "occurrences", label: "Most seen" },
];

const triageFilters: Array<{ id: TriageFilter; label: string }> = [
  { id: "active", label: "Active queue" },
  { id: "all", label: "All triage" },
  ...(Object.entries(triageLabel) as Array<[TriageStatus, string]>).map(([id, label]) => ({
    id,
    label,
  })),
];

const statuses: Array<{
  id: StatusFilter;
  label: string;
  hint: string;
  align: "start" | "center" | "end";
  mobileAlign?: "start" | "center" | "end";
  wide?: boolean;
}> = [
  {
    id: "all",
    label: "All",
    hint: "Every open failure group, regardless of status.",
    align: "start",
  },
  {
    id: "new",
    label: "New",
    hint: "First seen in the latest run and never seen before.",
    align: "start",
    mobileAlign: "end",
  },
  {
    id: "recurring",
    label: "Recurring",
    hint: "Still failing across more than one run with the same fingerprint.",
    align: "start",
  },
  {
    id: "regressed",
    label: "Regressed",
    hint: "A failure that was resolved, then came back in a later run.",
    align: "end",
  },
  {
    id: "needs_ai",
    label: "Needs AI",
    hint: "No rule, AI or human diagnosis yet. Run AI or set the cause manually.",
    align: "end",
    wide: true,
  },
];

const categories = Object.entries(categoryLabel) as Array<[Category, string]>;

function isToday(iso: string): boolean {
  const date = new Date(iso);
  const now = new Date();
  return (
    date.getFullYear() === now.getFullYear() &&
    date.getMonth() === now.getMonth() &&
    date.getDate() === now.getDate()
  );
}

function StatusFilterPill({
  item,
  count,
  active,
  onSelect,
}: {
  item: (typeof statuses)[number];
  count: number;
  active: boolean;
  onSelect: () => void;
}) {
  return (
    <div
      className={cn(
        "flex h-8 w-full items-center rounded-full border transition-colors sm:w-auto",
        item.wide && "col-span-2 sm:col-span-1",
        active
          ? "border-brand/40 bg-brand/10 text-[#79c0ff]"
          : "border-line bg-[#0f141a] text-muted hover:border-[#3d444d] hover:bg-[#1b222b] hover:text-ink",
      )}
    >
      <button
        type="button"
        onClick={onSelect}
        aria-pressed={active}
        className="flex h-full min-w-0 flex-1 items-center justify-between gap-1.5 rounded-l-full pl-3 pr-1.5 text-[12px] font-medium whitespace-nowrap outline-none focus-visible:ring-2 focus-visible:ring-brand/60"
      >
        {item.label}
        <span
          className={cn(
            "rounded-full px-1.5 py-0.5 font-mono text-[10px] leading-none",
            active ? "bg-brand/15 text-[#79c0ff]" : "bg-[#21262d] text-faint",
          )}
        >
          {count}
        </span>
      </button>
      <Tooltip content={item.hint} align={item.align} mobileAlign={item.mobileAlign}>
        <button
          type="button"
          aria-label={`About the ${item.label} filter`}
          className={cn(
            "mr-1 grid h-6 w-6 shrink-0 place-items-center rounded-full outline-none transition-colors hover:text-brand focus-visible:ring-2 focus-visible:ring-brand/60",
            active ? "text-[#79c0ff]/80" : "text-muted",
          )}
        >
          <CircleHelp className="h-4 w-4" />
        </button>
      </Tooltip>
    </div>
  );
}

export function FailuresPage() {
  const params = useMemo(() => new URLSearchParams(window.location.search), []);
  const [status, setStatus] = useState<StatusFilter>(() => {
    const value = params.get("status");
    return value === "new" || value === "recurring" || value === "regressed" || value === "needs_ai"
      ? value
      : "all";
  });
  const [category, setCategory] = useState<CategoryFilter>(() => {
    const value = params.get("category");
    return categories.some(([id]) => id === value) ? (value as Category) : "all";
  });
  const [query, setQuery] = useState(() => params.get("q") ?? "");
  const [sort, setSort] = useState<SortMode>(() => {
    const value = params.get("sort");
    return value === "oldest" || value === "occurrences" ? value : "recent";
  });
  const [triage, setTriage] = useState<TriageFilter>(() => {
    const value = params.get("triage");
    if (value === "all") return "all";
    return value && value in triageLabel ? (value as TriageStatus) : "active";
  });
  // Deep link from a run detail's "Failure history" button.
  const [selected, setSelected] = useState(() => params.get("failure") ?? "");

  const groupsQuery = useQuery({ queryKey: ["groups"], queryFn: api.groups });
  const groups = groupsQuery.data ?? [];

  const base = useMemo(() => groups.map((group) => toFailure(group)), [groups]);
  const triageScoped = useMemo(
    () =>
      base.filter((failure) => {
        if (triage === "active") {
          return failure.triageStatus !== "muted" && failure.triageStatus !== "wont_fix";
        }
        return triage === "all" || failure.triageStatus === triage;
      }),
    [base, triage],
  );
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return triageScoped.filter((failure) => {
      if (status === "needs_ai") {
        if (!failure.needsAI) return false;
      } else if (status !== "all" && failure.status !== status) {
        return false;
      }
      if (category !== "all" && failure.category !== category) return false;
      if (!needle) return true;
      return [
        failure.title,
        failure.test,
        failure.testCaseId,
        failure.file,
        failure.endpoint,
        failure.fingerprint,
        failure.triageOwner,
        failure.triageNote,
      ]
        .join(" ")
        .toLowerCase()
        .includes(needle);
    });
  }, [triageScoped, status, category, query]);

  const sorted = useMemo(() => {
    const copy = [...filtered];
    switch (sort) {
      case "oldest":
        copy.sort((a, b) => Date.parse(a.lastSeenAt) - Date.parse(b.lastSeenAt));
        break;
      case "occurrences":
        copy.sort((a, b) => b.occurrences - a.occurrences);
        break;
      default:
        copy.sort((a, b) => Date.parse(b.lastSeenAt) - Date.parse(a.lastSeenAt));
    }
    return copy;
  }, [filtered, sort]);

  const activeFingerprint =
    (sorted.some((failure) => failure.fingerprint === selected)
      ? selected
      : sorted[0]?.fingerprint) ?? "";

  useEffect(() => {
    if (selected !== activeFingerprint) setSelected(activeFingerprint);
  }, [activeFingerprint, selected]);

  useEffect(() => {
    const next = new URLSearchParams();
    if (status !== "all") next.set("status", status);
    if (category !== "all") next.set("category", category);
    if (query) next.set("q", query);
    if (sort !== "recent") next.set("sort", sort);
    if (triage !== "active") next.set("triage", triage);
    if (activeFingerprint) next.set("failure", activeFingerprint);
    const search = next.toString();
    const url = `${window.location.pathname}${search ? `?${search}` : ""}`;
    if (url !== `${window.location.pathname}${window.location.search}`) {
      window.history.replaceState(null, "", url);
    }
  }, [status, category, query, sort, triage, activeFingerprint]);

  const activeGroup = groups.find((group) => group.fingerprint === activeFingerprint);
  const detailQuery = useQuery({
    queryKey: ["group", activeFingerprint],
    queryFn: () => api.group(activeFingerprint),
    enabled: activeFingerprint !== "",
    retry: false,
  });
  const knowledgeQuery = useQuery({
    queryKey: ["knowledge", activeFingerprint],
    queryFn: () => api.knowledge(activeFingerprint),
    enabled: activeFingerprint !== "",
    retry: false,
  });

  const active = activeGroup
    ? toFailure(activeGroup, detailQuery.data, knowledgeQuery.data)
    : undefined;

  const stats = useMemo(() => {
    const active = base.filter(
      (failure) => failure.triageStatus !== "muted" && failure.triageStatus !== "wont_fix",
    );
    return {
      open: active.length,
      occurrences: groups.reduce((total, group) => total + group.occurrences, 0),
      newToday: groups.filter((group) => isToday(group.first_seen_at)).length,
      needsAI: active.filter((failure) => failure.needsAI).length,
      projects: new Set(groups.map((group) => group.project)).size,
    };
  }, [groups, base]);

  if (groupsQuery.isPending) {
    return (
      <div className="rounded-[10px] border border-line bg-surface p-8 text-sm text-muted">
        Loading failures…
      </div>
    );
  }

  if (groupsQuery.isError) {
    return (
      <div className="rounded-[10px] border border-line bg-surface p-6">
        <div className="text-sm font-semibold">Cannot load failures</div>
        <p className="mt-1 text-xs text-muted">
          {groupsQuery.error instanceof Error ? groupsQuery.error.message : "The API request failed."}
        </p>
        <Button className="mt-3" onClick={() => groupsQuery.refetch()}>
          Retry
        </Button>
      </div>
    );
  }

  return (
    <>
      <Kpis {...stats} />

      <section className="rounded-[10px] border border-line bg-surface p-2">
        <div className="flex flex-wrap items-center gap-2">
          <div
            className="grid w-full grid-cols-2 gap-1.5 sm:flex sm:w-auto sm:flex-wrap"
            role="group"
            aria-label="Filter by status"
          >
            {statuses.map((item) => {
              const count =
                item.id === "all"
                  ? triageScoped.length
                  : item.id === "needs_ai"
                    ? triageScoped.filter((failure) => failure.needsAI).length
                    : triageScoped.filter((failure) => failure.status === item.id).length;
              return (
                <StatusFilterPill
                  key={item.id}
                  item={item}
                  count={count}
                  active={status === item.id}
                  onSelect={() => setStatus(item.id)}
                />
              );
            })}
          </div>

          <span aria-hidden="true" className="hidden h-5 w-px bg-line sm:block" />

          <div className="flex h-8 w-full items-center rounded-full border border-line bg-[#0f141a] transition-colors hover:border-[#3d444d] focus-within:border-brand/60 focus-within:ring-2 focus-within:ring-brand/60 sm:w-auto">
            <div className="relative min-w-0 flex-1">
              <select
                value={category}
                onChange={(event) => setCategory(event.target.value as CategoryFilter)}
                className={cn(
                  "h-8 w-full appearance-none rounded-l-full bg-transparent pl-3 pr-7 text-[12px] outline-none sm:min-w-[112px]",
                  category === "all" ? "text-muted" : "text-ink",
                )}
                aria-label="Filter by category"
              >
                <option value="all">Category</option>
                {categories.map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
              <ChevronDown className="pointer-events-none absolute right-1 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
            </div>
            <Tooltip
              content="Filter by the latest analysis or manual label. Unclear means no analysis has run yet."
              align="end"
            >
              <button
                type="button"
                aria-label="About the category filter"
                className="mr-1 grid h-6 w-6 shrink-0 place-items-center rounded-full text-muted outline-none transition-colors hover:text-brand focus-visible:ring-2 focus-visible:ring-brand/60"
              >
                <CircleHelp className="h-4 w-4" />
              </button>
            </Tooltip>
          </div>

          <div className="flex h-8 w-full items-center rounded-full border border-line bg-[#0f141a] transition-colors hover:border-[#3d444d] focus-within:border-brand/60 focus-within:ring-2 focus-within:ring-brand/60 sm:w-auto">
            <div className="relative min-w-0 flex-1">
              <select
                value={triage}
                onChange={(event) => setTriage(event.target.value as TriageFilter)}
                className={cn(
                  "h-8 w-full appearance-none rounded-full bg-transparent pl-3 pr-7 text-[12px] outline-none sm:min-w-[124px]",
                  triage === "active" ? "text-muted" : "text-ink",
                )}
                aria-label="Filter by triage status"
              >
                {triageFilters.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.label}
                  </option>
                ))}
              </select>
              <ChevronDown className="pointer-events-none absolute right-1 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
            </div>
          </div>

          <div className="flex h-8 w-full items-center rounded-full border border-line bg-[#0f141a] transition-colors hover:border-[#3d444d] focus-within:border-brand/60 focus-within:ring-2 focus-within:ring-brand/60 sm:w-auto">
            <div className="relative min-w-0 flex-1">
              <select
                value={sort}
                onChange={(event) => setSort(event.target.value as SortMode)}
                className="h-8 w-full appearance-none rounded-full bg-transparent pl-3 pr-7 text-[12px] text-muted outline-none sm:min-w-[118px]"
                aria-label="Sort failures"
              >
                {sorts.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.label}
                  </option>
                ))}
              </select>
              <ChevronDown className="pointer-events-none absolute right-1 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
            </div>
          </div>

          <div className="flex h-8 w-full min-w-0 items-center gap-1.5 rounded-full border border-line bg-[#0f141a] px-3 text-faint transition-colors focus-within:border-brand/60 focus-within:ring-2 focus-within:ring-brand/60 sm:ml-auto sm:w-auto sm:min-w-[230px]">
            <Search className="h-3.5 w-3.5 shrink-0" />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search test, ID, endpoint…"
              className="h-7 min-w-0 border-0 bg-transparent px-0 text-[12px] focus-visible:ring-0"
              aria-label="Search failures"
            />
          </div>
        </div>
      </section>

      <div className="grid items-start gap-3.5 lg:grid-cols-[minmax(0,1fr)_390px]">
        <FailureTable items={sorted} selected={activeFingerprint} onSelect={setSelected} />
        {active ? (
          <FailureDetail
            failure={active}
            loading={detailQuery.isPending || knowledgeQuery.isPending}
          />
        ) : (
          <aside className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">
            {base.length === 0
              ? "No open failures. Run a failing Playwright suite to populate this list."
              : "No failure matches this filter."}
          </aside>
        )}
      </div>

      <p className="text-[11px] text-faint">
        Live data from <span className="font-mono">GET /api/groups</span>. Confirm the diagnosis or
        analyze the stored failure with AI; neither action reruns the tests.
      </p>
    </>
  );
}
