//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// decodeWithLibopusFixedInt16Gain drives FIXED_POINT opus_decode() with a
// non-zero OPUS_SET_GAIN applied on the reference decoder, so the int16 output
// reflects the FIXED_POINT decode-gain stage (celt_exp2 + MULT32_32_Q16 +
// SATURATE) the gopus integer path must reproduce.
func decodeWithLibopusFixedInt16Gain(sampleRate, channels, frameSize, gainQ8 int, packets [][]byte) ([]int16, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt16, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int16 gain", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// decodeWithLibopusFixedInt24Gain drives FIXED_POINT opus_decode24() with a
// non-zero OPUS_SET_GAIN on the reference decoder.
func decodeWithLibopusFixedInt24Gain(sampleRate, channels, frameSize, gainQ8 int, packets [][]byte) ([]int32, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int24 gain", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// encodeFixedDecodeGainCELTSequence encodes a CELT-only multi-frame stream so the
// decode runs entirely through the integer CELT path; the decode-gain stage then
// applies to integer-exact accumulated opus_res samples.
func encodeFixedDecodeGainCELTSequence(t *testing.T, channels, frameSize, frames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
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
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	packets := make([][]byte, 0, frames)
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			// Large-amplitude tone so the decode-gain SATURATE clamp is exercised
			// for the higher positive gains.
			pcm[i*channels] = 0.7 * float32(math.Sin(2*math.Pi*440*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.65 * float32(math.Sin(2*math.Pi*523*tm+0.2))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointDecodeGainParity gates that the public DecodeInt16 /
// DecodeInt24 with a non-zero OPUS_SET_GAIN is bit-exact with the libopus
// FIXED_POINT opus_decode / opus_decode24 reference, for the integer CELT path.
// The gain stage (celt_exp2(MULT16_16_P15(...)) -> MULT32_32_Q16 -> SATURATE) is
// applied to the integer-exact opus_res accumulation rather than dropping to the
// lossy float conversion. Bit-exact on amd64; subject to the documented per-arch
// 1-ULP CELT drift budget on arm64.
func TestDecoderFixedPointDecodeGainParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	gains := []int{256, -256, 1536, -2048, 6144, -6144}
	type tc struct {
		name      string
		channels  int
		frameSize int
		frames    int
	}
	cases := []tc{
		{"mono_960", 1, 960, 4},
		{"stereo_960", 2, 960, 4},
		{"mono_480", 1, 480, 6},
	}

	for _, c := range cases {
		for _, g := range gains {
			c, g := c, g
			t.Run(fmt.Sprintf("%s_gain%d", c.name, g), func(t *testing.T) {
				packets := encodeFixedDecodeGainCELTSequence(t, c.channels, c.frameSize, c.frames)
				if toc := ParseTOC(packets[0][0]); toc.Mode != ModeCELT {
					t.Skipf("first packet mode %v, want CELT", toc.Mode)
				}

				refInt16, err := decodeWithLibopusFixedInt16Gain(sampleRate, c.channels, c.frameSize, g, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference decode int16 gain", err)
					return
				}
				refInt24, err := decodeWithLibopusFixedInt24Gain(sampleRate, c.channels, c.frameSize, g, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference decode int24 gain", err)
					return
				}

				dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
				dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
				if err := dec16.SetGain(g); err != nil {
					t.Fatalf("dec16 SetGain: %v", err)
				}
				if err := dec24.SetGain(g); err != nil {
					t.Fatalf("dec24 SetGain: %v", err)
				}

				var got16, got24 []int32
				for p, pkt := range packets {
					o16 := make([]int16, c.frameSize*c.channels)
					if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
						t.Fatalf("packet %d DecodeInt16: %v", p, err)
					}
					got16 = append(got16, int16ToInt32(o16)...)
					o24 := make([]int32, c.frameSize*c.channels)
					if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
						t.Fatalf("packet %d DecodeInt24: %v", p, err)
					}
					got24 = append(got24, o24...)
				}

				assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
				assertFixedExact(t, "int24", got24, refInt24)
			})
		}
	}
}
