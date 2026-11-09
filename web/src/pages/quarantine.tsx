import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CalendarDays, ChevronLeft, ChevronRight, Search, ShieldOff, X } from "lucide-react";
import { useMemo, useState } from "react";

import { api } from "@/api/client";
import type { ApiQuarantine } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useProject } from "@/lib/project";
import { cn } from "@/lib/utils";

const DAY_MS = 86_400_000;
const weekdays = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

type Notice = { tone: "ok" | "error"; text: string } | null;

export function QuarantinePage() {
  const { project } = useProject();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [month, setMonth] = useState(() => startOfMonth(new Date()));
  const [selectedDate, setSelectedDate] = useState<string | null>(null);
  const [notice, setNotice] = useState<Notice>(null);

  const query = useQuery({
    queryKey: ["quarantines", project],
    queryFn: () => api.quarantines(project),
    enabled: project !== "",
  });
  const release = useMutation({
    mutationFn: (test: string) => api.releaseQuarantine(test),
    onSuccess: async (_result, test) => {
      setNotice({ tone: "ok", text: `Released ${test}. The next run enforces it again.` });
      await invalidate();
    },
    onError: (error, test) => {
      setNotice({
        tone: "error",
        text: `${test}: ${error instanceof Error ? error.message : "release failed"}`,
      });
    },
  });

  const rows = query.data ?? [];
  const sorted = useMemo(
    () => [...rows].sort((left, right) => new Date(left.until).getTime() - new Date(right.until).getTime()),
    [rows],
  );
  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return sorted.filter((row) => {
      if (selectedDate && dateKey(row.until) !== selectedDate) return false;
      if (!needle) return true;
      return `${row.key} ${row.test ?? ""} ${row.test_case_id ?? ""} ${row.owner} ${row.reason}`
        .toLowerCase()
        .includes(needle);
    });
  }, [sorted, search, selectedDate]);
  const byDate = useMemo(() => {
    const groups = new Map<string, ApiQuarantine[]>();
    for (const row of sorted) {
      const key = dateKey(row.until);
      groups.set(key, [...(groups.get(key) ?? []), row]);
    }
    return groups;
  }, [sorted]);

  const due24 = rows.filter((row) => timeMs(row.until) - Date.now() <= DAY_MS).length;
  const due7 = rows.filter((row) => timeMs(row.until) - Date.now() <= 7 * DAY_MS).length;
  const oldest = rows.reduce((max, row) => Math.max(max, ageDays(row.created_at)), 0);

  async function invalidate() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["quarantines"] }),
      queryClient.invalidateQueries({ queryKey: ["test-health"] }),
      queryClient.invalidateQueries({ queryKey: ["test-history"] }),
      queryClient.invalidateQueries({ queryKey: ["quarantine-suggestions"] }),
      queryClient.invalidateQueries({ queryKey: ["groups"] }),
    ]);
  }

  function shiftMonth(delta: number) {
    setSelectedDate(null);
    setMonth((current) => new Date(current.getFullYear(), current.getMonth() + delta, 1));
  }

  function releaseRow(row: ApiQuarantine) {
    const test = row.test_case_id || row.test || row.key;
    if (!window.confirm(`Release ${test}? The test is enforced again on the next run.`)) return;
    release.mutate(test);
  }

  if (project === "") {
    return <State>Select a project in the sidebar to see its quarantines.</State>;
  }
  if (query.isPending) return <State>Loading quarantine…</State>;
  if (query.isError) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load quarantine</div>
        <div className="mt-1 text-xs">
          {query.error instanceof Error ? query.error.message : "The API request failed."}
        </div>
        <Button className="mt-3" onClick={() => query.refetch()}>
          Retry
        </Button>
      </State>
    );
  }

  return (
    <>
      {notice && <NoticeBar notice={notice} onClose={() => setNotice(null)} />}

      <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Kpi label="Active" value={String(rows.length)} hint={`project ${project}`} />
        <Kpi
          label="Due ≤24h"
          value={String(due24)}
          hint="needs review now"
          tone={due24 > 0 ? "danger" : "ok"}
        />
        <Kpi
          label="Due ≤7d"
          value={String(due7)}
          hint="this week"
          tone={due7 > 0 ? "warn" : "ok"}
        />
        <Kpi
          label="Longest open"
          value={rows.length === 0 ? "—" : oldest === 0 ? "today" : `${oldest}d`}
          hint="oldest active"
        />
      </section>

      {rows.length === 0 ? (
        <Card className="p-8 text-center">
          <ShieldOff className="mx-auto h-8 w-8 text-faint" />
          <div className="mt-3 text-sm font-semibold text-ink">No active quarantine</div>
          <p className="mx-auto mt-1 max-w-md text-xs leading-relaxed text-muted">
            Quarantines are time-boxed exceptions for known failures. The Health page suggests tests
            that may need one, based on flips, flake rate and recent failures.
          </p>
          <Link to="/health" className="mt-3 inline-block text-xs text-brand hover:underline">
            Open Health for suggestions
          </Link>
        </Card>
      ) : (
        <>
          <ExpiryCalendar
            month={month}
            byDate={byDate}
            selectedDate={selectedDate}
            onSelectDate={(date) => setSelectedDate((current) => (current === date ? null : date))}
            onShiftMonth={shiftMonth}
            onToday={() => {
              setSelectedDate(null);
              setMonth(startOfMonth(new Date()));
            }}
          />

          <Card className="overflow-hidden">
            <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
              <ShieldOff className="h-3.5 w-3.5 text-warn" />
              <span className="text-[12.5px] font-semibold">Active quarantines</span>
              <Badge tone="env">{filtered.length} shown</Badge>
              {selectedDate && (
                <button
                  type="button"
                  onClick={() => setSelectedDate(null)}
                  className="inline-flex items-center gap-1 rounded-full border border-line bg-canvas px-2 py-0.5 text-[10px] text-muted hover:text-ink"
                >
                  expires {formatDate(selectedDate)} <X className="h-3 w-3" />
                </button>
              )}
              <div className="ml-auto flex min-w-[200px] flex-1 items-center gap-1.5 rounded-md border border-line bg-canvas px-2.5 text-faint sm:max-w-xs">
                <Search className="h-3.5 w-3.5" />
                <Input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder="Search test, owner or reason…"
                  aria-label="Search quarantine"
                  className="h-7 border-0 bg-transparent px-0 text-[11px] focus-visible:ring-0"
                />
              </div>
            </div>

            {filtered.length === 0 ? (
              <div className="px-4 py-10 text-center text-sm text-muted">
                No quarantine matches this filter.
              </div>
            ) : (
              <div className="overflow-x-auto">
                <div className="min-w-[900px]">
                  <div className="grid grid-cols-[minmax(240px,1fr)_110px_150px_200px_90px_100px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2 text-[9.5px] uppercase tracking-[0.7px] text-faint">
                    <span>Test</span>
                    <span>Owner</span>
                    <span>Expires</span>
                    <span>Reason</span>
                    <span>Age</span>
                    <span className="text-right">Action</span>
                  </div>
                  {filtered.map((row) => (
                    <QuarantineRow
                      key={row.key}
                      row={row}
                      busy={release.isPending && release.variables === (row.test_case_id || row.test || row.key)}
                      onRelease={() => releaseRow(row)}
                    />
                  ))}
                </div>
              </div>
            )}
          </Card>
        </>
      )}
    </>
  );
}

function ExpiryCalendar({
  month,
  byDate,
  selectedDate,
  onSelectDate,
  onShiftMonth,
  onToday,
}: {
  month: Date;
  byDate: Map<string, ApiQuarantine[]>;
  selectedDate: string | null;
  onSelectDate: (date: string) => void;
  onShiftMonth: (delta: number) => void;
  onToday: () => void;
}) {
  const days = calendarDays(month);
  const today = dateKey(new Date());
  const label = new Intl.DateTimeFormat("en", { month: "long", year: "numeric" }).format(month);

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <CalendarDays className="h-3.5 w-3.5 text-brand" />
        <span className="text-[12.5px] font-semibold">Expiry calendar</span>
        <span className="text-[10.5px] text-faint">click a day to filter</span>
        <div className="ml-auto flex items-center gap-1">
          <Button size="icon" variant="ghost" onClick={() => onShiftMonth(-1)} aria-label="Previous month">
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <span className="min-w-[118px] text-center text-[11.5px] font-medium">{label}</span>
          <Button size="icon" variant="ghost" onClick={() => onShiftMonth(1)} aria-label="Next month">
            <ChevronRight className="h-4 w-4" />
          </Button>
          <Button size="sm" variant="ghost" onClick={onToday}>
            Today
          </Button>
        </div>
      </div>
      <div className="overflow-x-auto">
        <div className="min-w-[620px] p-3">
          <div className="grid grid-cols-7 gap-1 pb-1 text-center text-[9.5px] uppercase tracking-[0.7px] text-faint">
            {weekdays.map((day) => (
              <span key={day}>{day}</span>
            ))}
          </div>
          <div className="grid grid-cols-7 gap-1">
            {days.map((day) => {
              const key = dateKey(day);
              const events = byDate.get(key) ?? [];
              const current = day.getMonth() === month.getMonth();
              const selected = selectedDate === key;
              return (
                <button
                  key={key}
                  type="button"
                  disabled={!current}
                  onClick={() => onSelectDate(key)}
                  className={cn(
                    "min-h-[76px] rounded-md border p-1.5 text-left align-top transition-colors",
                    current
                      ? "border-line-soft bg-canvas hover:border-line hover:bg-[#12171e]"
                      : "border-transparent opacity-30",
                    key === today && !selected && "border-brand/40",
                    selected && "border-brand bg-[#1f6feb14]",
                  )}
                >
                  <div className="flex items-center justify-between">
                    <span
                      className={cn(
                        "font-mono text-[10px]",
                        key === today ? "font-bold text-brand" : "text-muted",
                      )}
                    >
                      {day.getDate()}
                    </span>
                    {events.length > 0 && (
                      <span className="rounded-full bg-warn/15 px-1.5 text-[9px] font-bold text-warn">
                        {events.length}
                      </span>
                    )}
                  </div>
                  <div className="mt-1 flex flex-col gap-0.5">
                    {events.slice(0, 2).map((row) => (
                      <span
                        key={row.key}
                        className="truncate rounded bg-warn/10 px-1 py-0.5 text-[9px] text-warn"
                        title={row.test || row.key}
                      >
                        {row.test_case_id || shortTest(row)}
                      </span>
                    ))}
                    {events.length > 2 && (
                      <span className="text-[9px] text-faint">+{events.length - 2} more</span>
                    )}
                  </div>
                </button>
              );
            })}
          </div>
        </div>
      </div>
    </Card>
  );
}

function QuarantineRow({
  row,
  busy,
  onRelease,
}: {
  row: ApiQuarantine;
  busy: boolean;
  onRelease: () => void;
}) {
  const days = daysLeft(row.until);
  return (
    <div className="grid grid-cols-[minmax(240px,1fr)_110px_150px_200px_90px_100px] items-center gap-3 border-b border-line-soft px-3.5 py-2.5 text-[11px] last:border-b-0 hover:bg-[#1b222b]">
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-1.5">
          <Badge tone="env" className="shrink-0 font-mono">
            {row.test_case_id || row.key}
          </Badge>
          {row.project && <span className="truncate text-[9.5px] text-faint">{row.project}</span>}
        </div>
        <div className="mt-0.5 truncate text-[11.5px] text-ink" title={row.test || row.key}>
          {testTitle(row)}
        </div>
      </div>
      <span className="truncate text-[10.5px] text-muted" title={row.owner}>
        {row.owner || "—"}
      </span>
      <div>
        <div className="text-[10.5px] text-muted">{formatDate(row.until)}</div>
        <Badge tone={days <= 1 ? "backend" : days <= 7 ? "env" : "neutral"}>
          {days === 0 ? "today" : `${days}d left`}
        </Badge>
      </div>
      <div className="truncate text-[10.5px] text-faint" title={row.reason}>
        {row.reason || "—"}
      </div>
      <span className="text-[10.5px] text-faint">{ageLabel(row.created_at)}</span>
      <div className="flex justify-end">
        <Button size="sm" variant="outline" onClick={onRelease} disabled={busy}>
          {busy ? "Releasing…" : "Release"}
        </Button>
      </div>
    </div>
  );
}

function Kpi({
  label,
  value,
  hint,
  tone = "neutral",
}: {
  label: string;
  value: string;
  hint: string;
  tone?: "neutral" | "ok" | "warn" | "danger";
}) {
  return (
    <div className="rounded-[10px] border border-line bg-surface p-3.5">
      <div className="text-[10px] uppercase tracking-[0.7px] text-faint">{label}</div>
      <div
        className={cn(
          "mt-1 text-[24px] font-semibold leading-none",
          tone === "ok"
            ? "text-ok"
            : tone === "warn"
              ? "text-warn"
              : tone === "danger"
                ? "text-danger"
                : "text-ink",
        )}
      >
        {value}
      </div>
      <div className="mt-1 truncate text-[10.5px] text-faint">{hint}</div>
    </div>
  );
}

function NoticeBar({ notice, onClose }: { notice: Exclude<Notice, null>; onClose: () => void }) {
  return (
    <div
      className={cn(
        "flex items-center gap-2 rounded-[10px] border px-3.5 py-2 text-[11.5px]",
        notice.tone === "ok"
          ? "border-ok/30 bg-ok/5 text-ok"
          : "border-danger/30 bg-danger/5 text-danger",
      )}
    >
      <span className="min-w-0 flex-1">{notice.text}</span>
      <button type="button" onClick={onClose} aria-label="Dismiss notification">
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

function State({ children }: { children: React.ReactNode }) {
  return <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>;
}

function startOfMonth(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), 1);
}

function calendarDays(month: Date): Date[] {
  const first = startOfMonth(month);
  const offset = (first.getDay() + 6) % 7;
  const start = new Date(first);
  start.setDate(first.getDate() - offset);
  return Array.from({ length: 42 }, (_, index) => {
    const day = new Date(start);
    day.setDate(start.getDate() + index);
    return day;
  });
}

function dateKey(value: string | Date): string {
  const date = typeof value === "string" ? new Date(value) : value;
  if (Number.isNaN(date.getTime())) return "";
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

function timeMs(iso: string): number {
  const value = new Date(iso).getTime();
  return Number.isNaN(value) ? 0 : value;
}

function daysLeft(iso: string): number {
  return Math.max(0, Math.ceil((timeMs(iso) - Date.now()) / DAY_MS));
}

function ageDays(iso: string): number {
  return Math.max(0, Math.floor((Date.now() - timeMs(iso)) / DAY_MS));
}

function ageLabel(iso: string): string {
  const days = ageDays(iso);
  return days === 0 ? "today" : `${days}d open`;
}

function formatDate(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("en", { month: "short", day: "numeric", year: "numeric" }).format(date);
}

function shortTest(row: ApiQuarantine): string {
  const value = row.test || row.key;
  return value.split(" › ").pop() || value;
}

function testTitle(row: ApiQuarantine): string {
  const value = row.test || row.key;
  const parts = value.split(" › ");
  if (parts.length < 2) return value;
  return parts.slice(2).join(" › ") || parts[parts.length - 1];
}
