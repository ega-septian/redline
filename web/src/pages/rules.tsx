import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";

import { api } from "@/api/client";
import type { ApiRule } from "@/api/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { categoryLabel, type Category } from "@/lib/failure";
import { cn } from "@/lib/utils";

const statuses = ["all", "proposed", "active", "rejected"] as const;
type StatusFilter = (typeof statuses)[number];

const categoryTone = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
} as const;

function categoryOf(rule: ApiRule): Category {
  return rule.category in categoryLabel ? (rule.category as Category) : "unclear";
}

function date(iso: string): string {
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

export function RulesPage() {
  const [status, setStatus] = useState<StatusFilter>("all");
  const [query, setQuery] = useState("");
  const rulesQuery = useQuery({ queryKey: ["rules"], queryFn: api.rules });
  const rules = rulesQuery.data ?? [];

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return rules.filter((rule) => {
      if (status !== "all" && rule.status !== status) return false;
      if (!needle) return true;
      return [rule.pattern, rule.summary, rule.category].join(" ").toLowerCase().includes(needle);
    });
  }, [rules, status, query]);

  const counts = useMemo(
    () => ({
      all: rules.length,
      proposed: rules.filter((rule) => rule.status === "proposed").length,
      active: rules.filter((rule) => rule.status === "active").length,
      rejected: rules.filter((rule) => rule.status === "rejected").length,
      hits: rules.reduce((total, rule) => total + rule.hits, 0),
    }),
    [rules],
  );

  if (rulesQuery.isPending) return <State>Loading rules…</State>;
  if (rulesQuery.isError) {
    return (
      <State>
        <div className="font-semibold text-ink">Cannot load rules</div>
        <div className="mt-1 text-xs">
          {rulesQuery.error instanceof Error ? rulesQuery.error.message : "The API request failed."}
        </div>
        <Button className="mt-3" onClick={() => rulesQuery.refetch()}>
          Retry
        </Button>
      </State>
    );
  }

  return (
    <>
      <section className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Kpi label="Rules" value={String(counts.all)} hint="learned from proven cases" />
        <Kpi label="Proposed" value={String(counts.proposed)} hint="waiting for approval" tone="warn" />
        <Kpi label="Active" value={String(counts.active)} hint="matched by the rules engine" tone="ok" />
        <Kpi label="Rule hits" value={String(counts.hits)} hint="since they were approved" tone="brand" />
      </section>

      <section className="flex flex-wrap items-center gap-2 rounded-[10px] border border-line bg-surface px-2.5 py-2">
        {statuses.map((item) => (
          <button
            key={item}
            type="button"
            onClick={() => setStatus(item)}
            className={cn(
              "rounded-full border px-2.5 py-1 text-xs capitalize whitespace-nowrap transition-colors",
              status === item
                ? "border-[#1f6feb66] bg-[#1f6feb22] text-[#79c0ff]"
                : "border-line bg-[#0f141a] text-muted hover:text-ink",
            )}
          >
            {item} <span className="ml-1 text-faint">{counts[item]}</span>
          </button>
        ))}
        <div className="flex w-full min-w-0 items-center gap-1.5 rounded-md border border-line bg-[#0f141a] px-2.5 py-1 text-faint sm:ml-auto sm:w-auto sm:min-w-[230px]">
          <Search className="h-3.5 w-3.5" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search pattern or summary…"
            className="h-6 border-0 bg-transparent px-0 focus-visible:ring-0"
            aria-label="Search rules"
          />
        </div>
      </section>

      {filtered.length === 0 ? (
        <State>No rule matches this filter. New rules are proposed from proven cases.</State>
      ) : (
        <div className="flex flex-col gap-2">
          {filtered.map((rule) => (
            <RuleCard key={rule.id} rule={rule} />
          ))}
        </div>
      )}

      <p className="text-[11px] text-faint">
        Approve/reject is saved here with a CSRF token. Rule proposals and analysis stay in the
        CLI/API.
      </p>
    </>
  );
}

function RuleCard({ rule }: { rule: ApiRule }) {
  const category = categoryOf(rule);
  const queryClient = useQueryClient();
  const decision = useMutation({
    mutationFn: (status: "active" | "rejected") => api.decideRule(rule.id, status),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["rules"] }),
        queryClient.invalidateQueries({ queryKey: ["scoreboard"] }),
      ]);
    },
  });
  const tone =
    rule.status === "active" ? "flaky" : rule.status === "rejected" ? "neutral" : "env";
  return (
    <Card className="p-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={categoryTone[category]}>{categoryLabel[category]}</Badge>
        <Badge tone={tone}>{rule.status.toUpperCase()}</Badge>
        <span className="font-mono text-[11px] text-faint">#{rule.id}</span>
        <span className="ml-auto text-[11px] text-faint">{date(rule.created_at)}</span>
      </div>
      <code className="mt-2 block overflow-x-auto rounded border border-line-soft bg-canvas px-2 py-1.5 font-mono text-[11px] text-[#adbac7]">
        {rule.pattern}
      </code>
      <p className="mt-2 text-[12.5px] text-[#cdd9e5]">{rule.summary}</p>
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-faint">
        <span>{rule.hits} hits</span>
        <span>{rule.also_matches} also matches</span>
        <span>from {rule.learned_from.length} proven cases</span>
        {rule.decided_by && <span>decided by {rule.decided_by}</span>}
        {decision.isSuccess && <span className="text-ok">Saved</span>}
        {decision.isError && (
          <span className="text-danger">
            {decision.error instanceof Error ? decision.error.message : "Failed to save"}
          </span>
        )}
        <span className="ml-auto flex gap-1.5">
          {rule.status !== "active" && (
            <Button
              variant="default"
              onClick={() => decision.mutate("active")}
              disabled={decision.isPending}
            >
              Approve
            </Button>
          )}
          {rule.status !== "rejected" && (
            <Button
              variant="outline"
              onClick={() => decision.mutate("rejected")}
              disabled={decision.isPending}
            >
              Reject
            </Button>
          )}
        </span>
      </div>
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
  hint: string;
  tone?: "warn" | "ok" | "brand";
}) {
  return (
    <Card className="px-3.5 py-3">
      <div className="text-[11.5px] text-muted">{label}</div>
      <div
        className={cn(
          "mt-0.5 text-2xl font-semibold tracking-tight",
          tone === "warn" && "text-warn",
          tone === "ok" && "text-[#56d364]",
          tone === "brand" && "text-[#79c0ff]",
        )}
      >
        {value}
      </div>
      <div className="text-[11px] text-faint">{hint}</div>
    </Card>
  );
}

function State({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">{children}</div>
  );
}
