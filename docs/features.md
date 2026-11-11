# Feature details

This page explains what each Redline screen does. For setup, see the [README](../README.md); for HTTP endpoints, see the [API reference](api.md).

## Failures and triage

The Failures page is the main workspace:

- Failures with the same error are grouped into one incident, so 40 failed tests do not become 40 problems.
- Each group shows its category (`backend_bug`, `test_bug`, `environment`, `flaky` or `unknown`), confidence, summary, next step, owner and run history.
- **Evidence** is quoted from the stored facts: error message, HTTP request/response shape, contract diff and a curl to reproduce it.
- **Triage health** shows aging buckets (0–1 day, 2–7 days, more than 7 days) and owner workload. Muted and won't-fix groups are excluded.
- Labels and triage status are stored with the person who changed them. `resolved` is automatic when the test passes again.
- The **Knowledge** tab builds a Markdown page from stored facts: test identity, cause, evidence, history, graph facts and proof experiments. It never calls an LLM.

### AI modes

`AI_MODE=fallback` (default) asks AI only when rules are not high-confidence. `AI_MODE=always` asks AI for every requested failure group and keeps the rule result as fallback if AI is disabled, over budget, or over the per-run limit.

Analyses are requested by `REDLINE_AI=1 npx playwright test`, `npx redline analyze`, or the Analyze action in the UI. The server does not call AI for every ingest automatically. `AI_MAX_PER_RUN` defaults to 20 and falls back to rules after the limit; `AI_DAILY_BUDGET_USD` caps the total daily spend.

`AUTO_ACCEPT=high` (default) marks high-confidence rule/AI results as **accepted**, so the Failures page does not ask for confirmation on clear cases. Accepted is not human truth: it is a separate field, and the scoreboard and rule learning still use only human labels and proven experiments. Lower-confidence re-analysis clears a stale acceptance; a human label always wins. On startup Redline backfills existing high-confidence analyses, so groups created before the feature get the same state without a manual re-analysis.

Calibration is available from `GET /api/auto-accept/stats` and `npx redline score`: how many groups were accepted, confirmed by a human, overridden to another category, still pending, and the override rate of decided cases. The endpoint accepts `?project=...`, and `npx redline score --project=<name>` scopes the same numbers. Once at least 10 cases are decided, an override rate above 10% raises a warning in the API response, the CLI and the Config card. The warning never disables auto-accept automatically; set `AUTO_ACCEPT=off` when you want to stop it.

The Failures page has an **Accepted** filter and a **Confirm accepted** bulk action. Confirming promotes the automatic category to a human label, which also feeds the scoreboard and rule learning.

## Runs and comparison

The Runs page lists every uploaded run with branch, commit, source, result counts, duration and who triggered it. Open a run to see:

- every test result, including retries, flaky status and artifacts;
- a comparison with the previous run on the same branch;
- the exact tests that changed, broke or started passing.

## Test health

The Health page summarizes a window (7, 30 or 90 days) and the selected project:

- pass rate, flake rate, open/new/resolved failures and MTTR;
- a per-run pass/fail/flaky trend;
- flakiest tests, most failed tests, slowest tests and duration regressions;
- per-test history, last/previous duration and owner;
- suite load errors that happened before a test could run.

A duration regression must be at least 30% and 100 ms slower than the previous executed result to appear.

## Suite load errors

Some runs fail before Playwright can report a test: a broken import, a missing package, a syntax error, a failed global setup or an unreachable service.

Redline stores these separately from test failures, groups identical messages across runs, and classifies them with deterministic rules. Each group gets a summary and next step. If the rules return `unclear`, an admin can opt into **Analyze with AI**; the result is validated, budget-capped and stored with source `ai`.

## Quarantine

A quarantine is an explicit, expiring exception for a known flaky or broken test. Every active quarantine needs an owner, a reason and an expiry between 1 and 90 days.

- The Quarantine page shows active exceptions, age, days left and a monthly expiry calendar.
- Releasing a quarantine returns the test to the normal gate immediately.
- Expiry is not a release: a test that is still failing will fail the gate again after its `until` date.
- The Health page suggests quarantines using evidence: at least 5 runs, a recent failure and repeated pass/fail flips. Nothing is quarantined automatically.

## Coverage

Coverage compares the newest OpenAPI snapshot with the calls recorded by the Redline fixture:

- documented endpoints vs covered endpoints;
- observed and documented status codes;
- contract changes between runs;
- which tests call each endpoint.


## Config

Config is the only place to manage runtime configuration:

- **Contract sources** — OpenAPI JSON URLs, validated from the server and applied without a restart.
- **API keys** — create, list and revoke Bearer keys for CI and other projects. The secret is shown once; only its SHA-256 hash is stored.
- **AI usage** — mode, model, global and selected-project spend, remaining budget and per-run cap.
- **Auto-accept calibration** — accepted/confirmed/overridden/pending and the override-rate warning.
- **Notifications** — a default Discord webhook and optional per-project overrides, with delivery history.

The configuration lives in Neo4j, so backing up the database also backs up these settings.

## Proof and knowledge

`npx redline verify` tries to turn a guess into proof in a temporary copy of the project:

1. rerun the test as-is; if it passes, it is flaky;
2. ask Claude for the smallest patch and rerun; if it passes, the cause is proven.

The patch is saved to `test-results/redline/<id>.patch`. Patches that remove assertions or add `skip`, `try/catch` or retries are rejected.

`npx redline fix <id>` takes a **proven `test_bug` patch** (reusing a stored experiment unless `--force`), creates a branch from `HEAD`, commits it, pushes it, and opens a PR against `main` (override with `REDLINE_PR_BASE` or `--base=<branch>`). The PR body carries the cause, evidence, curl, experiment, patch and a note that it was not auto-merged. Use `--dry-run` to print the branch and body without pushing. The command refuses patches that touch `.env`, CI workflows, secrets or generated directories, and requires `GITHUB_TOKEN` or an authenticated `gh`.

Shadow rules are the safe next step for rule learning: `npx redline shadow <id>` evaluates a rule against new failures and records what it would say, but never changes the analysis. `npx redline shadow --stats [--project=<name>]` compares those matches with human labels. A rule is ready to promote after at least 10 matches, 5 decided cases and 90% agreement; `npx redline shadow --promote <id>` moves it to `proposed`, and activating it is still a human `approve`.

## Reports

- **Markdown** — `test-results/redline-report.md`, with the cause, evidence and a curl per failed request.
- **Sticky PR comment** — `npx redline comment` creates or updates one comment marked `<!-- redline-report -->`. See [GitHub Actions](github-action.md).
- **Allure** — Redline adds a `redline` label, the explanation and a proven patch, so failures can be grouped by cause.
- **ReportPortal** — failed tests get a defect type and a comment with the cause, evidence and next step.

## Privacy

| Data | When |
| --- | --- |
| Test results, error messages and the code around the error | every run |
| File paths and content hashes of the test code and its imports | every run |
| Response shapes: field names and types, **no values** | with the fixture |
| Source code of failed tests | failed tests only |

Everything is redacted before it is stored, including tokens, passwords, JWTs, card/NIK numbers, email addresses and phone numbers. Response values are never stored.

AI is opt-in and budget-capped. With AI on, the redacted facts go to Claude and redacted error messages go to Voyage AI for similarity search. Without AI, nothing leaves the server for analysis.
