//go:build cgo_libopus

package silk

import (
	"testing"
)

func TestNLSFFullEncodeTraceAgainstLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	cfg := GetBandwidthConfig(BandwidthWideband)
	numSubfr := 4
	subfrLen := cfg.SubframeSamples
	frameSamples := numSubfr * subfrLen
	frames := 15

	trace := &EncoderTrace{
		NLSF: &NLSFTrace{
			CaptureLTPRes: true,
		},
	}
	enc.trace = trace
	enc.SetVADState(200, 0, [4]int{})

	signal := generateNLSFTraceSignal(frames*frameSamples, cfg.SampleRate)

	for frame := 0; frame < frames; frame++ {
		start := frame * frameSamples
		end := start + frameSamples
		pcm := signal[start:end]

		_ = enc.EncodeFrame(pcm, nil, true)

		nt := trace.NLSF
		if nt == nil {
			t.Fatalf("missing NLSF trace at frame %d", frame)
		}
		if nt.LPCOrder == 0 || nt.NbSubfr == 0 {
			t.Fatalf("invalid NLSF trace settings at frame %d", frame)
		}
		if len(nt.LTPRes) < nt.LTPResLen || nt.LTPResLen == 0 {
			t.Fatalf("missing LTP residual capture at frame %d", frame)
		}

		ltpRes := nt.LTPRes[:nt.LTPResLen]
		libInterp := libopusFindLPCInterpDebug(ltpRes, nt.NbSubfr, nt.SubfrLen, nt.LPCOrder, nt.UseInterp, nt.FirstFrameAfterReset, nt.PrevNLSFQ15, float32(nt.MinInvGain))

		// Sanity: ensure our A2NLSF matches libopus for identical Q16 input.
		segment := ltpRes
		nbSubfrUsed := nt.NbSubfr
		if libInterp.InterpQ2 < 4 && nt.NbSubfr == maxNbSubfr {
			halfOffset := (maxNbSubfr / 2) * nt.SubfrLenWithOrder
			if halfOffset < len(ltpRes) {
				segment = ltpRes[halfOffset:]
				nbSubfrUsed = maxNbSubfr / 2
			}
		}
		libA, _ := libopusBurgModified(segment, float32(nt.MinInvGain), nt.SubfrLenWithOrder, nbSubfrUsed, nt.LPCOrder)
		if len(libA) >= nt.LPCOrder {
			lpcQ16 := make([]int32, nt.LPCOrder)
			for i := 0; i < nt.LPCOrder; i++ {
				lpcQ16[i] = float64ToInt32Round(float64(libA[i] * 65536.0))
			}
			libA2NLSF := libopusA2NLSF(lpcQ16, nt.LPCOrder)
			goA2NLSF := make([]int16, nt.LPCOrder)
			silkA2NLSF(goA2NLSF, lpcQ16, nt.LPCOrder)
			for i := 0; i < nt.LPCOrder; i++ {
				if libA2NLSF[i] != libInterp.NLSF[i] {
					t.Fatalf("frame %d: libopus A2NLSF mismatch at %d: got=%d lib=%d", frame, i, libA2NLSF[i], libInterp.NLSF[i])
				}
				if goA2NLSF[i] != libA2NLSF[i] {
					t.Fatalf("frame %d: A2NLSF mismatch at %d: go=%d lib=%d", frame, i, goA2NLSF[i], libA2NLSF[i])
				}
			}
		}

		if nt.InterpIdx != libInterp.InterpQ2 {
			t.Fatalf("frame %d: interpIdx go=%d lib=%d", frame, nt.InterpIdx, libInterp.InterpQ2)
		}
		if len(nt.RawNLSFQ15) < nt.LPCOrder {
			t.Fatalf("frame %d: raw NLSF length go=%d order=%d", frame, len(nt.RawNLSFQ15), nt.LPCOrder)
		}
		for i := 0; i < nt.LPCOrder; i++ {
			diff := nt.RawNLSFQ15[i] - libInterp.NLSF[i]
			if diff < 0 {
				diff = -diff
			}
			if diff > 4 {
				t.Fatalf("frame %d: raw NLSF[%d] go=%d lib=%d diff=%d", frame, i, nt.RawNLSFQ15[i], libInterp.NLSF[i], diff)
			}
			if diff > 0 && frame < 3 {
				t.Logf("frame %d: raw NLSF[%d] go=%d lib=%d diff=%d", frame, i, nt.RawNLSFQ15[i], libInterp.NLSF[i], diff)
			}
		}

		isWB := nt.Bandwidth == BandwidthWideband
		libProc := libopusProcessNLSF(libInterp.NLSF, nt.PrevNLSFQ15, nt.LPCOrder, nt.NbSubfr, nt.SignalType, nt.SpeechQ8, nt.UseInterp, nt.InterpIdx, nt.NLSFSurvivors, isWB)

		if len(nt.Residuals) < nt.LPCOrder {
			t.Fatalf("frame %d: residual count go=%d order=%d", frame, len(nt.Residuals), nt.LPCOrder)
		}
		if len(libProc.Indices) < nt.LPCOrder+1 {
			t.Fatalf("frame %d: lib indices len=%d order=%d", frame, len(libProc.Indices), nt.LPCOrder)
		}
		if int8(nt.Stage1Idx) != libProc.Indices[0] {
			t.Fatalf("frame %d: stage1 idx go=%d lib=%d", frame, nt.Stage1Idx, libProc.Indices[0])
		}
		for i := 0; i < nt.LPCOrder; i++ {
			if int8(nt.Residuals[i]) != libProc.Indices[i+1] {
				t.Fatalf("frame %d: residual[%d] go=%d lib=%d", frame, i, nt.Residuals[i], libProc.Indices[i+1])
			}
		}

		if len(nt.QuantizedNLSFQ15) < nt.LPCOrder {
			t.Fatalf("frame %d: quantized NLSF length go=%d order=%d", frame, len(nt.QuantizedNLSFQ15), nt.LPCOrder)
		}
		for i := 0; i < nt.LPCOrder; i++ {
			if nt.QuantizedNLSFQ15[i] != libProc.NLSFQ15[i] {
				t.Fatalf("frame %d: quantized NLSF[%d] go=%d lib=%d", frame, i, nt.QuantizedNLSFQ15[i], libProc.NLSFQ15[i])
			}
		}
	}
}
