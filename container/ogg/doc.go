// Package ogg implements Ogg Opus container reading and writing.
//
// It provides low-level page, packet, OpusHead, and OpusTags primitives for
// RFC 7845 streams on top of the Ogg framing rules from RFC 3533. The package
// verifies Ogg CRCs, preserves packet boundaries across lacing segments, and
// leaves codec encode/decode work to the top-level gopus API.
package ogg
