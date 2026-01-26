package rangecoding

// Encoder implements the range encoder per RFC 6716 Section 4.1.
// This is a bit-exact port of libopus celt/entenc.c.
// The encoder is the symmetric inverse of the decoder.
type Encoder struct {
	buf        []byte // Output buffer (pre-allocated)
	storage    uint32 // Buffer capacity
	offs       uint32 // Current write offset
	endOffs    uint32 // End offset for raw bits (writes from end)
	endWindow  uint32 // Window for raw bits at end
	nendBits   int    // Bits in end window
	nbitsTotal int    // Total bits written (for tell functions)
	rng        uint32 // Range size
	val        uint32 // Low end of range
	rem        int    // Buffered byte for carry propagation (-1 = sentinel)
	ext        uint32 // Count of pending 0xFF bytes
	err        int    // Error flag (non-zero on failure)
}

// Init initializes the encoder with the given output buffer.
// The buffer must be pre-allocated to the maximum expected output size.
func (e *Encoder) Init(buf []byte) {
	e.buf = buf
	e.storage = uint32(len(buf))
	e.offs = 0
	e.endOffs = 0
	e.endWindow = 0
	e.nendBits = 0
	e.nbitsTotal = EC_CODE_BITS + 1
	e.rng = EC_CODE_TOP
	e.val = 0
	e.rem = -1 // Sentinel: no bytes buffered yet
	e.ext = 0
	e.err = 0
}

// carryOut handles carry propagation when outputting bytes.
// This is a direct port of libopus celt/entenc.c ec_enc_carry_out.
// NO byte complementing - the decoder handles that with its (255 &^ sym) formula.
//
// When symbol equals EC_SYM_MAX (0xFF), we increment ext (pending 0xFF bytes)
// because we can't know yet if there will be a carry from future symbols.
// When we get a non-0xFF symbol:
//   - Extract carry from high byte (symbol overflow)
//   - Write the buffered rem byte plus carry
//   - Write pending 0xFF bytes as (0xFF + carry) & 0xFF = 0x00 if carry, 0xFF if not
//   - Buffer the new symbol
//
// Reference: libopus celt/entenc.c ec_enc_carry_out
func (e *Encoder) carryOut(c int) {
	if c != EC_SYM_MAX {
		// c is not 0xFF, so we can flush buffered bytes
		carry := c >> EC_SYM_BITS // Extract carry from potential overflow

		// Write the previously buffered byte plus carry (if any)
		if e.rem >= 0 {
			e.writeByte(byte(e.rem + carry))
		}

		// Write any pending 0xFF bytes, adjusted for carry
		// This is a SEPARATE if, not inside the rem >= 0 block!
		// If carry=1: 0xFF + 1 = 0x100, masked to 0x00
		// If carry=0: 0xFF + 0 = 0xFF
		if e.ext > 0 {
			sym := (EC_SYM_MAX + carry) & EC_SYM_MAX
			for e.ext > 0 {
				e.writeByte(byte(sym))
				e.ext--
			}
		}

		// Buffer the new symbol (low 8 bits only)
		e.rem = c & EC_SYM_MAX
	} else {
		// Symbol is 0xFF - can't flush yet because might need to carry
		e.ext++
	}
}

// normalize handles range renormalization and byte output.
// This follows libopus celt/entenc.c ec_enc_normalize exactly.
//
// The encoder outputs the high bits of val, and the decoder reconstructs
// by reading those bytes and applying the inverse operation (255 &^ sym).
func (e *Encoder) normalize() {
	for e.rng <= EC_CODE_BOT {
		// Extract high bits to output via carry propagation
		e.carryOut(int(e.val >> EC_CODE_SHIFT))
		// Shift out 8 bits
		e.val = (e.val << EC_SYM_BITS) & (EC_CODE_TOP - 1)
		e.rng <<= EC_SYM_BITS
		e.nbitsTotal += EC_SYM_BITS
	}
}

// writeByte writes a byte to the output buffer.
func (e *Encoder) writeByte(b byte) {
	if e.offs+e.endOffs >= e.storage {
		e.err = -1
		return
	}
	e.buf[e.offs] = b
	e.offs++
}

// Encode encodes a symbol with cumulative frequencies [fl, fh) out of ft.
// fl is the cumulative frequency of symbols before this one.
// fh is the cumulative frequency up to and including this symbol.
// ft is the total frequency count.
func (e *Encoder) Encode(fl, fh, ft uint32) {
	r := e.rng / ft
	if fl > 0 {
		e.val += e.rng - r*(ft-fl)
		e.rng = r * (fh - fl)
	} else {
		e.rng -= r * (ft - fh)
	}
	e.normalize()
}

// EncodeBin encodes a symbol with power-of-two total frequency (1<<bits).
// This mirrors libopus ec_encode_bin.
func (e *Encoder) EncodeBin(fl, fh uint32, bits uint) {
	if bits == 0 {
		return
	}
	r := e.rng >> bits
	ft := uint32(1) << bits
	if fl > 0 {
		e.val += e.rng - r*(ft-fl)
		e.rng = r * (fh - fl)
	} else {
		e.rng -= r * (ft - fh)
	}
	e.normalize()
}

// EncodeICDF encodes a symbol using an inverse CDF table.
// s is the symbol to encode (0 to len(icdf)-2).
// icdf is the inverse cumulative distribution function table (decreasing values).
// ftb is the number of bits of precision (total = 1 << ftb).
//
// This is a direct port of libopus ec_enc_icdf.
func (e *Encoder) EncodeICDF(s int, icdf []uint8, ftb uint) {
	r := e.rng >> ftb
	if s > 0 {
		e.val += e.rng - r*uint32(icdf[s-1])
		e.rng = r * uint32(icdf[s-1]-icdf[s])
	} else {
		e.rng -= r * uint32(icdf[s])
	}
	e.normalize()
}

// EncodeICDF16 encodes a symbol using a uint16 ICDF table.
// Required because SILK tables use uint16 (256 doesn't fit in uint8).
// Per RFC 6716 Section 4.1.
//
// This is the uint16 variant of EncodeICDF, matching libopus ec_enc_icdf.
func (e *Encoder) EncodeICDF16(s int, icdf []uint16, ftb uint) {
	// Clamp symbol to valid range
	if s < 0 {
		s = 0
	}
	maxSymbol := len(icdf) - 2 // Last entry is always 0, not a valid symbol
	if s > maxSymbol {
		s = maxSymbol
	}

	r := e.rng >> ftb
	if s > 0 {
		e.val += e.rng - r*uint32(icdf[s-1])
		e.rng = r * uint32(icdf[s-1]-icdf[s])
	} else {
		e.rng -= r * uint32(icdf[s])
	}

	// Safety: ensure rng doesn't become 0 (would cause infinite loop)
	if e.rng == 0 {
		e.rng = 1
	}

	e.normalize()
}

// EncodeBit encodes a single bit with the given log probability.
// val is the bit to encode (0 or 1).
// logp is the log probability: P(0) = 1 - 1/(2^logp), P(1) = 1/(2^logp).
//
// Per RFC 6716 Section 4.1, the probability regions are:
// - [0, rng-r): bit = 0, probability = (2^logp - 1) / 2^logp
// - [rng-r, rng): bit = 1, probability = 1 / 2^logp
//
// For silence flag (logp=15): P(silence=1) = 1/32768, which is very rare.
//
// Encoder interval assignment (symmetric with decoder):
// - bit=0: use interval [0, threshold), rng = threshold, val unchanged
// - bit=1: use interval [threshold, rng), val += threshold, rng = r
func (e *Encoder) EncodeBit(val int, logp uint) {
	r := e.rng >> logp
	threshold := e.rng - r // '1' probability region is at TOP of range
	if val != 0 {
		// Encode 1: use interval [threshold, rng)
		e.val += threshold
		e.rng = r
	} else {
		// Encode 0: use interval [0, threshold)
		e.rng = threshold
	}
	e.normalize()
}

// Done finalizes the encoding and returns the encoded bytes.
// After calling Done, the encoder should not be used without re-initializing.
//
// This follows libopus celt/entenc.c ec_enc_done exactly.
func (e *Encoder) Done() []byte {
	// Compute how many bits we need to output to uniquely identify the interval.
	l := EC_CODE_BITS - ilog(e.rng)

	// Compute mask for rounding
	var msk uint32
	if l < EC_CODE_BITS {
		msk = (EC_CODE_TOP - 1) >> l
	}

	// Round up to alignment boundary
	end := (e.val + msk) & ^msk

	// Check if end is still within [val, val+rng)
	if (end | msk) >= e.val+e.rng {
		l++
		msk >>= 1
		end = (e.val + msk) & ^msk
	}

	// Output remaining bytes via carry propagation
	for l > 0 {
		e.carryOut(int(end >> EC_CODE_SHIFT))
		end = (end << EC_SYM_BITS) & (EC_CODE_TOP - 1)
		l -= EC_SYM_BITS
	}

	// Flush buffered range coder bytes (matches libopus ec_enc_done).
	if e.rem >= 0 || e.ext > 0 {
		e.carryOut(0)
	}

	// Flush any buffered raw bits as end bytes.
	window := e.endWindow
	used := e.nendBits
	for used >= EC_SYM_BITS {
		e.writeEndByte(byte(window & EC_SYM_MAX))
		window >>= EC_SYM_BITS
		used -= EC_SYM_BITS
	}

	if e.err == 0 {
		if e.buf != nil {
			start := int(e.offs)
			endIdx := int(e.storage - e.endOffs)
			for i := start; i < endIdx; i++ {
				e.buf[i] = 0
			}
		}
		if used > 0 {
			if e.endOffs >= e.storage {
				e.err = -1
			} else {
				l = -l
				if e.offs+e.endOffs >= e.storage && l < used {
					window &= (1 << l) - 1
					e.err = -1
				}
				idx := int(e.storage - e.endOffs - 1)
				if idx >= 0 && idx < len(e.buf) {
					e.buf[idx] |= byte(window)
				}
			}
		}
	}

	if e.err != 0 {
		used = 0
		if int(e.storage) <= len(e.buf) {
			return e.buf[:e.storage]
		}
		return e.buf
	}

	// Combine front bytes with end bytes
	padLen := 0
	if used > 0 {
		padLen = 1
		if int(e.offs) < len(e.buf) {
			e.buf[e.offs] = byte(window)
		}
	}

	packedSize := int(e.offs + e.endOffs + uint32(padLen))
	if e.endOffs > 0 {
		dst := int(e.offs) + padLen
		copy(e.buf[dst:], e.buf[e.storage-e.endOffs:e.storage])
	}

	if packedSize < 0 {
		packedSize = 0
	}
	if packedSize > len(e.buf) {
		packedSize = len(e.buf)
	}
	return e.buf[:packedSize]
}

// Tell returns the number of bits written so far.
func (e *Encoder) Tell() int {
	return e.nbitsTotal - ilog(e.rng)
}

// TellFrac returns the number of bits written with 1/8 bit precision.
// The value is in 1/8 bits, so divide by 8 to compare with Tell().
func (e *Encoder) TellFrac() int {
	correction := [8]uint32{35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535}

	nbits := e.nbitsTotal << 3
	l := ilog(e.rng)
	r := e.rng >> (l - 16)
	b := int((r >> 12) - 8)
	if r > correction[b] {
		b++
	}
	return nbits - ((l << 3) + b)
}

// Range returns the current range value (for testing/debugging).
func (e *Encoder) Range() uint32 {
	return e.rng
}

// Val returns the current val (low end of range) (for testing/debugging).
func (e *Encoder) Val() uint32 {
	return e.val
}

// Rem returns the stored remainder byte (for testing/debugging).
func (e *Encoder) Rem() int {
	return e.rem
}

// StorageBits returns the total number of bits in the output buffer.
func (e *Encoder) StorageBits() int {
	return int(e.storage * 8)
}

// Ext returns the extension count (for testing/debugging).
func (e *Encoder) Ext() uint32 {
	return e.ext
}

// Error returns the encoder error flag. Non-zero indicates an error.
func (e *Encoder) Error() int {
	return e.err
}

// RangeBytes returns the number of range-coded bytes written.
// This mirrors libopus ec_range_bytes.
func (e *Encoder) RangeBytes() int {
	return int(e.offs)
}

// PatchInitialBits overwrites the first few bits in the range coder stream.
// This mirrors libopus ec_enc_patch_initial_bits and is intended for testing.
func (e *Encoder) PatchInitialBits(val uint32, nbits uint) {
	if nbits == 0 || nbits > EC_SYM_BITS {
		e.err = -1
		return
	}
	shift := EC_SYM_BITS - nbits
	mask := (uint32(1)<<nbits - 1) << shift
	if e.offs > 0 {
		e.buf[0] = byte((uint32(e.buf[0]) &^ mask) | (val << shift))
		return
	}
	if e.rem >= 0 {
		e.rem = int((uint32(e.rem) &^ mask) | (val << shift))
		return
	}
	if e.rng <= (EC_CODE_TOP >> nbits) {
		shiftedMask := mask << EC_CODE_SHIFT
		e.val = (e.val &^ shiftedMask) | (val << (EC_CODE_SHIFT + shift))
		return
	}
	e.err = -1
}

// EncodeUniform encodes a uniformly distributed value in the range [0, ft).
// This is used for fine energy bits and PVQ indices.
// Reference: libopus celt/entenc.c ec_enc_uint()
func (e *Encoder) EncodeUniform(val uint32, ft uint32) {
	if ft <= 1 {
		return // Only one possible value, nothing to encode
	}

	// Calculate number of bits needed
	ftb := uint(ilog(ft - 1))
	if ftb > EC_SYM_BITS {
		// Multi-byte case: encode high bits with range coder, low bits raw
		ftb -= EC_SYM_BITS
		ft1 := (ft - 1) >> ftb
		e.encodeUniformInternal(val>>ftb, ft1+1)
		// Encode low bits raw
		e.EncodeRawBits(val&((1<<ftb)-1), ftb)
	} else {
		// Single-byte case
		e.encodeUniformInternal(val, ft)
	}
}

// encodeUniformInternal encodes a uniform value when ft <= 256.
// Uses the same approach as Encode() for uniformly distributed values.
func (e *Encoder) encodeUniformInternal(val uint32, ft uint32) {
	// For uniform distribution, fl=val, fh=val+1
	// Using the Encode formula adapted for uniform case
	r := e.rng / ft
	if val > 0 {
		e.val += e.rng - r*(ft-val)
		e.rng = r
	} else {
		// val == 0: stay at current position
		e.rng -= r * (ft - 1)
	}
	e.normalize()
}

// EncodeRawBits writes raw bits to the end of the buffer.
// This is the inverse of DecodeRawBits.
func (e *Encoder) EncodeRawBits(val uint32, bits uint) {
	if bits == 0 {
		return
	}
	window := e.endWindow
	used := e.nendBits
	if used+int(bits) > EC_WINDOW_SIZE {
		for used >= EC_SYM_BITS {
			e.writeEndByte(byte(window & EC_SYM_MAX))
			window >>= EC_SYM_BITS
			used -= EC_SYM_BITS
		}
	}
	window |= val << used
	used += int(bits)
	e.endWindow = window
	e.nendBits = used
	e.nbitsTotal += int(bits)
}

// writeEndByte writes a byte to the end of the buffer (growing backwards).
func (e *Encoder) writeEndByte(b byte) {
	if e.offs+e.endOffs >= e.storage {
		e.err = -1
		return
	}
	e.endOffs++
	e.buf[e.storage-e.endOffs] = b
}
