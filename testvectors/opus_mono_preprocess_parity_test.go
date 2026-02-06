//go:build cgo_libopus

package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
)

func floatToInt16RoundLocal(x float32) int16 {
	if x > 32767 {
		return 32767
	}
	if x < -32768 {
		return -32768
	}
	return int16(math.RoundToEven(float64(x)))
}

func TestOpusMonoPreprocessParityAgainstLibopus(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize
	original := generateEncoderTestSignal(totalSamples, channels)

	// Match top-level encoder DC reject state.
	coef := float32(6.3) * float32(3) / float32(sampleRate)
	coef2 := float32(1.0) - coef
	const verySmall = float32(1e-30)
	var hpMem float32

	// Match Opus mono input handoff via inputBuf+1 (sStereo.sMid).
	var hist [2]int16

	resampler := silk.NewDownsamplingResampler(48000, 16000)
	resampled := make([]float32, frameSize/3)
	in16 := make([]int16, frameSize/3)
	aligned := make([]int16, frameSize/3)

	for frame := 0; frame < numFrames; frame++ {
		start := frame * frameSize
		end := start + frameSize

		// dc_reject on 48 kHz mono input.
		inFrame := original[start:end]
		dcOut := make([]float32, len(inFrame))
		for i := 0; i < len(inFrame); i++ {
			x := inFrame[i]
			y := x - hpMem
			hpMem = coef*x + verySmall + coef2*hpMem
			dcOut[i] = y
		}

		nOut := resampler.ProcessInto(dcOut, resampled)
		if nOut != len(resampled) {
			t.Fatalf("frame %d: unexpected resampler out len %d", frame, nOut)
		}
		for i := 0; i < nOut; i++ {
			in16[i] = floatToInt16RoundLocal(resampled[i] * 32768.0)
		}

		aligned[0] = hist[1]
		copy(aligned[1:], in16[:nOut-1])
		hist[0] = in16[nOut-2]
		hist[1] = in16[nOut-1]

		snap, ok := captureLibopusOpusSilkState(original, sampleRate, channels, bitrate, frameSize, frame)
		if !ok {
			t.Fatalf("frame %d: failed to capture libopus state", frame)
		}
		if snap.FrameLength != nOut {
			t.Fatalf("frame %d: frame length mismatch go=%d lib=%d", frame, nOut, snap.FrameLength)
		}
		if len(snap.InputBufQ0) < snap.FrameLength+1 {
			t.Fatalf("frame %d: lib input buffer too short: %d", frame, len(snap.InputBufQ0))
		}

		for i := 0; i < nOut; i++ {
			libVal := snap.InputBufQ0[i+1]
			if aligned[i] != libVal {
				t.Fatalf("frame %d sample %d mismatch: go=%d lib=%d", frame, i, aligned[i], libVal)
			}
		}
	}
}
