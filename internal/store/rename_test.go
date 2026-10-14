package store_test

import (
	"context"
	"strings"
	"testing"

	"redline/internal/report"
	"redline/internal/store"
)

// tagged: satu test di dalam describe. msg kosong = lulus.
type tagged struct{ title, tag, msg string }

// describeReport: semua test di dalam satu describe, seperti test.describe("...", () => { ... }).
func describeReport(describe string, tests ...tagged) *report.Report {
	specs := []report.Spec{}
	for _, t := range tests {
		spec := passedTest(t.title)
		if t.msg != "" {
			spec = failedTest(t.title, t.msg, "")
		}
		if t.tag != "" {
			spec.Tags = []string{t.tag}
		}
		specs = append(specs, spec)
	}
	return &report.Report{Suites: []report.Suite{{Title: "shop.spec.ts", File: "shop.spec.ts",
		Suites: []report.Suite{{Title: describe, File: "shop.spec.ts", Specs: specs}}}}}
}

func groupOf(t *testing.T, res *store.IngestResult, title string) store.GroupChange {
	t.Helper()
	for _, list := range [][]store.GroupChange{res.New, res.Recurring, res.Regressed} {
		for _, g := range list {
			if strings.HasSuffix(g.TestKey, " › "+title) {
				return g
			}
		}
	}
	t.Fatalf("kelompok %q tidak ada: %+v", title, res)
	return store.GroupChange{}
}

func TestRenameFollowsTestCaseID(t *testing.T) {
	ctx := context.Background()
	const msg = "Expected: 201\nReceived: 422"

	t.Run("RenamedDescribeKeepsHistoryAndGroup", func(t *testing.T) {
		s := newStore(t)
		run1 := ingest(t, s, describeReport("Brand test suits",
			tagged{"get all", "TC-BRD-001", ""},
			tagged{"create", "TC-BRD-002", msg}), store.RunMeta{})
		before := groupOf(t, run1, "create")

		// describe diganti: kunci kedua test berubah, ID-nya tetap.
		run2 := ingest(t, s, describeReport("POST /brands",
			tagged{"get all", "TC-BRD-001", "Expected: 200\nReceived: 500"},
			tagged{"create", "TC-BRD-002", msg}), store.RunMeta{})

		create := groupOf(t, run2, "create")
		if create.Fingerprint != before.Fingerprint || len(run2.Recurring) != 1 || len(run2.New) != 1 {
			t.Fatalf("error yang sama setelah rename harus melanjutkan kelompok lama: %+v", run2)
		}
		g, occ := group(t, s, create.Fingerprint)
		if !strings.Contains(g.TestKey, "POST /brands › create") || g.Occurrences != 2 || len(occ) != 2 {
			t.Errorf("kelompok lama harus memakai nama baru dan punya 2 kemunculan: %+v %d", g, len(occ))
		}

		// get all: belum pernah gagal, tapi pernah lulus dengan nama lama -> ada pembanding.
		f, err := s.Facts(ctx, groupOf(t, run2, "get all").Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		if f.LastPass == nil || f.LastPass.RunID != run1.RunID || strings.Join(f.RecentStatuses, ",") != "passed,failed" {
			t.Errorf("riwayat dengan nama lama harus ikut: lastPass=%+v statuses=%v", f.LastPass, f.RecentStatuses)
		}

		// Lulus dengan nama baru: kelompok lama (dibuka sebelum rename) ikut selesai.
		run3 := ingest(t, s, describeReport("POST /brands",
			tagged{"get all", "TC-BRD-001", ""},
			tagged{"create", "TC-BRD-002", ""}), store.RunMeta{})
		if len(run3.Resolved) != 2 {
			t.Errorf("kedua kelompok harus selesai: %+v", run3.Resolved)
		}

		// Rename kedua kali: kunci lama yang pertama tetap tersambung.
		run4 := ingest(t, s, describeReport("Brands API",
			tagged{"create", "TC-BRD-002", msg}), store.RunMeta{})
		if g := groupOf(t, run4, "create"); g.Fingerprint != before.Fingerprint || len(run4.Regressed) != 1 {
			t.Errorf("rename kedua harus tetap memakai kelompok yang sama (regressed): %+v", run4)
		}
	})

	t.Run("DuplicateTagIsNotFollowed", func(t *testing.T) {
		s := newStore(t)
		ingest(t, s, describeReport("Old", tagged{"get all", "TC-1", ""}), store.RunMeta{})
		run2 := ingest(t, s, describeReport("New",
			tagged{"get all", "TC-1", msg},
			tagged{"other", "TC-1", ""}), store.RunMeta{})
		if f, _ := s.Facts(ctx, groupOf(t, run2, "get all").Fingerprint); f.LastPass != nil {
			t.Errorf("ID dobel di satu run: tidak boleh dianggap rename: %+v", f.LastPass)
		}
	})

	t.Run("TagMovedToAnotherTestIsNotFollowed", func(t *testing.T) {
		s := newStore(t)
		ingest(t, s, describeReport("Brands", tagged{"a", "TC-9", ""}), store.RunMeta{})
		// Test a masih ada (tanpa tag), dan tag-nya sekarang di test b yang baru.
		run2 := ingest(t, s, describeReport("Brands",
			tagged{"a", "", ""},
			tagged{"b", "TC-9", msg}), store.RunMeta{})
		if f, _ := s.Facts(ctx, groupOf(t, run2, "b").Fingerprint); f.LastPass != nil {
			t.Errorf("test lama masih ada: b adalah test baru, bukan rename: %+v", f.LastPass)
		}
	})

	t.Run("UntaggedRenameIsANewTest", func(t *testing.T) {
		s := newStore(t)
		ingest(t, s, describeReport("Old", tagged{"get all", "", ""}), store.RunMeta{})
		run2 := ingest(t, s, describeReport("New", tagged{"get all", "", msg}), store.RunMeta{})
		if f, _ := s.Facts(ctx, groupOf(t, run2, "get all").Fingerprint); f.LastPass != nil {
			t.Errorf("tanpa tag tidak ada yang bisa disambung: %+v", f.LastPass)
		}
	})
}
