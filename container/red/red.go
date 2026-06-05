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
// Encoder and Decoder are the high-level, allocation-free API; they own their
// buffers and the redundant-frame history so callers do not manage them. Parse,
// Build, AppendHistory and FindRecovery remain as the underlying stateless
// primitives.
//
// Typical send path:
//
//	enc := red.NewEncoder(opusPT, frameSamples, depth)
//	for each frame:
//	    payload, _ := enc.Encode(primary, timestamp)
//	    send(payload) // valid until the next Encode
//
// Typical receive path:
//
//	dec := red.NewDecoder(opusPT)
//	primary, blocks, err := dec.Parse(pkt.Payload)
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

// Sentinel errors returned by Parse/ParseInto. They are package-level so the
// error paths allocate nothing and callers can match with errors.Is.
var (
	errEmptyPayload        = errors.New("red: empty payload")
	errTruncatedHeader     = errors.New("red: truncated header")
	errTruncatedRedHeader  = errors.New("red: truncated redundant header")
	errInvalidRedBlock     = errors.New("red: invalid redundant block: zero offset or zero length")
	errUnexpectedPrimaryPT = errors.New("red: unexpected primary payload type")
	errUnexpectedRedPT     = errors.New("red: unexpected redundant payload type")
	errTooManyBlocks       = errors.New("red: too many redundant blocks")
	errTruncatedRedPayload = errors.New("red: truncated redundant payload")
	errMissingPrimary      = errors.New("red: missing primary payload")
)

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

// ParseInto decodes an RFC 2198 RED payload, appending the redundant blocks
// into dst[:0] and returning the primary Opus payload alongside them. Reusing a
// dst slice with capacity >= the block count (at most MaxDepth) across packets
// makes parsing allocation-free; passing a nil dst allocates a fresh slice.
//
// blocks are ordered oldest-first (the order they appear on the wire).
// primaryPayloadType is the expected RTP payload type for both the primary and
// all redundant blocks (e.g. 111 for Opus in WebRTC). ParseInto returns one of
// the package sentinel errors for any condition defined in RFC 2198:
//
//   - empty input
//   - truncated header or payload region
//   - a redundant block with zero timestamp offset or zero length
//   - more than MaxDepth redundant blocks
//   - a payload type that does not match primaryPayloadType
//
// ParseInto does not copy payload bytes; the returned primary slice and Block
// Payload fields reference the input buf directly.
func ParseInto(buf []byte, primaryPayloadType byte, dst []Block) (primary []byte, blocks []Block, err error) {
	if len(buf) == 0 {
		return nil, nil, errEmptyPayload
	}

	type hdr struct {
		payloadType     byte
		timestampOffset int
		length          int
	}
	var hdrs [MaxDepth]hdr
	n := 0

	pos := 0
	for {
		if pos >= len(buf) {
			return nil, nil, errTruncatedHeader
		}
		b := buf[pos]
		if b&0x80 == 0 {
			// Primary block header: F bit is 0.
			if b&0x7f != primaryPayloadType {
				return nil, nil, errUnexpectedPrimaryPT
			}
			pos++
			break
		}
		// Redundant block header: 4 bytes.
		if pos+4 > len(buf) {
			return nil, nil, errTruncatedRedHeader
		}
		offset := int(buf[pos+1])<<6 | int(buf[pos+2]>>2)
		length := int(buf[pos+2]&0x03)<<8 | int(buf[pos+3])
		if offset == 0 || length == 0 {
			return nil, nil, errInvalidRedBlock
		}
		pt := b & 0x7f
		if pt != primaryPayloadType {
			return nil, nil, errUnexpectedRedPT
		}
		if n == MaxDepth {
			return nil, nil, errTooManyBlocks
		}
		hdrs[n] = hdr{payloadType: pt, timestampOffset: offset, length: length}
		n++
		pos += 4
	}

	blocks = dst[:0]
	for i := 0; i < n; i++ {
		h := hdrs[i]
		if pos+h.length > len(buf) {
			return nil, nil, errTruncatedRedPayload
		}
		blocks = append(blocks, Block{
			PayloadType:     h.payloadType,
			TimestampOffset: h.timestampOffset,
			Payload:         buf[pos : pos+h.length],
		})
		pos += h.length
	}

	if pos >= len(buf) {
		return nil, nil, errMissingPrimary
	}
	return buf[pos:], blocks, nil
}

// Parse is ParseInto with a freshly allocated block slice. See ParseInto for the
// allocation-free variant that reuses a caller-supplied slice.
func Parse(buf []byte, primaryPayloadType byte) (primary []byte, blocks []Block, err error) {
	return ParseInto(buf, primaryPayloadType, nil)
}

// BuildAppend constructs an RFC 2198 RED payload into dst[:0], returning the
// encoded payload and the total number of redundant payload bytes included.
// Reusing a dst buffer across calls makes building allocation-free; passing a
// nil dst allocates a fresh buffer.
//
// The payload contains the primary Opus payload and up to depth redundant copies
// drawn from history, which must be ordered newest-first (as returned by
// AppendHistory). frameSamples is the number of RTP timestamp ticks per Opus
// frame (960 for 20 ms at 48 kHz). Only history entries whose timestamp
// difference from primaryTimestamp is an exact multiple of frameSamples and fits
// in 14 bits are eligible as redundant blocks.
//
// Special cases:
//   - If primary is empty, the result is empty with 0 redundant bytes.
//   - If depth ≤ 0, the result is a raw copy of primary (no RED envelope).
//   - If frameSamples ≤ 0, primary is wrapped in a minimal RED envelope with no
//     redundant blocks (one primary header + payload).
func BuildAppend(dst []byte, primary []byte, primaryTimestamp uint32, history []Frame, depth, frameSamples int, primaryPayloadType byte) (out []byte, redundantBytes int) {
	if len(primary) == 0 || depth <= 0 {
		return append(dst[:0], primary...), 0
	}
	if frameSamples <= 0 {
		out = append(dst[:0], primaryPayloadType)
		return append(out, primary...), 0
	}
	if depth > MaxDepth {
		depth = MaxDepth
	}

	type candidate struct {
		timestampOffset int
		payload         []byte
	}
	var cands [MaxDepth]candidate
	nc := 0
	for _, f := range history {
		if nc == depth {
			break
		}
		offset := int(primaryTimestamp - f.Timestamp)
		if offset <= 0 || offset > 0x3fff || len(f.Payload) > 0x3ff {
			continue
		}
		if offset%frameSamples != 0 {
			continue
		}
		cands[nc] = candidate{timestampOffset: offset, payload: f.Payload}
		nc++
	}

	if nc == 0 {
		out = append(dst[:0], primaryPayloadType)
		return append(out, primary...), 0
	}

	// Wire order is oldest redundant first; history is newest-first, so reverse.
	for i, j := 0, nc-1; i < j; i, j = i+1, j-1 {
		cands[i], cands[j] = cands[j], cands[i]
	}

	out = dst[:0]
	for i := 0; i < nc; i++ {
		blen := len(cands[i].payload)
		off := cands[i].timestampOffset
		out = append(out,
			0x80|(primaryPayloadType&0x7f),
			byte(off>>6),
			byte((off&0x3f)<<2)|byte(blen>>8),
			byte(blen),
		)
		redundantBytes += blen
	}
	out = append(out, primaryPayloadType)
	for i := 0; i < nc; i++ {
		out = append(out, cands[i].payload...)
	}
	return append(out, primary...), redundantBytes
}

// Build is BuildAppend with a freshly allocated buffer. See BuildAppend for the
// allocation-free variant that reuses a caller-supplied buffer.
func Build(primary []byte, primaryTimestamp uint32, history []Frame, depth, frameSamples int, primaryPayloadType byte) ([]byte, int) {
	return BuildAppend(nil, primary, primaryTimestamp, history, depth, frameSamples, primaryPayloadType)
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

// AppendHistory inserts payload as the newest (front) Frame of history, trims to
// at most maxDepth entries, and returns the updated slice — store it as your new
// history. The input payload is copied so the caller may reuse its buffer.
//
// To stay allocation-free once the window is full, AppendHistory shifts the
// slice in place and recycles the payload buffer of the entry it evicts. A
// Frame's Payload is therefore only valid while that Frame remains within the
// maxDepth window; do not retain it after it falls out. If payload is empty or
// maxDepth <= 0, history is returned unchanged.
func AppendHistory(history []Frame, payload []byte, timestamp uint32, maxDepth int) []Frame {
	if len(payload) == 0 || maxDepth <= 0 {
		return history
	}

	// Recycle the evicted entry's payload buffer when the window is full,
	// otherwise add a slot (the only growth point).
	var buf []byte
	if len(history) >= maxDepth {
		buf = history[len(history)-1].Payload
		history = history[:maxDepth]
	} else {
		history = append(history, Frame{})
	}

	copy(history[1:], history[:len(history)-1]) // shift older entries back

	if cap(buf) >= len(payload) {
		buf = buf[:len(payload)]
	} else {
		buf = make([]byte, len(payload))
	}
	copy(buf, payload)
	history[0] = Frame{Timestamp: timestamp, Payload: buf}
	return history
}

// Decoder parses a stream of RFC 2198 RED packets, reusing an internal block
// slice so that steady-state parsing allocates nothing. It is the stateful,
// allocation-free counterpart to Parse. A Decoder is not safe for concurrent use.
type Decoder struct {
	pt     byte
	blocks []Block
}

// NewDecoder returns a Decoder for packets whose primary and redundant blocks
// carry primaryPayloadType.
func NewDecoder(primaryPayloadType byte) *Decoder {
	return &Decoder{pt: primaryPayloadType}
}

// Parse decodes one RED payload into the primary Opus payload and the redundant
// blocks (oldest-first). The returned primary and Block payloads alias buf, and
// the blocks slice aliases the Decoder's reused buffer; all are valid only until
// the next Parse call or until buf is modified. It does not allocate once warm.
func (d *Decoder) Parse(buf []byte) (primary []byte, blocks []Block, err error) {
	primary, d.blocks, err = ParseInto(buf, d.pt, d.blocks[:0])
	return primary, d.blocks, err
}

// Encoder builds a stream of RFC 2198 RED packets, owning both the redundant
// frame history and the output buffer so that steady-state building allocates
// nothing and the caller manages neither. It is the stateful counterpart to
// Build plus a managed history. An Encoder is not safe for concurrent use.
type Encoder struct {
	pt           byte
	frameSamples int
	depth        int
	history      []Frame
	out          []byte
}

// NewEncoder returns an Encoder that emits up to depth redundant blocks per
// packet — each an exact multiple of frameSamples RTP ticks older than the
// primary — using primaryPayloadType for every block. depth is clamped to
// MaxDepth.
func NewEncoder(primaryPayloadType byte, frameSamples, depth int) *Encoder {
	if depth > MaxDepth {
		depth = MaxDepth
	}
	return &Encoder{pt: primaryPayloadType, frameSamples: frameSamples, depth: depth}
}

// Encode builds the RED packet carrying primary at the given RTP timestamp plus
// the eligible recent frames, then records primary in the history for later
// packets. The returned payload aliases the Encoder's reused buffer and is valid
// only until the next Encode call; redundantBytes is the redundant payload total.
// primary is copied into the history, so it never escapes and the caller may
// reuse its buffer immediately.
func (e *Encoder) Encode(primary []byte, timestamp uint32) (payload []byte, redundantBytes int) {
	e.out, redundantBytes = BuildAppend(e.out[:0], primary, timestamp, e.history, e.depth, e.frameSamples, e.pt)
	e.history = AppendHistory(e.history, primary, timestamp, e.depth)
	return e.out, redundantBytes
}

// Reset clears the redundant-frame history, e.g. after an RTP discontinuity. The
// payload type and timing stay configured.
func (e *Encoder) Reset() {
	e.history = e.history[:0]
}
