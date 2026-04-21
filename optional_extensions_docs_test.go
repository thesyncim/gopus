package gopus

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func mustReadDocForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestOptionalExtensionDocsContract(t *testing.T) {
	optionalDoc := mustReadDocForTest(t, "docs/optional-extensions.md")
	for _, tc := range []struct {
		name   string
		ext    OptionalExtension
		status string
	}{
		{name: "DNN blob loading", ext: OptionalExtensionDNNBlob, status: "Supported"},
		{name: "QEXT", ext: OptionalExtensionQEXT, status: "Supported"},
		{name: "DRED", ext: OptionalExtensionDRED, status: "Unsupported and quarantined"},
		{name: "OSCE BWE", ext: OptionalExtensionOSCEBWE, status: "Unsupported and quarantined"},
	} {
		wantLine := fmt.Sprintf("| %s | %s |", tc.name, tc.status)
		if !strings.Contains(optionalDoc, wantLine) {
			t.Fatalf("docs/optional-extensions.md missing matrix row %q", wantLine)
		}
	}

	readme := mustReadDocForTest(t, "README.md")
	for _, needle := range []string{
		"[Optional Extensions](docs/optional-extensions.md)",
		"`SetDNNBlob(...)` plus `SetQEXT(...)` / `QEXT()` are supported",
		"`SetDREDDuration(...)` and `SetOSCEBWE(...)` are absent unless you build with `-tags gopus_unsupported_controls`",
	} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}

	releaseNotes := mustReadDocForTest(t, "docs/releases/v0.1.0.md")
	for _, needle := range []string{
		"## Optional Extension Contract",
		"`SetDNNBlob(...)` on `Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder`",
		"`SetDREDDuration(...)` / `DREDDuration()` are absent unless built with `-tags gopus_unsupported_controls`",
		"`SetOSCEBWE(...)` / `OSCEBWE()` are absent unless built with `-tags gopus_unsupported_controls`",
	} {
		if !strings.Contains(releaseNotes, needle) {
			t.Fatalf("docs/releases/v0.1.0.md missing %q", needle)
		}
	}

	releaseGuide := mustReadDocForTest(t, "docs/releases/README.md")
	if !strings.Contains(releaseGuide, "the optional extension contract, including supported default-build methods and any tag-gated or absent surfaces") {
		t.Fatal("docs/releases/README.md missing optional-extension contract guidance")
	}

	for _, needle := range []string{
		"It does not change `SupportsOptionalExtension(...)`, and it does not turn DRED or OSCE BWE into supported release features.",
		"Some control state is retained and observable, but full model-backed DRED encode/decode and OSCE BWE runtime behavior remain incomplete.",
	} {
		if !strings.Contains(optionalDoc, needle) {
			t.Fatalf("docs/optional-extensions.md missing %q", needle)
		}
	}

	examples := mustReadDocForTest(t, "examples/README.md")
	if !strings.Contains(examples, "These examples target the supported default build and intentionally do not demonstrate tag-gated unsupported controls such as DRED or OSCE BWE.") {
		t.Fatal("examples/README.md missing default-build note")
	}
}
