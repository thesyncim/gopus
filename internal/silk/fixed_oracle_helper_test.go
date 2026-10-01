//go:build gopus_fixed_point

package silk

import (
	"github.com/thesyncim/gopus/internal/libopustest"
	"sync"
)

// fixed_oracle_helper_test.go centralizes the FIXED_POINT libopus oracle build
// shared by every internal/silk *_fixedpoint_libopus_parity_test.go file. Each
// SILK fixed-point kernel has a matching tools/csrc/libopus_silk_*_info.c probe
// that is compiled once and linked against the pinned --enable-fixed-point
// libopus reference tree (built on demand via tools/ensure_libopus.sh). The
// per-kernel test files supply only their probe source name plus a unique
// binary slug and decode the oracle's wire output themselves.

var (
	fixedOracleMu   sync.Mutex
	fixedOracleBins = map[string]fixedOracleResult{}
)

type fixedOracleResult struct {
	bin string
	err error
}

// buildFixedSILKOracle compiles tools/csrc/<srcName> against the FIXED_POINT
// libopus reference and returns the cached helper binary path. srcName is the
// probe source basename (for example "libopus_silk_fixed_schur_info.c") and
// binSlug is a filesystem-safe name for the produced binary (for example
// "schur"); results are memoized per binSlug so each probe is built at most
// once per test process.
func buildFixedSILKOracle(srcName, binSlug string) (string, error) {
	fixedOracleMu.Lock()
	defer fixedOracleMu.Unlock()
	if got, ok := fixedOracleBins[binSlug]; ok {
		return got.bin, got.err
	}
	bin, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "silk fixed " + binSlug,
		OutputBase:  "gopus_silk_fixed_" + binSlug,
		SourceFile:  srcName,
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "silk/fixed"},
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
	})
	fixedOracleBins[binSlug] = fixedOracleResult{bin: bin, err: err}
	return bin, err
}
