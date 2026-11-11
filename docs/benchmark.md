# Benchmark: AI modes

This page records how the three analysis modes scored on a fixed set of planted bugs, and why
`AI_MODE=fallback` is the default.

Measured on 11 November 2026 with Redline v1.15.0, model `claude-haiku-5-5`, prompt `v11`.

## Setup

| Item | Value |
| --- | --- |
| Target | Toolshop sprint 1, hosted at `https://api-v1.practicesoftwaretesting.com` |
| Scenario | `history` (every test passed before the bug was planted) |
| Cases | 12: 5 `test_bug`, 4 `backend_bug`, 3 `environment` |
| Memory | off |
| Contract | on (OpenAPI from `/docs`) |
| Model | `claude-haiku-5-5` |
| Pricing | $0.10 per million input tokens, $0.50 per million output tokens |
| `AI_MAX_PER_RUN` | 50 |
| `AI_DAILY_BUDGET_USD` | 0 (no cap for the benchmark) |

The cases are planted bugs from the benchmark harness: a mix of broken tests, broken backend
responses and unreachable hosts. Each case has one known answer, so a wrong high-confidence answer
can be counted separately from an honest `unknown`.

Cases: `T01`, `T03`, `T04`, `T06`, `T08`, `B01`, `B02`, `B04`, `B06`, `E01`, `E02`, `E03`.

## Results

| Mode | Decided | Correct | Unresolved | Effective accuracy | AI calls | Cost |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Rules only | 10 | 10 | 2 | 10/12 (83%) | 0 | $0 |
| `fallback` | 12 | 12 | 0 | 12/12 (100%) | 3 | $0.0021 |
| `always` | 12 | 12 | 0 | 12/12 (100%) | 11 | $0.0077 |

Effective accuracy counts unresolved cases as not correct. The harness report shows rules only as
10/10 (100%) because it drops error cases from the denominator; this page keeps them so the modes
are comparable.

Wrong high-confidence answers: **0** in every mode.

### By category

| Mode | BUG DI TEST | BUG BACKEND | ENVIRONMENT |
| --- | ---: | ---: | ---: |
| Rules only | 3/5 (2 unresolved) | 4/4 | 3/3 |
| `fallback` | 5/5 | 4/4 | 3/3 |
| `always` | 5/5 | 4/4 | 3/3 |

### Which source decided

| Case | Bug | Rules only | `fallback` | `always` |
| --- | --- | --- | --- | --- |
| T01 | missing `await response.json()` | rules | rules | rules |
| T03 | shared schema has the wrong type | unresolved | AI | AI |
| T04 | wrong expected status (200 instead of 201) | rules (medium) | AI | AI |
| T06 | typo in the endpoint path | rules | rules | AI |
| T08 | test payload misses a required field | unresolved | AI | AI |
| B01 | `GET /brands` returns 500 | rules | rules | AI |
| B02 | `GET /brands` drops `slug` | rules | rules | AI |
| B04 | `POST /brands` returns 200 instead of 201 | rules | rules | AI |
| B06 | `GET /categories` renames `parent_id` | rules | rules | AI |
| E01 | wrong port, server unreachable | rules | rules | AI |
| E02 | unknown host | rules | rules | AI |
| E03 | connection dropped mid-request | rules | rules | AI |

## Conclusion

- Rules alone decide 10 of 12 cases and never answer with the wrong category, but they leave two
  test bugs unresolved. Those are the cases AI exists for.
- `fallback` closes both gaps with 3 AI calls. It matches `always` on category accuracy.
- `always` calls AI even for failures the rules already decide correctly. On this sample it adds no
  category accuracy and costs 3.6 times more.

Keep the default:

```dotenv
AI_MODE=fallback
AI_MAX_PER_RUN=20
AI_DAILY_BUDGET_USD=1
```

`AI_MODE=always` stays available for projects that want an AI explanation for every requested
failure, but it is not the default.

## Caveats

- 12 cases, one run, one scenario. This is a smoke benchmark, not a statistically strong claim.
- Accuracy is measured on planted bugs in a benchmark project, not on production failures.
- Latency is not measured by the harness.
- Costs come from token usage at the built-in pricing above; provider prices can change.
- AI answers vary between runs. Repeat the benchmark before quoting a new model or prompt version.

## Reproduce

The harness lives in the test project, not in this repository. From a project that has the
benchmark harness and the Toolshop specs:

```bash
# Rules only
ANTHROPIC_API_KEY= AI_DAILY_BUDGET_USD=0 AI_MAX_PER_RUN=50 \
npm run redline:bench -- --only=T01,T03,T04,T06,T08,B01,B02,B04,B06,E01,E02,E03 \
  --scenario=history --memory=off

# Fallback
AI_MODE=fallback AI_DAILY_BUDGET_USD=0 AI_MAX_PER_RUN=50 \
npm run redline:bench -- --only=T01,T03,T04,T06,T08,B01,B02,B04,B06,E01,E02,E03 \
  --scenario=history --memory=off

# Always
AI_MODE=always AI_DAILY_BUDGET_USD=0 AI_MAX_PER_RUN=50 \
npm run redline:bench -- --only=T01,T03,T04,T06,T08,B01,B02,B04,B06,E01,E02,E03 \
  --scenario=history --memory=off
```

Each run writes a Markdown and JSON report under `test-results/redline-bench/`.
