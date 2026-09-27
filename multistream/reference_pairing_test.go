package multistream

import (
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// pairMultistreamReference pairs a public multistream oracle with the same
// compile-time codec features and instruction lane as the Go package.
func pairMultistreamReference(cfg libopustest.CHelperConfig) libopustest.CHelperConfig {
	cfg.Libs = append([]string{multistreamReferenceArchive()}, cfg.Libs...)
	multistreamReferenceFlags(&cfg)
	return cfg
}

func TestMultistreamReferenceFeaturePairing(t *testing.T) {
	cfg := pairMultistreamReference(libopustest.CHelperConfig{Libs: []string{"-lm"}})
	if len(cfg.Libs) != 2 || cfg.Libs[0] != multistreamReferenceArchive() || cfg.Libs[1] != "-lm" {
		t.Fatalf("paired helper libraries=%v", cfg.Libs)
	}
	archiveCount := 0
	for _, lib := range cfg.Libs {
		if lib == multistreamReferenceArchive() {
			archiveCount++
		}
	}
	if archiveCount != 1 {
		t.Fatalf("paired helper has %d libopus archives: %v", archiveCount, cfg.Libs)
	}
	if cfg.QEXTRef != multistreamReferenceUsesQEXT() ||
		cfg.DREDQEXTRef != multistreamReferenceUsesDREDQEXT() ||
		cfg.FixedRef != multistreamReferenceUsesFixed() ||
		cfg.FixedQEXTRef != multistreamReferenceUsesFixedQEXT() {
		t.Fatalf("feature selectors QEXT=%t DREDQEXT=%t fixed=%t fixed+QEXT=%t; want QEXT=%t DREDQEXT=%t fixed=%t fixed+QEXT=%t",
			cfg.QEXTRef, cfg.DREDQEXTRef, cfg.FixedRef, cfg.FixedQEXTRef,
			multistreamReferenceUsesQEXT(), multistreamReferenceUsesDREDQEXT(),
			multistreamReferenceUsesFixed(), multistreamReferenceUsesFixedQEXT())
	}
	if multistreamReferenceUsesDREDQEXT() && !slices.Contains(cfg.RefIncludes, "dnn") {
		t.Fatalf("combined DRED+QEXT helper include paths=%v, missing dnn", cfg.RefIncludes)
	}
}
