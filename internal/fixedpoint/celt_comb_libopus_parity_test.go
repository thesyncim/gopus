//go:build gopus_fixed_point

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	combFixedModeConst = uint32(0)
	combFixedModeFull  = uint32(1)
)

var combFixedHelper libopustest.HelperCache

func buildCombFixedHelper() (string, error) {
	// Build against the --enable-fixed-point reference so comb_filter (and the
	// reproduced comb_filter_const_c) run the integer celt_coef=opus_val16
	// path. libopus.a is linked to resolve comb_filter and its dependencies.
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT fixed comb",
		OutputBase:  "gopus_libopus_celt_comb_fixed",
		SourceFile:  "libopus_celt_comb_fixed_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		FixedRef:    true,
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func runCombFixed(t *testing.T, payload *libopustest.OraclePayload) *libopustest.OracleReader {
	t.Helper()
	binPath, err := combFixedHelper.Path(buildCombFixedHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT fixed comb", err)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT fixed comb", "GCFO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT fixed comb", err)
	}
	return reader
}

// oracleCombConst runs comb_filter_const_c over the processed region
// x[history:history+n], where x carries `history` samples of pitch history.
func oracleCombConst(t *testing.T, x []int32, history, n int, tt int32, g10, g11, g12 int16) []int32 {
	t.Helper()
	p := libopustest.NewOraclePayload("GCFI", combFixedModeConst)
	p.U32(uint32(history))
	p.U32(uint32(n))
	p.I32(tt)
	p.I16(g10)
	p.I16(g11)
	p.I16(g12)
	for _, v := range x {
		p.I32(v)
	}
	r := runCombFixed(t, p)
	if mode := r.U32(); mode != combFixedModeConst {
		t.Fatalf("comb_const mode=%d want %d", mode, combFixedModeConst)
	}
	count := int(r.U32())
	if count != n {
		t.Fatalf("comb_const count=%d want %d", count, n)
	}
	out := make([]int32, n)
	for i := range out {
		out[i] = r.I32()
	}
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

func oracleCombFull(t *testing.T, x []int32, history, n, overlap int, t0, t1 int32, g0, g1 int16, tapset0, tapset1 int, window []int16) []int32 {
	t.Helper()
	p := libopustest.NewOraclePayload("GCFI", combFixedModeFull)
	p.U32(uint32(history))
	p.U32(uint32(n))
	p.U32(uint32(overlap))
	p.I32(t0)
	p.I32(t1)
	p.I16(g0)
	p.I16(g1)
	p.U32(uint32(tapset0))
	p.U32(uint32(tapset1))
	for _, v := range x {
		p.I32(v)
	}
	for i := 0; i < overlap; i++ {
		p.I16(window[i])
	}
	r := runCombFixed(t, p)
	if mode := r.U32(); mode != combFixedModeFull {
		t.Fatalf("comb mode=%d want %d", mode, combFixedModeFull)
	}
	count := int(r.U32())
	if count != n {
		t.Fatalf("comb count=%d want %d", count, n)
	}
	out := make([]int32, n)
	for i := range out {
		out[i] = r.I32()
	}
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return out
}

// randSig returns int32 signal samples within [-SIG_SAT, SIG_SAT] so that the
// post-filter accumulators stay in the regime the comb filter actually sees,
// while still exercising the saturation clamp at the extremes.
func randSig(rng *rand.Rand, n int) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(rng.Intn(2*sigSat+1) - sigSat)
	}
	return out
}

// randWindow returns a monotone Q15 overlap window in [0, 32767], mirroring the
// shape of the real CELT overlap window enough to drive the cross-fade math.
func randWindow(rng *rand.Rand, overlap int) []int16 {
	out := make([]int16, overlap)
	for i := range out {
		out[i] = int16(rng.Intn(32768))
	}
	return out
}

func TestCombFilterConstMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0xC0FFEEEE))
	periods := []int32{15, 16, 33, 64, 100, 720}
	lengths := []int{1, 2, 3, 4, 5, 9, 16, 60, 120, 240}
	for _, tt := range periods {
		for _, n := range lengths {
			for trial := 0; trial < 6; trial++ {
				history := int(tt) + 2
				x := randSig(rng, history+n)
				g10 := int16(rng.Intn(32768))
				g11 := int16(rng.Intn(32768))
				g12 := int16(rng.Intn(32768))
				want := oracleCombConst(t, x, history, n, tt, g10, g11, g12)
				y := make([]int32, history+n)
				CombFilterConst(y, x, history, int(tt), n, g10, g11, g12)
				for i := 0; i < n; i++ {
					if y[history+i] != want[i] {
						t.Fatalf("CombFilterConst T=%d N=%d trial=%d y[%d]=%d want=%d",
							tt, n, trial, i, y[history+i], want[i])
					}
				}
			}
		}
	}
}

func TestCombFilterMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0xBADC0DE5))
	type tc struct {
		t0, t1           int32
		g0, g1           int16
		tapset0, tapset1 int
		n, overlap       int
	}
	cases := []tc{
		// Zero gains: pure copy.
		{0, 0, 0, 0, 0, 0, 64, 16},
		// Filter unchanged: overlap is skipped internally.
		{40, 40, 12000, 12000, 1, 1, 120, 24},
		// Changing filter: full overlap cross-fade then constant region.
		{40, 56, 9000, 15000, 0, 2, 240, 24},
		{33, 80, 20000, 8000, 2, 1, 120, 16},
		{16, 16, 0, 14000, 0, 0, 60, 12},
		// g1 == 0: copy after overlap.
		{50, 30, 14000, 0, 1, 0, 96, 20},
		// Period clamping (T below COMBFILTER_MINPERIOD).
		{3, 5, 11000, 13000, 0, 1, 80, 16},
		// Larger overlap, longer block.
		{200, 240, 7000, 9000, 2, 2, 480, 120},
	}
	for ci, c := range cases {
		for trial := 0; trial < 6; trial++ {
			maxT := c.t0
			if c.t1 > maxT {
				maxT = c.t1
			}
			if maxT < 15 {
				maxT = 15
			}
			history := int(maxT) + 2
			x := randSig(rng, history+c.n)
			window := randWindow(rng, c.overlap)
			want := oracleCombFull(t, x, history, c.n, c.overlap, c.t0, c.t1, c.g0, c.g1, c.tapset0, c.tapset1, window)
			y := make([]int32, history+c.n)
			CombFilter(y, x, history, int(c.t0), int(c.t1), c.n, c.g0, c.g1, c.tapset0, c.tapset1, window, c.overlap)
			for i := 0; i < c.n; i++ {
				if y[history+i] != want[i] {
					t.Fatalf("CombFilter case=%d trial=%d y[%d]=%d want=%d (t0=%d t1=%d g0=%d g1=%d tap0=%d tap1=%d N=%d ov=%d)",
						ci, trial, i, y[history+i], want[i], c.t0, c.t1, c.g0, c.g1, c.tapset0, c.tapset1, c.n, c.overlap)
				}
			}
		}
	}
}
