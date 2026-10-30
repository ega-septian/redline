# Redline

[![CI](https://github.com/ega-septian/redline/actions/workflows/ci.yml/badge.svg)](https://github.com/ega-septian/redline/actions/workflows/ci.yml)

Failure triage for Playwright. When a test fails, Redline tells you whether it is a **backend bug**, a
**test bug**, an **environment problem** or **flaky** — with the evidence, a curl to reproduce it, and
an optional proof.

```
[redline] run #12 · 8 Okt 2026, 19.36 WIB: 40 lulus, 9 gagal, 0 flaky, 1 skip
  9 kegagalan dari 2 penyebab

  #  INSIDEN                    TEST  STATUS     KATEGORI     PENYEBAB
  1  POST /users/login → 500       8  BARU       BUG BACKEND  API membalas 500 padahal test…
  2  TC-USR-001 Register user      1  REGRESSED  BUG DI TEST  Test memeriksa objek response…
```

_The report is in Indonesian. 9 failures, 2 causes, each explained._

Install it in two steps: **run the server**, then **point your Playwright project at it**.

## Quick start

### 1. Run the server

Requires Go 1.24+ and Docker. Node.js 20+ is only needed for the Playwright package.

```bash
git clone https://github.com/ega-septian/redline.git
cd redline
cp .env.example .env
docker compose up -d        # Neo4j/DozerDB: bolt://localhost:7688, browser http://localhost:7475
go run ./cmd/server         # API on http://localhost:8787
```

Check that both the process and the database are ready:

```bash
curl -s localhost:8787/healthz   # {"status":"ok","version":"…","api":1}
curl -s localhost:8787/readyz    # {"status":"ready"}
```

Redline works without any API key. AI analysis is optional; without it, the deterministic rules still
run. To turn it on, put these in `.env` and restart the server:

```bash
ANTHROPIC_API_KEY=sk-ant-...
AI_DAILY_BUDGET_USD=5      # optional: stop AI calls after $5/day
```

### 2. Connect a Playwright project

Install the package, pinned to a release (use the same tag as your server):

```bash
npm i -D github:ega-septian/redline#v0.6.0
```

Add the reporter **after** the JSON reporter in `playwright.config.ts`:

```ts
reporter: [
  ["html"],
  ["json", { outputFile: "test-results/results.json" }],
  ["redline-playwright/reporter"],
],
```

Import `test` and `expect` from the fixture in your specs. It records response shapes and a curl per
request:

```ts
import { test, expect } from "redline-playwright/fixture";

test("Register user", { tag: "@TC-USR-001" }, async ({ request }) => {
  const response = await request.post("/users/register", { data: payload }); // your payload
  expect(response.status()).toBe(201);
});
```

Give every test one unique `@TC-` tag. It appears in reports and keeps a test's history when you rename
its title, `describe` or file.

Run your tests as usual:

```bash
npx playwright test
cat test-results/redline-report.md     # causes, evidence, curl per failed request
```

Explain the failures with AI as well:

```bash
REDLINE_AI=1 npx playwright test
```

If the Redline server is down, your tests still run normally.

## What it does

- **Groups failures.** The same failure over ten runs is one problem; 300 tests failing because the
  server is down is one cause.
- **Explains each cause**: backend bug, test bug, environment or flaky. Free rules first, Claude only
  when rules cannot decide.
- **Proves a cause on request** by rerunning the test and patching it in a temporary copy of your
  project.
- **Knows how tests connect**: which files and endpoints each test uses, so it can tell you which tests
  a change affects.
- **Redacts secrets and PII** (tokens, passwords, JWTs, card numbers, emails, phone numbers) before
  anything is stored or sent to AI.

## Requirements

| Tool       | Version | Why                                  |
| ---------- | ------- | ------------------------------------ |
| Go         | 1.24+   | Build and run the server             |
| Docker     | any     | Neo4j (DozerDB) for the graph        |
| Node.js    | 20+     | `redline-playwright` package         |
| Playwright | 1.50+   | Peer dependency of the package       |

## Configuration

Everything has a default that works on a laptop. The settings you are most likely to change:

| Server setting    | Default        | Meaning                                                          |
| ----------------- | -------------- | ---------------------------------------------------------------- |
| `HOST`            | `127.0.0.1`    | Interface to bind. A non-loopback host requires `REDLINE_API_KEY` |
| `PORT`            | `8787`         | HTTP port                                                        |
| `REDLINE_API_KEY` | empty          | Key for `/api/...`; empty is only allowed on a loopback host     |
| `STATUS_FROM`     | `ci`           | Which runs may open or close a failure: `ci`, `local`, `all`     |
| `RETENTION_DAYS`  | `30`           | Old test details are cleaned up (`0` = never)                    |
| `TIMEZONE`        | `Asia/Jakarta` | Time zone for the API and logs                                   |
| `MAX_REPORT_MB`   | `50`           | Largest accepted Playwright JSON report                           |

| AI setting                                                 | Default           | Meaning                                                       |
| ---------------------------------------------------------- | ----------------- | ------------------------------------------------------------- |
| `ANTHROPIC_API_KEY`                                        | empty             | AI analysis and patches; empty = rules only                   |
| `ANTHROPIC_MODEL`                                          | `claude-haiku-5-5` | Model used for analysis                                       |
| `ANTHROPIC_INPUT_PRICE`, `ANTHROPIC_OUTPUT_PRICE`          | empty             | USD per 1M tokens for a model that is not built in            |
| `AI_DAILY_BUDGET_USD`                                      | `0`               | Daily AI budget; when reached, rules keep running, Claude stops |
| `VOYAGE_API_KEY`                                           | empty             | Finds similar past proven cases (Voyage AI)                   |

| Integration                | Default | Meaning                                                     |
| -------------------------- | ------- | ----------------------------------------------------------- |
| `OPENAPI_SPECS`            | empty   | API contract per project, e.g. `toolshop=http://localhost:8091/docs` |
| `REPORTPORTAL_URL`, `REPORTPORTAL_API_KEY` | empty | Fill ReportPortal with causes (optional) |
| `NEO4J_URI`                | `bolt://localhost:7688` | Neo4j/DozerDB address                        |

Only CI changes a failure's status by default, so a half-written test on a laptop does not become a team
failure. Working alone? Use `STATUS_FROM=all`.

### Running a shared server

To let others reach Redline, bind it to a reachable host **and** set a key. The server refuses to start
on a non-loopback host without one.

```bash
# server .env
HOST=0.0.0.0
REDLINE_API_KEY=$(openssl rand -hex 32)

# every Playwright project .env
REDLINE_URL=http://your-server:8787
REDLINE_API_KEY=<the same key>
```

`/healthz` and `/readyz` stay open; every other `/api/...` request must send
`Authorization: Bearer <key>`. The UI asks for the same key at `/ui/login` and keeps a signed,
HttpOnly cookie for 12 hours. API reads accept that cookie; cookie writes also need the
`X-CSRF-Token` header that matches the `redline_csrf` cookie.

## Commands

From your Playwright project:

| Command                               | What it does                                         |
| ------------------------------------- | ---------------------------------------------------- |
| `REDLINE_AI=1 npx playwright test`    | Run tests and explain each failure                   |
| `npx redline verify`                  | Prove the cause of the last run's failures           |
| `npx redline impact schemas/brand.ts` | Tests affected by a file (or `impact GET /brands/1`) |
| `npx redline score`                   | How often Redline's guesses turned out right         |
| `npx redline learn`                   | Turn proven cases into rules; then `approve <id>`    |

| Client env variable   | Effect                                                  |
| --------------------- | ------------------------------------------------------- |
| `REDLINE_URL`         | Server address, default `http://localhost:8787`         |
| `REDLINE_API_KEY`     | The server's API key, if it has one                     |
| `REDLINE_AI=1`        | Explain failures with Claude                            |
| `REDLINE_AI_MAX`      | Maximum AI analyses per run, default `10`               |
| `REDLINE_VERIFY=1`    | Prove each new failure after the run (local only)       |
| `REDLINE_APP_VERSION` | Version of the app under test, e.g. `sprint5`           |
| `REDLINE=0`           | Turn Redline off                                        |

## How it decides

Redline tries the cheapest, most reliable source first:

1. **A human label**, if someone already labelled the failure.
2. **Proof** from an earlier `verify`.
3. **Rules**, free and instant: server unreachable → environment; 5xx → backend bug; missing `await`
   → test bug; response breaks the API contract → backend bug; plus rules you approved.
4. **Claude**, with the redacted error, test code, response shapes, API contract and what changed.
   Every piece of evidence must be quoted from those facts, or the confidence is lowered.

The strongest signal is **what changed since the test last passed**:

| Test code | API response | API contract         | Verdict                         |
| --------- | ------------ | -------------------- | ------------------------------- |
| same      | changed      | same                 | **backend bug** (regression)    |
| same      | changed      | changed the same way | **test bug** (test is outdated) |
| changed   | same         | —                    | **test bug**                    |

Tests, the files they import (however deep) and the endpoints they call are stored as a graph in Neo4j.
That catches what a single test cannot show: a shared schema two imports deep that changed, or an
endpoint failing for unrelated tests too. More in [`docs/kag.md`](docs/kag.md).

Measured with bugs planted on purpose, using Claude Haiku 5.5: 92–96% correct on a new test, 96–100% on
a test that passed before, 6/6 on app upgrades, and 18/18 on shared code by rules alone. When unsure,
Redline says "unclear" instead of guessing.

## Proving a cause

`npx redline verify` turns a guess into proof, in a temporary copy of your project (your code is never
touched). It uses Claude, so the server needs `ANTHROPIC_API_KEY`:

1. **Rerun** the test as is. If it passes now, it is flaky.
2. **Patch**: Claude proposes the smallest fix and the test runs again. If it passes, the cause is
   proven.

The patch is saved to `test-results/redline/<id>.patch` to review. Patches that remove assertions or add
`skip`, `try/catch` or retries are rejected. About $0.001 per verify.

## Reports

- **Markdown**: `test-results/redline-report.md`, with the cause, evidence and a curl per failed request.
- **Allure** (`allure-playwright` in the reporter list, `REDLINE_AI=1`): Redline adds a `redline` label,
  the explanation and a proven patch. Group them by cause in `allurerc.mjs`:

  ```js
  categories: [
    { name: "Backend bug (Redline)", matchers: { labels: { redline: "backend_bug" } } },
    { name: "Test bug (Redline)", matchers: { labels: { redline: "test_bug" } } },
    { name: "Environment (Redline)", matchers: { labels: { redline: "environment" } } },
    { name: "Flaky (Redline)", matchers: { labels: { redline: "flaky" } } },
    { name: "Unclear (Redline)", matchers: { labels: { redline: "unknown" } } },
  ],
  ```

- **ReportPortal**: set `REPORTPORTAL_URL` and `REPORTPORTAL_API_KEY` on Redline, install
  `@reportportal/agent-js-playwright` in the Playwright project, then run
  `REDLINE=1 REPORTPORTAL=1 npx playwright test`. Each failed test still "To Investigate" gets a defect
  type and a comment with the cause, evidence and next step. Labels set by people or Auto-Analysis are
  never overwritten.

## Privacy: what is sent

| Data                                                           | When              |
| -------------------------------------------------------------- | ----------------- |
| Test results, error messages, code around the error            | every run         |
| File paths and content hashes of the test code and its imports | every run         |
| Response shapes: field names and types, **no values**          | with the fixture  |
| **Source code of failed tests**                                | failed tests only |

Everything is redacted before it is stored, including tokens, passwords, JWTs, card/NIK numbers, email
addresses and phone numbers. With AI on, the redacted facts go to Claude and redacted error messages to
Voyage AI.

## Troubleshooting

| Symptom                                                        | Fix                                                               |
| -------------------------------------------------------------- | ----------------------------------------------------------------- |
| `go run` fails: cannot connect to Neo4j                        | `docker compose up -d`, then check `docker compose ps`            |
| Server exits on a non-loopback host                            | Set `REDLINE_API_KEY`, or use `HOST=127.0.0.1`                    |
| Test output: `server tidak bisa dihubungi`                     | Start Redline, or set `REDLINE_URL`; disable with `REDLINE=0`     |
| `npm i` fails while building the package                       | Use Node.js 20+                                                   |
| Database tests are skipped                                     | Set `REDLINE_TEST_NEO4J_URI=bolt://localhost:7688`                |
| Reporter warns about an API version mismatch                   | Use the same tag for the server and `redline-playwright`          |

## API

<details>
<summary>Endpoints and manual upload</summary>

| Endpoint                                                           | Purpose                                                        |
| ------------------------------------------------------------------ | -------------------------------------------------------------- |
| `POST /api/runs`                                                   | Send a Playwright JSON report                                  |
| `GET /api/runs`                                                    | Recent runs                                                    |
| `GET /api/groups`                                                  | Open failures (`?status=all` for every failure)                |
| `GET /api/groups/{id}`                                             | One failure: occurrences, experiments, analysis                |
| `POST /api/groups/{id}/analyze`                                    | Find the cause (`?force=1` to redo)                            |
| `PUT /api/groups/{id}/label`                                       | Label it: `{"label": "test_bug", "note": "...", "by": "name"}` |
| `GET /api/groups/{id}/knowledge`                                   | Graph facts behind a decision                                  |
| `POST /api/groups/{id}/fix`, `.../experiments`                     | Used by `verify`                                               |
| `GET /api/rules`, `POST /api/rules/propose`, `PUT /api/rules/{id}` | Learned rules                                                  |
| `GET /api/scoreboard`                                              | How often guesses matched the proof                            |
| `GET /api/impact?file=...` or `?endpoint=...`                      | Tests affected by a file or endpoint                           |
| `GET /healthz`                                                     | Liveness check                                                 |
| `GET /readyz`                                                      | Readiness: the database answers                                |

Labels: `backend_bug`, `test_bug`, `environment`, `flaky`, `unknown`.

No reporter? Post the JSON report yourself:
`curl -X POST "http://localhost:8787/api/runs?source=local" --data-binary @test-results/results.json`
(add `-H "Authorization: Bearer $REDLINE_API_KEY"` when the server has a key).

</details>

## Development

<details>
<summary>Layout, tests and migrations</summary>

```
cmd/server/          HTTP server
internal/report/     reads Playwright JSON reports
internal/triage/     redaction, grouping, shared causes
internal/analysis/   rules, AI, experiments, learning
internal/contract/   OpenAPI checks
internal/embed/      Voyage AI client
internal/llm/        Claude client
internal/store/      Neo4j graph model, queries, retention
internal/api/        HTTP handlers
playwright/src/      redline-playwright: reporter, fixture, CLI (TypeScript)
```

```bash
REDLINE_TEST_NEO4J_URI=bolt://localhost:7688 go test ./...   # without it, database tests are skipped
npm ci && npm run build                                      # the Playwright package
```

The React UI lives in [`web/`](web/README.md). Build it before `go build` to embed it; without a
build, `/ui/` serves a "UI not built" page.

Tests create and empty their own databases (`redlinetest-*`); never point them at a server with real
data. No real AI calls are made in tests. CI runs gofmt, vet and all tests against DozerDB on every pull
request, and fails if the database tests are skipped.

Migrations in `internal/store/migrations/` run one statement at a time, because Neo4j does not allow
schema commands and data writes in the same transaction. Every statement is idempotent (`IF NOT EXISTS`,
`MERGE`), so a failed migration can be rerun safely; the version is recorded only after the whole file
succeeds. Keep new statements idempotent.

</details>

## Versions

The server reports its build and API version at `/healthz` (`version`, `api`) and on every response as
`X-Redline-Version` / `X-Redline-Api-Version`. The Playwright package expects API version `1` and logs a
warning when the server serves another one, so a mismatched server and package are visible instead of
failing in a confusing way.

| Version  | Highlights                                                              |
| -------- | ----------------------------------------------------------------------- |
| `v0.6.0` | Safe binding and `/readyz`, PII redaction, AI pricing and daily budget, version handshake |
| `v0.5.0` | Playwright package installed from GitHub, optional API key, MIT license |
| `v0.4.0` | Fills ReportPortal with the cause of each failed test                   |
| `v0.3.1` | A renamed test keeps its history through its `@TC-` ID                  |
| `v0.3.0` | Knowledge graph and impact analysis                                     |
| `v0.2.0` | Neo4j (DozerDB) as the only database                                    |
| `v0.1.0` | First release, Postgres + pgvector                                      |

## What's next

- **ReportPortal, the other way**: labels corrected by the team in ReportPortal flow back into Redline as proven cases.
- Analyse errors that stop a spec from loading (syntax errors, broken imports).
- An inferred contract for APIs without documentation.

## License

[MIT](LICENSE) © 2026 Ega Septian
