---
phase: 11-container
plan: 01
subsystem: ogg
tags: [ogg, container, crc32, opus, rfc7845, rfc3533]
depends_on:
  requires: []
  provides:
    - container/ogg package with CRC-32, page structure, and Opus headers
    - Ogg page encoding/decoding with segment table handling
    - OpusHead and OpusTags per RFC 7845
  affects:
    - 11-02: Writer builds on page encoding
    - 11-03: Reader builds on page parsing
tech-stack:
  added: []
  patterns:
    - Pre-computed CRC lookup table in init()
    - Segment table for packet continuation across pages
    - Little-endian binary encoding for all headers
key-files:
  created:
    - container/ogg/doc.go
    - container/ogg/crc.go
    - container/ogg/page.go
    - container/ogg/header.go
    - container/ogg/errors.go
    - container/ogg/ogg_test.go
  modified: []
decisions:
  - id: D11-01-01
    decision: Use polynomial 0x04C11DB7 (not IEEE) for Ogg CRC-32
    rationale: Ogg specification requires non-IEEE polynomial; hash/crc32 cannot be used
  - id: D11-01-02
    decision: Segment table handles packets > 255 bytes with continuation
    rationale: Ogg format requires splitting large packets into 255-byte segments
  - id: D11-01-03
    decision: Mapping family 0 implicit, family 1/255 explicit channel mapping
    rationale: Per RFC 7845, mono/stereo (family 0) has implicit order
metrics:
  duration: ~6 minutes
  completed: 2026-01-22
---

# Phase 11 Plan 01: Ogg Page Layer Foundation Summary

Ogg container package with CRC-32, page structure, segment tables, and Opus headers (OpusHead, OpusTags) per RFC 7845 and RFC 3533.

## Commits

| Hash | Type | Description |
|------|------|-------------|
| 0f5dc22 | feat | Add Ogg package foundation with CRC and page structure |
| 6f7f224 | feat | Implement OpusHead and OpusTags headers per RFC 7845 |
| 1ad4b40 | test | Add comprehensive CRC verification and continuation tests |

## Implementation Details

### Ogg CRC-32

- Pre-computed 256-entry lookup table using polynomial 0x04C11DB7
- NOT the IEEE polynomial used by hash/crc32 (0xEDB88320)
- Functions: `oggCRC(data)` and `oggCRCUpdate(crc, data)`
- Proven compatible with opusdec via existing crossval_test.go

### Page Structure

- 27-byte fixed header with "OggS" magic
- Variable-length segment table (max 255 segments)
- Flags: BOS (0x02), EOS (0x04), Continuation (0x01)
- `Page.Encode()` produces bytes with proper CRC
- `ParsePage()` verifies CRC and returns `ErrBadCRC` on mismatch

### Segment Table

- `BuildSegmentTable(packetLen)` creates segment entries
- Packets > 255 bytes span multiple segments (255, 255, ..., remainder)
- Exact multiples of 255 need trailing 0 segment
- `ParseSegmentTable()` extracts packet lengths

### OpusHead (Identification Header)

- 19 bytes for mapping family 0 (mono/stereo)
- 21 + channels bytes for family 1/255 (surround/discrete)
- Fields: Version, Channels, PreSkip (312), SampleRate, OutputGain, MappingFamily
- Extended fields for surround: StreamCount, CoupledCount, ChannelMapping

### OpusTags (Comment Header)

- Magic "OpusTags" + vendor string + user comments
- Comments as key=value pairs
- Default vendor: "gopus"

### Error Types

- `ErrInvalidPage`: Missing magic, bad version, truncated data
- `ErrInvalidHeader`: Bad Opus header format or version
- `ErrBadCRC`: CRC checksum mismatch
- `ErrUnexpectedEOS`: Stream ended unexpectedly

## Test Coverage

27 test functions with 50 subtests:

- CRC: empty, consistency, uniqueness, corruption detection, polynomial verification
- Segment table: various sizes (0-2000 bytes), round-trip
- Page: encode, parse, bad CRC, truncated, multiple packets, large packets, flags
- OpusHead: mono, stereo, 5.1, 7.1, quad, round-trip, error cases
- OpusTags: default, with comments, empty vendor, error cases
- Integration: full Ogg Opus page round-trip, audio data, multiple pages

## Verification

All success criteria met:

1. [x] container/ogg package compiles with zero errors
2. [x] Ogg CRC-32 produces correct checksums (verified against crossval_test.go)
3. [x] Page encoding produces valid Ogg pages starting with "OggS"
4. [x] Page parsing verifies CRC and extracts packets correctly
5. [x] OpusHead handles all mapping families (0, 1, 255)
6. [x] OpusTags encodes/parses vendor and comments
7. [x] All round-trip tests pass
8. [x] Segment table handles packets of any size (including > 255 bytes)

## Deviations from Plan

None - plan executed exactly as written.

## Next Phase Readiness

Ready for 11-02 (Writer):
- Page.Encode() available for generating pages
- BuildSegmentTable() for packet segmentation
- OpusHead/OpusTags for header generation
- All exports documented and tested
