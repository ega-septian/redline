# Benchmark: AI modes

This page records how the three analysis modes scored on a fixed set of planted bugs, and why
`AI_MODE=fallback` is the default.

Measured on 11 November 2026 with Redline v1.16.0, model `claude-haiku-5-5`, prompt `v11`, three
runs per mode.

## Setup

| Item | Value |
| --- | --- |
| Target | Toolshop sprint 1, hosted at `https://api-v1.practicesoftwaretesting.com` |
| Scenario | `history` (every test passed before the bug was planted) |
| Cases | 12: 5 `test_bug`, 4 `backend_bug`, 3 `environment` |
| Runs | 3 per mode, fresh benchmark database each run |
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

| Mode | Correct per run | Unresolved per run | Effective accuracy | AI calls per run | Cost per run | Cost range |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Rules only | 10/12 | 2 | 10/12 (83%) | 0 | $0 | $0 |
| `fallback` | 12/12 | 0 | 12/12 (100%) | 3 | $0.00212 | $0.00208–$0.00216 |
| `always` | 12/12 | 0 | 12/12 (100%) | 11–12 (mean 11.7) | $0.00806 | $0.00763–$0.00837 |

All nine runs together used 44 AI calls and cost $0.0305.

Effective accuracy counts unresolved cases as not correct. The harness report shows rules only as
10/10 (100%) because it drops error cases from the denominator; this page keeps them so the modes
are comparable.

Wrong high-confidence answers: **0** in all nine runs.

The first single-run measurement on v1.15.0 gave the same accuracy: 10/12, 12/12, 12/12. The three
runs above replace its cost figures with a mean and range.

### Consistency

- **Rules only** returned the same 10/12 in every run. `T03` and `T08` stayed unresolved because no
  rule matches them.
- **`fallback`** returned 12/12 in every run with exactly 3 AI calls: `T03`, `T04`, `T08`.
- **`always`** returned 12/12 in every run with 12, 12 and 11 AI calls. In the third run one AI
  response failed validation; the rule fallback decided `B06` and stayed correct.
- Confidence moved on a few `always` cases (`B01` medium/high, `T06` and `E03` low), but the category
  never changed. Low confidence is not the same as a wrong answer.

### By category

| Mode | BUG DI TEST | BUG BACKEND | ENVIRONMENT |
| --- | ---: | ---: | ---: |
| Rules only | 3/5 (2 unresolved) | 4/4 | 3/3 |
| `fallback` | 5/5 | 4/4 | 3/3 |
| `always` | 5/5 | 4/4 | 3/3 |

### Which source decided

| Case | Bug | Rules only | `fallback` | `always` |
| --- | --- | --- | --- | --- |
| T01 | missing `await response.json()` | rules | rules | AI |
| T03 | shared schema has the wrong type | unresolved | AI | AI |
| T04 | wrong expected status (200 instead of 201) | rules (medium) | AI | AI |
| T06 | typo in the endpoint path | rules | rules | AI |
| T08 | test payload misses a required field | unresolved | AI | AI |
| B01 | `GET /brands` returns 500 | rules | rules | AI |
| B02 | `GET /brands` drops `slug` | rules | rules | AI |
| B04 | `POST /brands` returns 200 instead of 201 | rules | rules | AI |
| B06 | `GET /categories` renames `parent_id` | rules | rules | AI (rules once) |
| E01 | wrong port, server unreachable | rules | rules | AI |
| E02 | unknown host | rules | rules | AI |
| E03 | connection dropped mid-request | rules | rules | AI |

`B06` was decided by rules in one `always` run because the AI response was invalid, not because
`always` skipped it.

## Conclusion

- Rules alone decide 10 of 12 cases in every run and never answer with the wrong category, but they
  leave two test bugs unresolved. Those are the cases AI exists for.
- `fallback` closes both gaps with 3 AI calls in every run. It matches `always` on category accuracy.
- `always` calls AI even for failures the rules already decide correctly. On this sample it adds no
  category accuracy and costs 3.8 times more on average ($0.00806 vs $0.00212).
- The one failed AI response in the `always` runs shows why the rule result is kept as fallback: the
  category stayed correct without a retry.

Keep the default:

```dotenv
AI_MODE=fallback
AI_MAX_PER_RUN=20
AI_DAILY_BUDGET_USD=1
```

`AI_MODE=always` stays available for projects that want an AI explanation for every requested
failure, but it is not the default.

## Caveats

- 12 cases, one scenario, three runs. This is a smoke benchmark, not a statistically strong claim.
- Accuracy is measured on planted bugs in a benchmark project, not on production failures.
- One AI response out of 36 `always` calls failed validation and fell back to rules. This is a
  robustness observation from a small sample, not a failure rate.
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
