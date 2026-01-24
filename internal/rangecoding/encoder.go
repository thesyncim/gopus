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
}

// carryOut handles carry propagation when outputting bytes.
// This is based on libopus celt/entenc.c ec_enc_carry_out, but with output
// bytes inverted (255 - B) to match the decoder's XOR-255 reconstruction.
//
// The decoder's normalize does: val = (val << 8) + (255 &^ sym)
// where sym is derived from input bytes. For correct round-trip, the encoder
// must output complemented bytes so the decoder's inversion reconstructs
// the original interval.
func (e *Encoder) carryOut(c int) {
	// Complement the input to work in "decoder space"
	// The encoder tracks val in increasing direction, decoder in decreasing
	c = EC_SYM_MAX - (c & EC_SYM_MAX)

	if e.rem >= 0 {
		// Check for carry (now inverted: carry when c wraps below 0)
		if c > EC_SYM_MAX {
			// Underflow in complemented space = carry in original
			e.writeByte(byte(e.rem - 1))
			for e.ext > 0 {
				e.writeByte(0xFF)
				e.ext--
			}
			e.rem = c & EC_SYM_MAX
		} else if c == 0 {
			// Byte is 0x00 in complemented space (was 0xFF): might borrow later
			e.ext++
		} else {
			// No carry: output rem as-is, output pending 0x00 bytes
			e.writeByte(byte(e.rem))
			for e.ext > 0 {
				e.writeByte(0)
				e.ext--
			}
			e.rem = c
		}
	} else {
		// First byte: just store it
		e.rem = c
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
	if e.offs < e.storage-e.endOffs {
		e.buf[e.offs] = b
		e.offs++
	}
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

// EncodeICDF encodes a symbol using an inverse CDF table.
// s is the symbol to encode (0 to len(icdf)-2).
// icdf is the inverse cumulative distribution function table (decreasing values).
// ftb is the number of bits of precision (total = 1 << ftb).
//
// The decoder maps: val >= r*icdf[k] -> symbol k
// So symbol 0 requires val >= r*icdf[0] (high val), symbol N requires low val.
// The encoder must place symbol 0 in the HIGH interval.
func (e *Encoder) EncodeICDF(s int, icdf []uint8, ftb uint) {
	ft := uint32(1) << ftb
	// For decoder compatibility, symbol s maps to interval [icdf[s], icdf[s-1])
	// scaled by r. In terms of fl/fh:
	// fl = icdf[s] (the threshold for this symbol)
	// fh = icdf[s-1] (the threshold for the previous symbol, or ft for s=0)
	fl := uint32(icdf[s])
	var fh uint32
	if s > 0 {
		fh = uint32(icdf[s-1])
	} else {
		fh = ft
	}
	e.Encode(fl, fh, ft)
}

// EncodeICDF16 encodes a symbol using a uint16 ICDF table.
// Required because SILK tables use uint16 (256 doesn't fit in uint8).
// Per RFC 6716 Section 4.1.
//
// Note: SILK ICDF tables typically have icdf[0] = 256, meaning symbol 0
// has zero probability and cannot be encoded. If s=0 is passed with such
// a table, this function clamps s to 1 to avoid infinite loops.
func (e *Encoder) EncodeICDF16(s int, icdf []uint16, ftb uint) {
	// Clamp symbol to valid range
	if s < 0 {
		s = 0
	}
	maxSymbol := len(icdf) - 1
	if s > maxSymbol {
		s = maxSymbol
	}

	ft := uint32(1) << ftb
	var fl, fh uint32
	if s > 0 {
		fl = ft - uint32(icdf[s-1])
	}
	fh = ft - uint32(icdf[s])

	// Check for zero-probability symbol (would cause infinite loop)
	// If fl >= fh, the symbol has zero or negative probability
	if fl >= fh {
		// Skip to next valid symbol
		for s < maxSymbol && fl >= fh {
			s++
			fl = ft - uint32(icdf[s-1])
			fh = ft - uint32(icdf[s])
		}
		// If still invalid, clamp to last symbol
		if fl >= fh {
			s = maxSymbol
			fl = ft - uint32(icdf[s-1])
			fh = ft - uint32(icdf[s])
		}
	}

	e.Encode(fl, fh, ft)
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

	// Flush pending byte
	if e.rem >= 0 {
		e.writeByte(byte(e.rem))
		for e.ext > 0 {
			e.writeByte(0)
			e.ext--
		}
	}

	// Flush any remaining raw bits in the end window
	if e.nendBits > 0 {
		e.writeEndByte(byte(e.endWindow))
		e.nendBits = 0
		e.endWindow = 0
	}

	// Combine front bytes with end bytes
	if e.endOffs > 0 {
		totalSize := e.offs + e.endOffs
		if totalSize > e.storage {
			totalSize = e.storage
		}
		// Copy end bytes to after front bytes
		copy(e.buf[e.offs:], e.buf[e.storage-e.endOffs:e.storage])
		return e.buf[:totalSize]
	}

	return e.buf[:e.offs]
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
	e.endWindow |= val << e.nendBits
	e.nendBits += int(bits)
	for e.nendBits >= 8 {
		e.writeEndByte(byte(e.endWindow))
		e.endWindow >>= 8
		e.nendBits -= 8
	}
}

// writeEndByte writes a byte to the end of the buffer (growing backwards).
func (e *Encoder) writeEndByte(b byte) {
	e.endOffs++
	if e.endOffs <= e.storage-e.offs {
		e.buf[e.storage-e.endOffs] = b
	}
}
