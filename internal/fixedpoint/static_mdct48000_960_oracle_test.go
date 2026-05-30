//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestStaticMDCT48000TablesMatchLibopus validates that the baked static 48000/960
// mode->mdct tables (trig, per-shift FFT scale/scaleShift/shift/factors/bitrev,
// and the shared twiddle table) reproduce the real libopus mode->mdct exactly.
func TestStaticMDCT48000TablesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	ref, err := libopustest.ProbeCELTFixedMDCTTables()
	if err != nil {
		libopustest.HelperUnavailable(t, "celt mdct tables", err)
		return
	}

	l := NewStaticMDCTLookup48000()
	if l == nil {
		t.Fatal("NewStaticMDCTLookup48000 returned nil")
	}

	if l.n != ref.N {
		t.Fatalf("n = %d, libopus = %d", l.n, ref.N)
	}
	if l.maxshift != ref.MaxShift {
		t.Fatalf("maxshift = %d, libopus = %d", l.maxshift, ref.MaxShift)
	}
	if len(l.trig) != len(ref.Trig) {
		t.Fatalf("trig len = %d, libopus = %d", len(l.trig), len(ref.Trig))
	}
	for i := range l.trig {
		if l.trig[i] != ref.Trig[i] {
			t.Fatalf("trig[%d] = %d, libopus = %d", i, l.trig[i], ref.Trig[i])
		}
	}

	if len(l.kfft) != len(ref.KFFT) {
		t.Fatalf("kfft count = %d, libopus = %d", len(l.kfft), len(ref.KFFT))
	}
	for s, rk := range ref.KFFT {
		k := l.kfft[s]
		if k.nfft != rk.Nfft {
			t.Fatalf("kfft[%d].nfft = %d, libopus = %d", s, k.nfft, rk.Nfft)
		}
		if int32(k.scale) != rk.Scale {
			t.Fatalf("kfft[%d].scale = %d, libopus = %d", s, k.scale, rk.Scale)
		}
		if int32(k.scaleShift) != rk.ScaleShift {
			t.Fatalf("kfft[%d].scaleShift = %d, libopus = %d", s, k.scaleShift, rk.ScaleShift)
		}
		if int32(k.shift) != rk.Shift {
			t.Fatalf("kfft[%d].shift = %d, libopus = %d", s, k.shift, rk.Shift)
		}
		for i := 0; i < 16; i++ {
			if k.factors[i] != rk.Factors[i] {
				t.Fatalf("kfft[%d].factors[%d] = %d, libopus = %d", s, i, k.factors[i], rk.Factors[i])
			}
		}
		if len(k.bitrev) != len(rk.Bitrev) {
			t.Fatalf("kfft[%d].bitrev len = %d, libopus = %d", s, len(k.bitrev), len(rk.Bitrev))
		}
		for i := range k.bitrev {
			if k.bitrev[i] != rk.Bitrev[i] {
				t.Fatalf("kfft[%d].bitrev[%d] = %d, libopus = %d", s, i, k.bitrev[i], rk.Bitrev[i])
			}
		}
		// k shares the base twiddle table; compare against the dumped table.
		if len(k.twiddles) != len(rk.Twiddles) {
			t.Fatalf("kfft[%d].twiddles len = %d, libopus = %d", s, len(k.twiddles), len(rk.Twiddles))
		}
		for i := range k.twiddles {
			if k.twiddles[i].R != rk.Twiddles[i][0] || k.twiddles[i].I != rk.Twiddles[i][1] {
				t.Fatalf("kfft[%d].twiddles[%d] = {%d,%d}, libopus = {%d,%d}",
					s, i, k.twiddles[i].R, k.twiddles[i].I, rk.Twiddles[i][0], rk.Twiddles[i][1])
			}
		}
	}
}
