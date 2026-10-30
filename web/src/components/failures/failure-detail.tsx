import { Check, Copy, ExternalLink } from "lucide-react";
import { useState, type ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { categoryLabel, type Failure } from "@/lib/failure";
import { cn } from "@/lib/utils";

const categoryTone = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
} as const;

const tabs = ["Analysis", "Occurrences", "Knowledge"] as const;
type Tab = (typeof tabs)[number];

export function FailureDetail({
  failure,
  loading = false,
}: {
  failure: Failure;
  loading?: boolean;
}) {
  const [tab, setTab] = useState<Tab>("Analysis");
  const [copied, setCopied] = useState(false);

  async function copyError() {
    await navigator.clipboard.writeText(failure.reproduce);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  return (
    <aside className="overflow-hidden rounded-[10px] border border-line bg-surface lg:sticky lg:top-4">
      <div className="border-b border-line bg-[#12171e] p-4 pb-3">
        <div className="mb-2 flex items-center gap-2">
          <Badge tone={categoryTone[failure.category]}>{categoryLabel[failure.category]}</Badge>
          <Badge>
            {failure.source === "rule" && "RULE · FREE"}
            {failure.source === "ai" && `AI · ${failure.model ?? "model"}`}
            {failure.source === "experiment" && "PROVEN · EXPERIMENT"}
            {failure.source === "human" && "HUMAN"}
            {failure.source === "none" && "UNANALYZED"}
          </Badge>
          <span className="ml-auto font-mono text-[11px] text-faint">
            {failure.fingerprint.slice(0, 8)}…
          </span>
          {loading && <span className="text-[10px] text-faint">loading…</span>}
        </div>

        <h2 className="text-sm font-semibold leading-snug">{failure.title}</h2>
        <div className="mt-1 break-words font-mono text-[11px] text-faint">
          {failure.file} › {failure.test} · {failure.occurrences}{" "}
          {failure.occurrences === 1 ? "occurrence" : "occurrences"}
        </div>

        <div className="mt-3 flex flex-wrap gap-1.5">
          <Button variant="default" onClick={copyError}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            {copied ? "Copied" : "Copy error"}
          </Button>
          <Button asChild>
            <a
              href={`/api/groups/${encodeURIComponent(failure.fingerprint)}`}
              target="_blank"
              rel="noreferrer"
            >
              Open JSON <ExternalLink className="h-3 w-3" />
            </a>
          </Button>
        </div>
      </div>

      <div className="flex gap-0.5 border-b border-line bg-[#12171e] px-2.5">
        {tabs.map((item) => (
          <button
            key={item}
            type="button"
            onClick={() => setTab(item)}
            className={cn(
              "border-b-2 px-2.5 pb-2 pt-2.5 text-[11.5px] transition-colors",
              tab === item
                ? "border-brand text-ink"
                : "border-transparent text-faint hover:text-muted",
            )}
          >
            {item}
          </button>
        ))}
      </div>

      <div className="flex flex-col gap-3.5 p-4">
        {tab === "Analysis" && <Analysis failure={failure} />}
        {tab === "Occurrences" && <Occurrences failure={failure} />}
        {tab === "Knowledge" && <Knowledge failure={failure} />}

        <div className="flex items-center justify-between border-t border-line-soft pt-2.5 text-[10.5px] text-faint">
          <span>
            Decided by{" "}
            <b className="font-semibold text-muted">
              {failure.source === "none" ? "not analyzed" : failure.source}
            </b>{" "}
            · {failure.latency} · {failure.cost}
          </span>
          <span>first seen {failure.firstSeen}</span>
        </div>
      </div>
    </aside>
  );
}

function Analysis({ failure }: { failure: Failure }) {
  return (
    <>
      <section>
        <BlockLabel>Summary</BlockLabel>
        <p className="text-[12.5px] leading-relaxed text-[#cdd9e5]">{failure.summary}</p>
      </section>

      <section>
        <BlockLabel>Evidence · quoted from the run</BlockLabel>
        <div className="flex flex-col gap-1">
          {failure.evidence.map((line) => (
            <code
              key={line}
              className="truncate rounded border border-line-soft border-l-2 border-l-danger bg-canvas px-2 py-1.5 font-mono text-[11px] text-[#adbac7]"
            >
              {line}
            </code>
          ))}
        </div>
      </section>

      <div className="flex gap-2 text-xs text-[#cdd9e5]">
        <span className="text-brand">→</span>
        <span>{failure.nextStep}</span>
      </div>

      <section>
        <BlockLabel>Last 14 runs</BlockLabel>
        <div className="flex h-8 items-end gap-[3px]">
          {failure.spark.map((state, index) => (
            <i
              key={index}
              className={cn(
                "flex-1 rounded-t-sm",
                state === "fail" && "bg-danger",
                state === "pass" && "bg-[#2ea043]",
                state === "none" && "bg-line",
              )}
              style={{ height: `${38 + ((index * 9) % 40)}%` }}
            />
          ))}
        </div>
        <div className="mt-1.5 flex gap-2.5 text-[10.5px] text-faint">
          <span>
            <i className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-[#2ea043]" />
            passed
          </span>
          <span>
            <i className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-danger" />
            failed
          </span>
          <span>
            <i className="mr-1 inline-block h-1.5 w-1.5 rounded-full bg-line" />
            no data
          </span>
        </div>
      </section>

      <section>
        <BlockLabel>Last error</BlockLabel>
        <pre className="max-h-40 overflow-auto rounded-md border border-line-soft bg-[#0b0f14] p-2 font-mono text-[10.5px] leading-relaxed whitespace-pre-wrap text-[#adbac7]">
          {failure.reproduce}
        </pre>
      </section>
    </>
  );
}

function Occurrences({ failure }: { failure: Failure }) {
  const rows = failure.spark
    .map((state, index) => ({
      id: 48 - (failure.spark.length - index - 1),
      state,
      at: index === failure.spark.length - 1 ? failure.lastSeen : `run ${48 - (failure.spark.length - index - 1)}`,
    }))
    .reverse();

  return (
    <section>
      <BlockLabel>Occurrences</BlockLabel>
      <div className="flex flex-col divide-y divide-line-soft rounded-md border border-line-soft">
        {rows.map((row) => (
          <div key={row.id} className="flex items-center gap-2.5 px-2.5 py-2 text-[11.5px]">
            <span
              className={cn(
                "h-1.5 w-1.5 rounded-full",
                row.state === "fail" ? "bg-danger" : "bg-ok",
              )}
            />
            <span className="font-mono text-muted">run #{row.id}</span>
            <span className="text-faint">{row.state === "fail" ? "failed" : "passed"}</span>
            <span className="ml-auto text-faint">{row.at}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

function Knowledge({ failure }: { failure: Failure }) {
  return (
    <section className="flex flex-col gap-2">
      <BlockLabel>Knowledge graph</BlockLabel>
      <Fact label="Endpoint" value={failure.endpoint} />
      <Fact label="Files" value={failure.files.join(" · ")} />
      <Fact label="Also fails" value={failure.alsoFails} />
      <Fact label="Fingerprint" value={failure.fingerprint} />
      <p className="mt-1 text-[11px] leading-relaxed text-faint">
        The graph stores which files and endpoints each test uses, so a shared schema or a failing
        endpoint is visible across specs.
      </p>
    </section>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline gap-2 text-[11.5px]">
      <span className="w-[66px] shrink-0 text-faint">{label}</span>
      <span className="break-all font-mono text-[11px] text-[#cdd9e5]">{value}</span>
    </div>
  );
}

function BlockLabel({ children }: { children: ReactNode }) {
  return (
    <div className="mb-1.5 text-[10px] uppercase tracking-[0.8px] text-faint">{children}</div>
  );
}
