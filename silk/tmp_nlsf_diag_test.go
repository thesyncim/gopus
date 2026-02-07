//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func generateTmpEncoderSignal(samples, channels int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}

	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		tm := float64(sampleIdx) / 48000.0

		var val float64
		for fi, freq := range freqs {
			f := freq
			if channels == 2 && ch == 1 {
				f *= 1.01
			}
			modDepth := 0.5 + 0.5*math.Sin(2*math.Pi*modFreqs[fi]*tm)
			val += amp * modDepth * math.Sin(2*math.Pi*f*tm)
		}

		onsetSamples := int(0.010 * 48000)
		if sampleIdx < onsetSamples {
			frac := float64(sampleIdx) / float64(onsetSamples)
			val *= frac * frac * frac
		}
		signal[i] = float32(val)
	}
	return signal
}

func TestTmpNLSFDiag(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		frames     = 50
	)

	enc := NewEncoder(BandwidthWideband)
	enc.SetBitrate(32000)
	nlsfTrace := &NLSFTrace{CaptureLTPRes: true}
	enc.SetTrace(&EncoderTrace{NLSF: nlsfTrace})
	enc.SetVADState(255, 32766, [4]int{32767, 32767, 32767, 32767})

	signal := generateTmpEncoderSignal(frames*frameSize*channels, channels)
	for frame := 0; frame < frames; frame++ {
		start := frame * frameSize * channels
		end := start + frameSize*channels
		_ = enc.EncodeFrame(signal[start:end], nil, true)

		nt := nlsfTrace
		if nt == nil || nt.LTPResLen == 0 || len(nt.LTPRes) < nt.LTPResLen {
			t.Fatalf("frame %d: missing NLSF trace", frame)
		}
		ltpRes := nt.LTPRes[:nt.LTPResLen]
		lib := libopusFindLPCInterpDebug(
			ltpRes,
			nt.NbSubfr,
			nt.SubfrLen,
			nt.LPCOrder,
			nt.UseInterp,
			nt.FirstFrameAfterReset,
			nt.PrevNLSFQ15,
			float32(nt.MinInvGain),
		)
		if nt.InterpIdx != lib.InterpQ2 {
			t.Logf("frame %d mismatch: go=%d lib=%d use=%v first=%v minInv=%.9f",
				frame, nt.InterpIdx, lib.InterpQ2, nt.UseInterp, nt.FirstFrameAfterReset, nt.MinInvGain)
			t.Logf("  go interp energies: k3=%.9f k2=%.9f k1=%.9f k0=%.9f base=%.9f break=%d",
				nt.InterpResNrgQ2[3], nt.InterpResNrgQ2[2], nt.InterpResNrgQ2[1], nt.InterpResNrgQ2[0], nt.InterpBaseResNrg, nt.InterpBreakAt)
			t.Logf("  lib interp energies: k3=%.9f k2=%.9f k1=%.9f k0=%.9f res=%.9f last=%.9f",
				lib.ResNrgInterp[3], lib.ResNrgInterp[2], lib.ResNrgInterp[1], lib.ResNrgInterp[0], lib.ResNrg, lib.ResNrgLast)
		}
	}
}

