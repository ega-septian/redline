/**
 * Tambahkan analisis Redline ke hasil Allure (allure-results) test yang gagal: label "redline" untuk halaman
 * Categories (lihat allurerc.mjs), penjelasan di description, dan patch yang terbukti sebagai lampiran.
 * Dipanggil reporter Redline setelah analisis selesai, sebelum laporan Allure dibuat.
 */
import { randomUUID } from "node:crypto";
import { existsSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";

export type RedlineVerdict = {
  category: string;
  confidence: string;
  source: string;
  summary: string;
  evidence: string[];
  next_step: string;
  patch?: string;
};

/** Satu test yang gagal: kunci test Redline ("project › file › judul"), ID test case, dan analisisnya. */
export type FailedTest = { key: string; caseId?: string; verdict: RedlineVerdict };

type Label = { name: string; value: string };
type AllureResult = {
  status: string;
  fullName?: string;
  description?: string;
  labels?: Label[];
  parameters?: { name: string; value: string }[];
  attachments?: { name: string; source: string; type: string }[];
};

const CATEGORY_NAMES: Record<string, string> = {
  backend_bug: "Backend bug",
  test_bug: "Test bug",
  environment: "Environment",
  flaky: "Flaky",
  unknown: "Unclear",
};
const SOURCE_NAMES: Record<string, string> = {
  rule: "rules",
  ai: "AI",
  human: "a human label",
  experiment: "an experiment (proven)",
};

/** Penjelasan Redline dalam Markdown. */
export function describe(v: RedlineVerdict): string {
  const lines = [
    `**Redline: ${CATEGORY_NAMES[v.category] ?? v.category}** (${v.confidence} confidence, by ${SOURCE_NAMES[v.source] ?? v.source})`,
    "",
    v.summary,
  ];
  if (v.evidence.length > 0) {
    lines.push("", "Evidence:", ...v.evidence.map((e) => `- \`${e.replace(/`/g, "'")}\``));
  }
  if (v.next_step) lines.push("", `Next step: ${v.next_step}`);
  return lines.join("\n");
}

/**
 * Perkaya hasil Allure yang gagal dengan analisis Redline. Test dicocokkan lewat tag @TC- (unik, lihat
 * check:ids), atau lewat "project › fullName" kalau test tidak punya ID. Mengembalikan jumlah test yang diubah.
 */
export function enrichAllureResults(resultsDir: string, failed: FailedTest[]): number {
  if (!existsSync(resultsDir) || failed.length === 0) return 0;
  const byCase = new Map(failed.filter((f) => f.caseId).map((f) => [f.caseId!, f]));
  const byKey = new Map(failed.map((f) => [f.key, f]));
  let changed = 0;
  for (const file of readdirSync(resultsDir).filter((f) => f.endsWith("-result.json"))) {
    const full = path.join(resultsDir, file);
    const r = JSON.parse(readFileSync(full, "utf8")) as AllureResult;
    if (r.status !== "failed" && r.status !== "broken") continue;
    const tag = r.labels?.find((l) => l.name === "tag" && /^TC-/i.test(l.value))?.value;
    const project = r.parameters?.find((p) => p.name === "Project")?.value;
    const match = (tag && byCase.get(tag)) || byKey.get(`${project} › ${r.fullName}`);
    if (!match) continue;
    const v = match.verdict;
    r.labels = [
      ...(r.labels ?? []).filter((l) => !l.name.startsWith("redline")),
      { name: "redline", value: v.category },
      { name: "redline_confidence", value: v.confidence },
    ];
    r.description = [describe(v), r.description].filter(Boolean).join("\n\n---\n\n");
    if (v.patch) {
      const source = `${randomUUID()}-attachment.diff`;
      writeFileSync(path.join(resultsDir, source), v.patch);
      r.attachments = [
        ...(r.attachments ?? []),
        { name: "redline.patch", source, type: "text/x-diff" },
      ];
    }
    writeFileSync(full, JSON.stringify(r));
    changed++;
  }
  return changed;
}
