/** Hash blok test + file lokal yang di-import. Spasi dan komentar diabaikan. */
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";

const fileCache = new Map<string, string | null>();

function read(file: string): string | null {
  if (!fileCache.has(file)) {
    try {
      fileCache.set(file, readFileSync(file, "utf8"));
    } catch {
      fileCache.set(file, null);
    }
  }
  return fileCache.get(file) ?? null;
}

/** Buang komentar dan spasi di luar string. */
export function normalizeCode(src: string): string {
  let out = "";
  for (let i = 0; i < src.length; i++) {
    const c = src[i];
    const next = src[i + 1];
    if (c === "/" && next === "/") {
      const nl = src.indexOf("\n", i);
      i = nl < 0 ? src.length : nl;
    } else if (c === "/" && next === "*") {
      const end = src.indexOf("*/", i + 2);
      i = end < 0 ? src.length : end + 1;
    } else if (c === '"' || c === "'" || c === "`") {
      const end = skipString(src, i, c);
      out += src.slice(i, end + 1);
      i = end;
    } else if (!/\s/.test(c)) {
      out += c;
    }
  }
  return out;
}

/** Hash 16 karakter, atau "" kalau file tidak terbaca. */
export function sourceHash(file: string, line: number, column: number): string {
  const src = read(file);
  if (src === null) return "";
  const block = extractCall(src, offsetOf(src, line, column));
  const h = createHash("sha256").update(normalizeCode(block));
  for (const dep of localImports(file, src)) {
    h.update("\n--" + path.basename(dep) + "--\n" + normalizeCode(read(dep) ?? ""));
  }
  return h.digest("hex").slice(0, 16);
}

/** File lokal yang dipakai test: file spec + file yang di-import langsung (path absolut, urut). */
export function sourceFiles(file: string): string[] {
  const src = read(file);
  if (src === null) return [];
  return [file, ...localImports(file, src)];
}

/** Hash isi satu file (tanpa komentar dan spasi), 16 karakter. "" kalau file tidak terbaca. */
export function fileHash(file: string): string {
  const src = read(file);
  return src === null
    ? ""
    : createHash("sha256").update(normalizeCode(src)).digest("hex").slice(0, 16);
}

export type FileNode = { hash: string; imports: string[] };

/**
 * Graph import lokal: semua file yang bisa dicapai dari files (import bertingkat, path absolut) beserta hash
 * isinya dan import langsungnya. Dipakai Redline untuk menelusuri file yang benar-benar dipakai test (KAG).
 */
export function importGraph(files: Iterable<string>, max = 1000): Map<string, FileNode> {
  const graph = new Map<string, FileNode>();
  const queue = [...files];
  while (queue.length > 0 && graph.size < max) {
    const file = queue.shift()!;
    if (graph.has(file)) continue;
    const src = read(file);
    if (src === null) continue;
    const imports = localImports(file, src);
    graph.set(file, { hash: fileHash(file), imports });
    queue.push(...imports);
  }
  return graph;
}

function offsetOf(src: string, line: number, column: number): number {
  let offset = 0;
  for (let i = 1; i < line; i++) {
    const nl = src.indexOf("\n", offset);
    if (nl < 0) return src.length;
    offset = nl + 1;
  }
  return offset + Math.max(0, column - 1);
}

/** Teks dari `test(` sampai kurung tutupnya. */
export function extractCall(src: string, start: number): string {
  const open = src.indexOf("(", start);
  if (open < 0) return src.slice(start);
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    const c = src[i];
    const next = src[i + 1];
    if (c === "/" && next === "/") {
      const nl = src.indexOf("\n", i);
      i = nl < 0 ? src.length : nl;
    } else if (c === "/" && next === "*") {
      const end = src.indexOf("*/", i + 2);
      i = end < 0 ? src.length : end + 1;
    } else if (c === '"' || c === "'" || c === "`") {
      i = skipString(src, i, c);
    } else if (c === "(") {
      depth++;
    } else if (c === ")") {
      depth--;
      if (depth === 0) return src.slice(start, i + 1);
    }
  }
  return src.slice(start);
}

function skipString(src: string, i: number, quote: string): number {
  for (let j = i + 1; j < src.length; j++) {
    if (src[j] === "\\") j++;
    else if (src[j] === quote) return j;
  }
  return src.length;
}

/** Import lokal (./ atau ../), diurutkan. */
function localImports(file: string, src: string): string[] {
  const deps = new Set<string>();
  const re = /\bfrom\s+["'](\.{1,2}\/[^"']+)["']|\bimport\s+["'](\.{1,2}\/[^"']+)["']/g;
  for (const m of src.matchAll(re)) {
    const resolved = resolve(path.dirname(file), m[1] ?? m[2]);
    if (resolved) deps.add(resolved);
  }
  return [...deps].sort();
}

function resolve(dir: string, spec: string): string | null {
  const base = path.resolve(dir, spec);
  for (const candidate of [
    base,
    ...[".ts", ".js", ".mts", ".json"].map((e) => base + e),
    path.join(base, "index.ts"),
  ]) {
    if (existsSync(candidate) && !candidate.endsWith("/")) {
      try {
        if (readFileSync(candidate)) return candidate;
      } catch {
        // folder atau tidak bisa dibaca
      }
    }
  }
  return null;
}
