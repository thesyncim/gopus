//go:build gopus_fixed_point

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// mdctWindow builds a deterministic int16 Q15 window of the given even length.
// The MDCT parity only needs the Go and C sides to fold with identical
// coefficients; using a smooth half-sine-like ramp keeps the values in the
// valid Q15 twiddle range exercised by the real CELT window.
func mdctWindow(overlap int, seed int64) []int16 {
	w := make([]int16, overlap)
	rng := rand.New(rand.NewSource(seed))
	for i := range w {
		// Spread coefficients across the Q15 range, biased positive like the
		// CELT overlap window, with a deterministic jitter.
		base := int32(i+1) * 32767 / int32(overlap+1)
		jitter := int32(rng.Intn(64) - 32)
		v := base + jitter
		if v > 32767 {
			v = 32767
		}
		if v < -32767 {
			v = -32767
		}
		w[i] = int16(v)
	}
	return w
}

// mdctSignal builds a deterministic int32 input buffer of length n that mixes a
// few extreme values with a bounded pseudo-random signal. Every MDCT operation
// is either wraparound (_ovflw) or a well-defined shift, so full-range int32
// inputs remain a valid bit-exact comparison against libopus.
func mdctSignal(n int, seed int64) []int32 {
	buf := make([]int32, n)
	extremes := []int32{0, 1, -1, 2, -2, 32767, -32768, 1 << 20, -(1 << 20), 1 << 24, -(1 << 24)}
	rng := rand.New(rand.NewSource(seed))
	for i := range buf {
		if i < len(extremes) {
			buf[i] = extremes[i]
		} else {
			// Signal-range values (roughly +/- 2^23) plus occasional large ones.
			buf[i] = int32(rng.Intn(1<<24) - (1 << 23))
		}
	}
	return buf
}

// mdctShiftCases enumerates the standard CELT MDCT lookup (N=1920, maxshift=3,
// the 48 kHz mode) and each frame shift it serves: shift 0..3 give effective
// lengths 1920/960/480/240 with sub-FFTs of 480/240/120/60. These are exactly
// the sizes the integer CELT transform uses for the 20/10/5/2.5 ms frames.
func mdctShiftCases() (n, maxshift int, shifts []int) {
	return 1920, 3, []int{0, 1, 2, 3}
}

// TestMDCTForwardOracle drives the Go integer forward MDCT against the real
// libopus clt_mdct_forward_c bit-for-bit over the CELT MDCT sizes.
func TestMDCTForwardOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	n, maxshift, shifts := mdctShiftCases()
	l := NewMDCTLookup(n, maxshift)
	if l == nil {
		t.Fatalf("NewMDCTLookup(%d, %d) returned nil", n, maxshift)
	}

	for _, shift := range shifts {
		effN := n >> shift
		overlap := effN / 4 // even, satisfies (overlap+3)>>2 <= N4
		window := mdctWindow(overlap, int64(0x6677000+shift))
		in := mdctSignal(n, int64(0x46d637400+shift))

		params := libopustest.CELTMDCTParams{
			N: n, MaxShift: maxshift, Shift: shift, Overlap: overlap, Stride: 1, Window: window,
		}
		ref, err := libopustest.ProbeCELTMDCT(libopustest.CELTMDCTModeForward, params, in)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt mdct", err)
			return
		}

		// libopus trashes its input copy; give the Go side its own copy.
		goIn := append([]int32(nil), in...)
		out := make([]int32, len(ref))
		l.MDCTForward(goIn, out, window, overlap, shift, 1, nil)

		for i := range out {
			if out[i] != ref[i] {
				t.Fatalf("shift=%d (N=%d): forward[%d] = %d, libopus = %d", shift, effN, i, out[i], ref[i])
			}
		}
	}
}

// TestMDCTBackwardOracle drives the Go integer backward MDCT against the real
// libopus clt_mdct_backward_c bit-for-bit over the CELT MDCT sizes.
func TestMDCTBackwardOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	n, maxshift, shifts := mdctShiftCases()
	l := NewMDCTLookup(n, maxshift)
	if l == nil {
		t.Fatalf("NewMDCTLookup(%d, %d) returned nil", n, maxshift)
	}

	for _, shift := range shifts {
		effN := n >> shift
		n2 := effN >> 1
		overlap := effN / 4
		window := mdctWindow(overlap, int64(0x6277000+shift))
		// Backward input is n2 frequency samples (stride 1, contiguous).
		in := mdctSignal(n2, int64(0x4263400+shift))

		params := libopustest.CELTMDCTParams{
			N: n, MaxShift: maxshift, Shift: shift, Overlap: overlap, Stride: 1, Window: window,
		}
		ref, err := libopustest.ProbeCELTMDCT(libopustest.CELTMDCTModeBackward, params, in)
		if err != nil {
			libopustest.HelperUnavailable(t, "celt mdct", err)
			return
		}

		goIn := append([]int32(nil), in...)
		out := make([]int32, effN)
		l.MDCTBackward(goIn, out, window, overlap, shift, 1)

		for i := range out {
			if out[i] != ref[i] {
				t.Fatalf("shift=%d (N=%d): backward[%d] = %d, libopus = %d", shift, effN, i, out[i], ref[i])
			}
		}
	}
}
