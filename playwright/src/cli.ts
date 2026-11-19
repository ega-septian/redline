#!/usr/bin/env node
/**
 * CLI Redline: membuktikan penyebab kegagalan dengan eksperimen, dan mengajari Redline aturan baru.
 *
 *   npx redline                        status singkat: server, versi, contract, AI, command
 *   npx redline doctor                 cek setup server, project dan AI
 *   npx redline doctor --json          output untuk automation/UI
 *   npx redline doctor --strict        warning dianggap gagal (untuk CI)
 *   npx redline verify                 eksperimen untuk kegagalan run terakhir
 *   npx redline verify <id>            eksperimen untuk satu kelompok (fingerprint)
 *   npx redline fix <id>               buktikan fix, lalu buka PR dari patch yang lulus
 *   npx redline fix <id> --dry-run     tampilkan branch dan PR body tanpa push
 *   npx redline fix <id> --force       buktikan ulang walau sudah ada eksperimen yang lulus
 *   npx redline fix <id> --base=dev    PR ke branch lain (default main)
 *   npx redline learn                  promosikan candidate yang terbukti, lalu AI mengusulkan aturan baru
 *   npx redline candidates             daftar rule candidate (draft/proposed/rejected)
 *   npx redline analyze --run <id>     analisis failure pada run yang sudah ada (butuh AI di server)
 *   npx redline analyze --run <id> --rules  analisis lokal tanpa AI (rule + needs_ai)
 *   npx redline rules                  daftar aturan
 *   npx redline shadow <id>            jalankan aturan dalam mode shadow (tanpa mengubah hasil)
 *   npx redline shadow --stats         agreement shadow rule vs human label (--project=<name> scopes it)
 *   npx redline shadow --promote <id>  usulkan rule shadow menjadi proposed (butuh match/agreement)
 *   npx redline approve <id>           aktifkan aturan (reject <id> untuk menolak)
 *   npx redline score                  rapor: seberapa sering tebakan aturan dan AI terbukti benar
 *   npx redline score --project=name   kalibrasi auto-accept untuk satu project
 *   npx redline impact <file>          test yang terdampak kalau file ini berubah (import bertingkat ikut)
 *   npx redline impact GET /brands/1   test yang memanggil endpoint ini
 *   npx redline comment                tulis/update komentar PR dengan redline-report.md
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
 *   REDLINE_APP_URL           baseURL aplikasi yang dicek `doctor` (opsional)
 *   REDLINE_VERIFY_MAX        kegagalan yang diuji per perintah, default 3
 *   REDLINE_VERIFY_ATTEMPTS   percobaan patch per kegagalan, default 2
 *   REDLINE_RERUNS            berapa kali rerun, default 2
 *   REDLINE_KEEP_SANDBOX=1    jangan hapus folder salinan (untuk debug)
 *   GITHUB_TOKEN              token dengan izin pull-requests:write, untuk `comment` dan `fix`
 *   GITHUB_REPOSITORY         owner/repo, diisi GitHub Actions
 *   GITHUB_EVENT_PATH         event pull_request, diisi GitHub Actions
 *   REDLINE_PR_NUMBER         override nomor PR kalau tidak lewat Actions
 *   REDLINE_PR_BASE           base branch untuk `fix`, default main
 */
import { spawnSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir, userInfo } from "node:os";
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
  curl?: string;
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
type Experiment = {
  id: number;
  group_id: string;
  kind: string;
  outcome: string;
  category: string;
  hypothesis: string;
  patch: string;
  runs: number;
  passes: number;
  created_at: string;
};
type VerifyOutcome = {
  cost: number;
  proven: boolean;
  failure: Failure;
  patch?: string;
  category?: string;
  hypothesis?: string;
  experimentId?: number;
  evidence?: string[];
  curl?: string;
};
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
type Candidate = {
  id: number;
  fingerprint: string;
  test: string;
  pattern: string;
  category: string;
  summary: string;
  status: string;
  support: number;
  also_matches: number;
  backtest_reason?: string;
};
type Promotion = {
  candidates: number;
  promoted: Rule[];
  rejected: { id: number; pattern: string; reason: string }[];
};
type ShadowStat = {
  rule_id: number;
  status: string;
  pattern: string;
  category: string;
  summary: string;
  matches: number;
  confirmed: number;
  overridden: number;
  pending: number;
  agreement_rate: number;
  ready: boolean;
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
    throw new Error(`Redline server cannot be reached at ${baseURL()}`);
  }
  const data = (await res.json().catch(() => ({}))) as T & { error?: string };
  if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
  return data;
}

// ---------- verify ----------

async function verify(args: string[]) {
  const force = args.includes("--force");
  const dryRun = args.includes("--dry-run");
  const base = args.find((a) => a.startsWith("--base="))?.split("=")[1];
  const pr = args.includes("--pr") || dryRun;
  const fingerprints = args.filter((a) => !a.startsWith("--"));
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
      console.log("No failures in the last run. Run tests first (npx playwright test).");
      return;
    }
  }
  const max = env("REDLINE_VERIFY_MAX", 3);
  if (targets.length > max) {
    console.log(
      `${targets.length} failures; testing the first ${max} (set REDLINE_VERIFY_MAX).`,
    );
    targets = targets.slice(0, max);
  }
  let cost = 0;
  for (const [i, f] of targets.entries()) {
    console.log(
      `\n━━ ${i + 1}/${targets.length} ${f.id && f.id !== "-" ? f.id + " " : ""}${f.test}`,
    );
    try {
      const outcome = await verifyOne(f, force);
      cost += outcome.cost;
      if (pr && outcome.proven && outcome.patch) {
        try {
          await openPullRequest(outcome, dryRun, base);
        } catch (e) {
          console.log(`  ✗ PR failed: ${(e as Error).message}`);
        }
      }
    } catch (e) {
      console.log(`  ✗ experiment failed to run: ${(e as Error).message}`);
    }
  }
  if (cost > 0) console.log(`\nAI cost: $${cost.toFixed(5)}`);
}

async function fix(args: string[]) {
  const targets = args.filter((a) => !a.startsWith("--"));
  if (targets.length !== 1) {
    throw new Error("usage: npx redline fix <fingerprint> [--dry-run] [--force]");
  }
  const force = args.includes("--force");
  const dryRun = args.includes("--dry-run");
  const base = args.find((a) => a.startsWith("--base="))?.split("=")[1];
  if (!force) {
    const proof = await storedProof(targets[0]);
    if (proof) {
      console.log(`  using stored experiment #${proof.experimentId}; no rerun or AI call.`);
      try {
        await openPullRequest(proof, dryRun, base);
      } catch (e) {
        console.log(`  ✗ PR failed: ${(e as Error).message}`);
      }
      return;
    }
  }
  return verify([...args, "--pr"]);
}

/** Reuse the latest passed patch experiment, so `fix` does not rerun or call AI again. */
async function storedProof(fp: string): Promise<VerifyOutcome | null> {
  const detail = await api<{
    group: { test: string; test_case_id?: string };
    experiments?: Experiment[];
    analysis?: { evidence?: string[] };
  }>("GET", `/api/groups/${fp}`);
  const proof = (detail.experiments ?? [])
    .filter((e) => e.kind === "patch" && e.outcome === "passed" && e.patch)
    .sort((a, b) => b.id - a.id)[0];
  if (!proof) return null;
  const known = lastRun().failures.find((f) => f.fingerprint === fp);
  return {
    cost: 0,
    proven: true,
    failure: {
      fingerprint: fp,
      test: detail.group.test,
      id: detail.group.test_case_id,
      curl: known?.curl,
    },
    patch: proof.patch,
    category: proof.category,
    hypothesis: proof.hypothesis,
    experimentId: proof.id,
    evidence: detail.analysis?.evidence ?? [],
    curl: known?.curl ?? "",
  };
}

async function groupAsFailure(fp: string): Promise<Failure> {
  const g = await api<{ group: { test: string } }>("GET", `/api/groups/${fp}`);
  return { fingerprint: fp, test: g.group.test };
}

/** "project › file › describe › judul" */
function parseTestKey(key: string) {
  const [project, file, ...title] = key.split(" › ");
  if (!project || !file || title.length === 0)
    throw new Error(`unknown test name format: ${key}`);
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
  throw new Error(`file ${file} does not exist in this project`);
}

async function verifyOne(f: Failure, force: boolean): Promise<VerifyOutcome> {
  const { project, title, ...key } = parseTestKey(f.test);
  const file = specPath(key.file);

  // 0. Tebakan Redline SEBELUM eksperimen, supaya nanti bisa dinilai benar atau salah.
  let cost = 0;
  const guess = await guessFirst(f.fingerprint);
  const evidence = guess?.analysis?.evidence ?? [];
  if (guess?.analysis) {
    const a = guess.analysis;
    if (a.source === "experiment" && !force) {
      console.log(`  already proven: ${categoryName(a.category)}. ${a.summary}`);
      console.log(`  (repeat the experiment with: npx redline verify --force)`);
      return { cost, failure: f, proven: false };
    }
    if (!guess.cached && a.source === "ai") cost += a.cost_usd;
    console.log(
      `  0. tebakan Redline: ${categoryName(a.category)} (${sourceName(a.source)}, ${a.confidence})`,
    );
    console.log(`      ${a.summary}`);
    for (const c of a.similar ?? []) {
      const title = c.test.split(" › ").slice(2).join(" › ");
      console.log(
        `      similar (${c.similarity.toFixed(2)}): ${title} → proven ${categoryName(c.category)}`,
      );
    }
  } else if (guess?.error) {
    console.log(`  0. Redline guess: - (${guess.error})`);
  }

  const sandbox = makeSandbox(ROOT);
  try {
    const run = (repeat: number) => runTest(sandbox, project, file, title, repeat);

    // 1. Rerun apa adanya.
    const reruns = env("REDLINE_RERUNS", 2);
    process.stdout.write(`  1. rerun ${reruns}x without changes… `);
    const rerun = run(reruns);
    const flaky = rerun.passes > 0;
    console.log(
      flaky ? `passed ${rerun.passes}/${rerun.runs}` : `failed ${rerun.runs}/${rerun.runs}`,
    );
    const rerunSaved = await saveExperiment(f.fingerprint, {
      kind: "rerun",
      outcome: flaky ? "passed" : "failed",
      category: flaky ? "flaky" : "unknown",
      hypothesis: "The failure can be reproduced without any changes.",
      detail: rerun.error,
      runs: rerun.runs,
      passes: rerun.passes,
    });
    if (flaky) {
      console.log(
        `  ⚠ INCONSISTENT: the test passed when rerun. Likely flaky or a transient condition.`,
      );
      printVerdicts(rerunSaved.verdicts);
      return { cost, failure: f, proven: false, category: "flaky" };
    }

    // 2. Patch dari AI, beberapa percobaan.
    const files = collectSources(file);
    const attempts: Attempt[] = [];
    const maxAttempts = env("REDLINE_VERIFY_ATTEMPTS", 2);
    for (let n = 1; n <= maxAttempts; n++) {
      process.stdout.write(`  2.${n} AI is making a hypothesis… `);
      const fix = await api<Fix>("POST", `/api/groups/${f.fingerprint}/fix`, { files, attempts });
      cost += fix.cost_usd;
      console.log(`${fix.category} (${fix.confidence})`);
      console.log(`      ${fix.hypothesis}`);

      if (fix.edits.length === 0) {
        console.log(
          `  ○ AI decided this is not a test bug, so it cannot be proven with a test patch.`,
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
        return { cost, failure: f, proven: false, category: fix.category };
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
      process.stdout.write(`      running the test with the patch… `);
      const result = run(1);
      const passed = result.passes === result.runs && result.runs > 0;
      console.log(passed ? "PASSED" : "still failing");
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
        console.log(`  ✓ PROVEN: test passed after the patch.\n`);
        console.log(indent(patch, "      "));
        console.log(`\n      Terapkan: git apply ${path.relative(ROOT, out)}`);
        if (fix.category === "test_bug") {
          console.log(`      Buka PR: npx redline fix ${f.fingerprint}`);
        }
        printVerdicts(saved.verdicts);
        return {
          cost,
          failure: f,
          proven: true,
          patch,
          category: fix.category,
          hypothesis: fix.hypothesis,
          experimentId: saved.id,
          evidence,
          curl: f.curl ?? "",
        };
      }
      attempts.push({ hypothesis: fix.hypothesis, patch, result: result.error });
      resetSandbox(sandbox);
    }
    console.log(
      `  ✗ Not proven after ${maxAttempts} attempts. The failed hypothesis is stored in Redline.`,
    );
    return { cost, failure: f, proven: false };
  } finally {
    if (process.env.REDLINE_KEEP_SANDBOX === "1") console.log(`  (copy saved at ${sandbox})`);
    else rmSync(sandbox, { recursive: true, force: true });
  }
}

// ---------- fix -> PR ----------

type PullRequestPlan = {
  repo: string;
  base: string;
  branch: string;
  title: string;
  body: string;
  patch: string;
};

function shortTest(test: string): string {
  const parts = test.split(" › ");
  return parts.slice(2).join(" › ") || test;
}

function slug(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
}

function githubRepo(): string {
  if (process.env.GITHUB_REPOSITORY) return process.env.GITHUB_REPOSITORY;
  const remote = git(ROOT, ["remote", "get-url", "origin"]).trim();
  const match = remote.match(/github\.com[/:]([^/]+)\/([^/]+?)(?:\.git)?$/);
  if (!match) throw new Error("cannot detect the GitHub repo; set GITHUB_REPOSITORY=owner/repo");
  return `${match[1]}/${match[2]}`;
}

function githubToken(): string {
  if (process.env.GITHUB_TOKEN) return process.env.GITHUB_TOKEN;
  if (process.env.GH_TOKEN) return process.env.GH_TOKEN;
  const r = spawnSync("gh", ["auth", "token"], { encoding: "utf8" });
  if (r.status === 0 && r.stdout.trim()) return r.stdout.trim();
  throw new Error("GITHUB_TOKEN (or an authenticated `gh`) is required to open a PR");
}

/** Reject patches that touch secrets, CI, or generated files. */
function assertSafePatch(patch: string) {
  if (!patch.trim()) throw new Error("patch is empty");
  if (/^(<<<<<<<|=======|>>>>>>>)/m.test(patch)) throw new Error("patch contains conflict markers");
  const forbidden = [
    ".env",
    ".github/workflows/",
    ".git/",
    "secrets/",
    "credentials",
    "node_modules/",
    "test-results/",
  ];
  const unsafe = (file: string) =>
    forbidden.some((prefix) => file === prefix || file.startsWith(prefix));
  for (const line of patch.split("\n")) {
    for (const prefix of ["+++ b/", "--- a/", "rename from ", "rename to "]) {
      if (!line.startsWith(prefix)) continue;
      const file = line.slice(prefix.length);
      if (unsafe(file)) throw new Error(`refusing to open a PR that changes ${file}`);
    }
  }
}

function pullRequestPlan(outcome: VerifyOutcome, baseOverride?: string): PullRequestPlan {
  if (!outcome.patch) throw new Error("no proven patch to open");
  if (outcome.category !== "test_bug") {
    throw new Error(`only proven test_bug patches can open a PR, got ${outcome.category ?? "unknown"}`);
  }
  assertSafePatch(outcome.patch);
  const repo = githubRepo();
  const base = baseOverride || process.env.REDLINE_PR_BASE || "main";
  const test = shortTest(outcome.failure.test);
  const id = outcome.failure.id && outcome.failure.id !== "-" ? outcome.failure.id : "";
  const branch = `redline/fix-${slug(id || outcome.failure.fingerprint.slice(0, 8))}-${Date.now().toString(36)}`;
  const title = `fix(test): ${id ? id + " " : ""}${test}`.slice(0, 72);
  const patch = outcome.patch.length > 50_000 ? outcome.patch.slice(0, 50_000) + "\n… (truncated)" : outcome.patch;
  const evidenceLines =
    (outcome.evidence ?? [])
      .slice(0, 3)
      .map((line) => `- \`${line.replace(/`/g, "'")}\``)
      .join("\n") || "- No evidence was stored for this failure.";
  const curlSection = outcome.curl
    ? `\n**Reproduce with curl**\n\n\`\`\`bash\n${outcome.curl}\n\`\`\`\n`
    : "";
  const body = `## Cause

${outcome.hypothesis ?? "Proven test fix."}

- Category: \`test_bug\`
- Test: \`${outcome.failure.test}\`
- Fingerprint: \`${outcome.failure.fingerprint}\`
${outcome.experimentId ? `- Redline experiment: #${outcome.experimentId}` : ""}

## Evidence

${evidenceLines}

## Verification

- Redline reran the test with this patch: **1/1 passed**.
- Redline rejects patches that remove assertions, add skip/only/retry, or wrap errors in try/catch.
- This PR was created by Redline and is **not auto-merged**. A human reviews and merges it.

## Patch

\`\`\`diff
${patch}
\`\`\`

## Reproduce

\`\`\`bash
npx redline verify ${outcome.failure.fingerprint} --force
\`\`\`
${curlSection}`;
  return { repo, base, branch, title, body, patch: outcome.patch };
}

async function openPullRequest(outcome: VerifyOutcome, dryRun: boolean, baseOverride?: string) {
  const plan = pullRequestPlan(outcome, baseOverride);
  console.log(`\n  PR plan: ${plan.repo} ${plan.branch} -> ${plan.base}`);
  console.log(`  ${plan.title}`);
  if (dryRun) {
    console.log(`\n${plan.body}`);
    console.log("  (dry run: no branch, push, or PR was created)");
    return;
  }
  const dirty = git(ROOT, ["status", "--porcelain"])
    .split("\n")
    .filter((line) => line.trim() && !line.startsWith("??"));
  if (dirty.length > 0) {
    throw new Error("commit or stash tracked changes before opening a PR");
  }
  const token = githubToken();
  const patchFile = path.join(tmpdir(), `redline-${Date.now().toString(36)}.patch`);
  const worktree = mkdtempSync(path.join(tmpdir(), "redline-pr-"));
  rmSync(worktree, { recursive: true, force: true });
  writeFileSync(patchFile, plan.patch);
  try {
    git(ROOT, ["worktree", "add", "-b", plan.branch, worktree, "HEAD"]);
    git(worktree, ["apply", "--check", patchFile]);
    git(worktree, ["apply", patchFile]);
    git(worktree, ["add", "-A"]);
    git(worktree, ["diff", "--cached", "--check"]);
    git(worktree, [
      "-c",
      "user.name=redline",
      "-c",
      "user.email=redline@localhost",
      "commit",
      "-m",
      plan.title,
    ]);
    git(worktree, ["push", "-u", "origin", plan.branch]);
    const response = await fetch(`https://api.github.com/repos/${plan.repo}/pulls`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: "application/vnd.github+json",
        "Content-Type": "application/json",
        "User-Agent": "redline-playwright",
      },
      body: JSON.stringify({
        title: plan.title,
        head: plan.branch,
        base: plan.base,
        body: plan.body,
      }),
    });
    const data = (await response.json().catch(() => ({}))) as { html_url?: string; message?: string };
    if (!response.ok) {
      throw new Error(`GitHub rejected the PR (${response.status}): ${data.message ?? "unknown error"}`);
    }
    console.log(`  ✓ PR opened: ${data.html_url ?? `${plan.repo}#${plan.branch}`}`);
  } finally {
    try {
      git(ROOT, ["worktree", "remove", "--force", worktree]);
    } catch {
      // The worktree is only a temporary copy; a cleanup failure must not hide the PR result.
    }
    rmSync(patchFile, { force: true });
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
  evidence?: string[];
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
  console.log(`\n  Previous guesses vs evidence:`);
  for (const v of verdicts) {
    const mark = v.correct
      ? "✅ correct"
      : v.predicted === "unknown"
        ? "➖ no guess"
        : `❌ wrong (proven ${categoryName(v.truth)})`;
    console.log(
      `    ${sourceName(v.source)}: ${categoryName(v.predicted)} (${v.confidence}) → ${mark}`,
    );
  }
}

const categoryNames: Record<string, string> = {
  backend_bug: "BACKEND BUG",
  test_bug: "TEST BUG",
  environment: "ENVIRONMENT",
  flaky: "FLAKY",
  unknown: "UNCLEAR",
};
const categoryName = (c: string) => categoryNames[c] ?? c;
const sourceNames: Record<string, string> = {
  rule: "rule",
  ai: "AI",
  human: "manual label",
  experiment: "experiment",
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
      `playwright produced no report: ${(r.stderr || r.stdout).slice(0, 500)}`,
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
  if (runs === 0) throw new Error(`test not found: ${title}`);
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
    ["schema validation (.parse)", /\.(?:safeParse|parse)\s*\(/g],
  ];
  for (const [name, re] of mustNotDecrease) {
    if (count(after, re) < count(before, re)) return `number of ${name} decreased`;
  }
  const mustNotIncrease: [string, RegExp][] = [
    ["skip/fixme/only/fail", /\.(?:skip|fixme|only|fail)\s*\(/g],
    ["try/catch", /\btry\s*\{/g],
    ["retry/timeout", /\b(?:retries|retry|setTimeout|timeout)\b/gi],
    ["redaction marker", /\[(?:REDACTED|JWT|NIK\/16-DIGIT)\]/g],
  ];
  for (const [name, re] of mustNotIncrease) {
    if (count(after, re) > count(before, re)) return `added ${name}`;
  }
  return "";
}

/** Terapkan edit AI ke salinan. Mengembalikan alasan penolakan (string) atau true. */
function applyEdits(dir: string, files: SourceFile[], edits: Edit[]): true | string {
  const byPath = new Map(files.map((f) => [f.path, f.content]));
  const changed = new Map<string, string>();
  for (const e of edits) {
    const current = changed.get(e.path) ?? byPath.get(e.path);
    if (current === undefined) return `file ${e.path} is not one of the test files sent`;
    const hits = current.split(e.find).length - 1;
    if (hits !== 1) return `the search text appears ${hits}x in ${e.path} (must be exactly 1)`;
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
  console.log("Processing rule candidates that have evidence…\n");
  let promotion: Promotion | undefined;
  try {
    promotion = await api<Promotion>("POST", "/api/rules/candidates/promote");
  } catch (e) {
    console.log(`(candidate dilewati: ${e instanceof Error ? e.message : e})\n`);
  }
  if (promotion) {
    console.log(
      `${promotion.candidates} candidate draft, ${promotion.promoted.length} dipromosikan, ` +
        `${promotion.rejected.length} ditolak.\n`,
    );
    for (const r of promotion.promoted) printRule(r);
    for (const r of promotion.rejected) console.log(`  ✗ candidate #${r.id} /${r.pattern}/\n      ${r.reason}\n`);
  }

  console.log("AI is learning from proven cases (manual labels + passing experiments)…\n");
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
  console.log(`${res.cases} cases are not covered by rules.\n`);
  for (const r of res.proposed) printRule(r);
  for (const r of res.rejected)
    console.log(`  ✗ ditolak  /${r.pattern}/ → ${r.category}\n             ${r.reason}\n`);
  if (res.proposed.length > 0)
    console.log(`Review the proposals above, then: npx redline approve <id>  (or reject <id>)`);
  if (res.cost_usd) console.log(`AI cost: $${res.cost_usd.toFixed(5)}`);
}

async function candidates() {
  const list = await api<Candidate[]>("GET", "/api/rules/candidates");
  if (list.length === 0) {
    console.log("No rule candidates yet. Run with REDLINE_AI=1, then add a label/experiment.");
    return;
  }
  for (const c of list) {
    const mark = { draft: "…", promoted: "✓", rejected: "✗", expired: "·" }[c.status] ?? " ";
    console.log(`  ${mark} #${c.id} [${c.status}] /${c.pattern}/ → ${c.category}`);
    console.log(`      ${c.summary}`);
    console.log(`      ${c.test || c.fingerprint}`);
    console.log(`      support ${c.support}, also matches ${c.also_matches}`);
    if (c.backtest_reason) console.log(`      ${c.backtest_reason}`);
    console.log();
  }
}

async function analyzeRun(args: string[]) {
  const at = args.indexOf("--run");
  const runID = at >= 0 ? args[at + 1] : "";
  const rulesOnly = args.includes("--rules");
  if (!runID || !/^\d+$/.test(runID)) {
    throw new Error("tulis run id, misalnya: npx redline analyze --run 112");
  }
  const run = await api<{
    run: { id: number };
    tests: { test: string; test_case_id?: string; status: string; fingerprint?: string }[];
  }>("GET", `/api/runs/${runID}`);
  const seen = new Set<string>();
  const failed: typeof run.tests = [];
  for (const t of run.tests) {
    if (t.status === "failed" && t.fingerprint && !seen.has(t.fingerprint)) {
      seen.add(t.fingerprint);
      failed.push(t);
    }
  }
  if (failed.length === 0) {
    console.log(`Run #${runID} has no failures with a fingerprint.`);
    return;
  }
  console.log(`Analyzing ${failed.length} failures in run #${runID}…\n`);
  let needsAI = 0;
  for (const test of failed) {
    const fp = test.fingerprint as string;
    try {
      const res = await api<{
        analysis?: { source: string; category: string; summary: string; next_step: string };
        error?: string;
      }>("POST", `/api/groups/${encodeURIComponent(fp)}/analyze${rulesOnly ? "?ai=0" : ""}`);
      if (!res.analysis) {
        console.log(`  ✗ ${test.test}\n      ${res.error ?? "no result"}\n`);
        needsAI++;
        continue;
      }
      if (res.analysis.source === "none") needsAI++;
      console.log(
        `  ${res.analysis.source === "none" ? "…" : "✓"} ${test.test}\n` +
          `      ${res.analysis.source} · ${res.analysis.category} · ${res.analysis.summary}\n`,
      );
      if (res.analysis.source === "none" && res.analysis.next_step)
        console.log(`      ${res.analysis.next_step}\n`);
    } catch (e) {
      console.log(`  ✗ ${test.test}\n      ${e instanceof Error ? e.message : e}\n`);
      needsAI++;
    }
  }
  if (needsAI > 0) {
    console.log(
      `${needsAI} failures still need AI. Set ANTHROPIC_API_KEY on the server, then rerun this command.`,
    );
  } else {
    console.log("All failures already have an analysis. Add a label/experiment, then: npx redline learn");
  }
}

function printRule(r: Rule) {
  const mark = { proposed: "?", shadow: "◐", active: "✓", rejected: "✗" }[r.status] ?? " ";
  console.log(`  ${mark} #${r.id} [${r.status}] /${r.pattern}/ → ${r.category}`);
  console.log(`      ${r.summary}`);
  console.log(
    `      backtest: matched ${r.hits} proven cases, also matched ${r.also_matches} other groups\n`,
  );
}

async function score(args: string[]) {
  const project = args.find((a) => a.startsWith("--project="))?.split("=")[1] ?? "";
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
  console.log("Redline prediction scoreboard (compared with experiments or manual labels)\n");
  for (const s of board.sources) {
    const pct = s.graded > 0 ? `${Math.round(s.accuracy * 100)}%` : "-";
    const unknown = s.unknown > 0 ? `, ${s.unknown} no guess` : "";
    console.log(
      `  ${sourceName(s.source).padEnd(7)} ${s.correct}/${s.graded} correct (${pct})${unknown}`,
    );
  }
  try {
    const auto = await api<{
      project?: string;
      accepted: number;
      confirmed: number;
      overridden: number;
      pending: number;
      decided: number;
      override_rate: number;
      threshold: number;
      warning: boolean;
    }>(
      "GET",
      `/api/auto-accept/stats${project ? `?project=${encodeURIComponent(project)}` : ""}`,
    );
    if (auto.accepted > 0) {
      const rate = Math.round(auto.override_rate * 100);
      const scope = auto.project ? ` (${auto.project})` : "";
      console.log(
        `\nAuto-accept${scope}: ${auto.confirmed} confirmed, ${auto.overridden} overridden, ${auto.pending} pending` +
          ` (override ${rate}% of ${auto.decided} decided)`,
      );
      if (auto.warning) {
        console.log(
          `  ⚠ Override rate is above ${Math.round(auto.threshold * 100)}%; consider setting AUTO_ACCEPT=off.`,
        );
      }
    }
  } catch {
    // Older servers do not have the endpoint; the scoreboard is still useful.
  }
  if (board.entries.length === 0) {
    console.log("\nNo predictions to grade yet. Run: npx redline verify");
    return;
  }
  console.log("\nLatest:");
  for (const e of board.entries) {
    const mark = e.correct ? "✅" : e.predicted === "unknown" ? "➖" : "❌";
    const title = e.test.split(" › ").slice(2).join(" › ");
    console.log(
      `  ${mark} ${sourceName(e.source).padEnd(7)} ${categoryName(e.predicted).padEnd(12)} proven ${categoryName(e.truth).padEnd(12)} (${e.truth_by})  ${title}`,
    );
  }
  console.log("\nNote: the numbers become meaningful after dozens of proven cases.");
}

async function rules() {
  const list = await api<Rule[]>("GET", "/api/rules");
  if (list.length === 0) console.log("No rules yet. Run: npx redline learn");
  for (const r of list) printRule(r);
}

async function shadow(args: string[]) {
  if (args.includes("--stats")) {
    const project = args.find((a) => a.startsWith("--project="))?.split("=")[1] ?? "";
    const stats = await api<ShadowStat[]>(
      "GET",
      `/api/rules/shadow/stats${project ? `?project=${encodeURIComponent(project)}` : ""}`,
    );
    if (stats.length === 0) {
      console.log("No shadow matches yet. Put a rule in shadow, then let failures be analyzed.");
      return;
    }
    console.log("Shadow rule agreement (compared with human labels)\n");
    for (const s of stats) {
      const decided = s.confirmed + s.overridden;
      const rate = decided > 0 ? `${Math.round(s.agreement_rate * 100)}%` : "-";
      const ready = s.ready ? " · READY TO PROMOTE" : "";
      console.log(
        `  #${s.rule_id} [${s.category}] /${s.pattern}/  matches ${s.matches}, ` +
          `confirmed ${s.confirmed}, overridden ${s.overridden}, pending ${s.pending}, agreement ${rate}${ready}`,
      );
    }
    return;
  }
  if (args.includes("--promote")) {
    const target = args.find((a) => /^\d+$/.test(a));
    if (!target) throw new Error("usage: npx redline shadow --promote <id>");
    const r = await api<Rule>("POST", `/api/rules/${target}/promote-shadow`, { by: who() });
    printRule(r);
    console.log("Promoted to proposed. Activate it with: npx redline approve " + target);
    return;
  }
  const id = args[0];
  if (!id || !/^\d+$/.test(id))
    throw new Error("usage: npx redline shadow <id>  (or: npx redline shadow --stats)");
  const r = await api<Rule>("PUT", `/api/rules/${id}`, { status: "shadow", by: who() });
  printRule(r);
  console.log("Shadow mode: the rule is evaluated and recorded, but never changes the analysis.");
}

async function decide(id: string | undefined, status: "active" | "rejected") {
  if (!id || !/^\d+$/.test(id))
    throw new Error("write a rule id, for example: npx redline approve 3");
  const r = await api<Rule>("PUT", `/api/rules/${id}`, { status, by: who() });
  printRule(r);
  if (status === "active")
    console.log("Rule active: failures matching this pattern are now decided without AI.");
}

/** Test yang terdampak kalau file atau endpoint berubah, dari graph Redline (run lulus terakhir tiap test). */
async function impact(args: string[]) {
  const q = args.join(" ").trim();
  if (!q)
    throw new Error(
      "write a file or endpoint, for example: npx redline impact schemas/brand.ts",
    );
  const isEndpoint = q.startsWith("/") || /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s/i.test(q);
  const res = await api<{ tests: { test: string; test_case_id?: string; run_id: number }[] }>(
    "GET",
    `/api/impact?${new URLSearchParams({ [isEndpoint ? "endpoint" : "file"]: q })}`,
  );
  if (res.tests.length === 0) {
    console.log(
      `No test ${isEndpoint ? "calls" : "uses"} ${q} (according to runs already sent to Redline).`,
    );
    return;
  }
  console.log(`${res.tests.length} tests ${isEndpoint ? "call" : "depend on"} ${q}:\n`);
  for (const t of res.tests) {
    console.log(`  ${(t.test_case_id ?? "").padEnd(14)} ${t.test}`);
  }
  const ids = res.tests.map((t) => t.test_case_id).filter(Boolean);
  if (ids.length > 0)
    console.log(`\nRun only these tests: npx playwright test --grep "${ids.join("|")}"`);
}

// ---------- comment ----------

type GithubComment = { id: number; body?: string };

function pullRequestNumber(): number {
  const explicit = Number(process.env.REDLINE_PR_NUMBER || "");
  if (Number.isInteger(explicit) && explicit > 0) return explicit;

  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (eventPath && existsSync(eventPath)) {
    const event = JSON.parse(readFileSync(eventPath, "utf8")) as {
      pull_request?: { number?: number };
      issue?: { number?: number };
    };
    const number = event.pull_request?.number ?? event.issue?.number;
    if (number) return number;
  }
  throw new Error(
    "PR number not found: run in a GitHub Actions pull_request event or set REDLINE_PR_NUMBER",
  );
}

async function comment(args: string[]) {
  const file = args[0] ? path.resolve(args[0]) : path.join(RESULTS_DIR, "redline-report.md");
  if (!existsSync(file)) {
    throw new Error(`${file} does not exist yet; run npx playwright test first`);
  }

  const token = process.env.GITHUB_TOKEN;
  const repo = process.env.GITHUB_REPOSITORY;
  if (!token || !repo) {
    throw new Error("GITHUB_TOKEN and GITHUB_REPOSITORY are required (automatic in GitHub Actions)");
  }
  const pr = pullRequestNumber();
  const api = process.env.GITHUB_API_URL || "https://api.github.com";
  const marker = "<!-- redline-report -->";
  let body = readFileSync(file, "utf8").trim();
  if (body.length > 60_000) body = `${body.slice(0, 60_000)}\n\n_… report truncated_`;
  body = `${marker}\n${body}`;

  const headers = {
    Authorization: `Bearer ${token}`,
    Accept: "application/vnd.github+json",
    "X-GitHub-Api-Version": "2022-11-28",
    "User-Agent": "redline",
    "Content-Type": "application/json",
  };
  const base = `${api}/repos/${repo}/issues`;

  const list = await fetch(`${base}/${pr}/comments?per_page=100`, { headers });
  if (!list.ok) {
    throw new Error(`GitHub menolak daftar komentar (${list.status}): ${(await list.text()).slice(0, 200)}`);
  }
  const comments = (await list.json()) as GithubComment[];
  const existing = comments.find((item) => item.body?.includes(marker));

  const url = existing ? `${api}/repos/${repo}/issues/comments/${existing.id}` : `${base}/${pr}/comments`;
  const response = await fetch(url, {
    method: existing ? "PATCH" : "POST",
    headers,
    body: JSON.stringify({ body }),
  });
  if (!response.ok) {
    throw new Error(`GitHub menolak komentar (${response.status}): ${(await response.text()).slice(0, 200)}`);
  }
  console.log(`[redline] PR comment ${existing ? "updated" : "created"}: ${repo}#${pr}`);
}

// ---------- status ----------

type Health = {
  status?: string;
  version?: string;
  profile?: string;
  api?: number;
  auth?: boolean;
};

function packageVersion(): string {
  try {
    const raw = readFileSync(path.join(__dirname, "..", "..", "package.json"), "utf8");
    return (JSON.parse(raw) as { version?: string }).version ?? "unknown";
  } catch {
    return "unknown";
  }
}

async function quickJSON<T>(url: string, timeoutMs = 3000): Promise<T> {
  const headers: Record<string, string> = {};
  if (process.env.REDLINE_API_KEY) headers.Authorization = `Bearer ${process.env.REDLINE_API_KEY}`;
  const response = await fetch(baseURL() + url, {
    headers,
    signal: AbortSignal.timeout(timeoutMs),
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return (await response.json()) as T;
}

function printCommands() {
  console.log(`
Commands
  npx redline doctor                 check server, project and AI setup
  npx redline verify                 prove the last run's failures
  npx redline fix <id>               open a PR from a proven fix
  npx redline analyze --run <id>     analyze an existing run
  npx redline score                  accuracy and auto-accept calibration
  npx redline impact <file|METHOD /path>  affected tests
  npx redline comment                update the PR comment

More:
  npx redline rules | candidates | shadow --stats | learn
  npx redline approve <id> | reject <id> | score --project=<name>`);
}

async function status() {
  const pkg = packageVersion();
  const url = baseURL();
  console.log(`Redline ${pkg}`);

  let health: Health;
  try {
    health = await quickJSON<Health>("/healthz");
  } catch (e) {
    console.log(`  server   ✗ cannot reach ${url}`);
    console.log(`           ${(e as Error).message}`);
    printCommands();
    return;
  }

  const serverVersion = health.version ?? "dev";
  const profile = health.profile ? ` · ${health.profile}` : "";
  console.log(`  server   ✓ ${url} · ${serverVersion} · api ${health.api ?? "?"}${profile}`);

  if (serverVersion !== "dev" && serverVersion !== pkg) {
    console.log(`  version  ⚠ package ${pkg}, server ${serverVersion}`);
  } else {
    console.log(`  version  ✓ ${pkg}`);
  }

  if (health.auth && !process.env.REDLINE_API_KEY) {
    console.log(`  auth     ⚠ server requires REDLINE_API_KEY`);
  } else if (health.auth) {
    console.log(`  auth     ✓ API key set`);
  } else {
    console.log(`  auth     ○ open (no API key)`);
  }

  if (health.auth && !process.env.REDLINE_API_KEY) {
    console.log(`  contract ? skipped (no API key)`);
    console.log(`  AI       ? skipped (no API key)`);
    printCommands();
    return;
  }

  const [config, ai] = await Promise.allSettled([
    quickJSON<{ contracts?: Array<{ project?: string }> }>("/api/config"),
    quickJSON<{ enabled?: boolean; mode?: string; model?: string }>("/api/ai/usage"),
  ]);

  if (config.status === "fulfilled") {
    const projects = (config.value.contracts ?? [])
      .map((source) => source.project)
      .filter((project): project is string => Boolean(project));
    console.log(
      `  contract ${projects.length > 0 ? `✓ ${projects.join(", ")}` : "⚠ none configured"}`,
    );
  } else {
    console.log(`  contract ⚠ cannot read config`);
  }

  if (ai.status === "fulfilled") {
    if (ai.value.enabled) {
      console.log(`  AI       ✓ ${ai.value.mode ?? "fallback"} · ${ai.value.model ?? "unknown"}`);
    } else {
      console.log(`  AI       ○ disabled (rules only)`);
    }
  } else {
    console.log(`  AI       ⚠ cannot read usage`);
  }

  printCommands();
}

// ---------- doctor ----------

type DoctorStatus = "ok" | "warn" | "fail";
type DoctorSection = "Server" | "Project" | "AI";
type DoctorCheck = {
  section: DoctorSection;
  name: string;
  status: DoctorStatus;
  detail: string;
  fix?: string;
};

function projectDependency(): string | null {
  try {
    const pkg = JSON.parse(readFileSync("package.json", "utf8")) as {
      dependencies?: Record<string, string>;
      devDependencies?: Record<string, string>;
    };
    return pkg.devDependencies?.["redline-playwright"] ?? pkg.dependencies?.["redline-playwright"] ?? null;
  } catch {
    return null;
  }
}

function installedPackageVersion(): string | null {
  try {
    const pkg = JSON.parse(
      readFileSync(path.join("node_modules", "redline-playwright", "package.json"), "utf8"),
    ) as { version?: string };
    return pkg.version ?? null;
  } catch {
    return null;
  }
}

function specFiles(dir: string): string[] {
  const out: string[] = [];
  function walk(current: string) {
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      if (entry.name === "node_modules" || entry.name === ".git") continue;
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.name.endsWith(".spec.ts")) out.push(full);
    }
  }
  try {
    walk(dir);
  } catch {
    // Missing tests dir is reported by the empty list.
  }
  return out;
}

async function doctor(args: string[]) {
  const checks: DoctorCheck[] = [];
  const pkg = packageVersion();
  const serverURL = baseURL();

  let health: Health | undefined;
  try {
    health = await quickJSON<Health>("/healthz");
    const profile = health.profile ? ` · ${health.profile}` : "";
    checks.push({
      section: "Server", name: "server reachable", status: "ok",
      detail: `${serverURL} · ${health.version ?? "dev"} · api ${health.api ?? "?"}${profile}`,
    });
    checks.push({
      section: "Server", name: "package vs server",
      status: health.version && health.version !== "dev" && health.version !== pkg ? "warn" : "ok",
      detail:
        health.version && health.version !== "dev" && health.version !== pkg
          ? `package ${pkg}, server ${health.version}`
          : `v${pkg}`,
      fix: "Use the same tag for the server and redline-playwright.",
    });
  } catch (e) {
    checks.push({
      section: "Server", name: "server reachable", status: "fail",
      detail: `cannot reach ${serverURL}: ${(e as Error).message}`,
      fix: "Start the Redline server and check REDLINE_URL.",
    });
  }

  if (health) {
    if (health.auth && !process.env.REDLINE_API_KEY) {
      checks.push({
        section: "Server", name: "API key", status: "fail",
        detail: "server requires REDLINE_API_KEY, but none is set",
        fix: "Put the same REDLINE_API_KEY in this project's .env.",
      });
    } else {
      checks.push({
        section: "Server", name: "API key",
        status: "ok", detail: health.auth ? "accepted" : "server is open (no key)",
      });
    }

    if (!health.auth || process.env.REDLINE_API_KEY) {
      const [config, ai] = await Promise.allSettled([
        quickJSON<{ contracts?: Array<{ project?: string }> }>("/api/config"),
        quickJSON<{ enabled?: boolean; mode?: string; model?: string }>("/api/ai/usage"),
      ]);
      if (config.status === "fulfilled") {
        const projects = (config.value.contracts ?? [])
          .map((source) => source.project)
          .filter((project): project is string => Boolean(project));
        checks.push({
          section: "Project", name: "contract source",
          status: projects.length > 0 ? "ok" : "warn",
          detail: projects.length > 0 ? projects.join(", ") : "none configured",
          fix: "Add an OpenAPI source in Config.",
        });
      } else {
        checks.push({
          section: "Server", name: "contract config", status: "fail",
          detail: "cannot read /api/config; check the API key",
        });
      }
      if (ai.status === "fulfilled") {
        checks.push({
          section: "AI", name: "analysis",
          status: ai.value.enabled ? "ok" : "warn",
          detail: ai.value.enabled
            ? `${ai.value.mode ?? "fallback"} · ${ai.value.model ?? "unknown"}`
            : "disabled (rules only)",
          fix: "Set ANTHROPIC_API_KEY on the server to enable AI analysis.",
        });
      } else {
        checks.push({
          section: "AI", name: "usage", status: "warn",
          detail: "cannot read /api/ai/usage",
        });
      }
    }
  }

  const dependency = projectDependency();
  const installed = installedPackageVersion();
  checks.push({
    section: "Project", name: "redline-playwright",
    status: dependency && installed ? "ok" : "fail",
    detail: dependency
      ? installed
        ? `${installed} (declared ${dependency})`
        : `declared ${dependency}, not installed`
      : "not declared in package.json",
    fix: "npm i -D github:ega-septian/redline#<tag>",
  });

  const configPath = "playwright.config.ts";
  let config = "";
  if (!existsSync(configPath)) {
    checks.push({
      section: "Project", name: "playwright config", status: "fail",
      detail: `${configPath} not found`, fix: "Run doctor from the Playwright project root.",
    });
  } else {
    config = readFileSync(configPath, "utf8");
    checks.push({
      section: "Project", name: "reporter",
      status: /redline-playwright\/reporter/.test(config) ? "ok" : "fail",
      detail: /redline-playwright\/reporter/.test(config)
        ? "redline-playwright/reporter configured"
        : "reporter not found in playwright.config.ts",
      fix: 'Add ["redline-playwright/reporter"] to the reporter array.',
    });
  }

  const literalBaseURL = config.match(/baseURL:\s*["'`]([^"'`]+)["'`]/)?.[1] ?? "";
  const appURL = process.env.REDLINE_APP_URL || process.env.PLAYWRIGHT_BASE_URL || literalBaseURL;
  if (!appURL || appURL.includes("${")) {
    checks.push({
      section: "Project", name: "baseURL", status: "warn",
      detail: literalBaseURL
        ? `dynamic value ${literalBaseURL}; set REDLINE_APP_URL to check it`
        : "not found in playwright.config.ts; set REDLINE_APP_URL to check it",
      fix: "Set REDLINE_APP_URL (for example https://api.example.com) and rerun doctor.",
    });
  } else {
    try {
      const response = await fetch(appURL, {
        method: "GET",
        signal: AbortSignal.timeout(8000),
      });
      checks.push({
        section: "Project", name: "baseURL", status: response.status >= 500 ? "fail" : "ok",
        detail: `${appURL} → HTTP ${response.status}`,
        fix: response.status >= 500 ? "Check the application under test." : undefined,
      });
    } catch (e) {
      checks.push({
        section: "Project", name: "baseURL", status: "fail",
        detail: `${appURL} → ${(e as Error).message}`,
        fix: "Check the URL, DNS, port and that the application is running.",
      });
    }
  }

  const specs = specFiles("tests");
  const usesFixture = specs.some((file) => {
    const text = readFileSync(file, "utf8");
    return /from\s+["'][^"']*fixtures["']/.test(text) || /redline-playwright\/fixture/.test(text);
  });
  checks.push({
    section: "Project", name: "Redline fixture",
    status: usesFixture ? "ok" : "warn",
    detail: usesFixture
      ? `used by ${specs.length} spec${specs.length === 1 ? "" : "s"}`
      : specs.length === 0
        ? "no spec files found under tests/"
        : "no spec imports the fixture",
    fix: 'Import { test, expect } from "./fixtures" so requests and shapes are recorded.',
  });

  const lastRunPath = path.join(RESULTS_DIR, "redline-last-run.json");
  if (!existsSync(lastRunPath)) {
    checks.push({
      section: "Project", name: "last run", status: "warn",
      detail: "redline-last-run.json not found",
      fix: "Run npx playwright test once.",
    });
  } else {
    const ageHours = (Date.now() - statSync(lastRunPath).mtimeMs) / 3_600_000;
    let runID = "";
    try {
      const raw = JSON.parse(readFileSync(lastRunPath, "utf8")) as { run_id?: number };
      runID = raw.run_id ? `run #${raw.run_id}` : "run file";
    } catch {
      // Keep the file name in the detail.
    }
    checks.push({
      section: "Project", name: "last run",
      status: ageHours > 24 * 7 ? "warn" : "ok",
      detail: `${runID || "last run"} · ${ageHours < 1 ? "less than 1h ago" : `${Math.round(ageHours)}h ago`}`,
      fix: "Send a fresh run if this project changed.",
    });
  }

  const problems = checks.filter((check) => check.status === "fail").length;
  const warnings = checks.filter((check) => check.status === "warn").length;
  const strict = args.includes("--strict");
  const failed = problems > 0 || (strict && warnings > 0);

  if (args.includes("--json")) {
    console.log(JSON.stringify({ version: pkg, checks, problems, warnings }, null, 2));
  } else {
    console.log(`Redline doctor · v${pkg}\n`);
    for (const section of ["Server", "Project", "AI"] as DoctorSection[]) {
      const rows = checks.filter((check) => check.section === section);
      if (rows.length === 0) continue;
      console.log(section);
      for (const check of rows) {
        const mark = check.status === "ok" ? "✓" : check.status === "warn" ? "⚠" : "✗";
        console.log(`  ${mark} ${check.name.padEnd(22)} ${check.detail}`);
        if (check.fix && check.status !== "ok") console.log(`      → ${check.fix}`);
      }
      console.log("");
    }
    const verdict = problems > 0 ? "FAIL" : warnings > 0 ? "WATCH" : "OK";
    console.log(`${verdict} · ${problems} failed, ${warnings} warning${warnings === 1 ? "" : "s"}`);
  }
  process.exitCode = failed ? 1 : 0;
}

// ---------- main ----------

async function main() {
  const [cmd, ...args] = process.argv.slice(2);
  if (!cmd) return status();
  switch (cmd) {
    case "doctor":
      return doctor(args);
    case "verify":
      return verify(args);
    case "fix":
      return fix(args);
    case "analyze":
      return analyzeRun(args);
    case "learn":
      return learn();
    case "candidates":
      return candidates();
    case "rules":
      return rules();
    case "shadow":
      return shadow(args);
    case "score":
      return score(args);
    case "approve":
      return decide(args[0], "active");
    case "reject":
      return decide(args[0], "rejected");
    case "impact":
      return impact(args);
    case "comment":
      return comment(args);
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
