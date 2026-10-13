# KAG: knowledge-augmented analysis

Redline already gives Claude facts about one failure (error, test code, response, last passing run)
and similar proven cases (RAG, by meaning of the error). KAG adds knowledge that is only visible by
following relationships in the graph: which files a test really depends on, which other tests share
them, and which tests call the same endpoint.

## Questions KAG must answer

For one failure:

1. **Files.** Which files did this test use, including files imported by its imports, and which of
   them changed since the test last passed?
   Today only the spec and its *direct* imports are hashed, so a change two imports deep shows as
   "test code unchanged".
2. **Users of a changed file.** Of the other tests that use the changed file, which failed and
   which passed in this run?
3. **Users of the endpoint.** Of the other tests that call the same endpoint, which failed,
   especially tests that do *not* use the changed file?
4. **Spread.** Do the failures on this endpoint come from different specs with different code
   (points at the backend) or from one shared piece of test code (points at the test)?

Impact analysis, without a failure:

5. If file X changes, which tests are affected (through any chain of imports)?
6. If endpoint X changes, which tests call it?

## Benchmark scenario `kag`

`npm run redline:bench -- --scenario=kag` in the test project. Specs import a shared helper, which
imports a shared schema; one spec calls the same endpoint without them:

```
brands.spec.ts   ─┐
products.spec.ts ─┴→ helpers/brands.ts → schemas/brand.ts
direct.spec.ts   → GET /brands/1 without helper
```

| Case | Bug | Truth | Needs |
|---|---|---|---|
| K01 | shared schema, two imports deep: `id` becomes string | test_bug | 1, 2 |
| K02 | shared helper: path typo `/brand/:id` | test_bug | 2, 3 |
| K03 | shared schema requires a field the API never had | test_bug | 1, 2 |
| K04 | unrelated helper change **and** `/brands/1` loses `slug` | backend_bug | 3 |
| K05 | `/brands/1` returns another brand, same shape; code unchanged | backend_bug | 3, 4 |
| K06 | unrelated schema change **and** `/brands/1` loses `name` | backend_bug | 1, 3 (guard) |

K06 guards against over-correction: once KAG sees changes two imports deep, it must not blame the
test when the endpoint is also broken for tests that never import the changed file.

## Baseline (v0.2.0, no KAG), three runs

| Case | Run 1 | Run 2 | Run 3 |
|---|---|---|---|
| K01 | ✅ AI medium | ✅ AI high | ✅ AI medium |
| K02 | ➖ AI low (unclear) | ✅ AI low | ✅ AI low |
| K03 | ✅ AI medium | ✅ AI high | ✅ AI medium |
| K04 | ✅ AI high | ✅ AI high | ✅ AI high |
| K05 | ✅ AI medium | ✅ AI low | ✅ AI low |
| K06 | ✅ rule high | ✅ rule high | ✅ rule high |

17/18 correct, no wrong guesses, $0.016 for three runs. Accuracy is already high, so KAG is judged
mainly on **certainty and cost**: five of six cases depend on the AI, often with low or medium
confidence, and K02 flips between correct and unclear.

## Targets

- `kag`: 18/18 over three runs, no wrong guesses.
- K01, K02, K03, K05 decided with high confidence, ideally by rules from graph facts (no AI cost).
- K06 stays backend_bug.
- Existing scenarios do not drop: new test 92%, test that passed before 100%, upgrade 6/6.
- Impact analysis: for the `kag` specs, `schemas/brand.ts` → TC-KAG-01, 02, 03;
  `GET /brands/1` → TC-KAG-02, 03, 05.

## Stages

0. Questions, benchmark scenario and baseline (this document).
1. Graph (done): the reporter sends the import graph (`files`: path → content hash and direct
   imports, for every file reachable from the specs). Stored as `(:File)-[:IMPORTS]->(:File)`
   (latest), `(:Run)-[:HAD]->(:FileVersion)` (content per run) and
   `(:Result)-[:DEPENDS_ON]->(:File)` (every file a test uses, however deep). Test case IDs are
   already on `(:Test {caseId})`, so no separate node is needed.
2. Store queries for questions 1–6, with tests.
3. Analysis: a "graph knowledge" section for Claude, and rules where the pattern is unambiguous.
4. Impact analysis API.
5. Benchmark: `--kag=off` to compare; all targets above.
6. README, merge, tag `v0.3.0`.
