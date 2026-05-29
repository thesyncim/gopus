//go:build gopus_fixedpoint

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	pitchFixedModeInnerProd     = uint32(0)
	pitchFixedModeDualInnerProd = uint32(1)
	pitchFixedModeXcorrKernel   = uint32(2)
	pitchFixedModePitchXcorr    = uint32(3)
)

var pitchFixedHelper libopustest.HelperCache

func buildPitchFixedHelper() (string, error) {
	// Build the oracle against the --enable-fixed-point reference so the
	// integer celt_inner_prod_c / dual_inner_prod_c / xcorr_kernel_c /
	// celt_pitch_xcorr_c kernels are exercised. The libopus static library is
	// linked to resolve celt_fatal (used by the kernels' celt_assert).
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT fixed pitch",
		OutputBase:  "gopus_libopus_celt_pitch_fixed",
		SourceFile:  "libopus_celt_pitch_fixed_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		FixedRef:    true,
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func runPitchFixed(t *testing.T, payload *libopustest.OraclePayload) *libopustest.OracleReader {
	t.Helper()
	binPath, err := pitchFixedHelper.Path(buildPitchFixedHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT fixed pitch", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT fixed pitch", "GPFO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT fixed pitch", err)
	}
	return reader
}

func oracleInnerProd(t *testing.T, x, y []int16) int32 {
	t.Helper()
	p := libopustest.NewOraclePayload("GPFI", pitchFixedModeInnerProd)
	p.U32(uint32(len(x)))
	for _, v := range x {
		p.I16(v)
	}
	for _, v := range y {
		p.I16(v)
	}
	r := runPitchFixed(t, p)
	if mode := r.U32(); mode != pitchFixedModeInnerProd {
		t.Fatalf("inner_prod mode=%d want %d", mode, pitchFixedModeInnerProd)
	}
	got := r.I32()
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return got
}

func oracleDualInnerProd(t *testing.T, x, y01, y02 []int16) (int32, int32) {
	t.Helper()
	p := libopustest.NewOraclePayload("GPFI", pitchFixedModeDualInnerProd)
	p.U32(uint32(len(x)))
	for _, v := range x {
		p.I16(v)
	}
	for _, v := range y01 {
		p.I16(v)
	}
	for _, v := range y02 {
		p.I16(v)
	}
	r := runPitchFixed(t, p)
	if mode := r.U32(); mode != pitchFixedModeDualInnerProd {
		t.Fatalf("dual_inner_prod mode=%d want %d", mode, pitchFixedModeDualInnerProd)
	}
	xy1 := r.I32()
	xy2 := r.I32()
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return xy1, xy2
}

func oracleXcorrKernel(t *testing.T, x, y []int16, length int) [4]int32 {
	t.Helper()
	p := libopustest.NewOraclePayload("GPFI", pitchFixedModeXcorrKernel)
	p.U32(uint32(length))
	for i := 0; i < length; i++ {
		p.I16(x[i])
	}
	for i := 0; i < length+3; i++ {
		p.I16(y[i])
	}
	r := runPitchFixed(t, p)
	if mode := r.U32(); mode != pitchFixedModeXcorrKernel {
		t.Fatalf("xcorr_kernel mode=%d want %d", mode, pitchFixedModeXcorrKernel)
	}
	var out [4]int32
	for i := range out {
		out[i] = r.I32()
	}
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func oraclePitchXcorr(t *testing.T, x, y []int16, length, maxPitch int) (int32, []int32) {
	t.Helper()
	p := libopustest.NewOraclePayload("GPFI", pitchFixedModePitchXcorr)
	p.U32(uint32(length))
	p.U32(uint32(maxPitch))
	for i := 0; i < length; i++ {
		p.I16(x[i])
	}
	for i := 0; i < length+maxPitch-1; i++ {
		p.I16(y[i])
	}
	r := runPitchFixed(t, p)
	if mode := r.U32(); mode != pitchFixedModePitchXcorr {
		t.Fatalf("pitch_xcorr mode=%d want %d", mode, pitchFixedModePitchXcorr)
	}
	maxcorr := r.I32()
	count := int(r.U32())
	if count != maxPitch {
		t.Fatalf("pitch_xcorr count=%d want %d", count, maxPitch)
	}
	xcorr := make([]int32, count)
	for i := range xcorr {
		xcorr[i] = r.I32()
	}
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return maxcorr, xcorr
}

// randI16Big returns int16 samples spread across the full range so that the
// int32 accumulators are exercised near (and across) their wrap boundary,
// confirming that Go's wraparound matches the libopus two's-complement path.
func randI16Big(rng *rand.Rand, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(rng.Intn(65536) - 32768)
	}
	return out
}

func TestCeltInnerProdMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0xC0FFEE))
	for _, n := range []int{1, 2, 3, 4, 7, 8, 15, 16, 31, 64, 120, 240} {
		for trial := 0; trial < 8; trial++ {
			x := randI16Big(rng, n)
			y := randI16Big(rng, n)
			want := oracleInnerProd(t, x, y)
			got := CeltInnerProd(x, y, n)
			if got != want {
				t.Fatalf("CeltInnerProd n=%d trial=%d got=%d want=%d", n, trial, got, want)
			}
		}
	}
}

func TestDualInnerProdMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0xBADF00D))
	for _, n := range []int{1, 3, 4, 7, 16, 64, 240} {
		for trial := 0; trial < 8; trial++ {
			x := randI16Big(rng, n)
			y01 := randI16Big(rng, n)
			y02 := randI16Big(rng, n)
			w1, w2 := oracleDualInnerProd(t, x, y01, y02)
			g1, g2 := DualInnerProd(x, y01, y02, n)
			if g1 != w1 || g2 != w2 {
				t.Fatalf("DualInnerProd n=%d trial=%d got=(%d,%d) want=(%d,%d)", n, trial, g1, g2, w1, w2)
			}
		}
	}
}

func TestXcorrKernelMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x5EED))
	// Cover every residue of len mod 4 to exercise all unrolled tail branches.
	for _, length := range []int{3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 18, 64, 120} {
		for trial := 0; trial < 8; trial++ {
			x := randI16Big(rng, length)
			y := randI16Big(rng, length+3)
			want := oracleXcorrKernel(t, x, y, length)
			var got [4]int32
			XcorrKernel(x, y, &got, length)
			if got != want {
				t.Fatalf("XcorrKernel len=%d trial=%d got=%v want=%v", length, trial, got, want)
			}
		}
	}
}

func TestCeltPitchXcorrMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x1CEB00DA))
	type tc struct{ length, maxPitch int }
	cases := []tc{
		{4, 1}, {4, 2}, {4, 3}, {4, 4}, {4, 5},
		{8, 4}, {16, 8}, {16, 9}, {16, 10}, {16, 11},
		{60, 24}, {120, 48}, {240, 96},
	}
	for _, c := range cases {
		for trial := 0; trial < 6; trial++ {
			x := randI16Big(rng, c.length)
			y := randI16Big(rng, c.length+c.maxPitch-1)
			wantMax, wantXcorr := oraclePitchXcorr(t, x, y, c.length, c.maxPitch)
			gotXcorr := make([]int32, c.maxPitch)
			gotMax := CeltPitchXcorr(x, y, gotXcorr, c.length, c.maxPitch)
			if gotMax != wantMax {
				t.Fatalf("CeltPitchXcorr maxcorr len=%d max_pitch=%d trial=%d got=%d want=%d",
					c.length, c.maxPitch, trial, gotMax, wantMax)
			}
			for i := range gotXcorr {
				if gotXcorr[i] != wantXcorr[i] {
					t.Fatalf("CeltPitchXcorr xcorr[%d] len=%d max_pitch=%d trial=%d got=%d want=%d",
						i, c.length, c.maxPitch, trial, gotXcorr[i], wantXcorr[i])
				}
			}
		}
	}
}
