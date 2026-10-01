//go:build gopus_fixed_point

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
