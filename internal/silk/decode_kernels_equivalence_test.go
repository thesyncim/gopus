package silk

import (
	"math"
	"math/rand"
	"testing"
)

// lpcSynthesisReference is the LPC synthesis loop of libopus
// silk/decode_core.c written literally, with the silk_SMLAWB chain in C order.
func lpcSynthesisReference(sLPC []int32, aQ12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength, order int) {
	for i := range subfrLength {
		lpcPredQ10 := int32(order >> 1)
		for j := range order {
			lpcPredQ10 = silkSMLAWB(lpcPredQ10, sLPC[maxLPCOrder+i-j-1], int32(aQ12[j]))
		}
		sLPC[maxLPCOrder+i] = silkAddSat32(presQ14[i], silkLShiftSAT32(lpcPredQ10, 4))
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(sLPC[maxLPCOrder+i], gainQ10), 8))
	}
}

func randomLPCSynthesisCase(rng *rand.Rand) (sLPC [maxLPCOrder + maxSubFrameLength]int32, a [maxLPCOrder]int16, pres [maxSubFrameLength]int32, gainQ10 int32) {
	// Mix realistic (small, stable) cases with full-range ones that saturate.
	wide := rng.Intn(3) == 0
	for i := range sLPC {
		if wide {
			sLPC[i] = int32(rng.Uint32())
		} else {
			sLPC[i] = int32(rng.Intn(1<<20)) - 1<<19
		}
	}
	for i := range a {
		if wide {
			a[i] = int16(rng.Uint32())
		} else {
			a[i] = int16(rng.Intn(8192) - 4096)
		}
	}
	for i := range pres {
		if wide {
			pres[i] = int32(rng.Uint32())
		} else {
			pres[i] = int32(rng.Intn(1<<18)) - 1<<17
		}
	}
	gainQ10 = int32(rng.Intn(1 << 22))
	if wide {
		gainQ10 = int32(rng.Uint32())
	}
	return
}

func TestLPCSynthesisMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1c5))
	for iter := 0; iter < 20000; iter++ {
		sLPC, a, pres, gainQ10 := randomLPCSynthesisCase(rng)
		n := 1 + rng.Intn(maxSubFrameLength)
		for _, order := range []int{minLPCOrder, maxLPCOrder} {
			got, want := sLPC, sLPC
			var gotOut, wantOut [maxSubFrameLength]int16
			if order == maxLPCOrder {
				synthesizeLPCOrder16(got[:], a[:], pres[:], gotOut[:], gainQ10, n)
			} else {
				synthesizeLPCOrder10(got[:], a[:], pres[:], gotOut[:], gainQ10, n)
			}
			lpcSynthesisReference(want[:], a[:], pres[:], wantOut[:], gainQ10, n, order)
			if got != want || gotOut != wantOut {
				for k := range got {
					if got[k] != want[k] {
						t.Errorf("sLPC[%d] got %d want %d", k, got[k], want[k])
						break
					}
				}
				for k := range gotOut {
					if gotOut[k] != wantOut[k] {
						t.Errorf("pxq[%d] got %d want %d", k, gotOut[k], wantOut[k])
						break
					}
				}
				t.Fatalf("iter %d order %d n %d: synthesis mismatch", iter, order, n)
			}
		}
	}
}

func TestLShiftSAT32By4MatchesLShiftSAT32(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5a7))
	edges := []int32{0, 1, -1, 0x07ffffff, 0x08000000, -0x08000000, -0x08000001, 1<<31 - 1, -1 << 31}
	for i := 0; i < 200000; i++ {
		x := int32(rng.Uint32())
		if i < len(edges) {
			x = edges[i]
		}
		if got, want := lShiftSAT32By4(x), silkLShiftSAT32(x, 4); got != want {
			t.Fatalf("lShiftSAT32By4(%d) = %d, want %d", x, got, want)
		}
	}
}

// lpcAnalysisFilterReference is libopus silk/LPC_analysis_filter.c
// silk_LPC_analysis_filter written literally.
func lpcAnalysisFilterReference(out, in, B []int16, length, order int) {
	for ix := order; ix < length; ix++ {
		outQ12 := silkSMULBB(int32(in[ix-1]), int32(B[0]))
		for j := 1; j < order; j++ {
			outQ12 = silkSMLABB(outQ12, int32(in[ix-1-j]), int32(B[j]))
		}
		out[ix] = silkSAT16(silkRSHIFT_ROUND(silkLSHIFT(int32(in[ix]), 12)-outQ12, 12))
	}
	for i := range order {
		out[i] = 0
	}
}

func TestLPCAnalysisFilterMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa7a))
	for iter := 0; iter < 20000; iter++ {
		order := []int{minLPCOrder, maxLPCOrder, 12, 6}[rng.Intn(4)]
		length := order + rng.Intn(330)
		in := make([]int16, length+rng.Intn(4))
		wide := rng.Intn(3) == 0
		for i := range in {
			if wide {
				in[i] = int16(rng.Uint32())
			} else {
				in[i] = int16(rng.Intn(4000) - 2000)
			}
		}
		B := make([]int16, order)
		for i := range B {
			if wide {
				B[i] = int16(rng.Uint32())
			} else {
				B[i] = int16(rng.Intn(8192) - 4096)
			}
		}
		got := make([]int16, length)
		want := make([]int16, length)
		for i := range got {
			got[i] = int16(rng.Uint32())
			want[i] = got[i]
		}
		silkLPCAnalysisFilter(got, in, B, length, order)
		lpcAnalysisFilterReference(want, in, B, length, order)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d order %d length %d: out[%d] = %d, want %d", iter, order, length, i, got[i], want[i])
			}
		}
	}
}

// stereoMSToLRReference is libopus silk/stereo_MS_to_LR.c silk_stereo_MS_to_LR
// written literally.
func stereoMSToLRReference(state *stereoDecState, x1, x2 []int16, predQ13 []int32, fsKHz, frameLength int) {
	copy(x1, state.sMid[:])
	copy(x2, state.sSide[:])
	copy(state.sMid[:], x1[frameLength:frameLength+2])
	copy(state.sSide[:], x2[frameLength:frameLength+2])
	pred0 := int32(state.predPrevQ13[0])
	pred1 := int32(state.predPrevQ13[1])
	denomQ16 := int32((1 << 16) / (stereoInterpLenMs * fsKHz))
	delta0 := silkRSHIFT_ROUND(silkSMULBB(predQ13[0]-int32(state.predPrevQ13[0]), denomQ16), 16)
	delta1 := silkRSHIFT_ROUND(silkSMULBB(predQ13[1]-int32(state.predPrevQ13[1]), denomQ16), 16)
	step := func(n int) {
		sum := silkLSHIFT(silkADD_LSHIFT32(int32(x1[n])+int32(x1[n+2]), int32(x1[n+1]), 1), 9)
		sum = silkSMLAWB(silkLSHIFT(int32(x2[n+1]), 8), sum, pred0)
		sum = silkSMLAWB(sum, silkLSHIFT(int32(x1[n+1]), 11), pred1)
		x2[n+1] = silkSAT16(silkRSHIFT_ROUND(sum, 8))
	}
	for n := 0; n < stereoInterpLenMs*fsKHz; n++ {
		pred0 += delta0
		pred1 += delta1
		step(n)
	}
	pred0 = predQ13[0]
	pred1 = predQ13[1]
	for n := stereoInterpLenMs * fsKHz; n < frameLength; n++ {
		step(n)
	}
	state.predPrevQ13[0] = int16(predQ13[0])
	state.predPrevQ13[1] = int16(predQ13[1])
	for n := range frameLength {
		sum := int32(x1[n+1]) + int32(x2[n+1])
		diff := int32(x1[n+1]) - int32(x2[n+1])
		x1[n+1] = silkSAT16(sum)
		x2[n+1] = silkSAT16(diff)
	}
}

func TestStereoMSToLRMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x57e))
	for iter := 0; iter < 20000; iter++ {
		fsKHz := []int{8, 12, 16}[rng.Intn(3)]
		frameLength := fsKHz * []int{10, 20}[rng.Intn(2)]
		wide := rng.Intn(3) == 0
		sample := func() int16 {
			if wide {
				return int16(rng.Uint32())
			}
			return int16(rng.Intn(8000) - 4000)
		}
		mid := make([]int16, frameLength+2)
		side := make([]int16, frameLength+2)
		for i := range mid {
			mid[i] = sample()
			side[i] = sample()
		}
		var state stereoDecState
		for i := range 2 {
			state.sMid[i] = sample()
			state.sSide[i] = sample()
			state.predPrevQ13[i] = int16(rng.Intn(1<<15) - 1<<14)
		}
		predQ13 := []int32{int32(rng.Intn(1<<15) - 1<<14), int32(rng.Intn(1<<15) - 1<<14)}
		if wide {
			predQ13[0] = int32(int16(rng.Uint32()))
			predQ13[1] = int32(int16(rng.Uint32()))
			state.predPrevQ13[0] = int16(rng.Uint32())
		}
		wantState := state
		wantMid := append([]int16(nil), mid...)
		wantSide := append([]int16(nil), side...)
		silkStereoMSToLR(&state, mid, side, predQ13, fsKHz, frameLength)
		stereoMSToLRReference(&wantState, wantMid, wantSide, predQ13, fsKHz, frameLength)
		if state != wantState {
			t.Fatalf("iter %d: state %+v want %+v", iter, state, wantState)
		}
		for i := range mid {
			if mid[i] != wantMid[i] || side[i] != wantSide[i] {
				t.Fatalf("iter %d fs=%d len=%d: sample %d got (%d,%d) want (%d,%d)", iter, fsKHz, frameLength, i, mid[i], side[i], wantMid[i], wantSide[i])
			}
		}
	}
}

func TestUp2HQCoreMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x2b9))
	for iter := 0; iter < 5000; iter++ {
		n := rng.Intn(500)
		in := make([]int16, n)
		for i := range in {
			in[i] = int16(rng.Uint32())
		}
		var state [6]int32
		for i := range state {
			state[i] = int32(rng.Uint32())
			if rng.Intn(2) == 0 {
				state[i] >>= 6
			}
		}
		wantState := state
		got := make([]int16, 2*n)
		want := make([]int16, 2*n)
		up2HQCore(got, in, &state)
		up2HQCoreGo(want, in, &wantState)
		if state != wantState {
			t.Fatalf("iter %d n %d: state %v want %v", iter, n, state, wantState)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d n %d: out[%d] = %d, want %d", iter, n, i, got[i], want[i])
			}
		}
	}
}

// firInterpolLibopusRef is silk_resampler_private_IIR_FIR_INTERPOL as written
// in libopus: the phase's four taps from frac_FIR_12 and the mirrored phase's
// taps in reverse, accumulated in order with int32 wraparound.
func firInterpolLibopusRef(out []int16, buf []int16, incr int32) {
	indexQ16 := int32(0)
	for n := range out {
		tableIndex := int32((int64(indexQ16&0xFFFF) * 12) >> 16)
		b := buf[indexQ16>>16:]
		f := silkResamplerFracFIR12Flat[tableIndex*4:]
		m := silkResamplerFracFIR12Flat[(11-tableIndex)*4:]
		res := int32(b[0]) * int32(f[0])
		res += int32(b[1]) * int32(f[1])
		res += int32(b[2]) * int32(f[2])
		res += int32(b[3]) * int32(f[3])
		res += int32(b[4]) * int32(m[3])
		res += int32(b[5]) * int32(m[2])
		res += int32(b[6]) * int32(m[1])
		res += int32(b[7]) * int32(m[0])
		res = ((res >> 14) + 1) >> 1
		out[n] = int16(max(-32768, min(32767, res)))
		indexQ16 += incr
	}
}

func TestFIRInterpolMatchesGeneric(t *testing.T) {
	rng := rand.New(rand.NewSource(0xf12))
	incrs := []int32{21846, 32768, 43691, 65536, 87381, 87382, 131072, 98304, 49152}
	for iter := 0; iter < 5000; iter++ {
		incr := incrs[rng.Intn(len(incrs))]
		if rng.Intn(4) == 0 {
			incr = int32(8192 + rng.Intn(3<<16))
		}
		nIn := 1 + rng.Intn(480)
		maxIndexQ16 := int32(nIn) << 17
		buf := make([]int16, 2*nIn+resamplerOrderFIR12)
		for i := range buf {
			buf[i] = int16(rng.Uint32())
		}
		nOut := int((maxIndexQ16 + incr - 1) / incr)
		r := &LibopusResampler{invRatioQ16: incr}
		got := make([]int16, nOut)
		want := make([]int16, nOut)
		if n := r.firInterpol(got, 0, buf, maxIndexQ16); n != nOut {
			t.Fatalf("iter %d: firInterpol wrote %d, want %d", iter, n, nOut)
		}
		firInterpolGeneric(want, buf, 0, incr)
		ref := make([]int16, nOut)
		firInterpolLibopusRef(ref, buf, incr)
		for i := range want {
			if want[i] != ref[i] {
				t.Fatalf("iter %d incr %d nIn %d: generic out[%d] = %d, libopus loop %d", iter, incr, nIn, i, want[i], ref[i])
			}
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("iter %d incr %d nIn %d: out[%d] = %d, want %d", iter, incr, nIn, i, got[i], want[i])
			}
		}
	}
}

func TestWriteInt16AsFloat32MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1f3))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(1000)
		src := make([]int16, n)
		for i := range src {
			src[i] = int16(rng.Uint32())
		}
		src[0] = -32768
		got := make([]float32, n)
		writeInt16AsFloat32Core(got, src, n)
		for i, s := range src {
			if want := float32(s) / 32768; math.Float32bits(got[i]) != math.Float32bits(want) {
				t.Fatalf("n %d: dst[%d] = %v, want %v", n, i, got[i], want)
			}
		}
	}
}
