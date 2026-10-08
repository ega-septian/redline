package report

import "testing"

func TestProjectPath(t *testing.T) {
	root := "/Users/ega/Documents/playwright/coba2"
	cases := map[string]string{
		"/Users/ega/Documents/playwright/coba2/test-results/brand-toolshop/trace.zip": "test-results/brand-toolshop/trace.zip",
		"/home/runner/work/coba2/coba2/test-results/x/test-failed-1.png":              "test-results/x/test-failed-1.png", // root berbeda (CI)
		`C:\Users\ega\coba2\test-results\x\trace.zip`:                                 "test-results/x/trace.zip",         // Windows
		"/Users/ega/Desktop/lain/trace.zip":                                           "trace.zip",
		"":                                                                            "",
	}
	for in, want := range cases {
		if got := ProjectPath(root, in); got != want {
			t.Errorf("ProjectPath(%q) = %q, mau %q", in, got, want)
		}
	}
}
