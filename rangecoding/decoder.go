package rangecoding

import "math/bits"

// Decoder implements the range decoder per RFC 6716 Section 4.1.
// This is a bit-exact port of libopus entdec.c.
type Decoder struct {
	buf        []byte // Input buffer
	storage    uint32 // Buffer size
	offs       uint32 // Current read offset
	endOffs    uint32 // End offset for raw bits
	endWindow  uint32 // Window for raw bits at end
	nendBits   int    // Number of valid bits in end window
	nbitsTotal int    // Total bits read (for tell functions)
	rng        uint32 // Range size (must stay > EC_CODE_BOT after normalize)
	val        uint32 // Current value in range
	ext        uint32 // Saved normalization factor from decode()
	rem        int    // Buffered partial byte
	err        int    // Error flag
}

// Init initializes the decoder with the given byte buffer.
// This follows libopus ec_dec_init exactly.
func (d *Decoder) Init(buf []byte) {
	d.buf = buf
	d.storage = uint32(len(buf))
	d.offs = 0
	d.endOffs = 0
	d.endWindow = 0
	d.nendBits = 0
	d.err = 0

	// Initialize range to 1 << EC_CODE_EXTRA (128)
	d.rng = 1 << EC_CODE_EXTRA

	// Read first byte and compute initial value
	d.rem = int(d.readByte())
	d.val = d.rng - 1 - uint32(d.rem>>(EC_SYM_BITS-EC_CODE_EXTRA))

	// Set initial bit count BEFORE normalize (matches libopus ec_dec_init).
	// This compensates for bits that will be added in normalize().
	d.nbitsTotal = EC_CODE_BITS + 1 -
		((EC_CODE_BITS-EC_CODE_EXTRA)/EC_SYM_BITS)*EC_SYM_BITS
	d.ext = 0

	// Normalize to fill the range (this will add more bits to nbitsTotal)
	d.normalize()
}

// readByte reads the next byte from the buffer.
// Returns 0 if reading past end (per spec).
func (d *Decoder) readByte() byte {
	if d.offs < d.storage {
		b := d.buf[d.offs]
		d.offs++
		return b
	}
	return 0
}

// normalize ensures rng > EC_CODE_BOT by reading more bytes.
// This is the core renormalization loop from RFC 6716 Section 4.1.1.
func (d *Decoder) normalize() {
	for d.rng <= EC_CODE_BOT {
		d.nbitsTotal += EC_SYM_BITS
		d.rng <<= EC_SYM_BITS

		// Combine previous remainder with new byte
		sym := d.rem
		d.rem = int(d.readByte())
		sym = (sym<<EC_SYM_BITS | d.rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)

		// Update val: shift in new bits, mask to valid range
		d.val = ((d.val << EC_SYM_BITS) + uint32(EC_SYM_MAX&^sym)) & (EC_CODE_TOP - 1)
	}
}

// DecodeICDF decodes a symbol using an inverse cumulative distribution function table.
// The icdf table contains values in decreasing order from 256 down to 0.
// ftb is the number of bits of precision in the table (typically 8).
// Returns the decoded symbol index.
func (d *Decoder) DecodeICDF(icdf []uint8, ftb uint) int {
	s := d.rng
	dval := d.val
	r := s >> ftb
	ret := -1
	for {
		t := s
		ret++
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
}

// DecodeICDF16 decodes a symbol using a uint16 ICDF table.
// This variant is needed because SILK ICDF tables use values 0-256,
// and 256 doesn't fit in uint8.
// The icdf table contains values in decreasing order from 256 down to 0.
// ftb is the number of bits of precision in the table (typically 8).
// Returns the decoded symbol index.
func (d *Decoder) DecodeICDF16(icdf []uint16, ftb uint) int {
	s := d.rng
	dval := d.val
	r := s >> ftb
	ret := -1
	for {
		t := s
		ret++
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
}

// DecodeBit decodes a single bit with the given log probability.
// logp is the number of bits of probability for a 0 (1 to 15).
// P(0) = 1 - 1/(2^logp), P(1) = 1/(2^logp)
// Returns 0 or 1.
//
// Per libopus entdec.c, the probability regions are:
// - [0, s): bit = 1, probability = 1 / 2^logp (rare, bottom region)
// - [s, rng): bit = 0, probability = (2^logp - 1) / 2^logp
//
// For silence flag (logp=15): P(silence=1) = 1/32768, which is very rare.
func (d *Decoder) DecodeBit(logp uint) int {
	r := d.rng
	dval := d.val
	s := r >> logp

	// Per libopus: bit is 1 when dval < s (bottom region).
	ret := 0
	if dval < s {
		ret = 1
	} else {
		d.val = dval - s
	}

	if ret == 1 {
		d.rng = s
	} else {
		d.rng = r - s
	}

	d.normalize()
	return ret
}

// Tell returns the number of bits consumed so far.
func (d *Decoder) Tell() int {
	return d.nbitsTotal - ilog(d.rng)
}

// TellFrac returns the number of bits consumed with 1/8 bit precision.
// The value is in 1/8 bits, so divide by 8 to compare with Tell().
func (d *Decoder) TellFrac() int {
	correction := [8]uint32{35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535}

	nbits := d.nbitsTotal << 3
	l := ilog(d.rng)
	r := d.rng >> (l - 16)
	b := int((r >> 12) - 8)
	if r > correction[b] {
		b++
	}
	return nbits - ((l << 3) + b)
}

// State returns the internal range decoder state (rng, val).
// Useful for bit-exact comparisons against libopus in tests.
func (d *Decoder) State() (uint32, uint32) {
	return d.rng, d.val
}

// ilog computes the integer log base 2 (position of highest set bit + 1).
// Returns 0 for input 0.
func ilog(x uint32) int {
	return bits.Len32(x)
}

// Error returns the error flag. Non-zero indicates a decoding error.
func (d *Decoder) Error() int {
	return d.err
}

// BytesUsed returns the number of bytes consumed from the buffer.
func (d *Decoder) BytesUsed() int {
	return int(d.offs)
}

// StorageBits returns the total number of bits in the input buffer.
func (d *Decoder) StorageBits() int {
	return int(d.storage * 8)
}

// ShrinkStorage reduces the effective input size by the given number of bytes.
// This is used to exclude trailing redundancy bytes from the range decoder
// while preserving the current decoding state.
func (d *Decoder) ShrinkStorage(bytes int) {
	if bytes <= 0 {
		return
	}
	if uint32(bytes) >= d.storage {
		d.storage = 0
		if d.offs > d.storage {
			d.offs = d.storage
		}
		if d.endOffs > d.storage {
			d.endOffs = d.storage
		}
		return
	}
	d.storage -= uint32(bytes)
	if d.offs > d.storage {
		d.offs = d.storage
	}
	if d.endOffs > d.storage {
		d.endOffs = d.storage
	}
}

// Range returns the current range value (for testing/debugging).
func (d *Decoder) Range() uint32 {
	return d.rng
}

// Val returns the current val (for testing/debugging).
func (d *Decoder) Val() uint32 {
	return d.val
}

// Offs returns the current read offset (for testing/debugging).
func (d *Decoder) Offs() uint32 {
	return d.offs
}

// DecodeUniform decodes a uniformly distributed value in the range [0, ft).
// This is used for fine energy bits and PVQ indices.
// Reference: libopus celt/entdec.c ec_dec_uint()
func (d *Decoder) DecodeUniform(ft uint32) uint32 {
	if ft <= 1 {
		return 0
	}

	ft--
	ftb := ilog(ft)

	if ftb > EC_UINT_BITS {
		ftb -= EC_UINT_BITS
		ft1 := (ft >> uint(ftb)) + 1
		s := d.decode(ft1)
		d.update(s, s+1, ft1)

		t := (s << uint(ftb)) | d.DecodeRawBits(uint(ftb))
		if t <= ft {
			return t
		}
		d.err = 1
		return ft
	}

	ft++
	s := d.decode(ft)
	d.update(s, s+1, ft)
	return s
}

func (d *Decoder) decode(ft uint32) uint32 {
	d.ext = d.rng / ft
	s := d.val / d.ext
	if s+1 > ft {
		s = ft - 1
	}
	return ft - (s + 1)
}

// Decode returns the current cumulative frequency value without updating state.
// This mirrors libopus ec_decode().
func (d *Decoder) Decode(ft uint32) uint32 {
	return d.decode(ft)
}

// DecodeBin decodes a symbol when the total frequency is 1<<bits.
// This mirrors libopus ec_decode_bin.
func (d *Decoder) DecodeBin(bits uint) uint32 {
	if bits == 0 {
		return 0
	}
	ft := uint32(1) << bits
	d.ext = d.rng >> bits
	s := d.val / d.ext
	if s+1 > ft {
		s = ft - 1
	}
	return ft - (s + 1)
}

func (d *Decoder) update(fl, fh, ft uint32) {
	s := d.ext * (ft - fh)
	d.val -= s
	if fl > 0 {
		d.rng = d.ext * (fh - fl)
	} else {
		d.rng -= s
	}
	d.normalize()
}

// Update applies the range update using the provided cumulative frequencies.
// This mirrors libopus ec_dec_update().
func (d *Decoder) Update(fl, fh, ft uint32) {
	d.update(fl, fh, ft)
}

// DecodeSymbol decodes a symbol given cumulative frequencies and updates state.
// fl: cumulative frequency of symbols before this one
// fh: cumulative frequency up to and including this symbol
// ft: total frequency (sum of all symbol frequencies)
//
// This implements the range decoder update: rng = s * fh, val = val - s * fl
// where s = rng / ft (the scale factor).
//
// Reference: libopus celt/entdec.c ec_dec_update()
func (d *Decoder) DecodeSymbol(fl, fh, ft uint32) {
	if ft == 0 {
		return
	}
	d.ext = d.rng / ft
	d.update(fl, fh, ft)
}

// DecodeRawBits reads raw bits from the end of the buffer.
// This is used for fine energy bits and PVQ sign bits.
// Reference: libopus celt/entdec.c ec_dec_bits()
func (d *Decoder) DecodeRawBits(bits uint) uint32 {
	if bits == 0 {
		return 0
	}

	// Read from end of buffer
	for d.nendBits < int(bits) {
		// Read more bytes from end
		if d.endOffs < d.storage {
			d.endOffs++
			d.endWindow |= uint32(d.buf[d.storage-d.endOffs]) << d.nendBits
			d.nendBits += 8
		} else {
			d.nendBits = int(bits) // Force exit
		}
	}

	val := d.endWindow & ((1 << bits) - 1)
	d.endWindow >>= bits
	d.nendBits -= int(bits)
	d.nbitsTotal += int(bits)

	return val
}
