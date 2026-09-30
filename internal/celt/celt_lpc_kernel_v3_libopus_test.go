//go:build amd64.v3 && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

const (
	celtLPCKernelOrder       = 4
	celtLPCPositiveCaseCount = 16
	celtLPCBoundaryCaseCount = 6
	celtLPCPLCCaseIndex      = celtLPCPositiveCaseCount + celtLPCBoundaryCaseCount
	celtLPCAllOrdersStart    = celtLPCPLCCaseIndex + 1
)

var celtLPCKernelHelper libopustest.HelperCache

func buildCELTLPCKernelV3Helper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "CELT v3 LPC kernel",
		OutputBase:  "gopus_libopus_celt_lpc_kernel_v3",
		SourceFile:  "libopus_celt_lpc_kernel.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return libopustest.BuildCHelper(cfg)
}

func celtLPCKernelPositiveDefiniteCases() [][]float32 {
	cases := make([][]float32, celtLPCPositiveCaseCount)
	state := uint32(0x4c50434b)
	// With ac[0]=10 and 2*sum(abs(ac[1:])) < 9.32 for every case, each
	// symmetric Toeplitz matrix is strictly diagonally dominant and positive
	// definite. The non-grid lag values exercise rounded LPC updates.
	for caseIndex := range cases {
		ac := make([]float32, celtLPCKernelOrder+1)
		ac[0] = 10
		for lag := 1; lag <= celtLPCKernelOrder; lag++ {
			state = 1664525*state + 1013904223
			var bits uint32
			switch lag {
			case 1:
				bits = 0x40000000 | (state & 0x007fffff) // [2, 4)
			case 2:
				bits = 0x3e800000 | (state & 0x003fffff) // [0.25, 0.375)
			case 3:
				bits = 0x3e000000 | (state & 0x003fffff) // [0.125, 0.1875)
			default:
				bits = 0x3d800000 | (state & 0x003fffff) // [0.0625, 0.09375)
			}
			if state&0x100 != 0 {
				bits |= 0x80000000
			}
			ac[lag] = math.Float32frombits(bits)
		}
		cases[caseIndex] = ac
	}
	return cases
}

func celtLPCKernelAllOrderCases() [][]float32 {
	cases := make([][]float32, celtPLCLPCOrder)
	state := uint32(0x504c434c)
	for order := 1; order <= celtPLCLPCOrder; order++ {
		ac := make([]float32, order+1)
		ac[0] = 10
		for lag := 1; lag <= order; lag++ {
			state = 1664525*state + 1013904223
			bits := uint32(0x3ca00000) | (state & 0x003fffff) // [0.0195, 0.0215)
			if state&0x100 != 0 {
				bits |= 0x80000000
			}
			ac[lag] = math.Float32frombits(bits)
		}
		// The absolute lag sum stays below 0.53, so each Toeplitz matrix is
		// strictly diagonally dominant with ac[0]=10.
		cases[order-1] = ac
	}
	return cases
}

func celtLPCKernelBoundaryCases() [][]float32 {
	return [][]float32{
		{0, 0},
		{1e-12, 5e-13},
		{float32(1e-10), 0},
		{float32(2e-10), float32(5e-11)},
		{math.Float32frombits(0x7fc00000), 0},
		{1, 0.9999, 0.5},
	}
}

func celtLPCKernelBoundaryCaseName(index int) string {
	return [...]string{
		"zero_energy",
		"tiny_positive_energy",
		"at_energy_guard",
		"above_energy_guard",
		"qnan_energy",
		"early_exit_30db",
	}[index]
}

func celtLPCKernelCases(t *testing.T, variant libopustooling.LibopusReferenceVariant) ([][]float32, []float32, []float32, []float32) {
	t.Helper()
	cases := celtLPCKernelPositiveDefiniteCases()
	var captured [celtLPCKernelOrder + 1]uint32
	switch variant {
	case libopustooling.LibopusReferenceScalar:
		captured = [celtLPCKernelOrder + 1]uint32{
			0x4f238f01, 0x4f12b99e, 0x4ec9e0fd, 0x4e1965c1, 0xcdc51c42,
		}
	case libopustooling.LibopusReferenceSIMD:
		captured = [celtLPCKernelOrder + 1]uint32{
			0x4f238f01, 0x4f12b998, 0x4ec9e109, 0x4e1965b7, 0xcdc51c3b,
		}
	default:
		panic("unsupported CELT LPC oracle variant")
	}
	actual := make([]float32, celtLPCKernelOrder+1)
	for i, bits := range captured {
		actual[i] = math.Float32frombits(bits)
	}
	// Carry a live order-24 PLC autocorrelation through the direct LPC helper.
	// This vector is the actual input returned by the pinned PLC C helper.
	frame := makeCELTPLCTestSignal(combFilterMaxPeriod, 0x42504c43, 1800)
	window := GetWindowBufferF32(Overlap)
	plc := probeLibopusPLCLPC(t, frame, window)
	if len(plc.ac) != celtPLCLPCOrder+1 || len(plc.lpc) != celtPLCLPCOrder {
		t.Fatalf("live PLC C LPC geometry: ac=%d lpc=%d, want %d/%d",
			len(plc.ac), len(plc.lpc), celtPLCLPCOrder+1, celtPLCLPCOrder)
	}
	var goAC [celtPLCLPCOrder + 1]float32
	NewDecoder(1).computePLCAutocorr(frame, window, goAC[:])
	cases = append(cases, celtLPCKernelBoundaryCases()...)
	cases = append(cases, plc.ac)
	cases = append(cases, celtLPCKernelAllOrderCases()...)
	// These five values are the matching Go/C post-lag-window LPC inputs from
	// the frame95 pitch_downsample capture, carried as a same-input kernel case.
	return append(cases, actual), plc.ac, goAC[:], plc.lpc
}

func probeCELTLPCKernelV3(t *testing.T, helper string, cases [][]float32) [][]float32 {
	t.Helper()
	payload := libopustest.NewOraclePayloadVersion("GCLK", 1, uint32(len(cases)))
	for _, ac := range cases {
		payload.U32(uint32(len(ac) - 1))
		payload.Float32s(ac...)
	}
	reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "CELT v3 LPC kernel", "GCKO", 1)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT v3 LPC kernel", err)
	}
	if got := reader.Count(len(cases)); got != len(cases) {
		t.Fatalf("C LPC output count=%d want %d", got, len(cases))
	}
	want := make([][]float32, len(cases))
	for caseIndex := range cases {
		order := len(cases[caseIndex]) - 1
		if got := reader.Count(order); got != order {
			t.Fatalf("case %d C LPC order=%d want %d", caseIndex, got, order)
		}
		want[caseIndex] = make([]float32, order)
		for i := range want[caseIndex] {
			want[caseIndex][i] = reader.Float32()
		}
	}
	if err := reader.Err(); err != nil {
		t.Fatalf("C LPC output: %v", err)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return want
}

func celtLPCSeparateErrorUpdate32(r, errorPower float32) float32 {
	// celt/celt_lpc.c::_celt_lpc rounds r*r, then the product with error,
	// before subtracting it from error. Keep each C-float boundary explicit.
	square := mul32(r, r)
	decay := mul32(square, errorPower)
	return sub32(errorPower, decay)
}

//go:noinline
func celtLPCModelFMA32(a, b, c float32) float32 {
	return a*b + c
}

func celtLPCReflectionSumReference32(coeffs, ac []float32, i int, simdTail bool) float32 {
	rr := float32(0)
	prefix := i
	if simdTail {
		// The paired x86 v3 kernel rounds each complete four-term product
		// group before its ordered scalar adds; the final zero-to-three terms
		// use contracted scalar multiply-adds.
		prefix &= ^3
	}
	for j := 0; j < prefix; j++ {
		product := mul32(coeffs[j], ac[i-j])
		rr = add32(rr, product)
	}
	for j := prefix; j < i; j++ {
		rr = celtLPCModelFMA32(coeffs[j], ac[i-j], rr)
	}
	return rr
}

func celtLPCSeparateErrorReference32(ac []float32, order int, simdTail bool) []float32 {
	var coeffs [celtPLCLPCOrder]float32
	out := make([]float32, order)
	if len(ac) < order+1 || !(ac[0] > 1e-10) {
		return out
	}
	base := ac[0]
	errorPower := base
	for i := range order {
		rr := celtLPCReflectionSumReference32(coeffs[:], ac, i, simdTail)
		rr = add32(rr, ac[i+1])
		r := -(rr / errorPower)
		coeffs[i] = r
		for j := 0; j < (i+1)>>1; j++ {
			tmp1 := coeffs[j]
			tmp2 := coeffs[i-1-j]
			coeffs[j] = fma32(r, tmp2, tmp1)
			coeffs[i-1-j] = fma32(r, tmp1, tmp2)
		}
		errorPower = celtLPCSeparateErrorUpdate32(r, errorPower)
		if errorPower <= mul32(0.001, base) {
			break
		}
	}
	copy(out, coeffs[:order])
	return out
}

func celtLPCFloat32Bits(values []float32) []uint32 {
	bits := make([]uint32, len(values))
	for i, value := range values {
		bits[i] = math.Float32bits(value)
	}
	return bits
}

func celtLPCFloat32BitsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

func TestCELTV3LPCMatchesLinkedLibopusAndSeparateErrorUpdate(t *testing.T) {
	requireCELTV3OracleTarget(t)
	variant := requirePairedCELTOracleMode(t)
	if variant != libopustooling.LibopusReferenceScalar && variant != libopustooling.LibopusReferenceSIMD {
		t.Fatalf("paired CELT LPC reference=%s, want scalar or SIMD", variant)
	}
	libopustest.RequireOracle(t)

	helper, err := celtLPCKernelHelper.Path(buildCELTLPCKernelV3Helper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT v3 LPC kernel", err)
	}
	cases, livePLCAutocorrC, livePLCAutocorrGo, livePLCLPC := celtLPCKernelCases(t, variant)
	want := probeCELTLPCKernelV3(t, helper, cases)
	earlyExitIndex := celtLPCPositiveCaseCount + celtLPCBoundaryCaseCount - 1
	if want[earlyExitIndex][1] != 0 {
		t.Fatalf("30 dB early-exit case produced LPC[1]=%08x, want zero", math.Float32bits(want[earlyExitIndex][1]))
	}
	liveInputMatches := celtLPCFloat32BitsEqual(livePLCAutocorrGo, livePLCAutocorrC)
	if !liveInputMatches {
		t.Logf("live PLC order-24 ACF input: C=%08x Go=%08x", celtLPCFloat32Bits(livePLCAutocorrC), celtLPCFloat32Bits(livePLCAutocorrGo))
	}
	liveKernelMatches := celtLPCFloat32BitsEqual(want[celtLPCPLCCaseIndex], livePLCLPC)
	if !liveKernelMatches {
		t.Logf("live PLC order-24 C kernel output: PLC=%08x direct _celt_lpc=%08x",
			celtLPCFloat32Bits(livePLCLPC), celtLPCFloat32Bits(want[celtLPCPLCCaseIndex]))
	}
	caseName := func(index int) string {
		if index == len(cases)-1 {
			return "frame95_actual"
		}
		if index == celtLPCPLCCaseIndex {
			return "plc24_live_acf"
		}
		if index >= celtLPCPositiveCaseCount && index < celtLPCPLCCaseIndex {
			return celtLPCKernelBoundaryCaseName(index - celtLPCPositiveCaseCount)
		}
		if index >= celtLPCAllOrdersStart {
			return fmt.Sprintf("order_%02d", index-celtLPCAllOrdersStart+1)
		}
		return fmt.Sprintf("synthetic_%02d", index)
	}
	var firstProductionMismatch string
	var firstModelMismatch string
	productionMismatches := 0
	modelMismatches := 0
	for caseIndex, ac := range cases {
		order := len(ac) - 1
		got := make([]float32, order)
		plcLPCFromAutocorr(ac, got)
		separate := celtLPCSeparateErrorReference32(ac, order,
			variant == libopustooling.LibopusReferenceSIMD)
		if caseIndex == celtLPCPLCCaseIndex {
			t.Logf("live PLC order-24 LPC: acC=%08x acGo=%08x C=%08x Go=%08x model=%08x",
				celtLPCFloat32Bits(ac), celtLPCFloat32Bits(livePLCAutocorrGo),
				celtLPCFloat32Bits(want[caseIndex]), celtLPCFloat32Bits(got), celtLPCFloat32Bits(separate))
		}
		if caseIndex == len(cases)-1 {
			t.Logf("frame95 actual LPC: ac=%08x C=%08x Go=%08x separate-error-model=%08x",
				celtLPCFloat32Bits(ac), celtLPCFloat32Bits(want[caseIndex]),
				celtLPCFloat32Bits(got), celtLPCFloat32Bits(separate))
		}
		for i := range got {
			wantBits := math.Float32bits(want[caseIndex][i])
			if gotBits := math.Float32bits(got[i]); gotBits != wantBits {
				productionMismatches++
				if firstProductionMismatch == "" {
					firstProductionMismatch = fmt.Sprintf("%s Go LPC[%d]=%08x want linked _celt_lpc %08x",
						caseName(caseIndex), i, gotBits, wantBits)
				}
			}
			if modelBits := math.Float32bits(separate[i]); modelBits != wantBits {
				modelMismatches++
				if firstModelMismatch == "" {
					firstModelMismatch = fmt.Sprintf("%s separate-error model LPC[%d]=%08x want linked _celt_lpc %08x",
						caseName(caseIndex), i, modelBits, wantBits)
				}
			}
		}
	}
	if modelMismatches != 0 || productionMismatches != 0 || !liveInputMatches || !liveKernelMatches {
		t.Fatalf("linked _celt_lpc comparisons: production mismatches=%d (first: %s); separate-error model mismatches=%d (first: %s); live PLC ACF Go=C=%t; live PLC C kernel=direct kernel=%t",
			productionMismatches, firstProductionMismatch, modelMismatches, firstModelMismatch, liveInputMatches, liveKernelMatches)
	}
}

func TestCELTV3PLCLPCZeroAllocs(t *testing.T) {
	var ac [celtPLCLPCOrder + 1]float32
	var lpc [celtPLCLPCOrder]float32
	ac[0] = 10
	for lag := 1; lag <= celtPLCLPCOrder; lag++ {
		ac[lag] = float32(lag) * 0.001
	}
	plcLPCFromAutocorr(ac[:], lpc[:])
	if got := testing.AllocsPerRun(100, func() {
		plcLPCFromAutocorr(ac[:], lpc[:])
	}); got != 0 {
		t.Fatalf("plcLPCFromAutocorr allocations=%v, want 0", got)
	}
}
