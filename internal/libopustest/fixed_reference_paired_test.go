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
	identity, err := ResolvePublicAPIReferenceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	variant := identity.Variant
	if !strings.Contains(string(variant), "fixed") {
		t.Fatalf("selected public reference variant %q is not fixed-point", variant)
	}
	helper, err := getOpusEncodeFixedHelperPath()
	if err != nil {
		HelperUnavailable(t, "paired fixed encoder", err)
		return
	}
	if !strings.Contains(filepath.Base(helper), "_"+string(variant)+"_") {
		t.Fatalf("fixed helper %q does not identify %s", helper, variant)
	}
	archive := identity.ArchivePath
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
	var other libopustooling.LibopusReferenceVariant
	if strings.HasSuffix(string(variant), "-scalar") {
		other = libopustooling.LibopusReferenceVariant(strings.TrimSuffix(string(variant), "-scalar") + "-simd")
	} else if strings.HasSuffix(string(variant), "-simd") {
		other = libopustooling.LibopusReferenceVariant(strings.TrimSuffix(string(variant), "-simd") + "-scalar")
	} else {
		t.Fatalf("selected fixed variant %q has no instruction lane", variant)
	}
	if err := libopustooling.ValidateLibopusReferenceArchive(archive, other, libopustooling.DefaultVersion); err == nil {
		t.Fatal("accepted opposite fixed ISA")
	}
}
