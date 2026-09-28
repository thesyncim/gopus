//go:build gopus_fixed_point && gopus_osce && !gopus_dred

package libopustest

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestFixedOSCEReferenceRejectsFloatDNNArchive(t *testing.T) {
	for _, build := range []struct {
		name string
		run  func() error
	}{
		{"ensure OSCE", func() error { _, _, err := EnsureOSCEBuild(t.TempDir()); return err }},
		{"ensure DRED", func() error { _, _, err := EnsureDREDBuild(t.TempDir()); return err }},
		{"OSCE helper", func() error { _, err := BuildOSCEHelper(t.TempDir(), "unused.c", "unused", false); return err }},
		{"DRED helper", func() error { _, err := BuildDREDHelper(t.TempDir(), "unused.c", "unused", false); return err }},
		{"public DNN helper", func() error {
			_, err := BuildDNNCHelper(t.TempDir(), CHelperConfig{OutputBase: "unused", SourceFile: "unused.c"})
			return err
		}},
	} {
		t.Run(build.name, func(t *testing.T) {
			err := build.run()
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) || !strings.Contains(err.Error(), "FIXED_POINT with ENABLE_OSCE") && !strings.Contains(err.Error(), "FIXED_POINT with ENABLE_DRED") {
				t.Fatalf("error=%v, want unsupported fixed DNN configuration", err)
			}
		})
	}
}
