# Development

## Layout

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
web/                 React UI, embedded into the server with go:embed
```

## Build and test

```bash
# Playwright reporter, fixture and CLI
npm ci && npm run build

# React UI, embedded by go:embed
npm run build --prefix web

# Go server
go build ./cmd/server

# All Go tests, including Neo4j
REDLINE_TEST_NEO4J_URI=bolt://localhost:7688 go test ./...
```

Without `REDLINE_TEST_NEO4J_URI`, the database tests are skipped. CI always runs them against DozerDB and fails if they are skipped.

Tests create and empty their own databases (`redlinetest-*`); never point them at a server with real data. No real AI calls are made in tests.

The React UI lives in [`web/`](../web/README.md). Build it before `go build`, otherwise `/ui/` serves a "UI not built" page.

## Migrations

Migrations in `internal/store/migrations/` run one statement at a time, because Neo4j does not allow schema commands and data writes in the same transaction.

- Every statement must be idempotent (`IF NOT EXISTS`, `MERGE`).
- A failed migration can be rerun safely; the version is recorded only after the whole file succeeds.
- Add a new numbered file instead of editing an applied migration.
