# API reference

Redline exposes a small HTTP API for the reporter, CLI and custom integrations. The UI uses the same endpoints.

| Endpoint                                                           | Purpose                                                        |
| ------------------------------------------------------------------ | -------------------------------------------------------------- |
| `POST /api/runs`                                                   | Send a Playwright JSON report                                  |
| `GET /api/runs`                                                    | Recent runs (`?project=...` scopes them)                       |
| `GET /api/runs/errors?limit=20`                                    | Workspace-level runs whose spec or setup failed before tests   |
| `GET /api/load-errors?limit=50`                                    | Grouped report-level load errors with owner/status/note       |
| `PUT /api/load-errors/{id}/triage`                                 | Triage a load error: `{"status": "...", "owner": "...", "note": "...", "by": "..."}` |
| `POST /api/load-errors/{id}/analyze?ai=1`                          | Re-run rules, or ask AI for an unclear load-error group        |
| `GET /api/projects`                                                | Projects with tests, runs and open-failure counts              |
| `GET /api/runs/{id}`, `GET /api/runs/{id}/compare`                 | Run detail, and comparison with the previous run               |
| `POST /api/runs/{id}/artifacts`                                    | Upload a trace/screenshot artifact (multipart)                 |
| `GET /api/runs/{id}/artifacts`, `GET /api/artifacts/{id}`          | List/download uploaded artifacts                               |
| `GET /api/tests`                                                   | Test health: pass/flake rates, top failures, slowest tests, duration regressions and per-test rows |
| `GET /api/tests/trend?days=30&project=...&limit=20`                | One point per run: passed, failed, flaky, skipped and rates    |
| `GET /api/tests/history?test=...`                                  | One test's run timeline (`@TC-` ID or test key)                |
| `GET /api/ai/usage`                                                | AI mode, model, daily budget, spend and per-run cap            |
| `GET /api/auto-accept/stats?project=...`                           | Accepted, confirmed, overridden, pending, override rate and warning |
| `POST /api/groups/accept`                                          | Confirm auto-accepted groups: `{"fingerprints": [...], "by": "..."}` |
| `GET /api/rules/shadow/stats`                                      | Shadow rule matches vs human labels and agreement rate          |
| `GET /api/tests/quarantine`                                        | Active quarantined tests (`?project=...` scopes them)          |
| `PUT /api/tests/quarantine`                                        | Quarantine a test: `{"test": "TC-...", "owner": "...", "reason": "...", "days": 7}` |
| `DELETE /api/tests/quarantine?test=...`                            | Release a quarantine                                           |
| `GET /api/tests/quarantine/suggestions?days=30`                    | Evidence-based candidates: flips, flake rate, last failure     |
| `POST /api/tests/quarantine/suggestions/dismiss`                   | Snooze a suggestion: `{"test": "TC-...", "days": 7}`           |
| `GET /api/coverage?project=...`                                    | Contract endpoints vs recorded calls, status codes, changes and the last 20 run points |
| `GET /api/config`                                                  | Contract sources, default/project notification channels and the last delivery |
| `PUT /api/config/contracts`                                        | Validate and save a source: `{"project": "toolshop", "url": "https://.../docs"}` |
| `POST /api/config/contracts/validate`                              | Check a URL without saving it                                  |
| `POST /api/config/contracts/check`                                 | Re-check a saved source: `{"project": "toolshop"}`             |
| `GET /api/config/contracts/history?project=...&limit=20`           | Stored source checks, newest first (max 100)                   |
| `DELETE /api/config/contracts?project=...`                         | Remove a database source (env fallback returns)                |
| `PUT /api/config/notifications`                                    | Save the default webhook and events                            |
| `PUT /api/config/notifications/projects/{project}`                 | Save a project override: same body as the default channel      |
| `DELETE /api/config/notifications/projects/{project}`              | Remove an override and fall back to the default                |
| `POST /api/config/notifications/test`                              | Send a real Discord test message (`?project=...` for an override) |
| `GET /api/config/notifications/deliveries?limit=20`                | Newest deliveries with project, status, attempts and error      |
| `GET /api/keys`, `POST /api/keys`, `DELETE /api/keys/{id}`         | List, create and revoke Bearer keys (UI session only)           |
| `GET /api/groups`                                                  | Open failures (`?status=all` for every failure, `?project=...` scopes them) |
| `GET /api/groups/{id}`                                             | One failure: occurrences, experiments, analysis                |
| `POST /api/groups/{id}/analyze`                                    | Find the cause (`?force=1` to redo)                            |
| `PUT /api/groups/{id}/label`                                       | Label it: `{"label": "test_bug", "note": "...", "by": "name"}` |
| `PUT /api/groups/{id}/triage`                                      | Triage: `{"status": "acknowledged", "owner": "ega", "note": "...", "by": "ega"}` |
| `GET /api/groups/{id}/knowledge`                                   | Graph facts behind a decision                                  |
| `GET /api/groups/{id}/knowledge.md`                                | Read-only Markdown knowledge page with run and analysis citations |
| `POST /api/groups/{id}/fix`, `.../experiments`                     | Used by `verify`                                               |
| `GET /api/rules`, `POST /api/rules/propose`, `PUT /api/rules/{id}` | Learned rules                                                  |
| `GET /api/rules/candidates`, `POST /api/rules/candidates/promote` | AI rule candidates, promoted only after proof                 |
| `GET /api/scoreboard`                                              | How often guesses matched the proof                            |
| `GET /api/impact?file=...` or `?endpoint=...`                      | Tests affected by a file or endpoint                           |
| `GET /healthz`                                                     | Liveness check                                                 |
| `GET /readyz`                                                      | Readiness: the database answers                                |

Labels: `backend_bug`, `test_bug`, `environment`, `flaky`, `unknown`.
Triage statuses: `open`, `acknowledged`, `in_progress`, `muted`, `wont_fix`; `resolved` is automatic when the test passes again.

## Authentication

- `GET /healthz` and `GET /readyz` are open.
- `/api/...` accepts `Authorization: Bearer <key>` (`REDLINE_API_KEY`, a `REDLINE_USERS` key, or a key created in **Config → API keys**).
- The UI accepts a signed HttpOnly session cookie for reads. Cookie writes also need `X-CSRF-Token` matching the `redline_csrf` cookie.
- API key management (`/api/keys`) requires a UI session, not a Bearer key.

## Manual upload

No reporter? Post the Playwright JSON report yourself:

```bash
curl -X POST "http://localhost:8787/api/runs?source=local" \
  --data-binary @test-results/results.json
```

Add `-H "Authorization: Bearer $REDLINE_API_KEY"` when the server has a key.
