package celt

import (
	"math"
	"testing"
)

func referenceNormalizeResidualIntoAndCollapse(out []celtNorm, pulses []int, gain, yy opusVal16, b int) int {
	normalizeResidualInto(out, pulses, gain, yy)
	return extractCollapseMask(pulses, len(pulses), b)
}

func pulseEnergy(pulses []int) float64 {
	energy := 0.0
	for _, v := range pulses {
		energy += float64(v * v)
	}
	return energy
}

func pulseEnergy32(pulses []int32) float64 {
	energy := 0.0
	for _, v := range pulses {
		energy += float64(v * v)
	}
	return energy
}

func TestNormalizeResidualIntoAndCollapseMatchesReference(t *testing.T) {
	gains := []opusVal16{0.125, 0.5, 1.0, 1.75}
	lengths := []int{1, 2, 3, 4, 7, 8, 12, 16, 24, 32, 48}

	for _, n := range lengths {
		basePulses := make([]int, n)
		for i := range basePulses {
			v := ((i * 5) + 3) % 9
			basePulses[i] = v - 4
			if i%7 == 0 {
				basePulses[i] = 0
			}
		}
		if n > 0 {
			basePulses[0] = n % 5
		}

		for _, gain := range gains {
			energies := []opusVal16{0, opusVal16(pulseEnergy(basePulses))}
			for _, yy := range energies {
				for b := 1; b <= min(8, n); b++ {
					if celtUdiv(n, b) <= 0 {
						continue
					}

					got := make([]celtNorm, n)
					want := make([]celtNorm, n)
					pulses := append([]int(nil), basePulses...)

					gotMask := normalizeResidualIntoAndCollapse(got, pulses, gain, yy, b)
					wantMask := referenceNormalizeResidualIntoAndCollapse(want, pulses, gain, yy, b)

					if gotMask != wantMask {
						t.Fatalf("mask mismatch n=%d gain=%v yy=%v b=%d: got=%b want=%b",
							n, gain, yy, b, gotMask, wantMask)
					}
					for i := range got {
						if math.Float32bits(float32(got[i])) != math.Float32bits(float32(want[i])) {
							t.Fatalf("value mismatch n=%d gain=%v yy=%v b=%d idx=%d: got=%0.9g want=%0.9g",
								n, gain, yy, b, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

func TestNormalizeResidualKnownEnergyIntoAndCollapse32MatchesIntPath(t *testing.T) {
	gains := []opusVal16{0.125, 0.5, 1.0, 1.75}
	lengths := []int{1, 2, 3, 4, 7, 8, 12, 16, 24, 32, 48}

	for _, n := range lengths {
		basePulses := make([]int32, n)
		for i := range basePulses {
			v := ((i * 5) + 3) % 9
			basePulses[i] = int32(v - 4)
			if i%7 == 0 {
				basePulses[i] = 0
			}
		}
		if n > 0 {
			basePulses[0] = int32(n % 5)
		}

		for _, gain := range gains {
			yy := opusVal16(pulseEnergy32(basePulses))
			if yy == 0 {
				continue
			}
			for b := 1; b <= min(8, n); b++ {
				if celtUdiv(n, b) <= 0 {
					continue
				}

				got := make([]celtNorm, n)
				want := make([]celtNorm, n)
				pulses32 := append([]int32(nil), basePulses...)
				pulses := make([]int, n)
				for i, v := range basePulses {
					pulses[i] = int(v)
				}

				gotMask := normalizeResidualKnownEnergyIntoAndCollapse32(got, pulses32, gain, yy, b)
				wantMask := normalizeResidualIntoAndCollapse(want, pulses, gain, yy, b)

				if gotMask != wantMask {
					t.Fatalf("mask mismatch n=%d gain=%v b=%d: got=%b want=%b",
						n, gain, b, gotMask, wantMask)
				}
				for i := range got {
					if math.Float32bits(float32(got[i])) != math.Float32bits(float32(want[i])) {
						t.Fatalf("value mismatch n=%d gain=%v b=%d idx=%d: got=%0.9g want=%0.9g",
							n, gain, b, i, got[i], want[i])
					}
				}
			}
		}
	}
}

func BenchmarkNormalizeResidualIntoAndCollapseCurrent(b *testing.B) {
	pulses := make([]int, 48)
	for i := range pulses {
		pulses[i] = ((i * 11) % 7) - 3
	}
	out := make([]celtNorm, len(pulses))
	yy := opusVal16(pulseEnergy(pulses))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = normalizeResidualIntoAndCollapse(out, pulses, 1.0, yy, 8)
	}
}

func BenchmarkNormalizeResidualKnownEnergyIntoAndCollapse32Current(b *testing.B) {
	pulses := make([]int32, 48)
	for i := range pulses {
		pulses[i] = int32(((i * 11) % 7) - 3)
	}
	out := make([]celtNorm, len(pulses))
	yy := opusVal16(pulseEnergy32(pulses))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = normalizeResidualKnownEnergyIntoAndCollapse32(out, pulses, 1.0, yy, 8)
	}
}

func BenchmarkNormalizeResidualIntoAndCollapseReference(b *testing.B) {
	pulses := make([]int, 48)
	for i := range pulses {
		pulses[i] = ((i * 11) % 7) - 3
	}
	out := make([]celtNorm, len(pulses))
	yy := opusVal16(pulseEnergy(pulses))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = referenceNormalizeResidualIntoAndCollapse(out, pulses, 1.0, yy, 8)
	}
}
