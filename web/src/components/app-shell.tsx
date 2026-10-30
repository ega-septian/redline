import { useQuery } from "@tanstack/react-query";
import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import {
  AlertTriangle,
  BarChart3,
  GitBranch,
  LayoutDashboard,
  Sparkles,
  Waypoints,
  type LucideIcon,
} from "lucide-react";

import { api } from "@/api/client";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

type Health = { status: string; version?: string; api?: number; auth?: boolean };

async function fetchHealth(): Promise<Health> {
  const response = await fetch("/healthz");
  if (!response.ok) throw new Error(`healthz ${response.status}`);
  return (await response.json()) as Health;
}

const nav = [
  { label: "Overview", icon: LayoutDashboard, soon: true },
  { label: "Failures", icon: AlertTriangle, to: "/" as const, count: 0 },
  { label: "Runs", icon: GitBranch, to: "/runs" as const },
  { label: "Impact", icon: Waypoints, to: "/impact" as const },
  { label: "Rules", icon: Sparkles, to: "/rules" as const },
  { label: "Scoreboard", icon: BarChart3, to: "/scoreboard" as const },
];

export function AppShell() {
  const health = useQuery({
    queryKey: ["health"],
    queryFn: fetchHealth,
    refetchInterval: 30_000,
    retry: 1,
  });
  const groups = useQuery({ queryKey: ["groups"], queryFn: api.groups });
  const items = nav.map((item) =>
    item.label === "Failures" ? { ...item, count: groups.data?.length ?? 0 } : item,
  );
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const title = pathname.startsWith("/runs")
    ? "Runs"
    : pathname.startsWith("/impact")
      ? "Impact"
      : pathname.startsWith("/rules")
        ? "Rules"
        : pathname.startsWith("/scoreboard")
          ? "Scoreboard"
          : "Failures";
  const subtitle =
    title === "Failures"
      ? "open failures"
      : title === "Runs"
        ? "recent runs"
        : title === "Impact"
          ? "graph"
          : title === "Rules"
            ? "learned from proven cases"
            : "accuracy";

  function isActive(to?: string) {
    return to === "/" ? pathname === "/" : !!to && pathname.startsWith(to);
  }

  const online = health.isSuccess && !health.isError;
  const version = health.data?.version ?? "dev";
  const apiVersion = health.data?.api ?? 1;
  const host = window.location.host;

  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-[232px] shrink-0 flex-col gap-4 border-r border-line bg-[#0b0f14] p-3 md:flex">
        <div className="flex items-center gap-2.5 px-1 pb-2.5 pt-0.5">
          <div className="h-[26px] w-[26px] rounded-[7px] bg-gradient-to-br from-danger via-violet to-brand shadow-[inset_0_0_0_1px_#ffffff1a]" />
          <div>
            <div className="text-[15px] font-semibold leading-4">Redline</div>
            <div className="mt-0.5 text-[11px] text-faint">failure triage</div>
          </div>
        </div>

        <div className="flex items-center justify-between rounded-lg border border-line bg-surface px-2.5 py-2 text-[12.5px]">
          <span>Project</span>
          <span className="font-semibold">toolshop</span>
          <span className="text-faint">⌄</span>
        </div>

        <nav className="flex flex-col gap-0.5">
          <div className="px-2.5 pb-1 pt-1.5 text-[10.5px] uppercase tracking-[0.9px] text-faint">
            Workspace
          </div>
          {items.slice(0, 4).map((item) => (
            <NavItem
              key={item.label}
              {...item}
              active={"to" in item && isActive(item.to)}
            />
          ))}
          <div className="px-2.5 pb-1 pt-3 text-[10.5px] uppercase tracking-[0.9px] text-faint">
            Knowledge
          </div>
          {items.slice(4).map((item) => (
            <NavItem
              key={item.label}
              {...item}
              active={"to" in item && isActive(item.to)}
            />
          ))}
        </nav>

        <div className="mt-auto border-t border-line pt-3 text-[11px] leading-relaxed text-faint">
          <span className="font-semibold text-muted">Labels only</span>
          <br />
          Labels and notes are saved here. Analysis and rules stay in the CLI or API.
          {health.data?.auth && (
            <form method="post" action="/ui/logout" className="mt-2">
              <button type="submit" className="text-[11px] text-brand hover:underline">
                Sign out
              </button>
            </form>
          )}
        </div>
      </aside>

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex min-h-14 flex-wrap items-center gap-x-3.5 gap-y-1 border-b border-line px-4 py-2 md:px-5">
          <h1 className="shrink-0 text-[15px] font-semibold">{title}</h1>
          <span className="hidden text-xs text-faint sm:inline">{subtitle}</span>
          <Badge tone="brand" className="gap-1.5 px-2">
            <span className="h-1.5 w-1.5 rounded-full bg-brand" />
            Labels only
          </Badge>
          <div className="flex-1" />
          <div className="hidden items-center gap-2 text-xs text-muted sm:flex">
            <span
              className={cn(
                "h-[7px] w-[7px] rounded-full",
                online ? "bg-ok shadow-[0_0_0_3px_#3fb95022]" : "bg-danger shadow-[0_0_0_3px_#f8514922]",
              )}
            />
            {online ? host : "server offline"}
          </div>
          <span className="hidden font-mono text-[11.5px] text-faint lg:inline">
            {version} · api {apiVersion}
          </span>
          <div className="hidden h-[26px] w-[26px] place-items-center rounded-full bg-[#1f6feb] text-[11px] font-bold text-white sm:grid">
            ES
          </div>
        </header>

        <div className="flex flex-col gap-4 p-4 md:p-5">
          <Outlet />
        </div>
      </main>
    </div>
  );
}

function NavItem({
  label,
  icon: Icon,
  to,
  count,
  soon,
  active,
}: {
  label: string;
  icon: LucideIcon;
  to?: "/" | "/runs" | "/impact" | "/rules" | "/scoreboard";
  count?: number;
  soon?: boolean;
  active?: boolean;
}) {
  const content = (
    <>
      <Icon className="h-4 w-4 opacity-90" />
      <span>{label}</span>
      {count !== undefined && (
        <span className="ml-auto rounded-full border border-line bg-surface-2 px-1.5 text-[11px] text-muted">
          {count}
        </span>
      )}
      {soon && !count && <span className="ml-auto text-[10px] text-faint">soon</span>}
    </>
  );

  if (to) {
    return (
      <Link
        to={to}
        className={cn(
          "flex items-center gap-2.5 rounded-[7px] px-2.5 py-2 text-[13px] transition-colors",
          active
            ? "bg-surface-2 text-ink shadow-[inset_0_0_0_1px_#ffffff0d]"
            : "text-muted hover:bg-surface-2 hover:text-ink",
        )}
      >
        {content}
      </Link>
    );
  }

  return (
    <span
      title="Coming in Phase 1"
      className="flex cursor-default items-center gap-2.5 rounded-[7px] px-2.5 py-2 text-[13px] text-muted"
    >
      {content}
    </span>
  );
}
