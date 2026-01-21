package rangecoding

// Decoder implements the range decoder per RFC 6716 Section 4.1.
// This is a bit-exact port of libopus entdec.c.
type Decoder struct {
	buf       []byte // Input buffer
	storage   uint32 // Buffer size
	offs      uint32 // Current read offset
	endOffs   uint32 // End offset for raw bits
	endWindow uint32 // Window for raw bits at end
	nendBits  int    // Number of valid bits in end window
	nbitsTotal int   // Total bits read (for tell functions)
	rng       uint32 // Range size (must stay > EC_CODE_BOT after normalize)
	val       uint32 // Current value in range
	rem       int    // Buffered partial byte
	err       int    // Error flag
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

	// Set initial bit count BEFORE normalize
	// Per libopus: nbits_total = EC_CODE_BITS + 1
	// This accounts for the entropy consumed so far
	d.nbitsTotal = EC_CODE_BITS + 1

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
	// Scale the range
	r := d.rng >> ftb

	// Find the symbol - linear search through icdf
	// icdf values are in decreasing order: icdf[0] is largest, icdf[len-1] = 0
	k := 0
	for {
		threshold := r * uint32(icdf[k])
		if d.val >= threshold {
			break
		}
		k++
	}

	// Update decoder state
	// val = val - r * icdf[k]
	d.val -= r * uint32(icdf[k])

	// rng = r * (icdf[k-1] - icdf[k]) for k > 0, or rng - r*icdf[0] for k = 0
	if k > 0 {
		d.rng = r * uint32(icdf[k-1]-icdf[k])
	} else {
		d.rng -= r * uint32(icdf[0])
	}

	// Renormalize
	d.normalize()

	return k
}

// DecodeBit decodes a single bit with the given log probability.
// logp is the number of bits of probability for a 0 (1 to 15).
// P(0) = 1 - 1/(2^logp), P(1) = 1/(2^logp)
// Returns 0 or 1.
func (d *Decoder) DecodeBit(logp uint) int {
	r := d.rng >> logp
	if d.val >= r {
		// Bit is 1
		d.val -= r
		d.rng -= r
		d.normalize()
		return 1
	}
	// Bit is 0
	d.rng = r
	d.normalize()
	return 0
}

// Tell returns the number of bits consumed so far.
func (d *Decoder) Tell() int {
	return d.nbitsTotal - ilog(d.rng)
}

// TellFrac returns the number of bits consumed with 1/8 bit precision.
// The value is in 1/8 bits, so divide by 8 to compare with Tell().
func (d *Decoder) TellFrac() int {
	// Number of whole bits scaled by 8
	nbits := d.nbitsTotal << 3
	// Get the log of range
	l := ilog(d.rng)

	// Compute fractional correction
	// This approximates -log2(rng) * 8 using fixed-point arithmetic
	var r uint32
	if l > 16 {
		r = d.rng >> (l - 16)
	} else {
		r = d.rng << (16 - l)
	}
	// Correction using small correction table approximation
	// Based on libopus correction: uses top bits of range
	correction := int(r>>12) - 8
	if correction < 0 {
		correction = 0
	}
	if correction > 7 {
		correction = 7
	}
	return nbits - l*8 + correction
}

// ilog computes the integer log base 2 (position of highest set bit + 1).
// Returns 0 for input 0.
func ilog(x uint32) int {
	if x == 0 {
		return 0
	}
	n := 0
	if x >= (1 << 16) {
		n += 16
		x >>= 16
	}
	if x >= (1 << 8) {
		n += 8
		x >>= 8
	}
	if x >= (1 << 4) {
		n += 4
		x >>= 4
	}
	if x >= (1 << 2) {
		n += 2
		x >>= 2
	}
	if x >= (1 << 1) {
		n += 1
		x >>= 1
	}
	return n + int(x)
}

// Error returns the error flag. Non-zero indicates a decoding error.
func (d *Decoder) Error() int {
	return d.err
}

// BytesUsed returns the number of bytes consumed from the buffer.
func (d *Decoder) BytesUsed() int {
	return int(d.offs)
}
