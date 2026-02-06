package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msTraceNSQ traces NSQ input and output to understand the 10ms quality gap.
func TestSILK10msTraceNSQ(t *testing.T) {
	for _, frameSize := range []int{480, 960} {
		fsName := "10ms"
		if frameSize == 960 {
			fsName = "20ms"
		}
		t.Run(fsName, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
			enc.SetBitrate(32000)
			enc.SetComplexity(0) // Use simple NSQ for easier debugging

			enc.ensureSILKEncoder()

			for i := 0; i < 20; i++ {
				pcm := make([]float64, frameSize)
				for j := range pcm {
					sampleIdx := i*frameSize + j
					tm := float64(sampleIdx) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					pcm[j] = 0.5 * math.Sin(phase)
				}

				trace := &silk.EncoderTrace{}
				trace.Frame = &silk.FrameStateTrace{}
				trace.FramePre = &silk.FrameStateTrace{}
				trace.GainLoop = &silk.GainLoopTrace{}
				trace.NSQ = &silk.NSQTrace{CaptureInputs: true}
				enc.silkEncoder.SetTrace(trace)

				pkt, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}

				// Only log frames 5-10 (after warmup)
				if i >= 5 && i <= 10 {
					fr := trace.Frame
					nsq := trace.NSQ
					t.Logf("Frame %d:", i)
					if pkt != nil {
						t.Logf("  pktLen=%d TOC=0x%02x", len(pkt), pkt[0])
					}
					t.Logf("  SignalType=%d", fr.SignalType)
					t.Logf("  Gains: [%d,%d,%d,%d]", fr.GainIndices[0], fr.GainIndices[1], fr.GainIndices[2], fr.GainIndices[3])
					t.Logf("  PitchL: [%d,%d,%d,%d]", fr.PitchL[0], fr.PitchL[1], fr.PitchL[2], fr.PitchL[3])
					t.Logf("  LTPCorr=%.4f PrevLag=%d", fr.LTPCorr, fr.PrevLag)
					t.Logf("  NSQ: frameLen=%d subfrLen=%d nbSubfr=%d lpcOrder=%d seed=%d",
						nsq.FrameLength, nsq.SubfrLength, nsq.NbSubfr, nsq.PredLPCOrder, nsq.SeedIn)
					t.Logf("  NSQ: signalType=%d quantOffset=%d lambdaQ10=%d warpingQ16=%d",
						nsq.SignalType, nsq.QuantOffsetType, nsq.LambdaQ10, nsq.WarpingQ16)
					t.Logf("  NSQ: NStatesDD=%d interpQ2=%d ltpScaleQ14=%d",
						nsq.NStatesDelayedDecision, nsq.NLSFInterpCoefQ2, nsq.LTPScaleQ14)

					// Compute input and output energy
					var inputE float64
					for _, v := range nsq.InputQ0 {
						inputE += float64(v) * float64(v)
					}
					inputRMS := math.Sqrt(inputE / float64(len(nsq.InputQ0)))

					// Show gains
					for sf := 0; sf < nsq.NbSubfr && sf < len(nsq.GainsQ16); sf++ {
						t.Logf("  NSQ subframe %d: gainQ16=%d pitchL=%d",
							sf, nsq.GainsQ16[sf], nsq.PitchL[sf])
					}
					t.Logf("  NSQ inputRMS=%.1f pulsesLen=%d pulsesHash=%d xqHash=%d",
						inputRMS, nsq.PulsesLen, nsq.PulsesHash, nsq.XqHash)

					// Show first LPC coefficients
					if len(nsq.PredCoefQ12) >= 16 {
						t.Logf("  LPC[0:4]: [%d,%d,%d,%d]",
							nsq.PredCoefQ12[0], nsq.PredCoefQ12[1], nsq.PredCoefQ12[2], nsq.PredCoefQ12[3])
						t.Logf("  LPC[16:20]: [%d,%d,%d,%d]",
							nsq.PredCoefQ12[16], nsq.PredCoefQ12[17], nsq.PredCoefQ12[18], nsq.PredCoefQ12[19])
					}
				}

				enc.silkEncoder.SetTrace(nil)
			}
		})
	}
}
