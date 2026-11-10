package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"redline/internal/store"
)

// PromptVersion dinaikkan setiap kali prompt atau format fakta berubah,
// supaya cache lama tidak dipakai untuk prompt baru.
const PromptVersion = "v11"

const toolName = "report_verdict"

const systemPrompt = `You help an SDET determine the cause of an automated test failure.

Choose one category:
- backend_bug: API/application behavior is wrong or changed (missing field, wrong value, wrong status code, contract change).
- test_bug: test code, test data, or an assertion is wrong or outdated.
- environment: service down, network, configuration, or seed data mismatch.
- flaky: inconsistent result due to timing, ordering, or test interdependence.
- unknown: not enough facts to decide.

Rules:
1. Use ONLY the facts in the FACTS section. Do not invent endpoints, fields, or response content that is not written.
2. Every "evidence" item must be an EXACT quote (copy as-is) from FACTS, at most 4 items.
3. Compare with the last passing run. "Test code changed" comes from the test code; "API response" from the response
   shape (fields, types, status). Same test code + changed response leans backend_bug; changed test code + same
   response leans test_bug. Same response shape does not mean same values: changed values (for example status
   "PAID" to "PENDING") are visible in the error message, not in the shape. If a signal is "unknown", do not assume.
4. Extra response fields are not a breaking change. Missing fields or changed types are contract changes.
5. When unsure, choose the MOST LIKELY category with low confidence and state the uncertainty in the summary.
   Choose unknown only when the facts truly do not point to any category. Being confidently wrong (high confidence
   but wrong) is worse than low confidence.
6. Write summary and next_step in English, concise (summary at most 3 sentences).
7. Code map: if other tests that use the same endpoint or file PASS, the cause leans toward this test. If they all
   FAIL, the cause leans toward shared code: shared schema/helper, or backend.
8. Read the TEST CODE before blaming the API. Compare what the test sends and expects with the recorded response:
   path, method, payload, field names, schema types, expected status and message. If the test sends incomplete data,
   calls the wrong path, or expects something inconsistent with a reasonable API response, that is test_bug. If the
   test code is reasonable and the response deviates (missing field, wrong type, wrong status), that is backend_bug.
9. SIMILAR CASES are only historical references, not evidence. Do not quote them as evidence. Use them only if the
   facts of this case genuinely match; a similar error may have a different cause.
10. If an API CONTRACT is present, it is the primary reference. Compare what the test expects/sends and the recorded
    response with the contract:
    - test expects or sends something different from the contract -> deviates "test", category test_bug
    - response differs from the contract (status, missing field, wrong type) -> deviates "api", category backend_bug
    - contract does not cover the disputed detail (for example a message text) -> decide from other facts
    Without a contract and without a passing history, "test expects X, API returns Y" is hard to decide: use low confidence.
    If the contract changed since the last passing run and the response follows the new contract, the change is
    intentional: outdated test (test_bug). If the contract is unchanged but the response changed, that is an API
    regression (backend_bug).
11. GRAPH KNOWLEDGE follows imports to any depth, so it can find a changed file even when "Test code changed: no"
    (the matrix only sees the spec and direct imports). If a changed file is used by this test, and other tests that
    call the same endpoint WITHOUT that file pass, lean test_bug. If other tests without the changed file also fail
    on the same endpoint, lean backend_bug. A changed response is still strong evidence: an unrelated file change
    does not turn it into test_bug.
12. Answer order: write summary and evidence first, then deviates (the side that deviates according to the summary),
    then the matching category: test -> test_bug, api -> backend_bug, environment -> environment.
13. If you are confident enough, include rule_candidate: a Go (RE2) regex that captures the MECHANISM of this error,
    not the test name, endpoint, data, or changing numbers. The regex must work for similar future cases. The
    candidate is only a draft: it is not used until the cause is proven and it passes backtesting. If there is no
    safe pattern, leave rule_candidate empty.

Answer by calling the report_verdict tool.`

var verdictTool = json.RawMessage(`{
  "type": "object",
  "properties": {
    "summary":    {"type": "string", "description": "Write this first: explanation of the cause, at most 3 sentences."},
    "evidence":   {"type": "array", "items": {"type": "string"}, "maxItems": 4,
                   "description": "Exact quotes from FACTS supporting the conclusion."},
    "deviates":   {"type": "string", "enum": ["test", "api", "environment", "unclear"],
                   "description": "The side that deviates from correct behavior, matching the summary (see the API contract if present)."},
    "category":   {"type": "string", "enum": ["backend_bug", "test_bug", "environment", "flaky", "unknown"],
                   "description": "Matches deviates: test -> test_bug, api -> backend_bug, environment -> environment."},
    "confidence": {"type": "string", "enum": ["low", "medium", "high"]},
    "next_step":  {"type": "string", "description": "One concrete next step for the SDET."},
    "rule_candidate": {
      "type": "object",
      "description": "Optional. Draft Go (RE2) regex for this error; not used until proven and backtested.",
      "properties": {
        "pattern":   {"type": "string", "description": "Go (RE2) regex against the error message, not the test name/data."},
        "summary":   {"type": "string", "description": "General cause that is correct for every matching error."},
        "next_step": {"type": "string", "description": "One concrete, general next step."}
      },
      "required": ["pattern", "summary", "next_step"]
    }
  },
  "required": ["summary", "evidence", "deviates", "category", "confidence", "next_step"]
}`)

type verdict struct {
	Deviates      string         `json:"deviates"`
	Category      string         `json:"category"`
	Confidence    string         `json:"confidence"`
	Summary       string         `json:"summary"`
	Evidence      []string       `json:"evidence"`
	NextStep      string         `json:"next_step"`
	RuleCandidate *ruleCandidate `json:"rule_candidate,omitempty"`
}

// BuildFacts menyusun bagian FAKTA untuk prompt. Semua isi sudah diredaksi saat ingest.
func BuildFacts(f *store.Facts) string {
	g := f.Group
	var b strings.Builder
	fmt.Fprintf(&b, "Test: %s\nProject: %s\nFile: %s\n", g.Title, g.Project, g.File)
	if f.ErrorLocation != "" {
		fmt.Fprintf(&b, "Error location: %s\n", f.ErrorLocation)
	}
	fmt.Fprintf(&b, "\nError message:\n%s\n", clip(f.ErrorMessage, 3000))
	if len(f.Sources) > 0 {
		b.WriteString(codeSection(f.Sources))
	} else if f.ErrorSnippet != "" {
		fmt.Fprintf(&b, "\nTest code around the error:\n%s\n", clip(f.ErrorSnippet, 1500))
	}

	b.WriteString("\nHistory:\n")
	fmt.Fprintf(&b, "- Group status: %s, seen %d times, regressed %d times.\n", g.Status, g.Occurrences, g.Regressions)
	if len(f.RecentStatuses) > 0 {
		fmt.Fprintf(&b, "- This test in recent runs (oldest to newest): %s\n", strings.Join(f.RecentStatuses, ", "))
	}
	fmt.Fprintf(&b, "- Flaky results in the last 20 runs: %d\n", f.FlakyRecent)

	if len(f.CurrentCalls) > 0 {
		b.WriteString("\nAPI response at failure (shape only, no values; JavaScript type: number includes integers and decimals):\n")
		for _, c := range f.CurrentCalls {
			fmt.Fprintf(&b, "- %s %s -> %d %s\n", c.Method, c.Path, c.Status, clip(compactJSON(c.Shape), 400))
		}
	}

	if len(f.CodeFiles) > 0 || len(f.CurrentCalls) > 0 {
		b.WriteString("\nCode map (other tests in the same run):\n")
		if len(f.CodeFiles) > 0 {
			fmt.Fprintf(&b, "- Files used by this test: %s\n", strings.Join(f.CodeFiles, ", "))
		}
		if len(f.CodeMap) == 0 {
			b.WriteString("- No other test in this run uses the same endpoint or file.\n")
		}
		for _, l := range f.CodeMap {
			fmt.Fprintf(&b, "- %s %s: %d other tests passed, %d failed\n", l.Kind, l.Name, l.Passed, l.Failed)
		}
	}

	if f.Contract != "" {
		fmt.Fprintf(&b, "\nAPI contract (OpenAPI) at failure, for called endpoints, * = required:\n%s", clip(f.Contract, 4000))
		b.WriteString("Response conformance to the contract (checked automatically):\n")
		if len(f.ContractViolations) == 0 {
			b.WriteString("- matches the contract\n")
		}
		for _, v := range f.ContractViolations {
			fmt.Fprintf(&b, "- does not match: %s\n", v)
		}
	}

	if k := f.Knowledge; k != nil && len(k.Files)+len(k.Endpoints) > 0 {
		b.WriteString(graphSection(k))
	}

	b.WriteString("\nChange matrix:\n")
	if f.LastPass == nil {
		b.WriteString("- This test has not passed since Redline started recording it, so there is no baseline.\n")
	} else {
		fmt.Fprintf(&b, "- Baseline: last passing run #%d, first failure after it #%d\n", f.LastPass.RunID, f.FirstFail.RunID)
		fmt.Fprintf(&b, "- Test code changed: %s\n", yesNo(f.TestChanged))
		fmt.Fprintf(&b, "- API response: %s\n", responseVerdictLabel(f.Response.Verdict))
		for _, l := range f.Response.Lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
		if f.ContractChanged != "" {
			fmt.Fprintf(&b, "- API contract changed: %s\n", yesNo(f.ContractChanged))
			if f.ContractBefore != "" {
				fmt.Fprintf(&b, "  Contract at the last passing run:\n%s", indent(clip(f.ContractBefore, 2000), "    "))
			}
		}
		if info := versionInfo(f); info != "" {
			b.WriteString(info)
		}
	}
	return b.String()
}

// graphSection menuliskan pengetahuan graph (KAG): file sampai import terdalam beserta perubahannya, dan
// test lain di run yang sama yang memakai file atau endpoint yang sama.
func graphSection(k *store.Knowledge) string {
	var b strings.Builder
	b.WriteString("\nGraph knowledge (transitive imports and other tests in the same run):\n")
	if k.Compared {
		fmt.Fprintf(&b, "- File baseline: last passing run #%d\n", k.PassRunID)
	} else {
		b.WriteString("- File changes cannot be evaluated (no passing run with an import graph).\n")
	}
	for _, f := range k.Files {
		how := "direct"
		if !f.Direct {
			how = "through another import"
		}
		state := ""
		if f.Changed {
			state = ", CHANGED since last pass"
		}
		fmt.Fprintf(&b, "- File %s (%s%s)\n", f.Path, how, state)
		if f.Changed {
			fmt.Fprintf(&b, "  other tests using it: %s\n", splitText(f.Others))
		}
	}
	for _, e := range k.Endpoints {
		seen := "passed before"
		if !e.SeenPassing {
			seen = "never called by a passing test"
		}
		fmt.Fprintf(&b, "- Endpoint %s (%s)\n", e.Key, seen)
		if len(e.WithChanged.Passed)+len(e.WithChanged.Failed) > 0 {
			fmt.Fprintf(&b, "  other tests using the changed file: %s\n", splitText(e.WithChanged))
		}
		fmt.Fprintf(&b, "  other tests without the changed file: %s\n", splitText(e.Without))
	}
	return b.String()
}

// splitText: "lulus 1 (direct.spec.ts › x); gagal 0".
func splitText(t store.TestSplit) string {
	part := func(label string, tests []string) string {
		if len(tests) == 0 {
			return label + " 0"
		}
		names := titles(tests)
		if len(names) > 4 {
			names = append(names[:4], fmt.Sprintf("+%d more", len(names)-4))
		}
		return fmt.Sprintf("%s %d (%s)", label, len(tests), strings.Join(names, ", "))
	}
	return part("passed", t.Passed) + "; " + part("failed", t.Failed)
}

// Batas kode test di prompt: cukup untuk spec + schema/helper, tetap murah.
const (
	maxCodePerFile = 6000
	maxCodeTotal   = 16000
)

// codeSection menulis kode test dengan nomor baris, supaya cocok dengan lokasi error (file:baris).
func codeSection(files []store.SourceFile) string {
	var b strings.Builder
	b.WriteString("\nTest code (spec files then imported files, already redacted):\n")
	budget := maxCodeTotal
	for _, f := range files {
		if budget <= 0 {
			fmt.Fprintf(&b, "\n=== %s ===\n(skipped: code length limit)\n", f.Path)
			continue
		}
		content := clip(f.Content, min(maxCodePerFile, budget))
		budget -= len(content)
		fmt.Fprintf(&b, "\n=== %s ===\n", f.Path)
		for i, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
			fmt.Fprintf(&b, "%4d | %s\n", i+1, line)
		}
	}
	return b.String()
}

// References menyusun bagian KASUS MIRIP. Sengaja di luar FAKTA: bukti AI hanya boleh dikutip dari FAKTA.
func References(f *store.Facts) string {
	if len(f.Similar) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nPROVEN SIMILAR CASES (reference, not evidence; the cause may differ):\n")
	for i, c := range f.Similar {
		by := "manual label"
		if c.Source == "experiment" {
			by = "experiment"
		}
		fmt.Fprintf(&b, "\n%d. %s, proven by %s, error semantic similarity %.2f\n   Test: %s\n   Error: %s\n",
			i+1, c.Category, by, c.Similarity, c.Test, strings.ReplaceAll(clip(c.Error, 400), "\n", "\n   "))
		if c.Reason != "" {
			fmt.Fprintf(&b, "   Cause: %s\n", clip(c.Reason, 300))
		}
	}
	return b.String()
}

// versionInfo: commit dan versi app hanya info tambahan, ditulis kalau diisi.
func versionInfo(f *store.Facts) string {
	var b strings.Builder
	if f.LastPass.CommitSHA != "" || f.FirstFail.CommitSHA != "" {
		fmt.Fprintf(&b, "- Test commit: %s -> %s\n", orUnknown(f.LastPass.CommitSHA), orUnknown(f.FirstFail.CommitSHA))
	}
	if f.LastPass.AppVersion != "" || f.FirstFail.AppVersion != "" {
		fmt.Fprintf(&b, "- App version: %s -> %s (changed: %s)\n", orUnknown(f.LastPass.AppVersion),
			orUnknown(f.FirstFail.AppVersion), changed(f.LastPass.AppVersion, f.FirstFail.AppVersion))
	}
	return b.String()
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n") + "\n"
}

func changed(before, after string) string {
	switch {
	case before == "" || after == "":
		return "unknown"
	case before == after:
		return "no"
	default:
		return "yes"
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "(not recorded)"
	}
	return s
}

func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "\n…(truncated)"
	}
	return s
}
