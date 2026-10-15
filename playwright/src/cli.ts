#!/usr/bin/env node
/**
 * CLI Redline: membuktikan penyebab kegagalan dengan eksperimen, dan mengajari Redline aturan baru.
 *
 *   npx redline verify                 eksperimen untuk kegagalan run terakhir
 *   npx redline verify <id>            eksperimen untuk satu kelompok (fingerprint)
 *   npx redline learn                  AI mengusulkan aturan dari kasus yang sudah terbukti
 *   npx redline rules                  daftar aturan
 *   npx redline approve <id>           aktifkan aturan (reject <id> untuk menolak)
 *   npx redline score                  rapor: seberapa sering tebakan aturan dan AI terbukti benar
 *   npx redline impact <file>          test yang terdampak kalau file ini berubah (import bertingkat ikut)
 *   npx redline impact GET /brands/1   test yang memanggil endpoint ini
 *
 * Sebelum eksperimen, tebakan Redline dicatat dulu. Setelah terbukti, tebakan itu dinilai benar atau salah.
 * Kegagalan yang sudah terbukti dilewati; pakai `verify --force` untuk mengulang.
 *
 * Eksperimen dijalankan di SALINAN project (folder sementara), jadi kode kamu tidak pernah diubah.
 *   1. rerun: test dijalankan ulang apa adanya. Lulus = tidak konsisten (flaky), berhenti di sini.
 *   2. patch: AI membuat hipotesis + patch, test dijalankan dengan patch itu. Lulus = hipotesis terbukti.
 *      Patch yang menghapus assertion, menambah skip/only, try/catch, atau retry/timeout ditolak otomatis.
 *
 * Env (opsional):
 *   REDLINE_URL               default dari redline-last-run.json, lalu http://localhost:8787
 *   REDLINE_API_KEY           kalau server Redline memakai API key (dibaca juga dari .env)
 *   REDLINE_VERIFY_MAX        kegagalan yang diuji per perintah, default 3
 *   REDLINE_VERIFY_ATTEMPTS   percobaan patch per kegagalan, default 2
 *   REDLINE_RERUNS            berapa kali rerun, default 2
 *   REDLINE_KEEP_SANDBOX=1    jangan hapus folder salinan (untuk debug)
 */
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { userInfo } from "node:os";
import path from "node:path";
import dotenv from "dotenv";
import { git, makeSandbox, resetSandbox } from "./sandbox";

const ROOT = process.cwd();
// REDLINE_API_KEY dan REDLINE_URL dari .env project. Nilai dari perintah tidak ditimpa.
dotenv.config({ path: path.join(ROOT, ".env"), quiet: true });
const RESULTS_DIR = path.join(ROOT, "test-results");
const LAST_RUN = path.join(RESULTS_DIR, "redline-last-run.json");

type Failure = {
  fingerprint: string;
  test: string;
  id?: string;
  status?: string;
  affected?: number;
};
type SourceFile = { path: string; content: string };
type Edit = { path: string; find: string; replace: string };
type Fix = {
  category: string;
  confidence: string;
  hypothesis: string;
  edits: Edit[];
  cost_usd: number;
};
type Attempt = { hypothesis: string; patch: string; result: string };
type RunResult = { runs: number; passes: number; error: string };
type Rule = {
  id: number;
  pattern: string;
  category: string;
  summary: string;
  status: string;
  hits: number;
  also_matches: number;
  learned_from: string[];
};

const env = (name: string, fallback: number) => Number(process.env[name] || fallback);

function lastRun(): { url?: string; failures: Failure[] } {
  if (!existsSync(LAST_RUN)) return { failures: [] };
  return JSON.parse(readFileSync(LAST_RUN, "utf8"));
}

function baseURL(): string {
  return (process.env.REDLINE_URL || lastRun().url || "http://localhost:8787").replace(/\/$/, "");
}

function who(): string {
  if (process.env.REDLINE_TRIGGERED_BY) return process.env.REDLINE_TRIGGERED_BY;
  try {
    return userInfo().username;
  } catch {
    return "unknown";
  }
}

async function api<T>(method: string, url: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(baseURL() + url, {
      method,
      headers: {
        ...(body ? { "Content-Type": "application/json" } : {}),
        ...(process.env.REDLINE_API_KEY
          ? { Authorization: `Bearer ${process.env.REDLINE_API_KEY}` }
          : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
      signal: AbortSignal.timeout(180_000),
    });
  } catch {
    throw new Error(`server Redline tidak bisa dihubungi di ${baseURL()}`);
  }
  const data = (await res.json().catch(() => ({}))) as T & { error?: string };
  if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
  return data;
}

// ---------- verify ----------

async function verify(args: string[]) {
  const force = args.includes("--force");
  const fingerprints = args.filter((a) => a !== "--force");
  let targets: Failure[];
  if (fingerprints.length > 0) {
    const known = new Map(lastRun().failures.map((f) => [f.fingerprint, f]));
    targets = [];
    for (const fp of fingerprints) {
      const f = known.get(fp) ?? (await groupAsFailure(fp));
      targets.push(f);
    }
  } else {
    targets = lastRun().failures;
    if (targets.length === 0) {
      console.log("Tidak ada kegagalan di run terakhir. Jalankan test dulu (npx playwright test).");
      return;
    }
  }
  const max = env("REDLINE_VERIFY_MAX", 3);
  if (targets.length > max) {
    console.log(
      `${targets.length} kegagalan, yang diuji ${max} pertama (atur REDLINE_VERIFY_MAX).`,
    );
    targets = targets.slice(0, max);
  }
  let cost = 0;
  for (const [i, f] of targets.entries()) {
    console.log(
      `\n━━ ${i + 1}/${targets.length} ${f.id && f.id !== "-" ? f.id + " " : ""}${f.test}`,
    );
    try {
      cost += await verifyOne(f, force);
    } catch (e) {
      console.log(`  ✗ eksperimen gagal dijalankan: ${(e as Error).message}`);
    }
  }
  if (cost > 0) console.log(`\nBiaya AI: $${cost.toFixed(5)}`);
}

async function groupAsFailure(fp: string): Promise<Failure> {
  const g = await api<{ group: { test: string } }>("GET", `/api/groups/${fp}`);
  return { fingerprint: fp, test: g.group.test };
}

/** "project › file › describe › judul" */
function parseTestKey(key: string) {
  const [project, file, ...title] = key.split(" › ");
  if (!project || !file || title.length === 0)
    throw new Error(`format nama test tidak dikenal: ${key}`);
  return { project, file, title: title.join(" ") };
}

/** Path file di laporan relatif ke rootDir Playwright (testDir); ubah jadi relatif ke project. */
function specPath(file: string): string {
  const dirs = [ROOT];
  const results = path.join(RESULTS_DIR, "results.json");
  if (existsSync(results)) {
    const rootDir = JSON.parse(readFileSync(results, "utf8")).config?.rootDir;
    if (rootDir) dirs.unshift(rootDir);
  }
  for (const dir of dirs) {
    const abs = path.join(dir, file);
    if (existsSync(abs)) return path.relative(ROOT, abs).split(path.sep).join("/");
  }
  throw new Error(`file ${file} tidak ada di project ini`);
}

async function verifyOne(f: Failure, force: boolean): Promise<number> {
  const { project, title, ...key } = parseTestKey(f.test);
  const file = specPath(key.file);

  // 0. Tebakan Redline SEBELUM eksperimen, supaya nanti bisa dinilai benar atau salah.
  let cost = 0;
  const guess = await guessFirst(f.fingerprint);
  if (guess?.analysis) {
    const a = guess.analysis;
    if (a.source === "experiment" && !force) {
      console.log(`  sudah terbukti sebelumnya: ${categoryName(a.category)}. ${a.summary}`);
      console.log(`  (ulangi eksperimen dengan: npx redline verify --force)`);
      return 0;
    }
    if (!guess.cached && a.source === "ai") cost += a.cost_usd;
    console.log(
      `  0. tebakan Redline: ${categoryName(a.category)} (${sourceName(a.source)}, ${a.confidence})`,
    );
    console.log(`      ${a.summary}`);
    for (const c of a.similar ?? []) {
      const title = c.test.split(" › ").slice(2).join(" › ");
      console.log(
        `      mirip (${c.similarity.toFixed(2)}): ${title} → terbukti ${categoryName(c.category)}`,
      );
    }
  } else if (guess?.error) {
    console.log(`  0. tebakan Redline: - (${guess.error})`);
  }

  const sandbox = makeSandbox(ROOT);
  try {
    const run = (repeat: number) => runTest(sandbox, project, file, title, repeat);

    // 1. Rerun apa adanya.
    const reruns = env("REDLINE_RERUNS", 2);
    process.stdout.write(`  1. rerun ${reruns}x tanpa perubahan… `);
    const rerun = run(reruns);
    const flaky = rerun.passes > 0;
    console.log(
      flaky ? `lulus ${rerun.passes}/${rerun.runs}` : `gagal ${rerun.runs}/${rerun.runs}`,
    );
    const rerunSaved = await saveExperiment(f.fingerprint, {
      kind: "rerun",
      outcome: flaky ? "passed" : "failed",
      category: flaky ? "flaky" : "unknown",
      hypothesis: "Kegagalan bisa direproduksi tanpa perubahan apa pun.",
      detail: rerun.error,
      runs: rerun.runs,
      passes: rerun.passes,
    });
    if (flaky) {
      console.log(
        `  ⚠ TIDAK KONSISTEN: test lulus saat diulang. Kemungkinan flaky atau kondisi sesaat.`,
      );
      printVerdicts(rerunSaved.verdicts);
      return cost;
    }

    // 2. Patch dari AI, beberapa percobaan.
    const files = collectSources(file);
    const attempts: Attempt[] = [];
    const maxAttempts = env("REDLINE_VERIFY_ATTEMPTS", 2);
    for (let n = 1; n <= maxAttempts; n++) {
      process.stdout.write(`  2.${n} AI membuat hipotesis… `);
      const fix = await api<Fix>("POST", `/api/groups/${f.fingerprint}/fix`, { files, attempts });
      cost += fix.cost_usd;
      console.log(`${fix.category} (${fix.confidence})`);
      console.log(`      ${fix.hypothesis}`);

      if (fix.edits.length === 0) {
        console.log(
          `  ○ AI menilai ini bukan salah test, jadi tidak bisa dibuktikan dengan patch test.`,
        );
        await saveExperiment(f.fingerprint, {
          kind: "patch",
          outcome: "skipped",
          category: fix.category,
          hypothesis: fix.hypothesis,
          runs: 1,
          passes: 0,
          cost_usd: fix.cost_usd,
        });
        return cost;
      }

      const applied = applyEdits(sandbox, files, fix.edits);
      if (typeof applied === "string") {
        console.log(`      ✗ patch ditolak: ${applied}`);
        attempts.push({
          hypothesis: fix.hypothesis,
          patch: JSON.stringify(fix.edits),
          result: `ditolak: ${applied}`,
        });
        await saveExperiment(f.fingerprint, {
          kind: "patch",
          outcome: "rejected",
          category: fix.category,
          hypothesis: fix.hypothesis,
          detail: applied,
          runs: 1,
          passes: 0,
          cost_usd: fix.cost_usd,
        });
        resetSandbox(sandbox);
        continue;
      }

      const patch = git(sandbox, ["diff", "--no-color"]);
      process.stdout.write(`      menjalankan test dengan patch… `);
      const result = run(1);
      const passed = result.passes === result.runs && result.runs > 0;
      console.log(passed ? "LULUS" : "masih gagal");
      const saved = await saveExperiment(f.fingerprint, {
        kind: "patch",
        outcome: passed ? "passed" : "failed",
        category: fix.category,
        hypothesis: fix.hypothesis,
        patch,
        detail: result.error,
        runs: result.runs,
        passes: result.passes,
        cost_usd: fix.cost_usd,
      });
      if (passed) {
        const out = path.join(RESULTS_DIR, "redline", `${f.fingerprint}.patch`);
        mkdirSync(path.dirname(out), { recursive: true });
        writeFileSync(out, patch);
        console.log(`  ✓ TERBUKTI: test lulus setelah patch.\n`);
        console.log(indent(patch, "      "));
        console.log(`\n      Terapkan: git apply ${path.relative(ROOT, out)}`);
        printVerdicts(saved.verdicts);
        return cost;
      }
      attempts.push({ hypothesis: fix.hypothesis, patch, result: result.error });
      resetSandbox(sandbox);
    }
    console.log(
      `  ✗ Belum terbukti setelah ${maxAttempts} percobaan. Hipotesis yang gagal tersimpan di Redline.`,
    );
    return cost;
  } finally {
    if (process.env.REDLINE_KEEP_SANDBOX === "1") console.log(`  (salinan disimpan di ${sandbox})`);
    else rmSync(sandbox, { recursive: true, force: true });
  }
}

type Verdict = {
  source: string;
  predicted: string;
  confidence: string;
  truth: string;
  correct: boolean;
};

async function saveExperiment(fp: string, e: Record<string, unknown>) {
  return api<{ id: number; verdicts: Verdict[] }>("POST", `/api/groups/${fp}/experiments`, {
    run_by: who(),
    ...e,
  });
}

type Analysis = {
  source: string;
  category: string;
  confidence: string;
  summary: string;
  cost_usd: number;
  similar?: { test: string; category: string; similarity: number }[];
};

/** Minta analisis (aturan, lalu AI kalau perlu). Gagal di sini tidak menghentikan eksperimen. */
async function guessFirst(
  fp: string,
): Promise<{ analysis?: Analysis; cached?: boolean; error?: string }> {
  try {
    return await api("POST", `/api/groups/${fp}/analyze`);
  } catch (e) {
    return { error: (e as Error).message };
  }
}

/** Hasil nyata: tebakan sebelum eksperimen dibanding bukti. */
function printVerdicts(verdicts: Verdict[]) {
  if (verdicts.length === 0) return;
  console.log(`\n  Tebakan sebelumnya vs bukti:`);
  for (const v of verdicts) {
    const mark = v.correct
      ? "✅ benar"
      : v.predicted === "unknown"
        ? "➖ tidak menebak"
        : `❌ salah (terbukti ${categoryName(v.truth)})`;
    console.log(
      `    ${sourceName(v.source)}: ${categoryName(v.predicted)} (${v.confidence}) → ${mark}`,
    );
  }
}

const categoryNames: Record<string, string> = {
  backend_bug: "BUG BACKEND",
  test_bug: "BUG DI TEST",
  environment: "ENVIRONMENT",
  flaky: "FLAKY",
  unknown: "BELUM JELAS",
};
const categoryName = (c: string) => categoryNames[c] ?? c;
const sourceNames: Record<string, string> = {
  rule: "aturan",
  ai: "AI",
  human: "label manual",
  experiment: "eksperimen",
};
const sourceName = (s: string) => sourceNames[s] ?? s;

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** Jalankan satu test di salinan, tanpa reporter Redline, lalu hitung hasilnya dari JSON. */
function runTest(
  dir: string,
  project: string,
  file: string,
  title: string,
  repeat: number,
): RunResult {
  const out = path.join(dir, "redline-verify.json");
  rmSync(out, { force: true });
  const r = spawnSync(
    "npx",
    [
      "playwright",
      "test",
      file,
      `--project=${project}`,
      `--grep=${escapeRegex(title)}`,
      "--retries=0",
      `--repeat-each=${repeat}`,
      "--workers=1",
      "--reporter=json",
    ],
    {
      cwd: dir,
      encoding: "utf8",
      timeout: 5 * 60_000,
      env: { ...process.env, REDLINE: "0", PLAYWRIGHT_JSON_OUTPUT_FILE: out },
    },
  );
  if (!existsSync(out))
    throw new Error(
      `playwright tidak menghasilkan laporan: ${(r.stderr || r.stdout).slice(0, 500)}`,
    );
  const report = JSON.parse(readFileSync(out, "utf8"));
  let runs = 0;
  let passes = 0;
  let error = "";
  const walk = (suite: { suites?: unknown[]; specs?: unknown[] }) => {
    for (const s of (suite.suites ?? []) as (typeof suite)[]) walk(s);
    for (const spec of (suite.specs ?? []) as {
      tests: { results: { status: string; error?: { message?: string } }[] }[];
    }[]) {
      for (const t of spec.tests) {
        for (const res of t.results) {
          if (res.status === "skipped") continue;
          runs++;
          if (res.status === "passed") passes++;
          else if (!error) error = stripAnsi(res.error?.message ?? res.status);
        }
      }
    }
  };
  for (const s of report.suites ?? []) walk(s);
  if (runs === 0) throw new Error(`test tidak ditemukan: ${title}`);
  return { runs, passes, error: error.slice(0, 3000) };
}

function stripAnsi(s: string): string {
  // eslint-disable-next-line no-control-regex
  return s.replace(/\x1b\[[0-9;]*m/g, "");
}

/** File spec + file lokal yang di-import (relatif), maksimal 8 file, terdekat dulu. */
function collectSources(specFile: string): SourceFile[] {
  const out: SourceFile[] = [];
  const seen = new Set<string>();
  const queue = [specFile];
  while (queue.length > 0 && out.length < 8) {
    const rel = queue.shift()!;
    if (seen.has(rel)) continue;
    seen.add(rel);
    const content = readFileSync(path.join(ROOT, rel), "utf8");
    if (content.length > 30_000) continue;
    out.push({ path: rel, content });
    for (const m of content.matchAll(/(?:from\s*|import\s*\(?\s*)["'](\.{1,2}\/[^"']+)["']/g)) {
      const found = resolveImport(path.dirname(rel), m[1]);
      if (found) queue.push(found);
    }
  }
  return out;
}

function resolveImport(dir: string, spec: string): string | null {
  const base = path.join(dir, spec);
  for (const c of [base, `${base}.ts`, `${base}.tsx`, `${base}.js`, path.join(base, "index.ts")]) {
    const abs = path.join(ROOT, c);
    if (existsSync(abs) && statSync(abs).isFile()) return c.split(path.sep).join("/");
  }
  return null;
}

// ---------- penjaga patch ----------

const count = (s: string, re: RegExp) => (s.match(re) ?? []).length;

/** Patch tidak boleh membuat test "lulus dengan curang". Mengembalikan alasan penolakan, atau "". */
function guard(before: string, after: string): string {
  const mustNotDecrease: [string, RegExp][] = [
    ["expect", /\bexpect(?:\.soft)?\s*\(/g],
    ["matcher (toBe, toEqual, ...)", /\.(?:not\.)?to[A-Z]\w*\s*\(/g],
    ["validasi schema (.parse)", /\.(?:safeParse|parse)\s*\(/g],
  ];
  for (const [name, re] of mustNotDecrease) {
    if (count(after, re) < count(before, re)) return `jumlah ${name} berkurang`;
  }
  const mustNotIncrease: [string, RegExp][] = [
    ["skip/fixme/only/fail", /\.(?:skip|fixme|only|fail)\s*\(/g],
    ["try/catch", /\btry\s*\{/g],
    ["retry/timeout", /\b(?:retries|retry|setTimeout|timeout)\b/gi],
    ["teks yang disamarkan", /\[(?:REDACTED|JWT|NIK\/16-DIGIT)\]/g],
  ];
  for (const [name, re] of mustNotIncrease) {
    if (count(after, re) > count(before, re)) return `menambah ${name}`;
  }
  return "";
}

/** Terapkan edit AI ke salinan. Mengembalikan alasan penolakan (string) atau true. */
function applyEdits(dir: string, files: SourceFile[], edits: Edit[]): true | string {
  const byPath = new Map(files.map((f) => [f.path, f.content]));
  const changed = new Map<string, string>();
  for (const e of edits) {
    const current = changed.get(e.path) ?? byPath.get(e.path);
    if (current === undefined) return `file ${e.path} tidak termasuk file test yang dikirim`;
    const hits = current.split(e.find).length - 1;
    if (hits !== 1) return `teks yang dicari muncul ${hits}x di ${e.path} (harus tepat 1)`;
    changed.set(
      e.path,
      current.replace(e.find, () => e.replace),
    );
  }
  for (const [p, after] of changed) {
    const reason = guard(byPath.get(p)!, after);
    if (reason) return `${p}: ${reason}`;
  }
  for (const [p, after] of changed) writeFileSync(path.join(dir, p), after);
  return true;
}

function indent(s: string, pad: string): string {
  return s
    .trimEnd()
    .split("\n")
    .map((l) => pad + l)
    .join("\n");
}

// ---------- aturan ----------

async function learn() {
  console.log("AI mempelajari kasus yang sudah terbukti (label manual + eksperimen yang lulus)…\n");
  const res = await api<{
    cases: number;
    proposed: Rule[];
    rejected: { pattern: string; category: string; reason: string }[];
    cost_usd?: number;
    message?: string;
  }>("POST", "/api/rules/propose");
  if (res.message) {
    console.log(res.message);
    return;
  }
  console.log(`${res.cases} kasus belum ditangani aturan.\n`);
  for (const r of res.proposed) printRule(r);
  for (const r of res.rejected)
    console.log(`  ✗ ditolak  /${r.pattern}/ → ${r.category}\n             ${r.reason}\n`);
  if (res.proposed.length > 0)
    console.log(`Review usulan di atas, lalu: npx redline approve <id>  (atau reject <id>)`);
  if (res.cost_usd) console.log(`Biaya AI: $${res.cost_usd.toFixed(5)}`);
}

function printRule(r: Rule) {
  const mark = { proposed: "?", active: "✓", rejected: "✗" }[r.status] ?? " ";
  console.log(`  ${mark} #${r.id} [${r.status}] /${r.pattern}/ → ${r.category}`);
  console.log(`      ${r.summary}`);
  console.log(
    `      uji ke data lama: cocok ${r.hits} kasus terbukti, ikut cocok ${r.also_matches} kelompok lain\n`,
  );
}

async function score() {
  const board = await api<{
    sources: {
      source: string;
      graded: number;
      correct: number;
      unknown: number;
      accuracy: number;
    }[];
    entries: (Verdict & { test: string; truth_by: string })[];
  }>("GET", "/api/scoreboard?limit=15");
  console.log("Rapor tebakan Redline (dibanding bukti eksperimen atau label manual)\n");
  for (const s of board.sources) {
    const pct = s.graded > 0 ? `${Math.round(s.accuracy * 100)}%` : "-";
    const unknown = s.unknown > 0 ? `, ${s.unknown} tidak menebak` : "";
    console.log(
      `  ${sourceName(s.source).padEnd(7)} ${s.correct}/${s.graded} benar (${pct})${unknown}`,
    );
  }
  if (board.entries.length === 0) {
    console.log("\nBelum ada tebakan yang bisa dinilai. Jalankan: npx redline verify");
    return;
  }
  console.log("\nTerbaru:");
  for (const e of board.entries) {
    const mark = e.correct ? "✅" : e.predicted === "unknown" ? "➖" : "❌";
    const title = e.test.split(" › ").slice(2).join(" › ");
    console.log(
      `  ${mark} ${sourceName(e.source).padEnd(7)} ${categoryName(e.predicted).padEnd(12)} terbukti ${categoryName(e.truth).padEnd(12)} (${e.truth_by})  ${title}`,
    );
  }
  console.log("\nCatatan: angka baru bermakna setelah ada puluhan kasus terbukti.");
}

async function rules() {
  const list = await api<Rule[]>("GET", "/api/rules");
  if (list.length === 0) console.log("Belum ada aturan. Jalankan: npx redline learn");
  for (const r of list) printRule(r);
}

async function decide(id: string | undefined, status: "active" | "rejected") {
  if (!id || !/^\d+$/.test(id))
    throw new Error("tulis id aturan, misalnya: npx redline approve 3");
  const r = await api<Rule>("PUT", `/api/rules/${id}`, { status, by: who() });
  printRule(r);
  if (status === "active")
    console.log("Aturan aktif: kegagalan dengan pola ini sekarang diputuskan tanpa AI.");
}

/** Test yang terdampak kalau file atau endpoint berubah, dari graph Redline (run lulus terakhir tiap test). */
async function impact(args: string[]) {
  const q = args.join(" ").trim();
  if (!q)
    throw new Error(
      "tulis file atau endpoint, misalnya: npx redline impact schemas/brand.ts",
    );
  const isEndpoint = q.startsWith("/") || /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s/i.test(q);
  const res = await api<{ tests: { test: string; test_case_id?: string; run_id: number }[] }>(
    "GET",
    `/api/impact?${new URLSearchParams({ [isEndpoint ? "endpoint" : "file"]: q })}`,
  );
  if (res.tests.length === 0) {
    console.log(
      `Tidak ada test yang ${isEndpoint ? "memanggil" : "memakai"} ${q} (menurut run yang sudah dikirim ke Redline).`,
    );
    return;
  }
  console.log(`${res.tests.length} test ${isEndpoint ? "memanggil" : "bergantung pada"} ${q}:\n`);
  for (const t of res.tests) {
    console.log(`  ${(t.test_case_id ?? "").padEnd(14)} ${t.test}`);
  }
  const ids = res.tests.map((t) => t.test_case_id).filter(Boolean);
  if (ids.length > 0)
    console.log(`\nJalankan hanya test ini: npx playwright test --grep "${ids.join("|")}"`);
}

// ---------- main ----------

async function main() {
  const [cmd, ...args] = process.argv.slice(2);
  switch (cmd) {
    case "verify":
      return verify(args);
    case "learn":
      return learn();
    case "rules":
      return rules();
    case "score":
      return score();
    case "approve":
      return decide(args[0], "active");
    case "reject":
      return decide(args[0], "rejected");
    case "impact":
      return impact(args);
    default:
      console.log(
        readFileSync(__filename, "utf8")
          .slice(readFileSync(__filename, "utf8").indexOf("/**"))
          .split("*/")[0]
          .replace(/^\/\*\*|^ \* ?/gm, ""),
      );
  }
}

main().catch((e) => {
  console.error(`[redline] ${(e as Error).message}`);
  process.exit(1);
});
