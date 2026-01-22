# Phase 12: Compliance & Polish - Research

**Researched:** 2026-01-22
**Domain:** Opus RFC 8251 compliance testing, encoder validation, release documentation
**Confidence:** HIGH

## Summary

Phase 12 focuses on validating gopus against official Opus test vectors and preparing for release. Research reveals that Opus compliance testing uses a perceptual quality metric (opus_compare) with a clear pass/fail threshold, not bit-exact comparison. The official test vectors use a proprietary framing format (.bit files) that requires parsing, and decoder output is compared against reference .dec files.

The project already has extensive libopus cross-validation tests using opusdec, which validates encoder output. For decoder compliance, the official test vectors from RFC 8251 must be downloaded, parsed, and run through the decoder with output compared using the opus_compare quality metric (Q >= 0 passes).

**Primary recommendation:** Implement test vector parsing for opus_demo format, run decoder compliance tests with quality metric verification, fix the internal/encoder import cycle, and complete API documentation with testable examples.

## Standard Stack

### Core
| Tool/Resource | Version | Purpose | Why Standard |
|--------------|---------|---------|--------------|
| RFC 8251 test vectors | 2017+ | Official decoder compliance | IETF-mandated test suite |
| opusdec (opus-tools) | 0.2+ | Encoder validation | Reference libopus decoder |
| go test | 1.21+ | Test framework | Standard Go tooling |

### Supporting
| Tool | Purpose | When to Use |
|------|---------|-------------|
| file/ldd | Binary analysis | Verify static/no-cgo build |
| godoc | Documentation generation | API doc verification |
| go doc | Package doc preview | Local doc check |

### Test Vector Downloads
```bash
# RFC 8251 test vectors (recommended)
curl -OL https://opus-codec.org/docs/opus_testvectors-rfc8251.tar.gz
tar -zxf opus_testvectors-rfc8251.tar.gz

# Original RFC 6716 vectors (still valid)
curl -OL https://opus-codec.org/static/testvectors/opus_testvectors.tar.gz
```

## Architecture Patterns

### Test Vector File Format

The official test vectors use opus_demo's proprietary framing:

```
Test Vector Archive Contents:
opus_testvectors/
  testvector01.bit     # Encoded bitstream (opus_demo format)
  testvector01.dec     # Reference decoded output (raw PCM)
  testvector01m.dec    # Alternative reference (no phase shift)
  testvector02.bit
  testvector02.dec
  testvector02m.dec
  ... (12 test vectors total)
```

**opus_demo framing format:**
- Input/output: little-endian signed 16-bit PCM
- Bitstream: Simple proprietary framing with packet length prefix
- NOT Ogg Opus - raw packet sequences for testing

### opus_demo Bitstream Format (HIGH confidence)

Based on libopus source analysis, the .bit file format is:
```
For each packet:
  uint32_le: packet_length (4 bytes)
  uint32_le: enc_final_range (4 bytes, for range coder verification)
  byte[packet_length]: opus_packet_data
```

The decoder must:
1. Read packet length (4 bytes, little-endian)
2. Read enc_final_range (4 bytes, for verification)
3. Read opus packet data
4. Decode and output samples
5. Repeat until EOF

### Quality Metric Verification (opus_compare)

The opus_compare tool computes a perceptual quality metric:

```c
// Quality formula from opus_compare.c
Q = 100 * (1 - 0.5 * log(1 + err) / log(1.13))

// Pass/fail threshold
if (Q >= 0) {
    // Test passes - "quality 0" = 48 dB SNR threshold
} else {
    // Test fails
}
```

**Key characteristics:**
- Uses per-band spectral energy comparison (21 bands)
- Applies frequency masking (10 dB/Bark downward, 15 dB/Bark upward)
- Applies temporal masking (-3 dB/2.5ms)
- Q >= 90 recommended for 48kHz, Q >= 50 acceptable for other rates

### Decoder Test Pattern

```go
// Recommended test structure
func TestDecoderCompliance(t *testing.T) {
    testVectors := []string{
        "testvector01", "testvector02", ..., "testvector12"
    }

    for _, tv := range testVectors {
        t.Run(tv, func(t *testing.T) {
            // 1. Parse .bit file (opus_demo format)
            packets, ranges := parseOpusDemoBitstream(tv + ".bit")

            // 2. Decode all packets
            decoded := decodeAllPackets(packets)

            // 3. Compare with reference output
            reference := readPCMFile(tv + ".dec")
            referenceAlt := readPCMFile(tv + "m.dec")

            // 4. Compute quality metric
            q1 := computeQualityMetric(decoded, reference)
            q2 := computeQualityMetric(decoded, referenceAlt)

            // 5. Pass if either matches (RFC 8251 allows either)
            if q1 < 0 && q2 < 0 {
                t.Errorf("Quality metric failed: Q1=%.1f, Q2=%.1f", q1, q2)
            }
        })
    }
}
```

### Encoder Validation Pattern

Existing pattern is correct - validate via libopus opusdec:

```go
// Already implemented in internal/encoder/libopus_test.go
func TestLibopusHybridDecode(t *testing.T) {
    // 1. Encode with gopus
    // 2. Write to Ogg Opus container
    // 3. Decode with opusdec
    // 4. Check energy ratio > 10%
}
```

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Quality metric | Custom SNR calculation | opus_compare algorithm | Perceptual weighting matters |
| Test vector parsing | Ad-hoc format guessing | Document opus_demo format | Framing is specific |
| CGO verification | Manual inspection | `CGO_ENABLED=0 go build` | Build-time guarantee |
| Doc generation | Manual markdown | godoc/pkgsite conventions | Standard Go ecosystem |

**Key insight:** The opus_compare quality metric is NOT simple SNR - it includes psychoacoustic masking that makes it more lenient than raw sample comparison. Implementing this correctly is important for meaningful compliance testing.

## Common Pitfalls

### Pitfall 1: Assuming Bit-Exact Decoder Output Required
**What goes wrong:** Expecting decoder output to match reference byte-for-byte
**Why it happens:** Misunderstanding of RFC compliance definition
**How to avoid:** Use opus_compare quality metric; Q >= 0 passes
**Warning signs:** Tests fail with minor numerical differences

### Pitfall 2: Testing With Ogg Opus Instead of Raw Packets
**What goes wrong:** Using .opus files instead of .bit test vectors
**Why it happens:** Confusion between container and codec testing
**How to avoid:** Parse opus_demo .bit format correctly
**Warning signs:** Packet length mismatches, invalid TOC bytes

### Pitfall 3: Ignoring Phase Shift Variants
**What goes wrong:** Failing tests that should pass with RFC 8251 phase change
**Why it happens:** Only comparing against one reference file
**How to avoid:** Compare against both .dec and m.dec; pass if either matches
**Warning signs:** Tests fail with "good" quality scores (Q close to 0)

### Pitfall 4: Internal Package Import Cycles in Tests
**What goes wrong:** `go test ./...` fails with import cycle error
**Why it happens:** Test files import both public API and internal packages
**How to avoid:** Move tests to separate test package or restructure imports
**Warning signs:** Individual tests pass, batch mode fails

### Pitfall 5: Incomplete Documentation for pkg.go.dev
**What goes wrong:** Package doesn't render well on pkg.go.dev
**Why it happens:** Missing doc.go, poor first sentence, no examples
**How to avoid:** Follow godoc conventions, add testable examples
**Warning signs:** Empty or confusing documentation on pkg.go.dev

## Code Examples

### opus_demo Bitstream Parser

```go
// Source: Derived from libopus opus_demo.c analysis

// ParseOpusDemoBitstream reads opus_demo .bit file format
func ParseOpusDemoBitstream(filename string) ([]Packet, error) {
    data, err := os.ReadFile(filename)
    if err != nil {
        return nil, err
    }

    var packets []Packet
    offset := 0

    for offset < len(data) {
        if offset+8 > len(data) {
            break
        }

        // Read packet length (4 bytes, little-endian)
        packetLen := binary.LittleEndian.Uint32(data[offset:])
        offset += 4

        // Read enc_final_range (4 bytes, for range coder verification)
        finalRange := binary.LittleEndian.Uint32(data[offset:])
        offset += 4

        if offset+int(packetLen) > len(data) {
            return nil, fmt.Errorf("truncated packet at offset %d", offset)
        }

        // Read packet data
        packetData := make([]byte, packetLen)
        copy(packetData, data[offset:offset+int(packetLen)])
        offset += int(packetLen)

        packets = append(packets, Packet{
            Data:       packetData,
            FinalRange: finalRange,
        })
    }

    return packets, nil
}

type Packet struct {
    Data       []byte
    FinalRange uint32 // For range decoder state verification
}
```

### Quality Metric Implementation

```go
// Source: Adapted from opus_compare.c algorithm

const NBANDS = 21

// ComputeQualityMetric computes opus_compare quality metric
func ComputeQualityMetric(decoded, reference []int16, sampleRate int) float64 {
    // Compute per-band spectral energy for both signals
    // Apply frequency masking (10 dB/Bark down, 15 dB/Bark up)
    // Apply temporal masking (-3 dB/2.5ms)
    // Normalize and compute weighted error

    // Simplified version - full implementation requires FFT and bark scale
    err := computeWeightedError(decoded, reference, sampleRate)

    // Q = 100 * (1 - 0.5 * log(1+err) / log(1.13))
    q := 100.0 * (1.0 - 0.5*math.Log(1+err)/math.Log(1.13))

    return q
}

// TestPasses checks if quality metric passes threshold
func TestPasses(q float64) bool {
    return q >= 0 // Q >= 0 is the pass threshold (48 dB SNR equivalent)
}
```

### CGO Verification

```go
// Source: Go build system

// TestNoCGO verifies the build has no cgo dependencies
func TestNoCGO(t *testing.T) {
    // Build with CGO_ENABLED=0
    cmd := exec.Command("go", "build", "-o", "/dev/null", ".")
    cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("Build with CGO_ENABLED=0 failed: %v\n%s", err, output)
    }

    t.Log("PASS: Zero cgo dependencies verified")
}
```

### Testable Example Pattern

```go
// Source: Go godoc conventions

// In example_test.go
func ExampleEncoder() {
    enc, err := gopus.NewEncoder(48000, 2, gopus.ApplicationAudio)
    if err != nil {
        log.Fatal(err)
    }

    // Generate 20ms of stereo audio at 48kHz
    pcm := make([]float32, 960*2)
    for i := range pcm {
        pcm[i] = float32(math.Sin(float64(i) * 0.1))
    }

    packet, err := enc.EncodeFloat32(pcm)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Encoded %d samples to %d bytes\n", len(pcm)/2, len(packet))
    // Output: Encoded 960 samples to XX bytes
}

func ExampleDecoder() {
    dec, err := gopus.NewDecoder(48000, 2)
    if err != nil {
        log.Fatal(err)
    }

    // Decode a packet (example packet bytes)
    packet := []byte{0xFC, 0xFF, 0xFE} // Example silence packet

    pcm, err := dec.DecodeFloat32(packet)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Decoded to %d samples\n", len(pcm)/2)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| RFC 6716 vectors only | RFC 8251 vectors | 2017 | Phase shift flexibility |
| Bit-exact required | Q >= 0 metric | RFC 6716 | Perceptual tolerance |
| Single reference | Dual reference (.dec, m.dec) | RFC 8251 | More implementations pass |

**Current best practice:**
- Use RFC 8251 test vectors (backward compatible)
- Pass if either .dec or m.dec matches (Q >= 0)
- Quality 90+ recommended for 48kHz, 50+ for other rates

## Open Questions

1. **opus_compare Implementation Completeness**
   - What we know: Quality metric formula and threshold documented
   - What's unclear: Full FFT and bark-scale implementation details
   - Recommendation: Consider implementing simplified SNR-based comparison first, enhance to full opus_compare if needed

2. **Test Vector Sampling Rates**
   - What we know: Vectors tested at 8000, 12000, 16000, 24000, 48000 Hz
   - What's unclear: Which vectors require which rates
   - Recommendation: Start with 48kHz, expand to other rates after initial pass

3. **Import Cycle Resolution**
   - What we know: internal/encoder tests import gopus causing cycle
   - What's unclear: Best architectural fix
   - Recommendation: Move encoder integration tests to separate package or use build tags

## Sources

### Primary (HIGH confidence)
- [RFC 8251](https://www.rfc-editor.org/rfc/rfc8251.html) - Official Opus update specification
- [opus-codec.org/testvectors](https://opus-codec.org/testvectors/) - Test vector downloads
- [xiph/opus GitHub](https://github.com/xiph/opus/blob/main/src/opus_compare.c) - opus_compare source

### Secondary (MEDIUM confidence)
- [opus-website testvectors.md](https://github.com/xiph/opus-website/blob/master/testvectors.md) - Test vector documentation
- [RFC 6716](https://www.rfc-editor.org/rfc/rfc6716.html) - Original Opus specification

### Tertiary (LOW confidence)
- Various blog posts on Go documentation best practices
- Stack Overflow on CGO_ENABLED verification

## Metadata

**Confidence breakdown:**
- Test vector format: HIGH - Derived from official run_vectors.sh and opus_compare.c
- Quality metric: HIGH - Documented in RFC and opus_compare.c source
- Encoder validation: HIGH - Existing libopus tests validate approach
- Documentation: MEDIUM - Standard Go practices, no Opus-specific requirements

**Research date:** 2026-01-22
**Valid until:** 2026-03-22 (60 days - stable specification)
