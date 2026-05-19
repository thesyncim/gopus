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

func containsDocText(doc, needle string) bool {
	if strings.Contains(doc, needle) {
		return true
	}
	return strings.Contains(
		strings.Join(strings.Fields(doc), " "),
		strings.Join(strings.Fields(needle), " "),
	)
}

func TestOptionalExtensionDocsContract(t *testing.T) {
	optionalDoc := mustReadDocForTest(t, "README.md")
	for _, tc := range []struct {
		name   string
		ext    OptionalExtension
		status string
	}{
		{name: "DNN blob loading", ext: OptionalExtensionDNNBlob, status: "Supported by default"},
		{name: "QEXT", ext: OptionalExtensionQEXT, status: "Tagged support"},
		{name: "DRED", ext: OptionalExtensionDRED, status: "Tagged control/standalone support"},
		{name: "OSCE BWE", ext: OptionalExtensionOSCEBWE, status: "Unsupported and quarantined"},
	} {
		wantLine := fmt.Sprintf("| %s | %s | `%s` |", tc.name, tc.status, optionalExtensionDocSymbol(tc.ext))
		if !containsDocText(optionalDoc, wantLine) {
			t.Fatalf("README.md missing optional-extension matrix row %q", wantLine)
		}
	}

	for _, needle := range []string{
		"Default builds support `SetDNNBlob(...)` only.",
		"QEXT and DRED require build tags.",
		"OSCE BWE remains quarantine-only.",
		"make test-dnn-blob-parity",
		"make test-qext-parity",
		"make test-dred-tag",
		"make test-unsupported-controls-parity",
		"does not make unsupported features part of the public support claim",
	} {
		if !containsDocText(optionalDoc, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}
	assertOptionalExtensionDocsMatchSupport(t, optionalDoc)

	for _, needle := range []string{
		"Default builds support `SetDNNBlob(...)` only.",
		"QEXT requires `-tags gopus_qext`",
		"DRED control/standalone surfaces require `-tags gopus_dred`",
		"OSCE BWE remains unsupported outside quarantine builds",
	} {
		if !containsDocText(optionalDoc, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}

	examples := mustReadDocForTest(t, "examples/README.md")
	if !strings.Contains(examples, "These examples target the supported default build. QEXT examples require `-tags gopus_qext`; DRED examples require `-tags gopus_dred`; OSCE BWE remains quarantine-only.") {
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

	if !SupportsOptionalExtension(OptionalExtensionDNNBlob) {
		t.Fatalf("%s documented as default-supported but current build reports unsupported", optionalExtensionDocSymbol(OptionalExtensionDNNBlob))
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("OptionalExtensionOSCEBWE documented as unsupported but current build reports supported")
	}

	if SupportsOptionalExtension(OptionalExtensionQEXT) && !strings.Contains(optionalDoc, "go test -tags gopus_qext ./...") {
		t.Fatal("README.md missing QEXT tag guidance")
	}
	if SupportsOptionalExtension(OptionalExtensionDRED) && !strings.Contains(optionalDoc, "go test -tags gopus_dred ./...") {
		t.Fatal("README.md missing DRED tag guidance")
	}
}
