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
	if err := s.RecordSpend(ctx, 0.25); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSpend(ctx, 0.5); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSpend(ctx, 0); err != nil {
		t.Fatalf("biaya 0 tidak perlu dicatat: %v", err)
	}

	got, err := s.SpendSince(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-0.75) > 1e-9 {
		t.Errorf("total biaya = %v, harusnya 0.75", got)
	}
	if none, err := s.SpendSince(ctx, time.Now().Add(time.Hour)); err != nil || none != 0 {
		t.Errorf("setelah jendela harus 0: %v %v", none, err)
	}
}
