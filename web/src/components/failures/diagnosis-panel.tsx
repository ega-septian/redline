import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Check, Pencil, Sparkles } from "lucide-react";
import { useState } from "react";

import { api } from "@/api/client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { categoryLabel, type Failure } from "@/lib/failure";

const options: Array<{ value: string; label: string }> = [
  { value: "backend_bug", label: categoryLabel.backend_bug },
  { value: "test_bug", label: categoryLabel.test_bug },
  { value: "environment", label: categoryLabel.environment },
  { value: "flaky", label: categoryLabel.flaky },
  { value: "unknown", label: "Unclear" },
];

const categoryTone = {
  backend_bug: "backend",
  test_bug: "test",
  environment: "env",
  flaky: "flaky",
  unclear: "neutral",
} as const;

/**
 * Human truth, with one click when the rule/AI diagnosis is right and an
 * explicit editor when it is wrong. A label is still stored, but the common
 * path is "Confirm" instead of filling a form.
 */
export function DiagnosisPanel({ failure }: { failure: Failure }) {
  const queryClient = useQueryClient();
  const initial = failure.category === "unclear" ? "unknown" : failure.category;
  const [editing, setEditing] = useState(false);
  const [label, setLabel] = useState(initial);
  const [note, setNote] = useState(failure.labelNote);
  const [saved, setSaved] = useState(false);

  const mutation = useMutation({
    mutationFn: (value: { label: string; note: string }) =>
      api.label(failure.fingerprint, value),
    onSuccess: async () => {
      setSaved(true);
      setEditing(false);
      await refresh();
    },
    onMutate: () => setSaved(false),
  });

  const analyzeMutation = useMutation({
    mutationFn: () => api.analyze(failure.fingerprint),
    onSuccess: refresh,
    onMutate: () => setSaved(false),
  });

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["groups"] }),
      queryClient.invalidateQueries({ queryKey: ["group", failure.fingerprint] }),
      queryClient.invalidateQueries({ queryKey: ["knowledge", failure.fingerprint] }),
      queryClient.invalidateQueries({ queryKey: ["scoreboard"] }),
      queryClient.invalidateQueries({ queryKey: ["rules"] }),
      queryClient.invalidateQueries({ queryKey: ["rule-candidates"] }),
    ]);
  }

  const hasDiagnosis = failure.source === "rule" || failure.source === "ai";
  const proven = failure.source === "experiment";
  const confirmed = failure.source === "human";
  const needsAI = failure.source === "none";
  const accepted = failure.acceptedBy !== "" && !confirmed && !proven;
  const canConfirm = hasDiagnosis && initial !== "unknown";
  const category = failure.category;

  function confirm() {
    mutation.mutate({ label: initial, note: failure.labelNote });
  }

  return (
    <section className="border-t border-line-soft pt-3">
      <div className="mb-1.5 flex items-baseline gap-2">
        <span className="text-[10px] uppercase tracking-[0.8px] text-faint">Diagnosis</span>
        <span className="text-[10px] text-faint">human truth, used by the scoreboard and rule learning</span>
      </div>

      {!editing && (hasDiagnosis || proven || confirmed || needsAI) && (
        <div className="rounded-md border border-line-soft bg-[#0b0f14] p-2.5">
          <div className="flex flex-wrap items-center gap-2">
            {hasDiagnosis && (
              <>
                <Badge tone={categoryTone[category]}>{categoryLabel[category]}</Badge>
                <span className="text-[11px] text-faint">
                  {failure.source === "ai" ? "suggested by AI" : "suggested by rule"}
                </span>
                {accepted && (
                  <Badge tone="brand">
                    Accepted by {failure.acceptedBy === "ai" ? "AI" : "rule"}
                  </Badge>
                )}
              </>
            )}
            {proven && (
              <>
                <Badge tone="flaky">Proven by experiment</Badge>
                <span className="text-[11px] text-faint">{categoryLabel[category]}</span>
              </>
            )}
            {confirmed && (
              <>
                <Badge tone="brand">Confirmed by human</Badge>
                <span className="text-[11px] text-faint">{categoryLabel[category]}</span>
                {failure.acceptedOutcome === "confirmed" && (
                  <span className="text-[10.5px] text-faint">AI suggestion confirmed</span>
                )}
                {failure.acceptedOutcome === "overridden" && (
                  <span className="text-[10.5px] text-faint">
                    AI suggested {categoryLabel[failure.acceptedCategory || "unclear"]}, overridden
                  </span>
                )}
              </>
            )}
            {needsAI && (
              <span className="text-[11.5px] text-muted">
                No diagnosis yet. Set the cause manually, or run AI first.
              </span>
            )}
          </div>

          {accepted && (
            <p className="mt-1.5 text-[10.5px] text-faint">
              High confidence, so no confirmation is needed. Confirm it only when you want this to
              become human truth for the scoreboard and rule learning.
            </p>
          )}

          <div className="mt-2 flex flex-wrap gap-1.5">
            {canConfirm && (
              <Button
                variant={accepted ? "outline" : "default"}
                onClick={confirm}
                disabled={mutation.isPending}
              >
                <Check className="h-3.5 w-3.5" />
                {mutation.isPending
                  ? "Saving…"
                  : accepted
                    ? "Confirm as truth"
                    : "Confirm diagnosis"}
              </Button>
            )}
            {confirmed || needsAI ? (
              <>
                {needsAI && (
                  <Button
                    variant="default"
                    onClick={() => analyzeMutation.mutate()}
                    disabled={analyzeMutation.isPending}
                    title="Reads the stored failure; it does not rerun the test"
                  >
                    <Sparkles className="h-3.5 w-3.5" />
                    {analyzeMutation.isPending ? "Analyzing…" : "Analyze with AI"}
                  </Button>
                )}
                <Button variant="outline" onClick={() => setEditing(true)}>
                  <Pencil className="h-3 w-3" />
                  {confirmed ? "Edit diagnosis" : "Set cause manually"}
                </Button>
              </>
            ) : (
              <Button variant="outline" onClick={() => setEditing(true)}>
                <Pencil className="h-3 w-3" />
                Change cause
              </Button>
            )}
          </div>
          {needsAI && (
            <p className="mt-1.5 text-[10.5px] text-faint">
              Uses the last stored failure. No test rerun; rerun only if you need fresh facts.
            </p>
          )}
        </div>
      )}

      {editing && (
        <div className="flex flex-col gap-1.5">
          <select
            value={label}
            onChange={(event) => setLabel(event.target.value)}
            className="h-8 rounded-md border border-line bg-canvas px-2 text-xs text-ink outline-none focus-visible:border-brand/60"
            aria-label="Cause"
          >
            {options.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
          <textarea
            value={note}
            onChange={(event) => setNote(event.target.value)}
            rows={2}
            placeholder="Optional note: why is this the cause?"
            className="resize-y rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-ink outline-none placeholder:text-faint focus-visible:border-brand/60"
            aria-label="Diagnosis note"
          />
          <div className="flex items-center gap-2">
            <Button
              variant="default"
              onClick={() => mutation.mutate({ label, note })}
              disabled={mutation.isPending}
            >
              {mutation.isPending ? "Saving…" : "Save cause"}
            </Button>
            <Button
              variant="ghost"
              onClick={() => {
                setLabel(initial);
                setNote(failure.labelNote);
                setEditing(false);
              }}
              disabled={mutation.isPending}
            >
              Cancel
            </Button>
          </div>
        </div>
      )}

      {saved && (
        <p className="mt-2 text-[11px] text-ok">
          Saved as {mutation.data?.by ?? "you"}. This is now the answer key for this failure.
        </p>
      )}
      {analyzeMutation.isSuccess && (
        <p className="mt-2 text-[11px] text-ok">
          {analyzeMutation.data.cached ? "Used the cached AI analysis." : "AI analysis saved."}
        </p>
      )}
      {analyzeMutation.isError && (
        <p className="mt-2 text-[11px] text-danger">
          {analyzeMutation.error instanceof Error
            ? analyzeMutation.error.message
            : "AI analysis failed"}
        </p>
      )}
      {mutation.isError && (
        <p className="mt-2 text-[11px] text-danger">
          {mutation.error instanceof Error ? mutation.error.message : "Failed to save"}
        </p>
      )}
    </section>
  );
}
