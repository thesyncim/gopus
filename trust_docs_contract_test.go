package gopus

import (
	"os"
	"os/exec"
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
		"[required checks and branch protection](docs/maintainers/CI_GUARDRAILS.md)",
		"[private reporting and supported versions](SECURITY.md)",
		"[release checklist](docs/maintainers/RELEASE_CHECKLIST.md)",
		"[Dependabot, Scorecard, action review, and release provenance plan](docs/maintainers/SUPPLY_CHAIN.md)",
		"[external consumer smoke test](examples/external-consumer-smoke/smoke_test.go)",
	} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README.md trust section missing %q", needle)
		}
	}

	releaseChecklist := mustReadDocForTest(t, "docs/maintainers/RELEASE_CHECKLIST.md")
	for _, command := range []string{
		"`go test ./...`",
		"`make test-doc-contract`",
		"`make lint`",
		"`make test-consumer-smoke`",
		"`make verify-production`",
		"`make verify-production-exhaustive`",
		"`make release-evidence`",
	} {
		if !strings.Contains(releaseChecklist, command) {
			t.Fatalf("release checklist missing required command %s", command)
		}
	}
	for _, evidence := range []string{
		"Commit SHA",
		"Go version",
		"OS/platform",
		"libopus reference version and SHA256",
		"Commands run with pass/fail summaries",
		"Benchmark guardrail result",
		"Fuzz/safety summary",
		"Parity summary",
		"Consumer-smoke result",
	} {
		if !strings.Contains(releaseChecklist, evidence) {
			t.Fatalf("release checklist missing evidence item %q", evidence)
		}
	}

	security := mustReadDocForTest(t, "SECURITY.md")
	for _, needle := range []string{
		"Do not open a public issue",
		"Prefer GitHub private vulnerability reporting",
		"email `thesyncim@gmail.com`",
		"Acknowledgment within 5 business days",
		"Initial triage or next-step update within 10 business days",
		"GitHub Security Advisory",
		"Ship a patched release",
		"Panics, crashes, memory exhaustion, CPU exhaustion, or unbounded allocation",
		"Supply-chain, release-integrity, CI-token, provenance, or dependency-update",
	} {
		if !strings.Contains(security, needle) {
			t.Fatalf("SECURITY.md missing %q", needle)
		}
	}

	releaseWorkflow := mustReadDocForTest(t, ".github/workflows/release.yml")
	for _, needle := range []string{
		"missing release notes:",
		"Run full release verification and evidence capture",
		"release evidence summary is missing",
		"Overall result: PASS",
		"release-evidence-${{ steps.meta.outputs.tag }}.tar.gz",
		"release-evidence-${{ steps.meta.outputs.tag }}.md",
		"gh release create",
		"gh release upload",
	} {
		if !strings.Contains(releaseWorkflow, needle) {
			t.Fatalf("release workflow missing %q", needle)
		}
	}

	ciGuardrails := mustReadDocForTest(t, "docs/maintainers/CI_GUARDRAILS.md")
	requiredChecks := extractRequiredChecks(t, ciGuardrails)
	wantChecks := []string{
		"lint-static-analysis",
		"test-linux",
		"perf-linux",
		"test-macos",
		"test-windows",
	}
	if !reflect.DeepEqual(requiredChecks, wantChecks) {
		t.Fatalf("required checks = %v, want %v", requiredChecks, wantChecks)
	}

	ciJobs := workflowJobNames(t, ".github/workflows/ci.yml")
	for _, check := range requiredChecks {
		if !ciJobs[check] {
			t.Fatalf("docs/maintainers/CI_GUARDRAILS.md lists stale required check %q; actual CI job names are %v", check, sortedKeys(ciJobs))
		}
	}

	supplyChain := mustReadDocForTest(t, "docs/maintainers/SUPPLY_CHAIN.md")
	for _, needle := range []string{
		"Dependabot is enabled",
		"OpenSSF Scorecard",
		"Existing workflows use version tags",
		"decide during review whether the action should be pinned by full commit SHA",
		"`go list -m -json all` module inventory",
		"signed checksums and SLSA provenance",
		"SPDX or CycloneDX",
	} {
		if !strings.Contains(supplyChain, needle) {
			t.Fatalf("docs/maintainers/SUPPLY_CHAIN.md missing %q", needle)
		}
	}
}

func TestTrustSensitiveFilesHaveCodeOwners(t *testing.T) {
	codeowners := mustReadDocForTest(t, ".github/CODEOWNERS")
	for _, pattern := range []string{
		".github/workflows/*",
		"SECURITY.md",
		"README.md",
		"docs/optional-extensions.md",
		"docs/maintainers/**",
		"docs/releases/**",
		"tools/ensure_libopus.sh",
		"Makefile",
	} {
		if !strings.Contains(codeowners, pattern+" @thesyncim") {
			t.Fatalf(".github/CODEOWNERS missing owner for %s", pattern)
		}
	}
}

func TestReleaseNotesExistForTags(t *testing.T) {
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not running inside a git checkout")
	}

	cmd := exec.Command("git", "tag", "--list", "v*")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list release tags: %v", err)
	}
	for _, tag := range strings.Fields(string(out)) {
		path := "docs/releases/" + tag + ".md"
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("release tag %s is missing non-empty release notes at %s", tag, path)
		}
	}

	if info, err := os.Stat("docs/releases/v0.1.0.md"); err != nil || info.Size() == 0 {
		t.Fatalf("prepared v0.1.0 release notes are missing or empty")
	}
}

func extractRequiredChecks(t *testing.T, doc string) []string {
	t.Helper()
	const start = "<!-- required-checks:start -->"
	const end = "<!-- required-checks:end -->"
	startAt := strings.Index(doc, start)
	endAt := strings.Index(doc, end)
	if startAt < 0 || endAt < 0 || endAt <= startAt {
		t.Fatalf("CI guardrails doc missing required-checks markers")
	}

	block := doc[startAt+len(start) : endAt]
	var checks []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- `") || !strings.HasSuffix(line, "`") {
			continue
		}
		checks = append(checks, strings.TrimSuffix(strings.TrimPrefix(line, "- `"), "`"))
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
