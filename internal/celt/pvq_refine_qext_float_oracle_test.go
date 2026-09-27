//go:build gopus_qext && !gopus_fixed_point

package celt

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func TestAlgQuantQEXTRefinementMatchesLibopusAtFloatRoundingBoundary(t *testing.T) {
	libopustest.RequireOracle(t)
	tc := algQuantQEXTOracleCase{
		name:      "extra_refine_just_below_half",
		x:         []float32{0.16666666, 0.16666667, 0.6666667},
		k:         1,
		spread:    spreadNone,
		b:         1,
		gain:      1,
		extraBits: 2,
	}

	// C evaluates floor(.5 + tmp) in double precision because .5 is a C
	// double literal. This fixture puts one extra-stage tmp at the preceding
	// float32 value, where a float32 addition would round the sum up to 1.
	sum := float32(0)
	for _, sample := range tc.x {
		if sample < 0 {
			sample = -sample
		}
		sum += sample
	}
	up := (1 << tc.extraBits) - 1
	tmp := float32(up*tc.k) * (tc.x[0] / sum)
	goRounded := floor32ToInt(float32(0.5) + tmp)
	cRounded := int(math.Floor(0.5 + float64(tmp)))
	if goRounded == cRounded || math.Float32bits(tmp) != 0x3effffff {
		t.Fatalf("fixture misses the C/Go rounding boundary: tmp=%08x Go/C=%d/%d", math.Float32bits(tmp), goRounded, cRounded)
	}
	if got := int(floorPVQRefineTmp(tmp)); got != cRounded {
		t.Fatalf("PVQ refinement rounding=%d want C floor decision %d", got, cRounded)
	}

	want, err := probeLibopusAlgQuantQEXT([]algQuantQEXTOracleCase{tc})
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext refinement boundary", err)
	}
	x := make([]celtNorm, len(tc.x))
	for i, sample := range tc.x {
		x[i] = celtNorm(sample)
	}
	var enc rangecoding.Encoder
	encBuf := make([]byte, 128)
	enc.Init(encBuf)
	var ext rangecoding.Encoder
	extBuf := make([]byte, 128)
	ext.Init(extBuf)
	var scratch bandEncodeScratch
	collapse := algQuantScratch(&enc, 0, x, len(x), tc.k, tc.spread, tc.b,
		opusVal16(tc.gain), false, &ext, tc.extraBits, &scratch)
	packet := enc.Done()
	extPacket := ext.Done()
	if collapse != want[0].collapse {
		t.Fatalf("collapse=%d want %d", collapse, want[0].collapse)
	}
	if !bytes.Equal(packet, want[0].packet) || !bytes.Equal(extPacket, want[0].extPacket) {
		t.Fatalf("main/extension packet mismatch: Go=%x/%x C=%x/%x", packet, extPacket, want[0].packet, want[0].extPacket)
	}
	for i := range x {
		if got, ref := math.Float32bits(float32(x[i])), math.Float32bits(want[0].x[i]); got != ref {
			t.Fatalf("resynth x[%d]=%08x want %08x", i, got, ref)
		}
	}
}

func TestAlgQuantQEXTSearchMatchesLibopusAtPVQCorrectionBoundary(t *testing.T) {
	libopustest.RequireOracle(t)
	tc := algQuantQEXTOracleCase{
		name: "n4_k112_up1023_correction_tie",
		x: []float32{
			math.Float32frombits(0x3ec8faae),
			math.Float32frombits(0xbeade4aa),
			math.Float32frombits(0x3f12a0d7),
			math.Float32frombits(0xbed5fb1d),
		},
		k:         112,
		spread:    spreadNone,
		b:         1,
		gain:      1,
		resynth:   false,
		extraBits: 10,
	}
	requireDefaultLibopusPVQ(t, len(tc.x), tc.k)

	// This is the raw four-coefficient band-2 input from the public QEXT
	// mismatch. The second PVQ refinement pass has N=4, K=112*1023, and an
	// exact residual tie; rounding the product to float32 before subtracting
	// selects the same correction coordinate as libopus.
	x := make([]celtNorm, len(tc.x))
	for i, sample := range tc.x {
		x[i] = celtNorm(sample)
	}
	iy, upIy, refine, _ := opPVQSearchExtraNorm(x, tc.k, (1<<tc.extraBits)-1)
	wantIy := [4]int32{26, -22, 37, -27}
	wantUpIy := [4]int32{26105, -22587, 38090, -27794}
	wantRefine := [4]int32{-493, -81, 239, -173}
	for i := range 4 {
		if iy[i] != wantIy[i] || upIy[i] != wantUpIy[i] || refine[i] != wantRefine[i] {
			t.Fatalf("PVQ search[%d]=iy %d, upIy %d, refine %d; want %d, %d, %d",
				i, iy[i], upIy[i], refine[i], wantIy[i], wantUpIy[i], wantRefine[i])
		}
	}

	want, err := probeLibopusAlgQuantQEXT([]algQuantQEXTOracleCase{tc})
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext PVQ correction boundary", err)
	}
	x = make([]celtNorm, len(tc.x))
	for i, sample := range tc.x {
		x[i] = celtNorm(sample)
	}
	var enc rangecoding.Encoder
	encBuf := make([]byte, 128)
	enc.Init(encBuf)
	var ext rangecoding.Encoder
	extBuf := make([]byte, 128)
	ext.Init(extBuf)
	var scratch bandEncodeScratch
	collapse := algQuantScratch(&enc, 2, x, len(x), tc.k, tc.spread, tc.b,
		opusVal16(tc.gain), tc.resynth, &ext, tc.extraBits, &scratch)
	packet := enc.Done()
	extPacket := ext.Done()
	if collapse != want[0].collapse {
		t.Fatalf("collapse=%d want %d", collapse, want[0].collapse)
	}
	if !bytes.Equal(packet, want[0].packet) || !bytes.Equal(extPacket, want[0].extPacket) {
		t.Fatalf("main/extension packet mismatch: Go=%x/%x C=%x/%x", packet, extPacket, want[0].packet, want[0].extPacket)
	}
	for i := range x {
		if got, ref := math.Float32bits(float32(x[i])), math.Float32bits(want[0].x[i]); got != ref {
			t.Fatalf("resynth x[%d]=%08x want %08x", i, got, ref)
		}
	}
}
