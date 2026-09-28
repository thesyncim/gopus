//go:build gopus_dred && gopus_osce && gopus_qext

package gopus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestDecoderDREDTripleReferenceArchiveMatchesGoFeatures(t *testing.T) {
	libopustest.RequireOracle(t)
	identity, err := libopustest.ResolvePublicAPIReferenceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	if !identity.DNN || !identity.DRED || !identity.OSCE || !identity.QEXT || identity.Variant != variant {
		t.Fatalf("triple-feature reference identity=%+v, want DRED+OSCE+QEXT and %s", identity, variant)
	}
	config, err := os.ReadFile(filepath.Join(identity.BuildDir, "config.h"))
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range []string{"ENABLE_DRED", "ENABLE_OSCE", "ENABLE_OSCE_BWE", "ENABLE_QEXT"} {
		if !strings.Contains(string(config), "#define "+feature+" 1\n") {
			t.Fatalf("selected %s reference config lacks %s", variant, feature)
		}
	}
	// Validate checks the stamped source, feature flags, and the C ISA selected
	// for the current Go instruction lane.
	if err := identity.Validate(); err != nil {
		t.Fatal(err)
	}
	helper, err := buildLibopusDREDHelper("libopus_decoder_dred_decode_float_info.c", "gopus_libopus_decoder_dred_decode_float", true)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(helper) != identity.BuildDir {
		t.Fatalf("DRED decoder helper %q does not use selected %s archive in %q", helper, variant, identity.BuildDir)
	}
}
