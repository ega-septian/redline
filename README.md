# Redline

When a Playwright test fails, the first question is always the same: **is it a bug in the app, a bug
in the test, or is the server just down?** Redline answers that for you.

Every test run is sent to Redline, which:

- **Groups repeated failures.** The same test failing with the same error across ten runs is one
  problem, not ten. Each group is tracked as open, resolved, or regressed.
- **Merges failures with a shared cause.** If the server is down and 300 tests fail, you see one
  cause, not 300 rows.
- **Explains why it failed**: backend bug, test bug, environment, or flaky. Cheap rules go first;
  Claude is only asked when the rules can't decide.
- **Proves it when you ask.** It reruns the test, lets the AI patch it in a copy of your project,
  and runs it again. If it passes, the cause is proven, not guessed.

Tokens, passwords, JWTs and 16-digit numbers are redacted before anything is stored or sent to AI.

```mermaid
flowchart TD
    A[npx playwright test] -->|reporter sends results| B[Redline]
    B --> C[Group failures<br/>and merge shared causes]
    C --> D{Labelled by a human<br/>or already proven?}
    D -->|yes| R[Verdict]
    D -->|no| E{A rule is sure?}
    E -->|yes| R
    E -->|no| F[Claude reads the facts:<br/>error, test code, API contract,<br/>what changed, similar cases]
    F --> R
    R --> G[Report in the terminal<br/>and redline-report.md]
    G -.->|optional: verify| H[Rerun the test,<br/>then patch it in a copy]
    H -->|passes| I[Proven cause<br/>+ patch to review]
    I -.->|learn| J[Rules and similar cases<br/>for next time]
    J -.-> E
```

### How accurate is it?

Measured with 24 bugs planted on purpose (12 in test code, 9 in the backend, 3 environment) and
6 app-upgrade cases, using Claude Haiku 5.5:

| Scenario | Correct | Wrong guesses |
|---|---|---|
| Brand-new test, no history | 92% | 0 |
| Test that passed before | 100% | 0 |
| App upgrade: outdated test vs regression | 6/6 | 0 |

When Redline isn't sure, it says "unclear" instead of guessing. One run per scenario; AI results can
vary a little between runs. The benchmark lives in the Playwright project
([`npm run redline:bench`](https://github.com/ega-septian/playwright-web-api-automation)).

## Quick start

You need Go 1.24+ and Docker.

```bash
cp .env.example .env
docker compose up -d     # Postgres with pgvector on port 5433
go run ./cmd/server      # API on http://localhost:8787, tables are created on start
```

Everything below is optional; Redline works without it, just with less insight:

| Setting in `.env` | What it adds |
|---|---|
| `ANTHROPIC_API_KEY` | AI analysis and AI patches. Without it, only rules are used |
| `VOYAGE_API_KEY` | Finds past proven cases with a similar error ([Voyage AI](https://dashboard.voyageai.com), first 200M tokens free) |
| `OPENAPI_SPECS` | Your API contract, e.g. `toolshop=http://localhost:8091/docs`, so Redline knows who is right: the test or the API |

## Connect your Playwright project

Copy these files from
[playwright-web-api-automation](https://github.com/ega-septian/playwright-web-api-automation):

| File | Purpose |
|---|---|
| `reporters/redline.ts`, `reporters/source-hash.ts` | Send results to Redline and print the summary |
| `fixtures/redline.ts` | Records the shape of API responses and a curl per request. Optional, but makes the analysis much better |
| `scripts/redline.mts`, `scripts/sandbox.mts` | CLI to prove causes, learn rules and see the scoreboard. Optional |

Add the reporter after the JSON reporter in `playwright.config.ts`, and import `test` from the
fixture in your specs:

```ts
reporter: [
  ["html"],
  ["json", { outputFile: "test-results/results.json" }],
  ["./reporters/redline.ts"],
],
```

```ts
import { test, expect } from "../../../fixtures/redline";
```

That's it. After every `npx playwright test` you get a summary (the output is in Indonesian):

```
[redline] run #12 · 8 Okt 2026, 19.36 WIB: 40 lulus, 9 gagal, 0 flaky, 1 skip
  9 kegagalan dari 2 penyebab

  #  INSIDEN                    TEST  STATUS     KATEGORI     PENYEBAB
  1  POST /users/login → 500       8  BARU       BUG BACKEND  API membalas 500 padahal test…
  2  TC-USR-001 Register user      1  REGRESSED  BUG DI TEST  Test memeriksa objek response…

  Laporan lengkap: test-results/redline-report.md
```

The full report, with a curl command to reproduce each failed request, goes to
`test-results/redline-report.md` (and to the job summary on GitHub Actions). If the Redline server
is down, tests still run normally.

Give tests an ID with a tag, and Redline will use it in reports: `test("...", { tag: "@TC-USR-001" }, ...)`.

| Environment variable | Effect |
|---|---|
| `REDLINE_AI=1` | Ask for the cause of each failure |
| `REDLINE_VERIFY=1` | Right after the run, prove each new failure (local only, ignored in CI) |
| `REDLINE_URL` | Server address, default `http://localhost:8787` |
| `REDLINE_APP_VERSION` | Version of the app under test, e.g. `sprint2` |
| `REDLINE=0` | Turn Redline off |

The CLI (Node 22.6+), run from your Playwright project:

```bash
node scripts/redline.mts verify      # prove the cause of the last run's failures
node scripts/redline.mts learn       # turn proven cases into rules
node scripts/redline.mts rules       # list rules; approve <id> / reject <id>
node scripts/redline.mts score       # how often the guesses turned out right
```

No reporter? You can also post the JSON report yourself:

```bash
curl -X POST "http://localhost:8787/api/runs?source=local" --data-binary @test-results/results.json
```

## How Redline decides

It tries the cheapest, most trustworthy source first:

1. **A human label**, if someone already labelled the failure.
2. **Proof from an experiment** (see below).
3. **Rules**, free and instant. For example: server unreachable → environment; expected 2xx but got
   5xx → backend bug; checking a `Promise` or an `APIResponse` instead of its body → test bug
   (missing `await` or `.json()`); response breaks the API contract → backend bug; plus rules you
   approved yourself.
4. **Claude**, with the redacted facts: error, the failed test's source code, response shapes, the
   API contract, what changed since the last pass, and similar proven cases.

The most useful signal is what changed since the test last passed:

| Test code | API response | API contract | Verdict |
|---|---|---|---|
| same | changed | same | **backend bug** (regression) |
| same | changed | changed the same way | **test bug**: the change was intended, the test is outdated |
| changed | same | — | **test bug** (Claude explains, since it can read the code) |

The contract is snapshotted on every run, so after an upgrade (say sprint 1 → sprint 2) a failure
is judged against the contract that was live when it failed.

To keep the AI honest, every piece of evidence it gives must be a quote from the facts it was sent.
Made-up evidence is dropped and the confidence is lowered. One analysis costs about $0.0002–0.0007.

## Proving a cause

A guess is still a guess. `verify` tests it:

1. **Rerun** the test twice as is. If it passes, the failure isn't consistent: flaky.
2. **Patch.** Claude proposes the smallest change to the test that would fix it, and the test is run
   again with that change. If it passes, the cause is proven.

Everything runs in a temporary copy of your project, so your code is never touched. A proven patch
is saved to `test-results/redline/<id>.patch` for you to review and `git apply`. Patches that remove
assertions, add `skip`/`only`, wrap code in `try/catch` or add retries are rejected automatically.
A verify costs about $0.001.

The patch is a minimal fix that proves the cause, not a code review: always read it before applying.

## Learning

- **Similar cases.** Error messages are turned into embeddings (Voyage AI, stored with pgvector), so
  a new failure can be matched with past *proven* cases that mean the same thing, even with
  different wording. These are given to Claude as references, never as proof.
- **Code map.** Redline knows which files and endpoints each test uses. "Three other tests on
  `GET /brands` passed" points at this test; "every test using this schema failed" points at the
  shared schema or the backend.
- **Learned rules.** `learn` turns proven cases into regex rules. Each rule is checked against past
  failures (it must not match a case with a different cause) and only becomes active after you
  approve it.
- **Scoreboard.** Every guess is recorded and graded once there is proof, so you can see whether a
  change actually made Redline more accurate.

## What is sent

| Data | When |
|---|---|
| Test results, error messages, a code snippet around the error | every run |
| A hash of each test's code and the list of local files it imports | every run |
| Response shapes: field names and types only, **no values** | every run, with the fixture |
| **The source code of failed tests** (spec plus local imports) | failed tests only |

Everything is redacted before it is stored. With AI enabled, those redacted facts are sent to
Claude, and redacted error messages to Voyage AI.

## Working as a team

| Server setting | Default | |
|---|---|---|
| `STATUS_FROM` | `ci` | Which runs may change a failure's status: `ci`, `local` or `all` |
| `TIMEZONE` | `Asia/Jakarta` | Time zone for the API, logs and database |
| `RETENTION_DAYS` | `30` | Old test details are cleaned up after this many days (`0` = never) |

Only CI changes status by default. Otherwise a half-written test on someone's laptop would show up
as a team failure, and a lucky local pass would close a failure that still breaks in CI. Local runs
are still stored and analysed, as a preview. Working alone? Use `STATUS_FROM=all`.

## API

| Endpoint | |
|---|---|
| `POST /api/runs` | Send a Playwright JSON report |
| `GET /api/runs` | Recent runs |
| `GET /api/groups` | Failures to work on (`?status=all` for every failure) |
| `GET /api/groups/{id}` | One failure: occurrences, experiments and analysis |
| `POST /api/groups/{id}/analyze` | Find the cause (`?force=1` to redo) |
| `PUT /api/groups/{id}/label` | Label it yourself: `{"label": "test_bug", "note": "...", "by": "name"}` |
| `POST /api/groups/{id}/fix` | Ask Claude for a hypothesis and patch (used by `verify`) |
| `POST /api/groups/{id}/experiments` | Store an experiment result (used by `verify`) |
| `GET /api/rules`, `POST /api/rules/propose`, `PUT /api/rules/{id}` | Learned rules |
| `GET /api/scoreboard` | How often guesses matched the proof |
| `GET /healthz` | Health check |

Labels are `backend_bug`, `test_bug`, `environment`, `flaky` or `unknown`.

## Development

```
cmd/server/          HTTP server
internal/report/     reads Playwright JSON reports
internal/triage/     redaction, grouping, shared causes
internal/analysis/   rules, AI, experiments, learning
internal/contract/   OpenAPI summaries and response checks
internal/embed/      Voyage AI client
internal/llm/        Claude client
internal/store/      Postgres: schema, queries, retention
internal/api/        HTTP handlers
```

```bash
go test ./...    # tests that need Postgres are skipped

REDLINE_TEST_DATABASE_URL="postgres://redline:redline@localhost:5433/redline_test?sslmode=disable" go test ./...
```

`docker compose` creates the `redline_test` database. Each test package recreates its own schema
there, so never point it at your real database. No real AI calls are made in tests. The schema,
with a comment on every column, is in [`internal/store/migrations/`](internal/store/migrations/).

## What's next

- **ReportPortal integration**: read "To Investigate" items, write back the defect type, comment
  and proven patch, and learn from the team's corrections.
- Analyse errors that stop a spec from loading at all (syntax errors, broken imports).
- An inferred contract for APIs without documentation, built from responses of passing tests.
