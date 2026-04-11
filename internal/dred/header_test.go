package dred

import (
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func makeHeaderPayloadForTest(t *testing.T, q0, dq, qmax, extraOffset, dredFrameOffset, dredOffset int) []byte {
	t.Helper()

	if extraOffset < 0 || extraOffset%32 != 0 || extraOffset/32 >= 256 {
		t.Fatalf("extraOffset=%d invalid for DRED test payload", extraOffset)
	}
	rawOffset := 16 - dredOffset - extraOffset + dredFrameOffset
	if rawOffset < 0 || rawOffset >= 32 {
		t.Fatalf("rawOffset=%d out of range for dredOffset=%d frameOffset=%d extraOffset=%d", rawOffset, dredOffset, dredFrameOffset, extraOffset)
	}

	var enc rangecoding.Encoder
	enc.Init(make([]byte, MinBytes))
	enc.EncodeUniform(uint32(q0), 16)
	enc.EncodeUniform(uint32(dq), 8)
	if extraOffset > 0 {
		enc.EncodeUniform(1, 2)
		enc.EncodeUniform(uint32(extraOffset/32), 256)
	} else {
		enc.EncodeUniform(0, 2)
	}
	enc.EncodeUniform(uint32(rawOffset), 32)
	if q0 < 14 && dq > 0 {
		nvals := 15 - (q0 + 1)
		if qmax >= 15 {
			enc.Encode(0, uint32(nvals), uint32(2*nvals))
		} else {
			fl := nvals + qmax - (q0 + 1)
			fh := nvals + qmax - q0
			enc.Encode(uint32(fl), uint32(fh), uint32(2*nvals))
		}
	}
	enc.Shrink(MinBytes)
	return enc.Done()
}

func TestParseHeader(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 6, 3, 9, 0, 0, 4)

	got, err := ParseHeader(payload, 0)
	if err != nil {
		t.Fatalf("ParseHeader error: %v", err)
	}
	if got.Q0 != 6 || got.DQ != 3 || got.QMax != 9 {
		t.Fatalf("ParseHeader q=(%d,%d,%d) want (6,3,9)", got.Q0, got.DQ, got.QMax)
	}
	if got.ExtraOffset != 0 {
		t.Fatalf("ParseHeader extraOffset=%d want 0", got.ExtraOffset)
	}
	if got.DredOffset != 4 {
		t.Fatalf("ParseHeader dredOffset=%d want 4", got.DredOffset)
	}
	if got.QuantizerLevel(0) != 6 || got.QuantizerLevel(2) != 7 || got.QuantizerLevel(20) != 9 {
		t.Fatalf("QuantizerLevel sequence got (%d,%d,%d) want (6,7,9)", got.QuantizerLevel(0), got.QuantizerLevel(2), got.QuantizerLevel(20))
	}
}

func TestParseHeaderWithExtraOffsetAndFrameOffset(t *testing.T) {
	payload := makeHeaderPayloadForTest(t, 7, 2, 15, 64, 8, -40)

	got, err := ParseHeader(payload, 8)
	if err != nil {
		t.Fatalf("ParseHeader error: %v", err)
	}
	if got.Q0 != 7 || got.DQ != 2 || got.QMax != 15 {
		t.Fatalf("ParseHeader q=(%d,%d,%d) want (7,2,15)", got.Q0, got.DQ, got.QMax)
	}
	if got.ExtraOffset != 64 {
		t.Fatalf("ParseHeader extraOffset=%d want 64", got.ExtraOffset)
	}
	if got.DredFrameOffset != 8 {
		t.Fatalf("ParseHeader dredFrameOffset=%d want 8", got.DredFrameOffset)
	}
	if got.DredOffset != -40 {
		t.Fatalf("ParseHeader dredOffset=%d want -40", got.DredOffset)
	}
	if got.OffsetSamples(48000) != -4800 {
		t.Fatalf("OffsetSamples=%d want -4800", got.OffsetSamples(48000))
	}
	if got.EndSamples(48000) != 4800 {
		t.Fatalf("EndSamples=%d want 4800", got.EndSamples(48000))
	}
}

func TestParseHeaderRejectsShortPayload(t *testing.T) {
	if _, err := ParseHeader(make([]byte, MinBytes-1), 0); err == nil {
		t.Fatal("ParseHeader(short) error=nil want non-nil")
	}
}
