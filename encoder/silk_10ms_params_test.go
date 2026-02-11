package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msEncoderParams dumps key SILK encoding parameters for 10ms vs 20ms
// to identify what diverges.
func TestSILK10msEncoderParams(t *testing.T) {
	for _, tc := range []struct {
		name      string
		frameSize int
	}{
		{"10ms", 480},
		{"20ms", 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
			enc.SetBitrate(32000)

			enc.ensureSILKEncoder()

			numFrames := 15
			for i := 0; i < numFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					pcm[j] = 0.5 * math.Sin(phase)
				}

				trace := &silk.EncoderTrace{}
				trace.Frame = &silk.FrameStateTrace{}
				trace.FramePre = &silk.FrameStateTrace{}
				trace.GainLoop = &silk.GainLoopTrace{}
				enc.silkEncoder.SetTrace(trace)

				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}

				if i >= 2 && i <= 10 {
					tr := trace.Frame
					pre := trace.FramePre
					gain := trace.GainLoop

					t.Logf("Frame %d:", i)
					if pkt != nil {
						t.Logf("  TOC=0x%02x pktLen=%d", pkt[0], len(pkt))
					}
					t.Logf("  SignalType=%d", tr.SignalType)
					t.Logf("  TargetRate=%d SNR_Q7=%d NBitsExceeded=%d",
						pre.TargetRateBps, pre.SNRDBQ7, pre.NBitsExceeded)
					t.Logf("  Gains: [%d,%d,%d,%d]",
						tr.GainIndices[0], tr.GainIndices[1], tr.GainIndices[2], tr.GainIndices[3])
					t.Logf("  PitchL: [%d,%d,%d,%d]",
						tr.PitchL[0], tr.PitchL[1], tr.PitchL[2], tr.PitchL[3])
					t.Logf("  LTPCorr=%.4f PrevLag=%d", tr.LTPCorr, tr.PrevLag)
					if gain != nil {
						t.Logf("  MaxBits=%d UseCBR=%v NumSubframes=%d SNRDBQ7=%d",
							gain.MaxBits, gain.UseCBR, gain.NumSubframes, gain.SNRDBQ7)
						t.Logf("  QuantOffset: before=%d after=%d", gain.QuantOffsetBefore, gain.QuantOffsetAfter)
						t.Logf("  GainIters=%d SeedOut=%d", len(gain.Iterations), gain.SeedOut)
						for _, it := range gain.Iterations {
							t.Logf("    iter=%d mult=%d bits=%d found(%v,%v)",
								it.Iter, it.GainMultQ8, it.Bits, it.FoundLower, it.FoundUpper)
						}
					}
				}

				enc.silkEncoder.SetTrace(nil)
			}
		})
	}
}
