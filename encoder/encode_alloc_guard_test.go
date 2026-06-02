package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// encodeAllocGuardSine builds a steady tone the encoder can drive through any
// mode without buffering stalls.
func encodeAllocGuardSine(samples int) []float32 {
	pcm := make([]float32, samples)
	for i := range pcm {
		pcm[i] = float32(0.45 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	return pcm
}

type encodeAllocGuardCase struct {
	name      string
	mode      Mode
	bandwidth types.Bandwidth
	channels  int
	frameSize int
	bitrate   int
	voip      bool
}

// encodeAllocGuardCases exercises the steady-state Encode hot path across
// mono+stereo, CELT/SILK/Hybrid, and single-frame + long/multi-frame packets.
// Stereo SILK in particular drives the dual mid/side encoder packet assembly,
// which previously copied its result slice on every call.
var encodeAllocGuardCases = []encodeAllocGuardCase{
	{"CELT-mono-20ms", ModeCELT, types.BandwidthFullband, 1, 960, 128000, false},
	{"CELT-stereo-20ms", ModeCELT, types.BandwidthFullband, 2, 960, 128000, false},
	{"CELT-mono-120ms", ModeCELT, types.BandwidthFullband, 1, 5760, 128000, false},
	{"CELT-stereo-120ms", ModeCELT, types.BandwidthFullband, 2, 5760, 128000, false},
	{"SILK-mono-20ms", ModeSILK, types.BandwidthWideband, 1, 960, 24000, true},
	{"SILK-stereo-20ms", ModeSILK, types.BandwidthWideband, 2, 960, 32000, true},
	{"SILK-mono-60ms", ModeSILK, types.BandwidthWideband, 1, 2880, 24000, true},
	{"SILK-stereo-60ms", ModeSILK, types.BandwidthWideband, 2, 2880, 32000, true},
	{"SILK-mono-120ms", ModeSILK, types.BandwidthWideband, 1, 5760, 24000, true},
	{"Hybrid-mono-20ms", ModeHybrid, types.BandwidthFullband, 1, 960, 64000, false},
	{"Hybrid-stereo-20ms", ModeHybrid, types.BandwidthFullband, 2, 960, 96000, false},
	{"Hybrid-mono-120ms", ModeHybrid, types.BandwidthFullband, 1, 5760, 64000, false},
	{"Hybrid-stereo-120ms", ModeHybrid, types.BandwidthFullband, 2, 5760, 96000, false},
}

// TestEncodeHotPathAllocs locks the steady-state per-call allocation count of
// the internal Encode hot path. The default (float) build is strictly
// zero-alloc across every case. The gated fixed-point build's integer CELT
// encode driver is likewise zero-alloc (encoder-owned scratch threaded through
// the whole frame), while the integer SILK encode bodies retain a bounded
// per-frame footprint; the budget is therefore a per-case ceiling supplied by
// encodeHotPathCaseBudget so the CELT cases are guarded at strict zero and the
// SILK/Hybrid cases catch regressions against their measured baseline.
func TestEncodeHotPathAllocs(t *testing.T) {
	for _, c := range encodeAllocGuardCases {
		t.Run(c.name, func(t *testing.T) {
			e := NewEncoder(48000, c.channels)
			e.SetMode(c.mode)
			e.SetBandwidth(c.bandwidth)
			e.SetBitrate(c.bitrate)
			if c.voip {
				e.SetVoIPApplication(true)
			}
			pcm := encodeAllocGuardSine(c.frameSize * c.channels)

			for i := 0; i < 8; i++ {
				if _, err := e.EncodeFloat32WithAnalysisMaxBytes(pcm, c.frameSize, pcm, 4000); err != nil {
					t.Fatalf("warmup Encode: %v", err)
				}
			}

			budget := encodeHotPathCaseBudget(c)
			allocs := testing.AllocsPerRun(100, func() {
				if _, err := e.EncodeFloat32WithAnalysisMaxBytes(pcm, c.frameSize, pcm, 4000); err != nil {
					t.Fatalf("Encode: %v", err)
				}
			})
			if allocs > float64(budget) {
				t.Fatalf("Encode allocs/op = %.2f, want <= %d", allocs, budget)
			}
		})
	}
}
