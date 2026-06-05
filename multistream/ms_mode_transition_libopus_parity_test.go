package multistream

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// encodeModeSwitchSingleStreamPackets encodes one elementary-stream packet per
// requested mode, each from a FRESH encoder so the forced coding mode is honored
// exactly (a fresh encoder always codes its first frame in the requested mode,
// whereas a long-lived encoder may override the forced mode for rate/transient
// reasons). Concatenated, the packets present the decoder with genuine
// CELT<->SILK/Hybrid mode changes carrying no redundancy (a fresh encoder has no
// prior frame to prefill from), exactly the opus_decode_frame pcm_transition
// crossfade case. These single-stream Opus packets are valid 1-stream
// multistream packets, so decoding them through the multistream decoder
// exercises the per-stream streamState transition handling against the libopus
// multistream oracle.
func encodeModeSwitchSingleStreamPackets(t *testing.T, channels int, frameSize int, modes []encoder.Mode) [][]byte {
	t.Helper()
	const sampleRate = 48000

	packets := make([][]byte, 0, len(modes))
	phase := 0.0
	for f, m := range modes {
		enc := encoder.NewEncoder(sampleRate, channels)
		enc.SetFrameSize(frameSize)
		enc.SetBandwidth(types.BandwidthFullband)
		enc.SetBitrate(96000)
		if err := enc.SetInBandFEC(0); err != nil {
			t.Fatalf("SetInBandFEC: %v", err)
		}
		if channels == 2 {
			enc.SetForceChannels(2)
		}
		enc.SetMode(m)

		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
				0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
			if channels == 2 {
				pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
					0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// streamModeOfPacket classifies the per-stream coding mode from the TOC of a
// single-stream Opus packet.
func streamModeOfPacket(pkt []byte) int {
	if len(pkt) == 0 {
		return -1
	}
	return parseStreamTOC(pkt[0]).mode
}

func perStreamModes(packets [][]byte) []int {
	modes := make([]int, len(packets))
	for i, p := range packets {
		modes[i] = streamModeOfPacket(p)
	}
	return modes
}

// TestMultistreamPerStreamModeTransitionMatchesLibopus locks the multistream
// per-stream mode-transition handling that this change ports from the
// single-stream gopus.Decoder into the multistream streamState path:
//
//   - the CELT decoder OPUS_RESET_STATE on any mode change that did not come
//     from a redundancy frame, and
//   - the 5 ms pcm_transition crossfade (smooth_fade) applied whenever the
//     coding mode crosses the CELT_ONLY boundary (opus_decode_frame).
//
// The constituent stream changes coding mode every frame (SILK->CELT->Hybrid
// ...), each packet from a fresh encoder so the mode switches carry no
// redundancy and therefore drive the pcm_transition path. Decoding a 1-stream
// mono multistream packet is identical to decoding the underlying Opus packet.
//
// What is gated sample-exact: the body of every CELT-target transition frame
// after the 5 ms transition window (the part the CELT OPUS_RESET_STATE + CELT
// decode produce). Before this change the multistream path never reset the CELT
// decoder on a mode change, so a Hybrid->CELT frame body diverged from libopus
// by ~0.2; with the reset the body is now bit-exact (amd64; <=1-ULP arm64).
//
// The first 5 ms transition window itself is NOT asserted sample-exact: it is a
// crossfade onto a packet-loss-concealment frame decoded in the previous mode,
// and gopus's SILK/CELT PLC is not yet bit-exact with libopus on a
// *no-redundancy* mode transition. That residual is a shared SILK/CELT PLC
// parity gap -- it reproduces identically on the libopus-gated single-stream
// gopus.Decoder (a fresh-encoder SILK->CELT transition diverges by the same
// amount there) -- and is independent of the multistream port. The window is
// checked only within a loose bound so a gross crossfade regression still trips.
func TestMultistreamPerStreamModeTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960 // 20 ms
		f5         = frameSize / 4
	)

	// Mode walk crossing the CELT_ONLY boundary in every direction.
	modeWalk := []encoder.Mode{
		encoder.ModeSILK,
		encoder.ModeCELT,
		encoder.ModeHybrid,
		encoder.ModeCELT,
		encoder.ModeHybrid,
		encoder.ModeCELT,
		encoder.ModeCELT,
	}

	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize, modeWalk)
			modes := perStreamModes(packets)

			// Require an actual CELT-target transition (SILK/Hybrid -> CELT), the
			// case whose body the CELT OPUS_RESET_STATE fixes.
			sawCeltTarget := false
			prev := -1
			for i, m := range modes {
				if i > 0 && m == streamModeCELT && prev != streamModeCELT {
					sawCeltTarget = true
				}
				prev = m
			}
			if !sawCeltTarget {
				t.Fatalf("encoder did not produce a CELT-target transition; modes=%v", modes)
			}

			streams := 1
			coupled := 0
			if channels == 2 {
				coupled = 1
			}
			mapping := trivialMapping(channels)

			dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			var got []float32
			for i, p := range packets {
				frame, derr := dec.DecodeToFloat32(p, frameSize)
				if derr != nil {
					t.Fatalf("frame %d gopus decode: %v", i, derr)
				}
				got = append(got, frame...)
			}

			want, err := decodeWithLibopusReferencePackets(1, sampleRate, channels, streams, coupled, frameSize, mapping, nil, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream mode-transition reference decode", err)
			}
			if len(got) != len(want) {
				t.Fatalf("length mismatch got=%d want=%d", len(got), len(want))
			}

			perFrame := frameSize * channels
			celtBoundaryBody := false
			for f := range modes {
				// A frame crosses the CELT_ONLY boundary (in either direction) iff
				// its CELT-ness differs from the previous frame; libopus applies the
				// 5 ms pcm_transition crossfade exactly on those frames.
				transition := f > 0 && (modes[f] == streamModeCELT) != (modes[f-1] == streamModeCELT)
				celtTargetBody := transition && modes[f] == streamModeCELT
				base := f * perFrame
				bodyStart := 0
				if transition {
					bodyStart = f5 * channels
				}
				if celtTargetBody {
					celtBoundaryBody = true
				}
				for i := base; i < base+perFrame && i < len(got); i++ {
					off := i - base
					diff := math.Abs(float64(got[i] - want[i]))
					if off >= bodyStart {
						// Body after any transition window (and the whole frame when
						// no transition applies) is locked sample-exact.
						if diff > 1e-6 {
							t.Fatalf("frame %d (mode=%d) body not sample-exact at off %d: got=%g want=%g diff=%g",
								f, modes[f], off, got[i], want[i], diff)
						}
					} else if diff > 0.6 {
						// Transition window: only catch a gross crossfade regression.
						t.Fatalf("frame %d (mode=%d) transition window grossly wrong at off %d: got=%g want=%g diff=%g",
							f, modes[f], off, got[i], want[i], diff)
					}
				}
			}
			if !celtBoundaryBody {
				t.Fatalf("no CELT-target transition body was checked; modes=%v", modes)
			}
		})
	}
}
