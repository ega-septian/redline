package store_test

import (
	"context"
	"testing"

	"redline/internal/store"
)

func TestProjectSummariesAndFilters(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	web := passedTest("web checkout")
	web.Tests[0].ProjectName = "web"
	ingest(t, s, shopReport(passedTest("api list"), web), store.RunMeta{Branch: "main", CommitSHA: "one"})
	ingest(t, s, shopReport(failedTest("api create", "boom", "")), store.RunMeta{Branch: "main", CommitSHA: "two"})
	if _, err := s.SetContractSource(ctx, store.ContractSource{
		Project: "worker", URL: "https://worker.example.com/docs", Enabled: true, By: "ega",
	}); err != nil {
		t.Fatal(err)
	}

	projects, err := s.ProjectSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byProject := map[string]store.ProjectSummary{}
	for _, project := range projects {
		byProject[project.Project] = project
	}
	api, ok := byProject["api"]
	if !ok || api.Tests != 2 || api.Runs != 2 || api.OpenFailures != 1 || api.LastRunAt == nil {
		t.Errorf("api salah: %+v", api)
	}
	webSummary, ok := byProject["web"]
	if !ok || webSummary.Tests != 1 || webSummary.Runs != 1 || webSummary.OpenFailures != 0 || webSummary.LastRunAt == nil {
		t.Errorf("web salah: %+v", webSummary)
	}
	worker, ok := byProject["worker"]
	if !ok || worker.Tests != 0 || worker.Runs != 0 || worker.LastRunAt != nil {
		t.Errorf("worker dari contract source salah: %+v", worker)
	}

	if runs, err := s.ListRunsForProject(ctx, "web", 10); err != nil || len(runs) != 1 {
		t.Errorf("run web salah: %+v %v", runs, err)
	}
	if runs, err := s.ListRunsForProject(ctx, "api", 10); err != nil || len(runs) != 2 {
		t.Errorf("run api salah: %+v %v", runs, err)
	}
	if runs, err := s.ListRunsForProject(ctx, "", 10); err != nil || len(runs) != 2 {
		t.Errorf("run semua salah: %+v %v", runs, err)
	}
	if groups, err := s.ListGroupsForProject(ctx, nil, 10, "api"); err != nil || len(groups) != 1 {
		t.Errorf("group api salah: %+v %v", groups, err)
	}
	if groups, err := s.ListGroupsForProject(ctx, nil, 10, "web"); err != nil || len(groups) != 0 {
		t.Errorf("group web salah: %+v %v", groups, err)
	}
}
