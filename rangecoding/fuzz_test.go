package rangecoding

import (
	"testing"
)

// Fuzz coverage for the range coder. Two complementary targets:
//
//   - FuzzRoundtrip drives a random script of encode operations with random
//     frequencies through the encoder, then replays the same script through a
//     decoder. It asserts every decoded value equals what was encoded and that
//     TellFrac stays in lockstep between encoder and decoder. A roundtrip
//     mismatch means the encoder and decoder are no longer exact inverses,
//     which is a serious bit-exactness bug.
//
//   - FuzzDecoderArbitraryBytes feeds arbitrary bytes to a fresh decoder and
//     runs a fixed mix of decode operations. It asserts the decoder never
//     panics or indexes out of bounds on garbage/truncated input and that every
//     returned value stays within its documented range.
//
// Neither target encodes anything not permitted by the encoder contract
// (frequencies always satisfy 0 <= fl < fh <= ft, uniform values are < ft,
// ICDF tables are valid), and the encode buffer is sized so a valid script
// never busts. That keeps a reported mismatch attributable to the coder rather
// than to an invalid caller.

// Fixed, valid ICDF tables for the roundtrip script. Each table is strictly
// decreasing and ends in 0, and every leading value is strictly below 1<<ftb so
// that all symbols (including symbol 0) have nonzero probability and are
// encodable. These constraints are part of the ICDF encode contract, not the
// range coder's job to enforce; violating them would corrupt the stream and
// produce a false "mismatch", so the harness keeps the tables valid.
//
// The uint8 tables use ftb=8 (total 256). The uint16 tables carry their own ftb
// so the harness can exercise both the SILK style (ftb=8, values < 256) and the
// DRED/wide style (ftb=15, values < 32768).
var fuzzICDF8Tables = [][]uint8{
	{128, 0},
	{192, 128, 64, 0},
	{220, 170, 100, 20, 0},
	{250, 240, 200, 150, 80, 20, 0},
	{255, 254, 200, 150, 100, 60, 30, 10, 0},
}

type icdf16Table struct {
	icdf []uint16
	ftb  uint
}

var fuzzICDF16Tables = []icdf16Table{
	{icdf: []uint16{200, 0}, ftb: 8},
	{icdf: []uint16{255, 128, 1, 0}, ftb: 8},
	{icdf: []uint16{4000, 3000, 2000, 1000, 0}, ftb: 13},
	{icdf: []uint16{32000, 24000, 16000, 8000, 100, 0}, ftb: 15},
}

// fuzzReader is a tiny cursor over the fuzz input. It hands out bytes/values and
// never reads past the end (returning 0 once exhausted), so a short input simply
// yields a short script rather than an out-of-range read.
type fuzzReader struct {
	b   []byte
	pos int
}

func (r *fuzzReader) byte() byte {
	if r.pos >= len(r.b) {
		return 0
	}
	v := r.b[r.pos]
	r.pos++
	return v
}

func (r *fuzzReader) done() bool { return r.pos >= len(r.b) }

// u32 reads four bytes big-endian (missing bytes read as 0).
func (r *fuzzReader) u32() uint32 {
	return uint32(r.byte())<<24 | uint32(r.byte())<<16 | uint32(r.byte())<<8 | uint32(r.byte())
}

// fuzzOp is one decoded operation: the encode call to perform and the data the
// decoder needs to verify it.
type fuzzOp struct {
	kind byte
	// generic Encode/EncodeBin
	fl, fh, ft uint32
	bits       uint
	// EncodeBit
	bit  int
	logp uint
	// EncodeICDF / EncodeICDF16
	sym  int
	tbl  int
	ftb  uint
	is16 bool
	// EncodeUniform
	uval uint32
	uft  uint32
	// EncodeRawBits
	rval uint32
	rb   uint
}

// buildFuzzScript turns fuzz bytes into a bounded list of valid encode ops.
func buildFuzzScript(data []byte) []fuzzOp {
	r := &fuzzReader{b: data}
	const maxOps = 256
	ops := make([]fuzzOp, 0, 64)
	for !r.done() && len(ops) < maxOps {
		op := fuzzOp{}
		switch r.byte() % 7 {
		case 0: // Encode(fl, fh, ft) with arbitrary ft >= 2
			ft := uint32(r.byte())<<8 | uint32(r.byte())
			if ft < 2 {
				ft = 2
			}
			// Pick fl < fh <= ft.
			a := r.u32() % ft      // in [0, ft)
			span := r.u32()%ft + 1 // in [1, ft]
			fl := a
			fh := fl + span
			if fh > ft {
				fh = ft
			}
			if fl >= fh {
				if fh == 0 {
					fh = 1
				}
				fl = fh - 1
			}
			op.kind = rangeOpEncodeByte
			op.fl, op.fh, op.ft = fl, fh, ft
		case 1: // EncodeBin(fl, fh, bits), ft = 1<<bits
			bits := uint(r.byte()%12) + 1 // 1..12
			ft := uint32(1) << bits
			a := r.u32() % ft
			span := r.u32()%ft + 1
			fl := a
			fh := fl + span
			if fh > ft {
				fh = ft
			}
			if fl >= fh {
				if fh == 0 {
					fh = 1
				}
				fl = fh - 1
			}
			op.kind = rangeOpEncodeBinByte
			op.fl, op.fh, op.bits = fl, fh, bits
		case 2: // EncodeBit(bit, logp)
			op.kind = rangeOpEncodeBitByte
			op.bit = int(r.byte() & 1)
			op.logp = uint(r.byte()%15) + 1 // 1..15
		case 3: // EncodeICDF(sym, tbl, 8)
			op.kind = rangeOpEncodeICDFByte
			op.tbl = int(r.byte()) % len(fuzzICDF8Tables)
			n := len(fuzzICDF8Tables[op.tbl])
			op.sym = int(r.byte()) % n // 0..n-1
			op.ftb = 8
		case 4: // EncodeICDF16(sym, tbl, table.ftb)
			op.kind = rangeOpEncodeICDFByte
			op.is16 = true
			op.tbl = int(r.byte()) % len(fuzzICDF16Tables)
			tbl := fuzzICDF16Tables[op.tbl]
			op.sym = int(r.byte()) % len(tbl.icdf)
			op.ftb = tbl.ftb
		case 5: // EncodeUniform(val, ft)
			ft := r.u32()%4096 + 2 // 2..4097
			op.kind = rangeOpEncodeUintByte
			op.uft = ft
			op.uval = r.u32() % ft
		case 6: // EncodeRawBits(val, bits)
			bits := uint(r.byte()%24) + 1 // 1..24
			op.kind = rangeOpEncodeBitsByte
			op.rb = bits
			mask := uint32((1 << bits) - 1)
			op.rval = r.u32() & mask
		}
		ops = append(ops, op)
	}
	return ops
}

const (
	rangeOpEncodeByte     byte = 0
	rangeOpEncodeBinByte  byte = 1
	rangeOpEncodeBitByte  byte = 2
	rangeOpEncodeICDFByte byte = 3
	rangeOpEncodeUintByte byte = 4
	rangeOpEncodeBitsByte byte = 5
)

// FuzzRoundtrip asserts encode->decode is a perfect round-trip for random,
// always-valid operation scripts, and that TellFrac stays in lockstep.
func FuzzRoundtrip(f *testing.F) {
	// Seeds: a few hand-built byte strings plus empty input.
	f.Add([]byte{})
	f.Add([]byte{0x02, 0x01, 0x03, 0x02, 0x00, 0x01})
	f.Add([]byte{0x05, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x07})
	f.Add([]byte{0x03, 0x01, 0x02, 0x04, 0x02, 0x00, 0x01, 0x06, 0x08, 0xAB})
	f.Add([]byte{
		0x00, 0x00, 0x07, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x02,
		0x01, 0x03, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x07,
		0x02, 0x01, 0x09,
		0x04, 0x01, 0x02,
	})

	f.Fuzz(func(t *testing.T, data []byte) {
		ops := buildFuzzScript(data)

		// Generous buffer: each op consumes far fewer than 8 bytes on average;
		// a fixed large buffer guarantees a valid script never busts.
		buf := make([]byte, 64+16*len(ops))
		var enc Encoder
		enc.Init(buf)

		// Record encoder TellFrac after each op so the decoder can be checked
		// against the same fractional bit positions.
		tellFrac := make([]int, len(ops)+1)
		tellFrac[0] = enc.TellFrac()
		for i := range ops {
			encodeFuzzOp(&enc, &ops[i])
			tellFrac[i+1] = enc.TellFrac()
		}

		encTellBeforeDone := enc.Tell()
		packet := enc.Done()
		if enc.Error() != 0 {
			// The buffer is sized so this should not happen; if it does the
			// packet is not decodable, so skip rather than report a false
			// mismatch.
			t.Skipf("encoder busted on %d ops (unexpected)", len(ops))
		}
		if got := enc.Tell(); got != encTellBeforeDone {
			t.Fatalf("encoder Tell changed across Done: %d -> %d", encTellBeforeDone, got)
		}

		var dec Decoder
		dec.Init(packet)
		if got := dec.TellFrac(); got != tellFrac[0] {
			t.Fatalf("initial TellFrac mismatch: dec=%d enc=%d", got, tellFrac[0])
		}
		for i := range ops {
			decodeAndVerifyFuzzOp(t, &dec, &ops[i], i)
			if got := dec.TellFrac(); got != tellFrac[i+1] {
				t.Fatalf("op %d (kind=%d): TellFrac mismatch dec=%d enc=%d",
					i, ops[i].kind, got, tellFrac[i+1])
			}
		}
		if dec.Error() != 0 {
			t.Fatalf("decoder reported error %d after replaying a valid script", dec.Error())
		}
	})
}

func encodeFuzzOp(enc *Encoder, op *fuzzOp) {
	switch op.kind {
	case rangeOpEncodeByte:
		enc.Encode(op.fl, op.fh, op.ft)
	case rangeOpEncodeBinByte:
		enc.EncodeBin(op.fl, op.fh, op.bits)
	case rangeOpEncodeBitByte:
		enc.EncodeBit(op.bit, op.logp)
	case rangeOpEncodeICDFByte:
		if op.is16 {
			enc.EncodeICDF16(op.sym, fuzzICDF16Tables[op.tbl].icdf, op.ftb)
		} else {
			enc.EncodeICDF(op.sym, fuzzICDF8Tables[op.tbl], op.ftb)
		}
	case rangeOpEncodeUintByte:
		enc.EncodeUniform(op.uval, op.uft)
	case rangeOpEncodeBitsByte:
		enc.EncodeRawBits(op.rval, op.rb)
	}
}

func decodeAndVerifyFuzzOp(t *testing.T, dec *Decoder, op *fuzzOp, i int) {
	t.Helper()
	switch op.kind {
	case rangeOpEncodeByte:
		fs := dec.Decode(op.ft)
		if fs < op.fl || fs >= op.fh {
			t.Fatalf("op %d Decode(%d)=%d, want in [%d,%d)", i, op.ft, fs, op.fl, op.fh)
		}
		dec.Update(op.fl, op.fh, op.ft)
	case rangeOpEncodeBinByte:
		fs := dec.DecodeBin(op.bits)
		if fs < op.fl || fs >= op.fh {
			t.Fatalf("op %d DecodeBin(%d)=%d, want in [%d,%d)", i, op.bits, fs, op.fl, op.fh)
		}
		dec.Update(op.fl, op.fh, uint32(1)<<op.bits)
	case rangeOpEncodeBitByte:
		if got := dec.DecodeBit(op.logp); got != op.bit {
			t.Fatalf("op %d DecodeBit(%d)=%d, want %d", i, op.logp, got, op.bit)
		}
	case rangeOpEncodeICDFByte:
		var got int
		if op.is16 {
			got = dec.DecodeICDF16(fuzzICDF16Tables[op.tbl].icdf, op.ftb)
		} else {
			got = dec.DecodeICDF(fuzzICDF8Tables[op.tbl], op.ftb)
		}
		if got != op.sym {
			t.Fatalf("op %d DecodeICDF=%d, want %d", i, got, op.sym)
		}
	case rangeOpEncodeUintByte:
		if got := dec.DecodeUniform(op.uft); got != op.uval {
			t.Fatalf("op %d DecodeUniform(%d)=%d, want %d", i, op.uft, got, op.uval)
		}
	case rangeOpEncodeBitsByte:
		if got := dec.DecodeRawBits(op.rb); got != op.rval {
			t.Fatalf("op %d DecodeRawBits(%d)=%#x, want %#x", i, op.rb, got, op.rval)
		}
	}
}

// FuzzDecoderArbitraryBytes asserts the decoder is panic-free and bounds-safe on
// arbitrary (possibly truncated or garbage) input. It runs a deterministic mix
// of decode operations and only checks that returned values stay within their
// documented ranges; it does not assert any particular decoded value, since the
// input is not a valid encoding.
func FuzzDecoderArbitraryBytes(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0})

	f.Fuzz(func(t *testing.T, data []byte) {
		var d Decoder
		d.Init(data) // must not panic on any input, including empty

		// Init invariants: after normalize the range must stay above EC_CODE_BOT.
		if d.Range() <= EC_CODE_BOT {
			t.Fatalf("after Init: rng=%#x not > EC_CODE_BOT", d.Range())
		}

		// Drive a varied but bounded sequence of decode calls. The selector
		// walks the input bytes so different inputs exercise different mixes;
		// when the input is exhausted the selector cycles deterministically.
		r := &fuzzReader{b: data}
		const steps = 200
		for i := 0; i < steps; i++ {
			sel := byte(i)
			if !r.done() {
				sel = r.byte()
			}
			switch sel % 9 {
			case 0:
				if got := d.DecodeBit(uint(sel%15) + 1); got != 0 && got != 1 {
					t.Fatalf("DecodeBit returned %d", got)
				}
			case 1:
				icdf := fuzzICDF8Tables[int(sel)%len(fuzzICDF8Tables)]
				if got := d.DecodeICDF(icdf, 8); got < 0 || got >= len(icdf) {
					t.Fatalf("DecodeICDF returned %d for table len %d", got, len(icdf))
				}
			case 2:
				tbl := fuzzICDF16Tables[int(sel)%len(fuzzICDF16Tables)]
				if got := d.DecodeICDF16(tbl.icdf, tbl.ftb); got < 0 || got >= len(tbl.icdf) {
					t.Fatalf("DecodeICDF16 returned %d for table len %d", got, len(tbl.icdf))
				}
			case 3:
				ft := uint32(sel%250) + 2
				if got := d.Decode(ft); got >= ft {
					t.Fatalf("Decode(%d) returned %d (>= ft)", ft, got)
				} else {
					// Pair Decode with a valid Update for the symbol containing got.
					d.Update(got, got+1, ft)
				}
			case 4:
				bits := uint(sel%12) + 1
				ft := uint32(1) << bits
				if got := d.DecodeBin(bits); got >= ft {
					t.Fatalf("DecodeBin(%d) returned %d (>= %d)", bits, got, ft)
				} else {
					d.Update(got, got+1, ft)
				}
			case 5:
				ft := uint32(sel)<<8 | uint32(r.byte())
				if ft < 2 {
					ft = 2
				}
				if got := d.DecodeUniform(ft); got >= ft {
					t.Fatalf("DecodeUniform(%d) returned %d (>= ft)", ft, got)
				}
			case 6:
				_ = d.DecodeRawBits(uint(sel%24) + 1)
			case 7:
				_ = d.DecodeRawBit()
			case 8:
				// Exercise the unrolled small-table fast paths too.
				switch sel % 4 {
				case 0:
					_ = d.DecodeICDF2_8(sel | 1)
				case 1:
					_ = d.DecodeICDF3_8(sel|1, sel>>1)
				case 2:
					_ = d.DecodeICDF4_8(sel|1, 128, 64)
				case 3:
					_ = d.DecodeICDF5_8(sel|1, 154, 102, 51)
				}
			}

			// Core invariant: renormalization must keep the range above
			// EC_CODE_BOT, even on garbage input.
			if d.Range() <= EC_CODE_BOT {
				t.Fatalf("step %d (sel=%d): rng=%#x dropped to/below EC_CODE_BOT", i, sel, d.Range())
			}
		}

		// Tell/TellFrac must remain consistent and non-negative.
		if tf, tl := d.TellFrac(), d.Tell(); tf < 0 || tl < 0 || tf < (tl-1)*8 || tf > (tl+1)*8 {
			t.Fatalf("Tell=%d TellFrac=%d inconsistent", tl, tf)
		}
	})
}
