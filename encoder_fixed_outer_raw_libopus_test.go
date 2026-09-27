//go:build gopus_fixed_point

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestPublicFixedShortEncodeMatchesLibopus feeds identical raw signed16 PCM to
// the public short API and the selected FIXED_POINT+ENABLE_RES24 opus_encode.
func TestPublicFixedShortEncodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	pcm := genPCMInt16Pub(6716, 1, frameSize, frames, false)
	ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
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
		if frame == 3 {
			if _, err := e.EncodeInt16(pcm[frame*frameSize:(frame+1)*frameSize], nil); err != ErrBufferTooSmall {
				t.Fatalf("short output buffer error=%v want=%v", err, ErrBufferTooSmall)
			}
		}
		n, err := e.EncodeInt16(pcm[frame*frameSize:(frame+1)*frameSize], packet)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if ref[frame].Status != 0 || n != len(ref[frame].Packet) ||
			e.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
			t.Fatalf("frame %d len=%d want=%d range=%08x want=%08x", frame,
				n, len(ref[frame].Packet), e.FinalRange(), ref[frame].FinalRange)
		}
	}
	last := pcm[(frames-1)*frameSize:]
	var lastN int
	var lastErr error
	for range 3 {
		lastN, lastErr = e.EncodeInt16(last, packet)
		if lastErr != nil || lastN < 2 {
			t.Fatalf("warm encode len=%d err=%v", lastN, lastErr)
		}
	}
	allocs := testing.AllocsPerRun(20, func() {
		lastN, lastErr = e.EncodeInt16(last, packet)
	})
	if lastErr != nil || lastN < 2 || allocs != 0 {
		t.Fatalf("active caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
	}
}

func TestPublicFixedShortExpertFrameDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 5760, 3
	pcm := genPCMInt16Pub(6716, 1, frameSize, frames, false)
	ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, LSBDepth: 24, FrameSize: frameSize,
		ExpertFrameDuration: int(ExpertFrameDuration20Ms),
	}, []libopustest.OpusEncodeFixedMixedFrame{{
		Format: 0, ShortPCM: pcm[:frameSize],
	}, {
		Format: 0, ShortPCM: pcm[frameSize : 2*frameSize],
	}, {
		Format: 0, ShortPCM: pcm[2*frameSize:],
	}})
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
		func() error { return e.SetExpertFrameDuration(ExpertFrameDuration20Ms) },
		func() error { return e.SetComplexity(0) },
		func() error { return e.SetBitrate(64000) },
		func() error { return e.SetBitrateMode(BitrateModeCBR) },
		func() error { return e.SetForceChannels(1) },
		func() error { return e.SetLSBDepth(24) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, 4000)
	for f := range frames {
		n, err := e.EncodeInt16(pcm[f*frameSize:(f+1)*frameSize], packet)
		if err != nil || ref[f].Status != 0 || n != len(ref[f].Packet) ||
			e.FinalRange() != ref[f].FinalRange || !bytes.Equal(packet[:n], ref[f].Packet) {
			t.Fatalf("frame %d err=%v len=%d/%d range=%08x/%08x", f, err,
				n, len(ref[f].Packet), e.FinalRange(), ref[f].FinalRange)
		}
	}
}

// TestPublicFixedLongCELTPacketsMatchLibopus exercises the 40/60 ms public
// packet splitter with a persistent Q8 DC/delay history across three packets.
func TestPublicFixedLongCELTPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, app := range []struct {
		pub Application
		ref int
	}{
		{ApplicationAudio, libopustest.OpusApplicationAudio},
		{ApplicationLowDelay, libopustest.OpusApplicationRestrictedLowDelay},
	} {
		for _, channels := range []int{1, 2} {
			for _, frameSize := range []int{1920, 2880} {
				for _, rateMode := range []struct {
					mode      BitrateMode
					vbr, cvbr bool
				}{
					{BitrateModeCBR, false, false},
					{BitrateModeCVBR, true, true},
					{BitrateModeVBR, true, false},
				} {
					t.Run(fmt.Sprintf("app%d/ch%d/fs%d/mode%d", app.ref, channels, frameSize, rateMode.mode), func(t *testing.T) {
						const frames = 3
						seed := uint32(6716 + channels*13 + frameSize*19 + int(rateMode.mode)*31 + app.ref)
						pcm := genPCMInt16Pub(seed, channels, frameSize, frames, false)
						bitrate := 64000
						if channels == 2 {
							bitrate = 128000
						}
						ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
							SampleRate: 48000, Channels: channels, Application: app.ref,
							MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
							Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 0,
							VBR: rateMode.vbr, VBRConstraint: rateMode.cvbr,
							ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
						})
						if err != nil {
							t.Fatal(err)
						}
						e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: app.pub})
						if err != nil {
							t.Fatal(err)
						}
						for _, set := range []func() error{
							func() error { return e.SetMode(EncoderModeCELT) },
							func() error { return e.SetBandwidth(BandwidthFullband) },
							func() error { return e.SetFrameSize(frameSize) },
							func() error { return e.SetBitrate(bitrate) },
							func() error { return e.SetComplexity(0) },
							func() error { return e.SetBitrateMode(rateMode.mode) },
							func() error { return e.SetForceChannels(channels) },
						} {
							if err := set(); err != nil {
								t.Fatal(err)
							}
						}
						packet := make([]byte, 4000)
						stride := frameSize * channels
						for f := range frames {
							n, err := e.EncodeInt16(pcm[f*stride:(f+1)*stride], packet)
							if err != nil || ref[f].Status != 0 || n != len(ref[f].Packet) ||
								e.FinalRange() != ref[f].FinalRange || !bytes.Equal(packet[:n], ref[f].Packet) {
								t.Fatalf("frame %d err=%v len=%d/%d range=%08x/%08x", f, err,
									n, len(ref[f].Packet), e.FinalRange(), ref[f].FinalRange)
							}
						}
						last := pcm[(frames-1)*stride:]
						var lastN int
						var lastErr error
						for range 3 {
							lastN, lastErr = e.EncodeInt16(last, packet)
							if lastErr != nil || lastN < 2 {
								t.Fatalf("warm encode len=%d err=%v", lastN, lastErr)
							}
						}
						allocs := testing.AllocsPerRun(20, func() {
							lastN, lastErr = e.EncodeInt16(last, packet)
						})
						if lastErr != nil || lastN < 2 || allocs != 0 {
							t.Fatalf("active long caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
						}
					})
				}
			}
		}
	}
}

func TestPublicFixedInputAPIsShareQ8History(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 9
	short := genPCMInt16Pub(91247, 1, frameSize, frames, false)
	mixed := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
	for f := range frames {
		pcm16 := short[f*frameSize : (f+1)*frameSize]
		mixed[f].Format = uint32(f % 3)
		mixed[f].ResetBefore = f == 6
		switch mixed[f].Format {
		case 0:
			mixed[f].ShortPCM = pcm16
		case 1:
			mixed[f].FloatPCM = make([]float32, frameSize)
			for i, sample := range pcm16 {
				mixed[f].FloatPCM[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
			}
		case 2:
			mixed[f].PCM24 = make([]int32, frameSize)
			for i, sample := range pcm16 {
				mixed[f].PCM24[i] = int32(sample)<<8 + int32(i%17-8)
			}
		}
	}
	ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize,
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
			first := 0
			for first < n && first < len(ref[f].Packet) && packet[first] == ref[f].Packet[first] {
				first++
			}
			t.Fatalf("frame %d format=%d err=%v len=%d/%d first=%d range=%08x/%08x", f,
				frame.Format, err, n, len(ref[f].Packet), first, e.FinalRange(), ref[f].FinalRange)
		}
	}
	var lastErr error
	var lastN int
	encodeCycle := func() {
		lastN, lastErr = e.EncodeInt16(mixed[0].ShortPCM, packet)
		if lastErr != nil {
			return
		}
		lastN, lastErr = e.Encode(mixed[1].FloatPCM, packet)
		if lastErr != nil {
			return
		}
		lastN, lastErr = e.EncodeInt24(mixed[2].PCM24, packet)
	}
	for range 3 {
		encodeCycle()
		if lastErr != nil || lastN < 2 {
			t.Fatalf("warm mixed encode len=%d err=%v", lastN, lastErr)
		}
	}
	allocs := testing.AllocsPerRun(20, encodeCycle)
	if lastErr != nil || lastN < 2 || allocs != 0 {
		t.Fatalf("active mixed caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
	}
}

// TestPublicFixedStereoWidthFadeMatchesLibopus exercises the fixed Q15
// stereo_fade path that CELT uses when its effective rate narrows stereo width.
func TestPublicFixedStereoWidthFadeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames, channels = 960, 3, 2
	pcm := genPCMInt16Pub(11849, channels, frameSize, frames, false)
	for _, app := range []struct {
		pub Application
		ref int
	}{
		{ApplicationAudio, libopustest.OpusApplicationAudio},
		{ApplicationLowDelay, libopustest.OpusApplicationRestrictedLowDelay},
	} {
		ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
			SampleRate: 48000, Channels: channels, Application: app.ref,
			MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
			Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 32000, Complexity: 0,
			ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
		})
		if err != nil {
			t.Fatal(err)
		}
		e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: app.pub})
		if err != nil {
			t.Fatal(err)
		}
		for _, set := range []func() error{
			func() error { return e.SetMode(EncoderModeCELT) },
			func() error { return e.SetBandwidth(BandwidthFullband) },
			func() error { return e.SetFrameSize(frameSize) },
			func() error { return e.SetComplexity(0) },
			func() error { return e.SetBitrate(32000) },
			func() error { return e.SetBitrateMode(BitrateModeCBR) },
			func() error { return e.SetForceChannels(channels) },
		} {
			if err := set(); err != nil {
				t.Fatal(err)
			}
		}
		packet := make([]byte, 4000)
		for frame := range frames {
			input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
			n, err := e.EncodeInt16(input, packet)
			if err != nil || ref[frame].Status != 0 || n != len(ref[frame].Packet) ||
				e.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
				t.Fatalf("app=%d frame=%d err=%v len=%d/%d range=%08x/%08x", app.ref, frame,
					err, n, len(ref[frame].Packet), e.FinalRange(), ref[frame].FinalRange)
			}
		}
		activeInput := pcm[:frameSize*channels]
		var lastN int
		var lastErr error
		for range 3 {
			lastN, lastErr = e.EncodeInt16(activeInput, packet)
			if lastErr != nil || lastN < 2 {
				t.Fatalf("warm stereo fade len=%d err=%v", lastN, lastErr)
			}
		}
		allocs := testing.AllocsPerRun(20, func() {
			lastN, lastErr = e.EncodeInt16(activeInput, packet)
		})
		if lastErr != nil || lastN < 2 || allocs != 0 {
			t.Fatalf("active stereo fade len=%d err=%v allocs=%g", lastN, lastErr, allocs)
		}
	}
}
