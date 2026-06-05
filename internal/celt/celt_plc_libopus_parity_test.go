package celt

import (
	"math"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusCELTPLCModeLPC             = uint32(0)
	libopusCELTPLCModeFIR             = uint32(1)
	libopusCELTPLCModeIIR             = uint32(2)
	libopusCELTPLCModePitchDownsample = uint32(3)
	libopusCELTPLCModePitchSearch     = uint32(4)
	libopusCELTPLCModeRemoveDoubling  = uint32(5)
	libopusCELTPLCModePeriodicConceal = uint32(6)
)

var libopusCELTPLCHelper libopustest.HelperCache

func buildLibopusCELTPLCHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT PLC",
		OutputBase:  "gopus_libopus_celt_plc",
		SourceFile:  "libopus_celt_plc_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func runLibopusCELTPLC(t *testing.T, payload *libopustest.OraclePayload) *libopustest.OracleReader {
	t.Helper()
	binPath, err := libopusCELTPLCHelper.Path(buildLibopusCELTPLCHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT PLC", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT PLC", "GCPO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT PLC", err)
	}
	return reader
}

type libopusPLCLPCResult struct {
	lpc []float32
	ac  []float32
}

func probeLibopusPLCLPC(t *testing.T, frame []celtSig, window []float32) libopusPLCLPCResult {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeLPC)
	payload.U32(uint32(len(frame)))
	payload.U32(uint32(len(window)))
	for _, w := range window {
		payload.Float32(w)
	}
	for _, sample := range frame {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeLPC {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeLPC)
	}
	lpcCount := int(reader.U32())
	lpc := make([]float32, lpcCount)
	for i := range lpc {
		lpc[i] = reader.Float32()
	}
	acCount := int(reader.U32())
	ac := make([]float32, acCount)
	reader.ExpectRemaining(acCount * 4)
	for i := range ac {
		ac[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return libopusPLCLPCResult{lpc: lpc, ac: ac}
}

func probeLibopusPLCFIR(t *testing.T, exc []float32, start, length int, lpc []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeFIR)
	payload.U32(uint32(len(exc)))
	payload.U32(uint32(start))
	payload.U32(uint32(length))
	for _, coeff := range lpc {
		payload.Float32(coeff)
	}
	for _, sample := range exc {
		payload.Float32(sample)
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeFIR {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeFIR)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func probeLibopusPLCIIR(t *testing.T, x []celtSig, hist []celtSig, lpc []float32) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeIIR)
	payload.U32(uint32(len(x)))
	payload.U32(uint32(len(hist)))
	for _, coeff := range lpc {
		payload.Float32(coeff)
	}
	for _, sample := range hist {
		payload.Float32(float32(sample))
	}
	for _, sample := range x {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeIIR {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeIIR)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func probeLibopusPLCPitchDownsample(t *testing.T, x []celtSig, length, channels, factor int) []float32 {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModePitchDownsample)
	payload.U32(uint32(channels))
	payload.U32(uint32(length))
	payload.U32(uint32(factor))
	for _, sample := range x {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModePitchDownsample {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModePitchDownsample)
	}
	return readCELTPLCFloat32Vector(t, reader)
}

func probeLibopusPLCPitchSearch(t *testing.T, xLP, y []float32, length, maxPitch int) int {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModePitchSearch)
	payload.U32(uint32(length))
	payload.U32(uint32(maxPitch))
	for _, sample := range xLP[:length] {
		payload.Float32(sample)
	}
	for _, sample := range y[:length+maxPitch] {
		payload.Float32(sample)
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModePitchSearch {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModePitchSearch)
	}
	out := int(int32(reader.U32()))
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func probeLibopusRemoveDoubling(t *testing.T, x []float32, maxPeriod, minPeriod, n, t0, prevPeriod int, prevGain float32) (int, float32) {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModeRemoveDoubling)
	payload.U32(uint32(len(x)))
	payload.U32(uint32(maxPeriod))
	payload.U32(uint32(minPeriod))
	payload.U32(uint32(n))
	payload.U32(uint32(int32(t0)))
	payload.U32(uint32(prevPeriod))
	payload.Float32(prevGain)
	for _, sample := range x {
		payload.Float32(sample)
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModeRemoveDoubling {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModeRemoveDoubling)
	}
	outT0 := int(int32(reader.U32()))
	gain := reader.Float32()
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return outT0, gain
}

type libopusPLCPeriodicConcealResult struct {
	period int
	out    [][]float32
}

func probeLibopusPLCPeriodicConceal(t *testing.T, hist []celtSig, channels, frameSize int) libopusPLCPeriodicConcealResult {
	t.Helper()
	payload := libopustest.NewOraclePayload("GCPI", libopusCELTPLCModePeriodicConceal)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(Overlap))
	payload.U32(0)
	payload.U32(0)
	for _, w := range GetWindowBufferF32(Overlap) {
		payload.Float32(w)
	}
	for _, sample := range hist {
		payload.Float32(float32(sample))
	}
	reader := runLibopusCELTPLC(t, payload)
	if gotMode := reader.U32(); gotMode != libopusCELTPLCModePeriodicConceal {
		t.Fatalf("helper mode=%d want %d", gotMode, libopusCELTPLCModePeriodicConceal)
	}
	period := int(reader.U32())
	count := int(reader.U32())
	out := make([][]float32, channels)
	reader.ExpectRemaining(channels * count * 4)
	for ch := range channels {
		out[ch] = make([]float32, count)
		for i := range out[ch] {
			out[ch][i] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return libopusPLCPeriodicConcealResult{period: period, out: out}
}

func readCELTPLCFloat32Vector(t *testing.T, reader *libopustest.OracleReader) []float32 {
	t.Helper()
	count := int(reader.U32())
	reader.ExpectRemaining(count * 4)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConcealPeriodicPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	for _, tc := range []struct {
		name      string
		frameSize int
		channels  int
	}{
		{name: "frame_120_mono", frameSize: 120, channels: 1},
		{name: "frame_120_stereo", frameSize: 120, channels: 2},
		{name: "frame_240_mono", frameSize: 240, channels: 1},
		{name: "frame_480_stereo", frameSize: 480, channels: 2},
		{name: "frame_960_mono", frameSize: 960, channels: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hist := makeCELTPLCTestSignal(plcDecodeBufferSize*tc.channels, 0x9000+uint32(tc.frameSize)+uint32(tc.channels), 2400)
			want := probeLibopusPLCPeriodicConceal(t, hist, tc.channels, tc.frameSize)

			dec := NewDecoder(tc.channels)
			dec.plcDecodeMem = append(dec.plcDecodeMem[:0], hist...)
			dec.plcLPC = make([]float32, celtPLCLPCOrder*tc.channels)
			gotInterleaved := make([]float32, (tc.frameSize+Overlap)*tc.channels)
			if !dec.concealPeriodicPLC(gotInterleaved, tc.frameSize, 1, false, false) {
				t.Fatal("concealPeriodicPLC returned false")
			}
			if int(dec.plcLastPitchPeriod) != want.period {
				t.Fatalf("period=%d want libopus %d", dec.plcLastPitchPeriod, want.period)
			}
			for ch := 0; ch < tc.channels; ch++ {
				for i, wantSample := range want.out[ch] {
					got := float32(gotInterleaved[i*tc.channels+ch])
					if math.Float32bits(got) != math.Float32bits(wantSample) {
						t.Fatalf("ch=%d out[%d]=%08x %.10g want %08x %.10g",
							ch, i,
							math.Float32bits(got), got,
							math.Float32bits(wantSample), wantSample)
					}
				}
			}
		})
	}
}

func TestPitchDownsampleSigMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			const length = plcDecodeBufferSize >> 1
			x := makeCELTPLCTestSignal(length*2*channels, uint32(0x5100+channels), 2600)
			want := probeLibopusPLCPitchDownsample(t, x, length, channels, 2)

			got := make([]float32, length)
			pitchDownsampleSig(x, got, length, channels, 2)
			assertFloat32Bits(t, "xLP", got, want)
		})
	}
}

func TestPitchDownsampleFloatInputMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			const length = (combFilterMaxPeriod + 480) >> 1
			xSig := makeCELTPLCTestSignal(length*2*channels, uint32(0x6100+channels), 2100)
			x := make([]float64, len(xSig))
			for i := range xSig {
				x[i] = float64(xSig[i])
			}
			want := probeLibopusPLCPitchDownsample(t, xSig, length, channels, 2)

			got := make([]float32, length)
			pitchDownsample(x, got, length, channels, 2)
			assertFloat32Bits(t, "xLP", got, want)
		})
	}
}

func TestPitchSearchPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		length   = plcDecodeBufferSize - 720
		maxPitch = 720 - 100
	)
	y := make([]float32, length+maxPitch)
	xLP := make([]float32, length)
	src := makeCELTPLCTestSignal((length+maxPitch)*2, 0x71ab23cd, 1.0)
	for i := range y {
		y[i] = float32(src[i])
	}
	copy(xLP, y[720/2:])

	var scratch plcPitchSearchScratch
	scratch.xcorr = make([]float32, maxPitch>>1)
	for i := range scratch.xcorr {
		scratch.xcorr[i] = float32(math.NaN())
	}
	got := pitchSearchPLC(xLP, y, length, maxPitch, &scratch)
	want := probeLibopusPLCPitchSearch(t, xLP, y, length, maxPitch)
	if got != want {
		t.Fatalf("pitch=%d want %d", got, want)
	}
}

func TestRemoveDoublingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

	cases := []struct {
		name       string
		seed       uint32
		maxPeriod  int
		minPeriod  int
		n          int
		t0         int
		prevPeriod int
		prevGain   float32
	}{
		{"fullband_low_prev", 0x9201, combFilterMaxPeriod, combFilterMinPeriod, 960, 231, 197, 0.375},
		{"medium_continuity", 0x9202, 640, 30, 480, 188, 190, 0.5625},
		{"short_period", 0x9203, 320, combFilterMinPeriod, 240, 77, 80, 0.21875},
		{"wide_search", 0x9204, 900, 40, 720, 415, 300, 0.6875},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xSig := makeCELTPLCTestSignal(tc.maxPeriod+tc.n, tc.seed, 1.0)
			x := make([]float32, len(xSig))
			for i := range xSig {
				x[i] = float32(xSig[i])
			}
			wantT0, wantGain := probeLibopusRemoveDoubling(t, x, tc.maxPeriod, tc.minPeriod, tc.n, tc.t0, tc.prevPeriod, tc.prevGain)
			gotT0 := tc.t0
			var scratch encoderScratch
			gotGain := removeDoubling(x, tc.maxPeriod, tc.minPeriod, tc.n, &gotT0, tc.prevPeriod, tc.prevGain, &scratch)
			if gotT0 != wantT0 {
				t.Fatalf("pitch=%d want %d", gotT0, wantT0)
			}
			if math.Float32bits(gotGain) != math.Float32bits(wantGain) {
				t.Fatalf("gain=%08x %0.10g want %08x %0.10g",
					math.Float32bits(gotGain), gotGain,
					math.Float32bits(wantGain), wantGain)
			}
		})
	}
}

func TestComputePLCLPCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name  string
		seed  uint32
		scale float32
	}{
		{name: "baseline", seed: 0x42504c43, scale: 1800},
		{name: "plc_frame_120_stereo_ch0", seed: 0x9000 + 120 + 2, scale: 2400},
		{name: "plc_frame_240_stereo_ch0", seed: 0x9000 + 240 + 2, scale: 2400},
		{name: "plc_frame_480_mono", seed: 0x9000 + 480 + 1, scale: 2400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hist := makeCELTPLCTestSignal(plcDecodeBufferSize, tc.seed, tc.scale)
			frame := hist[plcDecodeBufferSize-combFilterMaxPeriod:]
			window32 := GetWindowBufferF32(Overlap)
			want := probeLibopusPLCLPC(t, frame, window32)

			dec := NewDecoder(1)
			gotAC := make([]float32, celtPLCLPCOrder+1)
			dec.computePLCAutocorr(frame, window32, gotAC)
			assertFloat32Bits(t, "ac", gotAC, want.ac)

			got := make([]float32, celtPLCLPCOrder)
			dec.computePLCLPC(frame, got, window32)
			assertFloat32Bits(t, "lpc", got, want.lpc)
		})
	}
}

func TestCELTPLCFIRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	exc := makeCELTPLCTestSignal(combFilterMaxPeriod+celtPLCLPCOrder, 0xf17f1e, 1400)
	exc32 := make([]float32, len(exc))
	for i := range exc {
		exc32[i] = float32(exc[i])
	}
	lpc := makeCELTPLPCTestCoeffs()
	for _, length := range []int{200, 320, 360, 600, 720, 1024} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			start := celtPLCLPCOrder + combFilterMaxPeriod - length
			want := probeLibopusPLCFIR(t, exc32, start, length, lpc)

			gotSig := make([]celtSig, length)
			celtFIRFloat32(gotSig, exc32, start, length, lpc)
			got := make([]float32, len(gotSig))
			copySigToFloat32(got, gotSig)
			assertFloat32Bits(t, "fir", got, want)
		})
	}
}

func TestCELTPLCIIRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	hist := makeCELTPLCTestSignal(plcDecodeBufferSize, 0x11a5011, 1300)
	lpc := makeCELTPLPCTestCoeffs()
	for _, length := range []int{240, 360, 600, 1080} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			in := makeCELTPLCTestSignal(length, 0x119911+uint32(length), 900)
			want := probeLibopusPLCIIR(t, in, hist, lpc)

			dec := NewDecoder(1)
			gotSig := append([]celtSig(nil), in...)
			dec.celtIIRFloat32(gotSig, hist, lpc, len(gotSig))
			got := make([]float32, len(gotSig))
			copySigToFloat32(got, gotSig)
			assertFloat32Bits(t, "iir", got, want)
		})
	}
}

func TestCELTPLCFIRActualPeriodicPLCInputsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name      string
		frameSize int
		channels  int
	}{
		{name: "frame_120_stereo_ch0", frameSize: 120, channels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			histAll := makeCELTPLCTestSignal(plcDecodeBufferSize*tc.channels, 0x9000+uint32(tc.frameSize)+uint32(tc.channels), 2400)
			hist := histAll[:plcDecodeBufferSize]

			dec := NewDecoder(tc.channels)
			dec.plcDecodeMem = append(dec.plcDecodeMem[:0], histAll...)
			period := dec.searchPLCPitchPeriod()
			if period <= 0 {
				t.Fatal("no PLC pitch period")
			}

			lpc := make([]float32, celtPLCLPCOrder)
			dec.computePLCLPC(hist[plcDecodeBufferSize-combFilterMaxPeriod:], lpc, GetWindowBufferF32(Overlap))

			const maxPeriod = combFilterMaxPeriod
			excLength := min(2*period, maxPeriod)
			exc := makeCELTPLCTestSignal(0, 0, 0)
			exc = append(exc, hist[plcDecodeBufferSize-maxPeriod-celtPLCLPCOrder:]...)
			exc32 := make([]float32, len(exc))
			for i := range exc {
				exc32[i] = float32(exc[i])
			}
			firStart := celtPLCLPCOrder + maxPeriod - excLength
			wantFIR := probeLibopusPLCFIR(t, exc32, firStart, excLength, lpc)
			firTmp := make([]celtSig, excLength)
			celtFIRFloat32(firTmp, exc32, firStart, excLength, lpc)
			gotFIR := make([]float32, len(firTmp))
			copySigToFloat32(gotFIR, firTmp)
			assertFloat32Bits(t, "actual periodic fir", gotFIR, wantFIR)
		})
	}
}

func makeCELTPLCTestSignal(n int, seed uint32, scale float32) []celtSig {
	out := make([]celtSig, n)
	state := seed
	for i := range out {
		state = 1664525*state + 1013904223
		centered := float32(int32(state>>9)-int32(1<<22)) / float32(1<<22)
		wave := float32(math.Sin(float64(i+3)*0.037))*0.41 + float32(math.Cos(float64(i+7)*0.019))*0.23
		out[i] = celtSig((centered*0.36 + wave) * scale)
	}
	return out
}

func makeCELTPLPCTestCoeffs() []float32 {
	coeffs := make([]float32, celtPLCLPCOrder)
	for i := range coeffs {
		sign := float32(1)
		if i%2 != 0 {
			sign = -1
		}
		coeffs[i] = sign * float32(0.018/(1+float64(i)))
	}
	return coeffs
}
