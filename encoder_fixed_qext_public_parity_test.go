//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"testing"

	internalencoder "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPublicFixedQEXTPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{48000, 96000} {
		for _, channels := range []int{1, 2} {
			sampleRate, channels := sampleRate, channels
			t.Run(fmt.Sprintf("rate_%d/channels_%d", sampleRate, channels), func(t *testing.T) {
				frameSize := sampleRate / 50
				const (
					frameCount = 4
					bitrate    = 256000
					maxBytes   = 4000
				)
				pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, frameCount)
				frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
				for frame := range frames {
					lo := frame * frameSize * channels
					frames[frame] = libopustest.OpusEncodeFixedMixedFrame{
						Format: 0, ShortPCM: pcm[lo : lo+frameSize*channels],
					}
				}
				params := libopustest.OpusEncodeFixedParams{
					SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
					MaxPacketBytes: maxBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
					Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
					VBR: false, VBRConstraint: false, ForceChannels: channels,
					LSBDepth: 24, FrameSize: frameSize,
				}
				want, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(params, frames)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed-QEXT public encoder", err)
					return
				}

				enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
				if err != nil {
					t.Fatal(err)
				}
				for _, set := range []func() error{
					func() error { return enc.SetMode(EncoderModeCELT) },
					func() error { return enc.SetBandwidth(BandwidthFullband) },
					func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
					func() error { return enc.SetFrameSize(frameSize) },
					func() error { return enc.SetBitrate(bitrate) },
					func() error { return enc.SetComplexity(10) },
					func() error { return enc.SetBitrateMode(BitrateModeCBR) },
					func() error { return enc.SetForceChannels(channels) },
					func() error { return enc.SetLSBDepth(24) },
					func() error { return enc.SetQEXT(true) },
				} {
					if err := set(); err != nil {
						t.Fatal(err)
					}
				}

				packet := make([]byte, maxBytes)
				cbrPacketBytes := bitrate * frameSize / (sampleRate * 8)
				wantCELTMaxBytes := cbrPacketBytes - 1 // The Opus TOC consumes one byte.
				q8Frames := make([]libopustest.CELTFixedQ8Frame, frameCount)
				goStates := make([]libopustest.CELTFixedQ8EncoderState, frameCount)
				goMain := make([][]byte, frameCount)
				goSide := make([][]byte, frameCount)
				goRanges := make([]uint32, frameCount)
				traceReady := true
				matched := 0
				for frame := range frameCount {
					lo := frame * frameSize * channels
					n, err := enc.EncodeInt16(pcm[lo:lo+frameSize*channels], packet)
					if err != nil {
						t.Fatalf("frame %d EncodeInt16: %v", frame, err)
					}
					got := packet[:n]
					input := enc.enc.LastFixedCELTInputQ8()
					if len(input) == frameSize*channels {
						analysis := enc.enc.LastFixedCELTAnalysis()
						q8Frames[frame] = libopustest.CELTFixedQ8Frame{
							PCM:      append([]int32(nil), input...),
							MaxBytes: wantCELTMaxBytes,
							Analysis: fixedQEXTOracleAnalysis(analysis),
						}
						goStates[frame] = fixedQEXTGoStateSnapshot(enc, channels, sampleRate)
						main, side := fixedQEXTPacketParts(t, got)
						goMain[frame] = append([]byte(nil), main...)
						goSide[frame] = append([]byte(nil), side...)
						goRanges[frame] = enc.FinalRange()
					} else if sampleRate == 48000 {
						traceReady = false
					}
					if want[frame].Status == 0 && n == len(want[frame].Packet) && enc.FinalRange() == want[frame].FinalRange && bytes.Equal(got, want[frame].Packet) {
						matched++
						continue
					}
					gotMain, gotQEXT := fixedQEXTPacketParts(t, got)
					wantMain, wantQEXT := fixedQEXTPacketParts(t, want[frame].Packet)
					gotBitrate, gotMaxBytes, gotLSBDepth := enc.enc.LastFixedCELTControls()
					analysis := enc.enc.LastFixedCELTAnalysis()
					t.Errorf("frame %d: public fixed-QEXT differs: packet{%s}; main{%s}; side{%s}; controls{%s}; input{%s}; range{got=%08x want=%08x}",
						frame,
						fixedQEXTBytesDiff(got, want[frame].Packet),
						fixedQEXTBytesDiff(gotMain, wantMain),
						fixedQEXTBytesDiff(gotQEXT, wantQEXT),
						fixedQEXTControlSummary(gotBitrate, gotMaxBytes, gotLSBDepth, wantCELTMaxBytes),
						fixedQEXTInputSummary(input, frameSize*channels, analysis.Valid),
						enc.FinalRange(), want[frame].FinalRange)
				}
				if traceReady {
					if sampleRate == 48000 {
						cAnalysis, err := probePublicFixedQEXTAnalysis(t, sampleRate, channels, frameSize, pcm)
						if err != nil {
							t.Errorf("same-input public analysis oracle: %v", err)
						} else {
							for frame, cInfo := range cAnalysis {
								if difference := fixedQEXTAnalysisDifference(cInfo, q8Frames[frame].Analysis); difference != "equal" {
									t.Errorf("same-input analysis frame %d: %s", frame, difference)
								}
							}
						}
					}
					trace, err := libopustest.ProbeCELTFixedQEXTQ8State(libopustest.CELTFixedQ8Params{
						SampleRate: sampleRate, Channels: channels, StreamChannels: channels, FrameSize: frameSize,
						Start: 0, End: 21, Bitrate: -1, Complexity: 10, LSBDepth: 16, QEXTEnabled: true, ConstrainedVBR: true,
						Frames: q8Frames,
					})
					if err != nil {
						t.Errorf("same-Q8 active-QEXT state replay: %v", err)
					} else {
						for frame, c := range trace {
							cMain, cSide := fixedQEXTPacketParts(t, append([]byte{want[frame].Packet[0]}, c.Packet...))
							stateDiff := fixedQEXTStateDifference(c.State, goStates[frame])
							if !bytes.Equal(cMain, goMain[frame]) || !bytes.Equal(cSide, goSide[frame]) || c.FinalRange != goRanges[frame] || stateDiff != "equal" {
								t.Errorf("same-Q8 active-QEXT frame %d: main{%s}; side{%s}; range=%08x/%08x; state{%s}",
									frame, fixedQEXTBytesDiff(goMain[frame], cMain), fixedQEXTBytesDiff(goSide[frame], cSide),
									goRanges[frame], c.FinalRange, stateDiff)
							} else {
								t.Logf("same-Q8 active-QEXT frame %d: raw CELT bytes, range, and persistent state match", frame)
							}
						}
					}
				}
				t.Logf("public fixed+QEXT exact packets=%d/%d at %d Hz, %d channels", matched, frameCount, sampleRate, channels)
			})
		}
	}
}

func TestPublicFixedQEXTConstraintPersistsAcrossCBR(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = sampleRate / 50
		frameCount = 3
		bitrate    = 256000
		maxBytes   = 4000
	)
	pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, frameCount)
	frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
	for frame := range frames {
		lo := frame * frameSize * channels
		frames[frame].ShortPCM = pcm[lo : lo+frameSize*channels]
	}
	frames[1].VBRAction = libopustest.OpusVBRDisable

	for _, tc := range []struct {
		name        string
		startMode   BitrateMode
		constrained bool
		maskAndLFE  bool
	}{
		{name: "cvbr_to_cbr", startMode: BitrateModeCVBR, constrained: true},
		{name: "vbr_to_cbr", startMode: BitrateModeVBR},
		{name: "cvbr_to_cbr_with_lfe_mask", startMode: BitrateModeCVBR, constrained: true, maskAndLFE: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caseFrames := append([]libopustest.OpusEncodeFixedMixedFrame(nil), frames...)
			energyMask := make([]int32, 21)
			if tc.maskAndLFE {
				for band := range energyMask {
					energyMask[band] = -int32(band+1) * (1 << 20)
				}
				caseFrames[0].EnergyMaskAction = libopustest.OpusEnergyMaskSet
				caseFrames[0].EnergyMask = energyMask
				caseFrames[2].EnergyMaskAction = libopustest.OpusEnergyMaskClear
			}
			want, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
				MaxPacketBytes: maxBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
				Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
				VBR: true, VBRConstraint: tc.constrained, ForceChannels: channels, LSBDepth: 24,
				LFE: tc.maskAndLFE, FrameSize: frameSize,
			}, caseFrames)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT VBR transition encoder", err)
				return
			}

			enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetBitrate(bitrate) },
				func() error { return enc.SetComplexity(10) },
				func() error { return enc.SetBitrateMode(tc.startMode) },
				func() error { return enc.SetForceChannels(channels) },
				func() error { return enc.SetLSBDepth(24) },
				func() error { return enc.SetQEXT(true) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			if tc.maskAndLFE {
				enc.enc.SetLFE(true)
			}

			packet := make([]byte, maxBytes)
			for frame := range caseFrames {
				if frame == 1 {
					if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
						t.Fatal(err)
					}
				}
				if tc.maskAndLFE {
					switch caseFrames[frame].EnergyMaskAction {
					case libopustest.OpusEnergyMaskSet:
						enc.enc.SetCELTEnergyMaskQ24(energyMask)
					case libopustest.OpusEnergyMaskClear:
						enc.enc.SetCELTEnergyMaskQ24(nil)
					}
				}
				lo := frame * frameSize * channels
				n, err := enc.EncodeInt16(pcm[lo:lo+frameSize*channels], packet)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				got := packet[:n]
				if want[frame].Status != 0 || n != len(want[frame].Packet) ||
					enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
					t.Errorf("frame %d: packet{%s}; final range=%08x want=%08x",
						frame, fixedQEXTBytesDiff(got, want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
				}
			}
		})
	}
}

func TestPublicFixedQEXTFrameSizeModeAndInputMatrix(t *testing.T) {
	libopustest.RequireOracle(t)
	type modeCase struct {
		name        string
		mode        BitrateMode
		vbr         bool
		constrained bool
	}
	modes := []modeCase{
		{name: "cbr", mode: BitrateModeCBR, constrained: true},
		{name: "cvbr", mode: BitrateModeCVBR, vbr: true, constrained: true},
		{name: "vbr", mode: BitrateModeVBR, vbr: true},
	}
	durations := []struct {
		name string
		size int
	}{
		{name: "2_5ms", size: 120},
		{name: "5ms", size: 240},
		{name: "10ms", size: 480},
		{name: "20ms", size: 960},
	}
	const sampleRate = 48000
	const bitrate = 640000
	const frameCount = 3
	for di, duration := range durations {
		for mi, mode := range modes {
			for _, channels := range []int{1, 2} {
				di, mi, duration, mode, channels := di, mi, duration, mode, channels
				inputFormat := uint32((di + mi + channels) % 3)
				maxPacketBytes := 4000
				if (di+mi+channels)%2 == 0 {
					maxPacketBytes = 256
				}
				resetBefore := (di+mi+channels)%2 == 1
				t.Run(fmt.Sprintf("%s/%s/ch%d/input%d/cap%d/reset=%t", duration.name, mode.name, channels, inputFormat, maxPacketBytes, resetBefore), func(t *testing.T) {
					pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, duration.size, frameCount)
					frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
					perFrame := duration.size * channels
					for frame := range frames {
						lo := frame * perFrame
						short := pcm[lo : lo+perFrame]
						frames[frame].Format = inputFormat
						frames[frame].ResetBefore = resetBefore && frame == 2
						switch inputFormat {
						case 0:
							frames[frame].ShortPCM = short
						case 1:
							frames[frame].FloatPCM = make([]float32, perFrame)
							for i, sample := range short {
								frames[frame].FloatPCM[i] = float32(sample) * (1.0 / 32768.0)
							}
						case 2:
							frames[frame].PCM24 = make([]int32, perFrame)
							for i, sample := range short {
								frames[frame].PCM24[i] = int32(sample) << 8
							}
						}
					}

					want, err := libopustest.ProbeOpusEncodeFixedQEXTMixedRecords(libopustest.OpusEncodeFixedParams{
						SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
						MaxPacketBytes: maxPacketBytes, ForceMode: libopustest.OpusForceModeCELTOnly,
						Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 10,
						VBR: mode.vbr, VBRConstraint: mode.constrained, ForceChannels: channels,
						LSBDepth: 24, FrameSize: duration.size,
					}, frames)
					if err != nil {
						libopustest.HelperUnavailable(t, "fixed-QEXT public frame-size/input matrix", err)
						return
					}

					enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
					if err != nil {
						t.Fatal(err)
					}
					for _, set := range []func() error{
						func() error { return enc.SetMode(EncoderModeCELT) },
						func() error { return enc.SetBandwidth(BandwidthFullband) },
						func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
						func() error { return enc.SetFrameSize(duration.size) },
						func() error { return enc.SetBitrate(bitrate) },
						func() error { return enc.SetComplexity(10) },
						func() error { return enc.SetBitrateMode(mode.mode) },
						func() error { return enc.SetForceChannels(channels) },
						func() error { return enc.SetLSBDepth(24) },
						func() error { return enc.SetQEXT(true) },
					} {
						if err := set(); err != nil {
							t.Fatal(err)
						}
					}
					packet := make([]byte, maxPacketBytes)
					for frame := range frames {
						if frames[frame].ResetBefore {
							enc.Reset()
						}
						var n int
						switch inputFormat {
						case 0:
							n, err = enc.EncodeInt16(frames[frame].ShortPCM, packet)
						case 1:
							n, err = enc.Encode(frames[frame].FloatPCM, packet)
						case 2:
							n, err = enc.EncodeInt24(frames[frame].PCM24, packet)
						}
						if err != nil {
							t.Fatalf("frame %d encode: %v", frame, err)
						}
						if want[frame].Status != 0 || n != len(want[frame].Packet) ||
							enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(packet[:n], want[frame].Packet) {
							t.Fatalf("frame %d: packet{%s}; final range=%08x want=%08x",
								frame, fixedQEXTBytesDiff(packet[:n], want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
						}
					}
				})
			}
		}
	}
}

func TestPublicFixedQEXTHighBudgetWarmEncodeAllocations(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
		bitrate    = 640000
	)
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return enc.SetMode(EncoderModeCELT) },
		func() error { return enc.SetBandwidth(BandwidthFullband) },
		func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
		func() error { return enc.SetFrameSize(frameSize) },
		func() error { return enc.SetBitrate(bitrate) },
		func() error { return enc.SetComplexity(10) },
		func() error { return enc.SetBitrateMode(BitrateModeCBR) },
		func() error { return enc.SetForceChannels(channels) },
		func() error { return enc.SetLSBDepth(24) },
		func() error { return enc.SetQEXT(true) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	pcm := makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, 1)
	packet := make([]byte, 4000)
	encode := func() {
		n, err := enc.EncodeInt16(pcm, packet)
		if err != nil {
			panic(err)
		}
		if n <= 1276 {
			panic(fmt.Sprintf("high-budget fixed-QEXT output length=%d, want >1276", n))
		}
	}
	for range 4 {
		encode()
	}
	_, maxBytes, _ := enc.enc.LastFixedCELTControls()
	if maxBytes <= 1275 {
		t.Fatalf("QEXT CELT budget=%d, want caller capacity beyond 1275", maxBytes)
	}
	if allocs := testing.AllocsPerRun(20, encode); allocs != 0 {
		t.Fatalf("warmed high-budget fixed-QEXT encode allocated %g objects", allocs)
	}
}

func makeFixedQEXTInventoryPCM(sampleRate, channels, frameSize, frameCount int) []int16 {
	pcm := make([]int16, frameSize*channels*frameCount)
	for frame := 0; frame < frameCount; frame++ {
		for i := 0; i < frameSize; i++ {
			low := 0.34 * math.Sin(2*math.Pi*6000*float64(i)/float64(sampleRate))
			highFrequency := 30000.0
			if sampleRate == 48000 {
				highFrequency = 21000
			}
			high := 0.21 * math.Sin(2*math.Pi*highFrequency*float64(i)/float64(sampleRate))
			for ch := 0; ch < channels; ch++ {
				v := low + high*float64(ch+1)/float64(channels)
				pcm[(frame*frameSize+i)*channels+ch] = int16(math.Round(v * 32767))
			}
		}
	}
	return pcm
}

func fixedQEXTPacketParts(t *testing.T, packet []byte) (main, qext []byte) {
	t.Helper()
	if len(packet) < 2 || packet[0]&0x03 != 3 {
		t.Fatalf("QEXT packet is not code 3: len=%d packet=%x", len(packet), packet)
	}
	count := packet[1]
	if count&0x80 != 0 || count&0x3f != 1 {
		t.Fatalf("QEXT packet is not one-frame CBR: count byte=%02x", count)
	}
	frameStart := 2
	padding := 0
	if count&0x40 != 0 {
		for {
			if frameStart >= len(packet) {
				t.Fatalf("QEXT packet padding length overruns packet: %x", packet)
			}
			b := int(packet[frameStart])
			frameStart++
			if b == 255 {
				padding += 254
				continue
			}
			padding += b
			break
		}
	}
	end := len(packet) - padding
	if end < frameStart {
		t.Fatalf("invalid QEXT packet bounds start=%d end=%d len=%d", frameStart, end, len(packet))
	}
	main = packet[frameStart:end]
	if padding > 0 {
		var payloads [maxRepacketizerFrames][]byte
		collectQEXTPacketExtensions(packet[end:], 1, qextPacketExtensionID, &payloads)
		qext = payloads[0]
	}
	return main, qext
}

func firstPacketByteDiff(got, want []byte) int {
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		if got[i] != want[i] {
			return i
		}
	}
	if len(got) != len(want) {
		return limit
	}
	return -1
}

func fixedQEXTBytesDiff(got, want []byte) string {
	first := firstPacketByteDiff(got, want)
	if first < 0 {
		return fmt.Sprintf("exact len=%d", len(got))
	}
	const context = 4
	gotStart := max(0, first-context)
	wantStart := max(0, first-context)
	gotEnd := min(len(got), first+context)
	wantEnd := min(len(want), first+context)
	return fmt.Sprintf("len=%d/%d first=%d got[%d:%d]=% x want[%d:%d]=% x",
		len(got), len(want), first,
		gotStart, gotEnd, got[gotStart:gotEnd],
		wantStart, wantEnd, want[wantStart:wantEnd])
}

func fixedQEXTControlSummary(gotBitrate, gotMaxBytes, gotLSBDepth, wantMaxBytes int) string {
	status := "ok"
	// C leaves the CELT target at OPUS_BITRATE_MAX in CBR and opus_encode
	// supplies the 16-bit input depth regardless of the configured depth ctl.
	if gotBitrate != -1 || gotLSBDepth != 16 || gotMaxBytes != wantMaxBytes {
		status = "mismatch"
	}
	return fmt.Sprintf("%s celtBitrate=%d/-1 maxBytes=%d/%d lsbDepth=%d/16", status,
		gotBitrate, gotMaxBytes, wantMaxBytes, gotLSBDepth)
}

func fixedQEXTInputSummary(input []int32, expected int, analysisValid bool) string {
	if len(input) != expected {
		return fmt.Sprintf("bridge-not-used len=%d want=%d", len(input), expected)
	}
	const samplePreview = 6
	end := min(len(input), samplePreview)
	return fmt.Sprintf("captured Q8 len=%d first[%d]=%v analysisValid=%t", len(input), end, input[:end], analysisValid)
}

func fixedQEXTOracleAnalysis(info internalencoder.AnalysisInfo) libopustest.CELTFixedQ8Analysis {
	return libopustest.CELTFixedQ8Analysis{
		Valid: info.Valid, Tonality: info.Tonality, TonalitySlope: info.TonalitySlope,
		Noisiness: info.NoisySpeech, Activity: info.Activity, MusicProb: info.MusicProb,
		MusicProbMin: info.MusicProbMin, MusicProbMax: info.MusicProbMax,
		Bandwidth: info.BandwidthIndex, ActivityProbability: info.VADProb,
		MaxPitchRatio: info.MaxPitchRatio, LeakBoost: info.LeakBoost,
	}
}

var publicFixedQEXTAnalysisHelper libopustest.HelperCache

func buildPublicFixedQEXTAnalysisHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "fixed-QEXT public int16 analysis",
		OutputBase:   "gopus_libopus_fixed_qext_analysis",
		SourceFile:   "libopus_analysis_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DGOPUS_REQUIRE_QEXT=1", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"include", "src", "celt", "silk", "silk/float"},
		Libs:         []string{libopustest.FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

func probePublicFixedQEXTAnalysis(t *testing.T, sampleRate, channels, frameSize int, pcm []int16) ([]libopustest.CELTFixedQ8Analysis, error) {
	t.Helper()
	frameCount := len(pcm) / (channels * frameSize)
	if frameCount < 1 || frameCount*channels*frameSize != len(pcm) {
		return nil, fmt.Errorf("invalid analysis PCM size %d", len(pcm))
	}
	c2 := int32(-2)
	payload := libopustest.NewOraclePayloadVersion("GANI", 1,
		uint32(sampleRate), uint32(channels), uint32(frameSize), uint32(frameCount),
		16, 0, uint32(c2), 1, uint32(len(pcm)))
	for _, sample := range pcm {
		payload.I16(sample)
	}
	if len(pcm)&1 != 0 {
		payload.I16(0)
	}
	bin, err := publicFixedQEXTAnalysisHelper.Path(buildPublicFixedQEXTAnalysisHelper)
	if err != nil {
		return nil, err
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "fixed-QEXT public int16 analysis", "GANO")
	if err != nil {
		return nil, err
	}
	reader.Count(frameCount)
	readInfo := func() libopustest.CELTFixedQ8Analysis {
		info := libopustest.CELTFixedQ8Analysis{Valid: reader.U32() != 0}
		info.Tonality = reader.Float32()
		info.TonalitySlope = reader.Float32()
		info.Noisiness = reader.Float32()
		info.Activity = reader.Float32()
		info.MusicProb = reader.Float32()
		info.MusicProbMin = reader.Float32()
		info.MusicProbMax = reader.Float32()
		info.Bandwidth = int32(reader.U32())
		info.ActivityProbability = reader.Float32()
		info.MaxPitchRatio = reader.Float32()
		_ = reader.U32() // reserved field in the trace record
		copy(info.LeakBoost[:], reader.Bytes(len(info.LeakBoost)))
		_ = reader.Bytes(1) // 20-byte C record aligns the 19 leak boosts.
		return info
	}
	infos := make([]libopustest.CELTFixedQ8Analysis, frameCount)
	for frame := range infos {
		infos[frame] = readInfo()
		_ = reader.Bytes(68 + 48 + 48) // latest info plus scalar and state hashes.
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return infos, nil
}

func fixedQEXTAnalysisDifference(c, goInfo libopustest.CELTFixedQ8Analysis) string {
	if c.Valid != goInfo.Valid {
		return fmt.Sprintf("valid C=%t Go=%t", c.Valid, goInfo.Valid)
	}
	for _, field := range []struct {
		name string
		c, g float32
	}{
		{"tonality", c.Tonality, goInfo.Tonality}, {"tonalitySlope", c.TonalitySlope, goInfo.TonalitySlope},
		{"noisiness", c.Noisiness, goInfo.Noisiness}, {"activity", c.Activity, goInfo.Activity},
		{"musicProb", c.MusicProb, goInfo.MusicProb}, {"musicProbMin", c.MusicProbMin, goInfo.MusicProbMin},
		{"musicProbMax", c.MusicProbMax, goInfo.MusicProbMax}, {"activityProbability", c.ActivityProbability, goInfo.ActivityProbability},
		{"maxPitchRatio", c.MaxPitchRatio, goInfo.MaxPitchRatio},
	} {
		if math.Float32bits(field.c) != math.Float32bits(field.g) {
			return fmt.Sprintf("%s C=%08x Go=%08x", field.name, math.Float32bits(field.c), math.Float32bits(field.g))
		}
	}
	if c.Bandwidth != goInfo.Bandwidth {
		return fmt.Sprintf("bandwidth C=%d Go=%d", c.Bandwidth, goInfo.Bandwidth)
	}
	for i := range c.LeakBoost {
		if c.LeakBoost[i] != goInfo.LeakBoost[i] {
			return fmt.Sprintf("leakBoost[%d] C=%d Go=%d", i, c.LeakBoost[i], goInfo.LeakBoost[i])
		}
	}
	return "equal"
}

func fixedQEXTGoStateSnapshot(public *Encoder, channels, sampleRate int) libopustest.CELTFixedQ8EncoderState {
	outer := reflect.ValueOf(public.enc).Elem()
	fixed := outer.FieldByName("fixedCELT")
	if fixed.IsNil() {
		panic("fixed CELT encoder state is nil")
	}
	root := fixed.Elem().FieldByName("enc").Elem()
	intField := func(name string) int32 {
		field := root.FieldByName(name)
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return int32(field.Int())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return int32(field.Uint())
		default:
			panic("unexpected fixed CELT state field kind: " + name)
		}
	}
	copySlice := func(name string, limit int) []int32 {
		field := root.FieldByName(name)
		if field.IsNil() {
			return nil
		}
		length := field.Len()
		if limit >= 0 && length > limit {
			length = limit
		}
		values := make([]int32, length)
		for i := range values {
			values[i] = int32(field.Index(i).Int())
		}
		return values
	}
	boolInt := func(name string) int32 {
		if root.FieldByName(name).Bool() {
			return 1
		}
		return 0
	}
	spreading := root.FieldByName("spreading")
	analysis := root.FieldByName("analysis")
	overlap, maxPeriod := 120, 1024
	if sampleRate == 96000 {
		overlap, maxPeriod = 240, 2048
	}
	floatBits := func(name string) uint32 {
		return math.Float32bits(float32(analysis.FieldByName(name).Float()))
	}
	state := libopustest.CELTFixedQ8EncoderState{
		RNG:              uint32(root.FieldByName("rng").Uint()),
		SpreadDecision:   intField("spreadDecision"),
		DelayedIntra:     intField("delayedIntra"),
		TonalAverage:     int32(spreading.FieldByName("TonalAverage").Int()),
		LastCodedBands:   intField("lastCodedBands"),
		HFAverage:        int32(spreading.FieldByName("HFAverage").Int()),
		TapsetDecision:   int32(spreading.FieldByName("TapsetDecision").Int()),
		PrefilterPeriod:  intField("prefilterPeriod"),
		PrefilterGain:    intField("prefilterGain"),
		PrefilterTapset:  intField("prefilterTapset"),
		ConsecTransient:  intField("consecTransient"),
		VBRReservoir:     intField("vbrReservoir"),
		VBRDrift:         intField("vbrDrift"),
		VBROffset:        intField("vbrOffset"),
		VBRCount:         intField("vbrCount"),
		OverlapMax:       intField("overlapMax"),
		StereoSaving:     intField("stereoSaving"),
		Intensity:        intField("intensity"),
		SpecAvg:          intField("specAvg"),
		ForceIntra:       boolInt("forceIntra"),
		DisablePrefilter: boolInt("disablePrefilter"),
		SilkSignalType:   intField("silkSignalType"),
		SilkOffset:       intField("silkOffset"),
		EnergyMask:       copySlice("energyMask", channels*21),
		PreemphMemE:      copySlice("preemphMemE", channels),
		InMem:            copySlice("inMem", channels*overlap),
		PrefilterMem:     copySlice("prefilterMem", channels*maxPeriod),
		OldBandE:         copySlice("oldBandE", channels*21),
		OldLogE:          copySlice("oldLogE", channels*21),
		OldLogE2:         copySlice("oldLogE2", channels*21),
		EnergyError:      copySlice("energyError", channels*21),
	}
	qext := root.FieldByName("qext")
	state.QEXTOldBandE = make([]int32, qext.FieldByName("oldBandE").Len())
	for i := range state.QEXTOldBandE {
		state.QEXTOldBandE[i] = int32(qext.FieldByName("oldBandE").Index(i).Int())
	}
	if analysis.FieldByName("Valid").Bool() {
		state.AnalysisValid = 1
	}
	state.AnalysisBandwidth = int32(analysis.FieldByName("Bandwidth").Int())
	state.AnalysisActivityBits = floatBits("Activity")
	state.AnalysisTonalityBits = floatBits("Tonality")
	state.AnalysisSlopeBits = floatBits("TonalitySlope")
	state.AnalysisMaxPitchRatioBits = floatBits("MaxPitchRatio")
	leakBoost := analysis.FieldByName("LeakBoost")
	for i := range state.AnalysisLeakBoost {
		state.AnalysisLeakBoost[i] = uint8(leakBoost.Index(i).Uint())
	}
	return state
}

func fixedQEXTStateDifference(c, goState libopustest.CELTFixedQ8EncoderState) string {
	if c.RNG != goState.RNG {
		return fmt.Sprintf("rng C=%08x Go=%08x", c.RNG, goState.RNG)
	}
	for _, field := range []string{"EnergyMask", "PreemphMemE", "InMem", "PrefilterMem"} {
		cv := reflect.ValueOf(c).FieldByName(field)
		gv := reflect.ValueOf(goState).FieldByName(field)
		if cv.Len() != gv.Len() {
			return fmt.Sprintf("%s length C=%d Go=%d", field, cv.Len(), gv.Len())
		}
		for i := 0; i < cv.Len(); i++ {
			if cv.Index(i).Int() != gv.Index(i).Int() {
				return fmt.Sprintf("%s[%d] C=%d Go=%d", field, i, cv.Index(i).Int(), gv.Index(i).Int())
			}
		}
	}
	for _, field := range []string{
		"SpreadDecision", "DelayedIntra", "TonalAverage", "LastCodedBands", "HFAverage", "TapsetDecision",
		"PrefilterPeriod", "PrefilterGain", "PrefilterTapset", "ConsecTransient", "VBRReservoir", "VBRDrift",
		"VBROffset", "VBRCount", "OverlapMax", "StereoSaving", "Intensity", "SpecAvg", "ForceIntra",
		"DisablePrefilter", "SilkSignalType", "SilkOffset", "AnalysisValid", "AnalysisBandwidth",
		"AnalysisActivityBits", "AnalysisTonalityBits", "AnalysisSlopeBits", "AnalysisMaxPitchRatioBits",
		"AnalysisLeakBoost",
	} {
		cv := reflect.ValueOf(c).FieldByName(field)
		gv := reflect.ValueOf(goState).FieldByName(field)
		if !reflect.DeepEqual(cv.Interface(), gv.Interface()) {
			return fmt.Sprintf("%s C=%v Go=%v", field, cv.Interface(), gv.Interface())
		}
	}
	for _, field := range []string{"OldBandE", "OldLogE", "OldLogE2", "EnergyError", "QEXTOldBandE"} {
		cv := reflect.ValueOf(c).FieldByName(field)
		gv := reflect.ValueOf(goState).FieldByName(field)
		if cv.Len() != gv.Len() {
			return fmt.Sprintf("%s length C=%d Go=%d", field, cv.Len(), gv.Len())
		}
		for i := 0; i < cv.Len(); i++ {
			if cv.Index(i).Int() != gv.Index(i).Int() {
				return fmt.Sprintf("%s[%d] C=%d Go=%d", field, i, cv.Index(i).Int(), gv.Index(i).Int())
			}
		}
	}
	return "equal"
}
