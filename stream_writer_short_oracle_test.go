//go:build !gopus_fixed_point

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestWriterInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 3
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels%d", channels), func(t *testing.T) {
			pcm := make([]int16, frameSize*channels*frames)
			seed := uint32(6716)
			inputSamples := frameSize*channels*2 + 443*channels
			for i := range inputSamples {
				seed ^= seed << 13
				seed ^= seed >> 17
				seed ^= seed << 5
				pcm[i] = int16(int32(seed) >> 30)
			}
			ref, err := libopustest.ProbeOpusEncodeFloatBuildShortRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
				MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
				Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0, VBR: true,
				ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
			})
			if err != nil {
				t.Fatal(err)
			}
			sink := new(slicePacketSink)
			w, err := NewWriter(48000, channels, sink, FormatInt16LE, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return w.enc.SetMode(EncoderModeCELT) },
				func() error { return w.enc.SetBandwidth(BandwidthFullband) },
				func() error { return w.SetComplexity(0) },
				func() error { return w.SetBitrate(64000) },
				func() error { return w.enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return w.enc.SetForceChannels(channels) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			raw := make([]byte, inputSamples*2)
			for i, v := range pcm[:inputSamples] {
				binary.LittleEndian.PutUint16(raw[i*2:], uint16(v))
			}
			// Split a sample across writes; Flush pads the final partial frame.
			split := frameSize*channels*2 + 1
			for _, chunk := range [][]byte{raw[:split], raw[split:]} {
				if n, err := w.Write(chunk); err != nil || n != len(chunk) {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if len(sink.packets) != len(ref) {
				t.Fatalf("packets=%d, want %d", len(sink.packets), len(ref))
			}
			for i, packet := range sink.packets {
				if ref[i].Status != 0 || !bytes.Equal(packet, ref[i].Packet) {
					t.Fatalf("packet %d differs from opus_encode(int16*): bytes=%d/%d C status=%d", i, len(packet), len(ref[i].Packet), ref[i].Status)
				}
			}
			if got := w.enc.FinalRange(); got != ref[len(ref)-1].FinalRange {
				t.Fatalf("final range=%08x, want %08x", got, ref[len(ref)-1].FinalRange)
			}
		})
	}
}
