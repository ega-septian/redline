import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

type Kpi = {
  label: string;
  value: string;
  hint: string;
  tone?: "danger" | "ok" | "brand";
};

export function Kpis({
  open,
  occurrences,
  newToday,
  projects,
}: {
  open: number;
  occurrences: number;
  newToday: number;
  projects: number;
}) {
  const items: Kpi[] = [
    { label: "Open failures", value: String(open), hint: `${newToday} new today`, tone: "danger" },
    { label: "Occurrences", value: String(occurrences), hint: "failed test results" },
    { label: "New today", value: String(newToday), hint: "first seen today", tone: "ok" },
    { label: "Projects", value: String(projects), hint: "reporting to this server", tone: "brand" },
  ];

  return (
    <section className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {items.map((item) => (
        <Card key={item.label} className="px-3.5 py-3">
          <div className="flex items-center gap-1.5 text-[11.5px] text-muted">{item.label}</div>
          <div
            className={cn(
              "mt-0.5 text-2xl font-semibold tracking-tight",
              item.tone === "danger" && "text-[#ff7b72]",
              item.tone === "ok" && "text-[#56d364]",
              item.tone === "brand" && "text-[#79c0ff]",
            )}
          >
            {item.value}
          </div>
          <div className="text-[11px] text-faint">{item.hint}</div>
        </Card>
      ))}
    </section>
  );
}
