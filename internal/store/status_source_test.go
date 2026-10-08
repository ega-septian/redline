package store

import (
	"context"
	"testing"

	"redline/internal/report"
)

func ingestFrom(t *testing.T, s *Store, source string, specs ...report.Spec) *IngestResult {
	t.Helper()
	r, err := s.IngestReport(context.Background(),
		&report.Report{Suites: []report.Suite{{Title: "shop.spec.ts", File: "shop.spec.ts", Specs: specs}}},
		RunMeta{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func groupStatus(t *testing.T, s *Store, title string) (status string, localOnly bool, occurrences int) {
	t.Helper()
	if err := s.pool.QueryRow(context.Background(),
		`SELECT status, local_only, occurrences FROM failure_groups WHERE title = $1`, title,
	).Scan(&status, &localOnly, &occurrences); err != nil {
		t.Fatal(err)
	}
	return
}

func sharedTitles(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	groups, err := s.ListGroups(context.Background(), []string{"open", "regressed", "resolved"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, g := range groups {
		out[g.Title] = true
	}
	return out
}

func TestIngest_OnlyCIChangesSharedStatus(t *testing.T) {
	s := openTestStore(t)
	s.StatusSources = []string{"ci"}
	broken := "expect(received).toBe(expected)\nExpected: 200\nReceived: 404"

	// CI: checkout gagal -> kelompok bersama.
	r := ingestFrom(t, s, "ci", failedTest("checkout", broken, ""))
	if !r.SharedStatus || len(r.New) != 1 {
		t.Fatalf("run CI harus mengubah status: %+v", r)
	}

	// Laptop A: checkout kebetulan lulus. Pratinjau "sudah beres", tapi status bersama tetap open.
	r = ingestFrom(t, s, "local", passedTest("checkout", ""))
	if r.SharedStatus || len(r.Resolved) != 1 {
		t.Fatalf("run lokal: pratinjau resolved, tanpa mengubah status: %+v", r)
	}
	if st, _, _ := groupStatus(t, s, "checkout"); st != "open" {
		t.Errorf("run lokal tidak boleh menutup kelompok bersama, status jadi %q", st)
	}

	// Laptop B: test baru yang belum selesai gagal -> hanya lokal, tidak masuk daftar tim.
	r = ingestFrom(t, s, "local", failedTest("wishlist (draft)", broken, ""))
	if len(r.New) != 1 {
		t.Fatalf("pratinjau tetap menunjukkan kegagalan baru: %+v", r)
	}
	if _, local, _ := groupStatus(t, s, "wishlist (draft)"); !local {
		t.Error("kegagalan baru dari run lokal harus local_only")
	}
	if titles := sharedTitles(t, s); titles["wishlist (draft)"] || !titles["checkout"] {
		t.Errorf("daftar tim hanya berisi kelompok bersama: %v", titles)
	}

	// Run lokal yang gagal lagi tidak menambah hitungan kelompok bersama.
	ingestFrom(t, s, "local", failedTest("checkout", broken, ""))
	if _, _, occ := groupStatus(t, s, "checkout"); occ != 1 {
		t.Errorf("run lokal tidak boleh menambah occurrences, jadi %d", occ)
	}

	// CI juga melihat kegagalan wishlist -> sekarang jadi kelompok bersama yang baru.
	r = ingestFrom(t, s, "ci", failedTest("wishlist (draft)", broken, ""))
	if len(r.New) != 1 || r.New[0].Occurrences != 1 {
		t.Fatalf("kelompok lokal yang terlihat di CI harus dihitung baru: %+v", r)
	}
	if st, local, occ := groupStatus(t, s, "wishlist (draft)"); st != "open" || local || occ != 1 {
		t.Errorf("wishlist harus jadi kelompok bersama open dengan 1 kemunculan: %q local=%v occ=%d", st, local, occ)
	}
	if !sharedTitles(t, s)["wishlist (draft)"] {
		t.Error("wishlist harus muncul di daftar tim setelah terlihat di CI")
	}

	// CI: checkout lulus -> baru benar-benar resolved.
	ingestFrom(t, s, "ci", passedTest("checkout", ""))
	if st, _, _ := groupStatus(t, s, "checkout"); st != "resolved" {
		t.Errorf("run CI yang lulus harus menutup kelompok, status %q", st)
	}
}

func TestIngest_AllSourcesByDefault(t *testing.T) {
	s := openTestStore(t) // StatusSources kosong = semua source
	r := ingestFrom(t, s, "local", failedTest("checkout", "Expected: 200\nReceived: 404", ""))
	if !r.SharedStatus {
		t.Fatal("tanpa StatusSources, run lokal tetap mengubah status (perilaku lama)")
	}
	if _, local, _ := groupStatus(t, s, "checkout"); local {
		t.Error("tanpa StatusSources, kelompok tidak boleh local_only")
	}
}
