package gopus

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestTrustDocsContract(t *testing.T) {
	readme := mustReadDocForTest(t, "README.md")
	for _, needle := range []string{
		"## Trust And Verification",
		"Released version: none yet.",
		"`v0.1.0` is not a release until the tag and GitHub Release are both published.",
		"Latest release evidence:",
		"Required branch checks:",
		"Release checklist:",
		"Dependabot is enabled",
		"OpenSSF Scorecard",
		"SPDX or CycloneDX",
		"[SECURITY.md](SECURITY.md)",
		"[examples/external-consumer-smoke/smoke_test.go](examples/external-consumer-smoke/smoke_test.go)",
	} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README.md trust section missing %q", needle)
		}
	}

	for _, command := range []string{
		"go test ./...",
		"make test-doc-contract",
		"make lint",
		"make test-consumer-smoke",
		"make test-examples-smoke",
		"make verify-production",
		"make verify-production-exhaustive",
		"make release-evidence",
	} {
		if !strings.Contains(readme, command) {
			t.Fatalf("README.md release checklist missing required command %s", command)
		}
	}

	security := mustReadDocForTest(t, "SECURITY.md")
	for _, needle := range []string{
		"Do not open a public issue",
		"Prefer GitHub private vulnerability reporting",
		"email `thesyncim@gmail.com`",
	} {
		if !strings.Contains(security, needle) {
			t.Fatalf("SECURITY.md missing %q", needle)
		}
	}

	requiredChecks := extractRequiredChecks(t, readme)
	wantChecks := []string{"lint-static-analysis", "test-linux", "perf-linux", "test-macos", "test-windows"}
	if !reflect.DeepEqual(requiredChecks, wantChecks) {
		t.Fatalf("required checks = %v, want %v", requiredChecks, wantChecks)
	}
	ciJobs := workflowJobNames(t, ".github/workflows/ci.yml")
	for _, check := range requiredChecks {
		if !ciJobs[check] {
			t.Fatalf("README.md lists stale required check %q; actual CI job names are %v", check, sortedKeys(ciJobs))
		}
	}

	for _, needle := range []string{"Dependabot is enabled", "OpenSSF Scorecard", "SPDX or CycloneDX"} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}
}

func TestTrustSensitiveFilesHaveCodeOwners(t *testing.T) {
	codeowners := mustReadDocForTest(t, ".github/CODEOWNERS")
	for _, pattern := range []string{
		".github/workflows/*",
		"SECURITY.md",
		"README.md",
		"tools/ensure_libopus.sh",
		"Makefile",
	} {
		if !strings.Contains(codeowners, pattern+" @thesyncim") {
			t.Fatalf(".github/CODEOWNERS missing owner for %s", pattern)
		}
	}
}

func TestReleaseNotesSourceIsReadme(t *testing.T) {
	readme := mustReadDocForTest(t, "README.md")
	for _, needle := range []string{
		"Released version: none yet.",
		"`v0.1.0` is not a release until the tag and GitHub Release are both published.",
		"make release-evidence",
	} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README.md release notes source missing %q", needle)
		}
	}
}

func extractRequiredChecks(t *testing.T, doc string) []string {
	t.Helper()
	const start = "<!-- required-checks:start -->"
	const end = "<!-- required-checks:end -->"
	startAt := strings.Index(doc, start)
	endAt := strings.Index(doc, end)
	if startAt < 0 || endAt < 0 || endAt <= startAt {
		t.Fatalf("README.md missing required-checks markers")
	}

	block := doc[startAt+len(start) : endAt]
	var checks []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- `") && strings.HasSuffix(line, "`") {
			checks = append(checks, strings.TrimSuffix(strings.TrimPrefix(line, "- `"), "`"))
		}
	}
	return checks
}

func workflowJobNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	data := mustReadDocForTest(t, path)
	names := workflowJobNamesFromText(t, path, data)
	crlfData := strings.ReplaceAll(data, "\n", "\r\n")
	crlfNames := workflowJobNamesFromText(t, path+" with CRLF", crlfData)
	if !reflect.DeepEqual(names, crlfNames) {
		t.Fatalf("workflow job names differ under CRLF line endings: lf=%v crlf=%v", sortedKeys(names), sortedKeys(crlfNames))
	}
	return names
}

func workflowJobNamesFromText(t *testing.T, path, data string) map[string]bool {
	t.Helper()
	names := make(map[string]bool)
	inJobs := false
	inJob := false

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(strings.TrimSpace(line), ":") {
			inJob = true
			continue
		}
		if inJob && strings.HasPrefix(line, "    name:") {
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "name:"))
			name = strings.Trim(name, `"'`)
			names[name] = true
		}
	}
	if len(names) == 0 {
		t.Fatalf("no job names parsed from %s", path)
	}
	return names
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
