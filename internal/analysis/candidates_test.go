package analysis

import (
	"context"
	"testing"

	"redline/internal/store"
)

// TestCandidatePromotion: candidate baru boleh menjadi Rule proposed setelah ada label/eksperimen
// dan lolos backtest. Candidate sendiri tidak pernah aktif.
func TestCandidatePromotion(t *testing.T) {
	ctx := context.Background()
	s, fps := setup(t)
	fp := fps["register user"]
	if fp == "" {
		t.Fatal("fingerprint register user tidak ada")
	}
	id, err := s.SaveRuleCandidate(ctx, store.RuleCandidate{
		Fingerprint: fp, Test: "toolshop › api.spec.ts › register user",
		Pattern: `Received: 500`, Category: "test_bug",
		Summary: "Payload test tidak lengkap.", NextStep: "Kirim payload lengkap.",
		Source: "ai", PromptVersion: PromptVersion, Confidence: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &Analyzer{Store: s}
	// Tanpa label/eksperimen, candidate tetap draft; bukan rejected.
	before, err := a.PromoteCandidates(ctx, fp)
	if err != nil || len(before.Promoted) != 0 || len(before.Rejected) != 0 {
		t.Fatalf("candidate tanpa bukti harus tetap draft: %+v %v", before, err)
	}
	if draft, err := s.ListRuleCandidates(ctx, fp, "draft"); err != nil || len(draft) != 1 {
		t.Fatalf("candidate harus masih draft: %+v %v", draft, err)
	}
	if err := s.SetLabel(ctx, fp, "test_bug", "payload test kurang", "ega"); err != nil {
		t.Fatal(err)
	}

	res, err := a.PromoteCandidates(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Promoted) != 1 || res.Promoted[0].Status != "proposed" || res.Promoted[0].Category != "test_bug" {
		t.Fatalf("candidate harus menjadi rule proposed: %+v", res)
	}
	rules, err := s.ListRules(ctx, "proposed")
	if err != nil || len(rules) != 1 || rules[0].Pattern != `Received: 500` {
		t.Fatalf("rule proposed tidak tersimpan: %+v %v", rules, err)
	}
	candidates, err := s.ListRuleCandidates(ctx, fp, "promoted")
	if err != nil || len(candidates) != 1 || candidates[0].PromotedRuleID == nil {
		t.Fatalf("candidate tidak ditandai promoted: %+v %v", candidates, err)
	}
	if candidates[0].ID != id {
		t.Errorf("candidate id berubah: %d != %d", candidates[0].ID, id)
	}
}
