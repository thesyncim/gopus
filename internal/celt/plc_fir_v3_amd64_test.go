//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const libopusCELTPLCModeXCorrKernel = uint32(8)

func probeCELTV3XcorrKernel(t *testing.T, order int, sum [4]float32, x, y []float32) [4]float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeXCorrKernel)
	payload.U32(uint32(order))
	payload.Float32s(sum[:]...)
	payload.Float32s(x...)
	payload.Float32s(y...)
	reader := runLibopusCELTPLC(t, payload)
	if got := reader.U32(); got != libopusCELTPLCModeXCorrKernel {
		t.Fatalf("C xcorr kernel mode=%d, want %d", got, libopusCELTPLCModeXCorrKernel)
	}
	if got := reader.Count(4); got != 4 {
		t.Fatalf("C xcorr output count=%d, want 4", got)
	}
	var out [4]float32
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.Err(); err != nil {
		t.Fatalf("C xcorr output: %v", err)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCELTV3SIMDXcorrKernelTailMatchesLibopus(t *testing.T) {
	requireCELTV3OracleTarget(t)
	if variant := requirePairedCELTOracleMode(t); variant != libopustooling.LibopusReferenceSIMD {
		t.Fatalf("Go v3 SIMD xcorr kernel needs the matching libopus SIMD reference, got %s", variant)
	}
	libopustest.RequireOracle(t)

	orders := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 21, 22, 23, 24}
	state := uint32(0x58434f52)
	for _, order := range orders {
		t.Run(fmt.Sprintf("order_%02d", order), func(t *testing.T) {
			var sum [4]float32
			for i := range sum {
				state = 1664525*state + 1013904223
				sum[i] = float32(int32(state>>8)-int32(1<<23)) / float32(1<<24)
			}
			x := make([]float32, order)
			y := make([]float32, order+3)
			for i := range x {
				state = 1664525*state + 1013904223
				x[i] = float32(int32(state>>8)-int32(1<<23)) / float32(1<<23)
			}
			for i := range y {
				state = 1664525*state + 1013904223
				y[i] = float32(int32(state>>8)-int32(1<<23)) / float32(1<<20)
			}

			want := probeCELTV3XcorrKernel(t, order, sum, x, y)
			got := sum
			celtPLCXcorrKernel4Float32SSE(x, y, &got, order)
			for i := range got {
				if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
					t.Fatalf("xcorr order=%d lane=%d Go=%08x C=%08x", order, i, gotBits, wantBits)
				}
			}
		})
	}
}

func TestCELTV3SIMDPLCFIRActualInputsMatchesLibopus(t *testing.T) {
	requireCELTV3OracleTarget(t)
	if variant := requirePairedCELTOracleMode(t); variant != libopustooling.LibopusReferenceSIMD {
		t.Fatalf("Go v3 SIMD PLC FIR needs the matching libopus SIMD reference, got %s", variant)
	}
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name      string
		frameSize int
		channels  int
	}{
		{name: "frame_120_stereo_ch0", frameSize: 120, channels: 2},
		{name: "frame_960_mono", frameSize: 960, channels: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := makeCELTPLCTestSignal(plcDecodeBufferSize*tc.channels,
				0x9000+uint32(tc.frameSize)+uint32(tc.channels), 2400)
			dec := NewDecoder(tc.channels)
			dec.plcDecodeMem = append(dec.plcDecodeMem[:0], history...)
			period := dec.searchPLCPitchPeriod()
			if period <= 0 {
				t.Fatal("no PLC pitch period")
			}

			const maxPeriod = combFilterMaxPeriod
			hist := history[:plcDecodeBufferSize]
			window := GetWindowBufferF32(Overlap)
			lpc := make([]float32, celtPLCLPCOrder)
			lpcFrame := hist[plcDecodeBufferSize-maxPeriod:]
			dec.computePLCLPC(lpcFrame, lpc, window)
			cLPC := probeLibopusPLCLPC(t, lpcFrame, window)
			assertFloat32Bits(t, "periodic LPC input coefficients", lpc, cLPC.lpc)

			excLength := min(2*period, maxPeriod)
			exc := make([]celtSig, celtPLCLPCOrder+maxPeriod)
			copy(exc, hist[plcDecodeBufferSize-maxPeriod-celtPLCLPCOrder:])
			firStart := celtPLCLPCOrder + maxPeriod - excLength
			exc32 := make([]float32, len(exc))
			copySigToFloat32(exc32, exc)
			want := probeLibopusPLCFIR(t, exc32, firStart, excLength, cLPC.lpc)
			gotSig := make([]celtSig, excLength)
			celtFIRFloat32(gotSig, exc, firStart, excLength, lpc)
			got := make([]float32, len(gotSig))
			copySigToFloat32(got, gotSig)
			assertFloat32Bits(t, "periodic FIR output", got, want)

			if allocs := testing.AllocsPerRun(100, func() {
				celtFIRFloat32(gotSig, exc, firStart, excLength, lpc)
			}); allocs != 0 {
				t.Fatalf("celtFIRFloat32 allocations/run=%g, want 0", allocs)
			}
		})
	}
}
