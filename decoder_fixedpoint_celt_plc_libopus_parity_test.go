//go:build gopus_fixed_point

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// encodeFixedCELTSequence encodes a multi-frame CELT-only stream through the
// public Encoder so the decode exercises real cross-frame CELT state
// (decode_mem, energy histories, post-filter). Each frame carries a distinct
// signal so the encoded packets differ frame to frame.
func encodeFixedCELTSequence(t *testing.T, channels, frameSize, frames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	var packets [][]byte
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.28 * float32(math.Sin(2*math.Pi*1200*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.19 * float32(math.Sin(2*math.Pi*1900*tm))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		if toc := ParseTOC(pkt[0]); toc.Mode != ModeCELT {
			t.Skipf("encoder produced mode %v, want CELT", toc.Mode)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointCELTPLCParity gates (A) and (B) at the public-API level:
// the public Decoder.DecodeInt16 / DecodeInt24 of a CELT-only stream containing
// LOST frames -- a single loss, a 3-frame burst, then the recovered good frame
// immediately after the loss -- is bit-exact with the libopus FIXED_POINT
// opus_decode / opus_decode24 reference.
//
// (A) the LOST frames route through the integer celt_decode_lost.
// (B) the first good frame after the burst enters celt_decode_with_ec with
// loss_duration != 0, exercising the coarse-energy prediction safety block.
//
// Bit-exact on every architecture (assertFixedExact): the integer decode has no
// fused-multiply-add, so there is no per-arch float drift.
func TestDecoderFixedPointCELTPLCParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		// lossAt[i] marks frame index i as a lost frame (PLC). Other frames are
		// the corresponding encoded packet.
		lossAt map[int]bool
		frames int
	}
	cases := []tc{
		{"mono_960_single", 1, 960, map[int]bool{2: true}, 6},
		{"mono_960_burst", 1, 960, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"mono_480_burst", 1, 480, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"mono_240_burst", 1, 240, map[int]bool{3: true, 4: true, 5: true}, 9},
		{"mono_120_burst", 1, 120, map[int]bool{3: true, 4: true, 5: true}, 9},
		{"stereo_960_single", 2, 960, map[int]bool{2: true}, 6},
		{"stereo_960_burst", 2, 960, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"stereo_480_burst", 2, 480, map[int]bool{2: true, 3: true, 4: true}, 8},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, c.channels, c.frameSize, c.frames)

			// Build the decode step sequence: nil for lost frames, the encoded
			// packet otherwise.
			steps := make([][]byte, c.frames)
			for i := 0; i < c.frames; i++ {
				if c.lossAt[i] {
					steps[i] = nil
				} else {
					steps[i] = encoded[i]
				}
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16 (PLC)", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24 (PLC)", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int24: %v", err)
			}

			var got16, got24 []int32
			for p, pkt := range steps {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("step %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)

				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("step %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}
