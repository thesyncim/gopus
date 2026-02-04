//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestPitchLagsTraceFullEncodeAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	cfg := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := cfg.SubframeSamples
	frameSamples := numSubfr * subfrLen
	frames := 20

	trace := &EncoderTrace{
		Pitch: &PitchTrace{
			CaptureResidual:  true,
			CapturePitchLags: true,
		},
	}
	enc.trace = trace
	enc.SetVADState(200, 0, [4]int{})

	signal := generatePitchTraceSignal(frames*frameSamples, cfg.SampleRate)

	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		_ = enc.EncodeFrame(pcm, nil, true)
		pt := trace.Pitch
		if pt == nil {
			t.Fatalf("missing pitch trace at frame %d", frame)
		}
		if pt.ResidualLen == 0 || len(pt.Residual) == 0 {
			t.Fatalf("missing residual capture at frame %d", frame)
		}
		if pt.XBufLen <= 0 {
			t.Fatalf("invalid x buffer length at frame %d", frame)
		}
		if pt.XBufLen > len(enc.inputBuffer) {
			t.Fatalf("trace x buffer length %d exceeds input buffer %d", pt.XBufLen, len(enc.inputBuffer))
		}
		xBufScaled := make([]float32, pt.XBufLen)
		for i := 0; i < pt.XBufLen; i++ {
			xBufScaled[i] = enc.inputBuffer[i] * silkSampleScale
		}
		if got := hashFloat32Slice(xBufScaled); got != pt.XBufHash {
			t.Fatalf("frame %d: xBufHash mismatch go=%d calc=%d", frame, pt.XBufHash, got)
		}
		if pt.LtpMemLen >= len(xBufScaled) {
			t.Fatalf("frame %d: LTP mem length %d exceeds buffer %d", frame, pt.LtpMemLen, len(xBufScaled))
		}
		xFrame := xBufScaled[pt.LtpMemLen:]
		if pt.FrameSamples+pt.LaPitch > len(xFrame) {
			t.Fatalf("frame %d: xFrame too short (%d) for frame+laPitch (%d)", frame, len(xFrame), pt.FrameSamples+pt.LaPitch)
		}

		lib := LibopusFindPitchLagsTrace(
			xFrame,
			pt.FrameSamples,
			pt.FsKHz,
			pt.LtpMemLen,
			pt.LaPitch,
			pt.PitchWinLen,
			pt.LPCOrder,
			pt.NbSubfr,
			pt.SubfrLen,
			enc.pitchEstimationThresholdQ16,
			pt.Complexity,
			pt.PrevSignal,
			pt.SpeechQ8,
			pt.InputTiltQ15,
			pt.PrevLag,
			pt.SignalType,
			pt.FirstFrameAfterReset,
		)

		if lib.BufLen != pt.BufLen {
			t.Fatalf("frame %d: bufLen go=%d lib=%d", frame, pt.BufLen, lib.BufLen)
		}
		if diff := math.Abs(lib.Thrhld - pt.Thrhld); diff > 1e-6 {
			t.Fatalf("frame %d: thrhld go=%.6f lib=%.6f", frame, pt.Thrhld, lib.Thrhld)
		}
		if diff := math.Abs(lib.PredGain - pt.PredGain); diff > 1e-2 {
			t.Fatalf("frame %d: predGain go=%.6f lib=%.6f", frame, pt.PredGain, lib.PredGain)
		}

		libResHash := hashFloat32Slice(lib.Residual)
		if len(pt.Residual) < len(lib.Residual) {
			t.Fatalf("frame %d: residual length go=%d lib=%d", frame, len(pt.Residual), len(lib.Residual))
		}
		maxDiff := 0.0
		maxIdx := -1
		for i := 0; i < len(lib.Residual); i++ {
			diff := math.Abs(float64(pt.Residual[i] - lib.Residual[i]))
			if diff > maxDiff {
				maxDiff = diff
				maxIdx = i
			}
		}
		if maxDiff > 2e-3 {
			t.Fatalf("frame %d: residual[%d] max diff=%.6f go=%.6f lib=%.6f", frame, maxIdx, maxDiff, pt.Residual[maxIdx], lib.Residual[maxIdx])
		}
		if libResHash != pt.ResidualHash {
			t.Logf("frame %d: residual hash mismatch go=%d lib=%d maxDiff=%.6f", frame, pt.ResidualHash, libResHash, maxDiff)
		}

		if pt.CapturePitchLags {
			if len(pt.PitchLags) != len(lib.Pitch) {
				t.Fatalf("frame %d: pitch lag len go=%d lib=%d", frame, len(pt.PitchLags), len(lib.Pitch))
			}
			for i := 0; i < len(lib.Pitch); i++ {
				if pt.PitchLags[i] != lib.Pitch[i] {
					t.Fatalf("frame %d: pitch lag[%d] go=%d lib=%d", frame, i, pt.PitchLags[i], lib.Pitch[i])
				}
			}
			if pt.LagIndex != lib.LagIndex {
				t.Fatalf("frame %d: lagIndex go=%d lib=%d", frame, pt.LagIndex, lib.LagIndex)
			}
			if pt.Contour != lib.Contour {
				t.Fatalf("frame %d: contour go=%d lib=%d", frame, pt.Contour, lib.Contour)
			}
			if diff := math.Abs(float64(pt.LTPCorr - lib.LTPCorr)); diff > 5e-4 {
				t.Fatalf("frame %d: ltpCorr go=%.6f lib=%.6f", frame, pt.LTPCorr, lib.LTPCorr)
			}
		}
	}
}
