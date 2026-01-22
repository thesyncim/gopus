// Package ogg implements the Ogg container format for Opus audio.
//
// This package provides low-level primitives for reading and writing Ogg Opus
// files as specified in RFC 7845 (Ogg Encapsulation for the Opus Audio Codec)
// and RFC 3533 (The Ogg Encapsulation Format).
//
// The Ogg format uses pages as atomic units of data, where each page contains:
//   - A 27-byte header with magic signature "OggS"
//   - A segment table describing packet boundaries
//   - Payload data containing one or more packets
//   - CRC-32 checksum for data integrity verification
//
// Opus-specific headers (per RFC 7845):
//   - OpusHead: Identification header with channel count, pre-skip, sample rate
//   - OpusTags: Comment header with vendor string and user comments
//
// # Page Structure
//
// An Ogg page has the following structure:
//
//	Bytes 0-3:   "OggS" capture pattern (magic signature)
//	Byte 4:      Stream structure version (always 0)
//	Byte 5:      Header type flags (continuation, BOS, EOS)
//	Bytes 6-13:  Granule position (samples decoded so far)
//	Bytes 14-17: Bitstream serial number
//	Bytes 18-21: Page sequence number
//	Bytes 22-25: CRC checksum
//	Byte 26:     Number of segments
//	Bytes 27+:   Segment table (one byte per segment)
//	Remaining:   Page payload data
//
// # Segment Table
//
// Packets are split into segments of up to 255 bytes each. A segment value
// of 255 indicates the packet continues in the next segment. A value less
// than 255 marks the end of a packet.
//
// Example: A 600-byte packet uses segments [255, 255, 90] (255+255+90=600)
//
// # CRC Calculation
//
// Ogg uses CRC-32 with polynomial 0x04C11DB7 (NOT the IEEE polynomial used
// by hash/crc32). The CRC is computed over the entire page with the CRC
// field set to zero.
//
// # OpusHead Format (RFC 7845 Section 5.1)
//
// The identification header appears in the first Ogg page (BOS flag set):
//
//	Bytes 0-7:   "OpusHead" magic signature
//	Byte 8:      Version (must be 1)
//	Byte 9:      Output channel count (1-255)
//	Bytes 10-11: Pre-skip (samples to discard at start)
//	Bytes 12-15: Input sample rate (informational)
//	Bytes 16-17: Output gain (Q7.8 dB)
//	Byte 18:     Channel mapping family
//	For mapping family 1 and 255:
//	  Byte 19:     Stream count
//	  Byte 20:     Coupled stream count
//	  Bytes 21+:   Channel mapping table
//
// # OpusTags Format (RFC 7845 Section 5.2)
//
// The comment header appears in the second Ogg page:
//
//	Bytes 0-7:   "OpusTags" magic signature
//	Bytes 8-11:  Vendor string length
//	Bytes 12+:   Vendor string (e.g., "gopus")
//	Next 4:      User comment count
//	For each comment:
//	  4 bytes:   Comment length
//	  N bytes:   Comment string ("FIELD=value")
//
// # References
//
//   - RFC 7845: Ogg Encapsulation for the Opus Audio Codec
//   - RFC 3533: The Ogg Encapsulation Format Version 0
//   - RFC 6716: Definition of the Opus Audio Codec
package ogg
