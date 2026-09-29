//go:build linux && amd64.v3 && !gopus_fixed_point

package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

// TestBuildLTPResidualLinkedKernelDiagnostic compares the production Go
// residual builder with a direct call to the selected libopus kernel. The
// packet-level encode tests remain the strict parity gate; this test localizes
// the standalone filter arithmetic after confirming identical float32 inputs.
func TestBuildLTPResidualLinkedKernelDiagnostic(t *testing.T) {
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err != nil || target != "v3" {
		message := "SILK LTP residual diagnostic requires GOPUS_LIBOPUS_AMD64_TARGET=v3"
		if err != nil {
			message += ": " + err.Error()
		} else {
			message += ", got " + target
		}
		if libopustest.StrictRefRequired() {
			t.Fatal(message)
		}
		t.Skip(message)
	}
	libopustest.RequireOracle(t)

	helperPath, err := getLTPResidualOracleHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK LTP residual filter", err)
		return
	}
	cases := makeLTPResidualDiagnosticCases()
	inputs := make([]ltpResidualOracleInput, len(cases))
	for i := range cases {
		inputs[i] = cases[i].oracle
	}
	payload, err := encodeLTPResidualOracleInput(inputs)
	if err != nil {
		t.Fatalf("encode LTP residual oracle input: %v", err)
	}
	trailingPayload := append(append([]byte(nil), payload...), 0)
	if _, err := libopustest.RunHelper(helperPath, trailingPayload); err == nil {
		t.Fatal("LTP residual helper accepted trailing input bytes")
	}
	output, err := libopustest.RunHelper(helperPath, payload)
	if err != nil {
		t.Fatalf("run LTP residual oracle helper: %v", err)
	}
	got, err := parseLTPResidualOracleOutput(output, len(cases))
	if err != nil {
		t.Fatalf("parse LTP residual oracle output: %v", err)
	}
	if len(got) != len(cases) {
		t.Fatalf("LTP residual oracle returned %d cases, want %d", len(got), len(cases))
	}

	for caseIndex, tc := range cases {
		want := got[caseIndex]
		if want.frameStart != tc.oracle.frameStart || want.subframeLength != tc.oracle.subframeLength ||
			want.numSubframes != tc.oracle.numSubframes || want.preLength != tc.oracle.preLength ||
			want.inputCount != len(tc.oracle.pitchBuffer) {
			t.Fatalf("case %s C geometry=%+v does not echo input %+v", tc.name, want, tc.oracle)
		}
		if err := validateLTPResidualOracleEcho(tc.oracle, want); err != nil {
			t.Fatalf("case %s C operand echo: %v", tc.name, err)
		}
		if err := validateLTPResidualGoSetup(tc); err != nil {
			t.Fatalf("case %s Go operands: %v", tc.name, err)
		}

		encoder := &Encoder{lpcOrder: int32(tc.oracle.preLength)}
		goResidual := encoder.buildLTPResidual(
			tc.rawPitchBuffer,
			tc.oracle.frameStart,
			tc.gains,
			tc.oracle.pitchLags,
			tc.coefficients,
			tc.oracle.numSubframes,
			tc.oracle.subframeLength,
			typeVoiced,
		)
		if len(goResidual) != len(want.samples) {
			t.Fatalf("case %s Go residual length=%d, C output length=%d", tc.name, len(goResidual), len(want.samples))
		}

		separateModel := makeLTPResidualSeparateModel(tc.oracle)
		goDiff := firstLTPResidualDifference(goResidual, want.samples)
		modelDiff := firstLTPResidualDifference(separateModel, want.samples)
		goModelDiff := firstLTPFloatDifference(goResidual, separateModel)
		t.Logf("case %s: linked-C vs Go buildLTPResidual differences=%d/%d first=%s; C vs separate-product C-domain model=%d/%d first=%s; Go vs model=%d/%d first=%s",
			tc.name,
			goDiff.count, len(goResidual), goDiff.summary("Go", "C"),
			modelDiff.count, len(separateModel), modelDiff.summary("model", "C"),
			goModelDiff.count, len(goResidual), goModelDiff.summary("Go", "model"),
		)
	}
}

type ltpResidualDiagnosticCase struct {
	name           string
	oracle         ltpResidualOracleInput
	rawPitchBuffer []float32
	gains          []float32
	coefficients   LTPCoeffsArray
}

func makeLTPResidualDiagnosticCases() []ltpResidualDiagnosticCase {
	return []ltpResidualDiagnosticCase{
		makeLTPResidualDiagnosticCase("synthetic_non_grid_mb", 10, 60, 220, 512, [4]int32{47, 59, 71, 83}, 0x1834a6d1),
		makeLTPResidualDiagnosticCase("synthetic_non_grid_wb", 16, 80, 320, 704, [4]int32{81, 93, 109, 127}, 0x731f0b5a),
	}
}

func makeLTPResidualDiagnosticCase(name string, order, subframeLength, frameStart, inputCount int, pitchLags [4]int32, seed uint32) ltpResidualDiagnosticCase {
	const scale = float32(silkSampleScale)
	state := seed
	raw := make([]float32, inputCount)
	cDomain := make([]float32, inputCount)
	for i := range raw {
		state = state*1664525 + 1013904223
		mantissa := int32(state>>8) - 1<<23
		raw[i] = float32(mantissa) / float32(1<<24)
		cDomain[i] = raw[i] * scale
	}

	pitchLagsSlice := append([]int32(nil), pitchLags[:]...)
	invGains := []float32{1, 0.5, 0.25, 2}
	gains := make([]float32, len(invGains))
	for i, inverse := range invGains {
		gains[i] = 1 / inverse
	}
	q7 := [4][ltpOrderConst]int8{
		{13, -7, 61, 19, -5},
		{-9, 27, 73, 11, 3},
		{7, 17, 55, -13, -3},
		{-5, 31, 67, 9, -11},
	}
	var coefficients LTPCoeffsArray
	taps := make([]float32, len(q7)*ltpOrderConst)
	for subframe := range q7 {
		for tap, q := range q7[subframe] {
			coefficients[subframe][tap] = q
			taps[subframe*ltpOrderConst+tap] = float32(q) / 128
		}
	}

	return ltpResidualDiagnosticCase{
		name:           name,
		rawPitchBuffer: raw,
		gains:          gains,
		coefficients:   coefficients,
		oracle: ltpResidualOracleInput{
			frameStart:     frameStart,
			subframeLength: subframeLength,
			numSubframes:   len(pitchLags),
			preLength:      order,
			pitchBuffer:    cDomain,
			pitchLags:      pitchLagsSlice,
			invGains:       invGains,
			taps:           taps,
		},
	}
}

func validateLTPResidualOracleEcho(input ltpResidualOracleInput, got ltpResidualOracleResult) error {
	if got.outputCount != len(got.samples) || len(got.subframes) != input.numSubframes {
		return fmtLTPResidualOracle("echo counts output=%d/%d subframes=%d/%d",
			got.outputCount, len(got.samples), len(got.subframes), input.numSubframes)
	}
	for subframe := range got.subframes {
		want := got.subframes[subframe]
		if want.pitchLag != input.pitchLags[subframe] || math.Float32bits(want.invGain) != math.Float32bits(input.invGains[subframe]) {
			return fmtLTPResidualOracle("subframe %d lag/gain echo=(%d,%08x), want=(%d,%08x)",
				subframe, want.pitchLag, math.Float32bits(want.invGain),
				input.pitchLags[subframe], math.Float32bits(input.invGains[subframe]))
		}
		for tap := range ltpOrderConst {
			wantBits := math.Float32bits(input.taps[subframe*ltpOrderConst+tap])
			if gotBits := math.Float32bits(want.taps[tap]); gotBits != wantBits {
				return fmtLTPResidualOracle("subframe %d tap %d echo=%08x want=%08x", subframe, tap, gotBits, wantBits)
			}
		}
	}

	index := 0
	xStart := input.frameStart - input.preLength
	outputLength := input.subframeLength + input.preLength
	for subframe := range input.numSubframes {
		xBase := xStart + subframe*input.subframeLength
		for sample := range outputLength {
			xIndex := xBase + sample
			gotSample := got.samples[index]
			if gotBits, wantBits := math.Float32bits(gotSample.x), math.Float32bits(input.pitchBuffer[xIndex]); gotBits != wantBits {
				return fmtLTPResidualOracle("sample %d x echo=%08x want=%08x", index, gotBits, wantBits)
			}
			for tap := range ltpOrderConst {
				lagIndex := xIndex - int(input.pitchLags[subframe]) + ltpOrderConst/2 - tap
				if gotBits, wantBits := math.Float32bits(gotSample.lags[tap]), math.Float32bits(input.pitchBuffer[lagIndex]); gotBits != wantBits {
					return fmtLTPResidualOracle("sample %d lag %d echo=%08x want=%08x", index, tap, gotBits, wantBits)
				}
			}
			index++
		}
	}
	return nil
}

func validateLTPResidualGoSetup(tc ltpResidualDiagnosticCase) error {
	if len(tc.rawPitchBuffer) != len(tc.oracle.pitchBuffer) || len(tc.gains) != tc.oracle.numSubframes {
		return fmtLTPResidualOracle("Go input sizes pitch=%d C=%d gains=%d subframes=%d",
			len(tc.rawPitchBuffer), len(tc.oracle.pitchBuffer), len(tc.gains), tc.oracle.numSubframes)
	}
	scale := float32(silkSampleScale)
	for i, raw := range tc.rawPitchBuffer {
		if got, want := math.Float32bits(raw*scale), math.Float32bits(tc.oracle.pitchBuffer[i]); got != want {
			return fmtLTPResidualOracle("sample %d Go scaled input=%08x C input=%08x", i, got, want)
		}
	}
	for subframe := range tc.gains {
		if got, want := math.Float32bits(1/tc.gains[subframe]), math.Float32bits(tc.oracle.invGains[subframe]); got != want {
			return fmtLTPResidualOracle("subframe %d Go reciprocal gain=%08x C input=%08x", subframe, got, want)
		}
		for tap := range ltpOrderConst {
			goTap := float32(tc.coefficients[subframe][tap]) / 128
			cTap := tc.oracle.taps[subframe*ltpOrderConst+tap]
			if got, want := math.Float32bits(goTap), math.Float32bits(cTap); got != want {
				return fmtLTPResidualOracle("subframe %d tap %d Go coefficient=%08x C input=%08x", subframe, tap, got, want)
			}
		}
	}
	return nil
}

type ltpResidualDifference struct {
	count int
	index int
	got   uint32
	want  uint32
}

func firstLTPResidualDifference(goResidual []float32, cResult []ltpResidualOracleSample) ltpResidualDifference {
	difference := ltpResidualDifference{index: -1}
	for i := range goResidual {
		if gotBits, wantBits := math.Float32bits(goResidual[i]), math.Float32bits(cResult[i].residual); gotBits != wantBits {
			difference.count++
			if difference.index < 0 {
				difference.index, difference.got, difference.want = i, gotBits, wantBits
			}
		}
	}
	return difference
}

func firstLTPFloatDifference(got, want []float32) ltpResidualDifference {
	difference := ltpResidualDifference{index: -1}
	for i := range got {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			difference.count++
			if difference.index < 0 {
				difference.index, difference.got, difference.want = i, gotBits, wantBits
			}
		}
	}
	return difference
}

func (difference ltpResidualDifference) summary(gotName, wantName string) string {
	if difference.index < 0 {
		return "none"
	}
	return fmt.Sprintf("sample %d %s=0x%08x %s=0x%08x", difference.index, gotName, difference.got, wantName, difference.want)
}

func makeLTPResidualSeparateModel(input ltpResidualOracleInput) []float32 {
	output := make([]float32, input.numSubframes*(input.subframeLength+input.preLength))
	xStart := input.frameStart - input.preLength
	outputLength := input.subframeLength + input.preLength
	index := 0
	for subframe := range input.numSubframes {
		xBase := xStart + subframe*input.subframeLength
		for sample := range outputLength {
			xIndex := xBase + sample
			residual := input.pitchBuffer[xIndex]
			for tap := range ltpOrderConst {
				lagIndex := xIndex - int(input.pitchLags[subframe]) + ltpOrderConst/2 - tap
				product := round32(input.taps[subframe*ltpOrderConst+tap] * input.pitchBuffer[lagIndex])
				residual = round32(residual - product)
			}
			output[index] = round32(residual * input.invGains[subframe])
			index++
		}
	}
	return output
}
