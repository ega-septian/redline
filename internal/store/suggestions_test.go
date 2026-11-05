package store_test

import (
	"context"
	"testing"
	"time"
)

func TestQuarantineSuggestionDismissal(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	until := time.Now().AddDate(0, 0, 7).Truncate(time.Second)
	if err := s.DismissQuarantineSuggestion(ctx, "TC-A", until, "ega"); err != nil {
		t.Fatal(err)
	}
	got, err := s.SuggestionDismissals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["TC-A"].IsZero() {
		t.Fatalf("dismissal tidak tersimpan: %+v", got)
	}
	if diff := got["TC-A"].Sub(until); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("until = %v, mau %v", got["TC-A"], until)
	}

	later := time.Now().AddDate(0, 0, 14).Truncate(time.Second)
	if err := s.DismissQuarantineSuggestion(ctx, "TC-A", later, "budi"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SuggestionDismissals(ctx)
	if len(got) != 1 || got["TC-A"].Before(until) {
		t.Errorf("upsert tidak memperbarui: %+v", got)
	}

	if err := s.DismissQuarantineSuggestion(ctx, "TC-B", time.Now().Add(-time.Hour), "ega"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SuggestionDismissals(ctx)
	if _, ok := got["TC-B"]; ok {
		t.Errorf("dismissal kedaluwarsa tidak boleh aktif: %+v", got)
	}
}
