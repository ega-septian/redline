import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Check, CheckCircle2, Circle, Copy, Sparkles } from "lucide-react";
import { useState, type ReactNode } from "react";

import { api } from "@/api/client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

const RUN_COMMAND = "REDLINE=1 npx playwright test";

/**
 * First-run guide for a fresh install. It reads the same config and run queries as the rest of the
 * app, changes nothing, and disappears once the two required steps are done.
 */
export function SetupChecklist() {
  const config = useQuery({ queryKey: ["config"], queryFn: api.config });
  const runs = useQuery({ queryKey: ["runs"], queryFn: api.runs });

  if (config.isPending || runs.isPending || config.isError || runs.isError) return null;

  const contracts = config.data.contracts;
  const hasSource = contracts.length > 0;
  const hasRun = (runs.data?.length ?? 0) > 0;
  if (hasSource && hasRun) return null;

  const done = Number(hasSource) + Number(hasRun);
  const notifications = config.data.notifications;

  return (
    <Card className="overflow-hidden border-brand/30" role="region" aria-label="Get started">
      <div className="flex flex-wrap items-center gap-2 border-b border-line bg-[#12171e] px-3.5 py-2.5">
        <Sparkles className="h-3.5 w-3.5 text-brand" />
        <span className="text-[12.5px] font-semibold">Get started</span>
        <Badge tone="brand">{done}/2 required</Badge>
        <span className="ml-auto text-[10.5px] text-faint">Set up this Redline instance</span>
      </div>

      <Step
        done={hasSource}
        title="Add a contract source"
        hint={
          hasSource
            ? `Configured: ${contracts.map((source) => source.project).join(", ")}`
            : "Point Redline at the OpenAPI JSON your API serves."
        }
      >
        {!hasSource && (
          <Link
            to="/config"
            className="inline-flex items-center gap-1 text-[11px] text-brand hover:underline"
          >
            Open Config <ArrowRight className="h-3 w-3" />
          </Link>
        )}
      </Step>

      <Step
        done={hasRun}
        title="Send your first run"
        hint={
          hasRun
            ? "A run is already stored; open Runs to inspect it."
            : "Run the Playwright reporter against this server."
        }
      >
        {!hasRun && <CommandCopy />}
      </Step>

      <Step
        done={notifications.webhook_set}
        optional
        title="Notifications"
        hint={
          notifications.webhook_set
            ? "Webhook configured; delivery is enabled in Config."
            : "Optional: get a message when a new failure appears."
        }
      >
        {!notifications.webhook_set && (
          <Link to="/config" className="text-[11px] text-brand hover:underline">
            Configure
          </Link>
        )}
      </Step>

      <div className="border-t border-line px-3.5 py-2 text-[10.5px] text-faint">
        Nothing here changes your config automatically. This card disappears when the two required
        steps are done.
      </div>
    </Card>
  );
}

function Step({
  done,
  optional,
  title,
  hint,
  children,
}: {
  done: boolean;
  optional?: boolean;
  title: string;
  hint: string;
  children?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-line-soft px-3.5 py-2.5 last:border-b-0">
      {done ? (
        <CheckCircle2 className="h-4 w-4 shrink-0 text-ok" aria-hidden="true" />
      ) : (
        <Circle className="h-4 w-4 shrink-0 text-faint" aria-hidden="true" />
      )}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="sr-only">{done ? "Done" : "Not done"}:</span>
          <span className={cn("text-[12px]", done ? "text-muted" : "text-ink")}>{title}</span>
          {optional && <Badge tone="neutral">optional</Badge>}
        </div>
        <div className="mt-0.5 text-[10.5px] text-faint">{hint}</div>
      </div>
      {children}
    </div>
  );
}

function CommandCopy() {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(RUN_COMMAND);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      window.prompt("Copy the command", RUN_COMMAND);
    }
  }

  return (
    <div className="flex items-center gap-1.5">
      <code className="rounded border border-line bg-canvas px-1.5 py-1 font-mono text-[10.5px] text-[#cdd9e5]">
        {RUN_COMMAND}
      </code>
      <Button size="sm" variant="ghost" onClick={copy} aria-label="Copy the first-run command">
        {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
        {copied ? "Copied" : "Copy"}
      </Button>
    </div>
  );
}
