//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
	"math"
	"testing"
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

func TestPublicCELTEncodeFixedByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		shortMdctSize = 120
		start         = 0
		end           = 21
	)

	next := func(state *uint32) uint32 {
		s := *state
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		*state = s
		return s
	}

	type kase struct {
		lm         int
		channels   int
		bitrate    int
		complexity int
		mode       BitrateMode
		transient  bool
	}
	var cases []kase
	for _, ch := range []int{1, 2} {
		for _, lm := range []int{0, 1, 2, 3} {
			for _, br := range []int{32000, 64000, 96000, 256000} {
				for _, cx := range []int{0, 5, 10} {
					for _, m := range []BitrateMode{ModeCBR, ModeCVBR, ModeVBR} {
						for _, tr := range []bool{false, true} {
							cases = append(cases, kase{lm: lm, channels: ch, bitrate: br, complexity: cx, mode: m, transient: tr})
						}
					}
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		name := fmt.Sprintf("ch%d/lm%d/br%d/cx%d/%v/tr=%v", c.channels, c.lm, c.bitrate, c.complexity, c.mode, c.transient)
		t.Run(name, func(t *testing.T) {
			frameSize := shortMdctSize << c.lm

			// Build float input in [-1,1). The public fixed encoder retains its
			// Q8 fractional samples through the Opus delay and CELT front end.
			state := uint32(0xC0FFEE + c.lm*131 + c.bitrate + c.complexity*7 + int(c.mode)*97)
			if c.transient {
				state ^= 0x33333333
			}
			pcm := make([]float32, c.channels*frameSize)
			for i := range pcm {
				v := int32(next(&state))
				s := float32(v>>16) / 32768.0 * 0.25
				if c.transient && i > len(pcm)/2 && i < len(pcm)/2+40 {
					s = float32(v>>16) / 32768.0
				}
				if s >= 1 {
					s = 0.9999
				}
				if s < -1 {
					s = -1
				}
				pcm[i] = s
			}

			enc := NewEncoder(48000, c.channels)
			enc.SetMode(ModeCELT)
			enc.SetLowDelay(true)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetComplexity(c.complexity)
			enc.SetBitrate(c.bitrate)
			enc.SetBitrateMode(c.mode)

			packet, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("public Encode: %v", err)
			}
			if !enc.fixedCELTUsed {
				t.Fatalf("frame was not routed through the integer CELT encoder")
			}
			if len(packet) < 1 {
				t.Fatalf("empty packet")
			}
			got := packet[1:] // strip TOC byte; single un-padded CELT frame

			frame := fixedQ8OracleFrame(enc)
			if len(frame.PCM) != c.channels*frameSize {
				t.Fatalf("LastFixedCELTInputQ8 len=%d want %d", len(frame.PCM), c.channels*frameSize)
			}
			bitrate, _, lsbDepth := enc.LastFixedCELTControls()

			vbr := c.mode != ModeCBR
			cvbr := c.mode == ModeCVBR
			want, err := probePublicFixedCELTQ8(libopustest.CELTFixedQ8Params{
				SampleRate: 48000, Channels: c.channels,
				StreamChannels: int(enc.celtEncoder.StreamChannels()), FrameSize: frameSize,
				Start: start, End: end, Bitrate: bitrate, Complexity: c.complexity,
				LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: cvbr,
				Frames: []libopustest.CELTFixedQ8Frame{frame},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
				return
			}
			if len(want) != 1 {
				t.Fatalf("raw Q8 oracle records=%d want 1", len(want))
			}

			if !bytes.Equal(got, want[0].Packet) || enc.FinalRange() != want[0].FinalRange {
				n := len(got)
				if len(want[0].Packet) < n {
					n = len(want[0].Packet)
				}
				diff := -1
				for i := 0; i < n; i++ {
					if got[i] != want[0].Packet[i] {
						diff = i
						break
					}
				}
				t.Fatalf("public CELT packet mismatch: got %d bytes, want %d bytes, first diff at %d range=%08x/%08x\n got=% x\nwant=% x",
					len(got), len(want[0].Packet), diff, enc.FinalRange(), want[0].FinalRange, got, want[0].Packet)
			}

			// The raw CELT oracle receives the coded channel count selected by
			// the public encoder. Verify that count against an independent public
			// libopus encode for a low-rate stereo case where auto channel
			// selection can choose mono; this prevents the direct oracle from
			// merely repeating a production channel-control mistake.
			if c.channels == 2 && c.lm == 0 && c.bitrate == 32000 &&
				c.complexity == 0 && c.mode == ModeCBR && !c.transient {
				ref, err := probePublicFixedMixedRecords(libopustest.OpusEncodeFixedParams{
					SampleRate: 48000, Channels: c.channels,
					Application:    libopustest.OpusApplicationRestrictedLowDelay,
					MaxPacketBytes: maxSilkPacketBytes,
					ForceMode:      libopustest.OpusForceModeCELTOnly,
					Bandwidth:      libopustest.OpusBandwidthFullband,
					Bitrate:        c.bitrate,
					Complexity:     c.complexity,
					VBR:            false,
					VBRConstraint:  false,
					FrameSize:      frameSize,
					FrameCount:     1,
					LSBDepth:       24,
				}, []libopustest.OpusEncodeFixedMixedFrame{{Format: 1, FloatPCM: pcm, ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband}})
				if err != nil {
					libopustest.HelperUnavailable(t, "public fixed Opus encode", err)
					return
				}
				if len(ref) != 1 {
					t.Fatalf("public fixed C records=%d, want one successful record", len(ref))
				}
				if ref[0].Status != 0 {
					t.Fatalf("public fixed C status=%d, want success", ref[0].Status)
				}
				codedChannels := 1
				if packet[0]&0x04 != 0 {
					codedChannels = 2
				}
				if int(enc.celtEncoder.StreamChannels()) != codedChannels {
					t.Fatalf("public TOC coded channels=%d, CELT control selected=%d", codedChannels, enc.celtEncoder.StreamChannels())
				}
				if !bytes.Equal(packet, ref[0].Packet) || enc.FinalRange() != ref[0].FinalRange {
					t.Fatalf("public fixed C packet mismatch: got %d bytes, want %d bytes, range=%08x/%08x\n got=% x\nwant=% x",
						len(packet), len(ref[0].Packet), enc.FinalRange(), ref[0].FinalRange, packet, ref[0].Packet)
				}
			}
		})
	}
}

func fixedQ8OracleAnalysis(info AnalysisInfo) libopustest.CELTFixedQ8Analysis {
	if !info.Valid {
		return libopustest.CELTFixedQ8Analysis{}
	}
	return libopustest.CELTFixedQ8Analysis{
		Valid:               true,
		Tonality:            info.Tonality,
		TonalitySlope:       info.TonalitySlope,
		Noisiness:           info.NoisySpeech,
		Activity:            info.Activity,
		MusicProb:           info.MusicProb,
		MusicProbMin:        info.MusicProbMin,
		MusicProbMax:        info.MusicProbMax,
		Bandwidth:           info.BandwidthIndex,
		ActivityProbability: info.VADProb,
		MaxPitchRatio:       info.MaxPitchRatio,
		LeakBoost:           info.LeakBoost,
	}
}

func fixedQ8OracleFrame(enc *Encoder) libopustest.CELTFixedQ8Frame {
	_, maxBytes, _ := enc.LastFixedCELTControls()
	return libopustest.CELTFixedQ8Frame{
		PCM:           append([]int32(nil), enc.LastFixedCELTInputQ8()...),
		MaxBytes:      maxBytes,
		Analysis:      fixedQ8OracleAnalysis(enc.LastFixedCELTAnalysis()),
		SetPrediction: true,
		Prediction:    int32(enc.celtEncoder.Prediction()),
	}
}

func celtFixedEndBand(bw types.Bandwidth) int {
	switch bw {
	case types.BandwidthNarrowband:
		return 13
	case types.BandwidthMediumband, types.BandwidthWideband:
		return 17
	case types.BandwidthSuperwideband:
		return 19
	case types.BandwidthFullband:
		return 21
	}
	return 21
}

// TestPublicCELTEncodeFixedRateByteExact drives the PUBLIC Encoder API in
// CELT-only mode at the sub-48 kHz API sample rates (24000/16000/12000/8000 Hz)
// under the gopus_fixed_point build and asserts the produced packet payload is
// byte-for-byte identical to the FIXED_POINT celt_encode_with_ec reference run
// (a CELT encoder created at the same API rate, so st->upsample matches). It
// exercises mono and stereo, the API-rate frame sizes that upsample to a valid
// 48 kHz core block (2.5/5/10/20 ms), a spread of bitrates and complexities, and
// CBR/CVBR/VBR rate control through the real public dispatch (sample conversion,
// bandwidth clamping/end-band selection, routing and Opus packet assembly).
func TestPublicCELTEncodeFixedRateByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	next := func(state *uint32) uint32 {
		s := *state
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		*state = s
		return s
	}

	// upsample factor for each API rate (resampling_factor).
	upsampleFor := map[int]int{24000: 2, 16000: 3, 12000: 4, 8000: 6}

	type kase struct {
		rate       int
		frameMS    int // 2.5ms encoded as 0 (handled below); use ms*10 to keep ints
		channels   int
		bitrate    int
		complexity int
		mode       BitrateMode
		transient  bool
	}

	// Frame durations expressed in tenths of a millisecond so 2.5 ms is exact.
	durTenthMS := []int{25, 50, 100, 200}

	var cases []kase
	for _, rate := range []int{24000, 16000, 12000, 8000} {
		for _, ch := range []int{1, 2} {
			for _, dur := range durTenthMS {
				for _, br := range []int{24000, 48000, 96000} {
					for _, cx := range []int{0, 5, 10} {
						for _, m := range []BitrateMode{ModeCBR, ModeCVBR, ModeVBR} {
							for _, tr := range []bool{false, true} {
								cases = append(cases, kase{
									rate: rate, frameMS: dur, channels: ch,
									bitrate: br, complexity: cx, mode: m, transient: tr,
								})
							}
						}
					}
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		// API-rate frame size: rate * (dur/10) / 1000 == rate*dur/10000.
		frameSize := c.rate * c.frameMS / 10000
		if frameSize <= 0 {
			continue
		}
		// Skip frame sizes that don't upsample to a valid 48 kHz core block.
		core := frameSize * upsampleFor[c.rate]
		if core != 120 && core != 240 && core != 480 && core != 960 {
			continue
		}
		name := fmt.Sprintf("fs%d/ch%d/dur%d/br%d/cx%d/%v/tr=%v",
			c.rate, c.channels, c.frameMS, c.bitrate, c.complexity, c.mode, c.transient)
		t.Run(name, func(t *testing.T) {
			state := uint32(0xBADF00D + c.rate + c.frameMS*131 + c.bitrate + c.complexity*7 + int(c.mode)*97)
			if c.transient {
				state ^= 0x33333333
			}
			pcm := make([]float32, c.channels*frameSize)
			for i := range pcm {
				v := int32(next(&state))
				s := float32(v>>16) / 32768.0 * 0.25
				if c.transient && i > len(pcm)/2 && i < len(pcm)/2+8 {
					s = float32(v>>16) / 32768.0
				}
				if s >= 1 {
					s = 0.9999
				}
				if s < -1 {
					s = -1
				}
				pcm[i] = s
			}

			enc := NewEncoder(c.rate, c.channels)
			enc.SetMode(ModeCELT)
			enc.SetLowDelay(true)
			enc.SetBandwidth(types.BandwidthFullband)
			enc.SetComplexity(c.complexity)
			enc.SetBitrate(c.bitrate)
			enc.SetBitrateMode(c.mode)

			packet, err := enc.Encode(pcm, frameSize)
			if err != nil {
				t.Fatalf("public Encode: %v", err)
			}
			if !enc.fixedCELTUsed {
				t.Fatalf("frame was not routed through the integer CELT encoder (rate=%d frameSize=%d bw=%v)",
					c.rate, frameSize, enc.effectiveBandwidth())
			}
			if len(packet) < 1 {
				t.Fatalf("empty packet")
			}
			got := packet[1:] // strip TOC byte; single un-padded CELT frame

			frame := fixedQ8OracleFrame(enc)
			if len(frame.PCM) != c.channels*frameSize {
				t.Fatalf("LastFixedCELTInputQ8 len=%d want %d", len(frame.PCM), c.channels*frameSize)
			}
			bitrate, _, lsbDepth := enc.LastFixedCELTControls()

			vbr := c.mode != ModeCBR
			cvbr := c.mode == ModeCVBR
			end := celtFixedEndBand(enc.effectiveBandwidth())
			want, err := probePublicFixedCELTQ8(libopustest.CELTFixedQ8Params{
				SampleRate: c.rate, Channels: c.channels,
				StreamChannels: int(enc.celtEncoder.StreamChannels()), FrameSize: frameSize,
				Start: 0, End: end, Bitrate: bitrate, Complexity: c.complexity,
				LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: cvbr,
				Frames: []libopustest.CELTFixedQ8Frame{frame},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
				return
			}
			if len(want) != 1 {
				t.Fatalf("raw Q8 oracle records=%d want 1", len(want))
			}

			if !bytes.Equal(got, want[0].Packet) || enc.FinalRange() != want[0].FinalRange {
				n := len(got)
				if len(want[0].Packet) < n {
					n = len(want[0].Packet)
				}
				diff := -1
				for i := 0; i < n; i++ {
					if got[i] != want[0].Packet[i] {
						diff = i
						break
					}
				}
				t.Fatalf("public CELT packet mismatch (rate=%d frameSize=%d end=%d): got %d bytes, want %d bytes, first diff at %d range=%08x/%08x\n got=% x\nwant=% x",
					c.rate, frameSize, end, len(got), len(want[0].Packet), diff, enc.FinalRange(), want[0].FinalRange, got, want[0].Packet)
			}
		})
	}
}
