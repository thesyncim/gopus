package red

import "testing"

func TestEncoderResetReleasesHistoryPayloads(t *testing.T) {
	enc := NewEncoder(111, 960, 3)
	payload := make([]byte, 1024)
	for i := range 5 {
		enc.Encode(payload, uint32((i+1)*960))
	}
	if len(enc.history) != 3 {
		t.Fatalf("history len = %d after warmup, want 3", len(enc.history))
	}

	enc.Reset()
	if len(enc.history) != 0 {
		t.Fatalf("history len after reset = %d, want 0", len(enc.history))
	}
	for i, frame := range enc.history[:cap(enc.history)] {
		if frame.Timestamp != 0 || frame.Payload != nil {
			t.Fatalf("history slot %d retains frame after reset: timestamp=%d payload-len=%d", i, frame.Timestamp, len(frame.Payload))
		}
	}
}

func TestDecoderParseReleasesUnusedBlockPayloads(t *testing.T) {
	dec := NewDecoder(111)
	twoBlocks := []byte{0xef, 0x0f, 0, 1, 0xef, 0x0f, 0, 1, 111, 0xa1, 0xa2, 0xcd}
	if _, blocks, err := dec.Parse(twoBlocks); err != nil || len(blocks) != 2 {
		t.Fatalf("Parse(two blocks) returned %d blocks, err=%v", len(blocks), err)
	}
	if dec.blocks[0].Payload == nil || dec.blocks[1].Payload == nil {
		t.Fatal("warm parse did not retain the redundant payloads")
	}

	if _, blocks, err := dec.Parse([]byte{111, 0xee}); err != nil || len(blocks) != 0 {
		t.Fatalf("Parse(primary only) returned %d blocks, err=%v", len(blocks), err)
	}
	assertDecoderStorageEmpty(t, dec)

	if _, blocks, err := dec.Parse(twoBlocks); err != nil || len(blocks) != 2 {
		t.Fatalf("Parse(two blocks) after reuse returned %d blocks, err=%v", len(blocks), err)
	}
	// The first block payload is complete, while the second is truncated. The
	// failed parse must not leave that partial payload or earlier blocks rooted
	// in the Decoder's reusable storage.
	malformed := []byte{0xef, 0x0f, 0, 1, 0xef, 0x0f, 0, 1, 111, 0xa1}
	if primary, blocks, err := dec.Parse(malformed); err == nil || primary != nil || blocks != nil {
		t.Fatalf("Parse(truncated block payload) returned primary=%x blocks=%d err=%v", primary, len(blocks), err)
	}
	assertDecoderStorageEmpty(t, dec)
}

func assertDecoderStorageEmpty(t *testing.T, dec *Decoder) {
	t.Helper()
	if len(dec.blocks) != 0 {
		t.Fatalf("decoder block length = %d after clearing parse, want 0", len(dec.blocks))
	}
	for i, block := range dec.blocks[:cap(dec.blocks)] {
		if block.TimestampOffset != 0 || block.PayloadType != 0 || block.Payload != nil {
			t.Fatalf("decoder block slot %d retains data: offset=%d pt=%d payload-len=%d", i, block.TimestampOffset, block.PayloadType, len(block.Payload))
		}
	}
}

func TestAppendHistoryShrinkReleasesDiscardedPayloads(t *testing.T) {
	var history []Frame
	for i := range MaxDepth {
		history = AppendHistory(history, make([]byte, 16), uint32((i+1)*960), MaxDepth)
	}
	backing := history[:cap(history)]
	history = AppendHistory(history, make([]byte, 16), 6000, 2)
	if len(history) != 2 {
		t.Fatalf("history len after shrink = %d, want 2", len(history))
	}
	for i, frame := range backing[len(history):] {
		if frame.Timestamp != 0 || frame.Payload != nil {
			t.Fatalf("discarded history slot %d retains payload after shrink: timestamp=%d payload-len=%d", i+len(history), frame.Timestamp, len(frame.Payload))
		}
	}
}
