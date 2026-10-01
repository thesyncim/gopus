// Package ogg reads and writes Ogg Opus streams.
//
// Reader parses OpusHead and OpusTags from the initial logical bitstream,
// verifies page framing and CRCs, and returns packets from that stream. Writer
// writes OpusHead and OpusTags immediately. Each audio packet starts on a fresh
// page and spans continuation pages as needed. Close emits a packetless
// end-of-stream page; it does not flush
// or close the underlying writer. Page, OpusHead, and OpusTags provide lower-
// level container primitives; codec encoding and decoding remain in package
// gopus.
//
// ParsePage and the header parsers return data independent of their input.
// Page.Packets returns slices that alias Page.Payload. Reader.ReadPacket returns
// an independently owned packet copy, while Reader.ReadPacketInto writes into
// the caller's buffer.
package ogg
