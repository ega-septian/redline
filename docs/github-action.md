# GitHub Actions: sticky PR comment

`npx redline comment` creates or updates one PR comment with the latest `test-results/redline-report.md`.
The comment carries the marker `<!-- redline-report -->`, so a new run edits the same comment instead
of adding another.

## Workflow

```yaml
name: API tests

on:
  pull_request:

permissions:
  contents: read
  pull-requests: write # npx redline comment needs this

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: lts/*
          cache: npm

      - run: npm ci

      - name: Run tests
        run: npx playwright test
        env:
          REDLINE_URL: ${{ secrets.REDLINE_URL }}
          REDLINE_API_KEY: ${{ secrets.REDLINE_API_KEY }}
          REDLINE_AI: "1" # optional; rules-only otherwise

      - name: Redline PR comment
        if: always()
        run: npx redline comment
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          REDLINE_URL: ${{ secrets.REDLINE_URL }}
          REDLINE_API_KEY: ${{ secrets.REDLINE_API_KEY }}
```

`GITHUB_REPOSITORY` and `GITHUB_EVENT_PATH` are provided by Actions. `npx redline comment` reads the
PR number from the event; outside Actions, set `REDLINE_PR_NUMBER`.

## Before this works

- `redline-playwright` is installed from a tag that includes `comment` (after this PR is released).
- The reporter writes `test-results/redline-report.md` after `npx playwright test`.
- The workflow has `pull-requests: write`; the default `GITHUB_TOKEN` is enough.
- The Redline server is reachable from the runner; otherwise the report is still generated but has no
  analysis, and the comment contains only the run summary.

The comment is plain Markdown: run totals, grouped incidents, causes, confidence, evidence and the
relative report path. Long reports are truncated to 60,000 characters.
