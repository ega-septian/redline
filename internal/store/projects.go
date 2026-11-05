package store

import (
	"context"
	"sort"
	"time"
)

// ProjectSummary is one Playwright project known to Redline, with the signal the project selector
// needs. Projects that only have a contract source are included with zero tests/runs.
type ProjectSummary struct {
	Project      string     `json:"project"`
	Tests        int        `json:"tests"`
	Runs         int        `json:"runs"`
	OpenFailures int        `json:"open_failures"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
}

// ProjectSummaries lists distinct projects from tests and contract sources. The counts come from the
// graph, not from a cached list, so a project appears as soon as its first run is ingested.
func (s *Store) ProjectSummaries(ctx context.Context) ([]ProjectSummary, error) {
	res, err := s.query(ctx, `
		MATCH (t:Test)
		WITH coalesce(t.project, '') AS project, count(t) AS tests
		WHERE project <> ''
		OPTIONAL MATCH (run:Run)-[:HAS]->(:Result)-[:OF]->(rt:Test)
		WHERE coalesce(rt.project, '') = project
		WITH project, tests, count(DISTINCT run) AS runs, max(run.createdAt) AS lastRun
		OPTIONAL MATCH (f:Failure)
		WHERE coalesce(f.project, '') = project AND f.status IN ['open', 'regressed']
		RETURN project, tests, runs, lastRun, count(DISTINCT f) AS openFailures
		ORDER BY project`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectSummary, 0, len(res.Records))
	known := map[string]bool{}
	for _, record := range res.Records {
		project := recString(record, "project")
		known[project] = true
		summary := ProjectSummary{
			Project: project, Tests: recInt(record, "tests"), Runs: recInt(record, "runs"),
			OpenFailures: recInt(record, "openFailures"),
		}
		if value := recValue(record, "lastRun"); value != nil {
			at := local(value)
			if !at.IsZero() {
				summary.LastRunAt = &at
			}
		}
		out = append(out, summary)
	}

	sources, err := s.ContractSources(ctx)
	if err != nil {
		return nil, err
	}
	for _, source := range sources {
		if known[source.Project] {
			continue
		}
		known[source.Project] = true
		out = append(out, ProjectSummary{Project: source.Project})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runs != out[j].Runs {
			return out[i].Runs > out[j].Runs
		}
		if out[i].Tests != out[j].Tests {
			return out[i].Tests > out[j].Tests
		}
		return out[i].Project < out[j].Project
	})
	return out, nil
}
