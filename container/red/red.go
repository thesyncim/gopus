// Package red implements RFC 2198 redundancy for RTP payloads.
//
// A RED payload carries a primary packet and may include older packets as
// redundant blocks. Parse and Build provide stateless helpers; Decoder and
// Encoder reuse block/output storage and maintain packet history for stream
// processing. Payload-type arguments are 7-bit RTP payload type values.
package red

import "errors"

// MaxDepth is the maximum number of redundant blocks supported per packet.
// Values above this are rejected by Parse and clamped by Build.
const MaxDepth = 5

// Internal sentinel errors returned by Parse and ParseInto. Reusing these
// package-private values avoids constructing a new error on each malformed
// packet; external callers cannot match them by name with errors.Is.
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

	// Payload aliases the buffer passed to Parse or ParseInto. It remains valid
	// until that buffer is modified; copy it before reusing the input storage.
	Payload []byte
}

// Frame is a single send-side history entry used when building RED packets.
type Frame struct {
	// Timestamp is the RTP timestamp of this frame.
	Timestamp uint32

	// Payload is the encoded Opus payload for this frame. BuildAppend reads it
	// without copying it into history; AppendHistory stores its own copy.
	Payload []byte
}

// ParseInto parses a RED payload into dst[:0] and returns the primary payload
// and redundant blocks in wire order. primaryPayloadType must be in 0..127, and
// every block header must carry that payload type. It reuses dst when its
// capacity is sufficient; the returned blocks slice aliases dst's backing
// array. The primary and block payloads alias buf; copy them if they must outlive
// or remain independent of the input. It returns an error for empty or
// truncated input, invalid blocks, unexpected payload types, a missing primary
// payload, or more than MaxDepth blocks.
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

// Parse calls ParseInto without a reusable block slice. It allocates block
// storage when the packet contains redundant blocks; ParseInto can reuse a
// caller-supplied slice for allocation-free parsing.
func Parse(buf []byte, primaryPayloadType byte) (primary []byte, blocks []Block, err error) {
	return ParseInto(buf, primaryPayloadType, nil)
}

// BuildAppend writes a RED payload to dst[:0], returning output backed by dst
// when its capacity is sufficient and the total redundant payload bytes (not
// header bytes). history must be ordered newest-first, as returned by
// AppendHistory; depth is clamped to MaxDepth. primaryPayloadType must be in
// 0..127. frameSamples is the RTP timestamp increment per Opus frame; eligible
// history entries must be an exact multiple of that interval, have a 14-bit
// timestamp offset, and have a nonempty payload that fits the 10-bit block-
// length field. A nil or undersized dst may allocate. An empty primary produces
// an empty result. A nonpositive depth copies primary without a RED header;
// nonpositive frameSamples emits only a primary header and payload, with no
// redundant blocks.
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
		if offset <= 0 || offset > 0x3fff || len(f.Payload) == 0 || len(f.Payload) > 0x3ff {
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

// Build calls BuildAppend without a reusable destination. It allocates output
// storage for nonempty results; use BuildAppend to reuse a caller-supplied
// buffer.
func Build(primary []byte, primaryTimestamp uint32, history []Frame, depth, frameSamples int, primaryPayloadType byte) ([]byte, int) {
	return BuildAppend(nil, primary, primaryTimestamp, history, depth, frameSamples, primaryPayloadType)
}

// FindRecovery returns the redundant payload for the missing timestamp, if it
// is present in blocks. lostAgo is the number of frames between the current and
// missing packets; frameSamples is the RTP timestamp increment per frame. RTP
// timestamp subtraction uses uint32 wraparound. The returned slice aliases the
// matching Block.Payload; it returns nil when the timestamp difference does not
// match or no block covers that offset.
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

// AppendHistory copies payload into the front of history, trims the result to
// maxDepth entries, and returns the updated slice. When history is full, it
// reuses the evicted payload buffer if its capacity is sufficient; otherwise it
// allocates a new buffer. A Frame.Payload remains valid only while that frame is
// retained. Empty payloads and nonpositive maxDepth leave history unchanged.
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

// Decoder parses RFC 2198 RED packets, reusing an internal block slice. After a
// parse has grown that slice to the needed capacity, later parses with no more
// blocks allocate no block storage. It is the stateful counterpart to Parse and
// is not safe for concurrent use.
type Decoder struct {
	pt     byte
	blocks []Block
}

// NewDecoder returns a Decoder for packets whose primary and redundant blocks
// carry primaryPayloadType.
func NewDecoder(primaryPayloadType byte) *Decoder {
	return &Decoder{pt: primaryPayloadType}
}

// Parse decodes one RED payload into the primary Opus payload and redundant
// blocks (oldest-first). The primary and Block payloads alias buf; the blocks
// slice aliases the Decoder's reused storage and is overwritten by a later Parse
// call. Copy any data that must remain independent. Parsing reuses storage after
// it has enough capacity for the packet's blocks.
func (d *Decoder) Parse(buf []byte) (primary []byte, blocks []Block, err error) {
	primary, blocks, err = ParseInto(buf, d.pt, d.blocks[:0])
	if err == nil {
		d.blocks = blocks
	}
	return primary, blocks, err
}

// Encoder builds RFC 2198 RED packets and owns its frame history and reusable
// output buffer. After those buffers have enough capacity for subsequent packet
// and history sizes, Encode reuses them without allocating. It is the stateful
// counterpart to Build with managed history and is not safe for concurrent use.
type Encoder struct {
	pt           byte
	frameSamples int
	depth        int
	history      []Frame
	out          []byte
}

// NewEncoder returns an Encoder that emits up to depth redundant blocks per
// packet, each an exact multiple of positive frameSamples RTP ticks older than
// the primary, using the 7-bit primaryPayloadType for every block. depth above
// MaxDepth is clamped; nonpositive depth emits no redundant blocks.
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

// Reset clears the redundant-frame history, for example after an RTP timestamp
// discontinuity. The payload type and timing stay configured; a packet returned
// by an earlier Encode remains valid until the next Encode reuses the output
// buffer.
func (e *Encoder) Reset() {
	e.history = e.history[:0]
}
