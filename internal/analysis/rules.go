// Package analysis menentukan penyebab kegagalan: aturan deterministik dulu,
// AI hanya kalau aturan tidak cukup. Hasilnya di-cache per (fingerprint, versi prompt).
package analysis

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"redline/internal/store"
	"redline/internal/triage"
)

var (
	connErr  = regexp.MustCompile(`(?i)\b(ECONNREFUSED|ENOTFOUND|EAI_AGAIN|ECONNRESET|ETIMEDOUT|getaddrinfo|socket hang up|net::ERR_[A-Z_]+)\b[^\n]*`)
	expected = regexp.MustCompile(`Expected: (2\d\d)\b`)
	received = regexp.MustCompile(`Received: (5\d\d)\b`)
	// Nilai yang diperiksa ternyata Promise: hampir selalu karena lupa await (misalnya response.json()).
	promise = regexp.MustCompile(`(?i)[^\n]*(received:?\s+Promise\b|\[object Promise\])[^\n]*`)
	// Nilai yang diperiksa ternyata objek response Playwright: lupa membaca body (.json() / .text()).
	apiResponse = regexp.MustCompile(`(?i)[^\n]*received:?\s+APIResponse\b[^\n]*`)
)

// ApplyRules mengembalikan hasil kalau penyebabnya jelas tanpa AI, atau nil. Hasil berkeyakinan di bawah
// high (matriks "kode test changed") hanya dipakai kalau AI mati atau gagal; lihat Analyzer.decide.
// Aturan sengaja sedikit dan ketat: lebih baik diserahkan ke AI daripada salah yakin.
// Urutan: aturan bawaan yang spesifik, matriks "test tetap + API changed" (bukti kuat bug backend), kontrak,
// pengetahuan graph (KAG), aturan yang dipelajari, lalu sisa matriks perubahan. Aturan yang dipelajari hanya melihat teks error,
// jadi tidak boleh mengalahkan bukti bahwa API-lah yang changed.
func ApplyRules(f *store.Facts, learned ...store.LearnedRule) *store.Analysis {
	if res := builtinRules(f.ErrorMessage); res != nil {
		return res
	}
	matrix := matrixRules(f)
	if matrix != nil && matrix.Confidence == "high" {
		return matrix
	}
	if res := contractRule(f); res != nil {
		return res
	}
	if res := graphRules(f); res != nil {
		return res
	}
	if res := learnedRules(f.ErrorMessage, learned); res != nil {
		return res
	}
	return matrix
}

// contractRule: response yang melanggar kontrak saat test gagal (status tidak terdokumentasi, field hilang,
// tipe berbeda) adalah penyimpangan API, selama kode test tidak sedang changed.
func contractRule(f *store.Facts) *store.Analysis {
	if len(f.ContractViolations) == 0 || f.TestChanged == "ya" {
		return nil
	}
	return &store.Analysis{
		Source:     "rule",
		Category:   "backend_bug",
		Confidence: "high",
		Summary: fmt.Sprintf("API response does not match the OpenAPI contract in effect when the test failed: %s.",
			f.ContractViolations[0]),
		Evidence: first(f.ContractViolations, 3),
		NextStep: "Report it to the backend team with the relevant contract section. If the contract is outdated, ask for the documentation to be updated.",
	}
}

// builtinRules hanya melihat pesan error.
func builtinRules(msg string) *store.Analysis {

	if m := connErr.FindString(msg); m != "" {
		return &store.Analysis{
			Source:     "rule",
			Category:   "environment",
			Confidence: "high",
			Summary:    "The target could not be reached, so the test never exercised the API or application.",
			Evidence:   []string{m},
			NextStep:   "Make sure the target service is running (for example `docker ps`) and that baseURL in playwright.config.ts is correct, then rerun.",
		}
	}

	if e, r := expected.FindStringSubmatch(msg), received.FindStringSubmatch(msg); e != nil && r != nil {
		return &store.Analysis{
			Source:     "rule",
			Category:   "backend_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("The API returned %s while the test expected %s. A 5xx means the server failed to process the request. "+
				"Even if test data triggered it, the server should return 4xx with a validation message, not 5xx.", r[1], e[1]),
			Evidence: []string{e[0], r[0]},
			NextStep: "Check the backend logs for this run (for example `docker logs <container-api>`) to find the exception, then report it to the backend team with the request.",
		}
	}

	if m := promise.FindString(msg); m != "" {
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: "The test checks a Promise instead of its result. An `await` is almost certainly missing, " +
				"for example `response.json()` without `await`, or a `test.step` that is not awaited.",
			Evidence: []string{strings.TrimSpace(m)},
			NextStep: "Add `await` to the async call whose value is checked (for example: `parse(await response.json())`). " +
				"Run `npm run lint`: the playwright/missing-playwright-await rule catches a `test.step` or `expect` that is missing an await.",
		}
	}

	if m := apiResponse.FindString(msg); m != "" {
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: "The test checks the Playwright response object (APIResponse), not its body. " +
				"It most likely forgot to read the body with `.json()` or `.text()`.",
			Evidence: []string{strings.TrimSpace(m)},
			NextStep: "Read the body before validating it, for example `Schema.parse(await response.json())`.",
		}
	}

	return nil
}

// learnedRules mencoba aturan yang sudah disetujui, berurutan. Pola yang tidak valid dilewati.
func learnedRules(msg string, rules []store.LearnedRule) *store.Analysis {
	for _, r := range rules {
		re := compileCached(r.Pattern)
		if re == nil {
			continue
		}
		if m := re.FindString(msg); m != "" {
			return &store.Analysis{
				Source:     "rule",
				Category:   r.Category,
				Confidence: "high",
				Summary:    r.Summary,
				Evidence: []string{strings.TrimSpace(firstLineOf(m)),
					fmt.Sprintf("Rule #%d, learned from %d confirmed cases", r.ID, len(r.LearnedFrom))},
				NextStep: r.NextStep,
			}
		}
	}
	return nil
}

// matrixRules: hanya kalau ada pembanding (run lulus terakhir) dan kedua sinyal diketahui.
func matrixRules(f *store.Facts) *store.Analysis {
	if f.LastPass == nil {
		return nil
	}
	switch {
	case f.TestChanged == "tidak" && f.Response.Verdict == triage.ResponseChanged && f.ContractChanged == "ya" &&
		len(f.ContractViolations) == 0:
		// Dimensi ketiga: kontrak ikut changed dan response mengikuti kontrak baru, jadi perubahannya disengaja.
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("The API contract for this endpoint changed since the last passing run (#%d), and the response now "+
				"matches the new contract: %s. The change is intentional; the test has not caught up with the new contract.",
				f.LastPass.RunID, f.Response.Lines[0]),
			Evidence: append([]string{"Test code changed: no", "API contract changed: yes"}, first(f.Response.Lines, 2)...),
			NextStep: "Update the test and schema to the new contract. If the contract change was not intentional, report it to the backend team.",
		}
	case f.TestChanged == "tidak" && f.Response.Verdict == triage.ResponseChanged:
		return &store.Analysis{
			Source:     "rule",
			Category:   "backend_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("The test code is identical to the last passing run (#%d), but the API response shape changed: %s. "+
				"The change came from the API side.", f.LastPass.RunID, f.Response.Lines[0]),
			Evidence: append([]string{"Test code changed: no"}, first(f.Response.Lines, 3)...),
			NextStep: "Confirm with the backend team whether this contract change is intentional. If it is, update the test and schema; if not, report it as a bug.",
		}
	case f.TestChanged == "ya" && (f.Response.Verdict == triage.ResponseSame || f.Response.Verdict == triage.ResponseAddedOnly):
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "medium",
			Summary: fmt.Sprintf("The API response shape is the same as the last passing run (#%d), while the test code changed. "+
				"The change in the test is the most likely cause of the failure.", f.LastPass.RunID),
			Evidence: []string{"Test code changed: yes", "API response: " + responseVerdictLabel(f.Response.Verdict)},
			NextStep: "Compare the latest test change (git diff). If the new assertion was intentionally added to catch an old bug, change this group label to backend_bug.",
		}
	}
	return nil
}

// graphRules: aturan dari pengetahuan graph (KAG). Dijalankan setelah matriks yang yakin dan kontrak, jadi
// tidak pernah mengalahkan bukti bahwa response atau kontrak API changed. Hanya dipakai kalau ada pembanding
// (run lulus terakhir yang juga punya graph import), supaya "file changed" bisa dipercaya.
func graphRules(f *store.Facts) *store.Analysis {
	k := f.Knowledge
	if k == nil || f.LastPass == nil || !k.Compared {
		return nil
	}
	ownSpec := specOf(f.Group.TestKey)

	// 1. Endpoint yang sama gagal untuk test di spec lain yang TIDAK memakai kode yang changed, dan tidak ada
	// yang lulus: masalahnya di endpoint, bukan di kode test.
	for _, e := range k.Endpoints {
		others := otherSpecs(e.Without.Failed, ownSpec)
		if len(others) == 0 || len(e.Without.Passed) > 0 {
			continue
		}
		return &store.Analysis{
			Source:     "rule",
			Category:   "backend_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("%s also failed for %d other tests in different specs that do not use the changed code, "+
				"and no test passed on that endpoint. The problem is in the endpoint, not in this test code.",
				e.Key, len(e.Without.Failed)),
			Evidence: append([]string{e.Key + ": other tests without the changed code failed"}, first(titles(others), 3)...),
			NextStep: "Check " + e.Key + " (response and data) with the backend team; every test that calls it failed too.",
		}
	}

	changed := []store.FileFact{}
	for _, file := range k.Files {
		if file.Changed {
			changed = append(changed, file)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	paths := []string{}
	for _, c := range changed {
		paths = append(paths, describeFile(c))
	}

	// 2. Endpoint yang belum pernah lulus, dan hanya dipanggil lewat kode yang changed: kode test yang baru
	// memanggil endpoint itu (misalnya salah ketik path).
	for _, e := range k.Endpoints {
		if e.SeenPassing || len(e.Without.Passed)+len(e.Without.Failed) > 0 {
			continue
		}
		return &store.Analysis{
			Source:     "rule",
			Category:   "test_bug",
			Confidence: "high",
			Summary: fmt.Sprintf("The test calls %s, which was never called by a passing test, and is only called through "+
				"code that changed since the last passing run (#%d): %s. The test code likely has a wrong path or method.",
				e.Key, f.LastPass.RunID, strings.Join(paths, ", ")),
			Evidence: append([]string{e.Key + ": never called by a passing test"}, paths...),
			NextStep: "Compare the path in " + changed[0].Path + " with the API contract or the path before the change.",
		}
	}

	// 3. Kode bersama changed, bentuk response tetap, dan test lain yang memanggil endpoint yang sama tanpa
	// kode itu lulus: endpoint-nya sehat, yang changed kode test.
	if f.Response.Verdict != triage.ResponseSame && f.Response.Verdict != triage.ResponseAddedOnly {
		return nil
	}
	var healthy *store.EndpointFact
	for i, e := range k.Endpoints {
		if len(e.Without.Failed) > 0 {
			return nil
		}
		if len(e.Without.Passed) > 0 && healthy == nil {
			healthy = &k.Endpoints[i]
		}
	}
	if healthy == nil {
		return nil
	}
	// Lebih yakin kalau test lain yang memakai file yang changed ikut gagal.
	confidence := "medium"
	for _, c := range changed {
		if len(c.Others.Failed) > 0 {
			confidence = "high"
		}
	}
	return &store.Analysis{
		Source:     "rule",
		Category:   "test_bug",
		Confidence: confidence,
		Summary: fmt.Sprintf("The code used by this test changed since the last passing run (#%d): %s. The API response shape "+
			"stayed the same, and %s passed for other tests that do not use that code. The cause is in the changed code.",
			f.LastPass.RunID, strings.Join(paths, ", "), healthy.Key),
		Evidence: append(append([]string{}, paths...),
			fmt.Sprintf("%s: passed for %s", healthy.Key, strings.Join(first(titles(healthy.Without.Passed), 2), ", ")),
			"API response: "+responseVerdictLabel(f.Response.Verdict)),
		NextStep: "Check the change in " + changed[0].Path + " (git diff); other tests use that file too.",
	}
}

// describeFile: "schemas/brand.ts (lewat import lain)".
func describeFile(f store.FileFact) string {
	if f.Direct {
		return f.Path + " changed"
	}
	return f.Path + " changed (through another import)"
}

// specOf mengambil file spec dari kunci test "project › file › judul".
func specOf(testKey string) string {
	parts := strings.SplitN(testKey, " › ", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func otherSpecs(tests []string, spec string) []string {
	out := []string{}
	for _, t := range tests {
		if specOf(t) != spec {
			out = append(out, t)
		}
	}
	return out
}

// titles: kunci test -> "file › judul" (tanpa project), supaya singkat.
func titles(tests []string) []string {
	out := make([]string, len(tests))
	for i, t := range tests {
		if _, rest, ok := strings.Cut(t, " › "); ok {
			t = rest
		}
		out[i] = t
	}
	return out
}

var compiled sync.Map // pola -> *regexp.Regexp (nil kalau tidak valid)

func compileCached(pattern string) *regexp.Regexp {
	if v, ok := compiled.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = nil
	}
	compiled.Store(pattern, re)
	return re
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func first(lines []string, n int) []string {
	if len(lines) < n {
		return lines
	}
	return lines[:n]
}

// yesNo converts the internal yes/no/unknown states to English output.
func yesNo(value string) string {
	switch value {
	case "ya":
		return "yes"
	case "tidak":
		return "no"
	default:
		return "unknown"
	}
}

// responseVerdictLabel keeps the stored verdict values stable while showing English text.
func responseVerdictLabel(value string) string {
	switch value {
	case triage.ResponseChanged:
		return "changed"
	case triage.ResponseAddedOnly:
		return "added fields only"
	case triage.ResponseSame:
		return "unchanged"
	default:
		return "unknown"
	}
}
