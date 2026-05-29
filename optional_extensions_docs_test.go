package gopus

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
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
		{name: "DNN blob loading", ext: OptionalExtensionDNNBlob, status: "Supported under `gopus_dred` / `gopus_extra_controls`"},
		{name: "QEXT", ext: OptionalExtensionQEXT, status: "Supported under `gopus_qext`"},
		{name: "DRED", ext: OptionalExtensionDRED, status: "Supported under `gopus_dred` (control + standalone)"},
		{name: "OSCE BWE", ext: OptionalExtensionOSCEBWE, status: "Supported under `gopus_extra_controls`"},
	} {
		wantLine := fmt.Sprintf("| %s | %s | `%s` |", tc.name, tc.status, optionalExtensionDocSymbol(tc.ext))
		if !containsDocText(optionalDoc, wantLine) {
			t.Fatalf("README.md missing optional-extension matrix row %q", wantLine)
		}
	}

	for _, needle := range []string{
		"Default builds expose no optional extensions; `SetDNNBlob(...)` is a no-op returning `ErrOptionalExtensionUnavailable`.",
		"DNN blob loading (USE_WEIGHTS_FILE model loading) requires `-tags gopus_dred` or",
		"`-tags gopus_extra_controls`; QEXT requires `-tags gopus_qext`; DRED",
		"control/standalone surfaces require `-tags gopus_dred`; OSCE BWE/LACE/NoLACE",
		"require `-tags gopus_extra_controls`.",
		"parity-complete and supported, exactly as libopus exposes them behind the",
		"corresponding compile flag.",
		"make test-dnn-blob-parity",
		"make test-qext-parity",
		"make test-dred-tag",
		"make test-extra-controls-parity",
		"enables the OSCE and deep-PLC family exactly as",
		"link zero code into the default build",
	} {
		if !containsDocText(optionalDoc, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}
	assertOptionalExtensionDocsMatchSupport(t, optionalDoc)

	examples := mustReadDocForTest(t, "examples/README.md")
	if !strings.Contains(examples, "These examples target the supported default build. QEXT examples require `-tags gopus_qext`; DRED examples require `-tags gopus_dred`; OSCE BWE remains extra-controls parity only.") {
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

	// DNN blob loading is now tag-gated exactly like libopus's USE_WEIGHTS_FILE
	// loaders (built only under ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC). The
	// default build reports no support; -tags gopus_dred / gopus_extra_controls
	// turn it on alongside the DRED/OSCE runtime hooks.
	if SupportsOptionalExtension(OptionalExtensionDNNBlob) != extsupport.DNNBlob {
		t.Fatalf("SupportsOptionalExtension(DNNBlob)=%v want %v", SupportsOptionalExtension(OptionalExtensionDNNBlob), extsupport.DNNBlob)
	}
	if SupportsOptionalExtension(OptionalExtensionDNNBlob) && !strings.Contains(optionalDoc, "go test -tags gopus_dred ./...") {
		t.Fatal("README.md missing DNN blob tag guidance")
	}
	if SupportsOptionalExtension(OptionalExtensionOSCEBWE) {
		t.Fatal("OptionalExtensionOSCEBWE documented as extra-control parity only but current build reports support")
	}

	if SupportsOptionalExtension(OptionalExtensionQEXT) && !strings.Contains(optionalDoc, "go test -tags gopus_qext ./...") {
		t.Fatal("README.md missing QEXT tag guidance")
	}
	if SupportsOptionalExtension(OptionalExtensionDRED) && !strings.Contains(optionalDoc, "go test -tags gopus_dred ./...") {
		t.Fatal("README.md missing DRED tag guidance")
	}
}
