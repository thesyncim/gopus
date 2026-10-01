//go:build gopus_fixed_point

package encoder

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

func TestPublicFixedAutoCELTSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
		frameCount = 8
	)
	cases := []struct {
		name     string
		channels int
		bitrate  int
		maxBytes int
	}{
		{name: "low-space-mono", channels: 1, bitrate: 64000, maxBytes: 14},
		{name: "normal-budget-stereo-auto-channels", channels: 2, bitrate: 128000, maxBytes: 4000},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
			pcmFrames := make([][]float32, frameCount)
			for frame := range frameCount {
				pcm := make([]float32, frameSize*tc.channels)
				for i := range frameSize {
					t := float64(frame*frameSize+i) / sampleRate
					amplitude := 0.12 + float64(frame%3)*0.025
					for ch := range tc.channels {
						freq := 330.0 + float64(ch)*230
						sample := amplitude*math.Sin(2*math.Pi*freq*t) +
							0.035*math.Sin(2*math.Pi*(3*freq)*t+float64(frame)*0.07+float64(ch)*0.11)
						pcm[i*tc.channels+ch] = float32(sample)
					}
				}
				pcmFrames[frame] = pcm
				frames[frame] = libopustest.OpusEncodeFixedMixedFrame{Format: 1, FloatPCM: pcm}
			}

			params := libopustest.OpusEncodeFixedParams{
				SampleRate: sampleRate, Channels: tc.channels, Application: libopustest.OpusApplicationAudio,
				MaxPacketBytes: tc.maxBytes, Bitrate: tc.bitrate, Complexity: 10,
				VBR: true, VBRConstraint: false, LSBDepth: 24,
				FrameSize: frameSize, FrameCount: frameCount,
			}
			var want []libopustest.OpusEncodeFixedRecord
			var err error
			if extsupport.QEXT {
				want, err = libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(params, frames)
			} else {
				want, err = libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
			}
			if err != nil {
				libopustest.HelperUnavailable(t, "persistent fixed auto-mode encoder", err)
				return
			}
			if len(want) != frameCount {
				t.Fatalf("selected fixed C records=%d, want %d", len(want), frameCount)
			}

			enc := NewEncoder(sampleRate, tc.channels)
			enc.SetMode(ModeAuto)
			enc.SetBandwidthAuto()
			enc.SetMaxBandwidth(types.BandwidthFullband)
			enc.SetBitrate(tc.bitrate)
			enc.SetBitrateMode(ModeVBR)
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)
			enc.SetComplexity(10)
			enc.SetLSBDepth(24)
			q8Frames := make([]libopustest.CELTFixedQ8Frame, frameCount)
			goStates := make([]libopustest.CELTFixedQ8EncoderState, frameCount)
			publicPackets := make([][]byte, frameCount)
			publicRanges := make([]uint32, frameCount)
			for frame, pcm := range pcmFrames {
				got, err := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, tc.maxBytes)
				if err != nil {
					t.Fatalf("frame %d EncodeFloat32WithAnalysisMaxBytes: %v", frame, err)
				}
				if want[frame].Status != 0 {
					t.Fatalf("frame %d selected fixed C status=%d", frame, want[frame].Status)
				}
				if modeFixtureLabelFromConfig(int(want[frame].Packet[0]>>3)) != "celt" {
					t.Fatalf("frame %d selected C TOC=%02x, want auto-selected CELT", frame, want[frame].Packet[0])
				}
				if enc.fixedCELTUsed {
					q8Frames[frame] = fixedQ8OracleFrame(enc)
					goStates[frame] = fixedCELTStateSnapshot(enc)
					publicPackets[frame] = append([]byte(nil), got...)
					publicRanges[frame] = enc.FinalRange()
				}
				if len(got) == 0 || !bytes.Equal(got, want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
					diff := 0
					for diff < len(got) && diff < len(want[frame].Packet) && got[diff] == want[frame].Packet[diff] {
						diff++
					}
					lo := max(0, diff-8)
					gotHi := min(len(got), diff+8)
					wantHi := min(len(want[frame].Packet), diff+8)
					rawDiag := "not-run"
					if extsupport.QEXT && enc.fixedCELTUsed && len(got) > 1 {
						bitrate, _, lsbDepth := enc.LastFixedCELTControls()
						raw, rawErr := libopustest.ProbeCELTFixedQEXTQ8(libopustest.CELTFixedQ8Params{
							SampleRate: sampleRate, Channels: tc.channels,
							StreamChannels: int(enc.celtEncoder.StreamChannels()), FrameSize: frameSize,
							Start: 0, End: 21, Bitrate: bitrate, Complexity: 10,
							LSBDepth: lsbDepth, VBR: true,
							Frames: q8Frames[:frame+1],
						})
						if rawErr != nil {
							rawDiag = "error: " + rawErr.Error()
						} else if len(raw) != frame+1 {
							rawDiag = fmt.Sprintf("records=%d", len(raw))
						} else {
							rawMismatch := -1
							for i, record := range raw {
								if !bytes.Equal(publicPackets[i][1:], record.Packet) || publicRanges[i] != record.FinalRange {
									rawMismatch = i
									break
								}
							}
							rawDiag = fmt.Sprintf("first-frame-mismatch=%d records=%d public-len=%d raw-len=%d public-range=%08x raw-range=%08x",
								rawMismatch, len(raw), len(publicPackets[frame])-1, len(raw[frame].Packet),
								publicRanges[frame], raw[frame].FinalRange)
						}
						trace, traceErr := libopustest.ProbeCELTFixedQEXTQ8State(libopustest.CELTFixedQ8Params{
							SampleRate: sampleRate, Channels: tc.channels,
							StreamChannels: int(enc.celtEncoder.StreamChannels()), FrameSize: frameSize,
							Start: 0, End: 21, Bitrate: bitrate, Complexity: 10,
							LSBDepth: lsbDepth, VBR: true,
							Frames: q8Frames[:frame+1],
						})
						if traceErr != nil {
							rawDiag += " state-trace-error=" + traceErr.Error()
						} else {
							for tracedFrame := range trace {
								if difference := fixedCELTStateDifference(trace[tracedFrame].State, goStates[tracedFrame]); difference != "equal" {
									rawDiag += fmt.Sprintf(" state-frame=%d{%s}", tracedFrame, difference)
									break
								}
							}
						}
						isolatedDiag := "not-run"
						if frame > 0 {
							isolatedParams := params
							isolatedParams.FrameCount = 1
							isolated, isolatedErr := libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(
								isolatedParams, []libopustest.OpusEncodeFixedMixedFrame{frames[frame]})
							if isolatedErr != nil {
								isolatedDiag = "C error: " + isolatedErr.Error()
							} else {
								fresh := NewEncoder(sampleRate, tc.channels)
								fresh.SetMode(ModeAuto)
								fresh.SetBandwidthAuto()
								fresh.SetMaxBandwidth(types.BandwidthFullband)
								fresh.SetBitrate(tc.bitrate)
								fresh.SetBitrateMode(ModeVBR)
								fresh.SetVBR(true)
								fresh.SetVBRConstraint(false)
								fresh.SetComplexity(10)
								fresh.SetLSBDepth(24)
								isolatedGot, isolatedGoErr := fresh.EncodeFloat32WithAnalysisMaxBytes(pcmFrames[frame], frameSize, pcmFrames[frame], tc.maxBytes)
								if isolatedGoErr != nil {
									isolatedDiag = "Go error: " + isolatedGoErr.Error()
								} else if len(isolated) != 1 {
									isolatedDiag = fmt.Sprintf("C records=%d", len(isolated))
								} else {
									isolatedDiag = fmt.Sprintf("equal=%t length=%d/%d range=%08x/%08x",
										bytes.Equal(isolatedGot, isolated[0].Packet), len(isolatedGot), len(isolated[0].Packet),
										fresh.FinalRange(), isolated[0].FinalRange)
								}
							}
						}
						rawDiag += " isolated=" + isolatedDiag
					}
					var gotTOC, wantTOC byte
					if len(got) > 0 {
						gotTOC = got[0]
					}
					if len(want[frame].Packet) > 0 {
						wantTOC = want[frame].Packet[0]
					}
					t.Fatalf("frame %d auto CELT differs: packet=%d/%d first=%d got[%d:%d]=%x want[%d:%d]=%x TOC=%02x/%02x fixed=%t bandwidth=%d streamChannels=%d range=%08x/%08x rawQ8=%s",
						frame, len(got), len(want[frame].Packet), diff,
						lo, gotHi, got[lo:gotHi], lo, wantHi, want[frame].Packet[lo:wantHi], gotTOC, wantTOC,
						enc.fixedCELTUsed, enc.effectiveBandwidth(), enc.celtEncoder.StreamChannels(),
						enc.FinalRange(), want[frame].FinalRange, rawDiag)
				}
				if modeFixtureLabelFromConfig(int(got[0]>>3)) != "celt" {
					t.Fatalf("frame %d Go TOC=%02x, want auto-selected CELT", frame, got[0])
				}
				if !enc.fixedCELTUsed {
					t.Fatalf("frame %d did not use fixed CELT for auto-selected CELT", frame)
				}
			}

			enc.Reset()
			for frame, pcm := range pcmFrames {
				got, err := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, tc.maxBytes)
				if err != nil {
					t.Fatalf("frame %d after reset EncodeFloat32WithAnalysisMaxBytes: %v", frame, err)
				}
				if !bytes.Equal(got, want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
					t.Fatalf("frame %d after reset differs from fresh selected fixed C: packet=%d/%d range=%08x/%08x",
						frame, len(got), len(want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
				}
				if !enc.fixedCELTUsed {
					t.Fatalf("frame %d after reset did not use fixed CELT", frame)
				}
			}

			pcm := pcmFrames[frameCount-1]
			for range 2 {
				if _, err := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, tc.maxBytes); err != nil {
					t.Fatal(err)
				}
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if _, err := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, tc.maxBytes); err != nil {
					panic(err)
				}
			}); allocs != 0 {
				t.Fatalf("warmed automatic fixed CELT encode allocated %g objects", allocs)
			}
		})
	}
}

func fixedCELTStateSnapshot(enc *Encoder) libopustest.CELTFixedQ8EncoderState {
	root := reflect.ValueOf(enc.fixedCELT.enc).Elem()
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
	floatBits := func(name string) uint32 {
		return math.Float32bits(float32(analysis.FieldByName(name).Float()))
	}
	channels := enc.fixedCELT.channels
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
		InMem:            copySlice("inMem", channels*120),
		PrefilterMem:     copySlice("prefilterMem", channels*1024),
		OldBandE:         copySlice("oldBandE", channels*21),
		OldLogE:          copySlice("oldLogE", channels*21),
		OldLogE2:         copySlice("oldLogE2", channels*21),
		EnergyError:      copySlice("energyError", channels*21),
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

func fixedCELTStateDifference(c, goState libopustest.CELTFixedQ8EncoderState) string {
	if c.RNG != goState.RNG {
		return fmt.Sprintf("rng C=%08x Go=%08x", c.RNG, goState.RNG)
	}
	// run_prefilter advances these buffers before the spectral and band-energy
	// stages, so report their differences before later frame-state scalars.
	earlyArrays := []struct {
		name        string
		c, goValues []int32
	}{
		{"energyMask", c.EnergyMask, goState.EnergyMask},
		{"preemphMemE", c.PreemphMemE, goState.PreemphMemE},
		{"inMem", c.InMem, goState.InMem},
		{"prefilterMem", c.PrefilterMem, goState.PrefilterMem},
	}
	for _, array := range earlyArrays {
		if len(array.c) != len(array.goValues) {
			return fmt.Sprintf("%s length C=%d Go=%d", array.name, len(array.c), len(array.goValues))
		}
		for i := range array.c {
			if array.c[i] != array.goValues[i] {
				return fmt.Sprintf("%s[%d] C=%d Go=%d", array.name, i, array.c[i], array.goValues[i])
			}
		}
	}
	scalars := []struct {
		name       string
		c, goValue int32
	}{
		{"spreadDecision", c.SpreadDecision, goState.SpreadDecision},
		{"delayedIntra", c.DelayedIntra, goState.DelayedIntra},
		{"tonalAverage", c.TonalAverage, goState.TonalAverage},
		{"lastCodedBands", c.LastCodedBands, goState.LastCodedBands},
		{"hfAverage", c.HFAverage, goState.HFAverage},
		{"tapsetDecision", c.TapsetDecision, goState.TapsetDecision},
		{"prefilterPeriod", c.PrefilterPeriod, goState.PrefilterPeriod},
		{"prefilterGain", c.PrefilterGain, goState.PrefilterGain},
		{"prefilterTapset", c.PrefilterTapset, goState.PrefilterTapset},
		{"consecTransient", c.ConsecTransient, goState.ConsecTransient},
		{"vbrReservoir", c.VBRReservoir, goState.VBRReservoir},
		{"vbrDrift", c.VBRDrift, goState.VBRDrift},
		{"vbrOffset", c.VBROffset, goState.VBROffset},
		{"vbrCount", c.VBRCount, goState.VBRCount},
		{"overlapMax", c.OverlapMax, goState.OverlapMax},
		{"stereoSaving", c.StereoSaving, goState.StereoSaving},
		{"intensity", c.Intensity, goState.Intensity},
		{"specAvg", c.SpecAvg, goState.SpecAvg},
		{"forceIntra", c.ForceIntra, goState.ForceIntra},
		{"disablePrefilter", c.DisablePrefilter, goState.DisablePrefilter},
		{"silkSignalType", c.SilkSignalType, goState.SilkSignalType},
		{"silkOffset", c.SilkOffset, goState.SilkOffset},
		{"analysisValid", c.AnalysisValid, goState.AnalysisValid},
		{"analysisBandwidth", c.AnalysisBandwidth, goState.AnalysisBandwidth},
	}
	for _, scalar := range scalars {
		if scalar.c != scalar.goValue {
			return fmt.Sprintf("%s C=%d Go=%d", scalar.name, scalar.c, scalar.goValue)
		}
	}
	floats := []struct {
		name       string
		c, goValue uint32
	}{
		{"analysisActivity", c.AnalysisActivityBits, goState.AnalysisActivityBits},
		{"analysisTonality", c.AnalysisTonalityBits, goState.AnalysisTonalityBits},
		{"analysisSlope", c.AnalysisSlopeBits, goState.AnalysisSlopeBits},
		{"analysisMaxPitchRatio", c.AnalysisMaxPitchRatioBits, goState.AnalysisMaxPitchRatioBits},
	}
	for _, scalar := range floats {
		if scalar.c != scalar.goValue {
			return fmt.Sprintf("%s C=%08x Go=%08x", scalar.name, scalar.c, scalar.goValue)
		}
	}
	for i := range c.AnalysisLeakBoost {
		if c.AnalysisLeakBoost[i] != goState.AnalysisLeakBoost[i] {
			return fmt.Sprintf("analysisLeakBoost[%d] C=%d Go=%d", i, c.AnalysisLeakBoost[i], goState.AnalysisLeakBoost[i])
		}
	}
	arrays := []struct {
		name        string
		c, goValues []int32
	}{
		{"energyMask", c.EnergyMask, goState.EnergyMask},
		{"preemphMemE", c.PreemphMemE, goState.PreemphMemE},
		{"inMem", c.InMem, goState.InMem},
		{"prefilterMem", c.PrefilterMem, goState.PrefilterMem},
		{"oldBandE", c.OldBandE, goState.OldBandE},
		{"oldLogE", c.OldLogE, goState.OldLogE},
		{"oldLogE2", c.OldLogE2, goState.OldLogE2},
		{"energyError", c.EnergyError, goState.EnergyError},
	}
	for _, array := range arrays {
		if len(array.c) != len(array.goValues) {
			return fmt.Sprintf("%s length C=%d Go=%d", array.name, len(array.c), len(array.goValues))
		}
		for i := range array.c {
			if array.c[i] != array.goValues[i] {
				return fmt.Sprintf("%s[%d] C=%d Go=%d", array.name, i, array.c[i], array.goValues[i])
			}
		}
	}
	return "equal"
}
