import { Search } from "lucide-react";
import { useMemo, useState } from "react";

import { FailureDetail } from "@/components/failures/failure-detail";
import { FailureTable } from "@/components/failures/failure-table";
import { Kpis } from "@/components/failures/kpis";
import { Input } from "@/components/ui/input";
import { categoryLabel, failures, type Category, type FailureStatus } from "@/data/mock";
import { cn } from "@/lib/utils";

type StatusFilter = "all" | FailureStatus;
type CategoryFilter = "all" | Category;

const statuses: Array<{ id: StatusFilter; label: string }> = [
  { id: "all", label: "All" },
  { id: "new", label: "New" },
  { id: "recurring", label: "Recurring" },
  { id: "regressed", label: "Regressed" },
];

const categories = Object.entries(categoryLabel) as Array<[Category, string]>;

export function FailuresPage() {
  const [status, setStatus] = useState<StatusFilter>("all");
  const [category, setCategory] = useState<CategoryFilter>("all");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(failures[0]?.fingerprint ?? "");

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return failures.filter((failure) => {
      if (status !== "all" && failure.status !== status) return false;
      if (category !== "all" && failure.category !== category) return false;
      if (!needle) return true;
      return [failure.title, failure.test, failure.file, failure.endpoint, failure.fingerprint]
        .join(" ")
        .toLowerCase()
        .includes(needle);
    });
  }, [status, category, query]);

  const active = filtered.find((item) => item.fingerprint === selected) ?? filtered[0];

  return (
    <>
      <Kpis />

      <section className="flex flex-wrap items-center gap-2 rounded-[10px] border border-line bg-surface px-2.5 py-2">
        {statuses.map((item) => {
          const count =
            item.id === "all"
              ? failures.length
              : failures.filter((failure) => failure.status === item.id).length;
          return (
            <button
              key={item.id}
              type="button"
              onClick={() => setStatus(item.id)}
              className={cn(
                "rounded-full border px-2.5 py-1 text-xs whitespace-nowrap transition-colors",
                status === item.id
                  ? "border-[#1f6feb66] bg-[#1f6feb22] text-[#79c0ff]"
                  : "border-line bg-[#0f141a] text-muted hover:text-ink",
              )}
            >
              {item.label} <span className="ml-1 text-faint">{count}</span>
            </button>
          );
        })}

        <select
          value={category}
          onChange={(event) => setCategory(event.target.value as CategoryFilter)}
          className="h-7 rounded-full border border-line bg-[#0f141a] px-2.5 text-xs text-muted outline-none focus-visible:border-brand/60"
          aria-label="Filter by category"
        >
          <option value="all">Category</option>
          {categories.map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>

        <div className="flex w-full min-w-0 items-center gap-1.5 rounded-md border border-line bg-[#0f141a] px-2.5 py-1 text-faint sm:ml-auto sm:w-auto sm:min-w-[210px]">
          <Search className="h-3.5 w-3.5" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search test, ID, endpoint…"
            className="h-6 border-0 bg-transparent px-0 focus-visible:ring-0"
            aria-label="Search failures"
          />
        </div>
      </section>

      <div className="grid items-start gap-3.5 lg:grid-cols-[minmax(0,1fr)_390px]">
        <FailureTable items={filtered} selected={active?.fingerprint ?? ""} onSelect={setSelected} />
        {active ? (
          <FailureDetail failure={active} />
        ) : (
          <aside className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">
            No failure selected.
          </aside>
        )}
      </div>

      <p className="text-[11px] text-faint">
        Foundation preview — sample data. The next PR reads <span className="font-mono">GET
        /api/groups</span> and adds the login cookie.
      </p>
    </>
  );
}
