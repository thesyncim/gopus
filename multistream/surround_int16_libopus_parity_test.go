package multistream

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestSurroundInt16PacketRangeMatchesLibopus drives the public short input
// entry point against opus_multistream_encode with the same PCM and controls.
func TestSurroundInt16PacketRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameCount = 6
	for _, tc := range []struct {
		name          string
		channels      int
		mappingFamily int
		frameSize     int
		bitrate       int
		vbr           bool
	}{
		{name: "stereo_coupled_20ms_cvbr", channels: 2, mappingFamily: 1, frameSize: 960, bitrate: 64000, vbr: true},
		{name: "stereo_discrete_20ms_cbr", channels: 2, mappingFamily: 255, frameSize: 960, bitrate: 64000},
		{name: "quad_20ms_cvbr", channels: 4, mappingFamily: 1, frameSize: 960, bitrate: 128000, vbr: true},
		{name: "surround_5_1_10ms_cbr", channels: 6, mappingFamily: 1, frameSize: 480, bitrate: 256000},
		{name: "surround_7_1_20ms_cbr", channels: 8, mappingFamily: 1, frameSize: 960, bitrate: 384000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcm := floatToInt16(generateSurroundSweep(tc.channels, tc.frameSize, frameCount))
			ref, err := encodeLibopusSurroundInt16(48000, tc.channels, tc.mappingFamily, 2049,
				tc.bitrate, tc.vbr, true, 10, -1000, tc.frameSize, frameCount,
				4000, pcm, false)
			if err != nil {
				t.Fatalf("live C short surround encode: %v", err)
			}
			if len(ref.packets) != frameCount || len(ref.ranges) != frameCount {
				t.Fatalf("oracle record count packets/ranges=%d/%d want %d", len(ref.packets), len(ref.ranges), frameCount)
			}
			var enc *Encoder
			if tc.mappingFamily == 255 {
				enc, err = NewEncoder(48000, tc.channels, 2, 0, []byte{0, 1})
			} else {
				enc, err = NewEncoderDefault(48000, tc.channels)
			}
			if err != nil {
				t.Fatal(err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				t.Fatalf("stream layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			if enc.MappingFamily() != tc.mappingFamily {
				t.Fatalf("mapping family Go=%d want %d", enc.MappingFamily(), tc.mappingFamily)
			}
			if !bytes.Equal(enc.mapping, ref.mapping) {
				t.Fatalf("channel mapping Go=%v C=%v", enc.mapping, ref.mapping)
			}
			enc.SetBitrate(tc.bitrate)
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(true)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()
			out := make([]byte, 4000)
			for frame := range frameCount {
				start := frame * tc.frameSize * tc.channels
				input := pcm[start : start+tc.frameSize*tc.channels]
				n, err := enc.EncodeInt16(input, tc.frameSize, out)
				if err != nil {
					t.Fatalf("frame %d Go short encode: %v", frame, err)
				}
				if n <= 0 || !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
					t.Fatalf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x",
						frame, firstByteMismatch(out[:n], ref.packets[frame]), n, len(ref.packets[frame]),
						enc.GetFinalRange(), ref.ranges[frame])
				}
			}
		})
	}
}
