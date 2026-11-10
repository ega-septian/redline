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

## Coverage and impact

Coverage compares the newest OpenAPI snapshot with the calls recorded by the Redline fixture:

- documented endpoints vs covered endpoints;
- observed and documented status codes;
- contract changes between runs;
- which tests call each endpoint.

The Impact page answers the reverse question: which tests import a file (including transitive imports) or call an endpoint. That is useful before changing a shared schema or an API route.

## Config

Config is the only place to manage runtime configuration:

- **Contract sources** — OpenAPI JSON URLs, validated from the server and applied without a restart.
- **API keys** — create, list and revoke Bearer keys for CI and other projects. The secret is shown once; only its SHA-256 hash is stored.
- **Notifications** — a default Discord webhook and optional per-project overrides, with delivery history.

The configuration lives in Neo4j, so backing up the database also backs up these settings.

## Proof and knowledge

`npx redline verify` tries to turn a guess into proof in a temporary copy of the project:

1. rerun the test as-is; if it passes, it is flaky;
2. ask Claude for the smallest patch and rerun; if it passes, the cause is proven.

The patch is saved to `test-results/redline/<id>.patch`. Patches that remove assertions or add `skip`, `try/catch` or retries are rejected.

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
