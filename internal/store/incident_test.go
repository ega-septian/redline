package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"redline/internal/report"
)

// failedTest membuat satu test gagal. calls (opsional) dikirim lewat attachment fixture Redline.
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

func TestIngest_GroupsIncidents(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	login500 := `[{"method":"POST","path":"/users/login","status":500,"shape":null}]`
	var specs []report.Spec
	// 5 test gagal karena login 500, dengan assertion yang berbeda-beda.
	for i := range 5 {
		specs = append(specs, failedTest(fmt.Sprintf("checkout %d", i),
			fmt.Sprintf("expect(received).toBe(expected)\nExpected: %d\nReceived: 500", 200+i%2), login500))
	}
	// 3 test gagal karena server mati.
	for i := range 3 {
		specs = append(specs, failedTest(fmt.Sprintf("produk %d", i),
			"Error: apiRequestContext.get: connect ECONNREFUSED 127.0.0.1:8091", ""))
	}
	// 1 kegagalan yang berdiri sendiri.
	specs = append(specs, failedTest("register", "expect(received).toBe(expected)\nExpected: 201\nReceived: 422", ""))

	rep := &report.Report{Suites: []report.Suite{{Title: "shop.spec.ts", File: "shop.spec.ts", Specs: specs}}}
	r, err := s.IngestReport(ctx, rep, RunMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Failed != 9 || len(r.New) != 9 {
		t.Fatalf("status per test tetap dilacak terpisah: %+v", r)
	}
	if len(r.Incidents) != 3 {
		t.Fatalf("mau 3 insiden, dapat %d: %+v", len(r.Incidents), r.Incidents)
	}
	want := []struct {
		kind, label string
		tests       int
	}{
		{"http", "POST /users/login → 500", 5},
		{"connection", "Tidak bisa terhubung: ECONNREFUSED localhost:8091", 3},
		{"error", "expect(received).toBe(expected)", 1},
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

	// Tersimpan di database untuk query lintas run.
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM failure_groups WHERE cause = 'POST /users/login → 500'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("mau 5 kelompok dengan penyebab login, dapat %d", n)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT cause_id) FROM test_results WHERE cause_id <> ''`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("mau 3 cause_id di test_results, dapat %d", n)
	}
}

func TestIngest_IncidentRepresentativePrefersRegressed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	down := "connect ECONNREFUSED 127.0.0.1:8091"
	run := func(specs ...report.Spec) *IngestResult {
		t.Helper()
		r, err := s.IngestReport(ctx, &report.Report{Suites: []report.Suite{{Title: "a.spec.ts", File: "a.spec.ts", Specs: specs}}}, RunMeta{})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	passed := report.Spec{Title: "lama", File: "shop.spec.ts", Tests: []report.Test{{
		ProjectName: "api", Status: "expected", Results: []report.Result{{Status: "passed"}},
	}}}

	run(failedTest("lama", down, ""))                                    // lama: open
	run(passed)                                                          // lama: resolved
	r := run(failedTest("baru", down, ""), failedTest("lama", down, "")) // baru: new, lama: regressed

	if len(r.Incidents) != 1 || r.Incidents[0].Tests != 2 {
		t.Fatalf("mau 1 insiden berisi 2 test: %+v", r.Incidents)
	}
	if len(r.Regressed) != 1 || r.Incidents[0].Representative != r.Regressed[0].Fingerprint {
		t.Errorf("representative harus kelompok yang regressed: %+v", r.Incidents[0])
	}
}
