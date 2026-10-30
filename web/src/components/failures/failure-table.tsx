import { Badge } from "@/components/ui/badge";
import { categoryLabel, statusLabel, type Failure } from "@/lib/failure";
import { cn } from "@/lib/utils";

const categoryTone = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
} as const;

const statusStyle = {
  new: "text-[#ff7b72]",
  regressed: "text-warn",
  recurring: "text-[#79c0ff]",
} as const;

const categoryDot = {
  backend_bug: "bg-danger",
  test_bug: "bg-violet",
  environment: "bg-warn",
  flaky: "bg-ok",
  unclear: "bg-faint",
} as const;

export function FailureTable({
  items,
  selected,
  onSelect,
}: {
  items: Failure[];
  selected: string;
  onSelect: (fingerprint: string) => void;
}) {
  return (
    <div className="overflow-hidden rounded-[10px] border border-line bg-surface">
      <div className="grid grid-cols-[92px_minmax(0,1fr)_120px_84px_96px] gap-3 border-b border-line bg-[#12171e] px-3.5 py-2.5 text-[10.5px] uppercase tracking-[0.7px] text-faint max-lg:hidden">
        <span>Status</span>
        <span>Test</span>
        <span>Category</span>
        <span>Confidence</span>
        <span>Occurrences</span>
      </div>

      {items.length === 0 && (
        <div className="px-4 py-10 text-center text-sm text-muted">
          No failure matches this filter.
        </div>
      )}

      {items.map((failure) => (
        <button
          key={failure.fingerprint}
          type="button"
          onClick={() => onSelect(failure.fingerprint)}
          className={cn(
            "grid w-full grid-cols-1 gap-2 border-b border-line-soft px-3.5 py-3 text-left transition-colors last:border-b-0 hover:bg-[#1b222b] lg:grid-cols-[92px_minmax(0,1fr)_120px_84px_96px] lg:items-center lg:gap-3",
            selected === failure.fingerprint &&
              "bg-[#1b2430] shadow-[inset_3px_0_0_var(--color-brand)]",
          )}
        >
          <span
            className={cn(
              "inline-flex items-center gap-1.5 text-[10.5px] font-bold tracking-wider",
              statusStyle[failure.status],
            )}
          >
            <span className="h-1.5 w-1.5 rounded-sm bg-current" />
            {statusLabel[failure.status]}
          </span>

          <div className="min-w-0">
            <div className="truncate text-[13px] font-medium">{failure.title}</div>
            <div className="mt-0.5 truncate font-mono text-[11px] text-faint">
              {failure.file} › {failure.test}
            </div>
          </div>

          <span className="flex items-center gap-1.5 text-[11px]">
            <span className={cn("h-[7px] w-[7px] rounded-full", categoryDot[failure.category])} />
            <Badge tone={categoryTone[failure.category]}>{categoryLabel[failure.category]}</Badge>
          </span>

          <Confidence value={failure.confidence} />

          <span className="font-mono text-xs text-muted">
            {failure.occurrences} {failure.occurrences === 1 ? "test" : "tests"}
          </span>
        </button>
      ))}
    </div>
  );
}

function Confidence({ value }: { value: Failure["confidence"] }) {
  const filled = value === "medium" ? 2 : value === "low" ? 1 : value === "none" ? 0 : 3;
  return (
    <span className="flex items-center gap-1.5 text-[11px] text-muted">
      <span className="flex gap-0.5">
        {[0, 1, 2].map((index) => (
          <i
            key={index}
            className={cn(
              "h-[11px] w-1 rounded-[1px]",
              index < filled ? (value === "high" ? "bg-ok" : "bg-warn") : "bg-line",
            )}
          />
        ))}
      </span>
      {value === "none" ? "—" : value}
    </span>
  );
}
