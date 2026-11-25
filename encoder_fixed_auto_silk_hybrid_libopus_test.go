//go:build gopus_fixed_point

package gopus

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"testing"
)

func TestPublicFixedAutoSILKHybridSequencesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name, wantMode string
		sampleRate     int
		channels       int
		frameSize      int
		bitrate        int
		application    Application
		oracleApp      int
	}{
		{
			name: "voip-16k-mono-low-rate", wantMode: "silk",
			sampleRate: 16000, channels: 1, frameSize: 320, bitrate: 12000,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
		},
		{
			name: "voip-48k-mono-medium-rate", wantMode: "hybrid",
			sampleRate: 48000, channels: 1, frameSize: 960, bitrate: 32000,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
		},
	}
	const frameCount = 8
	const resetFrame = 5
	const maxPacketBytes = 4000
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := testsignal.GenerateEncoderSignalVariant(
				testsignal.EncoderVariantSpeechLikeV1,
				tc.sampleRate,
				tc.frameSize*tc.channels*frameCount,
				tc.channels,
			)
			if err != nil {
				t.Fatal(err)
			}
			frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
			for frame := range frameCount {
				start := frame * tc.frameSize * tc.channels
				frames[frame] = libopustest.OpusEncodeFixedMixedFrame{
					Format: 1, FloatPCM: pcm[start : start+tc.frameSize*tc.channels],
					ResetBefore: frame == resetFrame,
				}
			}
			params := libopustest.OpusEncodeFixedParams{
				SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.oracleApp,
				MaxPacketBytes: maxPacketBytes, Bitrate: tc.bitrate, Complexity: 10,
				VBR: true, VBRConstraint: false, LSBDepth: 24,
				FrameSize: tc.frameSize, FrameCount: frameCount,
			}
			var want []libopustest.OpusEncodeFixedRecord
			if extsupport.QEXT {
				want, err = libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(params, frames)
			} else {
				want, err = libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
			}
			if err != nil {
				libopustest.HelperUnavailable(t, "persistent fixed automatic SILK/Hybrid encoder", err)
				return
			}
			if len(want) != frameCount {
				t.Fatalf("selected fixed C records=%d, want %d", len(want), frameCount)
			}

			enc, err := NewEncoder(EncoderConfig{
				SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.application,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeAuto) },
				func() error { return enc.SetBandwidthAuto() },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetBitrate(tc.bitrate) },
				func() error { return enc.SetComplexity(10) },
				func() error { return enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return enc.SetLSBDepth(24) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}

			packet := make([]byte, maxPacketBytes)
			seen := map[string]bool{}
			encodeSequence := func(label string) {
				t.Helper()
				for frame, input := range frames {
					if input.ResetBefore {
						enc.Reset()
					}
					n, err := enc.Encode(input.FloatPCM, packet)
					if err != nil {
						t.Fatalf("%s frame %d Encode: %v", label, frame, err)
					}
					if want[frame].Status != 0 {
						t.Fatalf("%s frame %d selected fixed C status=%d", label, frame, want[frame].Status)
					}
					mode := fixedPacketMode(want[frame].Packet)
					seen[mode] = true
					if mode != "silk" && mode != "hybrid" {
						t.Fatalf("%s frame %d selected C mode=%s, want automatic SILK/Hybrid", label, frame, mode)
					}
					if n != len(want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange ||
						!bytes.Equal(packet[:n], want[frame].Packet) {
						first := 0
						for first < n && first < len(want[frame].Packet) && packet[first] == want[frame].Packet[first] {
							first++
						}
						t.Fatalf("%s frame %d auto %s packet/range mismatch: bytes Go/C=%d/%d first=%d range=%08x/%08x",
							label, frame, mode, n, len(want[frame].Packet), first, enc.FinalRange(), want[frame].FinalRange)
					}
				}
			}
			encodeSequence("initial")
			if !seen[tc.wantMode] {
				t.Fatalf("selected C sequence did not cover %s mode; modes=%v", tc.wantMode, seen)
			}

			enc.Reset()
			encodeSequence("reset replay")

			warmPCM := frames[frameCount-1].FloatPCM
			for range 2 {
				if _, err := enc.Encode(warmPCM, packet); err != nil {
					t.Fatalf("warm automatic %s encode: %v", tc.wantMode, err)
				}
			}
			var allocErr error
			allocs := testing.AllocsPerRun(20, func() {
				if _, err := enc.Encode(warmPCM, packet); err != nil {
					allocErr = err
				}
			})
			if allocErr != nil {
				t.Fatalf("warm automatic %s cycle: %v", tc.wantMode, allocErr)
			}
			if allocs != 0 {
				t.Fatalf("warmed automatic %s caller-buffer cycle allocated %g objects", tc.wantMode, allocs)
			}
		})
	}
}

func fixedPacketMode(packet []byte) string {
	if len(packet) == 0 {
		return "empty"
	}
	config := int(packet[0] >> 3)
	switch {
	case config < 12:
		return "silk"
	case config < 16:
		return "hybrid"
	default:
		return "celt"
	}
}

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

// TestPublicFixedVoIPShortCELTMatchesLibopus covers the VoIP high-pass filter
// on the public short-input path while CELT is forced for every packet.
func TestPublicFixedVoIPShortCELTMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels%d", channels), func(t *testing.T) {
			pcm := genPCMInt16Pub(uint32(53417+channels*31), channels, frameSize, frames, false)
			bitrate := 64000 * channels
			ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationVoIP,
				MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
				Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 0,
				ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
			})
			if err != nil {
				t.Fatal(err)
			}
			e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationVoIP})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return e.SetMode(EncoderModeCELT) },
				func() error { return e.SetBandwidth(BandwidthFullband) },
				func() error { return e.SetFrameSize(frameSize) },
				func() error { return e.SetComplexity(0) },
				func() error { return e.SetBitrate(bitrate) },
				func() error { return e.SetBitrateMode(BitrateModeCBR) },
				func() error { return e.SetForceChannels(channels) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			packet := make([]byte, 4000)
			stride := frameSize * channels
			for frame := range frames {
				n, err := e.EncodeInt16(pcm[frame*stride:(frame+1)*stride], packet)
				if err != nil || ref[frame].Status != 0 || n != len(ref[frame].Packet) ||
					e.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
					t.Fatalf("frame %d err=%v len=%d/%d range=%08x/%08x", frame, err,
						n, len(ref[frame].Packet), e.FinalRange(), ref[frame].FinalRange)
				}
			}

			activeInput := pcm[(frames-1)*stride:]
			var lastN int
			var lastErr error
			for range 3 {
				lastN, lastErr = e.EncodeInt16(activeInput, packet)
				if lastErr != nil || lastN < 2 {
					t.Fatalf("warm VoIP CELT encode len=%d err=%v", lastN, lastErr)
				}
			}
			allocs := testing.AllocsPerRun(20, func() {
				lastN, lastErr = e.EncodeInt16(activeInput, packet)
			})
			if lastErr != nil || lastN < 2 || allocs != 0 {
				t.Fatalf("active VoIP CELT caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
			}
		})
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
		{ApplicationVoIP, libopustest.OpusApplicationVoIP},
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

func TestPublicFixedVoIPInputAPIsShareQ8History(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 9
	for _, channels := range []int{1, 2} {
		for _, complexity := range []int{0, 10} {
			t.Run(fmt.Sprintf("channels%d/complexity%d", channels, complexity), func(t *testing.T) {
				stride := frameSize * channels
				short := genPCMInt16Pub(uint32(91247+channels*13+complexity*31), channels, frameSize, frames, false)
				mixed := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
				for f := range frames {
					pcm16 := short[f*stride : (f+1)*stride]
					mixed[f].Format = uint32(f % 3)
					mixed[f].ResetBefore = f == 6
					switch mixed[f].Format {
					case 0:
						mixed[f].ShortPCM = pcm16
					case 1:
						mixed[f].FloatPCM = make([]float32, stride)
						for i, sample := range pcm16 {
							mixed[f].FloatPCM[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
						}
					case 2:
						mixed[f].PCM24 = make([]int32, stride)
						for i, sample := range pcm16 {
							mixed[f].PCM24[i] = int32(sample)<<8 + int32(i%17-8)
						}
					}
				}
				bitrate := 64000 * channels
				ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
					SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationVoIP,
					MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
					Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: complexity,
					ForceChannels: channels, FrameSize: frameSize,
				}, mixed)
				if err != nil {
					t.Fatal(err)
				}
				e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationVoIP})
				if err != nil {
					t.Fatal(err)
				}
				for _, set := range []func() error{
					func() error { return e.SetMode(EncoderModeCELT) },
					func() error { return e.SetBandwidth(BandwidthFullband) },
					func() error { return e.SetFrameSize(frameSize) },
					func() error { return e.SetComplexity(complexity) },
					func() error { return e.SetBitrate(bitrate) },
					func() error { return e.SetBitrateMode(BitrateModeCBR) },
					func() error { return e.SetForceChannels(channels) },
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

				var lastN int
				var lastErr error
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
						t.Fatalf("warm mixed VoIP encode len=%d err=%v", lastN, lastErr)
					}
				}
				allocs := testing.AllocsPerRun(20, encodeCycle)
				if lastErr != nil || lastN < 2 || allocs != 0 {
					t.Fatalf("active mixed VoIP caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
				}
			})
		}
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

type publicFixedOuterCase struct {
	name          string
	mode          EncoderMode
	oracleMode    int
	bandwidth     Bandwidth
	oracleBW      int
	sampleRate    int
	channels      int
	forceChannels int
	frameSize     int
	frames        int
	resetFrame    int
	application   Application
	oracleApp     int
	bitrate       int
	complexity    int
	bitrateMode   BitrateMode
}

func TestPublicFixedSILKHybridInputAPIsMatchLibopus(t *testing.T) {
	const frames = 12
	cases := []publicFixedOuterCase{
		{name: "silk-nb", mode: EncoderModeSILK, oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthNarrowband, oracleBW: libopustest.OpusBandwidthNarrowband},
		{name: "silk-mb", mode: EncoderModeSILK, oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthMediumband, oracleBW: libopustest.OpusBandwidthMediumband},
		{name: "silk-wb", mode: EncoderModeSILK, oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthWideband, oracleBW: libopustest.OpusBandwidthWideband},
		{name: "hybrid-swb", mode: EncoderModeHybrid, oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthSuperwideband, oracleBW: libopustest.OpusBandwidthSuperwideband},
		{name: "hybrid-fb", mode: EncoderModeHybrid, oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthFullband, oracleBW: libopustest.OpusBandwidthFullband},
	}
	for _, tc := range cases {
		for _, channels := range []int{1, 2} {
			tc, channels := tc, channels
			tc.sampleRate = 48000
			tc.channels = channels
			tc.forceChannels = channels
			tc.frameSize = 960
			tc.frames = frames
			tc.resetFrame = 9
			tc.application = ApplicationAudio
			tc.oracleApp = libopustest.OpusApplicationAudio
			tc.bitrate = 24000 * channels
			tc.complexity = 10
			tc.bitrateMode = BitrateModeCBR
			runPublicFixedOuterCase(t, tc)
		}
	}
}

func TestPublicFixedSILKHybridRatesDurationsAndDownmixMatchLibopus(t *testing.T) {
	cases := []publicFixedOuterCase{
		{
			name: "silk-16k-10ms-voip-downmix-vbr", mode: EncoderModeSILK,
			oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthWideband,
			oracleBW: libopustest.OpusBandwidthWideband, sampleRate: 16000, channels: 2,
			forceChannels: 1, frameSize: 160, frames: 6, resetFrame: 3,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
			bitrate: 24000, complexity: 0, bitrateMode: BitrateModeVBR,
		},
		{
			name: "hybrid-24k-10ms-audio-cvbr", mode: EncoderModeHybrid,
			oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthSuperwideband,
			oracleBW: libopustest.OpusBandwidthSuperwideband, sampleRate: 24000, channels: 2,
			forceChannels: 2, frameSize: 240, frames: 6, resetFrame: 3,
			application: ApplicationAudio, oracleApp: libopustest.OpusApplicationAudio,
			bitrate: 48000, complexity: 10, bitrateMode: BitrateModeCVBR,
		},
		{
			name: "hybrid-48k-40ms-audio-cvbr", mode: EncoderModeHybrid,
			oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthFullband,
			oracleBW: libopustest.OpusBandwidthFullband, sampleRate: 48000, channels: 2,
			forceChannels: 2, frameSize: 1920, frames: 4, resetFrame: 2,
			application: ApplicationAudio, oracleApp: libopustest.OpusApplicationAudio,
			bitrate: 64000, complexity: 10, bitrateMode: BitrateModeCVBR,
		},
		{
			name: "silk-48k-60ms-voip-vbr", mode: EncoderModeSILK,
			oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthWideband,
			oracleBW: libopustest.OpusBandwidthWideband, sampleRate: 48000, channels: 1,
			forceChannels: 1, frameSize: 2880, frames: 4, resetFrame: 2,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
			bitrate: 32000, complexity: 10, bitrateMode: BitrateModeVBR,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runPublicFixedOuterCase(t, tc) })
	}
}

func TestPublicFixedSILKHybridModeTransitionsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		bitrate    = 64000
		complexity = 10
		maxBytes   = 4000
	)
	type control struct {
		mode       EncoderMode
		oracleMode int
		bandwidth  Bandwidth
		oracleBW   int
	}
	controls := []control{
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeHybrid, libopustest.OpusForceModeHybrid, BandwidthSuperwideband, libopustest.OpusBandwidthSuperwideband},
		{EncoderModeHybrid, libopustest.OpusForceModeHybrid, BandwidthSuperwideband, libopustest.OpusBandwidthSuperwideband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeSILK, libopustest.OpusForceModeSILKOnly, BandwidthWideband, libopustest.OpusBandwidthWideband},
		{EncoderModeSILK, libopustest.OpusForceModeSILKOnly, BandwidthWideband, libopustest.OpusBandwidthWideband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
	}

	inputs := make([]libopustest.OpusEncodeFixedMixedFrame, len(controls))
	for frame := range controls {
		pcm := make([]float32, frameSize*channels)
		for i, sample := range genPCMInt16Pub(uint32(0x7911+frame*131), channels, frameSize, 1, false) {
			pcm[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
		}
		inputs[frame] = libopustest.OpusEncodeFixedMixedFrame{
			Format: 1, FloatPCM: pcm,
			ForceMode: controls[frame].oracleMode,
			Bandwidth: controls[frame].oracleBW,
		}
	}
	want, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: maxBytes, Bitrate: bitrate, Complexity: complexity,
		VBR: false, VBRConstraint: false, ForceChannels: channels,
		FrameSize: frameSize, LSBDepth: 24,
	}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(controls) {
		t.Fatalf("selected fixed C records=%d want %d", len(want), len(controls))
	}

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return enc.SetBitrate(bitrate) },
		func() error { return enc.SetComplexity(complexity) },
		func() error { return enc.SetBitrateMode(BitrateModeCBR) },
		func() error { return enc.SetForceChannels(channels) },
		func() error { return enc.SetLSBDepth(24) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, maxBytes)
	matched := 0
	for frame, ctl := range controls {
		if err := enc.SetBandwidth(ctl.bandwidth); err != nil {
			t.Fatalf("frame %d SetBandwidth: %v", frame, err)
		}
		if err := enc.SetMaxBandwidth(ctl.bandwidth); err != nil {
			t.Fatalf("frame %d SetMaxBandwidth: %v", frame, err)
		}
		if err := enc.SetMode(ctl.mode); err != nil {
			t.Fatalf("frame %d SetMode: %v", frame, err)
		}
		n, err := enc.Encode(inputs[frame].FloatPCM, packet)
		if err != nil {
			t.Fatalf("frame %d mode=%v: Encode: %v", frame, ctl.mode, err)
		}
		if want[frame].Status != 0 || n != len(want[frame].Packet) ||
			enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(packet[:n], want[frame].Packet) {
			first := 0
			for first < n && first < len(want[frame].Packet) && packet[first] == want[frame].Packet[first] {
				first++
			}
			t.Fatalf("frame %d mode=%v/%d bandwidth=%v/%d: packet/range mismatch status=%d len Go/C=%d/%d firstdiff=%d range Go/C=%08x/%08x",
				frame, ctl.mode, ctl.oracleMode, ctl.bandwidth, ctl.oracleBW,
				want[frame].Status, n, len(want[frame].Packet), first,
				enc.FinalRange(), want[frame].FinalRange)
		}
		matched++
	}
	t.Logf("persistent forced-mode transitions matched %d/%d packets and ranges", matched, len(controls))

	var allocErr error
	allocs := testing.AllocsPerRun(20, func() {
		for frame, ctl := range controls {
			if err := enc.SetBandwidth(ctl.bandwidth); err != nil {
				allocErr = err
				return
			}
			if err := enc.SetMaxBandwidth(ctl.bandwidth); err != nil {
				allocErr = err
				return
			}
			if err := enc.SetMode(ctl.mode); err != nil {
				allocErr = err
				return
			}
			if _, err := enc.Encode(inputs[frame].FloatPCM, packet); err != nil {
				allocErr = err
				return
			}
		}
	})
	if allocErr != nil {
		t.Fatalf("warm transition cycle: %v", allocErr)
	}
	if allocs != 0 {
		t.Fatalf("warm persistent forced-mode transition cycle allocated %.2f times, want 0", allocs)
	}
}

func runPublicFixedOuterCase(t *testing.T, tc publicFixedOuterCase) {
	t.Helper()
	libopustest.RequireOracle(t)
	stride := tc.frameSize * tc.channels
	short := genPCMInt16Pub(uint32(0x6a19+tc.channels*29+len(tc.name)*41+tc.sampleRate), tc.channels, tc.frameSize, tc.frames, false)
	mixed := make([]libopustest.OpusEncodeFixedMixedFrame, tc.frames)
	for frame := range tc.frames {
		pcm16 := short[frame*stride : (frame+1)*stride]
		mixed[frame].Format = uint32(frame % 3)
		mixed[frame].ResetBefore = frame == tc.resetFrame
		switch mixed[frame].Format {
		case 0:
			mixed[frame].ShortPCM = pcm16
		case 1:
			mixed[frame].FloatPCM = make([]float32, stride)
			for i, sample := range pcm16 {
				mixed[frame].FloatPCM[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
			}
		case 2:
			mixed[frame].PCM24 = make([]int32, stride)
			for i, sample := range pcm16 {
				mixed[frame].PCM24[i] = int32(sample)*256 + int32(i%17-8)
			}
		}
	}

	vbr := tc.bitrateMode != BitrateModeCBR
	vbrConstraint := tc.bitrateMode == BitrateModeCVBR
	const maxPacketBytes = 4000
	ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.oracleApp,
		MaxPacketBytes: maxPacketBytes, ForceMode: tc.oracleMode, Bandwidth: tc.oracleBW,
		Bitrate: tc.bitrate, Complexity: tc.complexity, VBR: vbr, VBRConstraint: vbrConstraint,
		ForceChannels: tc.forceChannels, FrameSize: tc.frameSize, LSBDepth: 24,
	}, mixed)
	if err != nil {
		t.Fatal(err)
	}
	if len(ref) != tc.frames {
		t.Fatalf("selected fixed C records=%d want %d", len(ref), tc.frames)
	}

	enc := newConfiguredPublicFixedOuterEncoder(t, tc)
	packet := make([]byte, maxPacketBytes)
	divergent := 0
	firstDivergence := -1
	for frame, input := range mixed {
		if input.ResetBefore {
			enc.Reset()
		}
		n, callErr := encodePublicFixedOuterInput(enc, input, packet)
		if callErr != nil {
			t.Fatalf("frame %d format=%d Encode: %v", frame, input.Format, callErr)
		}
		if ref[frame].Status != 0 {
			t.Fatalf("frame %d C status=%d", frame, ref[frame].Status)
		}
		if n <= 0 {
			t.Fatalf("frame %d format=%d Go packet length=%d", frame, input.Format, n)
		}
		if n != len(ref[frame].Packet) || enc.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
			divergent++
			if firstDivergence < 0 {
				firstDivergence = frame
				first := 0
				for first < n && first < len(ref[frame].Packet) && packet[first] == ref[frame].Packet[first] {
					first++
				}
				gotTOC, wantTOC := byte(0), byte(0)
				if n > 0 {
					gotTOC = packet[0]
				}
				if len(ref[frame].Packet) > 0 {
					wantTOC = ref[frame].Packet[0]
				}
				t.Logf("first mismatch frame=%d format=%d reset=%t packet len Go/C=%d/%d first-diff=%d TOC Go/C=%02x/%02x range Go/C=%08x/%08x",
					frame, input.Format, input.ResetBefore, n, len(ref[frame].Packet), first,
					gotTOC, wantTOC, enc.FinalRange(), ref[frame].FinalRange)
			}
		}
	}
	t.Logf("%s frames=%d matched=%d divergent=%d reset-before=%d", tc.name, tc.frames, tc.frames-divergent, divergent, tc.resetFrame)
	if divergent != 0 {
		t.Errorf("%d/%d public packets or final ranges differ from selected fixed libopus", divergent, tc.frames)
	}
	assertPublicFixedOuterZeroAllocs(t, tc, mixed)
}

func newConfiguredPublicFixedOuterEncoder(t *testing.T, tc publicFixedOuterCase) *Encoder {
	t.Helper()
	enc, err := NewEncoder(EncoderConfig{SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.application})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return enc.SetMode(tc.mode) },
		func() error { return enc.SetBandwidth(tc.bandwidth) },
		func() error { return enc.SetMaxBandwidth(tc.bandwidth) },
		func() error { return enc.SetFrameSize(tc.frameSize) },
		func() error { return enc.SetBitrate(tc.bitrate) },
		func() error { return enc.SetComplexity(tc.complexity) },
		func() error { return enc.SetBitrateMode(tc.bitrateMode) },
		func() error { return enc.SetForceChannels(tc.forceChannels) },
		func() error { return enc.SetLSBDepth(24) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	return enc
}

func encodePublicFixedOuterInput(enc *Encoder, input libopustest.OpusEncodeFixedMixedFrame, packet []byte) (int, error) {
	switch input.Format {
	case 0:
		return enc.EncodeInt16(input.ShortPCM, packet)
	case 1:
		return enc.Encode(input.FloatPCM, packet)
	case 2:
		return enc.EncodeInt24(input.PCM24, packet)
	default:
		return 0, fmt.Errorf("unknown input format %d", input.Format)
	}
}

func assertPublicFixedOuterZeroAllocs(t *testing.T, tc publicFixedOuterCase, mixed []libopustest.OpusEncodeFixedMixedFrame) {
	t.Helper()
	for format := uint32(0); format < 3; format++ {
		input := libopustest.OpusEncodeFixedMixedFrame{}
		for _, candidate := range mixed {
			if candidate.Format == format {
				input = candidate
				break
			}
		}
		if input.Format != format {
			t.Fatalf("case %s lacks format %d input for allocation check", tc.name, format)
		}
		enc := newConfiguredPublicFixedOuterEncoder(t, tc)
		packet := make([]byte, 4000)
		if _, err := encodePublicFixedOuterInput(enc, input, packet); err != nil {
			t.Fatalf("case %s format %d warm encode: %v", tc.name, format, err)
		}
		var callErr error
		allocs := testing.AllocsPerRun(20, func() {
			_, callErr = encodePublicFixedOuterInput(enc, input, packet)
		})
		if callErr != nil {
			t.Fatalf("case %s format %d measured encode: %v", tc.name, format, callErr)
		}
		if allocs != 0 {
			t.Errorf("case %s format %d steady-state allocations=%g want 0", tc.name, format, allocs)
		}
	}
}

func xorshift32Pub(state *uint32) uint32 {
	s := *state
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*state = s
	return s
}

// genPCMInt16Pub builds nframes of interleaved int16 PCM with periodic transient
// spikes, matching the fixedpoint oracle generator's character.
func genPCMInt16Pub(seed uint32, channels, frameSize, nframes int, transient bool) []int16 {
	state := seed
	pcm := make([]int16, channels*frameSize*nframes)
	perFrame := channels * frameSize
	for f := 0; f < nframes; f++ {
		for i := 0; i < perFrame; i++ {
			v := int32(xorshift32Pub(&state))
			s := int16(v >> 19)
			if transient && f%2 == 1 && i > perFrame/2 && i < perFrame/2+40 {
				s = int16(v >> 12)
			}
			pcm[f*perFrame+i] = s
		}
	}
	return pcm
}

// TestPublicEncodeFixedCELTLibopusParity validates the gopus_fixed_point public
// encode seam: a CELT-mode (ApplicationRestrictedCelt, full-band 48 kHz) frame
// routed through Encoder.Encode produces a byte-exact CELT packet versus the
// FIXED_POINT libopus celt_encode_with_ec reference.
//
// The selected C reference receives the exact opus_res Q8 input and per-frame
// analysis consumed by the integer CELT encoder. The test keeps C state across
// the same five-frame sequence and compares every complete payload and range.
func TestPublicEncodeFixedCELTLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		start   = 0
		end     = 21
		nframes = 5
	)

	type kase struct {
		channels   int
		lm         int
		bitrate    int
		complexity int
		mode       BitrateMode
		transient  bool
	}
	var cases []kase
	for _, ch := range []int{1, 2} {
		for _, lm := range []int{0, 2, 3} { // 120, 480, 960 samples
			for _, br := range []int{32000, 64000, 128000} {
				for _, cx := range []int{0, 5, 10} {
					for _, m := range []BitrateMode{BitrateModeVBR, BitrateModeCBR, BitrateModeCVBR} {
						cases = append(cases, kase{channels: ch, lm: lm, bitrate: br,
							complexity: cx, mode: m, transient: lm == 3})
					}
				}
			}
		}
	}

	for _, c := range cases {
		frameSize := 120 << c.lm
		seed := uint32(0x51EED + c.lm*131 + c.bitrate + c.complexity*7 + c.channels*1009 + int(c.mode)*97)
		if c.transient {
			seed ^= 0x33333333
		}
		pcm16 := genPCMInt16Pub(seed, c.channels, frameSize, nframes, c.transient)

		enc, err := NewEncoder(EncoderConfig{
			SampleRate:  48000,
			Channels:    c.channels,
			Application: ApplicationRestrictedCelt,
		})
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		if err := enc.SetComplexity(c.complexity); err != nil {
			t.Fatalf("SetComplexity: %v", err)
		}
		if err := enc.SetBitrate(c.bitrate); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetBitrateMode(c.mode); err != nil {
			t.Fatalf("SetBitrateMode: %v", err)
		}
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize: %v", err)
		}
		// The standalone C CELT reference fixes its coded channels and end
		// band; use the same public controls before capturing the inner input.
		if err := enc.SetForceChannels(c.channels); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}

		// Drive every frame through the public encoder, capturing the Q8 input,
		// analysis, and CELT payload before the next encode reuses scratch.
		perFrame := c.channels * frameSize
		fed := make([]libopustest.CELTFixedQ8Frame, nframes)
		payloads := make([][]byte, nframes)
		ranges := make([]uint32, nframes)
		f32 := make([]float32, perFrame)
		out := make([]byte, 4000)
		innerBitrate, lsbDepth := 0, 0
		for f := 0; f < nframes; f++ {
			for i := 0; i < perFrame; i++ {
				f32[i] = float32(pcm16[f*perFrame+i]) / 32768.0
			}
			n, err := enc.Encode(f32, out)
			if err != nil {
				t.Fatalf("Encode frame %d: %v", f, err)
			}
			input := enc.enc.LastFixedCELTInputQ8()
			if len(input) != perFrame {
				t.Fatalf("ch=%d lm=%d frame=%d: integer CELT Q8 input len=%d want %d",
					c.channels, c.lm, f, len(input), perFrame)
			}
			rate, maxBytes, depth := enc.enc.LastFixedCELTControls()
			if f == 0 {
				innerBitrate, lsbDepth = rate, depth
			} else if rate != innerBitrate || depth != lsbDepth {
				t.Fatalf("frame %d: inner controls changed bitrate=%d/%d depth=%d/%d", f, rate, innerBitrate, depth, lsbDepth)
			}
			analysis := enc.enc.LastFixedCELTAnalysis()
			fed[f] = libopustest.CELTFixedQ8Frame{
				PCM: append([]int32(nil), input...), MaxBytes: maxBytes,
				Analysis: libopustest.CELTFixedQ8Analysis{
					Valid: analysis.Valid, Tonality: analysis.Tonality,
					TonalitySlope: analysis.TonalitySlope, Noisiness: analysis.NoisySpeech,
					Activity: analysis.Activity, MusicProb: analysis.MusicProb,
					MusicProbMin: analysis.MusicProbMin, MusicProbMax: analysis.MusicProbMax,
					Bandwidth: analysis.BandwidthIndex, ActivityProbability: analysis.VADProb,
					MaxPitchRatio: analysis.MaxPitchRatio, LeakBoost: analysis.LeakBoost,
				},
			}
			if n < 1 {
				t.Fatalf("Encode frame %d: short packet %d", f, n)
			}
			payloads[f] = append([]byte(nil), out[1:n]...)
			ranges[f] = enc.FinalRange()
		}

		vbr := c.mode != BitrateModeCBR
		constrained := c.mode == BitrateModeCVBR

		probe := libopustest.ProbeCELTFixedRawQ8
		if extsupport.QEXT {
			probe = libopustest.ProbeCELTFixedQEXTQ8
		}
		want, err := probe(libopustest.CELTFixedQ8Params{
			SampleRate: 48000, Channels: c.channels, FrameSize: frameSize,
			Start: start, End: end, Bitrate: innerBitrate, Complexity: c.complexity,
			LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: constrained, Frames: fed,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
			return
		}
		if len(want) != nframes {
			t.Fatalf("raw Q8 oracle records=%d want %d", len(want), nframes)
		}

		for f := 0; f < nframes; f++ {
			label := fmt.Sprintf("ch=%d lm=%d br=%d cx=%d mode=%v frame=%d",
				c.channels, c.lm, c.bitrate, c.complexity, c.mode, f)
			got := payloads[f]
			if !bytes.Equal(got, want[f].Packet) || ranges[f] != want[f].FinalRange {
				mismatch := 0
				for mismatch < min(len(got), len(want[f].Packet)) && got[mismatch] == want[f].Packet[mismatch] {
					mismatch++
				}
				t.Errorf("%s: CELT payload/range mismatch at byte %d (gopus len=%d libopus len=%d range=%08x/%08x)",
					label, mismatch, len(got), len(want[f].Packet), ranges[f], want[f].FinalRange)
				break
			}
		}
	}
}
