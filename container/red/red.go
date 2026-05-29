// Package red implements RFC 2198 RTP Payload for Redundant Audio Data.
//
// RED wraps one or more Opus payloads in a single RTP packet. The primary
// block carries the most recent frame; zero or more redundant blocks carry
// earlier frames whose original RTP packets may have been lost. A receiver
// that detects a gap in the sequence can recover missing frames directly from
// the redundant blocks, avoiding PLC or FEC decode overhead.
//
// Wire format (RFC 2198 §2):
//
//	+-------+-------+-------+-------+
//	|F|  PT |     timestamp offset  |
//	+-------+-------+-------+-------+
//	|             length            |
//	+-------+-------+-------+-------+
//	     ... repeated for each redundant block ...
//	+-------+-------+-------+-------+
//	|0|  PT |   (primary block)     |
//	+-------+-------+-------+-------+
//	     redundant payloads in order, then primary payload
//
// The F bit (0x80) is set for every header except the last (primary) one.
// Timestamp offsets are unsigned, measured backwards from the primary
// timestamp (i.e. primary_ts − redundant_ts). Block lengths are 10-bit
// values fitting in two bytes together with the lower 2 bits of the offset.
//
// Typical usage for a send path:
//
//	var history []red.Frame
//	for each frame:
//	    payload, _ := red.Build(primary, primaryTS, history, depth, frameSamples)
//	    history = red.AppendHistory(history, primary, primaryTS, maxDepth)
//
// Typical usage for a receive path:
//
//	primary, blocks, err := red.Parse(pkt.Payload, opusPT)
//	for lostAgo := missing; lostAgo >= 1; lostAgo-- {
//	    if b := red.FindRecovery(blocks, lostAgo, frameSamples, pkt.Timestamp, missingTS); b != nil {
//	        // decode b as Opus
//	    }
//	}
package red

import "errors"

// MaxDepth is the maximum number of redundant blocks supported per packet.
// Values above this are rejected by Parse and clamped by Build.
const MaxDepth = 5

// Block is a single redundant entry parsed from a RED payload.
// The primary block is not represented as a Block; it is returned separately
// by Parse.
type Block struct {
	// PayloadType is the RTP payload type for this block (7-bit, 0–127).
	PayloadType byte

	// TimestampOffset is primary_timestamp − block_timestamp.
	// It is always positive and at most 0x3FFF (14 bits).
	TimestampOffset int

	// Payload is a sub-slice of the buffer passed to Parse; it is valid until
	// that buffer is modified. Callers that need to retain it beyond the next
	// Parse call must copy it.
	Payload []byte
}

// Frame is a single send-side history entry used when building RED packets.
type Frame struct {
	// Timestamp is the RTP timestamp of this frame.
	Timestamp uint32

	// Payload is the encoded Opus payload for this frame.
	Payload []byte
}

// Parse decodes an RFC 2198 RED payload and returns the primary Opus payload
// and any redundant blocks ordered oldest-first (the order they appear on the
// wire). primaryPayloadType is the expected RTP payload type for both the
// primary and all redundant blocks (e.g. 111 for Opus in WebRTC). Parse
// returns an error for any of the following conditions defined in RFC 2198:
//
//   - empty input
//   - truncated header or payload region
//   - a redundant block with zero timestamp offset or zero length
//   - more than MaxDepth redundant blocks
//   - a payload type that does not match primaryPayloadType
//
// Parse does not copy payload bytes; the returned primary slice and Block
// Payload fields reference the input buf directly.
func Parse(buf []byte, primaryPayloadType byte) (primary []byte, blocks []Block, err error) {
	if len(buf) == 0 {
		return nil, nil, errors.New("red: empty payload")
	}

	type hdr struct {
		payloadType     byte
		timestampOffset int
		length          int
	}
	hdrs := make([]hdr, 0, MaxDepth)

	pos := 0
	for {
		if pos >= len(buf) {
			return nil, nil, errors.New("red: truncated header")
		}
		b := buf[pos]
		if b&0x80 == 0 {
			// Primary block header: F bit is 0.
			if b&0x7f != primaryPayloadType {
				return nil, nil, errors.New("red: unexpected primary payload type")
			}
			pos++
			break
		}
		// Redundant block header: 4 bytes.
		if pos+4 > len(buf) {
			return nil, nil, errors.New("red: truncated redundant header")
		}
		offset := int(buf[pos+1])<<6 | int(buf[pos+2]>>2)
		length := int(buf[pos+2]&0x03)<<8 | int(buf[pos+3])
		if offset == 0 || length == 0 {
			return nil, nil, errors.New("red: invalid redundant block: zero offset or zero length")
		}
		pt := b & 0x7f
		if pt != primaryPayloadType {
			return nil, nil, errors.New("red: unexpected redundant payload type")
		}
		if len(hdrs) == MaxDepth {
			return nil, nil, errors.New("red: too many redundant blocks")
		}
		hdrs = append(hdrs, hdr{payloadType: pt, timestampOffset: offset, length: length})
		pos += 4
	}

	blocks = make([]Block, 0, len(hdrs))
	for _, h := range hdrs {
		if pos+h.length > len(buf) {
			return nil, nil, errors.New("red: truncated redundant payload")
		}
		blocks = append(blocks, Block{
			PayloadType:     h.payloadType,
			TimestampOffset: h.timestampOffset,
			Payload:         buf[pos : pos+h.length],
		})
		pos += h.length
	}

	if pos >= len(buf) {
		return nil, nil, errors.New("red: missing primary payload")
	}
	return buf[pos:], blocks, nil
}

// Build constructs an RFC 2198 RED payload containing the primary Opus payload
// and up to depth redundant copies drawn from history. history must be ordered
// newest-first (as returned by AppendHistory).
//
// frameSamples is the number of RTP timestamp ticks per Opus frame (960 for
// 20 ms at 48 kHz). Only history entries whose timestamp difference from
// primaryTimestamp is an exact multiple of frameSamples and fits in 14 bits
// are eligible as redundant blocks.
//
// Build returns the encoded RED payload and the total number of redundant
// payload bytes included (useful for statistics). The second return value is
// zero when no redundant data was included.
//
// Special cases:
//   - If primary is empty, Build returns an empty slice with 0 redundant bytes.
//   - If depth ≤ 0, Build returns a raw Opus payload (no RED envelope).
//   - If frameSamples ≤ 0, Build wraps primary in a minimal RED envelope with
//     no redundant blocks (one primary header + payload).
func Build(primary []byte, primaryTimestamp uint32, history []Frame, depth, frameSamples int, primaryPayloadType byte) ([]byte, int) {
	if len(primary) == 0 {
		return append([]byte(nil), primary...), 0
	}
	if depth <= 0 {
		return append([]byte(nil), primary...), 0
	}
	if frameSamples <= 0 {
		out := make([]byte, 1+len(primary))
		out[0] = primaryPayloadType
		copy(out[1:], primary)
		return out, 0
	}
	if depth > MaxDepth {
		depth = MaxDepth
	}

	type candidate struct {
		timestampOffset int
		payload         []byte
	}
	candidates := make([]candidate, 0, depth)
	for _, f := range history {
		if len(candidates) == depth {
			break
		}
		offset := int(primaryTimestamp - f.Timestamp)
		if offset <= 0 || offset > 0x3fff || len(f.Payload) > 0x3ff {
			continue
		}
		if offset%frameSamples != 0 {
			continue
		}
		candidates = append(candidates, candidate{timestampOffset: offset, payload: f.Payload})
	}

	if len(candidates) == 0 {
		out := make([]byte, 1+len(primary))
		out[0] = primaryPayloadType
		copy(out[1:], primary)
		return out, 0
	}

	// Wire order is oldest redundant first, so reverse the candidate slice
	// (which is currently newest-first from history).
	for i, j := 0, len(candidates)-1; i < j; i, j = i+1, j-1 {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	}

	headerLen := len(candidates)*4 + 1
	payloadLen := len(primary)
	for _, c := range candidates {
		payloadLen += len(c.payload)
	}
	out := make([]byte, headerLen+payloadLen)

	pos := 0
	redundantBytes := 0
	for _, c := range candidates {
		blen := len(c.payload)
		off := c.timestampOffset
		out[pos] = 0x80 | (primaryPayloadType & 0x7f)
		out[pos+1] = byte(off >> 6)
		out[pos+2] = byte((off&0x3f)<<2) | byte(blen>>8)
		out[pos+3] = byte(blen)
		pos += 4
		redundantBytes += blen
	}
	out[pos] = primaryPayloadType
	pos++
	for _, c := range candidates {
		copy(out[pos:], c.payload)
		pos += len(c.payload)
	}
	copy(out[pos:], primary)
	return out, redundantBytes
}

// FindRecovery searches blocks for a redundant entry whose timestamp matches
// the given missingTimestamp. lostAgo is the number of frames back the missing
// packet is relative to the packet that carried blocks (e.g. 1 means it is the
// immediately preceding frame). frameSamples is the RTP timestamp increment per
// frame.
//
// The expected timestamp offset is lostAgo*frameSamples. If currentTimestamp −
// missingTimestamp does not equal that value, or no block carries a matching
// offset, FindRecovery returns nil.
func FindRecovery(blocks []Block, lostAgo, frameSamples int, currentTimestamp, missingTimestamp uint32) []byte {
	if lostAgo <= 0 || frameSamples <= 0 {
		return nil
	}
	wantOffset := lostAgo * frameSamples
	if int(currentTimestamp-missingTimestamp) != wantOffset {
		return nil
	}
	for _, b := range blocks {
		if b.TimestampOffset == wantOffset {
			return b.Payload
		}
	}
	return nil
}

// AppendHistory prepends a new Frame to history and trims the slice to at most
// maxDepth entries. Callers should store the returned slice as their new
// history. The payload bytes are copied so the caller may reuse its buffer.
//
// If payload is empty, history is returned unchanged.
func AppendHistory(history []Frame, payload []byte, timestamp uint32, maxDepth int) []Frame {
	if len(payload) == 0 {
		return history
	}
	f := Frame{
		Timestamp: timestamp,
		Payload:   append([]byte(nil), payload...),
	}
	history = append([]Frame{f}, history...)
	if len(history) > maxDepth {
		history = history[:maxDepth]
	}
	return history
}
