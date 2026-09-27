//go:build !gopus_fixed_point

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestPublicShortInputMatchesFloatLibopus compares the public int16 entry with
// the selected non-fixed opus_encode(int16*) build on identical raw samples.
func TestPublicShortInputMatchesFloatLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	pcm := make([]int16, frameSize*frames)
	seed := uint32(6716)
	for i := range pcm {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		pcm[i] = int16(int32(seed) >> 19)
	}
	ref, err := libopustest.ProbeOpusEncodeFloatBuildShortRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return e.SetMode(EncoderModeCELT) },
		func() error { return e.SetBandwidth(BandwidthFullband) },
		func() error { return e.SetFrameSize(frameSize) },
		func() error { return e.SetComplexity(0) },
		func() error { return e.SetBitrate(64000) },
		func() error { return e.SetBitrateMode(BitrateModeCBR) },
		func() error { return e.SetForceChannels(1) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, 4000)
	for frame := range frames {
		n, err := e.EncodeInt16(pcm[frame*frameSize:(frame+1)*frameSize], packet)
		if err != nil || ref[frame].Status != 0 || n != len(ref[frame].Packet) ||
			e.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
			first := 0
			for first < n && first < len(ref[frame].Packet) && packet[first] == ref[frame].Packet[first] {
				first++
			}
			t.Fatalf("frame %d err=%v len=%d/%d first=%d range=%08x/%08x", frame, err,
				n, len(ref[frame].Packet), first, e.FinalRange(), ref[frame].FinalRange)
		}
	}
}

// TestPublicShortInputLSBAndAPISwitchMatchesFloatLibopus keeps one C encoder
// across short, float and int24 calls. opus_encode clamps only the short call's
// effective LSB depth to 16; the configured depth survives API switches/reset.
func TestPublicShortInputLSBAndAPISwitchMatchesFloatLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	for _, depth := range []int{24, 12} {
		t.Run(fmt.Sprintf("lsb%d", depth), func(t *testing.T) {
			const frames = 6
			short := make([]int16, frameSize*frames)
			seed := uint32(8123 + depth)
			for i := range short {
				seed ^= seed << 13
				seed ^= seed >> 17
				seed ^= seed << 5
				short[i] = int16(int32(seed) >> 19)
			}
			mixed := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
			for f := range mixed {
				pcm16 := short[f*frameSize : (f+1)*frameSize]
				mixed[f].Format = uint32(f % 3)
				mixed[f].ResetBefore = f == 3
				switch mixed[f].Format {
				case 0:
					mixed[f].ShortPCM = pcm16
				case 1:
					mixed[f].FloatPCM = make([]float32, frameSize)
					for i, v := range pcm16 {
						mixed[f].FloatPCM[i] = float32(v)*(1.0/32768.0) + float32(i%13-6)*(1.0/8388608.0)
					}
				case 2:
					mixed[f].PCM24 = make([]int32, frameSize)
					for i, v := range pcm16 {
						mixed[f].PCM24[i] = int32(v)<<8 + int32(i%13-6)
					}
				}
			}
			ref, err := libopustest.ProbeOpusEncodeFloatBuildMixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
				MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
				Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
				ForceChannels: 1, FrameSize: frameSize, LSBDepth: depth,
			}, mixed)
			if err != nil {
				t.Fatal(err)
			}
			e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return e.SetMode(EncoderModeCELT) },
				func() error { return e.SetBandwidth(BandwidthFullband) },
				func() error { return e.SetFrameSize(frameSize) },
				func() error { return e.SetComplexity(0) },
				func() error { return e.SetBitrate(64000) },
				func() error { return e.SetBitrateMode(BitrateModeCBR) },
				func() error { return e.SetForceChannels(1) },
				func() error { return e.SetLSBDepth(depth) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			packet := make([]byte, 4000)
			for f, frame := range mixed {
				if frame.ResetBefore {
					e.Reset()
				}
				var n int
				switch frame.Format {
				case 0:
					n, err = e.EncodeInt16(frame.ShortPCM, packet)
				case 1:
					n, err = e.Encode(frame.FloatPCM, packet)
				case 2:
					n, err = e.EncodeInt24(frame.PCM24, packet)
				}
				if err != nil || ref[f].Status != 0 || n != len(ref[f].Packet) ||
					e.FinalRange() != ref[f].FinalRange || !bytes.Equal(packet[:n], ref[f].Packet) {
					t.Fatalf("frame %d format=%d err=%v len=%d/%d range=%08x/%08x", f,
						frame.Format, err, n, len(ref[f].Packet), e.FinalRange(), ref[f].FinalRange)
				}
			}
		})
	}
}
