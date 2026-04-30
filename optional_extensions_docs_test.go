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
		{name: "DNN blob loading", ext: OptionalExtensionDNNBlob, status: "Supported by default"},
		{name: "QEXT", ext: OptionalExtensionQEXT, status: "Supported by default"},
		{name: "DRED", ext: OptionalExtensionDRED, status: "Supported with `gopus_dred` tag"},
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
		"Supported default controls are `SetDNNBlob(...)` plus `SetQEXT(...)` / `QEXT()`",
		"DRED support is compiled explicitly with `-tags gopus_dred`",
		"OSCE BWE remains quarantine-only under `-tags gopus_unsupported_controls`",
		"that quarantine tag does not itself make `SupportsOptionalExtension(...)` report support",
	} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}

	releaseNotes := mustReadDocForTest(t, "docs/releases/v0.1.0.md")
	for _, needle := range []string{
		"## Optional Extension Contract",
		"`SetDNNBlob(...)` on `Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder`",
		"Decoder-side `SetDNNBlob(...)` currently covers loader-derived validation and retained control state.",
		"DRED is supported only when built with `-tags gopus_dred`",
		"`SetOSCEBWE(...)` / `OSCEBWE()` are absent unless built with `-tags gopus_unsupported_controls`",
		"The `gopus_unsupported_controls` build remains a parity/quarantine umbrella",
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
		"Build DRED support explicitly when you need the libopus DRED surface",
		"does not, by itself, change `SupportsOptionalExtension(...)`",
		"release support comes from `gopus_dred`",
	} {
		if !strings.Contains(optionalDoc, needle) {
			t.Fatalf("docs/optional-extensions.md missing %q", needle)
		}
	}

	examples := mustReadDocForTest(t, "examples/README.md")
	if !strings.Contains(examples, "These examples target the supported default build. DRED examples require `-tags gopus_dred`; OSCE BWE remains quarantine-only.") {
		t.Fatal("examples/README.md missing default-build note")
	}
}
