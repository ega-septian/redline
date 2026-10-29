# Roadmap

What we build next, in order, and what "done" means. The order is deliberate: each phase gives
the next one something it needs. Decisions already made are recorded at the bottom.

Status: agreed after v0.6.0. Owner: Ega. Last updated: 2026-10-29.

## Phase 1 — Team-ready triage (3–4 weeks)

**Goal:** a QA team can use Redline every day without opening a terminal.

**Scope**

- Read-only UI in the same repository (`internal/ui`, Go templates + `go:embed`):
  Failures list and detail, Runs, Rules, Impact, Scoreboard.
- Auth: the existing Bearer key for API/CLI, plus an HttpOnly cookie for the UI. No key in
  `localStorage`.
- Labels and rule approval from the UI (the endpoints already exist). This is the first write path.
- GitHub Action + sticky PR comment: new failure vs already failing on main vs flaky.
- `Dockerfile` and `docker-compose.yml` (server + Neo4j/DozerDB) and a Deploy section in the README.
- Backup and restore notes for Neo4j/Aura, with one tested restore.

**Out of scope**

- SPA rewrite, realtime updates, multi-tenant SaaS, Kubernetes.

**Done when**

- A pilot runs in coba2 for a week: list/detail/label used without the CLI.
- Deploy is one command on a VPS; `/readyz` and version headers work.
- A restore from backup is documented and has been tried once.
- No API response changes.

## Phase 2 — Automate the API-test generation loop (prototype first)

**Goal:** a repeatable command turns an OpenAPI operation into reviewed Playwright API tests, with
verification instead of hope.

**Scope**

- `generate` command: input `METHOD /path`, read the contract (`npm run contract`), repo conventions
  (`AGENTS.md`, factories, fixtures, schemas, existing specs) and produce a draft spec + data + schema.
- Run the generated tests. On failure, ask Redline for the cause, propose a fix, rerun; reject patches
  that remove assertions or add `skip`, `try/catch` or retries.
- Record metrics: first-run pass rate, edits needed, reviewer acceptance, weakened tests (must be 0).
- Human review before merge; never auto-merge.

**Out of scope**

- Web UI tests, auto-merge, a hosted generation service, a UI for generation.

**Done when**

- The command runs end to end from a clean checkout.
- 20 generated test cases are measured: pass rate, review time, coverage of status codes, and zero
  weakened tests.
- The loop is documented so another engineer can run it.

**Notes**

- The capability already exists as an interactive agent workflow in coba2; this phase makes it
  repeatable and measurable, not a new generator from scratch.
- Prototype: 3–5 days. Hardening and metrics: 1–2 weeks.

## Phase 3 — Web UI test generation (4–8 weeks)

**Goal:** generate maintainable Playwright UI tests, not brittle recordings.

**Scope**

- Browser-assisted exploration (DOM, roles, labels, trace, console, network) as facts for the model.
- Page objects and semantic locators; no fixed waits; test data and auth setup from existing fixtures.
- Run, triage, and iterate with Phase 1/2 machinery.
- Human review; a generated test is a draft until a person accepts it.

**Out of scope**

- Autonomous merge, cross-browser matrix, visual regression.

**Done when**

- Acceptance rate and flake rate are measured over a real suite.
- Generated tests follow the repo rules (no hardcoded ids, no fixed waits, strict schemas).
- A reviewer can reject a draft and see why it was proposed.

## Phase 4 — Closed loop: generation + self-healing

**Goal:** from a failure to a verified patch PR, with a human only approving.

**Scope**

- Failure → analysis → patch in a sandbox → rerun → reject weakening → open a PR (or update the
  existing one) with evidence.
- The same loop works for generated tests and for tests written by people.
- Keep the proof: the patch is attached, the cause and evidence are in the PR body.

**Out of scope**

- Auto-merge, auto-deploy, production changes.

**Done when**

- Patch acceptance rate and false-fix rate are measured.
- A rejected patch explains which rule it would break.
- The PR body carries the cause, evidence, curl and the experiment result.

## Cross-cutting decisions

- **Backend stays Go.** No rewrite to Node/Python. The tested core (rules, graph, AI, verify) is the
  asset.
- **UI lives in this repository.** `internal/ui` with Go templates first; a SPA (`web/`, embedded via
  `go:embed`) only if the UI really needs it. One binary, one release.
- **API-first.** The UI uses the public `/api/...` shape, not Neo4j directly, so it can be split later
  without rewriting the server.
- **Rules first, AI optional.** CI runs rules only. AI is opt-in per run and capped by
  `AI_DAILY_BUDGET_USD`.
- **Model.** Claude Haiku 5.5 is the default for analysis. Any model change requires re-running the
  benchmark before quoting accuracy. OpenCode Go is an optional transport, not a different model.
- **Versioning.** Server and `redline-playwright` ship from the same tag; the consumer checks the
  alignment (`check:redline-ref`).

## Explicit non-goals

- Rewriting the backend in another language.
- A separate UI repository while Redline is self-hosted.
- Multi-tenant SaaS, SSO/SCIM before a team actually asks for it.
- Kubernetes.

## Open questions

- Per-user auth: built in (token per user + audit) or delegate to a reverse proxy (Cloudflare Access,
  nginx, office SSO)?
- When to add JUnit XML ingestion (Phase 1 or after the pilot)?
- Does the Phase 2 command live in coba2 (conventions live there) or in Redline (product surface)?
- Which model for generation: Haiku (cheap), Sonnet (quality) or a Go model after the benchmark?

## Success metrics

| Phase | Metric | Target |
|---|---|---|
| 1 | Team members using the UI weekly | 5 |
| 1 | Time to deploy a new instance | < 15 minutes |
| 2 | First-run pass rate of generated API tests | baseline, then improve |
| 2 | Weakened tests produced | 0 |
| 3 | Flake rate of generated UI tests | below the hand-written baseline |
| 4 | Patch acceptance rate | measured, no wrong fixes |

See also: [`docs/kag.md`](kag.md) for the knowledge graph, and the Playwright consumer
[`ega-septian/playwright-web-api-automation`](https://github.com/ega-septian/playwright-web-api-automation)
for the conventions Phase 2 reuses.
