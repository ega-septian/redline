package store_test

import (
	"context"
	"testing"

	"redline/internal/store"
)

func TestCoverageIgnores(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	ignore := store.CoverageIgnore{
		Project: "toolshop", Method: "GET", Path: "/brands", Status: "404",
		Reason: "No list input can trigger 404", By: "ega",
	}
	got, err := s.SetCoverageIgnore(ctx, ignore)
	if err != nil || got.Project != "toolshop" || got.Status != "404" || got.CreatedAt.IsZero() {
		t.Fatalf("ignore = %+v, %v", got, err)
	}
	ignore.Reason = "updated"
	if _, err := s.SetCoverageIgnore(ctx, ignore); err != nil {
		t.Fatal(err)
	}
	list, err := s.CoverageIgnores(ctx, "toolshop")
	if err != nil || len(list) != 1 || list[0].Reason != "updated" || list[0].By != "ega" {
		t.Fatalf("ignores = %+v, %v", list, err)
	}
	if other, err := s.CoverageIgnores(ctx, "other"); err != nil || len(other) != 0 {
		t.Errorf("project lain harus kosong: %+v %v", other, err)
	}
	if err := s.DeleteCoverageIgnore(ctx, "toolshop", "GET", "/brands", "404"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.CoverageIgnores(ctx, "toolshop"); len(list) != 0 {
		t.Errorf("ignore harus terhapus: %+v", list)
	}
	if err := s.DeleteCoverageIgnore(ctx, "toolshop", "GET", "/brands", "404"); err != nil {
		t.Errorf("delete idempotent: %v", err)
	}
}

func TestContractCoverageData(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	first := ingest(t, s, loadReport(t, "run1-bug.json"), store.RunMeta{Branch: "main"})
	second := ingest(t, s, loadReport(t, "run2-fixed.json"), store.RunMeta{Branch: "main"})

	contract := `{"openapi":"3.0.0","paths":{"/brands":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	if _, err := s.SaveRunContract(ctx, first.RunID, "toolshop", "http://example/docs", contract); err != nil {
		t.Fatal(err)
	}
	next := `{"openapi":"3.0.0","paths":{"/brands":{"get":{"responses":{"200":{"description":"ok"},"401":{"description":"auth"}}}}}}`
	if _, err := s.SaveRunContract(ctx, second.RunID, "toolshop", "http://example/docs", next); err != nil {
		t.Fatal(err)
	}

	snapshots, err := s.ContractSnapshots(ctx, "toolshop", 2)
	if err != nil || len(snapshots) != 2 {
		t.Fatalf("snapshots salah: %+v %v", snapshots, err)
	}
	if snapshots[0].RunID != second.RunID || snapshots[0].Document != next {
		t.Errorf("snapshot terbaru salah: %+v", snapshots[0])
	}
	if other, err := s.ContractSnapshots(ctx, "other", 2); err != nil || len(other) != 0 {
		t.Errorf("project lain harus kosong: %+v %v", other, err)
	}

	calls, err := s.EndpointCalls(ctx, "toolshop")
	if err != nil || len(calls) == 0 {
		t.Fatalf("endpoint calls kosong: %+v %v", calls, err)
	}
	found := false
	for _, call := range calls {
		if call.RunID == 0 || call.Method == "" || call.Path == "" || call.TestKey == "" || call.At.IsZero() {
			t.Errorf("call tidak lengkap: %+v", call)
		}
		if call.Status != 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("tidak ada status code pada calls: %+v", calls)
	}
	if other, err := s.EndpointCalls(ctx, "other"); err != nil || len(other) != 0 {
		t.Errorf("calls project lain harus kosong: %+v %v", other, err)
	}
}
