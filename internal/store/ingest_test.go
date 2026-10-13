// Test store lewat API publiknya saja. Dulu dipakai untuk membuktikan versi Neo4j setara dengan versi Postgres
// (v0.1.0); sekarang menjadi test utama store. Butuh DozerDB: REDLINE_TEST_NEO4J_URI=bolt://localhost:7688.
package store_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"redline/internal/report"
	"redline/internal/store"
	"redline/internal/triage"
)

// TestIngest: menyimpan report, kelompok kegagalan, insiden, status, dan test case ID.
func TestIngest(t *testing.T) {
	t.Run("Lifecycle", func(t *testing.T) { testLifecycle(t, newStore(t)) })
	t.Run("RedactsBeforeSaving", func(t *testing.T) { testRedacts(t, newStore(t)) })
	t.Run("GroupsIncidents", func(t *testing.T) { testIncidents(t, newStore(t)) })
	t.Run("RepresentativePrefersRegressed", func(t *testing.T) { testRepresentative(t, newStore(t)) })
	t.Run("OnlyCIChangesSharedStatus", func(t *testing.T) { testOnlyCI(t, newStore(t, "ci")) })
	t.Run("AllSourcesByDefault", func(t *testing.T) { testAllSources(t, newStore(t)) })
	t.Run("TestCaseID", func(t *testing.T) { testCaseID(t, newStore(t)) })
}

func loadReport(t *testing.T, name string) *report.Report {
	t.Helper()
	f, err := os.Open("../report/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rep, err := report.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// failedTest membuat satu test gagal di shop.spec.ts (project api). calls dikirim lewat attachment fixture.
func failedTest(title, msg, calls string) report.Spec {
	res := report.Result{Status: "failed", Error: &report.ErrorEntry{Message: msg}}
	if calls != "" {
		res.Attachments = []report.Attachment{{
			Name: "redline-http", ContentType: "application/json",
			Body: base64.StdEncoding.EncodeToString([]byte(calls)),
		}}
	}
	return report.Spec{Title: title, File: "shop.spec.ts", Tests: []report.Test{{
		ProjectName: "api", Status: "unexpected", Results: []report.Result{res},
	}}}
}

func passedTest(title string) report.Spec {
	return report.Spec{Title: title, File: "shop.spec.ts", Tests: []report.Test{{
		ProjectName: "api", Status: "expected", Results: []report.Result{{Status: "passed"}},
	}}}
}

func shopReport(specs ...report.Spec) *report.Report {
	return &report.Report{Suites: []report.Suite{{Title: "shop.spec.ts", File: "shop.spec.ts", Specs: specs}}}
}

// fingerprint menghitung id kelompok untuk test di shopReport, sama seperti saat ingest.
func fingerprint(title, msg string) string {
	return triage.Fingerprint("api › shop.spec.ts › "+title, triage.Normalize(triage.Clean(msg)))
}

func ingest(t *testing.T, b *store.Store, rep *report.Report, meta store.RunMeta) *store.IngestResult {
	t.Helper()
	r, err := b.IngestReport(context.Background(), rep, meta)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func group(t *testing.T, b *store.Store, fp string) (store.Group, []store.Occurrence) {
	t.Helper()
	g, occ, err := b.GetGroup(context.Background(), fp)
	if err != nil {
		t.Fatal(err)
	}
	return g, occ
}

func testLifecycle(t *testing.T, b *store.Store) {
	ctx := context.Background()
	bug, fixed := loadReport(t, "run1-bug.json"), loadReport(t, "run2-fixed.json")

	r1 := ingest(t, b, bug, store.RunMeta{Branch: "main", CommitSHA: "aaa111", AppVersion: "sprint5-with-bugs"})
	if r1.Total != 6 || r1.Failed != 3 || r1.Flaky != 1 || r1.Skipped != 1 || r1.Passed != 1 {
		t.Fatalf("hitungan run 1 salah: %+v", r1)
	}
	if len(r1.New) != 3 || len(r1.FlakyTests) != 1 || !r1.SharedStatus {
		t.Fatalf("mau 3 kelompok baru dan 1 flaky: %+v", r1)
	}

	r2 := ingest(t, b, bug, store.RunMeta{})
	if len(r2.New) != 0 || len(r2.Recurring) != 3 {
		t.Fatalf("error yang sama harus masuk kelompok yang sama: %+v", r2)
	}
	for _, c := range r2.Recurring {
		if c.Occurrences != 2 {
			t.Errorf("occurrences harus 2: %+v", c)
		}
	}

	r3 := ingest(t, b, fixed, store.RunMeta{Source: "ci"})
	if len(r3.Resolved) != 3 || r3.Passed != 4 {
		t.Fatalf("mau 3 resolved: %+v", r3)
	}
	if open, err := b.ListGroups(ctx, nil, 0); err != nil || len(open) != 0 {
		t.Fatalf("tidak boleh ada kelompok terbuka setelah fix: %+v %v", open, err)
	}
	all, err := b.ListGroups(ctx, []string{"resolved"}, 0)
	if err != nil || len(all) != 3 || all[0].ResolvedRun == nil || *all[0].ResolvedRun != r3.RunID || all[0].ResolvedAt == nil {
		t.Fatalf("kelompok resolved salah: %+v %v", all, err)
	}

	r4 := ingest(t, b, bug, store.RunMeta{})
	if len(r4.Regressed) != 3 || len(r4.New) != 0 {
		t.Fatalf("mau 3 regressed: %+v", r4)
	}
	groups, err := b.ListGroups(ctx, []string{"regressed"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 || groups[0].Regressions != 1 || groups[0].Occurrences != 3 || groups[0].ResolvedRun != nil {
		t.Fatalf("kelompok regressed salah: %+v", groups)
	}
	if groups[0].SampleError != "" || groups[0].Summary == "" || groups[0].FirstSeenRun != r1.RunID ||
		groups[0].LastSeenRun != r4.RunID || groups[0].FirstSeenAt.IsZero() || groups[0].LastSeenAt.Before(groups[0].FirstSeenAt) {
		t.Errorf("daftar: ringkasan tanpa contoh error, dan run pertama/terakhir benar: %+v", groups[0])
	}

	g, occ := group(t, b, groups[0].Fingerprint)
	if len(occ) != 3 || occ[0].RunID != r4.RunID || occ[len(occ)-1].CommitSHA != "aaa111" || occ[0].TracePath == "" {
		t.Errorf("riwayat salah: %+v", occ)
	}
	if !strings.HasPrefix(occ[0].TracePath, "test-results/") {
		t.Errorf("trace_path harus relatif terhadap project: %q", occ[0].TracePath)
	}
	if g.SampleError == "" || g.Summary == "" || g.Project == "" || g.TestKey != g.Project+" › "+g.File+" › "+g.Title {
		t.Errorf("detail kelompok salah: %+v", g)
	}
	if _, _, err := b.GetGroup(ctx, "tidakada"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("mau ErrNotFound, dapat %v", err)
	}

	runs, err := b.ListRuns(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 || runs[0].ID != r4.RunID || runs[1].Source != "ci" || runs[0].PlaywrightVersion == "" ||
		runs[3].Branch != "main" || runs[3].Total != 6 || runs[3].Failed != 3 || runs[0].CreatedAt.IsZero() {
		t.Errorf("daftar run salah: %+v", runs)
	}
	if runs[3].ID >= runs[0].ID || runs[0].Source != "local" {
		t.Errorf("run harus urut terbaru dulu, source default local: %+v", runs)
	}
}

func testRedacts(t *testing.T, b *store.Store) {
	msg := "Error: 401\nAuthorization: Bearer super-secret-token"
	rep := &report.Report{Suites: []report.Suite{{
		Title: "auth.spec.ts", File: "auth.spec.ts",
		Specs: []report.Spec{{Title: "login", File: "auth.spec.ts", Tests: []report.Test{{
			ProjectName: "api", Status: "unexpected",
			Results: []report.Result{{Status: "failed", Error: &report.ErrorEntry{
				Message: msg,
				Snippet: `request.post("/login", { data: { password: "rahasia" } })`,
			}}},
		}}}},
	}}}
	r := ingest(t, b, rep, store.RunMeta{})
	g, occ := group(t, b, r.New[0].Fingerprint)
	for _, v := range []string{g.SampleError, occ[0].ErrorSnippet, r.New[0].Error} {
		if strings.Contains(v, "super-secret-token") || strings.Contains(v, "rahasia") {
			t.Errorf("rahasia bocor ke database: %q", v)
		}
	}
}

func testIncidents(t *testing.T, b *store.Store) {
	ctx := context.Background()
	login500 := `[{"method":"POST","path":"/users/login","status":500,"shape":null}]`
	var specs []report.Spec
	for i := range 5 {
		specs = append(specs, failedTest(fmt.Sprintf("checkout %d", i),
			fmt.Sprintf("expect(received).toBe(expected)\nExpected: %d\nReceived: 500", 200+i%2), login500))
	}
	for i := range 3 {
		specs = append(specs, failedTest(fmt.Sprintf("produk %d", i),
			"Error: apiRequestContext.get: connect ECONNREFUSED 127.0.0.1:8091", ""))
	}
	specs = append(specs, failedTest("register", "expect(received).toBe(expected)\nExpected: 201\nReceived: 422", ""))

	r := ingest(t, b, shopReport(specs...), store.RunMeta{})
	if r.Failed != 9 || len(r.New) != 9 {
		t.Fatalf("status per test tetap dilacak terpisah: %+v", r)
	}
	want := []struct {
		kind, label string
		tests       int
	}{
		{"http", "POST /users/login → 500", 5},
		{"connection", "Tidak bisa terhubung: ECONNREFUSED localhost:8091", 3},
		{"error", "expect(received).toBe(expected)", 1},
	}
	if len(r.Incidents) != len(want) {
		t.Fatalf("mau %d insiden, dapat %+v", len(want), r.Incidents)
	}
	for i, w := range want {
		inc := r.Incidents[i]
		if inc.Kind != w.kind || inc.Label != w.label || inc.Tests != w.tests || len(inc.Fingerprints) != w.tests || inc.Representative == "" {
			t.Errorf("insiden %d: dapat %+v, mau %+v", i, inc, w)
		}
	}
	for _, c := range r.New {
		if c.Incident == "" {
			t.Errorf("tiap kelompok harus tahu insidennya: %+v", c)
		}
	}
	groups, err := b.ListGroups(ctx, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	causes := map[string]int{}
	for _, g := range groups {
		causes[g.Cause]++
	}
	if causes["POST /users/login → 500"] != 5 || len(causes) != 3 {
		t.Errorf("penyebab harus tersimpan per kelompok: %v", causes)
	}
}

func testRepresentative(t *testing.T, b *store.Store) {
	down := "connect ECONNREFUSED 127.0.0.1:8091"
	ingest(t, b, shopReport(failedTest("lama", down, "")), store.RunMeta{})
	ingest(t, b, shopReport(passedTest("lama")), store.RunMeta{})
	r := ingest(t, b, shopReport(failedTest("baru", down, ""), failedTest("lama", down, "")), store.RunMeta{})
	if len(r.Incidents) != 1 || r.Incidents[0].Tests != 2 {
		t.Fatalf("mau 1 insiden berisi 2 test: %+v", r.Incidents)
	}
	if len(r.Regressed) != 1 || r.Incidents[0].Representative != r.Regressed[0].Fingerprint {
		t.Errorf("representative harus kelompok yang regressed: %+v", r.Incidents[0])
	}
}

func sharedTitles(t *testing.T, b *store.Store) map[string]bool {
	t.Helper()
	groups, err := b.ListGroups(context.Background(), []string{"open", "regressed", "resolved"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, g := range groups {
		out[g.Title] = true
	}
	return out
}

func testOnlyCI(t *testing.T, b *store.Store) {
	broken := "expect(received).toBe(expected)\nExpected: 200\nReceived: 404"
	checkout, wishlist := fingerprint("checkout", broken), fingerprint("wishlist (draft)", broken)

	r := ingest(t, b, shopReport(failedTest("checkout", broken, "")), store.RunMeta{Source: "ci"})
	if !r.SharedStatus || len(r.New) != 1 {
		t.Fatalf("run CI harus mengubah status: %+v", r)
	}

	r = ingest(t, b, shopReport(passedTest("checkout")), store.RunMeta{Source: "local"})
	if r.SharedStatus || len(r.Resolved) != 1 {
		t.Fatalf("run lokal: pratinjau resolved, tanpa mengubah status: %+v", r)
	}
	if g, _ := group(t, b, checkout); g.Status != "open" {
		t.Errorf("run lokal tidak boleh menutup kelompok bersama, status %q", g.Status)
	}

	r = ingest(t, b, shopReport(failedTest("wishlist (draft)", broken, "")), store.RunMeta{Source: "local"})
	if len(r.New) != 1 {
		t.Fatalf("pratinjau tetap menunjukkan kegagalan baru: %+v", r)
	}
	if g, _ := group(t, b, wishlist); !g.LocalOnly {
		t.Error("kegagalan baru dari run lokal harus local_only")
	}
	if titles := sharedTitles(t, b); titles["wishlist (draft)"] || !titles["checkout"] {
		t.Errorf("daftar tim hanya berisi kelompok bersama: %v", titles)
	}

	ingest(t, b, shopReport(failedTest("checkout", broken, "")), store.RunMeta{Source: "local"})
	if g, _ := group(t, b, checkout); g.Occurrences != 1 {
		t.Errorf("run lokal tidak boleh menambah occurrences, jadi %d", g.Occurrences)
	}

	r = ingest(t, b, shopReport(failedTest("wishlist (draft)", broken, "")), store.RunMeta{Source: "ci"})
	if len(r.New) != 1 || r.New[0].Occurrences != 1 {
		t.Fatalf("kelompok lokal yang terlihat di CI harus dihitung baru: %+v", r)
	}
	if g, _ := group(t, b, wishlist); g.Status != "open" || g.LocalOnly || g.Occurrences != 1 {
		t.Errorf("wishlist harus jadi kelompok bersama open dengan 1 kemunculan: %+v", g)
	}
	if !sharedTitles(t, b)["wishlist (draft)"] {
		t.Error("wishlist harus muncul di daftar tim setelah terlihat di CI")
	}

	ingest(t, b, shopReport(passedTest("checkout")), store.RunMeta{Source: "ci"})
	if g, _ := group(t, b, checkout); g.Status != "resolved" {
		t.Errorf("run CI yang lulus harus menutup kelompok, status %q", g.Status)
	}
}

func testAllSources(t *testing.T, b *store.Store) {
	msg := "Expected: 200\nReceived: 404"
	r := ingest(t, b, shopReport(failedTest("checkout", msg, "")), store.RunMeta{Source: "local"})
	if !r.SharedStatus {
		t.Fatal("tanpa StatusSources, run lokal tetap mengubah status")
	}
	if g, _ := group(t, b, fingerprint("checkout", msg)); g.LocalOnly {
		t.Error("tanpa StatusSources, kelompok tidak boleh local_only")
	}
}

func tagSpecs(suites []report.Suite, tag func(title string) []string) {
	for i := range suites {
		for j := range suites[i].Specs {
			suites[i].Specs[j].Tags = tag(suites[i].Specs[j].Title)
		}
		tagSpecs(suites[i].Suites, tag)
	}
}

func testCaseID(t *testing.T, b *store.Store) {
	rep := loadReport(t, "run1-bug.json")
	n := 0
	tagSpecs(rep.Suites, func(string) []string { n++; return []string{"smoke", fmt.Sprintf("TC-BENCH-%03d", n)} })
	r := ingest(t, b, rep, store.RunMeta{})
	if len(r.New) == 0 || !strings.HasPrefix(r.New[0].TestCaseID, "TC-BENCH-") {
		t.Fatalf("hasil ingest harus membawa test_case_id: %+v", r.New)
	}
	g, _ := group(t, b, r.New[0].Fingerprint)
	if g.TestCaseID != r.New[0].TestCaseID {
		t.Fatalf("kelompok harus menyimpan test_case_id: %+v", g)
	}

	rep2 := loadReport(t, "run1-bug.json")
	tagSpecs(rep2.Suites, func(title string) []string { return []string{"TC-NEW-" + title} })
	ingest(t, b, rep2, store.RunMeta{})
	g2, _ := group(t, b, r.New[0].Fingerprint)
	if g2.TestCaseID == g.TestCaseID || !strings.HasPrefix(g2.TestCaseID, "TC-NEW-") {
		t.Errorf("tag baru harus dipakai tanpa memutus riwayat: lama %q, baru %q", g.TestCaseID, g2.TestCaseID)
	}
}
