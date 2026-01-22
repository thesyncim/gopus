# Phase 11: Container (Ogg Opus) - Research

**Researched:** 2026-01-22
**Domain:** Ogg container format for Opus audio (RFC 7845)
**Confidence:** HIGH

## Summary

Phase 11 implements Ogg Opus container read/write functionality, enabling gopus to produce `.opus` files playable by standard players (VLC, FFmpeg, browsers) and read `.opus` files created by other tools. The Ogg container provides packet framing, timing information (granule position), metadata (OpusTags), and error recovery via page-level CRC.

Research analyzed RFC 7845 (Ogg Opus encapsulation), RFC 3533 (Ogg format), existing Go Ogg implementations (pion/webrtc oggwriter/oggreader), and the existing test helpers in this codebase (`internal/celt/crossval_test.go`, `internal/multistream/libopus_test.go`). The codebase already has working Ogg page construction code with correct CRC calculation, OpusHead generation for both mono/stereo (family 0) and multichannel (family 1), which can be refactored into a production-ready container package.

**Primary recommendation:** Implement a self-contained `container/ogg` package inspired by pion/webrtc's oggwriter/oggreader structure, but extended for full multistream support. Use the existing CRC and page construction code from test files as a foundation, promoting it to production code with proper error handling and io.Reader/io.Writer interfaces.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go standard library | 1.25+ | All functionality | Zero dependencies, project requirement |
| `encoding/binary` | stdlib | Little-endian encoding for headers | Standard binary serialization |
| `io` | stdlib | Reader/Writer interfaces | Standard streaming interfaces |
| `hash/crc32` | N/A | Do NOT use | Ogg uses non-standard CRC polynomial |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `bytes` | stdlib | Buffer construction | Building pages in memory |
| `errors` | stdlib | Error types | Public error definitions |
| `internal/multistream` | Phase 9 | Channel mapping | DefaultMapping() for surround |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Custom CRC | github.com/grd/ogg | Ogg-specific CRC, but adds dependency |
| Custom implementation | pion/webrtc/pkg/media | Well-tested but RTP-focused, lacks multistream |
| Build from scratch | Refactor existing test code | Test code already proven to work with opusdec |

**No external dependencies required.** The Ogg format is simple enough to implement in pure Go, and the codebase already has working implementations in test files.

## Architecture Patterns

### Recommended Project Structure
```
gopus/
├── container/
│   └── ogg/
│       ├── doc.go           # Package documentation
│       ├── page.go          # Ogg page structure and CRC
│       ├── reader.go        # OggReader for demuxing
│       ├── writer.go        # OggWriter for muxing
│       ├── header.go        # OpusHead and OpusTags parsing/generation
│       ├── errors.go        # Public error types
│       └── ogg_test.go      # Tests
```

### Pattern 1: Page-Based Streaming
**What:** Pages are the atomic unit of Ogg; each page is independently CRC-verified
**When to use:** All read/write operations work at page granularity
**Source:** RFC 3533, pion/webrtc oggreader

```go
// OggPage represents a single Ogg page with header and payload
type OggPage struct {
    HeaderType    byte   // 0x00=continuation, 0x02=BOS, 0x04=EOS
    GranulePos    uint64 // Sample position (48kHz for Opus)
    SerialNumber  uint32 // Bitstream ID
    PageSequence  uint32 // Page number
    Segments      []byte // Segment table
    Payload       []byte // Page data (concatenated segments)
}

// Write constructs the page with CRC
func (p *OggPage) Write(w io.Writer) error {
    // Build header (27 bytes)
    header := make([]byte, 27+len(p.Segments))
    copy(header[0:4], "OggS")
    header[4] = 0  // version
    header[5] = p.HeaderType
    binary.LittleEndian.PutUint64(header[6:14], p.GranulePos)
    binary.LittleEndian.PutUint32(header[14:18], p.SerialNumber)
    binary.LittleEndian.PutUint32(header[18:22], p.PageSequence)
    // CRC placeholder at [22:26]
    header[26] = byte(len(p.Segments))
    copy(header[27:], p.Segments)

    // Compute CRC over header + payload (with CRC field zeroed)
    crc := oggCRC(header)
    crc = oggCRCUpdate(crc, p.Payload)
    binary.LittleEndian.PutUint32(header[22:26], crc)

    if _, err := w.Write(header); err != nil {
        return err
    }
    _, err := w.Write(p.Payload)
    return err
}
```

### Pattern 2: Segment Table for Packet Framing
**What:** Segments encode packet boundaries within pages; 255 = continuation, <255 = packet end
**When to use:** Converting between Opus packets and Ogg pages
**Source:** RFC 3533 Section 6, existing codebase

```go
// buildSegmentTable creates the segment table for a packet
// Packets > 255 bytes span multiple segments (each 255 except final)
func buildSegmentTable(packetLen int) []byte {
    segments := make([]byte, 0, (packetLen/255)+1)
    remaining := packetLen
    for remaining >= 255 {
        segments = append(segments, 255)
        remaining -= 255
    }
    segments = append(segments, byte(remaining))
    return segments
}

// parseSegmentTable extracts packet lengths from segment table
func parseSegmentTable(segments []byte) []int {
    var packetLens []int
    packetLen := 0
    for _, seg := range segments {
        packetLen += int(seg)
        if seg < 255 {
            packetLens = append(packetLens, packetLen)
            packetLen = 0
        }
    }
    // If last segment is 255, packet continues on next page
    if len(segments) > 0 && segments[len(segments)-1] == 255 {
        packetLens = append(packetLens, packetLen) // partial
    }
    return packetLens
}
```

### Pattern 3: Pre-skip and Granule Position Tracking
**What:** Granule position = total samples at 48kHz; PCM position = granule - preskip
**When to use:** Seeking and timing calculations
**Source:** RFC 7845 Section 4

```go
// GranulePosition tracks sample count for Ogg Opus
type GranuleTracker struct {
    preskip        uint16  // From OpusHead
    currentGranule uint64  // Running total
}

// AddSamples updates granule position after encoding a frame
func (t *GranuleTracker) AddSamples(samples int) {
    t.currentGranule += uint64(samples)
}

// PCMPosition returns the actual playback position
func (t *GranuleTracker) PCMPosition() int64 {
    if t.currentGranule < uint64(t.preskip) {
        return 0
    }
    return int64(t.currentGranule - uint64(t.preskip))
}

// SeekToSample returns the granule position for a PCM sample
func (t *GranuleTracker) SeekToSample(sample int64) uint64 {
    return uint64(sample) + uint64(t.preskip)
}
```

### Pattern 4: OpusHead Construction for Multistream
**What:** OpusHead varies by channel mapping family (0, 1, 255)
**When to use:** Creating headers for mono/stereo vs surround
**Source:** RFC 7845 Section 5.1.1, existing internal/multistream/libopus_test.go

```go
// OpusHead represents the Opus identification header
type OpusHead struct {
    Version        uint8
    Channels       uint8
    PreSkip        uint16
    SampleRate     uint32  // Original sample rate (metadata only)
    OutputGain     int16   // Q7.8 dB gain
    MappingFamily  uint8   // 0=mono/stereo, 1=surround, 255=undefined
    StreamCount    uint8   // For family 1/255
    CoupledCount   uint8   // For family 1/255
    ChannelMapping []byte  // For family 1/255
}

// Encode writes OpusHead to bytes
func (h *OpusHead) Encode() []byte {
    // Base size: 19 bytes for family 0
    size := 19
    if h.MappingFamily != 0 {
        size = 21 + int(h.Channels)
    }

    buf := make([]byte, size)
    copy(buf[0:8], "OpusHead")
    buf[8] = h.Version
    buf[9] = h.Channels
    binary.LittleEndian.PutUint16(buf[10:12], h.PreSkip)
    binary.LittleEndian.PutUint32(buf[12:16], h.SampleRate)
    binary.LittleEndian.PutUint16(buf[16:18], uint16(h.OutputGain))
    buf[18] = h.MappingFamily

    if h.MappingFamily != 0 {
        buf[19] = h.StreamCount
        buf[20] = h.CoupledCount
        copy(buf[21:], h.ChannelMapping)
    }

    return buf
}
```

### Anti-Patterns to Avoid
- **Using hash/crc32 package:** Ogg uses polynomial 0x04C11DB7 with non-standard initialization
- **Single-segment pages for large packets:** Packets > 255 bytes MUST span multiple segments
- **Forgetting header page granule position:** ID header and comment header pages MUST have granule = 0
- **Ignoring pre-skip on decode:** First `preskip` samples are encoder delay, not audio
- **Assuming fixed frame sizes:** Opus frame sizes vary (2.5ms to 60ms); parse TOC byte

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| CRC-32 | Use hash/crc32 package | Custom lookup table with poly 0x04C11DB7 | Ogg uses non-IEEE polynomial |
| Channel mapping | Manual tables | `multistream.DefaultMapping()` | Already implemented, RFC-compliant |
| Pre-skip value | Hardcode 312 | Query encoder lookahead | Varies by encoder implementation |
| Segment splitting | Simple loop | buildSegmentTable helper | Edge cases at 255 boundaries |
| Packet continuation | Ignore across pages | Track partial packets | Large packets span pages |

**Key insight:** The existing test code in `internal/celt/crossval_test.go` and `internal/multistream/libopus_test.go` already implements correct Ogg page construction, CRC, and headers. Refactor rather than rewrite.

## Common Pitfalls

### Pitfall 1: Ogg CRC Polynomial
**What goes wrong:** Using Go's hash/crc32 produces wrong checksums, files unplayable
**Why it happens:** Ogg uses polynomial 0x04C11DB7 (CRC-32/BZIP2), not IEEE
**How to avoid:** Use the custom CRC implementation from existing test code
**Warning signs:** opusdec fails with "hole in data"

```go
// CORRECT: Ogg-specific CRC (from internal/celt/crossval_test.go)
var oggCRCTable [256]uint32

func init() {
    poly := uint32(0x04C11DB7)
    for i := 0; i < 256; i++ {
        r := uint32(i) << 24
        for j := 0; j < 8; j++ {
            if r&0x80000000 != 0 {
                r = (r << 1) ^ poly
            } else {
                r <<= 1
            }
        }
        oggCRCTable[i] = r
    }
}

func oggCRC(data []byte) uint32 {
    var crc uint32
    for _, b := range data {
        crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
    }
    return crc
}
```

### Pitfall 2: Header Page Requirements
**What goes wrong:** ID header not on BOS page, or granule != 0
**Why it happens:** RFC 7845 has strict header page requirements
**How to avoid:** Follow exact sequence: BOS page with OpusHead alone, then OpusTags
**Warning signs:** "Invalid header" errors from opusdec

**Requirements:**
1. Page 0: BOS flag (0x02), granule = 0, contains ONLY OpusHead
2. Page 1+: granule = 0 until comment completes, OpusTags starts here
3. Page N+: Audio data, granule = sample count including pre-skip

### Pitfall 3: Pre-skip Mismatch
**What goes wrong:** First samples sound wrong, or audio offset from video
**Why it happens:** Encoder adds lookahead delay that must be trimmed on decode
**How to avoid:** Set pre-skip = encoder lookahead (typically 312 samples at 48kHz)
**Warning signs:** Click at start, or A/V sync issues

```go
// Pre-skip should match encoder's actual lookahead
// Standard value is 312 samples (6.5ms) but can vary
const defaultPreSkip = 312  // Standard Opus encoder lookahead

// For more accuracy, query the encoder
func getPreSkip(enc *encoder.Encoder) uint16 {
    // If encoder exposes lookahead, use it
    // Otherwise use default
    return defaultPreSkip
}
```

### Pitfall 4: Granule Position Calculation
**What goes wrong:** Seeking lands at wrong position, duration incorrect
**Why it happens:** Granule position is samples AFTER pre-skip adjustment
**How to avoid:** granulePos = totalSamplesEncoded (at 48kHz), NOT PCM position
**Warning signs:** Wrong duration reported, seek errors

```go
// CORRECT: Granule position calculation
// After encoding 10 frames of 960 samples each (20ms @ 48kHz):
// granulePos = 960 * 10 = 9600
// PCM position = 9600 - 312 (preskip) = 9288 samples playable
```

### Pitfall 5: End-of-Stream Trimming
**What goes wrong:** Extra samples at end, or truncated audio
**Why it happens:** Final granule position may indicate fewer samples than decoded
**How to avoid:** Honor final page granule position for end trimming
**Warning signs:** Clicks or silence at end

```go
// On EOS page, granule position may be less than expected
// This indicates samples to discard from final decode
func handleEOS(page *OggPage, lastGranule uint64, decodedSamples int) int {
    expectedSamples := int(page.GranulePos - lastGranule)
    if expectedSamples < decodedSamples {
        // Trim excess samples
        return expectedSamples
    }
    return decodedSamples
}
```

### Pitfall 6: Large Packet Segmentation
**What goes wrong:** Packets > 255 bytes cause parse errors
**Why it happens:** Not properly splitting into multiple segments
**How to avoid:** Use segment table with 255-byte continuations
**Warning signs:** "Invalid segment" errors, truncated audio

## Code Examples

Verified patterns from existing codebase and pion/webrtc.

### Ogg CRC Calculation (from internal/celt/crossval_test.go)
```go
// Source: internal/celt/crossval_test.go lines 66-94
var oggCRCLookup [256]uint32

func init() {
    poly := uint32(0x04C11DB7)
    for i := 0; i < 256; i++ {
        crc := uint32(i) << 24
        for j := 0; j < 8; j++ {
            if crc&0x80000000 != 0 {
                crc = (crc << 1) ^ poly
            } else {
                crc <<= 1
            }
        }
        oggCRCLookup[i] = crc
    }
}

func computeOggCRC(data []byte) uint32 {
    var crc uint32
    for _, b := range data {
        crc = (crc << 8) ^ oggCRCLookup[((crc>>24)&0xff)^uint32(b)]
    }
    return crc
}
```

### OpusHead for Multistream (from internal/multistream/libopus_test.go)
```go
// Source: internal/multistream/libopus_test.go lines 99-130
func makeOpusHeadMultistream(channels, sampleRate int, streams, coupledStreams int, mapping []byte) []byte {
    size := 21 + len(mapping)
    head := make([]byte, size)

    copy(head[0:8], "OpusHead")
    head[8] = 1                                                // Version
    head[9] = byte(channels)                                   // Channel count
    binary.LittleEndian.PutUint16(head[10:12], 312)            // Pre-skip
    binary.LittleEndian.PutUint32(head[12:16], uint32(sampleRate))
    binary.LittleEndian.PutUint16(head[16:18], 0)              // Output gain
    head[18] = 1                                               // Mapping family 1 (surround)
    head[19] = byte(streams)                                   // Stream count
    head[20] = byte(coupledStreams)                            // Coupled stream count
    copy(head[21:], mapping)                                   // Channel mapping table

    return head
}
```

### Ogg Page Construction (from internal/multistream/libopus_test.go)
```go
// Source: internal/multistream/libopus_test.go lines 52-97
func makeOggPage(serialNo, pageNo uint32, headerType byte, granulePos uint64, segments [][]byte) []byte {
    // Build segment table
    var segmentTable []byte
    for _, seg := range segments {
        remaining := len(seg)
        for remaining >= 255 {
            segmentTable = append(segmentTable, 255)
            remaining -= 255
        }
        segmentTable = append(segmentTable, byte(remaining))
    }

    // Build header (27 bytes + segment table)
    header := make([]byte, 27+len(segmentTable))
    copy(header[0:4], "OggS")
    header[4] = 0                       // Version
    header[5] = headerType              // Header type
    binary.LittleEndian.PutUint64(header[6:14], granulePos)
    binary.LittleEndian.PutUint32(header[14:18], serialNo)
    binary.LittleEndian.PutUint32(header[18:22], pageNo)
    // CRC at [22:26] - computed after
    header[26] = byte(len(segmentTable))
    copy(header[27:], segmentTable)

    // Compute CRC over header + data
    crc := oggCRC(header)
    for _, seg := range segments {
        crc = oggCRCUpdate(crc, seg)
    }
    binary.LittleEndian.PutUint32(header[22:26], crc)

    // Combine
    var buf bytes.Buffer
    buf.Write(header)
    for _, seg := range segments {
        buf.Write(seg)
    }
    return buf.Bytes()
}
```

### OggWriter API Pattern (from pion/webrtc)
```go
// Source: pion/webrtc oggwriter pattern
type OggWriter struct {
    stream       io.Writer
    sampleRate   uint32
    channels     uint16
    serial       uint32
    pageIndex    uint32
    granulePos   uint64
    crcTable     [256]uint32
}

func NewOggWriter(w io.Writer, sampleRate uint32, channels uint16) (*OggWriter, error) {
    ow := &OggWriter{
        stream:     w,
        sampleRate: sampleRate,
        channels:   channels,
        serial:     rand.Uint32(),
        crcTable:   generateCRCTable(),
    }

    if err := ow.writeHeaders(); err != nil {
        return nil, err
    }

    return ow, nil
}

func (ow *OggWriter) WritePacket(packet []byte, samples int) error {
    ow.granulePos += uint64(samples)
    return ow.writePage(packet, 0x00, ow.granulePos)
}

func (ow *OggWriter) Close() error {
    // Write EOS page
    return ow.writePage(nil, 0x04, ow.granulePos)
}
```

### OggReader API Pattern (from pion/webrtc)
```go
// Source: pion/webrtc oggreader pattern
type OggReader struct {
    stream    io.Reader
    header    *OggHeader
    crcTable  [256]uint32
}

type OggHeader struct {
    Channels       uint8
    PreSkip        uint16
    SampleRate     uint32
    OutputGain     int16
    MappingFamily  uint8
    StreamCount    uint8
    CoupledCount   uint8
    ChannelMapping []byte
}

func NewOggReader(r io.Reader) (*OggReader, error) {
    or := &OggReader{
        stream:   r,
        crcTable: generateCRCTable(),
    }

    // Read and parse ID header
    if err := or.readIDHeader(); err != nil {
        return nil, err
    }

    // Skip comment header
    if err := or.skipCommentHeader(); err != nil {
        return nil, err
    }

    return or, nil
}

func (or *OggReader) ReadPacket() ([]byte, uint64, error) {
    page, err := or.readPage()
    if err != nil {
        return nil, 0, err
    }
    return page.Payload, page.GranulePos, nil
}
```

## Ogg Format Fundamentals

### Page Header Structure (27 bytes fixed + variable segment table)

| Offset | Size | Field | Description |
|--------|------|-------|-------------|
| 0 | 4 | Capture pattern | "OggS" (0x4F 0x67 0x67 0x53) |
| 4 | 1 | Version | Always 0 |
| 5 | 1 | Header type | 0x00=continuation, 0x02=BOS, 0x04=EOS |
| 6 | 8 | Granule position | 64-bit sample position (little-endian) |
| 14 | 4 | Serial number | Stream identifier |
| 18 | 4 | Page sequence | Page number in stream |
| 22 | 4 | CRC checksum | CRC-32 of entire page |
| 26 | 1 | Segment count | Number of segment table entries (0-255) |
| 27+ | N | Segment table | N bytes, each 0-255 indicating segment length |
| 27+N | ... | Payload | Concatenated segments |

### Header Type Flags
```go
const (
    PageFlagContinuation = 0x01  // Packet continues from previous page
    PageFlagBOS          = 0x02  // Beginning of stream
    PageFlagEOS          = 0x04  // End of stream
)
```

### Granule Position Special Values
- `0`: Used for header pages (ID and comment)
- `-1` (0xFFFFFFFFFFFFFFFF): Page entirely spanned by a single continuing packet
- Otherwise: Total samples at 48kHz after all completed packets on this page

## RFC 7845 Opus Encapsulation Specifics

### OpusHead (ID Header) Format

| Offset | Size | Field | Description |
|--------|------|-------|-------------|
| 0 | 8 | Magic | "OpusHead" |
| 8 | 1 | Version | Must be 1 |
| 9 | 1 | Channels | Output channel count (1-255) |
| 10 | 2 | Pre-skip | Samples to discard (little-endian) |
| 12 | 4 | Sample rate | Original sample rate (little-endian) |
| 16 | 2 | Output gain | Q7.8 dB (signed, little-endian) |
| 18 | 1 | Mapping family | 0, 1, or 255 |
| 19+ | ... | (Family 1/255 only) | Stream count, coupled count, mapping |

### OpusTags (Comment Header) Format

| Offset | Size | Field | Description |
|--------|------|-------|-------------|
| 0 | 8 | Magic | "OpusTags" |
| 8 | 4 | Vendor length | Length of vendor string |
| 12 | N | Vendor string | UTF-8 encoder name (e.g., "gopus") |
| 12+N | 4 | Comment count | Number of user comments |
| ... | ... | Comments | Each: 4-byte length + UTF-8 "NAME=value" |

### Channel Mapping Families

**Family 0 (RTP-compatible):**
- 1-2 channels only
- No mapping table in header
- Mono or stereo, no surround

**Family 1 (Vorbis order):**
- 1-8 channels
- Extended header with stream count, coupled count, mapping table
- Surround configurations per RFC 7845 Section 5.1.1.2

**Family 255 (Discrete):**
- 1-255 channels
- Extended header format
- Channels have no defined semantics

### Mandatory Page Structure

```
Page 0:  BOS flag | granule=0 | OpusHead (complete, alone)
Page 1+: Normal   | granule=0 | OpusTags (may span pages)
Page N+: Normal   | granule>0 | Audio packets
Page M:  EOS flag | granule=final | Last audio packet
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| cgo libopusfile | Pure Go implementation | 2024+ | No C dependencies |
| libogg C bindings | Custom Ogg handling | Always for pure Go | Simpler, no FFI |
| Fixed pre-skip 312 | Query encoder lookahead | Variable | More accurate timing |
| Single-channel focus | Multistream family 1 | RFC 7845 | Full surround support |

**Deprecated/outdated:**
- Mapping family 2/3 (ambisonics): Defined in RFC 8486, not implemented in most players
- ogg.xiph.org C library wrappers: Prefer pure Go for this project

## Open Questions

1. **Seeking implementation complexity**
   - What we know: Bisection search on granule position is standard approach
   - What's unclear: Should reader support seeking initially, or just sequential read?
   - Recommendation: Implement sequential reader first; seeking can be added later

2. **Multiple logical bitstreams**
   - What we know: Ogg supports multiplexing via serial numbers
   - What's unclear: Should we support multiple Opus streams in one file?
   - Recommendation: Single stream for MVP; multi-stream is edge case

3. **Chained Ogg files**
   - What we know: Multiple logical streams can be concatenated
   - What's unclear: How common is this for Opus?
   - Recommendation: Detect EOS and support reading next stream

4. **R128 gain normalization**
   - What we know: OutputGain field + optional R128_TRACK_GAIN tag
   - What's unclear: Should reader apply gain automatically?
   - Recommendation: Expose gain values; let caller decide to apply

## Sources

### Primary (HIGH confidence)
- [RFC 7845 - Ogg Encapsulation for the Opus Audio Codec](https://datatracker.ietf.org/doc/html/rfc7845) - Complete specification
- [RFC 3533 - The Ogg Encapsulation Format](https://datatracker.ietf.org/doc/html/rfc3533) - Ogg container basics
- [pion/webrtc oggwriter](https://pkg.go.dev/github.com/pion/webrtc/v4/pkg/media/oggwriter) - Pure Go reference
- [pion/webrtc oggreader](https://pkg.go.dev/github.com/pion/webrtc/v4/pkg/media/oggreader) - Pure Go reference
- Existing codebase: `internal/celt/crossval_test.go`, `internal/multistream/libopus_test.go`

### Secondary (MEDIUM confidence)
- [Xiph.org Ogg Documentation](https://xiph.org/ogg/doc/framing.html) - Framing details
- [OggOpus wiki](https://wiki.xiph.org/OggOpus) - Implementation notes
- [Ogg Opus demuxing guide](https://gist.github.com/amishshah/68548e803c3208566e36e55fe1618e1c) - Practical walkthrough

### Tertiary (LOW confidence)
- [hraban/opus Go wrapper](https://github.com/hraban/opus) - cgo-based, limited Ogg write support
- [grd/ogg Pure Go](https://pkg.go.dev/github.com/grd/ogg) - Unmaintained but pure Go

## Metadata

**Confidence breakdown:**
- Ogg format: HIGH - RFC 3533 is definitive, existing code validates
- RFC 7845 specifics: HIGH - Specification is clear, pion implements it
- Implementation patterns: HIGH - Existing test code works with opusdec
- Multistream handling: HIGH - internal/multistream already RFC-compliant

**Research date:** 2026-01-22
**Valid until:** 2026-04-22 (90 days - stable specification, no expected changes)
