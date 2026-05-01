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
		{name: "DRED", ext: OptionalExtensionDRED, status: "Tagged control/standalone support"},
		{name: "OSCE BWE", ext: OptionalExtensionOSCEBWE, status: "Unsupported and quarantined"},
	} {
		wantLine := fmt.Sprintf("| %s | %s | `%s` |", tc.name, tc.status, optionalExtensionDocSymbol(tc.ext))
		if !strings.Contains(optionalDoc, wantLine) {
			t.Fatalf("docs/optional-extensions.md missing matrix row %q", wantLine)
		}
	}
	assertOptionalExtensionDocsMatchSupport(t, optionalDoc)

	readme := mustReadDocForTest(t, "README.md")
	for _, needle := range []string{
		"[Optional Extensions](docs/optional-extensions.md)",
		"Supported default controls are `SetDNNBlob(...)` plus `SetQEXT(...)` / `QEXT()`",
		"DRED control and standalone surfaces are supported with `-tags gopus_dred`",
		"this is not broad DRED decoder audio-path support",
		"may also expose DRED controls/helpers for parity work",
		"OSCE BWE remains quarantine-only under `-tags gopus_unsupported_controls`",
		"that quarantine tag does not itself make `SupportsOptionalExtension(...)` report support",
	} {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}

	docGo := mustReadDocForTest(t, "doc.go")
	for _, needle := range []string{
		"// # Supported Default Build",
		"// optional controls in the default build currently include SetDNNBlob plus",
		"// SetQEXT/QEXT. DRED control and standalone surfaces are supported only in",
		"// builds using `-tags gopus_dred`.",
		"// `-tags gopus_unsupported_controls` may also expose DRED controls/helpers for",
		"// parity work, but they do not report DRED support.",
		"// OSCE BWE remains quarantined from the default API surface.",
		"// `-tags gopus_unsupported_controls`, and that tag does not itself report",
	} {
		if !strings.Contains(docGo, needle) {
			t.Fatalf("doc.go missing %q", needle)
		}
	}

	releaseNotes := mustReadDocForTest(t, "docs/releases/v0.1.0.md")
	for _, needle := range []string{
		"## Optional Extension Contract",
		"`SetDNNBlob(...)` on `Encoder`, `Decoder`, `MultistreamEncoder`, and `MultistreamDecoder`",
		"Decoder-side `SetDNNBlob(...)` currently covers loader-derived validation and retained control state.",
		"DRED control and standalone surfaces are supported only when built with `-tags gopus_dred`",
		"standalone DRED wrapper lifecycle/no-allocation, libopus parse/decode/process metadata checks, real-packet standalone process state/feature and recovery scheduling parity",
		"`make test-dred-tag`",
		"mirrored by `make test-unsupported-controls-parity`",
		"`SetOSCEBWE(...)` / `OSCEBWE()` are absent unless built with `-tags gopus_unsupported_controls`",
		"The `gopus_unsupported_controls` build remains a parity/quarantine umbrella",
		"may expose DRED controls and standalone",
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
		"Build tag-gated DRED control/standalone support explicitly",
		"top-level decoder DRED internals",
		"expose DRED controls/standalone helpers",
		"does not, by itself, change `SupportsOptionalExtension(...)`",
		"release support comes from `gopus_dred`",
		"standalone DRED wrapper lifecycle,",
		"`make test-dred-tag` exercises standalone DRED wrapper lifecycle",
		"zero-allocation, libopus parse/decode/process metadata coverage, and real-packet",
		"standalone process state/feature and recovery scheduling parity plus the",
		"`make test-unsupported-controls-parity` also enforces that carried-payload seam",
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

func optionalExtensionDocSymbol(ext OptionalExtension) string {
	switch ext {
	case OptionalExtensionDRED:
		return "OptionalExtensionDRED"
	case OptionalExtensionDNNBlob:
		return "OptionalExtensionDNNBlob"
	case OptionalExtensionQEXT:
		return "OptionalExtensionQEXT"
	case OptionalExtensionOSCEBWE:
		return "OptionalExtensionOSCEBWE"
	default:
		return string(ext)
	}
}

func assertOptionalExtensionDocsMatchSupport(t *testing.T, optionalDoc string) {
	t.Helper()

	for _, ext := range []OptionalExtension{OptionalExtensionDNNBlob, OptionalExtensionQEXT} {
		if !SupportsOptionalExtension(ext) {
			t.Fatalf("%s documented as default-supported but current build reports unsupported", optionalExtensionDocSymbol(ext))
		}
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("OptionalExtensionOSCEBWE documented as unsupported but current build reports supported")
	}

	if SupportsOptionalExtension(OptionalExtensionDRED) {
		if !strings.Contains(optionalDoc, "`SupportsOptionalExtension(gopus.OptionalExtensionDRED)` reports `true` only in\nthat tagged DRED build") {
			t.Fatal("docs/optional-extensions.md missing gopus_dred-only DRED support probe wording")
		}
		return
	}
	if !strings.Contains(optionalDoc, "Build tag-gated DRED control/standalone support explicitly") {
		t.Fatal("docs/optional-extensions.md missing explicit DRED build-tag guidance")
	}
}
