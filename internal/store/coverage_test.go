package store_test

import (
	"context"
	"testing"

	"redline/internal/store"
)

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
		if call.Method == "" || call.Path == "" || call.TestKey == "" || call.At.IsZero() {
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
