//go:build gopus_fixed_point && gopus_qext

package libopustest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestFixedQEXTArchiveAndPublicHelperUsePairedReference(t *testing.T) {
	RequireOracle(t)
	variant, err := libopustooling.ResolveLibopusFixedQEXTReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	helper, err := opusEncodeFixedQEXTHelper.Path(buildOpusEncodeFixedQEXTHelper)
	if err != nil {
		HelperUnavailable(t, "paired fixed-QEXT public encoder", err)
		return
	}
	archive := FixedQEXTRefPath(".libs", "libopus.a")
	if err := libopustooling.ValidateLibopusReferenceArchive(archive, variant, libopustooling.DefaultVersion); err != nil {
		HelperUnavailable(t, "paired fixed-QEXT archive", err)
		return
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(filepath.Dir(filepath.Dir(archive))); got != "opus-"+libopustooling.DefaultVersion+suffix {
		t.Fatalf("fixed-QEXT archive tree=%q want suffix %q", got, suffix)
	}
	if !strings.Contains(filepath.Base(helper), "_"+string(variant)+"_") {
		t.Fatalf("fixed-QEXT helper %q does not identify %s", helper, variant)
	}
}
