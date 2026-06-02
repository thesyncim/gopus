package red_test

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/container/red"
)

// buildREDPayload constructs an RFC 2198 RED wire payload with the given
// redundant blocks and primary, using the opusPT payload type.  It does not
// use red.Build so that seed-generation is independent of the code under test.
func buildREDPayload(redundant []redSeedBlock, primary []byte) []byte {
	var out []byte
	for _, r := range redundant {
		out = append(out, 0x80|(opusPT&0x7f))
		out = append(out, byte(r.offset>>6))
		out = append(out, byte((r.offset&0x3f)<<2)|byte(len(r.payload)>>8))
		out = append(out, byte(len(r.payload)))
	}
	out = append(out, opusPT)
	for _, r := range redundant {
		out = append(out, r.payload...)
	}
	out = append(out, primary...)
	return out
}

type redSeedBlock struct {
	offset  int
	payload []byte
}

// FuzzREDParse exercises red.Parse against arbitrary wire payloads.
// It asserts:
//   - Parse never panics.
//   - If Parse succeeds the returned primary slice is non-empty.
//   - If Parse succeeds on output from red.Build, a subsequent Parse on that
//     same output agrees (idempotency).
func FuzzREDParse(f *testing.F) {
	// Seed: primary-only (degenerate RED envelope).
	f.Add(append([]byte{opusPT}, 0xF8, 0xFF, 0xFE))

	// Seed: one redundant block + primary.
	f.Add(buildREDPayload(
		[]redSeedBlock{{offset: 960, payload: []byte{0x11, 0x22}}},
		[]byte{0xF8, 0xFF},
	))

	// Seed: two redundant blocks + primary (oldest first).
	f.Add(buildREDPayload(
		[]redSeedBlock{
			{offset: 1920, payload: []byte{0x33, 0x44, 0x55}},
			{offset: 960, payload: []byte{0x11, 0x22}},
		},
		[]byte{0xF8, 0xFF, 0xFE},
	))

	// Seed: MaxDepth redundant blocks.
	{
		blocks := make([]redSeedBlock, red.MaxDepth)
		for i := range blocks {
			blocks[i] = redSeedBlock{
				offset:  (red.MaxDepth - i) * 960,
				payload: []byte{byte(i)},
			}
		}
		f.Add(buildREDPayload(blocks, []byte{0xAA}))
	}

	// Seed: zero-length input.
	f.Add([]byte{})

	// Seed: just the primary header byte, no payload → missing primary payload.
	f.Add([]byte{opusPT})

	// Seed: truncated redundant header (3 bytes when 4 are needed).
	f.Add([]byte{0x80 | opusPT, 0x00, 0x04})

	// Seed: redundant block header with zero offset → rejected.
	{
		// offset bits: byte1[7:0]=0, byte2[7:2]=0 → offset=0; length=1
		f.Add([]byte{0x80 | opusPT, 0x00, 0x00, 0x01, opusPT, 0xAA, 0xBB})
	}

	// Seed: redundant block header with zero length → rejected.
	{
		// offset=960, length=0: offset>>6=15, (offset&0x3f)<<2=0, length=0
		f.Add([]byte{0x80 | opusPT, 0x0F, 0x00, 0x00, opusPT, 0xAA})
	}

	// Seed: MaxDepth+1 redundant headers → too many.
	{
		var buf []byte
		for i := 0; i <= red.MaxDepth; i++ {
			buf = append(buf, 0x80|opusPT, 0x0F, 0x04, 0x01) // offset=960, len=1
		}
		buf = append(buf, opusPT)
		for i := 0; i <= red.MaxDepth; i++ {
			buf = append(buf, 0xAA)
		}
		buf = append(buf, 0xBB)
		f.Add(buf)
	}

	// Seed: redundant payload truncated.
	{
		// Header says length=5 but data region only has 2 bytes for the block.
		buf := []byte{0x80 | opusPT, 0x0F, 0x00, 0x05, opusPT, 0x11, 0x22}
		f.Add(buf)
	}

	// Seed: wrong primary payload type.
	f.Add([]byte{opusPT ^ 0x10, 0xAA})

	// Seed: wrong redundant payload type.
	f.Add([]byte{0x80 | (opusPT ^ 0x10), 0x0F, 0x04, 0x01, opusPT, 0xBB, 0xCC})

	// Seed: valid Build output for several depths.
	primary := []byte{0xF8, 0xFF, 0xFE, 0x01, 0x02}
	history := []red.Frame{
		{Timestamp: 2000, Payload: []byte{0x10, 0x20}},
		{Timestamp: 1040, Payload: []byte{0x30, 0x40, 0x50}},
	}
	for depth := 0; depth <= red.MaxDepth; depth++ {
		payload, _ := red.Build(primary, 2960, history, depth, 960, opusPT)
		if len(payload) > 0 {
			f.Add(payload)
		}
	}

	// Seed: timestamp wrap-around payload (uint32 underflow wraps modulo 2^32).
	{
		primaryTS := uint32(480)
		wrapTS := primaryTS - uint32(960) // wraps to 0xFFFFFC80
		hist := []red.Frame{{Timestamp: wrapTS, Payload: []byte{0xDE, 0xAD}}}
		payload, _ := red.Build([]byte{0xBE, 0xEF}, primaryTS, hist, 1, 960, opusPT)
		if len(payload) > 0 {
			f.Add(payload)
		}
	}

	// Seed: 14-bit maximum timestamp offset (0x3FFF).
	f.Add(buildREDPayload(
		[]redSeedBlock{{offset: 0x3FFF, payload: []byte{0xAB}}},
		[]byte{0xCD},
	))

	// Seed: 10-bit maximum block length (0x3FF = 1023 bytes).
	{
		bigPayload := make([]byte, 0x3FF)
		for i := range bigPayload {
			bigPayload[i] = byte(i)
		}
		f.Add(buildREDPayload(
			[]redSeedBlock{{offset: 960, payload: bigPayload}},
			[]byte{0xFF},
		))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		primary, blocks, err := red.Parse(data, opusPT)
		if err != nil {
			// Parse correctly rejected the input.
			return
		}
		// If Parse succeeded, primary must be non-empty.
		if len(primary) == 0 {
			t.Fatalf("Parse succeeded but returned empty primary (input len=%d)", len(data))
		}
		// Block payloads must be sub-slices of the input; detect out-of-range.
		for i, b := range blocks {
			if len(b.Payload) > len(data) {
				t.Fatalf("block[%d].Payload len=%d exceeds input len=%d", i, len(b.Payload), len(data))
			}
			if b.TimestampOffset <= 0 {
				t.Fatalf("block[%d].TimestampOffset=%d want positive", i, b.TimestampOffset)
			}
		}
		if len(blocks) > red.MaxDepth {
			t.Fatalf("Parse returned %d blocks, MaxDepth=%d", len(blocks), red.MaxDepth)
		}
		// The parsed primary must be within the data bounds.
		if len(primary) > len(data) {
			t.Fatalf("primary len=%d exceeds input len=%d", len(primary), len(data))
		}
	})
}

// FuzzREDBuildRoundTrip exercises red.Build followed by red.Parse, asserting
// the round-trip never panics and that Build output is always accepted by Parse
// (or correctly bypassed for depth=0 / empty primary).
func FuzzREDBuildRoundTrip(f *testing.F) {
	type buildArgs struct {
		primary      []byte
		primaryTS    uint32
		hist0Ts      uint32
		hist0Payload []byte
		hist1Ts      uint32
		hist1Payload []byte
		depth        uint8
		frameSamples uint32
	}

	// wrapTS480: uint32 modular subtraction 480 - 960 wraps to 0xFFFFFC80.
	var wrapTS480 uint32 = 480
	wrapTS480 -= 960

	seedCases := []buildArgs{
		{primary: []byte{0xF8, 0xFF, 0xFE}, primaryTS: 2960, hist0Ts: 2000, hist0Payload: []byte{0x11, 0x22}, hist1Ts: 1040, hist1Payload: []byte{0x33, 0x44, 0x55}, depth: 2, frameSamples: 960},
		{primary: []byte{0xAA}, primaryTS: 480, hist0Ts: wrapTS480, hist0Payload: []byte{0xBB}, depth: 1, frameSamples: 960},
		{primary: nil, primaryTS: 0, depth: 2, frameSamples: 960},
		{primary: []byte{0x01}, primaryTS: 960, depth: 0, frameSamples: 960},
		{primary: []byte{0x01}, primaryTS: 960, depth: 1, frameSamples: 0},
		{primary: make([]byte, 0x3FF), primaryTS: 5 * 960, hist0Ts: 4 * 960, hist0Payload: make([]byte, 0x3FF), depth: byte(red.MaxDepth), frameSamples: 960},
	}

	for _, s := range seedCases {
		f.Add(s.primary, s.primaryTS, s.hist0Ts, s.hist0Payload, s.hist1Ts, s.hist1Payload, s.depth, s.frameSamples)
	}

	f.Fuzz(func(t *testing.T,
		primary []byte,
		primaryTS uint32,
		hist0Ts uint32,
		hist0Payload []byte,
		hist1Ts uint32,
		hist1Payload []byte,
		depth uint8,
		frameSamples uint32,
	) {
		if len(primary) > 4096 {
			primary = primary[:4096]
		}
		if len(hist0Payload) > 4096 {
			hist0Payload = hist0Payload[:4096]
		}
		if len(hist1Payload) > 4096 {
			hist1Payload = hist1Payload[:4096]
		}

		var history []red.Frame
		if len(hist0Payload) > 0 {
			history = append(history, red.Frame{Timestamp: hist0Ts, Payload: hist0Payload})
		}
		if len(hist1Payload) > 0 {
			history = append(history, red.Frame{Timestamp: hist1Ts, Payload: hist1Payload})
		}

		payload, redundantBytes := red.Build(primary, primaryTS, history, int(depth), int(frameSamples), opusPT)

		// If primary was empty, output must be empty.
		if len(primary) == 0 {
			if len(payload) != 0 {
				t.Fatalf("Build(empty primary) returned non-empty payload len=%d", len(payload))
			}
			if redundantBytes != 0 {
				t.Fatalf("Build(empty primary) returned redundantBytes=%d", redundantBytes)
			}
			return
		}

		// depth=0: raw Opus pass-through, no RED envelope.
		if depth == 0 {
			if !bytes.Equal(payload, primary) {
				t.Fatalf("Build(depth=0) payload mismatch: got len=%d want len=%d", len(payload), len(primary))
			}
			if redundantBytes != 0 {
				t.Fatalf("Build(depth=0) redundantBytes=%d want 0", redundantBytes)
			}
			return
		}

		// For depth>0 and non-empty primary, Parse must accept the output.
		gotPrimary, gotBlocks, err := red.Parse(payload, opusPT)
		if err != nil {
			t.Fatalf("Build(depth=%d,frameSamples=%d) produced payload Parse rejected: %v", depth, frameSamples, err)
		}
		if !bytes.Equal(gotPrimary, primary) {
			t.Fatalf("Build/Parse primary mismatch: got len=%d want len=%d", len(gotPrimary), len(primary))
		}
		if len(gotBlocks) > red.MaxDepth {
			t.Fatalf("Parse returned %d blocks, exceeds MaxDepth=%d", len(gotBlocks), red.MaxDepth)
		}
		if redundantBytes < 0 {
			t.Fatalf("redundantBytes=%d want >= 0", redundantBytes)
		}
		// Sum of block payload lengths must equal redundantBytes.
		var sumBlockLen int
		for _, b := range gotBlocks {
			sumBlockLen += len(b.Payload)
		}
		if sumBlockLen != redundantBytes {
			t.Fatalf("block payload sum=%d != redundantBytes=%d", sumBlockLen, redundantBytes)
		}
	})
}

// FuzzREDFindRecovery exercises red.FindRecovery against fuzz-generated block
// slices, asserting it never panics and returns nil for nonsensical inputs.
func FuzzREDFindRecovery(f *testing.F) {
	// Seed: known-good recovery.
	f.Add(
		[]byte{0x11, 0x22}, // block 0 payload
		int64(960),         // block 0 timestamp offset
		int64(1),           // lostAgo
		int64(960),         // frameSamples
		uint32(1920),       // currentTimestamp
		uint32(960),        // missingTimestamp
	)
	// Seed: no match (wrong missingTimestamp).
	f.Add([]byte{0x11, 0x22}, int64(960), int64(1), int64(960), uint32(2000), uint32(500))
	// Seed: lostAgo=0 (invalid).
	f.Add([]byte{0xAA}, int64(960), int64(0), int64(960), uint32(1920), uint32(960))
	// Seed: frameSamples=0 (invalid).
	f.Add([]byte{0xAA}, int64(960), int64(1), int64(0), uint32(1920), uint32(960))
	// Seed: timestamp wrap (uint32 modular arithmetic: 480 - 960 wraps to 0xFFFFFC80).
	var wrapMissingTS uint32 = 480
	wrapMissingTS -= 960
	f.Add([]byte{0xDE, 0xAD}, int64(960), int64(1), int64(960), uint32(480), wrapMissingTS)

	f.Fuzz(func(t *testing.T,
		blockPayload []byte,
		blockOffset int64,
		lostAgo int64,
		frameSamples int64,
		currentTS uint32,
		missingTS uint32,
	) {
		if len(blockPayload) > 4096 {
			blockPayload = blockPayload[:4096]
		}
		// Clamp to RFC 2198 range for TimestampOffset (14-bit unsigned).
		if blockOffset < 0 {
			blockOffset = -blockOffset
		}
		blockOffset &= 0x3FFF

		blocks := []red.Block{
			{
				PayloadType:     opusPT,
				TimestampOffset: int(blockOffset),
				Payload:         blockPayload,
			},
		}

		// Must not panic.
		_ = red.FindRecovery(blocks, int(lostAgo), int(frameSamples), currentTS, missingTS)
	})
}

// FuzzREDAppendHistory exercises red.AppendHistory against fuzz-generated
// inputs, asserting it never panics and never grows the history beyond maxDepth.
func FuzzREDAppendHistory(f *testing.F) {
	f.Add([]byte{0x01, 0x02}, uint32(1000), uint8(3))
	f.Add([]byte{}, uint32(2000), uint8(0))
	f.Add(make([]byte, 1500), uint32(0xFFFFFFFF), uint8(red.MaxDepth))
	f.Add([]byte{0xFF}, uint32(0), uint8(1))

	f.Fuzz(func(t *testing.T, payload []byte, ts uint32, maxDepth uint8) {
		if len(payload) > 4096 {
			payload = payload[:4096]
		}
		md := int(maxDepth)
		if md < 0 {
			md = 0
		}

		var h []red.Frame
		// Append several times to exercise trimming.
		for i := 0; i < 8; i++ {
			p := make([]byte, len(payload))
			copy(p, payload)
			h = red.AppendHistory(h, p, ts+uint32(i)*960, md)
		}
		if md > 0 && len(h) > md {
			t.Fatalf("AppendHistory grew history to %d, maxDepth=%d", len(h), md)
		}
	})
}
