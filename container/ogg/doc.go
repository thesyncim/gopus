// Package ogg implements Ogg Opus container reading and writing.
//
// It provides low-level page, packet, OpusHead, and OpusTags primitives for
// RFC 7845 (Ogg Encapsulation for the Opus Audio Codec) streams on top of the
// Ogg framing rules from RFC 3533. The package verifies Ogg CRC-32 checksums,
// preserves packet boundaries across lacing segments, and leaves codec
// encode/decode work to the top-level gopus API.
//
// # Reading
//
// Reader consumes an io.Reader, parses the mandatory OpusHead identification
// header and OpusTags comment header up front, then yields one Opus packet per
// ReadPacket call. Packets that span multiple pages via the lacing table are
// reassembled transparently. When the underlying reader is also an
// io.ReadSeeker, SeekGranule provides sample-accurate positioning.
//
// # Writing
//
// Writer emits a well-formed Ogg Opus stream: a beginning-of-stream page with
// OpusHead, a comment page with OpusTags, one audio page per packet, and a
// terminal end-of-stream page on Close. Files produced this way play back in
// standard tools (VLC, FFmpeg, browsers).
//
// # Buffer ownership and partial reads
//
// ParsePage, ParseOpusHead, and ParseOpusTags copy the bytes they retain, so
// the caller may reuse or modify the input slice afterwards. Reader.ReadPacket
// likewise returns a freshly allocated slice the caller owns. The lower-level
// Page.Packets and Page.Payload, by contrast, are sub-slices of the page and
// share its backing array.
//
// # Error handling
//
// Parsing functions reject malformed input with the sentinel errors declared
// in this package (for example ErrInvalidPage, ErrBadCRC, ErrInvalidHeader)
// rather than panicking, and Reader.ReadPacket reports end of stream with
// io.EOF. All parse paths are bounds-checked and treat any byte slice as
// untrusted input.
package ogg
