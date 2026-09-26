package silk

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestStereoLPFilter(t *testing.T) {
	// Test the LP filter with a known input
	// LP filter is [1,2,1]/4, so for constant input, output should equal input
	signal := []int16{1000, 1000, 1000, 1000, 1000, 1000}
	frameLength := 4

	lp, hp := stereoLPFilter(signal, frameLength)

	// For constant input, LP should equal input and HP should be 0
	for i := range frameLength {
		if lp[i] != 1000 {
			t.Errorf("LP[%d] = %d, want 1000 (constant input)", i, lp[i])
		}
		if hp[i] != 0 {
			t.Errorf("HP[%d] = %d, want 0 (constant input)", i, hp[i])
		}
	}
}

func TestStereoLPFilterImpulse(t *testing.T) {
	// Test LP filter with an impulse
	// Input: [0, 0, 4000, 0, 0]
	// LP[0] = (0 + 2*0 + 4000 + 2) >> 2 = 1000
	// LP[1] = (0 + 2*4000 + 0 + 2) >> 2 = 2000
	// LP[2] = (4000 + 2*0 + 0 + 2) >> 2 = 1000
	signal := []int16{0, 0, 4000, 0, 0}
	frameLength := 3

	lp, hp := stereoLPFilter(signal, frameLength)

	expectedLP := []int16{1000, 2000, 1000}
	for i := range frameLength {
		if lp[i] != expectedLP[i] {
			t.Errorf("LP[%d] = %d, want %d", i, lp[i], expectedLP[i])
		}
	}

	// HP[n] = signal[n+1] - LP[n]
	// HP[0] = 0 - 1000 = -1000
	// HP[1] = 4000 - 2000 = 2000
	// HP[2] = 0 - 1000 = -1000
	expectedHP := []int16{-1000, 2000, -1000}
	for i := range frameLength {
		if hp[i] != expectedHP[i] {
			t.Errorf("HP[%d] = %d, want %d", i, hp[i], expectedHP[i])
		}
	}
}

func TestStereoConvertLRToMS(t *testing.T) {
	// Test L/R to M/S conversion
	// M = (L + R) / 2
	// S = (L - R) / 2
	left := []int16{1000, 2000, 3000, 4000}
	right := []int16{1000, 0, 1000, 2000}
	mid := make([]int16, 4)
	side := make([]int16, 4)

	stereoConvertLRToMS(left[:2], right[:2], mid[:2], side[:2], 0)

	// For first 2 samples:
	// M[0] = (1000 + 1000) / 2 = 1000
	// S[0] = (1000 - 1000) / 2 = 0
	// M[1] = (2000 + 0) / 2 = 1000
	// S[1] = (2000 - 0) / 2 = 1000

	if mid[0] != 1000 {
		t.Errorf("mid[0] = %d, want 1000", mid[0])
	}
	if side[0] != 0 {
		t.Errorf("side[0] = %d, want 0", side[0])
	}
	if mid[1] != 1000 {
		t.Errorf("mid[1] = %d, want 1000", mid[1])
	}
	if side[1] != 1000 {
		t.Errorf("side[1] = %d, want 1000", side[1])
	}
}

func TestIsqrt32(t *testing.T) {
	tests := []struct {
		input    uint32
		expected uint32
	}{
		{0, 0},
		{1, 1},
		{4, 2},
		{9, 3},
		{16, 4},
		{25, 5},
		{100, 10},
		{10000, 100},
		{1000000, 1000},
	}

	for _, tt := range tests {
		got := isqrt32(tt.input)
		if got != tt.expected {
			t.Errorf("isqrt32(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestSILKIsqrt32MatchesLibopusCELTOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	inputs := []uint32{
		0, 1, 2, 3, 4, 15, 16, 17,
		(1 << 16) - 1, 1 << 16, (1 << 16) + 1,
		(1 << 24) - 1, 1 << 24, (1 << 24) + 1,
		1000000, ^uint32(0) - 2, ^uint32(0) - 1, ^uint32(0),
	}
	want, err := libopustest.ProbeCELTMathWords(libopustest.CELTMathModeISqrt32, len(inputs), inputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, x := range inputs {
		got := isqrt32(x)
		if got != want[i] {
			t.Fatalf("isqrt32(%d)=%d want %d", x, got, want[i])
		}
	}
}

func TestStereoLRToMSKeepsFilterHistory(t *testing.T) {
	// silk_stereo_LR_to_MS carries the last two mid and side samples of a
	// frame into the next one (sMid/sSide).
	const frameLength = 320
	buf0 := make([]int16, frameLength+2)
	buf1 := make([]int16, frameLength+2)
	for i := 2; i < frameLength+2; i++ {
		buf0[i] = 16384
		buf1[i] = 9830
	}
	var state stereoEncState
	var scratch stereoLRToMSScratch
	silkStereoLRToMS(&state, buf0, buf1, 32000, 200, false, 16, frameLength, &scratch)

	wantMid := int16((16384 + 9830 + 1) >> 1)
	wantSide := int16((16384 - 9830 + 1) >> 1)
	if state.sMid != [2]int16{wantMid, wantMid} {
		t.Errorf("sMid = %v, want [%d %d]", state.sMid, wantMid, wantMid)
	}
	if state.sSide != [2]int16{wantSide, wantSide} {
		t.Errorf("sSide = %v, want [%d %d]", state.sSide, wantSide, wantSide)
	}
	// The mid output starts with the previous frame's history (zero here).
	if buf0[0] != 0 || buf0[1] != 0 || buf0[2] != wantMid {
		t.Errorf("mid = %v..., want [0 0 %d ...]", buf0[:3], wantMid)
	}
}

func TestStereoLPFilterMatchesLibopus(t *testing.T) {
	// Test that our LP filter matches the libopus implementation exactly
	// libopus formula: sum = silk_RSHIFT_ROUND(silk_ADD_LSHIFT32(mid[n] + mid[n+2], mid[n+1], 1), 2)
	// Which is: ((mid[n] + mid[n+2]) + (mid[n+1] << 1) + 2) >> 2
	// = (mid[n] + 2*mid[n+1] + mid[n+2] + 2) / 4  (rounded)

	testCases := []struct {
		signal   []int16
		expected []int16
	}{
		// Test case 1: Simple values
		{[]int16{100, 200, 300, 400, 500}, []int16{200, 300, 400}},
		// Test case 2: All same
		{[]int16{1000, 1000, 1000, 1000, 1000}, []int16{1000, 1000, 1000}},
		// Test case 3: Ramp
		{[]int16{0, 100, 200, 300, 400}, []int16{100, 200, 300}},
	}

	for i, tc := range testCases {
		lp, _ := stereoLPFilter(tc.signal, len(tc.expected))
		for j, exp := range tc.expected {
			if lp[j] != exp {
				t.Errorf("Test %d: LP[%d] = %d, want %d", i, j, lp[j], exp)
			}
		}
	}
}

func BenchmarkStereoLPFilter(b *testing.B) {
	signal := make([]int16, 322) // 320 + 2 history
	for i := range signal {
		signal[i] = int16(i * 10)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stereoLPFilter(signal, 320)
	}
}
