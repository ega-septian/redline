package store_test

import (
	"context"
	"testing"

	"redline/internal/store"
)

func TestRuleCandidates(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	run := ingest(t, s, describeReport("Brands",
		tagged{"register", "TC-USR-001", "Expected: 201\nReceived: 500"}), store.RunMeta{})
	fp := groupOf(t, run, "register").Fingerprint
	candidate := store.RuleCandidate{
		Fingerprint: fp, Test: "toolshop › api.spec.ts › register",
		Pattern: `Expected: 201\s*Received: 500`, Category: "test_bug",
		Summary: "Payload test tidak lengkap.", NextStep: "Kirim payload lengkap.",
		Source: "ai", Model: "test-model", PromptVersion: "v10", Confidence: "high",
	}

	id, err := s.SaveRuleCandidate(ctx, candidate)
	if err != nil || id == 0 {
		t.Fatalf("simpan candidate: id=%d err=%v", id, err)
	}
	// Pola yang sama untuk kelompok yang sama tidak dibuat dua kali.
	again, err := s.SaveRuleCandidate(ctx, candidate)
	if err != nil || again != id {
		t.Errorf("candidate duplikat harus mengembalikan id yang sama: %d vs %d (%v)", again, id, err)
	}

	list, err := s.ListRuleCandidates(ctx, "", "draft")
	if err != nil || len(list) != 1 || list[0].Pattern != candidate.Pattern || list[0].Test != candidate.Test {
		t.Fatalf("daftar candidate draft salah: %+v %v", list, err)
	}

	if err := s.MarkRuleCandidate(ctx, id, "promoted", "", 7, 3, 1); err != nil {
		t.Fatal(err)
	}
	promoted, err := s.ListRuleCandidates(ctx, fp, "promoted")
	if err != nil || len(promoted) != 1 || promoted[0].PromotedRuleID == nil || *promoted[0].PromotedRuleID != 7 ||
		promoted[0].Support != 3 || promoted[0].AlsoMatches != 1 || promoted[0].ProvenAt == nil || promoted[0].DecidedAt == nil {
		t.Fatalf("candidate promoted salah: %+v %v", promoted, err)
	}
	if err := s.MarkRuleCandidate(ctx, 9999, "rejected", "x", 0, 0, 0); err != store.ErrNotFound {
		t.Errorf("candidate tidak ada: mau ErrNotFound, dapat %v", err)
	}
}
