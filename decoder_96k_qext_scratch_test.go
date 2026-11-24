//go:build gopus_qext

package gopus

import "testing"

func TestDecode96kScratchBoundedOnLargeOutputBuffer(t *testing.T) {
	cfg := DefaultDecoderConfig(96000, 2)
	cfg.MaxPacketSamples = 11520
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder(96k): %v", err)
	}
	initialCap := cap(dec.scratchPCM)

	// Oversized packet must fail before touching scratchPCM.
	hugeBuf16 := make([]int16, 100000)
	if _, err := dec.DecodeInt16(make([]byte, cfg.MaxPacketBytes+1), hugeBuf16); err != ErrPacketTooLarge {
		t.Fatalf("DecodeInt16 oversized packet err = %v, want ErrPacketTooLarge", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt16 grew scratchPCM cap from %d to %d on rejected packet", initialCap, cap(dec.scratchPCM))
	}

	// Valid packet with oversized caller buffer must cap scratchPCM at maxPacketSamples*channels.
	// Code-0 silence frame (TOC=0xf8 = CELT FB 20ms mono).
	if _, err := dec.DecodeInt16([]byte{0xf8}, hugeBuf16); err != nil {
		t.Fatalf("DecodeInt16(96k) failed: %v", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt16 grew scratchPCM cap from %d to %d for oversized pcm buffer", initialCap, cap(dec.scratchPCM))
	}

	hugeBuf24 := make([]int32, 100000)
	if _, err := dec.DecodeInt24([]byte{0xf8}, hugeBuf24); err != nil {
		t.Fatalf("DecodeInt24(96k) failed: %v", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt24 grew scratchPCM cap from %d to %d for oversized pcm buffer", initialCap, cap(dec.scratchPCM))
	}

	// Invalid PLC buffer length (not a multiple of 2.5 ms) must not grow scratchPCM.
	if _, err := dec.DecodeInt16(nil, make([]int16, 100002)); err != ErrInvalidFrameSize {
		t.Fatalf("DecodeInt16 invalid PLC len err = %v, want ErrInvalidFrameSize", err)
	}
	if cap(dec.scratchPCM) != initialCap {
		t.Fatalf("DecodeInt16 invalid PLC grew scratchPCM cap from %d to %d", initialCap, cap(dec.scratchPCM))
	}
}
