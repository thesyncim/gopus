// Package rangecoding implements the range coder used by Opus per RFC 6716 Section 4.1.
package rangecoding

// Constants from RFC 6716 Section 4.1 and libopus celt/mfrngcod.h
const (
	EC_SYM_BITS   = 8                                // Bits output at a time
	EC_CODE_BITS  = 32                               // Total state register bits
	EC_SYM_MAX    = (1 << EC_SYM_BITS) - 1           // 255
	EC_CODE_TOP   = 1 << (EC_CODE_BITS - 1)          // 0x80000000
	EC_CODE_BOT   = EC_CODE_TOP >> EC_SYM_BITS       // 0x00800000
	EC_CODE_SHIFT = EC_CODE_BITS - EC_SYM_BITS - 1   // 23
	EC_CODE_EXTRA = (EC_CODE_BITS-2)%EC_SYM_BITS + 1 // 7
	EC_UINT_BITS  = 8                                // Bits for range-coded unsigned integers
)
