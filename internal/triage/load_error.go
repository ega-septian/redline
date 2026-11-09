package triage

import (
	"regexp"
	"strings"
)

// LoadErrorAnalysis is the rule-based diagnosis for a report-level load error. It never calls AI:
// the patterns are deliberately narrow so an unknown message stays "unclear" instead of guessing.
type LoadErrorAnalysis struct {
	Category   string `json:"category"`
	Confidence string `json:"confidence"`
	Source     string `json:"source"`
	Summary    string `json:"summary"`
	NextStep   string `json:"next_step"`
}

var (
	loadSyntax     = regexp.MustCompile(`(?i)(syntaxerror|unexpected token|cannot use import statement outside a module|failed to parse|invalid or unexpected token)`)
	loadModule     = regexp.MustCompile(`(?i)(cannot find module|cannot find package|module not found|failed to resolve import|err_module_not_found)`)
	loadModulePath = regexp.MustCompile(`(?i)(?:cannot find module|cannot find package|failed to resolve import)\s+['"]?([^'"\s]+)`)
	loadGlobal     = regexp.MustCompile(`(?i)(global ?setup|global ?teardown|web ?server)`)
	loadNetwork    = regexp.MustCompile(`(?i)(econnrefused|enotfound|etimedout|timeout|network)`)
	loadPermission = regexp.MustCompile(`(?i)(eacces|eperm|permission denied)`)
)

// AnalyzeLoadError classifies a report-level error using deterministic rules.
func AnalyzeLoadError(message string) LoadErrorAnalysis {
	text := strings.ToLower(message)
	switch {
	case loadSyntax.MatchString(text):
		return result("test_bug", "high", "A spec or helper has a syntax or module-format error.",
			"Fix the syntax or module setting, then run the spec directly.")
	case loadModule.MatchString(text):
		path := ""
		if match := loadModulePath.FindStringSubmatch(message); len(match) > 1 {
			path = strings.TrimSpace(match[1])
		}
		if path != "" && isLocalModule(path) {
			return result("test_bug", "high", "A spec or helper imports a file that cannot be resolved.",
				"Check the import path and file name, then run the spec directly.")
		}
		if path != "" {
			return result("environment", "high", "A package imported by the suite is not installed.",
				"Run npm ci and verify the dependency is declared in package.json.")
		}
		return result("test_bug", "medium", "A module needed to load the suite is missing.",
			"Check the import path and whether the dependency is installed.")
	case loadGlobal.MatchString(text):
		return result("environment", "medium", "Global setup or teardown failed before tests started.",
			"Run globalSetup manually and check its environment variables and external services.")
	case loadNetwork.MatchString(text):
		return result("environment", "high", "A service or network dependency was unreachable during setup.",
			"Start the service and verify the URL/port used by globalSetup or webServer.")
	case loadPermission.MatchString(text):
		return result("environment", "high", "File permissions blocked the suite from loading.",
			"Check ownership and permissions for the test and cache directories.")
	default:
		return result("unclear", "none", "Suite failed to load for an unclassified reason.",
			"Open the run detail and inspect the full report error.")
	}
}

func result(category, confidence, summary, nextStep string) LoadErrorAnalysis {
	source := "rule"
	if category == "unclear" {
		source = "none"
	}
	return LoadErrorAnalysis{
		Category: category, Confidence: confidence, Source: source,
		Summary: summary, NextStep: nextStep,
	}
}

func isLocalModule(path string) bool {
	return strings.HasPrefix(path, ".") || strings.HasPrefix(path, "/") ||
		strings.HasPrefix(path, "\\") || strings.HasPrefix(path, "../")
}
