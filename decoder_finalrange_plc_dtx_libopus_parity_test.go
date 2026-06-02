//go:build gopus_libopus_oracle

package gopus

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Frame-by-frame OPUS_GET_FINAL_RANGE parity vs the libopus oracle across
// normal -> PLC -> DTX -> recovery sequences, including multi-frame packets
// whose inner frames are DTX/empty.
//
// Ground truth (src/opus_decoder.c opus_decode_frame, libopus 1.6.1):
//
//	if (len <= 1)
//	   st->rangeFinal = 0;     // a PLC/DTX frame contributes a 0 final range
//	else
//	   st->rangeFinal ^= redundant_rng;
//
// This is a PER-FRAME condition on the frame's own coded size (size[i] in the
// opus_decode_native loop), and st->rangeFinal is overwritten by every frame,
// so OPUS_GET_FINAL_RANGE after a packet reflects the LAST frame:
//
//   - whole-packet PLC (opus_decode(NULL))      -> 0
//   - TOC-only DTX packet (len == 1)            -> 0
//   - multi-frame packet, last frame DTX        -> 0
//   - multi-frame packet, last frame normal     -> that frame's range (NOT 0),
//                                                  even if an earlier frame was DTX
//
// The oracle (libopus_refdecode_single.c version 6) decodes the SAME step
// sequence through one persistent libopus decoder and reports
// OPUS_GET_FINAL_RANGE after each step; gopus must match every step exactly.

// makeCode1Dup builds a code-1 (two equal CBR frames) packet by duplicating the
// real frame body of a code-0 packet. Both frames are normal.
func makeCode1Dup(base []byte) []byte {
	if len(base) < 2 {
		return nil
	}
	toc := (base[0] &^ 0x03) | 0x01 // code 1
	frame := base[1:]
	out := make([]byte, 0, 1+2*len(frame))
	out = append(out, toc)
	out = append(out, frame...)
	out = append(out, frame...)
	return out
}

// appendOpusFrameLength appends the RFC 6716 §3.1 frame-length encoding of L
// (length = 4*secondByte + firstByte for the two-byte form).
func appendOpusFrameLength(dst []byte, l int) []byte {
	if l < 252 {
		return append(dst, byte(l))
	}
	first := 252 + ((l - 252) & 3)
	second := (l - first) / 4
	return append(dst, byte(first), byte(second))
}

// makeCode2LastDTX builds a code-2 packet whose first frame is the real frame
// and whose last (second) frame is empty -> inner DTX last frame.
func makeCode2LastDTX(base []byte) []byte {
	if len(base) < 2 {
		return nil
	}
	frame1 := base[1:]
	if len(frame1) > maxOpusFrameBytes {
		return nil
	}
	toc := (base[0] &^ 0x03) | 0x02 // code 2
	out := make([]byte, 0, 3+len(frame1))
	out = append(out, toc)
	out = appendOpusFrameLength(out, len(frame1)) // frame1 length
	out = append(out, frame1...)
	// frame2 implicit length = remaining = 0 bytes -> DTX last frame.
	return out
}

// makeCode2FirstDTX builds a code-2 packet whose first frame is empty (DTX) and
// whose last (second) frame is the real frame.
func makeCode2FirstDTX(base []byte) []byte {
	if len(base) < 2 {
		return nil
	}
	toc := (base[0] &^ 0x03) | 0x02 // code 2
	frame := base[1:]
	out := make([]byte, 0, 2+len(frame))
	out = append(out, toc)
	out = append(out, byte(0))  // frame1 length = 0 -> DTX
	out = append(out, frame...) // frame2 = remaining = real frame
	return out
}

// makeTOCOnlyDTX returns the 1-byte TOC-only DTX packet for base's TOC (code 0).
func makeTOCOnlyDTX(base []byte) []byte {
	if len(base) < 1 {
		return nil
	}
	return []byte{base[0] &^ 0x03} // code 0, no payload
}

func TestDecodeFinalRangePLCDTXMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	const sampleRate = 48000

	type modecase struct {
		name string
		make func(t *testing.T, channels int) []byte
	}
	modes := []modecase{
		{"silk_wb", encodeAPIRateSILKPacket},
		{"celt_fb", encodeAPIRateCELTPacket},
		{"hybrid_fb", encodeAPIRateHybridPacket},
	}

	for _, mc := range modes {
		for _, channels := range []int{1, 2} {
			name := mc.name + "_ch" + itoaSmall(channels)
			t.Run(name, func(t *testing.T) {
				src := mc.make(t, channels)
				if len(src) <= 2 {
					t.Skipf("encoded %s packet too small (len=%d) to build multi-frame variants", mc.name, len(src))
				}

				tocDTX := makeTOCOnlyDTX(src)
				lastDTX := makeCode2LastDTX(src)
				firstDTX := makeCode2FirstDTX(src)
				code1 := makeCode1Dup(src)
				if tocDTX == nil || lastDTX == nil || firstDTX == nil || code1 == nil {
					t.Skipf("could not construct multi-frame variants for %s (src len=%d)", mc.name, len(src))
				}

				// Requested frame_size (uniform for every step in the oracle's v6
				// protocol): large enough for the longest (two-frame) packet at
				// 48 kHz (2 x 20 ms = 1920 samples/channel) plus headroom. A NULL
				// PLC step conceals up to this duration on both sides.
				const reqFrame = 2880

				// normal -> PLC(NULL) -> TOC-only DTX -> recovery
				//        -> last-DTX multiframe -> recovery
				//        -> first-DTX multiframe -> CBR code-1 -> recovery
				steps := []libopusAPIRateDecodeStep{
					{packet: src},
					{}, // PLC: nil packet
					{packet: tocDTX},
					{packet: src},
					{packet: lastDTX},
					{packet: src},
					{packet: firstDTX},
					{packet: code1},
					{packet: src},
				}

				_, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, reqFrame, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate final range reference decode", err)
				}
				if len(ranges) != len(steps) {
					t.Fatalf("oracle returned %d ranges, want %d", len(ranges), len(steps))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				buf := make([]float32, reqFrame*channels)

				labels := []string{
					"normal", "PLC(nil)", "DTX-TOC-only", "recovery",
					"multiframe-last-DTX", "recovery2",
					"multiframe-first-DTX", "CBR-code1-allnormal", "recovery3",
				}

				for i, s := range steps {
					clear(buf)
					var derr error
					if s.packet == nil {
						_, derr = dec.Decode(nil, buf)
					} else {
						_, derr = dec.Decode(s.packet, buf)
					}
					if derr != nil {
						t.Fatalf("step %d (%s) gopus decode: %v", i, labels[i], derr)
					}
					got := dec.FinalRange()
					want := ranges[i]
					if got != want {
						t.Fatalf("step %d (%s): FinalRange()=0x%08X want 0x%08X (len=%d)",
							i, labels[i], got, want, packetStepLen(s))
					}
				}

				// Sanity: the inner-DTX-last step must be 0 (regression for the
				// stale-range bug), and a normal recovery step must be non-zero.
				if ranges[4] != 0 {
					t.Fatalf("oracle ground truth changed: multiframe-last-DTX range=0x%08X want 0", ranges[4])
				}
				if ranges[3] == 0 {
					t.Fatalf("oracle ground truth changed: recovery range unexpectedly 0")
				}
			})
		}
	}
}

func packetStepLen(s libopusAPIRateDecodeStep) int {
	if s.packet == nil {
		return -1
	}
	return len(s.packet)
}
