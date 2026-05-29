package red_test

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/container/red"
)

// opusPT is the Opus RTP payload type used throughout these tests.
// RFC 7587 registers PT 111 as the conventional Opus payload type in WebRTC.
const opusPT = byte(111)

// ----------------------------------------------------------------------------
// RFC 2198 canonical wire-format vectors
//
// Each vector is constructed by hand from the bit-field layout in RFC 2198 §2:
//
//   redundant header (4 bytes, F=1):
//     byte 0: 0x80 | PT
//     byte 1: offset[13:6]
//     byte 2: offset[5:0]<<2 | length[9:8]
//     byte 3: length[7:0]
//
//   primary header (1 byte, F=0):
//     byte 0: PT
//
//   data region: redundant payloads (oldest first), then primary payload.

// makeRedundantHeader encodes one 4-byte redundant block header per RFC 2198.
func makeRedundantHeader(pt byte, offset, length int) []byte {
	return []byte{
		0x80 | (pt & 0x7f),
		byte(offset >> 6),
		byte((offset&0x3f)<<2) | byte(length>>8),
		byte(length),
	}
}

// TestParseRFC2198Vector_PrimaryOnly verifies a minimal RED packet that carries
// only a primary block (the degenerate case with no redundant data).
func TestParseRFC2198Vector_PrimaryOnly(t *testing.T) {
	// Wire: [PT] [primary_bytes...]
	primary := []byte{0xf8, 0xff, 0xfe}
	buf := append([]byte{opusPT}, primary...)

	gotPrimary, blocks, err := red.Parse(buf, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks=%d want 0", len(blocks))
	}
}

// TestParseRFC2198Vector_OneRedundantBlock verifies a RED packet with one
// redundant block followed by a primary block.
func TestParseRFC2198Vector_OneRedundantBlock(t *testing.T) {
	redPayload := []byte{0x11, 0x22} // 2-byte redundant Opus frame
	primary := []byte{0xaa, 0xbb, 0xcc}
	offset := 960 // 1 frame back at 48 kHz / 20 ms

	var buf []byte
	buf = append(buf, makeRedundantHeader(opusPT, offset, len(redPayload))...)
	buf = append(buf, opusPT)             // primary header
	buf = append(buf, redPayload...)      // data region: redundant first
	buf = append(buf, primary...)         // then primary

	gotPrimary, blocks, err := red.Parse(buf, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks=%d want 1", len(blocks))
	}
	if blocks[0].TimestampOffset != offset {
		t.Fatalf("offset=%d want %d", blocks[0].TimestampOffset, offset)
	}
	if blocks[0].PayloadType != opusPT {
		t.Fatalf("PT=%d want %d", blocks[0].PayloadType, opusPT)
	}
	if !bytes.Equal(blocks[0].Payload, redPayload) {
		t.Fatalf("payload=%x want %x", blocks[0].Payload, redPayload)
	}
}

// TestParseRFC2198Vector_TwoRedundantBlocks verifies the 2-redundant-block case
// that the example engine uses in its buildREDPayload round-trip tests.
func TestParseRFC2198Vector_TwoRedundantBlocks(t *testing.T) {
	red1 := []byte{0x33, 0x44, 0x55} // oldest redundant (2 frames back)
	red2 := []byte{0x11, 0x22}        // newer redundant (1 frame back)
	primary := []byte{0xaa, 0xbb, 0xcc}
	offset1 := 1920 // 2 frames * 960
	offset2 := 960  // 1 frame * 960

	// Wire order: oldest first.
	var buf []byte
	buf = append(buf, makeRedundantHeader(opusPT, offset1, len(red1))...)
	buf = append(buf, makeRedundantHeader(opusPT, offset2, len(red2))...)
	buf = append(buf, opusPT)   // primary header
	buf = append(buf, red1...)  // data region: oldest first
	buf = append(buf, red2...)
	buf = append(buf, primary...)

	gotPrimary, blocks, err := red.Parse(buf, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks=%d want 2", len(blocks))
	}
	if blocks[0].TimestampOffset != offset1 || !bytes.Equal(blocks[0].Payload, red1) {
		t.Fatalf("block[0]: offset=%d payload=%x want offset=%d payload=%x",
			blocks[0].TimestampOffset, blocks[0].Payload, offset1, red1)
	}
	if blocks[1].TimestampOffset != offset2 || !bytes.Equal(blocks[1].Payload, red2) {
		t.Fatalf("block[1]: offset=%d payload=%x want offset=%d payload=%x",
			blocks[1].TimestampOffset, blocks[1].Payload, offset2, red2)
	}
}

// TestParseRFC2198Vector_MaxDepthRedundantBlocks exercises the MaxDepth limit.
func TestParseRFC2198Vector_MaxDepthRedundantBlocks(t *testing.T) {
	const depth = red.MaxDepth
	primary := []byte{0xff}

	var buf []byte
	var dataRegion []byte
	for i := 0; i < depth; i++ {
		offset := (depth - i) * 960
		payload := []byte{byte(i)}
		buf = append(buf, makeRedundantHeader(opusPT, offset, len(payload))...)
		dataRegion = append(dataRegion, payload...)
	}
	buf = append(buf, opusPT)
	buf = append(buf, dataRegion...)
	buf = append(buf, primary...)

	_, blocks, err := red.Parse(buf, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(blocks) != depth {
		t.Fatalf("blocks=%d want %d", len(blocks), depth)
	}
}

// TestParseRejections covers all malformed-input paths from RFC 2198.
func TestParseRejections(t *testing.T) {
	tooManyHdrs := make([]byte, 0, (red.MaxDepth+1)*4+1)
	for i := 0; i < red.MaxDepth+1; i++ {
		tooManyHdrs = append(tooManyHdrs, makeRedundantHeader(opusPT, 960, 1)...)
	}
	tooManyHdrs = append(tooManyHdrs, opusPT, 0xaa)

	tests := []struct {
		name string
		buf  []byte
	}{
		{"empty", nil},
		{"truncated_redundant_header", []byte{0x80 | opusPT, 0x00}},
		{"zero_offset", append(makeRedundantHeader(opusPT, 0, 1), opusPT, 0xaa)},
		{"zero_length", append(makeRedundantHeader(opusPT, 960, 0), opusPT, 0xaa)},
		{"too_many_headers", tooManyHdrs},
		{"truncated_redundant_payload", append(makeRedundantHeader(opusPT, 960, 2), opusPT, 0xaa)},
		{"missing_primary_payload", []byte{opusPT}},
		{"wrong_primary_pt", []byte{0x60, 0x01}},
		{"wrong_redundant_pt", append(makeRedundantHeader(0x60, 960, 1), opusPT, 0xaa, 0xbb)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := red.Parse(tc.buf, opusPT)
			if err == nil {
				t.Fatalf("Parse accepted malformed payload %q", tc.name)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Build round-trip tests

// TestBuildRoundTrip_WithHistory verifies that Build + Parse is an identity
// for a two-block history case (matching the example engine test).
func TestBuildRoundTrip_WithHistory(t *testing.T) {
	history := []red.Frame{
		{Timestamp: 2000, Payload: []byte{0x11, 0x22}},
		{Timestamp: 1040, Payload: []byte{0x33, 0x44, 0x55}},
	}
	primary := []byte{0xaa, 0xbb, 0xcc}
	primaryTS := uint32(2960)

	payload, redundantBytes := red.Build(primary, primaryTS, history, 2, 960, opusPT)
	if redundantBytes != 5 { // 2 + 3 bytes of redundant payloads
		t.Fatalf("redundantBytes=%d want 5", redundantBytes)
	}

	gotPrimary, blocks, err := red.Parse(payload, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks=%d want 2", len(blocks))
	}
}

// TestBuildRoundTrip_PrimaryOnly verifies Build with no eligible history.
func TestBuildRoundTrip_PrimaryOnly(t *testing.T) {
	primary := []byte{0xf8, 0xff, 0xfe}
	payload, redundantBytes := red.Build(primary, 960, nil, 2, 960, opusPT)
	if redundantBytes != 0 {
		t.Fatalf("redundantBytes=%d want 0", redundantBytes)
	}

	gotPrimary, blocks, err := red.Parse(payload, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks=%d want 0", len(blocks))
	}
}

// TestBuildDepthZeroReturnsRawOpus verifies that depth=0 bypasses the RED
// envelope and returns the raw Opus payload unchanged.
func TestBuildDepthZeroReturnsRawOpus(t *testing.T) {
	primary := []byte{0x80, 0x01}
	payload, redundantBytes := red.Build(primary, 960, nil, 0, 960, opusPT)
	if redundantBytes != 0 {
		t.Fatalf("redundantBytes=%d want 0", redundantBytes)
	}
	if !bytes.Equal(payload, primary) {
		t.Fatalf("payload=%x want raw Opus %x", payload, primary)
	}
}

// TestBuildZeroFrameSamplesWrapsWithNoRedundant verifies that frameSamples=0
// produces a minimal RED envelope (primary header + primary payload).
func TestBuildZeroFrameSamplesWrapsWithNoRedundant(t *testing.T) {
	primary := []byte{0x80, 0x01}
	payload, redundantBytes := red.Build(primary, 960, nil, 1, 0, opusPT)
	if redundantBytes != 0 {
		t.Fatalf("redundantBytes=%d want 0", redundantBytes)
	}
	gotPrimary, blocks, err := red.Parse(payload, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !bytes.Equal(gotPrimary, primary) {
		t.Fatalf("primary=%x want %x", gotPrimary, primary)
	}
	if len(blocks) != 0 {
		t.Fatalf("blocks=%d want 0", len(blocks))
	}
}

// TestBuildEmptyPrimaryReturnsEmpty ensures an empty primary produces no output.
func TestBuildEmptyPrimaryReturnsEmpty(t *testing.T) {
	payload, redundantBytes := red.Build(nil, 960, nil, 2, 960, opusPT)
	if redundantBytes != 0 {
		t.Fatalf("redundantBytes=%d want 0", redundantBytes)
	}
	if len(payload) != 0 {
		t.Fatalf("payload=%x want empty", payload)
	}
}

// TestBuildTimestampWrap verifies that Build handles the uint32 timestamp
// wraparound correctly (modular subtraction).
func TestBuildTimestampWrap(t *testing.T) {
	primaryTS := uint32(480)
	history := []red.Frame{
		{Timestamp: primaryTS - 960, Payload: []byte{0x11, 0x22}},
	}
	payload, redundantBytes := red.Build([]byte{0xaa}, primaryTS, history, 1, 960, opusPT)
	if redundantBytes != len(history[0].Payload) {
		t.Fatalf("redundantBytes=%d want %d", redundantBytes, len(history[0].Payload))
	}
	gotPrimary, blocks, err := red.Parse(payload, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if !bytes.Equal(gotPrimary, []byte{0xaa}) {
		t.Fatalf("primary=%x want aa", gotPrimary)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks=%d want 1", len(blocks))
	}
}

// TestBuildDepthCap verifies that Build caps redundancy at MaxDepth even when
// the caller requests more.
func TestBuildDepthCap(t *testing.T) {
	history := make([]red.Frame, red.MaxDepth+3)
	for i := range history {
		offset := uint32((i + 1) * 960)
		history[i] = red.Frame{
			Timestamp: 10000 - offset,
			Payload:   []byte{byte(i)},
		}
	}
	payload, _ := red.Build([]byte{0xff}, 10000, history, red.MaxDepth+3, 960, opusPT)
	_, blocks, err := red.Parse(payload, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(blocks) > red.MaxDepth {
		t.Fatalf("blocks=%d want at most %d", len(blocks), red.MaxDepth)
	}
}

// TestBuildFiltersNonAlignedHistory verifies that history frames whose offset
// is not a multiple of frameSamples are excluded.
func TestBuildFiltersNonAlignedHistory(t *testing.T) {
	history := []red.Frame{
		// offset=900 is not a multiple of 960 — should be excluded
		{Timestamp: 10000 - 900, Payload: []byte{0xba}},
		// offset=960 is valid
		{Timestamp: 10000 - 960, Payload: []byte{0xab}},
	}
	payload, redundantBytes := red.Build([]byte{0xff}, 10000, history, 2, 960, opusPT)
	if redundantBytes != 1 {
		t.Fatalf("redundantBytes=%d want 1 (only aligned frame included)", redundantBytes)
	}
	_, blocks, err := red.Parse(payload, opusPT)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks=%d want 1", len(blocks))
	}
	if !bytes.Equal(blocks[0].Payload, []byte{0xab}) {
		t.Fatalf("block payload=%x want ab", blocks[0].Payload)
	}
}

// ----------------------------------------------------------------------------
// FindRecovery tests

// TestFindRecovery_Match verifies that FindRecovery returns the correct payload
// when currentTimestamp − missingTimestamp == lostAgo*frameSamples.
func TestFindRecovery_Match(t *testing.T) {
	history := []red.Frame{
		{Timestamp: 2000, Payload: []byte{0x11, 0x22}},
		{Timestamp: 1040, Payload: []byte{0x33, 0x44, 0x55}},
	}
	primary := []byte{0xaa, 0xbb, 0xcc}
	primaryTS := uint32(2960)

	payload, _ := red.Build(primary, primaryTS, history, 2, 960, opusPT)
	_, blocks, _ := red.Parse(payload, opusPT)

	// lostAgo=1 corresponds to history[0] (offset=960, ts=2000)
	got := red.FindRecovery(blocks, 1, 960, primaryTS, history[0].Timestamp)
	if !bytes.Equal(got, history[0].Payload) {
		t.Fatalf("lostAgo=1 payload=%x want %x", got, history[0].Payload)
	}

	// lostAgo=2 corresponds to history[1] (offset=1920, ts=1040)
	got = red.FindRecovery(blocks, 2, 960, primaryTS, history[1].Timestamp)
	if !bytes.Equal(got, history[1].Payload) {
		t.Fatalf("lostAgo=2 payload=%x want %x", got, history[1].Payload)
	}
}

// TestFindRecovery_NoMatch verifies nil return when no block matches.
func TestFindRecovery_NoMatch(t *testing.T) {
	blocks := []red.Block{
		{PayloadType: opusPT, TimestampOffset: 960, Payload: []byte{0xaa}},
	}
	// Wrong missingTimestamp — currentTS−missingTS ≠ 1*960
	got := red.FindRecovery(blocks, 1, 960, 2000, 500)
	if got != nil {
		t.Fatalf("expected nil, got %x", got)
	}
}

// TestFindRecovery_ZeroLostAgo verifies nil return for invalid lostAgo.
func TestFindRecovery_ZeroLostAgo(t *testing.T) {
	blocks := []red.Block{
		{PayloadType: opusPT, TimestampOffset: 960, Payload: []byte{0xaa}},
	}
	if got := red.FindRecovery(blocks, 0, 960, 2000, 1040); got != nil {
		t.Fatalf("expected nil for lostAgo=0, got %x", got)
	}
}

// TestFindRecovery_ZeroFrameSamples verifies nil return for invalid frameSamples.
func TestFindRecovery_ZeroFrameSamples(t *testing.T) {
	blocks := []red.Block{
		{PayloadType: opusPT, TimestampOffset: 960, Payload: []byte{0xaa}},
	}
	if got := red.FindRecovery(blocks, 1, 0, 2000, 1040); got != nil {
		t.Fatalf("expected nil for frameSamples=0, got %x", got)
	}
}

// TestFindRecovery_TimestampWrap verifies correct recovery when uint32
// timestamps wrap around zero.
func TestFindRecovery_TimestampWrap(t *testing.T) {
	const fs = 960
	primaryTS := uint32(480)
	missingTS := primaryTS - uint32(fs) // wraps around: 0xFFFFFC00 or similar

	blocks := []red.Block{
		{PayloadType: opusPT, TimestampOffset: fs, Payload: []byte{0xde, 0xad}},
	}
	got := red.FindRecovery(blocks, 1, fs, primaryTS, missingTS)
	if !bytes.Equal(got, []byte{0xde, 0xad}) {
		t.Fatalf("payload=%x want dead", got)
	}
}

// ----------------------------------------------------------------------------
// AppendHistory tests

// TestAppendHistory_Basic verifies that AppendHistory prepends and trims correctly.
func TestAppendHistory_Basic(t *testing.T) {
	var h []red.Frame
	h = red.AppendHistory(h, []byte{0x01}, 1000, 3)
	h = red.AppendHistory(h, []byte{0x02}, 2000, 3)
	h = red.AppendHistory(h, []byte{0x03}, 3000, 3)

	// h is newest-first: [3000, 2000, 1000]
	if len(h) != 3 {
		t.Fatalf("len(h)=%d want 3", len(h))
	}
	if h[0].Timestamp != 3000 || !bytes.Equal(h[0].Payload, []byte{0x03}) {
		t.Fatalf("h[0]={%d %x} want {3000 03}", h[0].Timestamp, h[0].Payload)
	}
	if h[2].Timestamp != 1000 || !bytes.Equal(h[2].Payload, []byte{0x01}) {
		t.Fatalf("h[2]={%d %x} want {1000 01}", h[2].Timestamp, h[2].Payload)
	}
}

// TestAppendHistory_Trim verifies the maxDepth trim.
func TestAppendHistory_Trim(t *testing.T) {
	var h []red.Frame
	for i := 0; i < red.MaxDepth+3; i++ {
		h = red.AppendHistory(h, []byte{byte(i)}, uint32(i*960), red.MaxDepth)
	}
	if len(h) > red.MaxDepth {
		t.Fatalf("len(h)=%d want at most %d", len(h), red.MaxDepth)
	}
}

// TestAppendHistory_EmptyPayloadNoOp verifies that an empty payload is a no-op.
func TestAppendHistory_EmptyPayloadNoOp(t *testing.T) {
	h := []red.Frame{{Timestamp: 1000, Payload: []byte{0x01}}}
	h2 := red.AppendHistory(h, nil, 2000, 5)
	if len(h2) != 1 || h2[0].Timestamp != 1000 {
		t.Fatalf("AppendHistory with empty payload modified history: %v", h2)
	}
}

// TestAppendHistory_CopiesPayload verifies that the stored payload is a copy,
// not an alias of the caller's buffer.
func TestAppendHistory_CopiesPayload(t *testing.T) {
	buf := []byte{0xaa, 0xbb}
	var h []red.Frame
	h = red.AppendHistory(h, buf, 1000, 5)
	buf[0] = 0xff
	if h[0].Payload[0] == 0xff {
		t.Fatal("AppendHistory stored a reference instead of a copy")
	}
}

// ----------------------------------------------------------------------------
// End-to-end recovery scenario (matches the documented RED→FEC→DRED→PLC order)

// TestEndToEnd_REDRecoveryScenario constructs an Opus loss scenario, builds
// the RED packet as a sender would, parses it as a receiver would, and
// verifies that FindRecovery can serve the lost frame.
func TestEndToEnd_REDRecoveryScenario(t *testing.T) {
	const fs = 960

	// Three consecutive frames.
	frames := []struct {
		ts      uint32
		payload []byte
	}{
		{ts: 1 * fs, payload: []byte{0xa1}},
		{ts: 2 * fs, payload: []byte{0xa2}}, // this frame will be "lost"
		{ts: 3 * fs, payload: []byte{0xa3}},
	}

	// Sender builds history after frame 0 and frame 1 are sent.
	var history []red.Frame
	history = red.AppendHistory(history, frames[0].payload, frames[0].ts, red.MaxDepth)
	history = red.AppendHistory(history, frames[1].payload, frames[1].ts, red.MaxDepth)

	// Frame 2 is sent with 2 redundant copies.
	wirePayload, redundantBytes := red.Build(frames[2].payload, frames[2].ts, history, 2, fs, opusPT)
	if redundantBytes == 0 {
		t.Fatal("expected redundant bytes in payload")
	}

	// Receiver: frame 1 was lost, frame 2 arrives.
	gotPrimary, blocks, err := red.Parse(wirePayload, opusPT)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !bytes.Equal(gotPrimary, frames[2].payload) {
		t.Fatalf("primary=%x want %x", gotPrimary, frames[2].payload)
	}

	// Recover the missing frame 1 (lostAgo=1 from frame 2's perspective).
	recovered := red.FindRecovery(blocks, 1, fs, frames[2].ts, frames[1].ts)
	if !bytes.Equal(recovered, frames[1].payload) {
		t.Fatalf("recovered=%x want %x", recovered, frames[1].payload)
	}

	// Frame 0 is also available for deeper recovery (lostAgo=2).
	recovered0 := red.FindRecovery(blocks, 2, fs, frames[2].ts, frames[0].ts)
	if !bytes.Equal(recovered0, frames[0].payload) {
		t.Fatalf("recovered0=%x want %x", recovered0, frames[0].payload)
	}
}
