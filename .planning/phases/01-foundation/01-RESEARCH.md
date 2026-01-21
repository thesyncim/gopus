# Phase 1: Foundation - Research

**Researched:** 2026-01-21
**Domain:** Entropy coding (range coder), Opus packet parsing
**Confidence:** HIGH

## Summary

Phase 1 establishes the entropy coding foundation that all Opus modes depend on. The range coder is a critical component used by both SILK and CELT layers, and must be implemented with bit-exact integer arithmetic per RFC 6716 Section 4.1. The packet parsing layer handles TOC byte extraction and frame count codes 0-3.

This research analyzed RFC 6716 specification, the reference libopus C implementation (entenc.c, entdec.c), and the pion/opus pure-Go implementation. The range coder algorithm is well-documented with clear pseudocode in the RFC. The pion/opus project provides a working Go reference, though it only implements the decoder side (no encoder) and only supports SILK mode.

**Primary recommendation:** Implement range decoder first following RFC 6716 Section 4.1 exactly, then implement encoder as the symmetric inverse. Use pion/opus as Go idiom reference but translate directly from libopus C for bit-exact correctness.

## Standard Stack

The established libraries/tools for this domain:

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go standard library | 1.21+ | All functionality | Zero dependencies requirement (CMP-03) |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `testing` | stdlib | Unit tests | All test code |
| `encoding/binary` | stdlib | Byte order operations | Reading/writing multi-byte values |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Custom bit reader | `bitio` package | External dependency violates CMP-03 |
| pion/opus import | Direct use | Would couple to their design, limit flexibility |

**Installation:**
```bash
# No external dependencies - pure stdlib
go mod init gopus  # Already done
```

## Architecture Patterns

### Recommended Project Structure
```
gopus/
├── go.mod
├── decoder.go              # Public decoder API (future)
├── encoder.go              # Public encoder API (future)
├── packet.go               # TOC byte, packet parsing
├── internal/
│   ├── rangecoding/
│   │   ├── decoder.go      # Range decoder implementation
│   │   ├── encoder.go      # Range encoder implementation
│   │   ├── constants.go    # EC_CODE_BITS, etc.
│   │   └── rangecoding_test.go
│   ├── silk/               # Future: SILK codec
│   └── celt/               # Future: CELT codec
└── testdata/               # Test vectors, sample packets
```

### Pattern 1: Internal Packages for Implementation Details
**What:** Use `internal/` directory for range coder and codec internals
**When to use:** Always - prevents external import of implementation details
**Example:**
```go
// internal/rangecoding/decoder.go
package rangecoding

// Decoder implements RFC 6716 Section 4.1 range decoding
type Decoder struct {
    buf     []byte   // Input buffer
    storage uint32   // Buffer size
    offs    uint32   // Current read offset
    rng     uint32   // Range size (must be > 2^23 after init)
    val     uint32   // High end - coded value - 1
    rem     int      // Buffered partial byte
    // ... additional state
}
```

### Pattern 2: Bit-Exact Integer Arithmetic
**What:** All range coder operations use 32-bit unsigned integer math exactly as specified
**When to use:** All range coder operations
**Example:**
```go
// Source: RFC 6716 Section 4.1, libopus entdec.c
// Integer division for frequency scaling
func (d *Decoder) decode(ft uint32) uint32 {
    d.ext = d.rng / ft  // Integer division
    s := d.val / d.ext
    return ft - min(s+1, ft)  // fs calculation
}
```

### Pattern 3: Constants Match Reference Implementation
**What:** Use identical constant names and values as libopus
**When to use:** All entropy coding constants
**Example:**
```go
// Source: libopus celt/mfrngcod.h
const (
    EC_SYM_BITS   = 8                        // Bits output at a time
    EC_CODE_BITS  = 32                       // Total state register bits
    EC_SYM_MAX    = (1 << EC_SYM_BITS) - 1   // 255
    EC_CODE_TOP   = 1 << 31                  // 0x80000000
    EC_CODE_BOT   = EC_CODE_TOP >> EC_SYM_BITS  // 0x00800000
    EC_CODE_SHIFT = EC_CODE_BITS - EC_SYM_BITS - 1  // 23
    EC_CODE_EXTRA = (EC_CODE_BITS-2)%EC_SYM_BITS + 1  // 7
)
```

### Pattern 4: Table-Driven Packet Parsing
**What:** Use lookup tables for TOC byte configuration mapping
**When to use:** TOC byte parsing
**Example:**
```go
// Source: RFC 6716 Section 3.1
type Mode uint8
const (
    ModeSILK   Mode = iota
    ModeHybrid
    ModeCELT
)

type Bandwidth uint8
const (
    BandwidthNarrowband     Bandwidth = iota  // 8 kHz
    BandwidthMediumband                        // 12 kHz
    BandwidthWideband                          // 16 kHz
    BandwidthSuperwideband                     // 24 kHz
    BandwidthFullband                          // 48 kHz
)

// Configuration lookup table (config 0-31)
var configTable = [32]struct {
    Mode      Mode
    Bandwidth Bandwidth
    FrameSize int // in samples at 48kHz
}{
    // SILK NB 10/20/40/60ms
    {ModeSILK, BandwidthNarrowband, 480},   // config 0
    {ModeSILK, BandwidthNarrowband, 960},   // config 1
    {ModeSILK, BandwidthNarrowband, 1920},  // config 2
    {ModeSILK, BandwidthNarrowband, 2880},  // config 3
    // ... continue for all 32 configs
}
```

### Anti-Patterns to Avoid
- **Floating-point in range coder:** Must use integer arithmetic only; float introduces non-determinism
- **Assuming byte alignment:** Range coder reads partial bytes; track bit position precisely
- **Ignoring carry propagation:** Encoder must handle multi-symbol carry chains
- **Modifying reference algorithm:** Even "optimizations" can break bit-exactness

## Don't Hand-Roll

Problems that look simple but have existing solutions:

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Integer log2 | Bit counting loop | `bits.Len32()` | Standard library, optimized |
| Min/max functions | Custom generics | `min()`/`max()` builtin (Go 1.21+) | Available since Go 1.21 |
| Byte order | Manual bit shifting | `encoding/binary` | Handles endianness correctly |
| Test comparison | Custom diff | `testing` + `reflect.DeepEqual` | Standard approach |

**Key insight:** This phase has minimal external dependencies by design. The complexity is in correctly implementing the RFC-specified algorithms, not in choosing libraries.

## Common Pitfalls

### Pitfall 1: Incorrect Range Coder Initialization
**What goes wrong:** Decoder produces wrong symbols from first byte
**Why it happens:** Incorrect extraction of initial 7 bits, wrong `val` calculation
**How to avoid:** Follow RFC 6716 exactly: `val = 127 - (b0 >> 1)`, `rng = 128`, then normalize
**Warning signs:** First decoded symbol is always wrong

### Pitfall 2: Normalization Invariant Violation
**What goes wrong:** Range becomes too small, precision lost
**Why it happens:** Forgetting to normalize after decode, wrong loop condition
**How to avoid:** Always normalize when `rng <= EC_CODE_BOT` (0x800000), loop until `rng > 2^23`
**Warning signs:** Decoding fails after several symbols

### Pitfall 3: Symbol 0 Special Case
**What goes wrong:** Slight probability errors accumulate
**Why it happens:** Not handling `fl[k] == 0` case differently
**How to avoid:** Per RFC: if `fl[k] > 0`: `rng = (rng/ft) * (fh[k]-fl[k])`, else: `rng = rng - (rng/ft) * (ft-fh[k])`
**Warning signs:** Symbols decode but audio has subtle artifacts

### Pitfall 4: Encoder Carry Propagation
**What goes wrong:** Output bytes are corrupted
**Why it happens:** Not buffering potential carries, writing bytes too early
**How to avoid:** Use `rem` and `ext` fields to buffer symbols, propagate carry through chain
**Warning signs:** Decoder fails to read encoder output

### Pitfall 5: Raw Bits vs Range Coded Confusion
**What goes wrong:** CELT data decodes incorrectly
**Why it happens:** CELT uses "raw bits" packed at frame end, bypassing range coder
**How to avoid:** Implement `ec_dec_bits()` separately, read from end of buffer backwards
**Warning signs:** SILK decodes fine, CELT fails (not Phase 1 scope, but design for it)

### Pitfall 6: TOC Byte Bit Order
**What goes wrong:** Wrong mode/bandwidth/frame size extracted
**Why it happens:** Confusing bit positions in TOC byte
**How to avoid:** config = bits[7:3] (top 5), stereo = bit[2], frameCode = bits[1:0] (bottom 2)
**Warning signs:** Packets parsed as wrong mode

### Pitfall 7: Frame Length Two-Byte Encoding
**What goes wrong:** Frame lengths > 251 bytes parsed incorrectly
**Why it happens:** Forgetting the special encoding for values 252-255
**How to avoid:** If first byte >= 252: length = (secondByte * 4) + firstByte
**Warning signs:** Large VBR frames fail to parse

## Code Examples

Verified patterns from official sources:

### Range Decoder Initialization
```go
// Source: RFC 6716 Section 4.1, libopus celt/entdec.c
func (d *Decoder) Init(buf []byte) {
    d.buf = buf
    d.storage = uint32(len(buf))
    d.offs = 0
    d.end_offs = 0
    d.end_window = 0
    d.nend_bits = 0

    // nbits_total starts accounting for the initial read
    d.nbits_total = EC_CODE_BITS + 1 -
        ((EC_CODE_BITS-EC_CODE_EXTRA)/EC_SYM_BITS)*EC_SYM_BITS

    d.rng = 1 << EC_CODE_EXTRA  // Start with partial range
    d.rem = int(d.readByte())
    d.val = d.rng - 1 - uint32(d.rem>>(EC_SYM_BITS-EC_CODE_EXTRA))
    d.error = 0

    d.normalize()  // Establish rng > 2^23 invariant
}
```

### Range Decoder Normalize
```go
// Source: libopus celt/entdec.c ec_dec_normalize()
func (d *Decoder) normalize() {
    for d.rng <= EC_CODE_BOT {
        d.nbits_total += EC_SYM_BITS
        d.rng <<= EC_SYM_BITS

        sym := d.rem
        d.rem = int(d.readByte())
        sym = (sym<<EC_SYM_BITS | d.rem) >> (EC_SYM_BITS - EC_CODE_EXTRA)

        d.val = ((d.val << EC_SYM_BITS) + uint32(EC_SYM_MAX&^sym)) & (EC_CODE_TOP - 1)
    }
}
```

### Decode Symbol with ICDF
```go
// Source: libopus celt/entdec.c ec_dec_icdf()
// Decodes symbol using inverse cumulative distribution function
func (d *Decoder) DecodeICDF(icdf []uint8, ftb uint) int {
    r := d.rng >> ftb
    v := d.val

    var s, t uint32
    ret := -1
    s = d.rng
    for {
        t = s
        ret++
        s = r * uint32(icdf[ret])
        if v >= s {
            break
        }
    }

    d.val = v - s
    d.rng = t - s
    d.normalize()
    return ret
}
```

### TOC Byte Parsing
```go
// Source: RFC 6716 Section 3.1
type TOCHeader uint8

func (t TOCHeader) Configuration() int {
    return int(t >> 3)  // Top 5 bits
}

func (t TOCHeader) IsStereo() bool {
    return (t & 0x04) != 0  // Bit 2
}

func (t TOCHeader) FrameCode() int {
    return int(t & 0x03)  // Bottom 2 bits
}
```

### Frame Count Byte Parsing (Code 3)
```go
// Source: RFC 6716 Section 3.2.5
func ParseFrameCountByte(b byte) (isVBR, hasPadding bool, frameCount int) {
    isVBR = (b & 0x80) != 0      // Bit 7
    hasPadding = (b & 0x40) != 0 // Bit 6
    frameCount = int(b & 0x3F)   // Bits 0-5 (M field)
    return
}
```

### Range Encoder Core
```go
// Source: libopus celt/entenc.c ec_encode()
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
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Arithmetic coding | Range coding | Pre-Opus (CELT heritage) | Faster, byte-aligned output |
| Separate SILK/CELT entropy | Unified range coder | Opus RFC 6716 (2012) | Seamless mode switching |
| RFC 6716 test vectors | RFC 8251 test vectors | 2017 | Allows phase shift flexibility |

**Deprecated/outdated:**
- Original RFC 6716 Appendix A reference code had security issues fixed in RFC 8251
- libopus < 1.1 had compliance issues; current stable is 1.6 (Dec 2025)

## Open Questions

Things that couldn't be fully resolved:

1. **ec_tell_frac() Implementation Complexity**
   - What we know: Returns bits used in 1/8th bit precision, uses quadratic refinement
   - What's unclear: Exact algorithm for fractional bit counting in encoder
   - Recommendation: Copy libopus implementation exactly; test against reference

2. **Test Vector Integration Strategy**
   - What we know: RFC 8251 test vectors available as tar.gz archives
   - What's unclear: Best way to integrate into Go test suite
   - Recommendation: Download and commit to testdata/, create helper to load

3. **Encoder Buffer Sizing**
   - What we know: Encoder needs pre-allocated buffer
   - What's unclear: Optimal sizing strategy for VBR
   - Recommendation: Start with max packet size (1275 bytes), shrink after encode

## Sources

### Primary (HIGH confidence)
- [RFC 6716 Section 4.1](https://datatracker.ietf.org/doc/html/rfc6716) - Range decoder specification
- [RFC 6716 Section 3](https://datatracker.ietf.org/doc/html/rfc6716) - Packet structure specification
- [libopus celt/entdec.c](https://github.com/cisco/opus/blob/master/celt/entdec.c) - Reference decoder implementation
- [libopus celt/entenc.c](https://github.com/cisco/opus/blob/master/celt/entenc.c) - Reference encoder implementation
- [libopus celt/mfrngcod.h](https://github.com/cisco/opus/blob/master/celt/mfrngcod.h) - Constants definitions

### Secondary (MEDIUM confidence)
- [pion/opus](https://github.com/pion/opus) - Pure Go Opus implementation (decoder only, SILK only)
- [RFC 8251](https://datatracker.ietf.org/doc/html/rfc8251) - Updates and new test vectors
- [Opus test vectors](https://opus-codec.org/testvectors/) - Compliance test data

### Tertiary (LOW confidence)
- Go project structure best practices from community sources
- General range coding academic papers (Martin 1979, Moffat et al 1998)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH - stdlib only, well understood
- Architecture: HIGH - follows pion/opus pattern with libopus algorithms
- Pitfalls: HIGH - documented in RFC and verified against reference
- Range coder algorithm: HIGH - RFC normative + reference implementation
- Packet parsing: HIGH - RFC normative specification

**Research date:** 2026-01-21
**Valid until:** 2026-03-21 (stable RFC, unlikely to change)
