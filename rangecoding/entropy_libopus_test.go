package rangecoding

import (
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

func entropySeed(t *testing.T) int64 {
	t.Helper()
	if env := os.Getenv("SEED"); env != "" {
		seed, err := strconv.ParseInt(env, 10, 64)
		if err != nil {
			t.Fatalf("invalid SEED: %v", err)
		}
		return seed
	}
	return 1
}

func TestLibopusEntropyPort(t *testing.T) {
	seed := entropySeed(t)
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed=%d", seed)

	maxFT := 256
	maxBits := 12
	randomIters := 2000
	randomMaxSize := 64
	compatIters := 2000
	compatMaxSize := 64
	bufSize := 1 << 20
	bufSize2 := 4096

	if testing.Short() {
		maxFT = 64
		maxBits = 8
		randomIters = 200
		randomMaxSize = 32
		compatIters = 200
		compatMaxSize = 32
		bufSize = 1 << 18
		bufSize2 = 1024
	}

	// Test encoding/decoding of uniform values and raw bits.
	encBuf := make([]byte, bufSize)
	enc := &Encoder{}
	enc.Init(encBuf)
	entropy := 0.0

	for ft := 2; ft < maxFT; ft++ {
		for i := 0; i < ft; i++ {
			entropy += math.Log2(float64(ft))
			enc.EncodeUniform(uint32(i), uint32(ft))
		}
	}

	for ftb := 1; ftb < maxBits; ftb++ {
		for i := 0; i < (1 << ftb); i++ {
			entropy += float64(ftb)
			before := enc.Tell()
			enc.EncodeRawBits(uint32(i), uint(ftb))
			after := enc.Tell()
			if after-before != ftb {
				t.Fatalf("raw bits: used %d bits to encode %d bits", after-before, ftb)
			}
		}
	}

	encBits := enc.TellFrac()
	out := enc.Done()
	t.Logf("entropy bits=%.2f packed=%.2f", entropy, float64(encBits)/8.0)
	if enc.Error() != 0 {
		t.Fatalf("encoder error in uniform/raw test")
	}

	dec := &Decoder{}
	dec.Init(out)

	for ft := 2; ft < maxFT; ft++ {
		for i := 0; i < ft; i++ {
			sym := dec.DecodeUniform(uint32(ft))
			if sym != uint32(i) {
				t.Fatalf("decode uint: got %d want %d (ft=%d)", sym, i, ft)
			}
		}
	}

	for ftb := 1; ftb < maxBits; ftb++ {
		for i := 0; i < (1 << ftb); i++ {
			sym := dec.DecodeRawBits(uint(ftb))
			if sym != uint32(i) {
				t.Fatalf("decode bits: got %d want %d (bits=%d)", sym, i, ftb)
			}
		}
	}

	if dec.TellFrac() != encBits {
		t.Fatalf("tell_frac mismatch: dec=%d enc=%d", dec.TellFrac(), encBits)
	}

	// Encoder bust prefers range coder data over raw bits.
	enc.Init(make([]byte, 2))
	enc.EncodeRawBits(0x55, 7)
	enc.EncodeUniform(1, 2)
	enc.EncodeUniform(1, 3)
	enc.EncodeUniform(1, 4)
	enc.EncodeUniform(1, 5)
	enc.EncodeUniform(2, 6)
	enc.EncodeUniform(6, 7)
	out = enc.Done()
	if enc.Error() == 0 {
		t.Fatalf("expected encoder error on buffer bust")
	}
	dec.Init(out)
	raw := dec.DecodeRawBits(7)
	v2 := dec.DecodeUniform(2)
	v3 := dec.DecodeUniform(3)
	v4 := dec.DecodeUniform(4)
	v5 := dec.DecodeUniform(5)
	v6 := dec.DecodeUniform(6)
	v7 := dec.DecodeUniform(7)
	if raw != 0x05 || v2 != 1 || v3 != 1 || v4 != 1 || v5 != 1 || v6 != 2 || v7 != 6 {
		t.Fatalf("encoder bust decode mismatch: raw=%#x v2=%d v3=%d v4=%d v5=%d v6=%d v7=%d buf=%x",
			raw, v2, v3, v4, v5, v6, v7, out)
	}

	// Random streams: encode/decode and verify tell() parity.
	for i := 0; i < randomIters; i++ {
		ft := rng.Intn(2048) + 10
		sz := rng.Intn(randomMaxSize + 1)
		data := make([]uint32, sz)
		tell := make([]int, sz+1)

		enc.Init(make([]byte, bufSize2))
		zeros := rng.Intn(13) == 0
		tell[0] = enc.TellFrac()

		for j := 0; j < sz; j++ {
			if zeros {
				data[j] = 0
			} else {
				data[j] = uint32(rng.Intn(ft))
			}
			enc.EncodeUniform(data[j], uint32(ft))
			tell[j+1] = enc.TellFrac()
		}

		if rng.Intn(2) == 0 {
			for enc.Tell()%8 != 0 {
				enc.EncodeUniform(uint32(rng.Intn(2)), 2)
			}
		}

		tellBits := enc.Tell()
		out = enc.Done()
		if tellBits != enc.Tell() {
			t.Fatalf("tell changed after done: %d -> %d (iter=%d)", tellBits, enc.Tell(), i)
		}
		if (tellBits+7)/8 < enc.RangeBytes() {
			t.Fatalf("tell underreported bytes: tell=%d range=%d (iter=%d)", tellBits, enc.RangeBytes(), i)
		}

		dec.Init(out)
		if dec.TellFrac() != tell[0] {
			t.Fatalf("tell mismatch at start: dec=%d enc=%d (iter=%d)", dec.TellFrac(), tell[0], i)
		}
		for j := 0; j < sz; j++ {
			sym := dec.DecodeUniform(uint32(ft))
			if sym != data[j] {
				t.Fatalf("decode mismatch: got %d want %d (ft=%d idx=%d iter=%d)", sym, data[j], ft, j, i)
			}
			if dec.TellFrac() != tell[j+1] {
				t.Fatalf("tell mismatch at %d: dec=%d enc=%d (iter=%d)", j+1, dec.TellFrac(), tell[j+1], i)
			}
		}
	}

	// Compatibility between encode/decode methods.
	for i := 0; i < compatIters; i++ {
		sz := rng.Intn(compatMaxSize + 1)
		logp1 := make([]uint, sz)
		data := make([]uint32, sz)
		tell := make([]int, sz+1)
		encMethod := make([]int, sz)

		enc.Init(make([]byte, bufSize2))
		tell[0] = enc.TellFrac()
		for j := 0; j < sz; j++ {
			data[j] = uint32(rng.Intn(2))
			logp1[j] = uint(rng.Intn(15) + 1)
			encMethod[j] = rng.Intn(4)
			ft := uint32(1) << logp1[j]
			fl := uint32(0)
			fh := ft - 1
			if data[j] != 0 {
				fl = ft - 1
				fh = ft
			}

			switch encMethod[j] {
			case 0:
				enc.Encode(fl, fh, ft)
			case 1:
				enc.EncodeBin(fl, fh, logp1[j])
			case 2:
				enc.EncodeBit(int(data[j]), logp1[j])
			case 3:
				icdf := []uint8{1, 0}
				enc.EncodeICDF(int(data[j]), icdf, logp1[j])
			}
			tell[j+1] = enc.TellFrac()
		}

		out = enc.Done()
		if (enc.Tell()+7)/8 < enc.RangeBytes() {
			t.Fatalf("tell underreported bytes in compat test (iter=%d)", i)
		}

		dec.Init(out)
		if dec.TellFrac() != tell[0] {
			t.Fatalf("compat tell mismatch at start: dec=%d enc=%d (iter=%d)", dec.TellFrac(), tell[0], i)
		}
		for j := 0; j < sz; j++ {
			decMethod := rng.Intn(4)
			ft := uint32(1) << logp1[j]
			fl := uint32(0)
			fh := ft - 1
			var sym uint32

			switch decMethod {
			case 0:
				fs := dec.Decode(ft)
				if fs >= ft-1 {
					sym = 1
					fl = ft - 1
					fh = ft
				}
				dec.Update(fl, fh, ft)
			case 1:
				fs := dec.DecodeBin(logp1[j])
				if fs >= ft-1 {
					sym = 1
					fl = ft - 1
					fh = ft
				}
				dec.Update(fl, fh, ft)
			case 2:
				sym = uint32(dec.DecodeBit(logp1[j]))
			case 3:
				icdf := []uint8{1, 0}
				sym = uint32(dec.DecodeICDF(icdf, logp1[j]))
			}

			if sym != data[j] {
				t.Fatalf("compat decode mismatch: got %d want %d (idx=%d iter=%d)", sym, data[j], j, i)
			}
			if dec.TellFrac() != tell[j+1] {
				t.Fatalf("compat tell mismatch at %d: dec=%d enc=%d (iter=%d)", j+1, dec.TellFrac(), tell[j+1], i)
			}
		}
	}

}
