//go:build gopus_qext

package libopustest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestQEXTArchiveAndHelperUsePairedReference(t *testing.T) {
	RequireOracle(t)
	variant, err := libopustooling.ResolveLibopusQEXTReferenceVariant()
	if err != nil {
		t.Fatal(err)
	}
	suffix, err := libopustooling.LibopusReferenceSourceSuffix(variant)
	if err != nil {
		t.Fatal(err)
	}
	archive := QEXTRefPath(".libs", "libopus.a")
	if got := filepath.Base(filepath.Dir(filepath.Dir(archive))); got != "opus-"+libopustooling.DefaultVersion+suffix {
		t.Fatalf("QEXT archive tree=%q want %q", got, "opus-"+libopustooling.DefaultVersion+suffix)
	}
	if err := libopustooling.ValidateLibopusReferenceArchive(archive, variant, libopustooling.DefaultVersion); err != nil {
		HelperUnavailable(t, "paired QEXT archive", err)
		return
	}
	helper, err := getQEXTEncode96kHelperPath()
	if err != nil {
		HelperUnavailable(t, "paired QEXT encoder", err)
		return
	}
	if !strings.Contains(filepath.Base(helper), "_"+string(variant)+"_") {
		t.Fatalf("QEXT helper %q does not identify selected %s variant", helper, variant)
	}
}
