export type Category = "backend_bug" | "test_bug" | "environment" | "flaky";
export type FailureStatus = "new" | "recurring" | "regressed";
export type Confidence = "high" | "medium" | "proven";
export type Source = "rule" | "ai" | "experiment" | "human";

export type Failure = {
  fingerprint: string;
  status: FailureStatus;
  title: string;
  test: string;
  file: string;
  category: Category;
  confidence: Confidence;
  occurrences: number;
  affectedTests: number;
  lastSeen: string;
  source: Source;
  model?: string;
  summary: string;
  evidence: string[];
  nextStep: string;
  endpoint: string;
  files: string[];
  alsoFails: string;
  curl: string;
  cost: string;
  latency: string;
  firstSeen: string;
  spark: Array<"pass" | "fail">;
};

// Static data for the foundation PR. PR 3 replaces this with GET /api/groups.
export const failures: Failure[] = [
  {
    fingerprint: "8f3c1a2b7d4e",
    status: "new",
    title: "POST /users/login → 500",
    test: "login with valid user",
    file: "auth.spec.ts",
    category: "backend_bug",
    confidence: "high",
    occurrences: 8,
    affectedTests: 8,
    lastSeen: "12:42",
    source: "rule",
    summary:
      "The API returned 500 but the test expected 200. A 5xx means the server failed to process the request; data from the test can trigger it, but the API should answer 4xx with a validation message, not 500.",
    evidence: ["Expected: 200", "Received: 500", "POST /users/login · 8 failed tests, same endpoint"],
    nextStep: "Check the backend log for this run, then report it with the curl below.",
    endpoint: "POST /users/login",
    files: ["auth.spec.ts", "user.schema.ts"],
    alsoFails: "7 other tests without changed code",
    curl: `curl -X POST 'https://api.practicesoftwaretesting.com/users/login' \\
  -H 'Content-Type: application/json' \\
  -H 'Authorization: <REDACTED>' \\
  --data-raw '{"email":"<email>","password":"<REDACTED>"}'`,
    cost: "$0.0000",
    latency: "0 ms",
    firstSeen: "today 12:42",
    spark: ["pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "fail", "fail", "fail", "fail", "fail", "fail"],
  },
  {
    fingerprint: "c41d9e07a8b2",
    status: "regressed",
    title: "TC-BRD-003 · Get brand by id",
    test: "GET /brands/{brandId}",
    file: "brand.spec.ts",
    category: "test_bug",
    confidence: "medium",
    occurrences: 1,
    affectedTests: 1,
    lastSeen: "12:38",
    source: "ai",
    model: "claude-haiku-5-5",
    summary:
      "The response shape is the same as the last passing run while the test code changed. The new assertion is the most likely cause, not the API.",
    evidence: ["Kode test berubah: ya", "Response API: sama", "GET /brands/{id} → 200"],
    nextStep: "Compare the last change to brand.spec.ts (git diff). If the assertion is intentional, label this as a backend bug.",
    endpoint: "GET /brands/{brandId}",
    files: ["brand.spec.ts"],
    alsoFails: "no other test uses this endpoint",
    curl: `curl 'https://api.practicesoftwaretesting.com/brands/01JC2X5V7K9QYQ0W8S3T4M6N8P' \\
  -H 'Accept: application/json'`,
    cost: "$0.0002",
    latency: "1.2 s",
    firstSeen: "today 12:38",
    spark: ["pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "fail"],
  },
  {
    fingerprint: "1b7a44c90d6f",
    status: "recurring",
    title: "GET /products › filter by category",
    test: "filters products",
    file: "catalog.spec.ts",
    category: "environment",
    confidence: "high",
    occurrences: 14,
    affectedTests: 3,
    lastSeen: "11:06",
    source: "rule",
    summary:
      "The target could not be reached, so the test never exercised the API. This is an environment failure, not a product or test bug.",
    evidence: ["connect ECONNREFUSED 127.0.0.1:8091", "GET /products · 3 tests, same error"],
    nextStep: "Start the target (docker compose ps) and check the baseURL in playwright.config.ts.",
    endpoint: "GET /products",
    files: ["catalog.spec.ts"],
    alsoFails: "3 tests in this spec",
    curl: `curl 'http://localhost:8091/products?category=01JC2X5V7K9QYQ0W8S3T4M6N8P'`,
    cost: "$0.0000",
    latency: "0 ms",
    firstSeen: "Oct 28, 09:12",
    spark: ["fail", "fail", "pass", "fail", "fail", "fail", "pass", "fail", "fail", "fail", "fail", "pass", "fail", "fail"],
  },
  {
    fingerprint: "e9d2f81c5a30",
    status: "new",
    title: "TC-BRD-040 · Delete brand in use",
    test: "DELETE /brands/{brandId}",
    file: "brand.spec.ts",
    category: "flaky",
    confidence: "proven",
    occurrences: 3,
    affectedTests: 1,
    lastSeen: "10:22",
    source: "experiment",
    summary:
      "The test passed 3 of 3 reruns without any change. The failure is not consistent: data, timing or a briefly unavailable service is the likely cause.",
    evidence: ["Eksperimen #41: lulus 3 dari 3 kali tanpa perubahan"],
    nextStep: "Look for shared data or ordering between tests. Run with --repeat-each=10 to measure how often it fails.",
    endpoint: "DELETE /brands/{brandId}",
    files: ["brand.spec.ts", "cleanup.fixture.ts"],
    alsoFails: "no other test uses this endpoint",
    curl: `curl -X DELETE 'https://api.practicesoftwaretesting.com/brands/01JC2X5V7K9QYQ0W8S3T4M6N8P' \\
  -H 'Authorization: Bearer <REDACTED>'`,
    cost: "$0.0010",
    latency: "8.4 s",
    firstSeen: "today 10:22",
    spark: ["pass", "fail", "pass", "pass", "pass", "fail", "pass", "pass", "pass", "pass", "fail", "pass", "pass", "pass"],
  },
  {
    fingerprint: "77a1c04be2d9",
    status: "recurring",
    title: "POST /brands › slug already exists",
    test: "@TC-BRD-008",
    file: "brand.spec.ts",
    category: "backend_bug",
    confidence: "medium",
    occurrences: 6,
    affectedTests: 1,
    lastSeen: "09:48",
    source: "ai",
    model: "claude-haiku-5-5",
    summary:
      "The API returned 409 with a different body than the contract permits. The contract allows a per-field message bag or a single message; this response is neither.",
    evidence: ["Expected: { slug: [...] }", "Received: { error: 'duplicate' }", "POST /brands → 409"],
    nextStep: "Keep the expectation as the contract says, mark the test skip with FIXME, and report the mismatch.",
    endpoint: "POST /brands",
    files: ["brand.spec.ts", "brand.schema.ts"],
    alsoFails: "no other test uses this endpoint",
    curl: `curl -X POST 'https://api.practicesoftwaretesting.com/brands' \\
  -H 'Content-Type: application/json' \\
  --data-raw '{"name":"Demo","slug":"demo"}'`,
    cost: "$0.0002",
    latency: "1.1 s",
    firstSeen: "Oct 26, 14:02",
    spark: ["pass", "pass", "fail", "pass", "fail", "fail", "pass", "fail", "fail", "pass", "fail", "fail", "fail", "fail"],
  },
];

export const categoryLabel: Record<Category, string> = {
  backend_bug: "Backend bug",
  test_bug: "Test bug",
  environment: "Environment",
  flaky: "Flaky",
};

export const statusLabel: Record<FailureStatus, string> = {
  new: "NEW",
  recurring: "RECURRING",
  regressed: "REGRESSED",
};
