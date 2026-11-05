import { Link } from "@tanstack/react-router";

import type { ApiCoveragePoint } from "@/api/types";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

export function CoverageTrend({ points }: { points: ApiCoveragePoint[] }) {
  if (points.length === 0) return null;
  const latest = points[points.length - 1];
  const changes = points.reduce((total, point) => total + point.changes, 0);
  const best = Math.max(1, ...points.map((point) => point.covered));
  const ratio = latest.documented ? Math.round((latest.covered / latest.documented) * 100) : 0;
  return (
    <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <span className="text-[12.5px] font-semibold">Coverage trend</span>
        <span className="text-[10.5px] text-faint">
          {points.length} runs · bar scaled to the best run · color = change
        </span>
        <span className="ml-auto text-[10.5px] text-faint">
          latest {latest.covered}/{latest.documented} covered ({ratio}%)
        </span>
      </div>
      <div className="flex h-24 items-end gap-1.5 px-3.5 pb-2 pt-3">
        {points.map((point, index) => {
          const height = Math.max(4, Math.round((point.covered / best) * 64));
          const previous = index > 0 ? points[index - 1] : null;
          const delta = previous ? point.covered - previous.covered : 0;
          return (
            <Link
              key={point.run_id}
              to="/runs/$runId"
              params={{ runId: String(point.run_id) }}
              className="group flex h-16 flex-1 items-end"
              title={`run #${point.run_id} · ${point.covered}/${point.documented} covered · ${point.status_codes_seen}/${point.status_codes_documented} status codes · ${point.changes} contract changes${delta ? ` · ${delta > 0 ? "+" : ""}${delta} vs previous` : ""}`}
            >
              <div
                className={cn(
                  "w-full rounded-t transition-colors",
                  delta > 0
                    ? "bg-ok/70 group-hover:bg-ok"
                    : delta < 0
                      ? "bg-danger/70 group-hover:bg-danger"
                      : "bg-brand/60 group-hover:bg-brand",
                )}
                style={{ height: `${height}px` }}
              />
            </Link>
          );
        })}
      </div>
      <div className="flex justify-between border-t border-line px-3.5 py-2 text-[10px] text-faint">
        <span>oldest</span>
        <span>
          {points.length < 2 ? "Collect more runs to see a trend." : `contract changes: ${changes}`}
        </span>
        <span>latest</span>
      </div>
    </Card>
  );
}
