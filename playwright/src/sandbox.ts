/** Salinan project di folder sementara, untuk eksperimen dan benchmark Redline. Kode asli tidak pernah diubah. */
import { spawnSync } from "node:child_process";
import { cpSync, mkdtempSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

const SKIP_COPY = new Set([
  "node_modules",
  ".git",
  "test-results",
  "playwright-report",
  "blob-report",
]);

/** Salin project ke folder sementara. node_modules di-link, bukan disalin. */
export function makeSandbox(root: string): string {
  const dir = mkdtempSync(path.join(tmpdir(), "redline-"));
  cpSync(root, dir, {
    recursive: true,
    filter: (src) => !SKIP_COPY.has(path.relative(root, src).split(path.sep)[0]),
  });
  symlinkSync(path.join(root, "node_modules"), path.join(dir, "node_modules"), "dir");
  // git di salinan hanya untuk membuat diff patch dan mengembalikan file setelah percobaan.
  git(dir, ["init", "-q"]);
  git(dir, ["add", "-A"]);
  git(dir, [
    "-c",
    "user.name=redline",
    "-c",
    "user.email=redline@localhost",
    "commit",
    "-qm",
    "base",
    "--no-verify",
  ]);
  return dir;
}

/** Kembalikan semua file di salinan ke keadaan awal. */
export function resetSandbox(dir: string) {
  git(dir, ["checkout", "-q", "--", "."]);
}

export function git(cwd: string, args: string[]): string {
  const r = spawnSync("git", args, { cwd, encoding: "utf8" });
  if (r.status !== 0) throw new Error(`git ${args[0]}: ${r.stderr.trim()}`);
  return r.stdout;
}
