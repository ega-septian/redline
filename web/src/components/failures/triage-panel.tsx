import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { api } from "@/api/client";
import { Button } from "@/components/ui/button";
import { triageLabel, type Failure, type TriageStatus } from "@/lib/failure";

const statuses = Object.entries(triageLabel) as Array<[TriageStatus, string]>;

function when(iso?: string): string {
  if (!iso) return "";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

export function TriagePanel({ failure }: { failure: Failure }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<TriageStatus>(failure.triageStatus);
  const [owner, setOwner] = useState(failure.triageOwner);
  const [note, setNote] = useState(failure.triageNote);

  const mutation = useMutation({
    mutationFn: () => api.triage(failure.fingerprint, { status, owner, note }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["groups"] }),
        queryClient.invalidateQueries({ queryKey: ["group", failure.fingerprint] }),
        queryClient.invalidateQueries({ queryKey: ["scoreboard"] }),
      ]);
    },
  });

  return (
    <section className="border-t border-line-soft pt-3">
      <div className="mb-1.5 flex items-baseline gap-2">
        <span className="text-[10px] uppercase tracking-[0.8px] text-faint">Triage</span>
        <span className="text-[10px] text-faint">workflow status and owner</span>
      </div>

      <div className="flex flex-col gap-1.5">
        <div className="grid grid-cols-[1fr_1fr] gap-1.5">
          <select
            value={status}
            onChange={(event) => setStatus(event.target.value as TriageStatus)}
            className="h-8 rounded-md border border-line bg-canvas px-2 text-xs text-ink outline-none focus-visible:border-brand/60"
            aria-label="Triage status"
          >
            {statuses.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
          <input
            value={owner}
            onChange={(event) => setOwner(event.target.value)}
            placeholder="Owner (e.g. ega)"
            className="h-8 min-w-0 rounded-md border border-line bg-canvas px-2 text-xs text-ink outline-none placeholder:text-faint focus-visible:border-brand/60"
            aria-label="Triage owner"
          />
        </div>
        <textarea
          value={note}
          onChange={(event) => setNote(event.target.value)}
          rows={2}
          placeholder="Optional triage note: next action, blocker, issue link…"
          className="resize-y rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-ink outline-none placeholder:text-faint focus-visible:border-brand/60"
          aria-label="Triage note"
        />
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="default" onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? "Saving…" : "Save triage"}
          </Button>
          {mutation.isSuccess && (
            <span className="text-[11px] text-ok">Saved as {mutation.data.by}</span>
          )}
          {mutation.isError && (
            <span className="text-[11px] text-danger">
              {mutation.error instanceof Error ? mutation.error.message : "Failed to save"}
            </span>
          )}
        </div>
        {failure.triageUpdatedAt && (
          <p className="text-[10.5px] text-faint">
            Last triage by {failure.triageUpdatedBy || "—"} · {when(failure.triageUpdatedAt)}
          </p>
        )}
      </div>
    </section>
  );
}
