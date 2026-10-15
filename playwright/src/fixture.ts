/**
 * Catat bentuk response API (field + tipe, tanpa nilai) untuk Redline, dan lampirkan curl
 * setiap request API ke report saat test gagal (header sensitif disamarkan).
 * Pakai: import { test, expect } from "redline-playwright/fixture";
 *
 * Kalau ReportPortal aktif (REPORTPORTAL=1), ID dari tag @TC- juga dikirim sebagai testCaseId. Paket
 * @reportportal/agent-js-playwright hanya dibutuhkan kalau ReportPortal dipakai.
 */
import { test as base, expect, type APIResponse } from "@playwright/test";

type ReportingApiLike = {
  setTestCaseId(id: string): void;
  addAttributes(attrs: { key: string; value: string }[]): void;
};

/** ReportingApi dari agen ReportPortal, dimuat hanya kalau REPORTPORTAL=1. */
function reportingApi(): ReportingApiLike {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  return (require("@reportportal/agent-js-playwright") as { ReportingApi: ReportingApiLike })
    .ReportingApi;
}

/** Contoh: "string", "null|string", ["number"], { id: "string" } */
export type Shape = string | Shape[] | { [field: string]: Shape };
export type HttpCall = { method: string; path: string; status: number; shape: Shape | null };

const METHODS = new Set(["get", "post", "put", "patch", "delete", "head", "fetch"]);
const MAX_CALLS = 30;
const MAX_DEPTH = 6;
const MAX_KEYS = 100;
const MAX_ITEMS = 20;
const SENSITIVE_HEADERS = /^(authorization|cookie|set-cookie|x-api-key|api-key|x-auth-token)$/i;

type RequestOptions = {
  method?: string;
  headers?: Record<string, string>;
  params?: Record<string, string | number | boolean>;
  data?: unknown;
  form?: Record<string, string | number | boolean>;
  multipart?: Record<string, unknown>;
};

export const test = base.extend<{ reportPortalCaseId: void }>({
  // Otomatis untuk setiap test: tidak perlu memanggil ReportingApi.setTestCaseId di tiap spec.
  // testCaseId dipakai ReportPortal untuk riwayat, tapi tidak tampil; atribut "tc" tampil di bawah nama
  // test dan bisa difilter (Add filter → Attribute → tc:TC-BRD-001).
  reportPortalCaseId: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use, testInfo) => {
      const id = testInfo.tags.find((t) => /^@TC-/i.test(t))?.slice(1);
      if (process.env.REPORTPORTAL === "1" && id) {
        const api = reportingApi();
        api.setTestCaseId(id);
        api.addAttributes([{ key: "tc", value: id }]);
      }
      await use();
    },
    { auto: true },
  ],
  request: async ({ request }, use, testInfo) => {
    const calls: HttpCall[] = [];
    const curls: string[] = [];
    const { baseURL, extraHTTPHeaders } = testInfo.project.use;
    const recorded = new Proxy(request, {
      get(target, prop) {
        const value = Reflect.get(target, prop);
        if (typeof value !== "function") return value;
        if (typeof prop !== "string" || !METHODS.has(prop)) return value.bind(target);
        return async (urlOrRequest: unknown, options?: RequestOptions) => {
          const method = methodOf(prop, urlOrRequest, options);
          const headers = { ...extraHTTPHeaders, ...options?.headers };
          let res: APIResponse;
          try {
            res = await value.call(target, urlOrRequest, options);
          } catch (err) {
            if (curls.length < MAX_CALLS) {
              const url = requestURL(urlOrRequest, options, baseURL);
              curls.push(
                toCurl(method, url, headers, options, `tidak ada response: ${firstLine(err)}`),
              );
            }
            throw err;
          }
          if (calls.length < MAX_CALLS) {
            calls.push(await describe(method, res));
            curls.push(toCurl(method, res.url(), headers, options, String(res.status())));
          }
          return res;
        };
      },
    });
    await use(recorded);
    if (calls.length > 0) {
      await testInfo.attach("redline-http", {
        body: JSON.stringify(calls),
        contentType: "application/json",
      });
    }
    if (curls.length > 0 && testInfo.status !== testInfo.expectedStatus) {
      await testInfo.attach("curl", { body: curls.join("\n\n"), contentType: "text/plain" });
    }
  },
});

export { expect };

/** URL lengkap untuk request yang gagal sebelum dapat response (misalnya server mati). */
function requestURL(
  urlOrRequest: unknown,
  options: RequestOptions | undefined,
  baseURL?: string,
): string {
  const req = urlOrRequest as { url?: () => string };
  const raw = typeof req?.url === "function" ? req.url() : String(urlOrRequest);
  let url: URL;
  try {
    url = new URL(raw, baseURL);
  } catch {
    return raw;
  }
  for (const [k, v] of Object.entries(options?.params ?? {})) url.searchParams.set(k, String(v));
  return url.toString();
}

/** curl yang bisa langsung dijalankan untuk mengulang request. Header sensitif disamarkan. */
export function toCurl(
  method: string,
  url: string,
  headers: Record<string, string>,
  options: RequestOptions | undefined,
  result: string,
): string {
  const lines = [
    `# ${method} ${new URL(url).pathname} → ${result}`,
    `curl -X ${method} ${quote(url)}`,
  ];
  const hasType = Object.keys(headers).some((h) => h.toLowerCase() === "content-type");
  for (const [name, value] of Object.entries(headers)) {
    lines.push(`  -H ${quote(`${name}: ${SENSITIVE_HEADERS.test(name) ? "<REDACTED>" : value}`)}`);
  }
  const data = options?.data;
  if (data !== undefined && data !== null) {
    if (typeof data === "string") {
      lines.push(`  --data-raw ${quote(data)}`);
    } else if (Buffer.isBuffer(data)) {
      lines.push(`  --data-binary '<${data.length} byte>'`);
    } else {
      if (!hasType) lines.push(`  -H ${quote("Content-Type: application/json")}`);
      lines.push(`  --data-raw ${quote(JSON.stringify(data))}`);
    }
  }
  for (const [k, v] of Object.entries(options?.form ?? {}))
    lines.push(`  --data-urlencode ${quote(`${k}=${v}`)}`);
  for (const [k, v] of Object.entries(options?.multipart ?? {})) {
    lines.push(`  -F ${quote(typeof v === "object" ? `${k}=@<file>` : `${k}=${v}`)}`);
  }
  return lines.join(" \\\n").replace(/^(# .*) \\\n/, "$1\n");
}

function quote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

function firstLine(err: unknown): string {
  return String((err as Error)?.message ?? err).split("\n")[0];
}

function methodOf(prop: string, urlOrRequest: unknown, options?: { method?: string }): string {
  if (prop !== "fetch") return prop.toUpperCase();
  if (options?.method) return options.method.toUpperCase();
  const req = urlOrRequest as { method?: () => string };
  return typeof req?.method === "function" ? req.method().toUpperCase() : "GET";
}

async function describe(method: string, res: APIResponse): Promise<HttpCall> {
  let shape: Shape | null = null;
  if ((res.headers()["content-type"] ?? "").includes("json")) {
    try {
      shape = shapeOf(await res.json(), 0);
    } catch {
      shape = null; // body bukan JSON valid
    }
  }
  return { method, path: normalizePath(new URL(res.url()).pathname), status: res.status(), shape };
}

/** /brands/01JC2X... -> /brands/:id */
export function normalizePath(path: string): string {
  return path
    .split("/")
    .map((seg) =>
      /^\d+$/.test(seg) ||
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(seg) ||
      /^[0-9A-HJKMNP-TV-Z]{26}$/.test(seg) ||
      /^[0-9a-f]{16,}$/i.test(seg) ||
      seg.includes("@")
        ? ":id"
        : seg,
    )
    .join("/");
}

export function shapeOf(value: unknown, depth: number): Shape {
  if (value === null) return "null";
  if (Array.isArray(value)) {
    if (value.length === 0) return [];
    if (depth >= MAX_DEPTH) return ["..."];
    let merged: Shape | undefined;
    for (const item of value.slice(0, MAX_ITEMS)) {
      const s = shapeOf(item, depth + 1);
      merged = merged === undefined ? s : mergeShapes(merged, s);
    }
    return [merged as Shape];
  }
  if (typeof value === "object") {
    if (depth >= MAX_DEPTH) return "object";
    const out: { [field: string]: Shape } = {};
    for (const key of Object.keys(value as object).slice(0, MAX_KEYS)) {
      out[key] = shapeOf((value as Record<string, unknown>)[key], depth + 1);
    }
    return out;
  }
  return typeof value; // string, number, boolean
}

function kind(s: Shape): string {
  return typeof s === "string" ? s : Array.isArray(s) ? "array" : "object";
}

/** Gabung bentuk elemen array; tipe berbeda jadi "a|b". */
export function mergeShapes(a: Shape, b: Shape): Shape {
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length === 0) return b;
    if (b.length === 0) return a;
    return [mergeShapes(a[0], b[0])];
  }
  const isObj = (s: Shape) => typeof s === "object" && !Array.isArray(s);
  if (isObj(a) && isObj(b)) {
    const ao = a as { [f: string]: Shape };
    const bo = b as { [f: string]: Shape };
    const out: { [f: string]: Shape } = { ...ao };
    for (const [k, v] of Object.entries(bo)) out[k] = k in ao ? mergeShapes(ao[k], v) : v;
    return out;
  }
  const kinds = new Set([...kind(a).split("|"), ...kind(b).split("|")]);
  return [...kinds].sort().join("|");
}
