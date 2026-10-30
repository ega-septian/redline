import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { api } from "@/api/client";
import { Button } from "@/components/ui/button";
import { categoryLabel, type Category, type Failure } from "@/lib/failure";

const options: Array<{ value: string; label: string }> = [
  { value: "backend_bug", label: categoryLabel.backend_bug },
  { value: "test_bug", label: categoryLabel.test_bug },
  { value: "environment", label: categoryLabel.environment },
  { value: "flaky", label: categoryLabel.flaky },
  { value: "unknown", label: "Unclear" },
];

export function LabelForm({ failure }: { failure: Failure }) {
  const queryClient = useQueryClient();
  const [label, setLabel] = useState(
    failure.category === "unclear" ? "unknown" : (failure.category as Category),
  );
  const [note, setNote] = useState(failure.labelNote);
  const [saved, setSaved] = useState(false);

  const mutation = useMutation({
    mutationFn: () => api.label(failure.fingerprint, { label, note }),
    onSuccess: async () => {
      setSaved(true);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["groups"] }),
        queryClient.invalidateQueries({ queryKey: ["group", failure.fingerprint] }),
        queryClient.invalidateQueries({ queryKey: ["knowledge", failure.fingerprint] }),
      ]);
    },
    onMutate: () => setSaved(false),
  });

  return (
    <section className="border-t border-line-soft pt-3">
      <div className="mb-1.5 flex items-baseline gap-2">
        <span className="text-[10px] uppercase tracking-[0.8px] text-faint">Manual label</span>
        <span className="text-[10px] text-faint">overrides rules and AI</span>
      </div>
      <div className="flex flex-col gap-1.5">
        <select
          value={label}
          onChange={(event) => setLabel(event.target.value)}
          className="h-8 rounded-md border border-line bg-canvas px-2 text-xs text-ink outline-none focus-visible:border-brand/60"
          aria-label="Label"
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
          placeholder="Why? This note is stored with the label."
          className="resize-y rounded-md border border-line bg-canvas px-2 py-1.5 text-xs text-ink outline-none placeholder:text-faint focus-visible:border-brand/60"
          aria-label="Label note"
        />
        <div className="flex items-center gap-2">
          <Button variant="default" onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? "Saving…" : "Save label"}
          </Button>
          {saved && <span className="text-[11px] text-ok">Saved</span>}
          {mutation.isError && (
            <span className="text-[11px] text-danger">
              {mutation.error instanceof Error ? mutation.error.message : "Failed to save"}
            </span>
          )}
        </div>
      </div>
    </section>
  );
}
