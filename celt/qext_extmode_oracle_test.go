//go:build gopus_qext

package celt

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusQEXTExtModeHelper libopustest.HelperCache

func buildLibopusQEXTExtModeHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext extmode",
		OutputBase:  "gopus_libopus_celt_qext_extmode",
		SourceFile:  "libopus_celt_qext_extmode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// qextExtModeOracle is the libopus compute_qext_mode() result plus the
// precomputed qext_cache PulseCache for the native 96 kHz mode.
type qextExtModeOracle struct {
	BaseFs, BaseShortMdctSize int
	NbEBands, EffEBands       int
	EBands, LogN              []int16
	CacheSize                 int
	CacheIndex                []int16
	CacheBits                 []uint8
	CacheCaps                 []uint8
}

func probeLibopusQEXTExtMode(t *testing.T) qextExtModeOracle {
	t.Helper()
	binPath, err := libopusQEXTExtModeHelper.Path(buildLibopusQEXTExtModeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext extmode", err)
	}
	payload := libopustest.NewOraclePayload("GQEI", 1)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext extmode", "GQEO")
	if err != nil {
		t.Fatalf("run qext extmode oracle: %v", err)
	}
	var o qextExtModeOracle
	o.BaseFs = int(reader.U32())
	o.BaseShortMdctSize = int(reader.U32())
	o.NbEBands = int(reader.U32())
	o.EffEBands = int(reader.U32())
	o.EBands = make([]int16, int(reader.U32()))
	for i := range o.EBands {
		o.EBands[i] = reader.I16()
	}
	o.LogN = make([]int16, int(reader.U32()))
	for i := range o.LogN {
		o.LogN[i] = reader.I16()
	}
	o.CacheSize = int(reader.U32())
	o.CacheIndex = make([]int16, int(reader.U32()))
	for i := range o.CacheIndex {
		o.CacheIndex[i] = reader.I16()
	}
	o.CacheBits = reader.Bytes(int(reader.U32()))
	o.CacheCaps = reader.Bytes(int(reader.U32()))
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext extmode oracle payload not fully consumed: %v", err)
	}
	return o
}

// TestComputeQEXTModeConfigMatchesLibopusOracle verifies that gopus's
// computeQEXTModeConfig() for the native 96 kHz mode reproduces libopus's
// compute_qext_mode() output (extension eBands, logN, nbEBands, effEBands)
// and the precomputed qext_cache PulseCache (index/bits/caps) exactly. These
// integer tables are platform-independent, so any mismatch fails on all archs.
func TestComputeQEXTModeConfigMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	o := probeLibopusQEXTExtMode(t)

	if o.BaseFs != 96000 || o.BaseShortMdctSize != 240 {
		t.Fatalf("unexpected base mode from oracle: Fs=%d shortMdctSize=%d", o.BaseFs, o.BaseShortMdctSize)
	}

	cfg, ok := computeQEXTModeConfig(o.BaseFs, o.BaseShortMdctSize)
	if !ok {
		t.Fatalf("computeQEXTModeConfig(%d,%d)=false want true", o.BaseFs, o.BaseShortMdctSize)
	}

	if got := nbQEXTBands; got != o.NbEBands {
		t.Fatalf("nbEBands: gopus NB_QEXT_BANDS=%d oracle=%d", got, o.NbEBands)
	}
	if cfg.EffBands != o.EffEBands {
		t.Fatalf("effEBands: gopus=%d oracle=%d", cfg.EffBands, o.EffEBands)
	}

	// The C eBands table has nbEBands+1 edges; gopus stores the full 15-entry
	// source table (qextEBands240). Both must agree edge-for-edge.
	if len(cfg.EBands) != len(o.EBands) {
		t.Fatalf("len(eBands): gopus=%d oracle=%d", len(cfg.EBands), len(o.EBands))
	}
	for i := range o.EBands {
		if int16(cfg.EBands[i]) != o.EBands[i] {
			t.Fatalf("eBands[%d]: gopus=%d oracle=%d", i, cfg.EBands[i], o.EBands[i])
		}
	}

	if len(cfg.LogN) != len(o.LogN) {
		t.Fatalf("len(logN): gopus=%d oracle=%d", len(cfg.LogN), len(o.LogN))
	}
	for i := range o.LogN {
		if int16(cfg.LogN[i]) != o.LogN[i] {
			t.Fatalf("logN[%d]: gopus=%d oracle=%d", i, cfg.LogN[i], o.LogN[i])
		}
	}

	// The precomputed qext_cache PulseCache.
	if len(cfg.CacheIndex) != len(o.CacheIndex) {
		t.Fatalf("len(cache.index): gopus=%d oracle=%d", len(cfg.CacheIndex), len(o.CacheIndex))
	}
	for i := range o.CacheIndex {
		if cfg.CacheIndex[i] != o.CacheIndex[i] {
			t.Fatalf("cache.index[%d]: gopus=%d oracle=%d", i, cfg.CacheIndex[i], o.CacheIndex[i])
		}
	}

	if len(cfg.CacheBits) != o.CacheSize {
		t.Fatalf("len(cache.bits): gopus=%d oracle cache.size=%d", len(cfg.CacheBits), o.CacheSize)
	}
	if len(cfg.CacheBits) != len(o.CacheBits) {
		t.Fatalf("len(cache.bits): gopus=%d oracle=%d", len(cfg.CacheBits), len(o.CacheBits))
	}
	for i := range o.CacheBits {
		if cfg.CacheBits[i] != o.CacheBits[i] {
			t.Fatalf("cache.bits[%d]: gopus=%d oracle=%d", i, cfg.CacheBits[i], o.CacheBits[i])
		}
	}

	if len(cfg.CacheCaps) != len(o.CacheCaps) {
		t.Fatalf("len(cache.caps): gopus=%d oracle=%d", len(cfg.CacheCaps), len(o.CacheCaps))
	}
	for i := range o.CacheCaps {
		if cfg.CacheCaps[i] != o.CacheCaps[i] {
			t.Fatalf("cache.caps[%d]: gopus=%d oracle=%d", i, cfg.CacheCaps[i], o.CacheCaps[i])
		}
	}
}
