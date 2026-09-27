package rangecoding

import (
	"math/rand"
	"testing"
)

// TestDecodeICDF2_8SignBlock16MatchesPerSymbolDecode checks the masked sign
// decoder against silk_decode_signs written as one DecodeICDF2_8 call per
// positive pulse.
func TestDecodeICDF2_8SignBlock16MatchesPerSymbolDecode(t *testing.T) {
	rng := rand.New(rand.NewSource(0x51))
	icdfs := []uint8{254, 49, 67, 77, 82, 93, 99, 198, 11, 42, 60, 128, 1, 255}
	for iter := 0; iter < 20000; iter++ {
		buf := make([]byte, rng.Intn(24))
		rng.Read(buf)
		var want, got Decoder
		want.Init(buf)
		got.Init(buf)
		blocks := 1 + rng.Intn(8)
		for range blocks {
			var block [16]int16
			density := rng.Intn(17)
			for j := range block {
				if rng.Intn(16) < density {
					block[j] = int16(1 + rng.Intn(40))
					if rng.Intn(20) == 0 {
						block[j] = int16(rng.Intn(32768))
					}
				}
			}
			icdf0 := icdfs[rng.Intn(len(icdfs))]
			wantBlock := block
			for j, v := range wantBlock {
				if v > 0 && want.DecodeICDF2_8(icdf0) == 0 {
					wantBlock[j] = -v
				}
			}
			got.DecodeICDF2_8SignBlock16(icdf0, &block)
			if block != wantBlock {
				t.Fatalf("iter %d: block %v want %v", iter, block, wantBlock)
			}
			if got.rng != want.rng || got.val != want.val || got.offs != want.offs || got.rem != want.rem ||
				got.nbitsTotal != want.nbitsTotal || got.err != want.err {
				t.Fatalf("iter %d: state mismatch got %+v want %+v", iter, got, want)
			}
		}
	}
}

// TestTellFracMatchesCorrectionTable checks the branch-free TellFrac against
// libopus celt/entcode.c ec_tell_frac written literally.
func TestTellFracMatchesCorrectionTable(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7e11))
	var d Decoder
	for iter := 0; iter < 2000000; iter++ {
		d.rng = rng.Uint32() | (1 << 23)
		if iter < 64 {
			d.rng = uint32(EC_CODE_BOT+1) << (iter % 9)
		}
		d.nbitsTotal = int32(rng.Intn(1 << 20))
		nbits := int(d.nbitsTotal) << 3
		l := ilog(d.rng)
		r := d.rng >> (l - 16)
		b := int((r >> 12) - 8)
		if r > tellFracCorrection[b] {
			b++
		}
		if got, want := d.TellFrac(), nbits-((l<<3)+b); got != want {
			t.Fatalf("rng %#x: TellFrac = %d, want %d", d.rng, got, want)
		}
	}
}

// TestDecodeBitMatchesLogpReference checks the branch-free DecodeBit against
// libopus celt/entdec.c ec_dec_bit_logp written literally.
func TestDecodeBitMatchesLogpReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xb17))
	for iter := 0; iter < 20000; iter++ {
		buf := make([]byte, rng.Intn(32))
		rng.Read(buf)
		var got, want Decoder
		got.Init(buf)
		want.Init(buf)
		for range 1 + rng.Intn(64) {
			logp := uint(1 + rng.Intn(15))
			r := want.rng
			s := r >> logp
			ret := 0
			if want.val < s {
				ret = 1
				want.rng = s
			} else {
				want.val -= s
				want.rng = r - s
			}
			want.normalize()
			if b := got.DecodeBit(logp); b != ret {
				t.Fatalf("iter %d: bit %d, want %d", iter, b, ret)
			}
			if got.rng != want.rng || got.val != want.val || got.offs != want.offs || got.rem != want.rem || got.nbitsTotal != want.nbitsTotal {
				t.Fatalf("iter %d: state mismatch", iter)
			}
		}
	}
}
