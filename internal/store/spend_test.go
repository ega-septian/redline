package store

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestSpendSince(t *testing.T) {
	s := TestStore(context.Background(), t, "redlinetest-spend")
	ctx := context.Background()

	if got, err := s.SpendSince(ctx, time.Now().Add(-time.Hour)); err != nil || got != 0 {
		t.Fatalf("awal harus 0: %v %v", got, err)
	}
	if err := s.RecordSpend(ctx, "", 0.25); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSpend(ctx, "", 0.5); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSpend(ctx, "", 0); err != nil {
		t.Fatalf("biaya 0 tidak perlu dicatat: %v", err)
	}
	if err := s.RecordSpend(ctx, "toolshop", 0.4); err != nil {
		t.Fatal(err)
	}

	got, err := s.SpendSince(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1.15) > 1e-9 {
		t.Errorf("total biaya = %v, harusnya 1.15", got)
	}
	if project, err := s.SpendSinceProject(ctx, time.Now().Add(-time.Hour), "toolshop"); err != nil ||
		math.Abs(project-0.4) > 1e-9 {
		t.Errorf("biaya project = %v, harusnya 0.4 (%v)", project, err)
	}
	if other, err := s.SpendSinceProject(ctx, time.Now().Add(-time.Hour), "other"); err != nil || other != 0 {
		t.Errorf("project lain harus 0: %v %v", other, err)
	}
	if none, err := s.SpendSince(ctx, time.Now().Add(time.Hour)); err != nil || none != 0 {
		t.Errorf("setelah jendela harus 0: %v %v", none, err)
	}
}
