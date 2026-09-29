// Package ogg reads and writes Ogg Opus streams.
//
// Reader parses OpusHead and OpusTags and returns Opus packets. Writer writes
// packets with Ogg page framing and CRC checksums. Page, OpusHead, and OpusTags
// provide lower-level container primitives; codec encoding and decoding remain
// in package gopus.
//
// ParsePage and the header parsers copy data they retain. Page.Packets returns
// slices that alias the page payload, and ReadPacketInto reuses the buffer
// supplied by the caller.
package ogg
