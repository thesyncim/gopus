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
	shrunk     bool   // True if Shrink() was called (for CBR mode)
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

// Shrink reduces the storage size to the given number of bytes.
// This is used in CBR mode to ensure the output packet is exactly the target size.
// The raw bits written from the end are moved to the new end position.
// This is a direct port of libopus celt/entenc.c ec_enc_shrink.
//
// After calling Shrink, Done() will return exactly 'size' bytes (padded with zeros).
func (e *Encoder) Shrink(size uint32) {
	if e.offs+e.endOffs > size {
		// Can't shrink to less than already written
		e.err = -1
		return
	}
	if size > e.storage {
		// Can't grow the buffer
		return
	}
	if e.endOffs > 0 {
		// Move end bytes to new position
		copy(e.buf[size-e.endOffs:size], e.buf[e.storage-e.endOffs:e.storage])
	}
	e.storage = size
	e.shrunk = true
}

// Limit reduces the storage size to the given number of bytes without forcing
// CBR-style output length. This is useful for VBR/CVBR paths that need a bit
// budget cap but still want Done() to return the actual number of bytes used.
// It mirrors ec_enc_shrink() without setting the "shrunk" flag.
func (e *Encoder) Limit(size uint32) {
	if e.offs+e.endOffs > size {
		// Can't shrink to less than already written
		e.err = -1
		return
	}
	if size > e.storage {
		// Can't grow the buffer
		return
	}
	if e.endOffs > 0 {
		// Move end bytes to new position
		copy(e.buf[size-e.endOffs:size], e.buf[e.storage-e.endOffs:e.storage])
	}
	e.storage = size
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
	if fl > 0 {
		e.val += e.rng - r*((uint32(1)<<bits)-fl)
		e.rng = r * (fh - fl)
	} else {
		e.rng -= r * ((uint32(1) << bits) - fh)
	}
	e.normalize()
}

// EncodeICDF8 encodes a symbol using a uint8 ICDF table.
// s is the symbol to encode (0 to len(icdf)-2).
// icdf is the inverse cumulative distribution function table (decreasing values).
// ftb is the number of bits of precision (total = 1 << ftb).
//
// This is the uint8 variant of EncodeICDF for SILK tables.
func (e *Encoder) EncodeICDF8(s int, icdf []uint8, ftb uint) {
	e.EncodeICDF(s, icdf, ftb)
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
	if logp == 0 {
		return
	}
	// logp is the log probability: P(1) = 1 / 2^logp (rare, bottom region).
	r := e.rng
	s := r >> logp
	if val != 0 {
		e.val += r - s
		e.rng = s
	} else {
		e.rng = r - s
	}
	e.normalize()
}

// Done finalizes the encoding and returns the encoded bytes.
// After calling Done, the encoder should not be used without re-initializing.
//
// This follows libopus celt/entenc.c ec_enc_done exactly.
func (e *Encoder) Done() []byte {
	// Compute how many bits we need to output to uniquely identify the interval.
	l := EC_CODE_BITS - int(ilog(e.rng))

	// Compute mask for rounding
	var msk uint32 = (EC_CODE_TOP - 1) >> uint(l)

	// Round up to alignment boundary
	end := (e.val + msk) & ^msk

	// Check if end is still within [val, val+rng)
	if (end | msk) >= e.val+e.rng {
		l++
		msk >>= 1
		end = (e.val + msk) & ^msk
	}

	// println("rangeCoder Done: l=", l, "rng=", e.rng, "val=", e.val, "end=", end, "Tell=", e.Tell())

	// Output remaining bytes via carry propagation
	for l > 0 {
		e.carryOut(int(end >> EC_CODE_SHIFT))
		end = (end << EC_SYM_BITS) & (EC_CODE_TOP - 1)
		l -= EC_SYM_BITS
	}

	// Final symbol buffer flush
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
				usable := -l
				if usable < 0 {
					usable = 0
				}
				if int(e.offs+e.endOffs) >= int(e.storage) && usable < used {
					if usable >= 32 {
						// No masking needed for full 32-bit window.
					} else if usable <= 0 {
						window = 0
					} else {
						window &= (uint32(1) << usable) - 1
					}
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
		if int(e.storage) <= len(e.buf) {
			return e.buf[:e.storage]
		}
		return e.buf
	}

	// Calculate packed size
	padSize := 0
	if used > 0 && e.offs+e.endOffs < e.storage {
		padSize = 1
		if int(e.offs) < len(e.buf) {
			e.buf[e.offs] = byte(window)
		}
	}
	packedSize := int(e.offs + e.endOffs + uint32(padSize))

	// For CBR mode (when Shrink was called), return exactly storage bytes.
	if e.shrunk {
		packedSize = int(e.storage)
	}

	if e.endOffs > 0 {
		dst := int(e.offs) + padSize
		copy(e.buf[dst:], e.buf[e.storage-e.endOffs:e.storage])
	}

	if packedSize < 0 {
		packedSize = 0
	}
	if packedSize > int(e.storage) {
		e.err = -1
		packedSize = int(e.storage)
	}
	if packedSize > len(e.buf) {
		packedSize = len(e.buf)
	}

	return e.buf[:packedSize]
}

// Tell returns the number of bits written so far.
func (e *Encoder) Tell() int {
	return e.nbitsTotal - int(ilog(e.rng))
}

// TellFrac returns the number of bits written with 1/8 bit precision.
// The value is in 1/8 bits, so divide by 8 to compare with Tell().
func (e *Encoder) TellFrac() int {
	correction := [8]uint32{35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535}

	nbits := e.nbitsTotal << 3
	l := int(ilog(e.rng))
	r := e.rng >> (uint(l) - 16)
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

// Offs returns the current write offset in bytes.
func (e *Encoder) Offs() uint32 {
	return e.offs
}

// Buffer returns the underlying output buffer.
// Callers must treat the returned slice as read-only.
func (e *Encoder) Buffer() []byte {
	return e.buf
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

// Storage returns the storage capacity in bytes.
func (e *Encoder) Storage() int {
	return int(e.storage)
}

// Ext returns the extension count (for testing/debugging).
func (e *Encoder) Ext() uint32 {
	return e.ext
}

// Error returns the encoder error flag. Non-zero indicates an error.
func (e *Encoder) Error() int {
	return e.err
}

// EncoderState captures the encoder state for save/restore operations.
// This is used by theta RDO to try different quantization choices.
type EncoderState struct {
	offs       uint32
	endOffs    uint32
	endWindow  uint32
	nendBits   int
	nbitsTotal int
	rng        uint32
	val        uint32
	rem        int
	ext        uint32
	err        int
	// Buffer bytes are saved separately for restoration
	bufFront []byte // bytes from [0, offs)
	bufBack  []byte // bytes from [storage-endOffs, storage)
}

// SaveState captures the current encoder state for later restoration.
// This allows trying different encoding choices and restoring to try again.
func (e *Encoder) SaveState() *EncoderState {
	state := &EncoderState{}
	e.SaveStateInto(state)
	return state
}

// SaveStateInto captures the current encoder state into a pre-allocated state struct.
// This is the allocation-free version of SaveState for hot paths.
func (e *Encoder) SaveStateInto(state *EncoderState) {
	state.offs = e.offs
	state.endOffs = e.endOffs
	state.endWindow = e.endWindow
	state.nendBits = e.nendBits
	state.nbitsTotal = e.nbitsTotal
	state.rng = e.rng
	state.val = e.val
	state.rem = e.rem
	state.ext = e.ext
	state.err = e.err

	// Save the bytes that have been written - reuse existing slices if large enough
	if e.offs > 0 {
		if cap(state.bufFront) < int(e.offs) {
			state.bufFront = make([]byte, e.offs)
		} else {
			state.bufFront = state.bufFront[:e.offs]
		}
		copy(state.bufFront, e.buf[:e.offs])
	} else {
		state.bufFront = state.bufFront[:0]
	}
	if e.endOffs > 0 {
		if cap(state.bufBack) < int(e.endOffs) {
			state.bufBack = make([]byte, e.endOffs)
		} else {
			state.bufBack = state.bufBack[:e.endOffs]
		}
		copy(state.bufBack, e.buf[e.storage-e.endOffs:e.storage])
	} else {
		state.bufBack = state.bufBack[:0]
	}
}

// RestoreState restores the encoder to a previously saved state.
func (e *Encoder) RestoreState(state *EncoderState) {
	e.offs = state.offs
	e.endOffs = state.endOffs
	e.endWindow = state.endWindow
	e.nendBits = state.nendBits
	e.nbitsTotal = state.nbitsTotal
	e.rng = state.rng
	e.val = state.val
	e.rem = state.rem
	e.ext = state.ext
	e.err = state.err
	// Restore the bytes
	if len(state.bufFront) > 0 {
		copy(e.buf[:state.offs], state.bufFront)
	}
	if len(state.bufBack) > 0 {
		copy(e.buf[e.storage-state.endOffs:e.storage], state.bufBack)
	}
}

// RangeBytes returns the number of range-coded bytes written.
// This mirrors libopus ec_range_bytes.
func (e *Encoder) RangeBytes() int {
	return int(e.offs)
}

// State returns the internal range encoder state (rng, val).
// Useful for bit-exact comparisons against libopus in tests.
func (e *Encoder) State() (uint32, uint32) {
	return e.rng, e.val
}

// PatchInitialBits overwrites the first few bits in the range coder stream.
// This mirrors libopus ec_enc_patch_initial_bits and is used for VAD/LBRR flag
// encoding in SILK packets where the flags must be written at the packet start
// but their values are only known after encoding the frame data.
func (e *Encoder) PatchInitialBits(val uint32, nbits uint) {
	if nbits <= 0 || nbits > EC_SYM_BITS {
		e.err = -1
		return
	}
	shift := EC_SYM_BITS - nbits
	mask := (uint32(1)<<nbits - 1) << shift
	if e.offs > 0 {
		e.buf[0] = (e.buf[0] &^ byte(mask)) | byte(val<<shift)
	} else if e.rem >= 0 {
		e.rem = int((uint32(e.rem) &^ mask) | (val << shift))
	} else if e.rng <= (EC_CODE_TOP >> nbits) {
		shiftedMask := mask << EC_CODE_SHIFT
		e.val = (e.val &^ shiftedMask) | (val << (EC_CODE_SHIFT + shift))
	} else {
		// If we reach here, it means we're trying to patch bits before
		// the range coder has progressed enough to stabilize the first bits.
		e.err = -1
	}
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
