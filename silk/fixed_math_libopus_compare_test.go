//go:build cgo_libopus

package silk

import (
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
)

type fixedLCG struct {
	state uint32
}

func (l *fixedLCG) next() uint32 {
	l.state = l.state*1664525 + 1013904223
	return l.state
}

func TestInverse32VarQMatchesLibopus(t *testing.T) {
	rng := fixedLCG{state: 11}
	for i := 0; i < 1000; i++ {
		b32 := int32(1 + (rng.next() & 0x7FFFFFFF))
		qres := 47
		got := silk_INVERSE32_varQ(b32, qres)
		want := cgowrap.SilkInverse32VarQ(b32, qres)
		if got != want {
			t.Fatalf("silk_INVERSE32_varQ mismatch: b32=%d q=%d go=%d lib=%d", b32, qres, got, want)
		}
	}
}

func TestDiv32VarQMatchesLibopus(t *testing.T) {
	rng := fixedLCG{state: 17}
	for i := 0; i < 1000; i++ {
		a32 := int32(1 + (rng.next() & 0x7FFFFFFF))
		b32 := int32(1 + (rng.next() & 0x7FFFFFFF))
		qres := 16
		got := silk_DIV32_varQ(a32, b32, qres)
		want := cgowrap.SilkDiv32VarQ(a32, b32, qres)
		if got != want {
			t.Fatalf("silk_DIV32_varQ mismatch: a32=%d b32=%d q=%d go=%d lib=%d", a32, b32, qres, got, want)
		}
	}
}

func TestFixedPointOpsMatchLibopus(t *testing.T) {
	rng := fixedLCG{state: 23}
	for i := 0; i < 1000; i++ {
		a := int32(rng.next())
		b := int32(rng.next())
		c := int32(rng.next())
		shift := int(1 + (rng.next() % 15))

		if got, want := silk_SMLAWB(a, b, c), cgowrap.SilkSMLAWB(a, b, c); got != want {
			t.Fatalf("silk_SMLAWB mismatch: a=%d b=%d c=%d go=%d lib=%d", a, b, c, got, want)
		}
		if got, want := silk_SMLAWT(a, b, c), cgowrap.SilkSMLAWT(a, b, c); got != want {
			t.Fatalf("silk_SMLAWT mismatch: a=%d b=%d c=%d go=%d lib=%d", a, b, c, got, want)
		}
		if got, want := silk_SMULWB(a, b), cgowrap.SilkSMULWB(a, b); got != want {
			t.Fatalf("silk_SMULWB mismatch: a=%d b=%d go=%d lib=%d", a, b, got, want)
		}
		if got, want := silk_SMULWW(a, b), cgowrap.SilkSMULWW(a, b); got != want {
			t.Fatalf("silk_SMULWW mismatch: a=%d b=%d go=%d lib=%d", a, b, got, want)
		}
		if got, want := silk_RSHIFT_ROUND(a, shift), cgowrap.SilkRSHIFT_ROUND(a, shift); got != want {
			t.Fatalf("silk_RSHIFT_ROUND mismatch: a=%d shift=%d go=%d lib=%d", a, shift, got, want)
		}
	}
}

func TestLTPPredMatchesLibopus(t *testing.T) {
	rng := fixedLCG{state: 31}
	sLTPQ15 := make([]int32, 640)
	for i := range sLTPQ15 {
		sLTPQ15[i] = int32(rng.next())
	}
	bQ14 := make([]int16, 5)
	for i := range bQ14 {
		bQ14[i] = int16(rng.next())
	}
	startIdx := 100
	length := 200

	got := make([]int32, length)
	predIdx := startIdx
	for i := 0; i < length; i++ {
		ltpPredQ14 := int32(2)
		for tap := 0; tap < 5; tap++ {
			idx := predIdx - tap
			ltpPredQ14 = silk_SMLAWB(ltpPredQ14, sLTPQ15[idx], int32(bQ14[tap]))
		}
		ltpPredQ14 = silk_LSHIFT32(ltpPredQ14, 1)
		got[i] = ltpPredQ14
		predIdx++
	}

	want := cgowrap.SilkLTPPredQ14(sLTPQ15, bQ14, startIdx, length)
	if want == nil {
		t.Fatalf("libopus LTP pred returned nil")
	}
	if len(want) != len(got) {
		t.Fatalf("ltp pred length mismatch: go=%d lib=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ltp pred mismatch at %d: go=%d lib=%d", i, got[i], want[i])
		}
	}
}
