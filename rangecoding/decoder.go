package rangecoding

import "math/bits"

var tellFracCorrection = [8]uint32{35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535}

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
	rem        uint32 // Buffered partial byte
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
	d.rem = uint32(d.readByte())
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
//
//go:nosplit
func (d *Decoder) readByte() byte {
	offs := int(d.offs)
	if offs < len(d.buf) {
		b := d.buf[offs]
		d.offs++
		return b
	}
	return 0
}

// normalize ensures rng > EC_CODE_BOT by reading more bytes.
// This is the core renormalization loop from RFC 6716 Section 4.1.1.
//
//go:nosplit
func (d *Decoder) normalize() {
	for d.rng <= EC_CODE_BOT {
		d.nbitsTotal += EC_SYM_BITS
		d.rng <<= EC_SYM_BITS

		// Combine previous remainder with new byte
		sym := d.rem
		d.rem = uint32(d.readByte())
		sym = (sym<<EC_SYM_BITS | d.rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)

		// Update val: shift in new bits, mask to valid range
		d.val = ((d.val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
	}
}

// DecodeICDF decodes a symbol using an inverse cumulative distribution function table.
// The icdf table contains values in decreasing order from 256 down to 0.
// ftb is the number of bits of precision in the table (typically 8).
// Returns the decoded symbol index.
//
//go:nosplit
func (d *Decoder) DecodeICDF(icdf []uint8, ftb uint) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> ftb
	for ret, prob := range icdf {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
	return len(icdf) - 1
}

// DecodeICDF8 decodes a symbol using an 8-bit ICDF table.
// This is the hot SILK/CELT entropy path and matches DecodeICDF(icdf, 8).
//
//go:nosplit
func (d *Decoder) DecodeICDF8(icdf []uint8) int {
	_ = icdf[0]
	return d.DecodeICDF8Unchecked(icdf)
}

// DecodeICDF8Unchecked decodes using a non-empty 8-bit ICDF table.
// Codec hot paths pass static tables. Callers must not pass an empty slice.
//
//go:nosplit
func (d *Decoder) DecodeICDF8Unchecked(icdf []uint8) int {
	_ = icdf[0]
	switch len(icdf) {
	case 2:
		return d.DecodeICDF2_8(icdf[0])
	case 3:
		return d.DecodeICDF3_8(icdf[0], icdf[1])
	case 4:
		return d.DecodeICDF4_8(icdf[0], icdf[1], icdf[2])
	case 5:
		return d.DecodeICDF5_8(icdf[0], icdf[1], icdf[2], icdf[3])
	case 6:
		r := d.rng >> 8
		t := d.rng
		dval := d.val
		s := r * uint32(icdf[0])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 0
		}
		t = s
		s = r * uint32(icdf[1])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 1
		}
		t = s
		s = r * uint32(icdf[2])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 2
		}
		t = s
		s = r * uint32(icdf[3])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 3
		}
		t = s
		s = r * uint32(icdf[4])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 4
		}
		d.val = dval
		d.rng = s
		d.normalize()
		return 5
	case 8:
		r := d.rng >> 8
		t := d.rng
		dval := d.val
		s := r * uint32(icdf[0])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 0
		}
		t = s
		s = r * uint32(icdf[1])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 1
		}
		t = s
		s = r * uint32(icdf[2])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 2
		}
		t = s
		s = r * uint32(icdf[3])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 3
		}
		t = s
		s = r * uint32(icdf[4])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 4
		}
		t = s
		s = r * uint32(icdf[5])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 5
		}
		t = s
		s = r * uint32(icdf[6])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return 6
		}
		d.val = dval
		d.rng = s
		d.normalize()
		return 7
	}
	s := d.rng
	dval := d.val
	r := s >> 8
	last := len(icdf) - 1
	ret := 0
	for ; ret+1 < last; ret += 2 {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
		t = s
		s = r * uint32(icdf[ret+1])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret + 1
		}
	}
	for ; ret < last; ret++ {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
	d.val = dval
	d.rng = s
	d.normalize()
	return last
}

// DecodeICDF8Linear decodes a non-empty 8-bit ICDF table with the generic
// linear walk directly. It is for known large tables, where DecodeICDF8Unchecked
// would first pay the small-table switch before reaching this same loop.
//
//go:nosplit
func (d *Decoder) DecodeICDF8Linear(icdf []uint8) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> 8
	last := len(icdf) - 1
	for ret := 0; ret < last; ret++ {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
	d.val = dval
	d.rng = s
	d.normalize()
	return last
}

// DecodeICDF8UncheckedN decodes using the first n entries of a non-empty
// 8-bit ICDF table. It is useful for packed zero-terminated table rows where
// the backing slice continues with subsequent rows.
//
//go:nosplit
func (d *Decoder) DecodeICDF8UncheckedN(icdf []uint8, n int) int {
	_ = icdf[n-1]
	s := d.rng
	dval := d.val
	r := s >> 8
	last := n - 1
	for ret := 0; ret < last; ret++ {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
	d.val = dval
	d.rng = s
	d.normalize()
	return last
}

// DecodeICDF8UncheckedNOffset decodes using n entries starting at off in a
// non-empty 8-bit ICDF table. This avoids materializing row slices in hot paths
// backed by packed static table storage.
//
//go:nosplit
func (d *Decoder) DecodeICDF8UncheckedNOffset(icdf []uint8, off, n int) int {
	_ = icdf[off+n-1]
	row := icdf[off : off+n : off+n]
	s := d.rng
	dval := d.val
	r := s >> 8
	last := n - 1
	for ret, prob := range row[:last] {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
	d.val = dval
	d.rng = s
	d.normalize()
	return last
}

// DecodeICDF2 decodes a 2-symbol ICDF with entries [icdf0, 0].
// This avoids generic loop/slice overhead in hot binary-symbol call sites.
//
//go:nosplit
func (d *Decoder) DecodeICDF2(icdf0 uint8, ftb uint) int {
	t := d.rng
	r := t >> ftb
	s := r * uint32(icdf0)
	if d.val >= s {
		d.val -= s
		d.rng = t - s
		d.normalize()
		return 0
	}
	d.rng = s
	d.normalize()
	return 1
}

// DecodeICDF2_8 decodes a 2-symbol 8-bit ICDF with entries [icdf0, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF2_8(icdf0 uint8) int {
	rng := d.rng
	val := d.val

	t := rng
	r := t >> 8
	s := r * uint32(icdf0)
	ret := 1
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		rng = s
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := rem
		if int(offs) < len(buf) {
			rem = uint32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF3_8 decodes a 3-symbol 8-bit ICDF with entries [icdf0, icdf1, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF3_8(icdf0, icdf1 uint8) int {
	rng := d.rng
	val := d.val

	r := rng >> 8
	t := rng
	s := r * uint32(icdf0)
	ret := 2
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		t = s
		s = r * uint32(icdf1)
		if val >= s {
			val -= s
			rng = t - s
			ret = 1
		} else {
			rng = s
		}
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := rem
		if int(offs) < len(buf) {
			rem = uint32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF4_8 decodes a 4-symbol 8-bit ICDF with entries [icdf0, icdf1, icdf2, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF4_8(icdf0, icdf1, icdf2 uint8) int {
	rng := d.rng
	val := d.val

	r := rng >> 8
	t := rng
	s := r * uint32(icdf0)
	ret := 3
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		t = s
		s = r * uint32(icdf1)
		if val >= s {
			val -= s
			rng = t - s
			ret = 1
		} else {
			t = s
			s = r * uint32(icdf2)
			if val >= s {
				val -= s
				rng = t - s
				ret = 2
			} else {
				rng = s
			}
		}
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := rem
		if int(offs) < len(buf) {
			rem = uint32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF5_8 decodes a 5-symbol 8-bit ICDF with entries [icdf0, icdf1, icdf2, icdf3, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF5_8(icdf0, icdf1, icdf2, icdf3 uint8) int {
	rng := d.rng
	val := d.val

	r := rng >> 8
	t := rng
	s := r * uint32(icdf0)
	ret := 4
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		t = s
		s = r * uint32(icdf1)
		if val >= s {
			val -= s
			rng = t - s
			ret = 1
		} else {
			t = s
			s = r * uint32(icdf2)
			if val >= s {
				val -= s
				rng = t - s
				ret = 2
			} else {
				t = s
				s = r * uint32(icdf3)
				if val >= s {
					val -= s
					rng = t - s
					ret = 3
				} else {
					rng = s
				}
			}
		}
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := rem
		if int(offs) < len(buf) {
			rem = uint32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF7_8Slice decodes a 7-symbol 8-bit ICDF.
//
//go:nosplit
func (d *Decoder) DecodeICDF7_8Slice(icdf []uint8) int {
	_ = icdf[5]
	r := d.rng >> 8
	t := d.rng
	dval := d.val
	s := r * uint32(icdf[0])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 0
	}
	t = s
	s = r * uint32(icdf[1])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 1
	}
	t = s
	s = r * uint32(icdf[2])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 2
	}
	t = s
	s = r * uint32(icdf[3])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 3
	}
	t = s
	s = r * uint32(icdf[4])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 4
	}
	t = s
	s = r * uint32(icdf[5])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 5
	}
	d.val = dval
	d.rng = s
	d.normalize()
	return 6
}

// DecodeICDF9_8Slice decodes a 9-symbol 8-bit ICDF.
//
//go:nosplit
func (d *Decoder) DecodeICDF9_8Slice(icdf []uint8) int {
	_ = icdf[7]
	r := d.rng >> 8
	t := d.rng
	dval := d.val
	s := r * uint32(icdf[0])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 0
	}
	t = s
	s = r * uint32(icdf[1])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 1
	}
	t = s
	s = r * uint32(icdf[2])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 2
	}
	t = s
	s = r * uint32(icdf[3])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 3
	}
	t = s
	s = r * uint32(icdf[4])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 4
	}
	t = s
	s = r * uint32(icdf[5])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 5
	}
	t = s
	s = r * uint32(icdf[6])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 6
	}
	t = s
	s = r * uint32(icdf[7])
	if dval >= s {
		d.val = dval - s
		d.rng = t - s
		d.normalize()
		return 7
	}
	d.val = dval
	d.rng = s
	d.normalize()
	return 8
}

// DecodeICDF2_8SignBlock applies binary 8-bit ICDF sign decoding to a
// 16-sample pulse block. Positive entries are conditionally negated when the
// decoded symbol is 0, matching repeated DecodeICDF2_8 calls. When pulseSum is
// positive, it must be the exact sum of positive magnitudes in block and is
// used to stop scanning once all sign-coded pulses have been consumed.
//
//go:nosplit
func (d *Decoder) DecodeICDF2_8SignBlock(icdf0 uint8, block []int16, pulseSum int) {
	_ = block[15]
	d.DecodeICDF2_8SignBlock16(icdf0, (*[16]int16)(block[:16]), pulseSum)
}

// DecodeICDF2_8SignBlock16 applies binary 8-bit ICDF sign decoding to one
// fixed SILK shell block. The array form lets hot SILK callers avoid carrying
// slice bounds through the unrolled sign loop.
//
//go:nosplit
func (d *Decoder) DecodeICDF2_8SignBlock16(icdf0 uint8, block *[16]int16, pulseSum int) {
	icdf := uint32(icdf0)
	remaining := pulseSum
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rng := d.rng
	val := d.val
	rem := d.rem
	for i := 0; i < 16; i += 4 {
		v := block[i]
		if (v | block[i+1] | block[i+2] | block[i+3]) == 0 {
			continue
		}
		if v > 0 {
			t := rng
			s := (t >> 8) * icdf
			if val >= s {
				val -= s
				rng = t - s
				block[i] = -v
			} else {
				rng = s
			}
			for rng <= EC_CODE_BOT {
				nbitsTotal += EC_SYM_BITS
				rng <<= EC_SYM_BITS

				sym := rem
				if int(offs) < len(buf) {
					rem = uint32(buf[offs])
					offs++
				} else {
					rem = 0
				}
				sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
				val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
			}
			if remaining > 0 {
				remaining -= int(v)
				if remaining <= 0 {
					goto done
				}
			}
		}
		v = block[i+1]
		if v > 0 {
			t := rng
			s := (t >> 8) * icdf
			if val >= s {
				val -= s
				rng = t - s
				block[i+1] = -v
			} else {
				rng = s
			}
			for rng <= EC_CODE_BOT {
				nbitsTotal += EC_SYM_BITS
				rng <<= EC_SYM_BITS

				sym := rem
				if int(offs) < len(buf) {
					rem = uint32(buf[offs])
					offs++
				} else {
					rem = 0
				}
				sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
				val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
			}
			if remaining > 0 {
				remaining -= int(v)
				if remaining <= 0 {
					goto done
				}
			}
		}
		v = block[i+2]
		if v > 0 {
			t := rng
			s := (t >> 8) * icdf
			if val >= s {
				val -= s
				rng = t - s
				block[i+2] = -v
			} else {
				rng = s
			}
			for rng <= EC_CODE_BOT {
				nbitsTotal += EC_SYM_BITS
				rng <<= EC_SYM_BITS

				sym := rem
				if int(offs) < len(buf) {
					rem = uint32(buf[offs])
					offs++
				} else {
					rem = 0
				}
				sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
				val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
			}
			if remaining > 0 {
				remaining -= int(v)
				if remaining <= 0 {
					goto done
				}
			}
		}
		v = block[i+3]
		if v > 0 {
			t := rng
			s := (t >> 8) * icdf
			if val >= s {
				val -= s
				rng = t - s
				block[i+3] = -v
			} else {
				rng = s
			}
			for rng <= EC_CODE_BOT {
				nbitsTotal += EC_SYM_BITS
				rng <<= EC_SYM_BITS

				sym := rem
				if int(offs) < len(buf) {
					rem = uint32(buf[offs])
					offs++
				} else {
					rem = 0
				}
				sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
				val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
			}
			if remaining > 0 {
				remaining -= int(v)
				if remaining <= 0 {
					goto done
				}
			}
		}
	}
done:
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
}

// DecodeICDF16 decodes a symbol using a uint16 ICDF table.
// This variant is needed because SILK ICDF tables use values 0-256,
// and 256 doesn't fit in uint8.
// The icdf table contains values in decreasing order from 256 down to 0.
// ftb is the number of bits of precision in the table (typically 8).
// Returns the decoded symbol index.
//
//go:nosplit
func (d *Decoder) DecodeICDF16(icdf []uint16, ftb uint) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> ftb
	for ret, prob := range icdf {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
	return len(icdf) - 1
}

// DecodeICDF16_8 decodes a symbol using a uint16 ICDF table with 8-bit precision.
//
//go:nosplit
func (d *Decoder) DecodeICDF16_8(icdf []uint16) int {
	_ = icdf[0]
	return d.DecodeICDF16_8Unchecked(icdf)
}

// DecodeICDF16_8Unchecked decodes using a non-empty uint16 ICDF table with
// 8-bit precision. Callers must not pass an empty slice.
//
//go:nosplit
func (d *Decoder) DecodeICDF16_8Unchecked(icdf []uint16) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> 8
	for ret, prob := range icdf {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.val = dval - s
			d.rng = t - s
			d.normalize()
			return ret
		}
	}
	return len(icdf) - 1
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
//
//go:nosplit
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
	nbits := d.nbitsTotal << 3
	l := ilog(d.rng)
	r := d.rng >> (l - 16)
	b := int((r >> 12) - 8)
	if r > tellFracCorrection[b] {
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
		d.buf = d.buf[:0]
		if d.offs > d.storage {
			d.offs = d.storage
		}
		if d.endOffs > d.storage {
			d.endOffs = d.storage
		}
		return
	}
	d.storage -= uint32(bytes)
	d.buf = d.buf[:int(d.storage)]
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
//
//go:nosplit
func (d *Decoder) DecodeUniform(ft uint32) uint32 {
	if ft <= 1 {
		return 0
	}
	ft--
	ftb := ilog(ft)

	if ftb > EC_UINT_BITS {
		ftb -= EC_UINT_BITS
		ft1 := (ft >> uint(ftb)) + 1
		rng := d.rng
		val := d.val
		ext := rng / ft1
		s := val / ext
		if s+1 > ft1 {
			s = ft1 - 1
		}
		ret := ft1 - (s + 1)
		scale := ext * s
		val -= scale
		if ret > 0 {
			rng = ext
		} else {
			rng -= scale
		}
		if rng > EC_CODE_BOT {
			d.rng = rng
			d.val = val
		} else {
			buf := d.buf
			offs := d.offs
			nbitsTotal := d.nbitsTotal
			rem := d.rem
			for {
				nbitsTotal += EC_SYM_BITS
				rng <<= EC_SYM_BITS

				sym := rem
				if int(offs) < len(buf) {
					rem = uint32(buf[offs])
					offs++
				} else {
					rem = 0
				}
				sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
				val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
				if rng > EC_CODE_BOT {
					break
				}
			}
			d.offs = offs
			d.nbitsTotal = nbitsTotal
			d.rng = rng
			d.val = val
			d.rem = rem
		}

		rawBits := uint(ftb)
		endWindow := d.endWindow
		endOffs := d.endOffs
		nendBits := d.nendBits
		storage := d.storage
		buf := d.buf
		for nendBits < ftb {
			if endOffs < storage {
				endOffs++
				endWindow |= uint32(buf[storage-endOffs]) << nendBits
				nendBits += 8
			} else {
				nendBits = ftb
			}
		}
		raw := endWindow & ((1 << rawBits) - 1)
		d.endWindow = endWindow >> rawBits
		d.endOffs = endOffs
		d.nendBits = nendBits - ftb
		d.nbitsTotal += ftb

		t := (ret << rawBits) | raw
		if t <= ft {
			return t
		}
		d.err = 1
		return ft
	}

	ft++
	rng := d.rng
	val := d.val
	ext := rng / ft
	s := val / ext
	if s+1 > ft {
		s = ft - 1
	}
	ret := ft - (s + 1)
	scale := ext * s
	val -= scale
	if ret > 0 {
		rng = ext
	} else {
		rng -= scale
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := rem
		if int(offs) < len(buf) {
			rem = uint32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeUniformSmall decodes a uniform value for totals that fit entirely in
// the range coder path. Callers must only use this for ft <= 1<<EC_UINT_BITS.
//
//go:nosplit
func (d *Decoder) DecodeUniformSmall(ft uint32) uint32 {
	if ft <= 1 {
		return 0
	}
	rng := d.rng
	val := d.val
	ext := rng / ft
	s := val / ext
	if s+1 > ft {
		s = ft - 1
	}
	ret := ft - (s + 1)
	scale := ext * s
	val -= scale
	if ret > 0 {
		rng = ext
	} else {
		rng -= scale
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := rem
		if int(offs) < len(buf) {
			rem = uint32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
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
//
//go:nosplit
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

//go:nosplit
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
//
//go:nosplit
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

	endWindow := d.endWindow
	endOffs := d.endOffs
	nendBits := d.nendBits
	storage := d.storage
	buf := d.buf

	for nendBits < int(bits) {
		if endOffs < storage {
			endOffs++
			endWindow |= uint32(buf[storage-endOffs]) << nendBits
			nendBits += 8
		} else {
			nendBits = int(bits)
		}
	}

	val := endWindow & ((1 << bits) - 1)
	d.endWindow = endWindow >> bits
	d.endOffs = endOffs
	d.nendBits = nendBits - int(bits)
	d.nbitsTotal += int(bits)

	return val
}

// DecodeRawBit reads a single raw bit from the end of the buffer.
func (d *Decoder) DecodeRawBit() uint32 {
	if d.nendBits == 0 {
		if d.endOffs < d.storage {
			d.endOffs++
			d.endWindow |= uint32(d.buf[d.storage-d.endOffs])
			d.nendBits = 8
		} else {
			d.nendBits = 1
		}
	}
	val := d.endWindow & 1
	d.endWindow >>= 1
	d.nendBits--
	d.nbitsTotal++
	return val
}
