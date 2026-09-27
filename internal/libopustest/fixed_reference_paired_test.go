//go:build gopus_fixed_point

package libopustest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestFixedArchiveAndHelperUsePairedReference(t *testing.T) {
	RequireOracle(t)
	variant, err := libopustooling.ResolveLibopusFixedReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	helper, err := getOpusEncodeFixedHelperPath()
	if err != nil {
		HelperUnavailable(t, "paired fixed encoder", err)
		return
	}
	if !strings.Contains(filepath.Base(helper), "_"+string(variant)+"_") {
		t.Fatalf("fixed helper %q does not identify %s", helper, variant)
	}
	archive := FixedRefPath(".libs", "libopus.a")
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(filepath.Dir(filepath.Dir(archive))); got != "opus-"+libopustooling.DefaultVersion+suffix {
		t.Fatalf("fixed archive tree=%q", got)
	}
	if err := libopustooling.ValidateLibopusReferenceArchive(archive, variant, libopustooling.DefaultVersion); err != nil {
		t.Fatal(err)
	}
	other := libopustooling.LibopusReferenceFixedScalar
	if variant == other {
		other = libopustooling.LibopusReferenceFixedSIMD
	}
	if err := libopustooling.ValidateLibopusReferenceArchive(archive, other, libopustooling.DefaultVersion); err == nil {
		t.Fatal("accepted opposite fixed ISA")
	}
}
