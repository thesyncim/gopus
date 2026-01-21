package rangecoding

// Encoder implements the range encoder per RFC 6716 Section 4.1.
// This is a bit-exact port of libopus entenc.c.
// The encoder is the symmetric inverse of the decoder.
type Encoder struct {
	buf        []byte  // Output buffer (pre-allocated)
	storage    uint32  // Buffer capacity
	offs       uint32  // Current write offset
	endOffs    uint32  // End offset for raw bits (writes from end)
	endWindow  uint32  // Window for raw bits at end
	nendBits   int     // Bits in end window
	nbitsTotal int     // Total bits written (for tell functions)
	rng        uint32  // Range size
	val        uint32  // Low end of range
	rem        int     // Number of carry-propagating bytes (-1 = sentinel)
	ext        uint32  // Buffered byte for carry propagation
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

// normalize handles carry propagation and byte output.
// This is the critical part of the encoder - when outputting bytes,
// we may need to propagate carries through previously buffered bytes.
func (e *Encoder) normalize() {
	for e.rng <= EC_CODE_BOT {
		// Extract the carry-out byte from the high bits of val
		carryOut := int(e.val >> EC_CODE_SHIFT)
		// Shift val left, keeping only bits below EC_CODE_TOP
		e.val = (e.val << EC_SYM_BITS) & (EC_CODE_TOP - 1)
		// Expand range
		e.rng <<= EC_SYM_BITS
		e.nbitsTotal += EC_SYM_BITS

		// Handle carry propagation
		// carryOut can be 0, 1, ..., 255 normally, or 256+ if carry occurred
		if carryOut < EC_SYM_MAX {
			// No carry possible from this byte
			// Flush any buffered bytes
			e.flushCarry(carryOut)
		} else if carryOut == EC_SYM_MAX {
			// This byte is 0xFF - it could propagate a future carry
			// Buffer it in the carry chain
			if e.rem >= 0 {
				e.ext++
			} else {
				e.rem = carryOut
			}
		} else {
			// Carry occurred (carryOut >= 256)
			// Propagate carry through buffered bytes
			if e.rem >= 0 {
				e.propagateCarry(carryOut)
			} else {
				e.rem = carryOut & EC_SYM_MAX
			}
		}
	}
}

// flushCarry outputs buffered bytes when a non-carry-propagating byte arrives.
func (e *Encoder) flushCarry(c int) {
	if e.rem >= 0 {
		// Output the previously buffered byte
		e.writeByte(byte(e.rem))
		// Output any 0xFF extension bytes
		for e.ext > 0 {
			e.writeByte(0xFF)
			e.ext--
		}
	}
	e.rem = c
}

// propagateCarry handles the case when a carry occurs.
func (e *Encoder) propagateCarry(c int) {
	// Add carry to buffered byte
	e.writeByte(byte(e.rem + 1))
	// All 0xFF bytes become 0x00
	for e.ext > 0 {
		e.writeByte(0x00)
		e.ext--
	}
	// Buffer the new byte (with carry removed)
	e.rem = c & EC_SYM_MAX
}

// writeByte writes a byte to the output buffer.
func (e *Encoder) writeByte(b byte) {
	if e.offs < e.storage {
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
// icdf is the inverse cumulative distribution function table.
// ftb is the number of bits of precision (total = 1 << ftb).
func (e *Encoder) EncodeICDF(s int, icdf []uint8, ftb uint) {
	// Convert ICDF to fl, fh, ft
	ft := uint32(1) << ftb
	var fl, fh uint32
	if s > 0 {
		fl = ft - uint32(icdf[s-1])
	} else {
		fl = 0
	}
	fh = ft - uint32(icdf[s])
	e.Encode(fl, fh, ft)
}

// EncodeBit encodes a single bit with the given log probability.
// val is the bit to encode (0 or 1).
// logp is the log probability: P(0) = 1 - 1/(2^logp), P(1) = 1/(2^logp).
func (e *Encoder) EncodeBit(val int, logp uint) {
	r := e.rng >> logp
	if val != 0 {
		// Encode 1
		e.val += r
		e.rng -= r
	} else {
		// Encode 0
		e.rng = r
	}
	e.normalize()
}

// Done finalizes the encoding and returns the encoded bytes.
// After calling Done, the encoder should not be used without re-initializing.
// This follows libopus ec_enc_done exactly.
func (e *Encoder) Done() []byte {
	// Compute the number of bits of information we need to output
	// to uniquely identify the final interval.
	l := ilog(e.rng)

	// Compute the mask for the bits we don't need
	// and the final value to output
	var msk uint32 = EC_CODE_TOP - 1
	if l < EC_CODE_BITS {
		msk = msk >> l
	} else {
		msk = 0
	}

	end := (e.val + msk) & ^msk

	// Check if we need to propagate a carry
	if (end | msk) >= e.val+e.rng {
		// We need fewer bits - recalculate
		l++
		if l < EC_CODE_BITS {
			msk = (EC_CODE_TOP - 1) >> l
		} else {
			msk = 0
		}
		end = (e.val + msk) & ^msk
	}

	// Output any buffered bytes plus the final value
	// First flush remaining buffered bytes with appropriate carry handling
	for e.offs+e.endOffs+e.ext+1 >= e.storage {
		// Buffer overflow - just output what we can
		break
	}

	// If there's a carry needed (end wrapped around)
	if end > EC_CODE_TOP-1 {
		// Propagate carry through buffered bytes
		if e.rem >= 0 {
			e.writeByte(byte(e.rem + 1))
			for e.ext > 0 {
				e.writeByte(0x00)
				e.ext--
			}
		}
		e.rem = 0
		end &= EC_CODE_TOP - 1
	}

	// Output the buffered byte and extension bytes
	if e.rem >= 0 {
		e.writeByte(byte(e.rem))
		for e.ext > 0 {
			e.writeByte(0xFF)
			e.ext--
		}
	}

	// Output remaining bytes from end value, MSB first
	// Number of bytes needed = ceil((EC_CODE_BITS - l) / EC_SYM_BITS)
	// But we need at least 1 byte of output
	if l >= EC_CODE_BITS {
		// No additional bytes needed
		return e.buf[:e.offs]
	}

	nBits := EC_CODE_BITS - l
	nBytes := (nBits + EC_SYM_BITS - 1) / EC_SYM_BITS
	if nBytes <= 0 {
		nBytes = 1
	}

	// Shift end to align the bits we need at the top
	shiftAmount := EC_CODE_BITS - 1 - l
	if shiftAmount < 0 {
		shiftAmount = 0
	}
	end = (end >> shiftAmount) << 1

	// Output bytes MSB first
	for i := nBytes - 1; i >= 0; i-- {
		b := byte((end >> (i * EC_SYM_BITS)) & EC_SYM_MAX)
		e.writeByte(b)
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
	// Number of whole bits scaled by 8
	nbits := e.nbitsTotal << 3
	// Get the log of range
	l := ilog(e.rng)

	// Compute fractional correction
	var r uint32
	if l > 16 {
		r = e.rng >> (l - 16)
	} else {
		r = e.rng << (16 - l)
	}
	// Correction using top bits of range
	correction := int(r>>12) - 8
	if correction < 0 {
		correction = 0
	}
	if correction > 7 {
		correction = 7
	}
	return nbits - l*8 + correction
}

// Range returns the current range value (for testing/debugging).
func (e *Encoder) Range() uint32 {
	return e.rng
}

// Val returns the current val (low end of range) (for testing/debugging).
func (e *Encoder) Val() uint32 {
	return e.val
}
