//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

func TestFixedCELTEnergyMaskFloatBoundaryConversion(t *testing.T) {
	tests := []struct {
		name  string
		value float32
		want  int32
		ok    bool
	}{
		{"zero", 0, 0, true},
		{"positive integer", 1, 1 << 24, true},
		{"negative integer GCONST rounding", -1, -(1 << 24) + 1, true},
		{"positive fractional", 0.1, 1677722, true},
		{"negative fractional", -0.1, -1677721, true},
		{"positive half unit", math.Float32frombits(0x33000000), 1, true},
		{"negative half unit", -math.Float32frombits(0x33000000), 0, true},
		{"negative int32 edge", -128, -(1 << 31) + 1, true},
		{"positive int32 overflow", 128, 0, false},
		{"nan", math.Float32frombits(0x7fc00000), 0, false},
		{"infinity", math.Float32frombits(0x7f800000), 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := fixedCELTEnergyMaskValue(tc.value)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("fixedCELTEnergyMaskValue(%08x)=(%d,%v), want (%d,%v)",
					math.Float32bits(tc.value), got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestPublicFixedCELTEnergyMaskAndLFEControlsMatchOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960

	tests := []struct {
		name     string
		channels int
		lfe      bool
		mask     []float32
	}{
		{name: "mono_lfe", channels: 1, lfe: true},
		{name: "stereo_surround_mask", channels: 2, mask: fixedCELTEnergyMaskTestValues(2)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, tc.channels)
			enc.SetMode(ModeCELT)
			enc.SetLowDelay(true)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetBitrate(128000)
			enc.SetBitrateMode(ModeVBR)
			enc.SetComplexity(5)
			enc.SetForceChannels(tc.channels)
			enc.SetLFE(tc.lfe)
			if tc.mask != nil {
				enc.SetCELTEnergyMask(tc.mask)
			}

			frames := make([]libopustest.CELTFixedQ8Frame, 3)
			packets := make([][]byte, len(frames))
			ranges := make([]uint32, len(frames))
			pcm := make([]float32, frameSize*tc.channels)
			for frame := range frames {
				if frame == 2 {
					enc.Reset()
					enc.SetMode(ModeCELT)
					// Reset clears the public mask. The fixed CELT state must not
					// reuse the previous Q24 control after it is recreated.
				}
				fillFixedExtensionPCM(pcm, uint32(0x4c464500+frame*97))
				packet, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d Encode: %v", frame, err)
				}
				if !enc.fixedCELTUsed || enc.fixedCELT == nil {
					t.Fatalf("frame %d did not use fixed CELT", frame)
				}
				packets[frame] = append([]byte(nil), packet[1:]...)
				ranges[frame] = enc.FinalRange()
				frames[frame] = fixedQ8OracleFrame(enc)
				frames[frame].ResetBefore = frame == 2
				if len(enc.celtEnergyMask) > 0 {
					frames[frame].EnergyMask = append([]int32(nil), enc.fixedEnergyMask...)
				}
			}

			if tc.mask != nil {
				allocs := testing.AllocsPerRun(100, func() {
					enc.setFixedCELTEnergyMask(enc.fixedCELT.enc)
				})
				if allocs != 0 {
					t.Fatalf("warmed fixed energy-mask conversion allocs=%g, want 0", allocs)
				}
			}

			endBand := 21
			if tc.lfe {
				endBand = 13
			}
			bitrate, _, lsbDepth := enc.LastFixedCELTControls()
			want, err := probePublicFixedCELTQ8(libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: tc.channels, StreamChannels: tc.channels,
				FrameSize: frameSize, Start: 0, End: endBand, Bitrate: bitrate,
				Complexity: 5, LSBDepth: lsbDepth, VBR: true, LFE: tc.lfe, Frames: frames,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed CELT extension raw Q8", err)
				return
			}
			if len(want) != len(frames) {
				t.Fatalf("raw Q8 oracle returned %d frames, want %d", len(want), len(frames))
			}
			for frame := range want {
				if !bytes.Equal(packets[frame], want[frame].Packet) || ranges[frame] != want[frame].FinalRange {
					t.Fatalf("frame %d packet/range mismatch: got len=%d range=%08x want len=%d range=%08x",
						frame, len(packets[frame]), ranges[frame], len(want[frame].Packet), want[frame].FinalRange)
				}
			}
		})
	}
}

func TestPublicFixedCELTEnergyMaskResetLifetimeMatchesOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 960
		channels  = 1
	)
	mask := fixedCELTEnergyMaskQ24TestValues(channels)
	frames := make([]libopustest.OpusEncodeFixedMixedFrame, 5)
	pcmFrames := make([][]float32, len(frames))
	for frame := range frames {
		pcm := make([]float32, frameSize*channels)
		fillFixedExtensionPCM(pcm, uint32(0x4d41534b+frame*131))
		pcmFrames[frame] = pcm
		frames[frame] = libopustest.OpusEncodeFixedMixedFrame{
			Format: 1, FloatPCM: pcm,
			ForceMode: libopustest.OpusForceModeCELTOnly,
		}
	}
	frames[0].ForceMode = libopustest.OpusForceModeHybrid
	frames[1].Bandwidth = libopustest.OpusBandwidthFullband
	frames[1].EnergyMaskAction = libopustest.OpusEnergyMaskSet
	frames[1].EnergyMask = mask
	frames[2].Bandwidth = libopustest.OpusBandwidthFullband
	frames[3].Bandwidth = libopustest.OpusBandwidthFullband
	frames[3].EnergyMaskAction = libopustest.OpusEnergyMaskSet
	frames[3].EnergyMask = mask
	frames[4].Bandwidth = libopustest.OpusBandwidthFullband
	frames[4].EnergyMaskAction = libopustest.OpusEnergyMaskClear

	want, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		Bitrate: 128000, Complexity: 5, Bandwidth: libopustest.OpusBandwidthSuperwideband,
		ForceChannels: channels, VBR: true, FrameSize: frameSize,
	}, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed public mask reset-lifetime oracle", err)
		return
	}
	if len(want) != len(frames) {
		t.Fatalf("fixed public oracle records=%d, want %d", len(want), len(frames))
	}

	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthSuperwideband)
	enc.SetMaxBandwidth(types.BandwidthFullband)
	enc.SetBitrate(128000)
	enc.SetBitrateMode(ModeVBR)
	enc.SetVBRConstraint(false)
	enc.SetForceChannels(channels)
	enc.SetComplexity(5)
	for frame, pcm := range pcmFrames {
		switch frame {
		case 1:
			enc.SetMode(ModeCELT)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetCELTEnergyMaskQ24(mask)
		case 3:
			// The transition prefill resets inner CELT energy_mask, so this
			// explicit setter must restore it for the next frame.
			enc.SetCELTEnergyMaskQ24(mask)
		case 4:
			// A nil control clears the pointer; it differs from leaving it alone.
			enc.SetCELTEnergyMaskQ24(nil)
		}
		got, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", frame, err)
		}
		if want[frame].Status != 0 || !bytes.Equal(got, want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
			diff := 0
			for diff < len(got) && diff < len(want[frame].Packet) && got[diff] == want[frame].Packet[diff] {
				diff++
			}
			t.Fatalf("frame %d fixed mask lifetime mismatch: status=%d packet=%d/%d first=%d toc=%02x/%02x range=%08x/%08x mode=%d prev=%d bw=%d mask=%t q24=%t fixed=%t",
				frame, want[frame].Status, len(got), len(want[frame].Packet), diff,
				got[0], want[frame].Packet[0], enc.FinalRange(), want[frame].FinalRange,
				enc.mode, enc.prevMode, enc.effectiveBandwidth(), enc.fixedMaskActive, enc.fixedMaskQ24, enc.fixedCELTUsed)
		}
		if frame < 2 && modeFixtureLabelFromConfig(int(got[0]>>3)) != "hybrid" {
			t.Fatalf("frame %d mode=%s, want hybrid transition frame", frame, modeFixtureLabelFromConfig(int(got[0]>>3)))
		}
		if frame >= 2 && modeFixtureLabelFromConfig(int(got[0]>>3)) != "celt" {
			t.Fatalf("frame %d mode=%s, want CELT after transition", frame, modeFixtureLabelFromConfig(int(got[0]>>3)))
		}
	}

	enc.SetCELTEnergyMaskQ24(mask)
	if _, err := enc.Encode(pcmFrames[len(pcmFrames)-1], frameSize); err != nil {
		t.Fatalf("warm public masked CELT encode: %v", err)
	}
	var encodeErr error
	encodeAllocs := testing.AllocsPerRun(100, func() {
		_, encodeErr = enc.Encode(pcmFrames[len(pcmFrames)-1], frameSize)
	})
	if encodeErr != nil {
		t.Fatalf("public masked CELT encode: %v", encodeErr)
	}
	if encodeAllocs != 0 {
		t.Fatalf("warmed public masked CELT encode allocated %g objects", encodeAllocs)
	}

	allocs := testing.AllocsPerRun(100, func() {
		enc.SetCELTEnergyMaskQ24(mask)
	})
	if allocs != 0 {
		t.Fatalf("warmed Q24 mask setter allocated %g objects", allocs)
	}
}

func TestPublicFixedCELTQ24MaskMatchesOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 960
		channels  = 1
	)
	mask := fixedCELTEnergyMaskQ24TestValues(channels)
	frames := make([]libopustest.OpusEncodeFixedMixedFrame, 3)
	pcmFrames := make([][]float32, len(frames))
	for frame := range frames {
		pcm := make([]float32, frameSize*channels)
		fillFixedExtensionPCM(pcm, uint32(0x51323400+frame*73))
		pcmFrames[frame] = pcm
		frames[frame] = libopustest.OpusEncodeFixedMixedFrame{Format: 1, FloatPCM: pcm}
	}
	frames[0].EnergyMaskAction = libopustest.OpusEnergyMaskSet
	frames[0].EnergyMask = mask
	frames[2].EnergyMaskAction = libopustest.OpusEnergyMaskClear
	want, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband,
		Bitrate: 128000, Complexity: 5, ForceChannels: channels, VBR: false, FrameSize: frameSize,
	}, frames)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed public Q24 mask oracle", err)
		return
	}
	clearFrames := append([]libopustest.OpusEncodeFixedMixedFrame(nil), frames...)
	clearFrames[0].EnergyMaskAction = libopustest.OpusEnergyMaskClear
	clearFrames[0].EnergyMask = nil
	cleared, clearErr := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband,
		Bitrate: 128000, Complexity: 5, ForceChannels: channels, VBR: false, FrameSize: frameSize,
	}, clearFrames)
	if clearErr != nil {
		t.Fatalf("clear-mask comparison helper: %v", clearErr)
	}
	if want[0].FinalRange == cleared[0].FinalRange && bytes.Equal(want[0].Packet, cleared[0].Packet) {
		t.Fatal("C public energy-mask setter did not change the packet")
	}
	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthFullband)
	enc.SetBitrate(128000)
	enc.SetBitrateMode(ModeCBR)
	enc.SetForceChannels(channels)
	enc.SetComplexity(5)
	enc.SetCELTEnergyMaskQ24(mask)
	for frame, pcm := range pcmFrames {
		if frame == 2 {
			enc.SetCELTEnergyMaskQ24(nil)
		}
		got, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", frame, err)
		}
		if want[frame].Status != 0 || !bytes.Equal(got, want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
			t.Fatalf("frame %d fixed Q24 mask mismatch: packet=%d/%d range=%08x/%08x",
				frame, len(got), len(want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
		}
		if !enc.fixedCELTUsed {
			t.Fatalf("frame %d did not use fixed CELT", frame)
		}
	}
}

func TestPublicFixedLFEMatchesOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 960
		channels  = 1
	)
	tests := []struct {
		name      string
		mode      Mode
		forceMode int
	}{
		{name: "requested_celt", mode: ModeCELT, forceMode: libopustest.OpusForceModeCELTOnly},
		{name: "requested_silk", mode: ModeSILK, forceMode: libopustest.OpusForceModeSILKOnly},
		{name: "requested_hybrid", mode: ModeHybrid, forceMode: libopustest.OpusForceModeHybrid},
		{name: "requested_auto", mode: ModeAuto, forceMode: libopustest.OpusForceModeAuto},
	}
	mask := fixedCELTEnergyMaskQ24TestValues(channels)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames := make([]libopustest.OpusEncodeFixedMixedFrame, 3)
			pcmFrames := make([][]float32, len(frames))
			for frame := range frames {
				pcm := make([]float32, frameSize*channels)
				fillFixedExtensionPCM(pcm, uint32(0x4c464500+frame*97))
				pcmFrames[frame] = pcm
				frames[frame] = libopustest.OpusEncodeFixedMixedFrame{Format: 1, FloatPCM: pcm}
			}
			frames[0].EnergyMaskAction = libopustest.OpusEnergyMaskSet
			frames[0].EnergyMask = mask
			frames[2].EnergyMaskAction = libopustest.OpusEnergyMaskClear
			want, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
				ForceMode: tc.forceMode, Bitrate: 64000, Complexity: 5,
				ForceChannels: channels, VBR: true, LFE: true, FrameSize: frameSize,
			}, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed public LFE oracle", err)
				return
			}
			enc := NewEncoder(48000, channels)
			enc.SetMode(tc.mode)
			enc.SetBitrate(64000)
			enc.SetBitrateMode(ModeVBR)
			enc.SetForceChannels(channels)
			enc.SetComplexity(5)
			enc.SetLFE(true)
			enc.SetCELTEnergyMaskQ24(mask)
			for frame, pcm := range pcmFrames {
				if frame == 2 {
					enc.SetCELTEnergyMaskQ24(nil)
				}
				got, err := enc.Encode(pcm, frameSize)
				if err != nil {
					t.Fatalf("frame %d Encode: %v", frame, err)
				}
				if want[frame].Status != 0 || !bytes.Equal(got, want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
					t.Fatalf("frame %d fixed LFE mismatch: status=%d packet=%d/%d range=%08x/%08x",
						frame, want[frame].Status, len(got), len(want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
				}
				if !enc.fixedCELTUsed {
					t.Fatalf("frame %d did not use fixed CELT under LFE", frame)
				}
			}
		})
	}
}

func TestPublicFixedShortFrameSILKRequestFallsBackToCELTOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 240
		channels  = 1
	)
	pcm := make([]float32, frameSize*channels)
	fillFixedExtensionPCM(pcm, 0x53484f52)
	want, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
		ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthFullband,
		Bitrate: 128000, Complexity: 5, ForceChannels: channels, VBR: true, FrameSize: frameSize,
	}, []libopustest.OpusEncodeFixedMixedFrame{{Format: 1, FloatPCM: pcm}})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed short-frame mode fallback oracle", err)
		return
	}

	enc := NewEncoder(48000, channels)
	enc.SetMode(ModeSILK)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthFullband)
	enc.SetBitrate(128000)
	enc.SetBitrateMode(ModeVBR)
	enc.SetForceChannels(channels)
	enc.SetComplexity(5)
	got, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(want) != 1 || want[0].Status != 0 || !bytes.Equal(got, want[0].Packet) || enc.FinalRange() != want[0].FinalRange {
		if len(want) == 1 {
			t.Fatalf("short-frame fallback mismatch: mode=%s fixed=%t packet=%d/%d range=%08x/%08x",
				modeFixtureLabelFromConfig(int(got[0]>>3)), enc.fixedCELTUsed,
				len(got), len(want[0].Packet), enc.FinalRange(), want[0].FinalRange)
		}
		t.Fatalf("short-frame oracle returned %d records, want 1", len(want))
	}
	if !enc.fixedCELTUsed || modeFixtureLabelFromConfig(int(got[0]>>3)) != "celt" {
		t.Fatalf("short-frame fallback did not use fixed CELT: mode=%s fixed=%t",
			modeFixtureLabelFromConfig(int(got[0]>>3)), enc.fixedCELTUsed)
	}
}

func fixedCELTEnergyMaskTestValues(channels int) []float32 {
	mask := make([]float32, channels*21)
	for i := range mask {
		mask[i] = -0.75 + float32(i%13)*0.0625 + 0.00000003
	}
	return mask
}

func fixedCELTEnergyMaskQ24TestValues(channels int) []int32 {
	mask := make([]int32, channels*21)
	for i := range mask {
		mask[i] = int32(-3<<22) + int32(i%13)*(1<<20)
	}
	return mask
}

func fillFixedExtensionPCM(pcm []float32, seed uint32) {
	state := seed
	for i := range pcm {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		pcm[i] = float32(int32(state)>>8) * (0.15 / float32(1<<23))
	}
}
