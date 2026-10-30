import type { ApiGroup, GroupDetail, Knowledge } from "@/api/types";

export type Category = "backend_bug" | "test_bug" | "environment" | "flaky" | "unclear";
export type FailureStatus = "new" | "recurring" | "regressed";
export type Confidence = "high" | "medium" | "low" | "none";
export type Source = "rule" | "ai" | "experiment" | "human" | "none";
export type RunStatus = "passed" | "failed" | "flaky";

export type FailureRun = {
  runId: number;
  at: string;
  status: RunStatus;
  retries: number;
  source: string;
  branch: string;
  commit: string;
};

export type Failure = {
  fingerprint: string;
  status: FailureStatus;
  title: string;
  test: string;
  file: string;
  category: Category;
  confidence: Confidence;
  occurrences: number;
  lastSeen: string;
  source: Source;
  model?: string;
  summary: string;
  evidence: string[];
  nextStep: string;
  endpoint: string;
  files: string[];
  alsoFails: string;
  reproduce: string;
  cost: string;
  latency: string;
  firstSeen: string;
  labelNote: string;
  runs: FailureRun[];
};

export const categoryLabel: Record<Category, string> = {
  backend_bug: "Backend bug",
  test_bug: "Test bug",
  environment: "Environment",
  flaky: "Flaky",
  unclear: "Unclear",
};

export const statusLabel: Record<FailureStatus, string> = {
  new: "NEW",
  recurring: "RECURRING",
  regressed: "REGRESSED",
};

const categories: Category[] = ["backend_bug", "test_bug", "environment", "flaky"];

function categoryOf(group: ApiGroup, detail?: GroupDetail | null): Category {
  const raw = group.human_label ?? detail?.analysis?.category ?? group.category ?? "";
  return categories.includes(raw as Category) ? (raw as Category) : "unclear";
}

function confidenceOf(group: ApiGroup, detail?: GroupDetail | null): Confidence {
  if (group.human_label) return "high";
  const raw = detail?.analysis?.confidence ?? group.confidence ?? "";
  return raw === "high" || raw === "medium" || raw === "low" ? raw : "none";
}

function statusOf(group: ApiGroup): FailureStatus {
  if (group.regressions > 0) return "regressed";
  return group.occurrences <= 1 ? "new" : "recurring";
}

function when(iso: string | undefined | null): string {
  if (!iso) return "—";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function sourceOf(group: ApiGroup, detail?: GroupDetail | null): Source {
  if (group.human_label) return "human";
  const raw = detail?.analysis?.source ?? group.analysis_source ?? "";
  if (raw === "rule" || raw === "ai" || raw === "experiment" || raw === "human") return raw;
  return "none";
}

function evidenceOf(group: ApiGroup, detail?: GroupDetail | null): string[] {
  const quoted = detail?.analysis?.evidence ?? [];
  if (quoted.length > 0) return quoted.slice(0, 4);
  const lines = (group.sample_error ?? "")
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  if (lines.length > 0) return lines.slice(0, 4);
  return group.summary ? [group.summary] : [];
}

function alsoFailsOf(knowledge?: Knowledge | null): string {
  if (!knowledge) return "—";
  const others = new Set<string>();
  for (const endpoint of knowledge.endpoints) {
    for (const test of endpoint.without.failed) others.add(test);
  }
  for (const file of knowledge.files) {
    for (const test of file.others.failed) others.add(test);
  }
  return others.size > 0 ? `${others.size} other test${others.size === 1 ? "" : "s"} in this run` : "no other test in this run";
}

function runsOf(detail?: GroupDetail | null): FailureRun[] {
  const occurrences = detail?.occurrences ?? [];
  return occurrences
    .map((occurrence) => ({
      runId: occurrence.run_id,
      at: occurrence.run_created_at,
      status:
        occurrence.status === "failed"
          ? ("failed" as const)
          : occurrence.status === "flaky"
            ? ("flaky" as const)
            : ("passed" as const),
      retries: occurrence.retries,
      source: occurrence.source,
      branch: occurrence.branch,
      commit: occurrence.commit,
    }))
    .slice(-20);
}

function titleOf(group: ApiGroup): string {
  const cause = group.cause ?? "";
  // Unanalysed groups store the raw error in cause; show the test title instead.
  if (cause.includes(" → ") || /^[A-Z]+ \//.test(cause)) return cause;
  return group.title;
}

export function toFailure(
  group: ApiGroup,
  detail?: GroupDetail | null,
  knowledge?: Knowledge | null,
): Failure {
  const analysis = detail?.analysis;
  return {
    fingerprint: group.fingerprint,
    status: statusOf(group),
    title: titleOf(group),
    test: group.title,
    file: group.file,
    category: categoryOf(group, detail),
    confidence: confidenceOf(group, detail),
    occurrences: group.occurrences,
    lastSeen: when(group.last_seen_at),
    source: sourceOf(group, detail),
    model: analysis?.model,
    summary:
      group.human_label && group.human_note
        ? group.human_note
        : analysis?.summary || group.analysis_summary || group.summary || "No analysis yet.",
    evidence: evidenceOf(group, detail),
    nextStep:
      analysis?.next_step ||
      "Open the run to see the error, or analyze this group from the CLI for a cause.",
    endpoint: group.cause?.split(" → ")[0] || knowledge?.endpoints[0]?.key || "—",
    files: knowledge?.files.map((file) => file.path) ?? [group.file],
    alsoFails: alsoFailsOf(knowledge),
    reproduce: group.sample_error || group.summary || "No error text stored.",
    cost: analysis?.cost_usd ? `$${analysis.cost_usd.toFixed(5)}` : "—",
    latency: "—",
    firstSeen: when(group.first_seen_at),
    labelNote: group.human_note ?? "",
    runs: runsOf(detail),
  };
}
