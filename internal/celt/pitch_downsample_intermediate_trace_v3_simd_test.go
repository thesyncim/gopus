//go:build linux && amd64.v3 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

const libopusCELTPLCModePitchDownsampleTrace = uint32(9)

var libopusCELTPitchKernelTraceHelper libopustest.HelperCache

type libopusPitchDownsampleKernelTrace struct {
	output []float32

	xcorrCalls    uint32
	xcorrLen      uint32
	xcorrMaxPitch uint32
	xcorrArch     uint32
	xcorrX        []float32
	xcorrY        []float32
	xcorrOutput   []float32
	autocorrCalls uint32
	lpcCalls      uint32
	overflow      uint32
	autocorrN     uint32
	autocorrLag   uint32
	autocorrArch  uint32
	windowPresent uint32
	overlap       uint32
	autocorrAC    []float32
	autocorrInput []float32

	lpcOrder  uint32
	lpcAC     []float32
	lpcOutput []float32
}

func buildLibopusCELTPitchKernelTraceHelper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:      "CELT pitch downsample intermediate trace",
		OutputBase: "gopus_libopus_celt_pitch_downsample_trace",
		SourceFile: "libopus_celt_plc_info.c",
		CFlags: []string{
			"-DHAVE_CONFIG_H",
			"-DGOPUS_CELT_PITCH_KERNEL_TRACE=1",
			"-O3",
			"-DNDEBUG",
		},
		LDFlags: []string{
			"-Wl,--wrap=celt_pitch_xcorr_avx2",
			"-Wl,--wrap=_celt_autocorr",
			"-Wl,--wrap=_celt_lpc",
		},
		RefIncludes: []string{"celt", "silk"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return libopustest.BuildCHelper(cfg)
}

func probeLibopusPitchDownsampleKernelTrace(t *testing.T, input []celtSig, length, channels, factor int) libopusPitchDownsampleKernelTrace {
	t.Helper()
	binPath, err := libopusCELTPitchKernelTraceHelper.Path(buildLibopusCELTPitchKernelTraceHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT pitch downsample intermediate trace", err)
	}
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModePitchDownsampleTrace)
	payload.U32(uint32(channels))
	payload.U32(uint32(length))
	payload.U32(uint32(factor))
	for _, sample := range input {
		payload.Float32(float32(sample))
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT pitch downsample intermediate trace", "GCPO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT pitch downsample intermediate trace", err)
	}
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModePitchDownsampleTrace {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModePitchDownsampleTrace)
	}
	outputCount := int(reader.U32())
	if outputCount > 32 {
		t.Fatalf("pitch output trace dimension out of bounds: %d", outputCount)
	}
	trace := libopusPitchDownsampleKernelTrace{output: make([]float32, outputCount)}
	for i := range trace.output {
		trace.output[i] = reader.Float32()
	}
	trace.xcorrCalls = reader.U32()
	trace.xcorrLen = reader.U32()
	trace.xcorrMaxPitch = reader.U32()
	trace.xcorrArch = reader.U32()
	xcorrXCount, xcorrYCount, xcorrOutputCount := int(reader.U32()), int(reader.U32()), int(reader.U32())
	if xcorrXCount > 32 || xcorrYCount > 32 || xcorrOutputCount > 32 {
		t.Fatalf("xcorr trace dimensions out of bounds: x=%d y=%d out=%d", xcorrXCount, xcorrYCount, xcorrOutputCount)
	}
	trace.xcorrX = make([]float32, xcorrXCount)
	for i := range trace.xcorrX {
		trace.xcorrX[i] = reader.Float32()
	}
	trace.xcorrY = make([]float32, xcorrYCount)
	for i := range trace.xcorrY {
		trace.xcorrY[i] = reader.Float32()
	}
	trace.xcorrOutput = make([]float32, xcorrOutputCount)
	for i := range trace.xcorrOutput {
		trace.xcorrOutput[i] = reader.Float32()
	}
	trace.autocorrCalls = reader.U32()
	trace.lpcCalls = reader.U32()
	trace.overflow = reader.U32()
	trace.autocorrN = reader.U32()
	trace.autocorrLag = reader.U32()
	trace.autocorrArch = reader.U32()
	trace.windowPresent = reader.U32()
	trace.overlap = reader.U32()
	autocorrACCount := int(reader.U32())
	if trace.autocorrN > 32 || autocorrACCount > 32 {
		t.Fatalf("autocorr trace dimensions out of bounds: input=%d ac=%d", trace.autocorrN, autocorrACCount)
	}
	trace.autocorrInput = make([]float32, int(trace.autocorrN))
	for i := range trace.autocorrInput {
		trace.autocorrInput[i] = reader.Float32()
	}
	trace.autocorrAC = make([]float32, autocorrACCount)
	for i := range trace.autocorrAC {
		trace.autocorrAC[i] = reader.Float32()
	}
	trace.lpcOrder = reader.U32()
	lpcACCount := int(reader.U32())
	if trace.lpcOrder > 32 || lpcACCount > 32 {
		t.Fatalf("LPC trace dimensions out of bounds: order=%d ac=%d", trace.lpcOrder, lpcACCount)
	}
	reader.ExpectRemaining((lpcACCount + int(trace.lpcOrder)) * 4)
	trace.lpcAC = make([]float32, lpcACCount)
	for i := range trace.lpcAC {
		trace.lpcAC[i] = reader.Float32()
	}
	trace.lpcOutput = make([]float32, int(trace.lpcOrder))
	for i := range trace.lpcOutput {
		trace.lpcOutput[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return trace
}

func TestCELTPitchDownsampleIntermediateTraceDiagnostic(t *testing.T) {
	libopustest.RequireOracle(t)
	requirePairedCELTOracleMode(t)

	const length = 8
	const channels = 1
	const factor = 2
	input := makeCELTPLCTestSignal(length*factor*channels, 0x5f150000+length*4+channels, 2600)
	trace := probeLibopusPitchDownsampleKernelTrace(t, input, length, channels, factor)
	ordinaryC := probeLibopusPLCPitchDownsample(t, input, length, channels, factor)
	if err := requirePitchTraceFloat32BitsEqual("traced C output vs ordinary C", trace.output, ordinaryC); err != nil {
		t.Fatal(err)
	}

	ordinaryGo := make([]float32, length)
	pitchDownsampleSig(input, ordinaryGo, length, channels, factor)
	goStages := replayPitchDownsampleStages(input, length)
	if err := requirePitchTraceFloat32BitsEqual("Go staged replay vs ordinary Go", goStages.output, ordinaryGo); err != nil {
		t.Fatal(err)
	}
	if trace.xcorrCalls != 1 || trace.xcorrLen != 4 || trace.xcorrMaxPitch != 5 ||
		len(trace.xcorrX) != 4 || len(trace.xcorrY) != 8 || len(trace.xcorrOutput) != 5 ||
		trace.autocorrCalls != 1 || trace.lpcCalls != 1 || trace.overflow != 0 ||
		trace.autocorrN != length || trace.autocorrLag != 4 || trace.windowPresent != 0 || trace.overlap != 0 ||
		len(trace.autocorrInput) != length || len(trace.autocorrAC) != 5 ||
		trace.lpcOrder != 4 || len(trace.lpcAC) != 5 || len(trace.lpcOutput) != 4 {
		t.Fatalf("invalid actual C capture: %+v", trace)
	}
	if !finitePitchTrace(trace.autocorrInput) || !finitePitchTrace(trace.autocorrAC) ||
		!finitePitchTrace(trace.lpcAC) || !finitePitchTrace(trace.lpcOutput) {
		t.Fatal("actual C capture contains a non-finite value")
	}

	t.Logf("actual decimated input bits: Go=%08x C-autocorr=%08x C-xcorr-x=%08x C-xcorr-y=%08x",
		float32Bits(goStages.decimated), float32Bits(trace.autocorrInput), float32Bits(trace.xcorrX), float32Bits(trace.xcorrY))
	if err := requirePitchTraceFloat32BitsEqual("Go/C autocorr input", goStages.decimated, trace.autocorrInput); err != nil {
		t.Fatal(err)
	}
	if err := requirePitchTraceFloat32BitsEqual("Go/C xcorr x prefix input", goStages.decimated[:4], trace.xcorrX); err != nil {
		t.Fatal(err)
	}
	if err := requirePitchTraceFloat32BitsEqual("Go/C xcorr y prefix input", goStages.decimated[:8], trace.xcorrY); err != nil {
		t.Fatal(err)
	}
	if index, gotBits, wantBits, ok := firstFloat32BitDifference(goStages.prefix[:], trace.xcorrOutput); ok {
		t.Logf("actual prefix first differs: index=%d Go=%08x C=%08x GoPrefix=%08x CPrefix=%08x", index,
			gotBits, wantBits, float32Bits(goStages.prefix[:]), float32Bits(trace.xcorrOutput))
	} else {
		t.Logf("actual fastN=4 xcorr prefix matches exactly: %08x", float32Bits(trace.xcorrOutput))
	}

	for _, stage := range []struct {
		name string
		goV  []float32
		cV   []float32
	}{
		{name: "decimated autocorr input", goV: goStages.decimated, cV: trace.autocorrInput},
		{name: "raw autocorrelation", goV: goStages.rawAC[:], cV: trace.autocorrAC},
		{name: "lag-windowed LPC input", goV: goStages.windowedAC[:], cV: trace.lpcAC},
		{name: "LPC coefficients", goV: goStages.lpc[:], cV: trace.lpcOutput},
	} {
		if index, gotBits, wantBits, ok := firstFloat32BitDifference(stage.goV, stage.cV); ok {
			t.Logf("first captured Go/C mismatch: stage=%s index=%d Go=%08x C=%08x GoValues=%08x CValues=%08x", stage.name, index,
				gotBits, wantBits, float32Bits(stage.goV), float32Bits(stage.cV))
			if stage.name == "raw autocorrelation" && index == 1 {
				var goTail, cTail float32
				for i := 5; i < length; i++ {
					product := noFMA32Mul(goStages.decimated[i], goStages.decimated[i-1])
					previous := goTail
					goTail = noFMA32Add(goTail, product)
					cTail = opusmath.FMA32(goStages.decimated[i], goStages.decimated[i-1], cTail)
					t.Logf("lag1 tail i=%d x=%08x y=%08x product=%08x GoAcc=%08x->%08x C-FMA-model=%08x", i,
						math.Float32bits(goStages.decimated[i]), math.Float32bits(goStages.decimated[i-1]),
						math.Float32bits(product), math.Float32bits(previous), math.Float32bits(goTail), math.Float32bits(cTail))
				}
				goCombined := noFMA32Add(goStages.prefix[1], goTail)
				cCombined := noFMA32Add(trace.xcorrOutput[1], cTail)
				cTailFromRaw := noFMA32Add(trace.autocorrAC[1], -trace.xcorrOutput[1])
				t.Logf("lag1 prefix=%08x GoTail=%08x C-FMA-tail-model=%08x CRaw=%08x GoRaw=%08x",
					math.Float32bits(goStages.prefix[1]), math.Float32bits(goTail), math.Float32bits(cTail),
					math.Float32bits(trace.autocorrAC[1]), math.Float32bits(goStages.rawAC[1]))
				t.Logf("lag1 combined Go=%08x C-FMA-model=%08x C-tail-from-raw=%08x",
					math.Float32bits(goCombined), math.Float32bits(cCombined), math.Float32bits(cTailFromRaw))
			}
			t.Fatalf("captured %s differs at index %d: Go=%08x C=%08x", stage.name, index, gotBits, wantBits)
		}
	}
	t.Log("all captured decimation, autocorrelation, and LPC stages match; final packet/output gates remain authoritative")
}

type pitchDownsampleReplay struct {
	decimated  []float32
	prefix     [5]float32
	rawAC      [5]float32
	windowedAC [5]float32
	lpc        [4]float32
	output     []float32
}

func replayPitchDownsampleStages(input []celtSig, length int) pitchDownsampleReplay {
	decimated := make([]float32, length)
	decimated[0] = float32(0.25)*float32(input[1]) + float32(0.5)*float32(input[0])
	pitchDownsample2(decimated, input, nil)

	var rawAC [5]float32
	var prefix [5]float32
	pitchXCorrFloat32(decimated, decimated, prefix[:], max(length-4, 0), 5)
	pitchAutocorr5F32(decimated, length, &rawAC)
	windowedAC := rawAC
	applyCELTPitchLagWindow32(windowedAC[:], 4)
	lpc := lpcFromAutocorr32(windowedAC)
	rawLPC := lpc
	decimatedInput := append([]float32(nil), decimated...)

	tmp := float32(1)
	for i := range lpc {
		tmp *= float32(0.9)
		lpc[i] *= tmp
	}
	c1 := float32(0.8)
	lpc2 := [5]float32{
		lpc[0] + float32(0.8),
		lpc[1] + c1*lpc[0],
		lpc[2] + c1*lpc[1],
		lpc[3] + c1*lpc[2],
		c1 * lpc[3],
	}
	celtFIR5F32(decimated, lpc2)
	return pitchDownsampleReplay{
		decimated:  decimatedInput,
		prefix:     prefix,
		rawAC:      rawAC,
		windowedAC: windowedAC,
		lpc:        rawLPC,
		output:     decimated,
	}
}

func requirePitchTraceFloat32BitsEqual(label string, got, want []float32) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s length=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			return fmt.Errorf("%s[%d]=%08x want %08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	return nil
}

func firstFloat32BitDifference(got, want []float32) (int, uint32, uint32, bool) {
	if len(got) != len(want) {
		return min(len(got), len(want)), uint32(len(got)), uint32(len(want)), true
	}
	for i := range got {
		gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i])
		if gotBits != wantBits {
			return i, gotBits, wantBits, true
		}
	}
	return 0, 0, 0, false
}

func float32Bits(values []float32) []uint32 {
	bits := make([]uint32, len(values))
	for i, value := range values {
		bits[i] = math.Float32bits(value)
	}
	return bits
}

func finitePitchTrace(values []float32) bool {
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}
