package gopus_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/thesyncim/gopus"
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
		ext    gopus.OptionalExtension
		status string
	}{
		{name: "DNN blob loading", ext: gopus.OptionalExtensionDNNBlob, status: "Available under `gopus_dred` / `gopus_osce`"},
		{name: "QEXT", ext: gopus.OptionalExtensionQEXT, status: "Available under `gopus_qext`"},
		{name: "DRED", ext: gopus.OptionalExtensionDRED, status: "Available under `gopus_dred` (control + standalone)"},
		{name: "OSCE BWE", ext: gopus.OptionalExtensionOSCEBWE, status: "Extra controls under `gopus_osce`; support probe returns false"},
	} {
		wantLine := fmt.Sprintf("| %s | %s | `%s` |", tc.name, tc.status, optionalExtensionDocSymbol(tc.ext))
		if !containsDocText(optionalDoc, wantLine) {
			t.Fatalf("README.md missing optional-extension matrix row %q", wantLine)
		}
	}

	// The matrix above locks feature availability; these checks preserve the
	// default-build error and the validation boundary without fixing prose layout.
	for _, needle := range []string{
		"`SetDNNBlob(...)`", "`ErrOptionalExtensionUnavailable`",
		"`USE_WEIGHTS_FILE`", "deep PLC", "excluded from the default",
		"does not establish parity", "reports/validation.md#coverage",
		"`SupportsOptionalExtension(OptionalExtensionOSCEBWE)` reports false",
	} {
		if !containsDocText(optionalDoc, needle) {
			t.Fatalf("README.md missing %q", needle)
		}
	}
	contributing := mustReadDocForTest(t, "CONTRIBUTING.md")
	for _, command := range []string{
		"make test-dnn-blob-parity", "make test-qext-parity",
		"make test-dred-tag", "make test-extra-controls-parity",
	} {
		if !strings.Contains(contributing, command) {
			t.Fatalf("CONTRIBUTING.md missing %q", command)
		}
	}
	assertOptionalExtensionDocsMatchSupport(t, optionalDoc)

	examples := optionalDoc
	for _, needle := range []string{
		"Most examples use the default build. Optional APIs require their matching build tag: QEXT uses `-tags gopus_qext`, DRED uses `-tags gopus_dred`, and OSCE uses `-tags gopus_osce`.",
		"These runnable examples demonstrate API usage; they do not imply that every optional feature and architecture has completed parity validation.",
	} {
		if !containsDocText(examples, needle) {
			t.Fatalf("README.md examples section missing %q", needle)
		}
	}
}

func optionalExtensionDocSymbol(ext gopus.OptionalExtension) string {
	switch ext {
	case gopus.OptionalExtensionDRED:
		return "OptionalExtensionDRED"
	case gopus.OptionalExtensionDNNBlob:
		return "OptionalExtensionDNNBlob"
	case gopus.OptionalExtensionQEXT:
		return "OptionalExtensionQEXT"
	case gopus.OptionalExtensionOSCEBWE:
		return "OptionalExtensionOSCEBWE"
	default:
		return string(ext)
	}
}

func assertOptionalExtensionDocsMatchSupport(t *testing.T, optionalDoc string) {
	t.Helper()

	// DNN blob loading is tag-gated like libopus's USE_WEIGHTS_FILE
	// loaders (built only under ENABLE_DRED/ENABLE_OSCE/ENABLE_DEEP_PLC). The
	// default build reports no support; -tags gopus_dred / gopus_osce
	// turn it on alongside the DRED/OSCE runtime hooks.
	if gopus.SupportsOptionalExtension(gopus.OptionalExtensionDNNBlob) != extsupport.DNNBlob {
		t.Fatalf("SupportsOptionalExtension(DNNBlob)=%v want %v", gopus.SupportsOptionalExtension(gopus.OptionalExtensionDNNBlob), extsupport.DNNBlob)
	}
	if gopus.SupportsOptionalExtension(gopus.OptionalExtensionDNNBlob) && !strings.Contains(optionalDoc, "go test -tags gopus_dred ./...") {
		t.Fatal("README.md missing DNN blob tag guidance")
	}
	if gopus.SupportsOptionalExtension(gopus.OptionalExtensionOSCEBWE) {
		t.Fatal("OptionalExtensionOSCEBWE documented as extra-control parity only but current build reports support")
	}

	if gopus.SupportsOptionalExtension(gopus.OptionalExtensionQEXT) && !strings.Contains(optionalDoc, "go test -tags gopus_qext ./...") {
		t.Fatal("README.md missing QEXT tag guidance")
	}
	if gopus.SupportsOptionalExtension(gopus.OptionalExtensionDRED) && !strings.Contains(optionalDoc, "go test -tags gopus_dred ./...") {
		t.Fatal("README.md missing DRED tag guidance")
	}
}
