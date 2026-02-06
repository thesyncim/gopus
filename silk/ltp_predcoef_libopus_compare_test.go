//go:build cgo_libopus

package silk

import (
	"math"
	"testing"
)

func TestLTPAnalysisFilterAndBurgMatchLibopus(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	fsKHz := 16
	nbSubfr := 4
	subfrLen := subFrameLengthMs * fsKHz
	frameLen := subfrLen * nbSubfr
	preLen := enc.lpcOrder
	ltpMem := ltpMemLengthMs * fsKHz
	laShape := laShapeMs * fsKHz

	if preLen <= 0 {
		t.Fatalf("invalid LPC order: %d", preLen)
	}

	pitchBuf := make([]float32, ltpMem+laShape+frameLen)
	for i := range pitchBuf {
		tm := float64(i) / float64(fsKHz*1000)
		pitchBuf[i] = float32(
			0.7*math.Sin(2*math.Pi*250*tm) +
				0.2*math.Sin(2*math.Pi*500*tm),
		)
	}
	// Quantize to int16 precision to match the real encoder path:
	// In the actual encoder, inputBuffer stores data that has been
	// through quantizePCMToInt16 (float32 -> int16 -> float32 at [-1,1] scale).
	// buildLTPResidual then scales by silkSampleScale to recover int16-range values.
	// Without this, we compare unquantized-then-scaled vs int16-quantized data.
	scale := float32(silkSampleScale)
	invScale := float32(1.0 / silkSampleScale)
	for i := range pitchBuf {
		pitchBuf[i] = float32(floatToInt16Round(pitchBuf[i]*scale)) * invScale
	}

	frameStart := ltpMem
	pitchLags := []int{80, 82, 85, 88}
	gains := make([]float32, nbSubfr)
	for i := range gains {
		gains[i] = 1.0
	}

	var ltpCoeffs LTPCoeffsArray
	for k := 0; k < nbSubfr; k++ {
		copy(ltpCoeffs[k][:], LTPFilterHigh[0][:])
	}

	ltpRes := enc.buildLTPResidual(pitchBuf, frameStart, gains, pitchLags, ltpCoeffs, nbSubfr, subfrLen, typeVoiced)
	if len(ltpRes) != nbSubfr*(subfrLen+preLen) {
		t.Fatalf("ltpRes length mismatch: %d", len(ltpRes))
	}

	xStart := frameStart - preLen
	if xStart < 0 {
		t.Fatalf("invalid xStart: %d", xStart)
	}
	xScaledFull := make([]float32, len(pitchBuf))
	for i := range pitchBuf {
		xScaledFull[i] = float32(floatToInt16Round(pitchBuf[i] * float32(silkSampleScale)))
	}
	x := xScaledFull[xStart:]
	invGains := make([]float32, nbSubfr)
	for i := range invGains {
		invGains[i] = 1.0 / gains[i]
	}

	b := make([]float32, nbSubfr*ltpOrderConst)
	for k := 0; k < nbSubfr; k++ {
		for j := 0; j < ltpOrderConst; j++ {
			b[k*ltpOrderConst+j] = float32(ltpCoeffs[k][j]) / 128.0
		}
	}

	ltpResLib := libopusLTPAnalysisFilter(x, b, pitchLags, invGains, subfrLen, nbSubfr, preLen)
	if len(ltpResLib) != len(ltpRes) {
		t.Fatalf("libopus LTP residual length mismatch: %d vs %d", len(ltpResLib), len(ltpRes))
	}
	for i := range ltpRes {
		if diff := math.Abs(float64(ltpRes[i]) - float64(ltpResLib[i])); diff > 1e-4 {
			t.Fatalf("LTP residual mismatch at %d: go=%g lib=%g diff=%g", i, ltpRes[i], ltpResLib[i], diff)
		}
	}

	subfrLenWithOrder := subfrLen + preLen
	libA, _ := libopusBurgModified(ltpRes, float32(minInvGain), subfrLenWithOrder, nbSubfr, preLen)

	goA, _ := enc.burgModifiedFLPZeroAllocF32(ltpRes, float32(minInvGain), subfrLenWithOrder, nbSubfr, preLen)
	if len(libA) != len(goA) {
		t.Fatalf("LPC length mismatch: go=%d lib=%d", len(goA), len(libA))
	}
	for i := range goA {
		goQ12 := float64ToInt16Round(goA[i] * 4096.0)
		libQ12 := int16(math.Round(float64(libA[i] * 4096.0)))
		diff := int(goQ12) - int(libQ12)
		if diff < 0 {
			diff = -diff
		}
		if diff > 2 {
			t.Fatalf("LPC Q12 mismatch at %d: go=%d lib=%d", i, goQ12, libQ12)
		}
	}
}
