// Package cgo compares resampler configuration between gopus and libopus.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/silk"
)

// TestResamplerConfigComparison compares resampler configs for NB, MB, WB.
func TestResamplerConfigComparison(t *testing.T) {
	// Test NB (8→48kHz)
	nbResampler := silk.NewLibopusResampler(8000, 48000)
	t.Logf("NB Resampler (8→48kHz):")
	t.Logf("  inputDelay: %d", nbResampler.InputDelay())
	t.Logf("  invRatioQ16: %d", nbResampler.InvRatioQ16())
	t.Logf("  fsInKHz: %d", nbResampler.FsInKHz())
	t.Logf("  fsOutKHz: %d", nbResampler.FsOutKHz())
	t.Logf("  batchSize: %d", nbResampler.BatchSize())

	// Expected for 8→48kHz:
	// inputDelay = delay_matrix_dec[0][4] = 0
	// invRatioQ16 = ((8000 << (14+1)) / 48000) << 2 = ((8000 << 15) / 48000) << 2
	// = (262144000 / 48000) << 2 = 5461 << 2 = 21844
	t.Logf("  Expected inputDelay: 0")
	t.Logf("  Expected invRatioQ16: ~21844 (may be rounded)")

	// Test MB (12→48kHz)
	mbResampler := silk.NewLibopusResampler(12000, 48000)
	t.Logf("\nMB Resampler (12→48kHz):")
	t.Logf("  inputDelay: %d", mbResampler.InputDelay())
	t.Logf("  invRatioQ16: %d", mbResampler.InvRatioQ16())
	t.Logf("  fsInKHz: %d", mbResampler.FsInKHz())
	t.Logf("  fsOutKHz: %d", mbResampler.FsOutKHz())
	t.Logf("  batchSize: %d", mbResampler.BatchSize())

	// Expected for 12→48kHz:
	// inputDelay = delay_matrix_dec[1][4] = 4
	// invRatioQ16 = ((12000 << (14+1)) / 48000) << 2
	// = ((12000 << 15) / 48000) << 2 = (393216000 / 48000) << 2 = 8192 << 2 = 32768
	t.Logf("  Expected inputDelay: 4")
	t.Logf("  Expected invRatioQ16: ~32768 (may be rounded)")

	// Test WB (16→48kHz)
	wbResampler := silk.NewLibopusResampler(16000, 48000)
	t.Logf("\nWB Resampler (16→48kHz):")
	t.Logf("  inputDelay: %d", wbResampler.InputDelay())
	t.Logf("  invRatioQ16: %d", wbResampler.InvRatioQ16())
	t.Logf("  fsInKHz: %d", wbResampler.FsInKHz())
	t.Logf("  fsOutKHz: %d", wbResampler.FsOutKHz())
	t.Logf("  batchSize: %d", wbResampler.BatchSize())

	// Expected for 16→48kHz:
	// inputDelay = delay_matrix_dec[2][4] = 7
	// invRatioQ16 = ((16000 << (14+1)) / 48000) << 2
	// = ((16000 << 15) / 48000) << 2 = (524288000 / 48000) << 2 = 10922 << 2 = 43688
	t.Logf("  Expected inputDelay: 7")
	t.Logf("  Expected invRatioQ16: ~43688 (may be rounded)")

	// Verify inputDelay values
	if nbResampler.InputDelay() != 0 {
		t.Errorf("NB inputDelay: got %d, want 0", nbResampler.InputDelay())
	}
	if mbResampler.InputDelay() != 4 {
		t.Errorf("MB inputDelay: got %d, want 4", mbResampler.InputDelay())
	}
	if wbResampler.InputDelay() != 7 {
		t.Errorf("WB inputDelay: got %d, want 7", wbResampler.InputDelay())
	}
}
