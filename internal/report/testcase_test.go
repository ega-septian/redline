package report

import "testing"

func TestTestCaseID(t *testing.T) {
	cases := map[string][]string{
		"TC-BRD-001": {"TC-BRD-001"},
		"TC-USR-002": {"@smoke", "@TC-USR-002"},
		"tc-brd-003": {"tc-brd-003"},
		"":           {"@smoke", "TC-"},
	}
	for want, tags := range cases {
		if got := TestCaseID(tags); got != want {
			t.Errorf("TestCaseID(%v) = %q, mau %q", tags, got, want)
		}
	}
	if got := TestCaseID(nil); got != "" {
		t.Errorf("tanpa tag harus kosong, dapat %q", got)
	}
}
