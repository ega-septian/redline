import { useQuery } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { useState, type FormEvent } from "react";

import { api } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

type Kind = "file" | "endpoint";

export function ImpactPage() {
  const [kind, setKind] = useState<Kind>("file");
  const [input, setInput] = useState("");
  const [query, setQuery] = useState("");

  const impactQuery = useQuery({
    queryKey: ["impact", kind, query],
    queryFn: () => api.impact(kind, query),
    enabled: query !== "",
    retry: false,
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    setQuery(input.trim());
  }

  const tests = impactQuery.data?.tests ?? [];

  return (
    <>
      <Card className="p-4">
        <div className="text-sm font-semibold">What does this change affect?</div>
        <p className="mt-1 text-xs text-muted">
          Find the tests that import a file (including transitive imports) or call an endpoint.
        </p>
        <form onSubmit={submit} className="mt-3 flex flex-col gap-2 sm:flex-row">
          <select
            value={kind}
            onChange={(event) => setKind(event.target.value as Kind)}
            className="h-8 rounded-md border border-line bg-canvas px-2 text-xs text-ink outline-none focus-visible:border-brand/60"
            aria-label="Impact query type"
          >
            <option value="file">File</option>
            <option value="endpoint">Endpoint</option>
          </select>
          <div className="flex min-w-0 flex-1 items-center gap-1.5 rounded-md border border-line bg-canvas px-2.5 text-faint">
            <Search className="h-3.5 w-3.5" />
            <Input
              value={input}
              onChange={(event) => setInput(event.target.value)}
              placeholder={
                kind === "file" ? "schemas/brand.ts or brand.spec.ts" : "GET /brands/{brandId}"
              }
              className="h-8 border-0 bg-transparent px-0 focus-visible:ring-0"
              aria-label="Impact query"
            />
          </div>
          <Button variant="default" type="submit" disabled={input.trim() === ""}>
            Check impact
          </Button>
        </form>
        <div className="mt-2 text-[11px] text-faint">
          Files match a path or its suffix. Endpoints accept a method; ids in a path become{" "}
          <span className="font-mono">:id</span>.
        </div>
      </Card>

      {impactQuery.isFetching && (
        <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-muted">
          Looking for affected tests…
        </div>
      )}

      {impactQuery.isError && (
        <div className="rounded-[10px] border border-line bg-surface p-6 text-sm text-danger">
          {impactQuery.error instanceof Error
            ? impactQuery.error.message
            : "The impact query failed."}
        </div>
      )}

      {impactQuery.isSuccess && (
        <div className="overflow-hidden rounded-[10px] border border-line bg-surface">
          <div className="flex items-center justify-between border-b border-line bg-[#12171e] px-3.5 py-2.5">
            <span className="text-[10.5px] uppercase tracking-[0.7px] text-faint">
              {tests.length} affected test{tests.length === 1 ? "" : "s"}
            </span>
            <span className="font-mono text-[11px] text-faint">
              {impactQuery.data.file || impactQuery.data.endpoint}
            </span>
          </div>
          {tests.length === 0 ? (
            <div className="px-4 py-10 text-center text-sm text-muted">
              No test is connected to this {kind} in the graph yet. The fixture must be enabled so
              Redline can record imports and calls.
            </div>
          ) : (
            tests.map((test) => (
              <div
                key={`${test.run_id}-${test.test}`}
                className="flex flex-col gap-1 border-b border-line-soft px-3.5 py-3 text-xs last:border-b-0 sm:flex-row sm:items-center sm:justify-between"
              >
                <div className="min-w-0">
                  <div className="truncate text-[13px] text-ink">{test.test}</div>
                  <div className="mt-0.5 font-mono text-[11px] text-faint">
                    {test.test_case_id ? `${test.test_case_id} · ` : ""}run #{test.run_id}
                  </div>
                </div>
                <span className="text-faint">last relevant run</span>
              </div>
            ))
          )}
        </div>
      )}

      {!query && (
        <div className="rounded-[10px] border border-dashed border-line bg-surface p-6 text-sm text-muted">
          Enter a file or endpoint above. Results come from <span className="font-mono">GET /api/impact</span>.
        </div>
      )}
    </>
  );
}
