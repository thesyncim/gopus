//go:build cgo_libopus

package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func generateEncoderTraceSignal(samples int, sampleRate int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	for i := 0; i < samples; i++ {
		tm := float64(i) / float64(sampleRate)
		val := 0.0
		for _, f := range freqs {
			val += amp * math.Sin(2*math.Pi*f*tm)
		}
		signal[i] = float32(val)
	}
	return signal
}

// TestPitchLagsFullEncodeTraceAgainstLibopus48k compares full-encode pitch lags against libopus
// using the same multi-sine input as TestSILKParamTraceAgainstLibopus (48 kHz, mono).
func TestPitchLagsFullEncodeTraceAgainstLibopus48k(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	frames := sampleRate / frameSize
	signal := generateEncoderTraceSignal(frames*frameSize*channels, sampleRate)

	enc := NewEncoder(sampleRate, channels)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthWideband)
	enc.SetBitrate(bitrate)
	enc.SetComplexity(10)
	enc.ensureSILKEncoder()

	trace := &silk.EncoderTrace{
		Pitch: &silk.PitchTrace{
			CaptureResidual:  true,
			CapturePitchLags: true,
		},
	}
	enc.silkEncoder.SetTrace(trace)

	var mismatchFrames []int
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSize * channels
		end := start + frameSize*channels
		pcm32 := signal[start:end]
		pcm64 := make([]float64, len(pcm32))
		for i, v := range pcm32 {
			pcm64[i] = float64(v)
		}

		if _, err := enc.Encode(pcm64, frameSize); err != nil {
			t.Fatalf("encode failed at frame %d: %v", frame, err)
		}

		pt := trace.Pitch
		if pt == nil {
			t.Fatalf("missing pitch trace at frame %d", frame)
		}
		if pt.XBufLen == 0 || pt.FrameSamples == 0 {
			t.Fatalf("invalid pitch trace at frame %d", frame)
		}

		buf := enc.silkEncoder.InputBuffer()
		if pt.XBufLen > len(buf) {
			t.Fatalf("frame %d: xBufLen %d exceeds buffer %d", frame, pt.XBufLen, len(buf))
		}
		xBufScaled := make([]float32, pt.XBufLen)
		for i := 0; i < pt.XBufLen; i++ {
			xBufScaled[i] = buf[i] * float32(silk.SilkSampleScale)
		}
		if pt.LtpMemLen >= len(xBufScaled) {
			t.Fatalf("frame %d: LTP mem length %d exceeds buffer %d", frame, pt.LtpMemLen, len(xBufScaled))
		}
		xFrame := xBufScaled[pt.LtpMemLen:]
		if pt.FrameSamples+pt.LaPitch > len(xFrame) {
			t.Fatalf("frame %d: xFrame too short (%d) for frame+laPitch (%d)", frame, len(xFrame), pt.FrameSamples+pt.LaPitch)
		}

		lib := silk.LibopusFindPitchLagsTrace(
			xFrame,
			pt.FrameSamples,
			pt.FsKHz,
			pt.LtpMemLen,
			pt.LaPitch,
			pt.PitchWinLen,
			pt.LPCOrder,
			pt.NbSubfr,
			pt.SubfrLen,
			int32(pt.PitchEstThresholdQ16),
			pt.Complexity,
			pt.PrevSignal,
			pt.SpeechQ8,
			pt.InputTiltQ15,
			pt.PrevLag,
			pt.SignalType,
			pt.FirstFrameAfterReset,
		)

		mismatch := false
		lagMismatch := false
		contourMismatch := false
		pitchLagMismatch := false

		if pt.LagIndex != lib.LagIndex {
			lagMismatch = true
		}
		if pt.Contour != lib.Contour {
			contourMismatch = true
		}
		if len(pt.PitchLags) != len(lib.Pitch) {
			pitchLagMismatch = true
		} else {
			for i := 0; i < len(lib.Pitch); i++ {
				if pt.PitchLags[i] != lib.Pitch[i] {
					pitchLagMismatch = true
					break
				}
			}
		}
		mismatch = lagMismatch || contourMismatch || pitchLagMismatch

		if mismatch {
			mismatchFrames = append(mismatchFrames, frame)
			t.Logf("frame %d: mismatch lag=%v contour=%v pitchLags=%v goLag=%d libLag=%d goContour=%d libContour=%d",
				frame, lagMismatch, contourMismatch, pitchLagMismatch, pt.LagIndex, lib.LagIndex, pt.Contour, lib.Contour)
			t.Logf("  pitchLags go=%v lib=%v", pt.PitchLags, lib.Pitch)
			t.Logf("  inputs: speechQ8=%d inputTiltQ15=%d prevSignal=%d prevLag=%d signalType=%d firstFrame=%v",
				pt.SpeechQ8, pt.InputTiltQ15, pt.PrevSignal, pt.PrevLag, pt.SignalType, pt.FirstFrameAfterReset)
			t.Logf("  thresholds: searchThres1=%.6f thrhld=%.6f thrhldClamped=%.6f pitchEstQ16=%d",
				pt.SearchThres1, pt.Thrhld, pt.ThrhldClamped, pt.PitchEstThresholdQ16)
			t.Logf("  ltpCorr=%.6f predGain=%.6f", pt.LTPCorr, pt.PredGain)

			if len(lib.Residual) > 0 && len(pt.Residual) >= len(lib.Residual) {
				maxDiff := 0.0
				maxIdx := -1
				for i := 0; i < len(lib.Residual); i++ {
					diff := math.Abs(float64(pt.Residual[i] - lib.Residual[i]))
					if diff > maxDiff {
						maxDiff = diff
						maxIdx = i
					}
				}
				t.Logf("  residual max diff=%.6f at %d (go=%.6f lib=%.6f)",
					maxDiff, maxIdx, pt.Residual[maxIdx], lib.Residual[maxIdx])
			}
		}
	}

	if len(mismatchFrames) > 0 {
		t.Logf("mismatch frames: %v", mismatchFrames)
	}
}
