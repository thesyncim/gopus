package silk

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKNoiseShapeInputMagic  = "GSNI"
	libopusSILKNoiseShapeOutputMagic = "GSNO"

	libopusSILKNoiseShapeModeWarpedAutocorr  = uint32(0)
	libopusSILKNoiseShapeModeApplySineWindow = uint32(1)
)

var libopusSILKNoiseShapeHelper libopustest.HelperCache

func getLibopusSILKNoiseShapeHelperPath() (string, error) {
	return libopusSILKNoiseShapeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "silk noise shape",
		OutputBase:  "gopus_libopus_silk_noise_shape",
		SourceFile:  "libopus_silk_noise_shape_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
}

type libopusSILKWarpedAutocorrCase struct {
	name    string
	order   int
	warping float32
	x       []float32
}

func probeLibopusSILKWarpedAutocorr(cases []libopusSILKWarpedAutocorrCase) ([][]float32, error) {
	binPath, err := getLibopusSILKNoiseShapeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKNoiseShapeInputMagic, libopusSILKNoiseShapeModeWarpedAutocorr, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.order))
		payload.Float32(tc.warping)
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk warped autocorr flp", libopusSILKNoiseShapeOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		order := int(reader.U32())
		if order <= 0 || order > maxShapeLpcOrder || order&1 != 0 {
			return nil, fmt.Errorf("helper order=%d", order)
		}
		out[i] = make([]float32, order+1)
		for j := range maxShapeLpcOrder + 1 {
			v := reader.Float32()
			if j <= order {
				out[i][j] = v
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

type libopusSILKSineWindowCase struct {
	name    string
	winType int
	x       []float32
}

func probeLibopusSILKSineWindow(cases []libopusSILKSineWindowCase) ([][]float32, error) {
	binPath, err := getLibopusSILKNoiseShapeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKNoiseShapeInputMagic, libopusSILKNoiseShapeModeApplySineWindow, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.winType))
		payload.Float32s(tc.x...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk apply sine window flp", libopusSILKNoiseShapeOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		length := int(reader.U32())
		if length <= 0 || length > 2048 || length&3 != 0 {
			return nil, fmt.Errorf("helper length=%d", length)
		}
		out[i] = make([]float32, length)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func silkWarpedAutocorrSignal(n int, seed int64, scale float32) []float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float32, n)
	for i := range out {
		out[i] = (rng.Float32()*2 - 1) * scale
	}
	return out
}

// TestSILKWarpedAutocorrelationFLPMatchesLibopusOracle verifies that the float
// warped autocorrelation kernel matches libopus silk_warped_autocorrelation_FLP
// (silk/float/warped_autocorrelation_FLP.c) bit-for-bit. The kernel accumulates
// in C double, but the input/warping/output round-trip through silk_float
// (float32), and the inner allpass-section expression
//
//	tmp2 = state[i] + warping*state[i+1] - warping*tmp1;
//
// is a strong candidate for arm64 FMA contraction divergence.
func TestSILKWarpedAutocorrelationFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKWarpedAutocorrCase{
		// SILK noise-shape analysis uses shapingLPCOrder of 12 (NB/MB) or 16 (WB/SWB),
		// warping ~ 0.0..0.5, shapeWinLength up to ~5*subfr.
		{name: "order16_warp_small", order: 16, warping: 0.015625, x: silkWarpedAutocorrSignal(240, 0x10203040, 0.3)},
		{name: "order16_warp_mid", order: 16, warping: 0.25, x: silkWarpedAutocorrSignal(320, 0x50607080, 1.0)},
		{name: "order16_warp_large", order: 16, warping: 0.45, x: silkWarpedAutocorrSignal(176, 0x90a0b0c0, 0.8)},
		{name: "order12_warp_mid", order: 12, warping: 0.1875, x: silkWarpedAutocorrSignal(200, 0x11223344, 0.5)},
		{name: "order10_warp_small", order: 10, warping: 0.03125, x: silkWarpedAutocorrSignal(160, 0x55667788, 0.25)},
		{name: "order24_warp_mid", order: 24, warping: 0.3, x: silkWarpedAutocorrSignal(480, 0x0badf00d, 2.0)},
		{name: "order16_loud", order: 16, warping: 0.21875, x: silkWarpedAutocorrSignal(256, 0xdeadbeef, 4096.0)},
		{name: "order16_neg_warp", order: 16, warping: -0.125, x: silkWarpedAutocorrSignal(256, 0xcafebabe, 1.0)},
		{name: "order16_short", order: 16, warping: 0.25, x: silkWarpedAutocorrSignal(48, 0x13579bdf, 0.7)},
	}
	want, err := probeLibopusSILKWarpedAutocorr(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk warped autocorr flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := make([]float32, tc.order+1)
			state := make([]float32, tc.order+1)
			warpedAutocorrelationFLP32(out, state, tc.x, tc.warping, len(tc.x), tc.order)
			if len(out) != len(want[i]) {
				t.Fatalf("corr len=%d want %d", len(out), len(want[i]))
			}
			for j := range out {
				if math.Float32bits(out[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("corr[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(out[j]), out[j],
						math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

// TestSILKApplySineWindowFLPMatchesLibopusOracle verifies the float sine-window
// kernel matches libopus silk_apply_sine_window_FLP
// (silk/float/apply_sine_window_FLP.c) bit-for-bit. The recurrence
// S0 = c*S1 - S0 (and S1 = c*S0 - S1) and the windowing products are
// single-statement multiply-adds that arm64 clang may contract into FMA.
func TestSILKApplySineWindowFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKSineWindowCase{
		{name: "type1_len64", winType: 1, x: silkWarpedAutocorrSignal(64, 0x10203040, 1.0)},
		{name: "type2_len64", winType: 2, x: silkWarpedAutocorrSignal(64, 0x50607080, 1.0)},
		{name: "type1_len192", winType: 1, x: silkWarpedAutocorrSignal(192, 0x11223344, 0.5)},
		{name: "type2_len192", winType: 2, x: silkWarpedAutocorrSignal(192, 0x55667788, 0.5)},
		{name: "type1_len320", winType: 1, x: silkWarpedAutocorrSignal(320, 0x99aabbcc, 2.0)},
		{name: "type2_len320", winType: 2, x: silkWarpedAutocorrSignal(320, 0xddeeff00, 2.0)},
		{name: "type1_loud", winType: 1, x: silkWarpedAutocorrSignal(240, 0xdeadbeef, 4096.0)},
		{name: "type2_loud", winType: 2, x: silkWarpedAutocorrSignal(240, 0xcafebabe, 32768.0)},
		{name: "type1_short", winType: 1, x: silkWarpedAutocorrSignal(16, 0x13579bdf, 0.7)},
		{name: "type2_long", winType: 2, x: silkWarpedAutocorrSignal(960, 0x02468ace, 1.5)},
	}
	want, err := probeLibopusSILKSineWindow(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk apply sine window flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := make([]float32, len(tc.x))
			applySineWindowFLP32(out, tc.x, tc.winType, len(tc.x))
			if len(out) != len(want[i]) {
				t.Fatalf("win len=%d want %d", len(out), len(want[i]))
			}
			for j := range out {
				if math.Float32bits(out[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("win[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(out[j]), out[j],
						math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

const libopusSILKNoiseShapeModeProcessGains = uint32(2)

type libopusSILKProcessGainsCase struct {
	name            string
	signalType      int
	nbSubfr         int
	subfrLength     int
	condCoding      int
	snrDBQ7         int
	speechActQ8     int
	inputTiltQ15    int
	nStatesDD       int
	quantOffsetType int
	lastGainIndex   int8
	predGainQ7      int32
	inputQuality    float32
	codingQuality   float32
	gains           []float32
	resNrg          []float32
}

type libopusSILKProcessGainsResult struct {
	gains           []float32
	gainsUnqQ16     []int32
	gainsIndices    []int8
	lambda          float32
	quantOffsetType int
	lastGainIndex   int8
	lastGainIdxPrev int8
}

func probeLibopusSILKProcessGains(cases []libopusSILKProcessGainsCase) ([]libopusSILKProcessGainsResult, error) {
	binPath, err := getLibopusSILKNoiseShapeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKNoiseShapeInputMagic, libopusSILKNoiseShapeModeProcessGains, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.signalType))
		payload.U32(uint32(tc.nbSubfr))
		payload.U32(uint32(tc.subfrLength))
		payload.U32(uint32(tc.condCoding))
		payload.I32(int32(tc.snrDBQ7))
		payload.I32(int32(tc.speechActQ8))
		payload.I32(int32(tc.inputTiltQ15))
		payload.U32(uint32(tc.nStatesDD))
		payload.U32(uint32(tc.quantOffsetType))
		payload.U32(uint32(int32(tc.lastGainIndex)))
		payload.Float32(float32(tc.predGainQ7) / 128.0)
		payload.Float32(tc.inputQuality)
		payload.Float32(tc.codingQuality)
		payload.Float32s(tc.gains...)
		payload.Float32s(tc.resNrg...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk process gains flp", libopusSILKNoiseShapeOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([]libopusSILKProcessGainsResult, count)
	for i := range out {
		nb := int(reader.U32())
		if nb != cases[i].nbSubfr {
			return nil, fmt.Errorf("helper nb_subfr=%d want %d", nb, cases[i].nbSubfr)
		}
		out[i].gains = make([]float32, nb)
		for j := range out[i].gains {
			out[i].gains[j] = reader.Float32()
		}
		out[i].gainsUnqQ16 = make([]int32, nb)
		for j := range out[i].gainsUnqQ16 {
			out[i].gainsUnqQ16[j] = reader.I32()
		}
		out[i].gainsIndices = make([]int8, nb)
		for j := range out[i].gainsIndices {
			out[i].gainsIndices[j] = int8(reader.I32())
		}
		out[i].lambda = reader.Float32()
		out[i].quantOffsetType = int(reader.I32())
		out[i].lastGainIndex = int8(reader.I32())
		out[i].lastGainIdxPrev = int8(reader.I32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// TestSILKProcessGainsFLPMatchesLibopusOracle verifies the full SILK float gain
// processing pipeline against libopus silk_process_gains_FLP
// (silk/float/process_gains_FLP.c): the voiced sigmoid gain reduction
// (s = 1 - 0.5*sigmoid(...)), the soft energy limit
// (gain = sqrt(gain*gain + ResNrg*InvMaxSqrVal)), the Q16 conversion,
// silk_gains_quant, and the Lambda computation. The squared-gain-plus-residual
// expression and the Lambda accumulation are single-statement multiply-adds and
// thus arm64 FMA-contraction candidates.
func TestSILKProcessGainsFLPMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKProcessGainsCase{
		{
			name: "voiced_4sf", signalType: typeVoiced, nbSubfr: 4, subfrLength: 80,
			condCoding: codeIndependently, snrDBQ7: 25 * 128, speechActQ8: 200,
			inputTiltQ15: -4000, nStatesDD: 4, quantOffsetType: 0, lastGainIndex: 40,
			predGainQ7: 14 * 128, inputQuality: 0.8, codingQuality: 0.6,
			gains:  []float32{120.5, 88.25, 200.0, 64.0},
			resNrg: []float32{15000.0, 22000.0, 8000.0, 30000.0},
		},
		{
			name: "unvoiced_4sf", signalType: typeUnvoiced, nbSubfr: 4, subfrLength: 80,
			condCoding: codeConditionally, snrDBQ7: 18 * 128, speechActQ8: 50,
			inputTiltQ15: 1000, nStatesDD: 1, quantOffsetType: 1, lastGainIndex: 20,
			predGainQ7: 0, inputQuality: 0.4, codingQuality: 0.3,
			gains:  []float32{300.0, 150.0, 75.5, 410.25},
			resNrg: []float32{50000.0, 12000.0, 9000.0, 70000.0},
		},
		{
			name: "voiced_2sf_lowgain", signalType: typeVoiced, nbSubfr: 2, subfrLength: 100,
			condCoding: codeIndependently, snrDBQ7: 30 * 128, speechActQ8: 255,
			inputTiltQ15: -10000, nStatesDD: 4, quantOffsetType: 0, lastGainIndex: 10,
			predGainQ7: 25 * 128, inputQuality: 0.95, codingQuality: 0.9,
			gains:  []float32{5.0, 3.25},
			resNrg: []float32{100.0, 250.0},
		},
		{
			name: "voiced_highltp_4sf", signalType: typeVoiced, nbSubfr: 4, subfrLength: 60,
			condCoding: codeIndependently, snrDBQ7: 22 * 128, speechActQ8: 180,
			inputTiltQ15: 5000, nStatesDD: 2, quantOffsetType: 0, lastGainIndex: 55,
			predGainQ7: 40 * 128, inputQuality: 0.7, codingQuality: 0.5,
			gains:  []float32{1000.0, 950.5, 1100.25, 800.0},
			resNrg: []float32{500000.0, 480000.0, 520000.0, 410000.0},
		},
		{
			name: "unvoiced_saturate", signalType: typeUnvoiced, nbSubfr: 4, subfrLength: 40,
			condCoding: codeIndependently, snrDBQ7: 10 * 128, speechActQ8: 10,
			inputTiltQ15: 0, nStatesDD: 1, quantOffsetType: 1, lastGainIndex: 0,
			predGainQ7: 0, inputQuality: 0.1, codingQuality: 0.05,
			gains:  []float32{40000.0, 50000.0, 30000.0, 60000.0},
			resNrg: []float32{1e9, 2e9, 5e8, 3e9},
		},
	}
	want, err := probeLibopusSILKProcessGains(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk process gains flp", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gains := append([]float32(nil), tc.gains...)
			resNrg := append([]float32(nil), tc.resNrg...)
			quantOffset := tc.quantOffsetType
			processed := applyGainProcessing(gains, resNrg, tc.predGainQ7, tc.snrDBQ7, tc.signalType, tc.inputTiltQ15, tc.subfrLength)
			if tc.signalType == typeVoiced {
				quantOffset = processed
			}

			// Soft-limited gains (pre-quant) feed GainsUnq_Q16 in libopus.
			gainsUnqQ16 := make([]int32, tc.nbSubfr)
			for k := 0; k < tc.nbSubfr; k++ {
				gainsUnqQ16[k] = int32(gains[k] * 65536.0)
			}
			gainsQ16 := append([]int32(nil), gainsUnqQ16...)
			gainIndices := make([]int8, tc.nbSubfr)
			prevInd := silkGainsQuantInto(gainIndices, gainsQ16, tc.lastGainIndex, tc.condCoding == codeConditionally, tc.nbSubfr)

			lambdaQ10 := computeLambdaQ10(tc.signalType, tc.speechActQ8, quantOffset, tc.nStatesDD, tc.codingQuality, tc.inputQuality)

			// quantOffsetType.
			if quantOffset != want[i].quantOffsetType {
				t.Fatalf("quantOffsetType=%d want %d", quantOffset, want[i].quantOffsetType)
			}
			// GainsUnq_Q16 (captures gain*gain + ResNrg*InvMaxSqrVal and sigmoid).
			for k := 0; k < tc.nbSubfr; k++ {
				if gainsUnqQ16[k] != want[i].gainsUnqQ16[k] {
					t.Fatalf("GainsUnq_Q16[%d]=%d want %d", k, gainsUnqQ16[k], want[i].gainsUnqQ16[k])
				}
			}
			// Quantized gain indices.
			for k := 0; k < tc.nbSubfr; k++ {
				if gainIndices[k] != want[i].gainsIndices[k] {
					t.Fatalf("GainsIndices[%d]=%d want %d", k, gainIndices[k], want[i].gainsIndices[k])
				}
			}
			// Quantized gains (Q16) -> libopus overwrites ctrl.Gains with pGains_Q16/65536.
			for k := 0; k < tc.nbSubfr; k++ {
				gotGain := float32(gainsQ16[k]) / 65536.0
				if math.Float32bits(gotGain) != math.Float32bits(want[i].gains[k]) {
					t.Fatalf("Gains[%d]=%08x %.10g want %08x %.10g", k,
						math.Float32bits(gotGain), gotGain,
						math.Float32bits(want[i].gains[k]), want[i].gains[k])
				}
			}
			// LastGainIndex updated by gains_quant.
			if prevInd != want[i].lastGainIndex {
				t.Fatalf("LastGainIndex=%d want %d", prevInd, want[i].lastGainIndex)
			}
			// Lambda: compare Q10 fixed-point against libopus float Lambda.
			wantLambdaQ10 := float32ToInt32RoundEven(want[i].lambda * 1024.0)
			if lambdaQ10 != wantLambdaQ10 {
				t.Fatalf("LambdaQ10=%d want %d (libopus Lambda=%.10g)", lambdaQ10, wantLambdaQ10, want[i].lambda)
			}
		})
	}
}
