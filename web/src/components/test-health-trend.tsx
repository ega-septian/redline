import { Link } from "@tanstack/react-router";

import type { TestHealthPoint } from "@/api/types";
import { Card } from "@/components/ui/card";

export function TestHealthTrend({ points }: { points: TestHealthPoint[] }) {
  if (points.length === 0) return null;
  const latest = points[points.length - 1];
  const maxTotal = Math.max(
    1,
    ...points.map((point) => point.passed + point.failed + point.flaky),
  );

  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <span className="text-[12.5px] font-semibold">Test health trend</span>
        <span className="text-[10.5px] text-faint">
          {points.length} runs · height = results · color = status
        </span>
        <span className="ml-auto text-[10.5px] text-faint">
          latest {pct(latest.pass_rate)} pass · {pct(latest.flake_rate)} flake
        </span>
      </div>
      <div className="flex h-24 items-end gap-1.5 px-3.5 pb-2 pt-3">
        {points.map((point) => {
          const total = point.passed + point.failed + point.flaky;
          const height = Math.max(4, Math.round((total / maxTotal) * 64));
          return (
            <Link
              key={point.run_id}
              to="/runs/$runId"
              params={{ runId: String(point.run_id) }}
              className="group flex h-16 flex-1 items-end"
              title={`run #${point.run_id} · ${when(point.at)} · ${point.passed} passed, ${point.failed} failed, ${point.flaky} flaky${point.branch ? ` · ${point.branch}` : ""}`}
            >
              <div
                className="flex w-full flex-col-reverse overflow-hidden rounded-t bg-[#21262d] transition-opacity group-hover:opacity-80"
                style={{ height: `${height}px` }}
              >
                <div className="min-h-0 bg-ok/70" style={{ flexGrow: point.passed, flexBasis: 0 }} />
                <div className="min-h-0 bg-danger/70" style={{ flexGrow: point.failed, flexBasis: 0 }} />
                <div className="min-h-0 bg-warn/70" style={{ flexGrow: point.flaky, flexBasis: 0 }} />
              </div>
            </Link>
          );
        })}
      </div>
      <div className="flex flex-wrap items-center gap-3 border-t border-line px-3.5 py-2 text-[10px] text-faint">
        <span>
          <i className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-ok" />
          passed
        </span>
        <span>
          <i className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-danger" />
          failed
        </span>
        <span>
          <i className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-warn" />
          flaky
        </span>
        <span className="ml-auto">oldest</span>
        <span>latest</span>
      </div>
    </Card>
  );
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
