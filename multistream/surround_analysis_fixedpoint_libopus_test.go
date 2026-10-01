//go:build gopus_fixed_point

package multistream

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var fixedSurroundEncodeHelper libopustest.HelperCache
var fixedSurroundAnalysisHelper libopustest.HelperCache

type fixedSurroundStreamOracle struct {
	toc            byte
	finalRange     uint32
	bitrate        int32
	bandwidth      int32
	forceChannels  int32
	complexity     int32
	packetLen      int
	decodedSamples int
}

type fixedSurroundCELTReplayFrame struct {
	oracle   libopustest.CELTFixedQ8Frame
	goPacket []byte
	cPacket  []byte
	goRange  uint32
	cRange   uint32
	bitrate  int
	lsbDepth int
}

func fixedSurroundBandwidthValue(libopusValue int32) int {
	if libopusValue >= 1101 && libopusValue <= 1105 {
		return int(libopusValue - 1101)
	}
	return int(libopusValue)
}

func fixedSurroundForceChannelsValue(libopusValue int32) int32 {
	if libopusValue == -1000 { // libopus OPUS_AUTO maps to the Go encoder's -1 sentinel.
		return -1
	}
	return libopusValue
}

func fixedSurroundEncodeRef(sampleRate, channels, frameSize, bitrate, complexity int, vbr, vbrConstraint bool, sampleFormat uint32, pcm16 [][]int16, pcmFloat [][]float32, resetBefore []bool, includeStreamTrace bool) ([]byte, int, int, [][]byte, []uint32, [][]fixedSurroundStreamOracle, error) {
	const (
		mappingFamily  = 1
		application    = libopustest.OpusApplicationAudio
		maxPacketBytes = 4000
	)
	frameCount := len(pcm16)
	if sampleFormat == 0 {
		frameCount = len(pcmFloat)
	} else if sampleFormat != 1 {
		return nil, 0, 0, nil, nil, nil, fmt.Errorf("invalid sample format %d", sampleFormat)
	}
	if frameCount == 0 || len(resetBefore) != frameCount || (sampleFormat == 0 && len(pcm16) != 0) || (sampleFormat == 1 && len(pcmFloat) != 0) {
		return nil, 0, 0, nil, nil, nil, fmt.Errorf("invalid frame sequence")
	}

	cfg := fixedSurroundReferenceConfig()
	cfg.Label = fixedSurroundReferenceLabel() + " multistream surround encode"
	cfg.OutputBase = "gopus_libopus_refencode_fixed_multistream_surround"
	cfg.SourceFile = "libopus_refencode_multistream.c"
	cfg.CFlags = []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}
	cfg.RefIncludes = []string{"celt", "silk", "src"}
	cfg.Libs = []string{fixedSurroundReferenceArchive(), "-lm"}
	binPath, err := fixedSurroundEncodeHelper.CHelperPath(cfg)
	if err != nil {
		return nil, 0, 0, nil, nil, nil, err
	}

	boolU32 := func(value bool) uint32 {
		if value {
			return 1
		}
		return 0
	}
	bandwidth := int32(-1000)
	version := uint32(4)
	if includeStreamTrace {
		version++
	}
	payload := libopustest.NewOraclePayloadVersion("GMEI", version,
		uint32(sampleRate), uint32(channels), mappingFamily, application,
		uint32(int32(bitrate)), boolU32(vbr), boolU32(vbrConstraint), uint32(complexity),
		uint32(bandwidth), uint32(frameSize), uint32(frameCount), maxPacketBytes,
		sampleFormat,
		0, // DTX disabled
	)
	for frame := range frameCount {
		payload.U32(boolU32(resetBefore[frame]))
		if sampleFormat == 1 {
			if len(pcm16[frame]) != channels*frameSize {
				return nil, 0, 0, nil, nil, nil, fmt.Errorf("frame %d has %d int16 samples, want %d", frame, len(pcm16[frame]), channels*frameSize)
			}
			for _, sample := range pcm16[frame] {
				payload.I16(sample)
			}
		} else {
			if len(pcmFloat[frame]) != channels*frameSize {
				return nil, 0, 0, nil, nil, nil, fmt.Errorf("frame %d has %d float32 samples, want %d", frame, len(pcmFloat[frame]), channels*frameSize)
			}
			payload.Float32s(pcmFloat[frame]...)
		}
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), fixedSurroundReferenceLabel()+" multistream surround encode", "GMEO", version)
	if err != nil {
		return nil, 0, 0, nil, nil, nil, err
	}
	streams, coupled := int(reader.U32()), int(reader.U32())
	gotChannels := int(reader.U32())
	if gotChannels != channels {
		return nil, 0, 0, nil, nil, nil, fmt.Errorf("oracle channels=%d want %d", gotChannels, channels)
	}
	mapping := append([]byte(nil), reader.Bytes(channels)...)
	packetCount := int(reader.U32())
	if packetCount != frameCount {
		return nil, 0, 0, nil, nil, nil, fmt.Errorf("oracle packet count=%d want %d", packetCount, frameCount)
	}
	packets := make([][]byte, packetCount)
	ranges := make([]uint32, packetCount)
	var streamsByFrame [][]fixedSurroundStreamOracle
	if includeStreamTrace {
		streamsByFrame = make([][]fixedSurroundStreamOracle, packetCount)
	}
	for frame := range packets {
		ranges[frame] = reader.U32()
		n := int(reader.U32())
		packets[frame] = append([]byte(nil), reader.Bytes(n)...)
		if includeStreamTrace {
			streamCount := int(reader.U32())
			if streamCount != streams {
				return nil, 0, 0, nil, nil, nil, fmt.Errorf("frame %d oracle stream count=%d want %d", frame, streamCount, streams)
			}
			streamsByFrame[frame] = make([]fixedSurroundStreamOracle, streamCount)
			for stream := range streamsByFrame[frame] {
				trace := &streamsByFrame[frame][stream]
				trace.toc = byte(reader.U32())
				trace.finalRange = reader.U32()
				trace.bitrate = int32(reader.U32())
				trace.bandwidth = int32(reader.U32())
				trace.forceChannels = int32(reader.U32())
				trace.complexity = int32(reader.U32())
				trace.packetLen = int(reader.U32())
				trace.decodedSamples = int(reader.U32())
			}
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, 0, 0, nil, nil, nil, err
	}
	return mapping, streams, coupled, packets, ranges, streamsByFrame, nil
}

func fixedSurroundAnalysisRef(sampleRate, channels, frameSize int, pcm [][]int16, resetBefore []bool) ([][]int32, [][]int32, error) {
	if len(pcm) == 0 || len(pcm) != len(resetBefore) {
		return nil, nil, fmt.Errorf("invalid analyzer frame sequence")
	}
	cfg := fixedSurroundReferenceConfig()
	cfg.Label = fixedSurroundReferenceLabel() + " surround analyzer"
	cfg.OutputBase = "gopus_libopus_surround_analysis_fixed"
	cfg.SourceFile = "libopus_surround_analysis_fixed_info.c"
	cfg.CFlags = []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}
	cfg.RefIncludes = []string{"celt", "silk", "src"}
	cfg.Libs = []string{fixedSurroundReferenceArchive(), "-lm"}
	binPath, err := fixedSurroundAnalysisHelper.CHelperPath(cfg)
	if err != nil {
		return nil, nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GSRI", 2,
		uint32(sampleRate), uint32(channels), uint32(frameSize), uint32(len(pcm)))
	for frame := range pcm {
		if len(pcm[frame]) != channels*frameSize {
			return nil, nil, fmt.Errorf("frame %d has %d samples, want %d", frame, len(pcm[frame]), channels*frameSize)
		}
		if resetBefore[frame] {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		for _, sample := range pcm[frame] {
			payload.I16(sample)
		}
	}

	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), fixedSurroundReferenceLabel()+" surround analyzer", "GSRO", 2)
	if err != nil {
		return nil, nil, err
	}
	if got := int(reader.U32()); got != channels {
		return nil, nil, fmt.Errorf("analyzer channels=%d want %d", got, channels)
	}
	if got := int(reader.U32()); got != len(pcm) {
		return nil, nil, fmt.Errorf("analyzer frame count=%d want %d", got, len(pcm))
	}
	rawFrames := make([][]int32, len(pcm))
	maskedFrames := make([][]int32, len(pcm))
	for frame := range rawFrames {
		rawFrames[frame] = make([]int32, channels*surroundBands)
		maskedFrames[frame] = make([]int32, channels*surroundBands)
		for i := range rawFrames[frame] {
			rawFrames[frame][i] = reader.I32()
		}
		for i := range maskedFrames[frame] {
			maskedFrames[frame][i] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return rawFrames, maskedFrames, nil
}

func fixedSurroundStreamMismatch(enc *Encoder, sampleRate int, gotPacket, refPacket []byte, gotRange, refRange uint32, refStreams []fixedSurroundStreamOracle) string {
	gotPackets, err := parseMultistreamPacket(gotPacket, len(refStreams))
	if err != nil {
		return fmt.Sprintf("cannot split Go multistream packet: %v", err)
	}
	refPackets, err := parseMultistreamPacket(refPacket, len(refStreams))
	if err != nil {
		return fmt.Sprintf("cannot split oracle multistream packet: %v", err)
	}
	if len(gotPackets) != len(refPackets) || len(gotPackets) != len(refStreams) || len(enc.encoders) != len(refStreams) {
		return fmt.Sprintf("elementary stream counts Go/C/trace/encoders=%d/%d/%d/%d", len(gotPackets), len(refPackets), len(refStreams), len(enc.encoders))
	}
	var gotRangeXOR uint32
	var cPacketBytes int
	for stream, goPacket := range gotPackets {
		cPacket := refPackets[stream]
		trace := refStreams[stream]
		child := enc.encoders[stream]
		if len(goPacket) == 0 || len(cPacket) == 0 {
			return fmt.Sprintf("stream %d empty elementary packet Go/C=%d/%d", stream, len(goPacket), len(cPacket))
		}
		gotTOC := parseStreamTOC(goPacket[0])
		cTOC := parseStreamTOC(trace.toc)
		cPacketBytes += trace.packetLen
		gotRangeXOR ^= child.FinalRange()
		packetDiff := firstByteMismatch(goPacket, cPacket)
		streamRange := child.FinalRange()
		parsedGoPacket, parseErr := parseOpusPacket(goPacket, false)
		if parseErr != nil {
			return fmt.Sprintf("stream %d cannot parse Go elementary packet: %v", stream, parseErr)
		}
		decodedSamples := opusSamplesPerFrameAtRate(goPacket[0], sampleRate) * len(parsedGoPacket.frames)
		if packetDiff >= 0 || streamRange != trace.finalRange || goPacket[0] != trace.toc ||
			child.Bitrate() != int(trace.bitrate) || int(child.Bandwidth()) != fixedSurroundBandwidthValue(trace.bandwidth) ||
			int32(child.ForceChannels()) != fixedSurroundForceChannelsValue(trace.forceChannels) || int32(child.Complexity()) != trace.complexity ||
			decodedSamples != trace.decodedSamples {
			gotChannels, cChannels := 1, 1
			if gotTOC.stereo {
				gotChannels = 2
			}
			if cTOC.stereo {
				cChannels = 2
			}
			return fmt.Sprintf("stream %d lfe=%v packetDiff=%d packetLen Go/C=%d/%d TOC Go/C=%02x/%02x actual(mode,bw,ch) Go/C=(%d,%d,%d)/(%d,%d,%d) controls(rate,bw) Go/C=(%d,%d)/(%d,%d) configuredModeGo=%d forceChannels Go/C=%d/%d complexity Go/C=%d/%d samples Go/C=%d/%d finalRange Go/C=%08x/%08x",
				stream, child.LFE(), packetDiff, len(goPacket), len(cPacket), goPacket[0], trace.toc,
				gotTOC.mode, gotTOC.bandwidth, gotChannels, cTOC.mode, cTOC.bandwidth, cChannels,
				child.Bitrate(), child.Bandwidth(), trace.bitrate, trace.bandwidth, child.Mode(), child.ForceChannels(), trace.forceChannels,
				child.Complexity(), trace.complexity, decodedSamples, trace.decodedSamples, streamRange, trace.finalRange)
		}
	}
	if cPacketBytes != len(refPacket) {
		return fmt.Sprintf("oracle elementary packet lengths sum=%d, multistream packet=%d", cPacketBytes, len(refPacket))
	}
	if gotRangeXOR != gotRange || gotRange != refRange {
		return fmt.Sprintf("elementary final-range XOR Go/C=%08x/%08x outer Go/C=%08x/%08x", gotRangeXOR, refRange, gotRange, refRange)
	}
	return ""
}

func fixedSurroundCELTReplay(enc *Encoder, stream, sampleRate, frameSize int, vbr, vbrConstraint bool, records []fixedSurroundCELTReplayFrame) string {
	coreFrameSize := frameSize * 48000 / sampleRate
	if coreFrameSize != 120 && coreFrameSize != 240 && coreFrameSize != 480 && coreFrameSize != 960 || len(records) == 0 {
		return fmt.Sprintf("raw Q8 replay outside CELT core frame scope (rate=%d frame=%d core=%d records=%d)", sampleRate, frameSize, coreFrameSize, len(records))
	}
	if stream < 0 || stream >= len(enc.encoders) {
		return fmt.Sprintf("raw Q8 replay stream %d outside encoder layout", stream)
	}
	child := enc.encoders[stream]
	channels := len(records[0].oracle.PCM) / frameSize
	streamChannels := 1
	if records[0].goPacket[0]&0x04 != 0 {
		streamChannels = 2
	}
	endBands := [...]int{13, 15, 17, 19, 21}
	bandwidth := parseStreamTOC(records[0].goPacket[0]).bandwidth
	if bandwidth < 0 || bandwidth >= len(endBands) {
		return fmt.Sprintf("raw Q8 replay has invalid CELT bandwidth %d", bandwidth)
	}
	bitrate := records[0].bitrate
	complexity := child.Complexity()
	lsbDepth := records[0].lsbDepth
	oracleFrames := make([]libopustest.CELTFixedQ8Frame, len(records))
	goPayloads := make([][]byte, len(records))
	cPayloads := make([][]byte, len(records))
	goRanges := make([]uint32, len(records))
	cRanges := make([]uint32, len(records))
	for frame := range records {
		record := records[frame]
		frameChannels := len(record.oracle.PCM) / frameSize
		if frameChannels != channels || len(record.goPacket) == 0 || len(record.cPacket) == 0 ||
			record.goPacket[0]&0x04 != records[0].goPacket[0]&0x04 {
			return fmt.Sprintf("raw Q8 replay input layout changes at frame %d", frame)
		}
		if record.bitrate != bitrate || record.lsbDepth != lsbDepth {
			return fmt.Sprintf("raw Q8 replay control changes at frame %d: rate=%d/%d lsb=%d/%d", frame, record.bitrate, bitrate, record.lsbDepth, lsbDepth)
		}
		oracleFrames[frame] = record.oracle
		goPayloads[frame] = record.goPacket[1:]
		cPayloads[frame] = record.cPacket[1:]
		goRanges[frame] = record.goRange
		cRanges[frame] = record.cRange
	}
	if channels < 1 || channels > 2 {
		return fmt.Sprintf("raw Q8 replay has unsupported CELT input channels %d", channels)
	}
	params := libopustest.CELTFixedQ8Params{
		SampleRate: sampleRate, Channels: channels, StreamChannels: streamChannels, FrameSize: frameSize,
		Start: 0, End: endBands[bandwidth], Bitrate: int(bitrate), Complexity: complexity,
		LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: vbrConstraint, LFE: child.LFE(), Frames: oracleFrames,
	}
	want, err := libopustest.ProbeCELTFixedQEXTQ8(params)
	if err != nil {
		return fmt.Sprintf("selected raw Q8 CELT oracle unavailable: %v", err)
	}
	if len(want) != len(records) {
		return fmt.Sprintf("raw Q8 CELT oracle frames=%d, want %d", len(want), len(records))
	}
	stateTrace, err := libopustest.ProbeCELTFixedQEXTQ8State(params)
	if err != nil {
		return fmt.Sprintf("selected raw Q8 CELT state trace unavailable: %v", err)
	}
	if len(stateTrace) != len(records) {
		return fmt.Sprintf("raw Q8 CELT state trace frames=%d, want %d", len(stateTrace), len(records))
	}
	for frame := range stateTrace {
		if len(stateTrace[frame].Packet) != len(want[frame].Packet) ||
			firstByteMismatch(stateTrace[frame].Packet, want[frame].Packet) >= 0 ||
			stateTrace[frame].FinalRange != want[frame].FinalRange {
			return fmt.Sprintf("raw Q8 CELT state helper packet/range differs from ordinary C helper at frame %d", frame)
		}
	}
	goReplay := fixedpoint.NewCELTEncoderRate(channels, sampleRate)
	goReplay.SetBandRange(0, endBands[bandwidth])
	goReplay.SetStreamChannels(int32(streamChannels))
	goReplay.SetBitrate(int(bitrate))
	goReplay.SetComplexity(complexity)
	goReplay.SetLSBDepth(lsbDepth)
	goReplay.SetVBR(vbr)
	goReplay.SetConstrainedVBR(vbrConstraint)
	goReplay.SetLFE(child.LFE())
	goReplayBuffer := make([]byte, 1275)
	goReplayRange := &rangecoding.Encoder{}
	goStates := make([]libopustest.CELTFixedQ8EncoderState, len(records))
	for frame := range records {
		input := oracleFrames[frame]
		if input.ResetBefore {
			goReplay.Reset()
		}
		goReplay.SetBandRange(0, endBands[bandwidth])
		goReplay.SetStreamChannels(int32(streamChannels))
		goReplay.SetBitrate(int(bitrate))
		goReplay.SetComplexity(complexity)
		goReplay.SetLSBDepth(lsbDepth)
		goReplay.SetPrediction(2)
		goReplay.SetSilkInfo(input.SilkSignalType, input.SilkOffset)
		analysis := input.Analysis
		goReplay.SetAnalysisInfo(fixedpoint.CELTAnalysisInfo{
			Valid: analysis.Valid, Bandwidth: analysis.Bandwidth,
			LeakBoost: analysis.LeakBoost, Activity: analysis.Activity,
			Tonality: analysis.Tonality, TonalitySlope: analysis.TonalitySlope,
			MaxPitchRatio: analysis.MaxPitchRatio,
		})
		goReplay.SetEnergyMask(input.EnergyMask)
		clear(goReplayBuffer)
		goReplayRange.Init(goReplayBuffer[:input.MaxBytes])
		goN := goReplay.EncodeWithECRes(input.PCM, frameSize, goReplayRange, input.MaxBytes)
		goRawPayload := goReplayRange.Buffer()[:goN]
		goRawRange := goReplayRange.Range()
		goStates[frame] = fixedSurroundCELTEncoderState(goReplay)
		goDiff := firstByteMismatch(goPayloads[frame], want[frame].Packet)
		cDiff := firstByteMismatch(cPayloads[frame], want[frame].Packet)
		goRawDiff := firstByteMismatch(goRawPayload, want[frame].Packet)
		if goDiff >= 0 || cDiff >= 0 || goRawDiff >= 0 || goRanges[frame] != want[frame].FinalRange ||
			cRanges[frame] != want[frame].FinalRange || goRawRange != want[frame].FinalRange {
			stateDiagnosis := "all traced state fields match"
			for i := 0; i <= frame; i++ {
				if diff := fixedSurroundCELTStateMismatch(goStates[i], stateTrace[i].State); diff != "" {
					stateDiagnosis = fmt.Sprintf("first C/Go state difference after frame %d: %s", i, diff)
					break
				}
			}
			return fmt.Sprintf("raw Q8 frame %d payload first diff publicGo/rawC publicC/rawC directGo/rawC=%d/%d/%d lengths Go/C/directGo/raw=%d/%d/%d/%d ranges Go/C/directGo/raw=%08x/%08x/%08x/%08x; %s",
				frame, goDiff, cDiff, goRawDiff, len(goPayloads[frame]), len(cPayloads[frame]), len(goRawPayload), len(want[frame].Packet),
				goRanges[frame], cRanges[frame], goRawRange, want[frame].FinalRange, stateDiagnosis)
		}
	}
	for frame := range stateTrace {
		if diff := fixedSurroundCELTStateMismatch(goStates[frame], stateTrace[frame].State); diff != "" {
			return fmt.Sprintf("raw Q8 CELT packet matched, but C/Go state differs after frame %d: %s", frame, diff)
		}
	}
	first, second := stateTrace[0].State, stateTrace[0].State
	if len(stateTrace) > 1 {
		second = stateTrace[1].State
	}
	return fmt.Sprintf("raw Q8 CELT matched Go/public C and traced state for %d frames (combined FIXED_POINT+ENABLE_QEXT archive, runtime-off; streamChannels=%d rate=%d endBand=%d); C prefilter frame0(period/gain/tapset)=%d/%d/%d frame1=%d/%d/%d",
		len(want), streamChannels, bitrate, endBands[bandwidth], first.PrefilterPeriod, first.PrefilterGain, first.PrefilterTapset,
		second.PrefilterPeriod, second.PrefilterGain, second.PrefilterTapset)
}

// fixedSurroundCELTEncoderState reads test-only logical state through reflection
// so the parity probe does not add state accessors or branches to the encoder.
func fixedSurroundCELTEncoderState(e *fixedpoint.CELTEncoder) libopustest.CELTFixedQ8EncoderState {
	state := libopustest.CELTFixedQ8EncoderState{}
	value := reflect.ValueOf(e).Elem()
	intField := func(name string) int32 { return int32(value.FieldByName(name).Int()) }
	boolField := func(name string) int32 {
		if value.FieldByName(name).Bool() {
			return 1
		}
		return 0
	}
	copyInt32Slice := func(v reflect.Value) []int32 {
		out := make([]int32, v.Len())
		for i := range out {
			out[i] = int32(v.Index(i).Int())
		}
		return out
	}
	state.RNG = uint32(value.FieldByName("rng").Uint())
	state.SpreadDecision = intField("spreadDecision")
	state.DelayedIntra = intField("delayedIntra")
	state.LastCodedBands = intField("lastCodedBands")
	state.PrefilterPeriod = intField("prefilterPeriod")
	state.PrefilterGain = int32(value.FieldByName("prefilterGain").Int())
	state.PrefilterTapset = intField("prefilterTapset")
	state.ConsecTransient = intField("consecTransient")
	state.VBRReservoir = intField("vbrReservoir")
	state.VBRDrift = intField("vbrDrift")
	state.VBROffset = intField("vbrOffset")
	state.VBRCount = intField("vbrCount")
	state.OverlapMax = intField("overlapMax")
	state.StereoSaving = int32(value.FieldByName("stereoSaving").Int())
	state.Intensity = intField("intensity")
	state.SpecAvg = intField("specAvg")
	state.ForceIntra = boolField("forceIntra")
	state.DisablePrefilter = boolField("disablePrefilter")
	state.SilkSignalType = intField("silkSignalType")
	state.SilkOffset = intField("silkOffset")
	analysis := value.FieldByName("analysis")
	state.AnalysisValid = 0
	if analysis.FieldByName("Valid").Bool() {
		state.AnalysisValid = 1
	}
	state.AnalysisBandwidth = int32(analysis.FieldByName("Bandwidth").Int())
	state.AnalysisActivityBits = math.Float32bits(float32(analysis.FieldByName("Activity").Float()))
	state.AnalysisTonalityBits = math.Float32bits(float32(analysis.FieldByName("Tonality").Float()))
	state.AnalysisSlopeBits = math.Float32bits(float32(analysis.FieldByName("TonalitySlope").Float()))
	state.AnalysisMaxPitchRatioBits = math.Float32bits(float32(analysis.FieldByName("MaxPitchRatio").Float()))
	leak := analysis.FieldByName("LeakBoost")
	for i := range state.AnalysisLeakBoost {
		state.AnalysisLeakBoost[i] = uint8(leak.Index(i).Uint())
	}
	spreading := value.FieldByName("spreading")
	state.TonalAverage = int32(spreading.FieldByName("TonalAverage").Int())
	state.HFAverage = int32(spreading.FieldByName("HFAverage").Int())
	state.TapsetDecision = int32(spreading.FieldByName("TapsetDecision").Int())
	for _, pair := range []struct {
		field string
		dest  *[]int32
	}{
		{"energyMask", &state.EnergyMask},
		{"preemphMemE", &state.PreemphMemE},
		{"inMem", &state.InMem},
		{"prefilterMem", &state.PrefilterMem},
		{"oldBandE", &state.OldBandE},
		{"oldLogE", &state.OldLogE},
		{"oldLogE2", &state.OldLogE2},
		{"energyError", &state.EnergyError},
	} {
		*pair.dest = copyInt32Slice(value.FieldByName(pair.field))
	}
	return state
}

func fixedSurroundCELTStateMismatch(goState, cState libopustest.CELTFixedQ8EncoderState) string {
	var differences []string
	appendInt32 := func(name string, got, want int32) {
		if got != want {
			differences = append(differences, fmt.Sprintf("%s Go/C=%d/%d", name, got, want))
		}
	}
	appendSlice := func(name string, got, want []int32) {
		if len(got) < len(want) {
			differences = append(differences, fmt.Sprintf("%s Go/C length=%d/%d", name, len(got), len(want)))
			return
		}
		first := -1
		count := 0
		for i := range want {
			if got[i] != want[i] {
				if first < 0 {
					first = i
				}
				count++
			}
		}
		if first >= 0 {
			differences = append(differences, fmt.Sprintf("%s first[%d] Go/C=%d/%d (%d differing values)", name, first, got[first], want[first], count))
		}
	}
	if goState.RNG != cState.RNG {
		differences = append(differences, fmt.Sprintf("rng Go/C=%08x/%08x", goState.RNG, cState.RNG))
	}
	for _, field := range []struct {
		name string
		got  int32
		want int32
	}{
		{"spreadDecision", goState.SpreadDecision, cState.SpreadDecision},
		{"delayedIntra", goState.DelayedIntra, cState.DelayedIntra},
		{"tonalAverage", goState.TonalAverage, cState.TonalAverage},
		{"lastCodedBands", goState.LastCodedBands, cState.LastCodedBands},
		{"hfAverage", goState.HFAverage, cState.HFAverage},
		{"tapsetDecision", goState.TapsetDecision, cState.TapsetDecision},
		{"prefilterPeriod", goState.PrefilterPeriod, cState.PrefilterPeriod},
		{"prefilterGain", goState.PrefilterGain, cState.PrefilterGain},
		{"prefilterTapset", goState.PrefilterTapset, cState.PrefilterTapset},
		{"consecTransient", goState.ConsecTransient, cState.ConsecTransient},
		{"vbrReservoir", goState.VBRReservoir, cState.VBRReservoir},
		{"vbrDrift", goState.VBRDrift, cState.VBRDrift},
		{"vbrOffset", goState.VBROffset, cState.VBROffset},
		{"vbrCount", goState.VBRCount, cState.VBRCount},
		{"overlapMax", goState.OverlapMax, cState.OverlapMax},
		{"stereoSaving", goState.StereoSaving, cState.StereoSaving},
		{"intensity", goState.Intensity, cState.Intensity},
		{"specAvg", goState.SpecAvg, cState.SpecAvg},
		{"forceIntra", goState.ForceIntra, cState.ForceIntra},
		{"disablePrefilter", goState.DisablePrefilter, cState.DisablePrefilter},
		{"silkSignalType", goState.SilkSignalType, cState.SilkSignalType},
		{"silkOffset", goState.SilkOffset, cState.SilkOffset},
		{"analysisValid", goState.AnalysisValid, cState.AnalysisValid},
		{"analysisBandwidth", goState.AnalysisBandwidth, cState.AnalysisBandwidth},
	} {
		appendInt32(field.name, field.got, field.want)
	}
	for _, field := range []struct {
		name string
		got  uint32
		want uint32
	}{
		{"analysisActivityBits", goState.AnalysisActivityBits, cState.AnalysisActivityBits},
		{"analysisTonalityBits", goState.AnalysisTonalityBits, cState.AnalysisTonalityBits},
		{"analysisSlopeBits", goState.AnalysisSlopeBits, cState.AnalysisSlopeBits},
		{"analysisMaxPitchRatioBits", goState.AnalysisMaxPitchRatioBits, cState.AnalysisMaxPitchRatioBits},
	} {
		if field.got != field.want {
			differences = append(differences, fmt.Sprintf("%s Go/C=%08x/%08x", field.name, field.got, field.want))
		}
	}
	if goState.AnalysisLeakBoost != cState.AnalysisLeakBoost {
		differences = append(differences, "analysisLeakBoost differs")
	}
	for _, field := range []struct {
		name string
		got  []int32
		want []int32
	}{
		{"energyMask", goState.EnergyMask, cState.EnergyMask},
		{"preemphMemE", goState.PreemphMemE, cState.PreemphMemE},
		{"inMem", goState.InMem, cState.InMem},
		{"prefilterMem", goState.PrefilterMem, cState.PrefilterMem},
		{"oldBandE", goState.OldBandE, cState.OldBandE},
		{"oldLogE", goState.OldLogE, cState.OldLogE},
		{"oldLogE2", goState.OldLogE2, cState.OldLogE2},
		{"energyError", goState.EnergyError, cState.EnergyError},
	} {
		appendSlice(field.name, field.got, field.want)
	}
	if len(differences) == 0 {
		return ""
	}
	if len(differences) > 6 {
		return fmt.Sprintf("%v and %d more", differences[:6], len(differences)-6)
	}
	return fmt.Sprint(differences)
}

func captureFixedSurroundCELTReplayFrame(enc *Encoder, stream, frameSize int, resetBefore bool, goPacket, cPacket []byte, cRange uint32) (fixedSurroundCELTReplayFrame, bool) {
	var captured fixedSurroundCELTReplayFrame
	if frameSize <= 0 || stream < 0 || stream >= len(enc.encoders) || len(goPacket) < 2 || len(cPacket) < 2 {
		return captured, false
	}
	child := enc.encoders[stream]
	input := child.LastFixedCELTInputQ8()
	if len(input) == 0 || len(input)%frameSize != 0 {
		return captured, false
	}
	bitrate, maxBytes, lsbDepth := child.LastFixedCELTControls()
	if maxBytes < 2 || maxBytes > 1275 {
		return captured, false
	}
	frame := libopustest.CELTFixedQ8Frame{
		PCM:            append([]int32(nil), input...),
		MaxBytes:       maxBytes,
		ResetBefore:    resetBefore,
		SilkSignalType: 0,
		SilkOffset:     0,
	}
	if !child.LFE() {
		c1, c2 := streamSourceChannels(enc.mapping, enc.coupledStreams, stream)
		channels := len(input) / frameSize
		if c1 < 0 || (channels == 2 && c2 < 0) || (channels == 1 && c2 >= 0) {
			return captured, false
		}
		frame.EnergyMask = make([]int32, channels*surroundBands)
		copy(frame.EnergyMask[:surroundBands], enc.surroundAnalysis.bandSMRQ24[c1*surroundBands:(c1+1)*surroundBands])
		if channels == 2 {
			copy(frame.EnergyMask[surroundBands:], enc.surroundAnalysis.bandSMRQ24[c2*surroundBands:(c2+1)*surroundBands])
		}
	}
	a := child.LastFixedCELTAnalysis()
	frame.Analysis = libopustest.CELTFixedQ8Analysis{
		Valid:               a.Valid,
		Tonality:            a.Tonality,
		TonalitySlope:       a.TonalitySlope,
		Noisiness:           a.NoisySpeech,
		Activity:            a.Activity,
		MusicProb:           a.MusicProb,
		MusicProbMin:        a.MusicProbMin,
		MusicProbMax:        a.MusicProbMax,
		Bandwidth:           a.BandwidthIndex,
		ActivityProbability: a.VADProb,
		MaxPitchRatio:       a.MaxPitchRatio,
		LeakBoost:           a.LeakBoost,
	}
	captured = fixedSurroundCELTReplayFrame{
		oracle: frame, goPacket: append([]byte(nil), goPacket...), cPacket: append([]byte(nil), cPacket...),
		goRange: child.FinalRange(), cRange: cRange, bitrate: bitrate, lsbDepth: lsbDepth,
	}
	return captured, true
}

func fixedSurroundRawBandLogsGo(enc *Encoder, sampleRate, frameSize int, pcm []int16, windowMem, preemphMem, inputScratch []int32, energies, logs []int32) bool {
	channels := enc.inputChannels
	upsample := resamplingFactor(sampleRate)
	analysisFrameSize := frameSize * upsample
	freqSize, ok := surroundAnalysisFreqSize(analysisFrameSize)
	if !ok || analysisFrameSize%freqSize != 0 || len(windowMem) < channels*surroundOverlap ||
		len(preemphMem) < channels || len(inputScratch) < surroundOverlap+analysisFrameSize ||
		len(energies) < channels*surroundBands || len(logs) < surroundBands {
		return false
	}
	lm := 0
	for lm < surroundMaxLM && (120<<lm) != freqSize {
		lm++
	}
	if (120 << lm) != freqSize {
		return false
	}
	input := inputScratch[:surroundOverlap+analysisFrameSize]
	for ch := 0; ch < channels; ch++ {
		copy(input[:surroundOverlap], windowMem[ch*surroundOverlap:(ch+1)*surroundOverlap])
		clear(input[surroundOverlap:])
		for i := 0; i < frameSize; i++ {
			input[surroundOverlap+i*upsample] = int32(pcm[i*channels+ch]) << 12
		}
		m := preemphMem[ch]
		for i := 0; i < analysisFrameSize; i++ {
			x := input[surroundOverlap+i]
			input[surroundOverlap+i] = x - m
			m = int32((int64(27853) * int64(x)) >> 15)
		}
		preemphMem[ch] = m
		row := energies[ch*surroundBands : (ch+1)*surroundBands]
		clear(row)
		for subframe := 0; subframe < analysisFrameSize/freqSize; subframe++ {
			start := subframe * freqSize
			if !enc.surroundAnalysis.bandAnalyzer.BandEnergyInto(input[start:], lm, upsample, logs) {
				return false
			}
			for band := range surroundBands {
				if logs[band] > row[band] {
					row[band] = logs[band]
				}
			}
		}
		fixedpoint.Amp2Log2(row, logs, surroundBands, surroundBands, surroundBands, 1)
		copy(row, logs)
		copy(windowMem[ch*surroundOverlap:(ch+1)*surroundOverlap], input[analysisFrameSize:analysisFrameSize+surroundOverlap])
	}
	return true
}

func makeFixedSurroundPCM(sampleRate, channels, frameSize, frameCount int) [][]int16 {
	frequencies := [...]float64{220, 330, 440, 550, 660, 75, 880, 990}
	frames := make([][]int16, frameCount)
	for frame := range frames {
		frames[frame] = make([]int16, channels*frameSize)
		for sample := range frameSize {
			t := float64(frame*frameSize+sample) / float64(sampleRate)
			for channel := range channels {
				amplitude := 7000 + channel*1900
				if channel == 5 {
					amplitude = 18000
				}
				phase := 2 * math.Pi * frequencies[channel] * (1 + float64(channel)*0.025) * t
				frames[frame][sample*channels+channel] = int16(float64(amplitude) * math.Sin(phase))
			}
		}
	}
	return frames
}

func makeFixedSurroundFloatPCM(sampleRate, channels, frameSize, frameCount int) [][]float32 {
	frequencies := [...]float64{220, 330, 440, 550, 660, 75, 880, 990}
	frames := make([][]float32, frameCount)
	for frame := range frames {
		frames[frame] = make([]float32, channels*frameSize)
		for sample := range frameSize {
			t := float64(frame*frameSize+sample) / float64(sampleRate)
			for channel := range channels {
				amplitude := 0.2 + float64(channel)*0.035
				if channel == 5 {
					amplitude = 0.68
				}
				phase := 2 * math.Pi * frequencies[channel] * (1 + float64(channel)*0.025) * t
				frames[frame][sample*channels+channel] = float32(amplitude * math.Sin(phase))
			}
		}
	}
	return frames
}

func TestFixedPointSurroundEncodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name       string
		sampleRate int
		channels   int
		frameSize  int
		bitrate    int
		vbr        bool
		complexity int
	}{
		{name: "3ch_lcr_10ms_vbr", sampleRate: 48000, channels: 3, frameSize: 480, bitrate: 96000, vbr: true, complexity: 5},
		{name: "4ch_quad_10ms_vbr", sampleRate: 48000, channels: 4, frameSize: 480, bitrate: 128000, vbr: true, complexity: 5},
		{name: "5ch_surround_10ms_vbr", sampleRate: 48000, channels: 5, frameSize: 480, bitrate: 160000, vbr: true, complexity: 5},
		{name: "5.1_10ms_vbr", sampleRate: 48000, channels: 6, frameSize: 480, bitrate: 160000, vbr: true, complexity: 10},
		{name: "6.1_10ms_vbr", sampleRate: 48000, channels: 7, frameSize: 480, bitrate: 224000, vbr: true, complexity: 5},
		{name: "7.1_20ms_cbr", sampleRate: 48000, channels: 8, frameSize: 960, bitrate: 320000, vbr: false, complexity: 5},
		{name: "7.1_40ms_24k_upsampled", sampleRate: 24000, channels: 8, frameSize: 960, bitrate: 256000, vbr: true, complexity: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const frameCount = 6
			pcm := makeFixedSurroundPCM(tc.sampleRate, tc.channels, tc.frameSize, frameCount)
			resetBefore := []bool{false, false, false, true, false, true}
			refMapping, refStreams, refCoupled, refPackets, refRanges, _, err := fixedSurroundEncodeRef(
				tc.sampleRate, tc.channels, tc.frameSize, tc.bitrate, tc.complexity, tc.vbr, true, 1, pcm, nil, resetBefore, false)
			if err != nil {
				libopustest.HelperUnavailable(t, fixedSurroundReferenceLabel()+" multistream surround encode", err)
			}

			enc, err := NewEncoderDefault(tc.sampleRate, tc.channels)
			if err != nil {
				t.Fatalf("NewEncoderDefault: %v", err)
			}
			if enc.Streams() != refStreams || enc.CoupledStreams() != refCoupled || !bytes.Equal(enc.mapping, refMapping) {
				t.Fatalf("layout Go=%d/%d/%v C=%d/%d/%v", enc.Streams(), enc.CoupledStreams(), enc.mapping, refStreams, refCoupled, refMapping)
			}
			enc.SetBitrate(tc.bitrate)
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(true)
			enc.SetComplexity(tc.complexity)
			enc.SetBandwidthAuto()

			out := make([]byte, 4000)
			replayFrames := make([][]fixedSurroundCELTReplayFrame, refStreams)
			for frame := range pcm {
				if resetBefore[frame] {
					enc.Reset()
				}
				n, err := enc.EncodeInt16(pcm[frame], tc.frameSize, out)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				got := out[:n]
				gotElementary, splitErr := parseMultistreamPacket(got, enc.Streams())
				if splitErr != nil {
					t.Fatalf("frame %d split Go multistream packet: %v", frame, splitErr)
				}
				cElementary, splitErr := parseMultistreamPacket(refPackets[frame], refStreams)
				if splitErr != nil {
					t.Fatalf("frame %d split oracle multistream packet: %v", frame, splitErr)
				}
				for stream := range replayFrames {
					captured, ok := captureFixedSurroundCELTReplayFrame(enc, stream, tc.frameSize, resetBefore[frame],
						gotElementary[stream], cElementary[stream], 0)
					if ok {
						replayFrames[stream] = append(replayFrames[stream], captured)
					}
				}
				if !bytes.Equal(got, refPackets[frame]) || enc.GetFinalRange() != refRanges[frame] {
					traceMapping, traceStreams, traceCoupled, tracePackets, traceRanges, trace, traceErr := fixedSurroundEncodeRef(
						tc.sampleRate, tc.channels, tc.frameSize, tc.bitrate, tc.complexity, tc.vbr, true, 1, pcm, nil, resetBefore, true)
					if traceErr != nil {
						t.Fatalf("frame %d packet mismatch; rerun with elementary trace: %v", frame, traceErr)
					}
					if traceStreams != refStreams || traceCoupled != refCoupled || !bytes.Equal(traceMapping, refMapping) || len(tracePackets) != len(refPackets) {
						t.Fatalf("frame %d traced oracle layout differs from packet oracle", frame)
					}
					for i := range refPackets {
						if !bytes.Equal(tracePackets[i], refPackets[i]) || traceRanges[i] != refRanges[i] {
							t.Fatalf("traced oracle frame %d changed packet/range: packetEqual=%v range=%08x/%08x", i,
								bytes.Equal(tracePackets[i], refPackets[i]), traceRanges[i], refRanges[i])
						}
					}
					traceElementary, splitErr := parseMultistreamPacket(tracePackets[frame], traceStreams)
					if splitErr != nil {
						t.Fatalf("frame %d split traced oracle packet: %v", frame, splitErr)
					}
					for stream := range replayFrames {
						for previous := range replayFrames[stream] {
							replayFrames[stream][previous].cRange = trace[previous][stream].finalRange
						}
					}
					streamDetail := fixedSurroundStreamMismatch(enc, tc.sampleRate, got, tracePackets[frame], enc.GetFinalRange(), traceRanges[frame], trace[frame])
					rawReplay := "no elementary packet/range divergence"
					for stream := range gotElementary {
						if firstByteMismatch(gotElementary[stream], traceElementary[stream]) >= 0 ||
							enc.encoders[stream].FinalRange() != trace[frame][stream].finalRange {
							rawReplay = fixedSurroundCELTReplay(enc, stream, tc.sampleRate, tc.frameSize, tc.vbr, true, replayFrames[stream])
							break
						}
					}
					t.Fatalf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x; elementary: %s; raw Q8: %s", frame,
						firstByteMismatch(got, refPackets[frame]), len(got), len(refPackets[frame]), enc.GetFinalRange(), refRanges[frame], streamDetail, rawReplay)
				}
			}
			if strings.Contains(fixedSurroundReferenceLabel(), "ENABLE_QEXT") {
				rawStream := -1
				switch tc.name {
				case "5.1_10ms_vbr":
					rawStream = 2
				case "7.1_20ms_cbr":
					rawStream = 1
				}
				if rawStream >= 0 {
					traceMapping, traceStreams, traceCoupled, tracePackets, traceRanges, streamTrace, traceErr := fixedSurroundEncodeRef(
						tc.sampleRate, tc.channels, tc.frameSize, tc.bitrate, tc.complexity, tc.vbr, true, 1, pcm, nil, resetBefore, true)
					if traceErr != nil {
						t.Fatalf("raw stream %d trace setup: %v", rawStream, traceErr)
					}
					if traceStreams != refStreams || traceCoupled != refCoupled || !bytes.Equal(traceMapping, refMapping) || len(tracePackets) != len(refPackets) {
						t.Fatalf("raw stream %d traced oracle layout differs", rawStream)
					}
					for i := range refPackets {
						if !bytes.Equal(tracePackets[i], refPackets[i]) || traceRanges[i] != refRanges[i] {
							t.Fatalf("raw stream %d traced oracle changed outer frame %d", rawStream, i)
						}
						replayFrames[rawStream][i].cRange = streamTrace[i][rawStream].finalRange
					}
					detail := fixedSurroundCELTReplay(enc, rawStream, tc.sampleRate, tc.frameSize, tc.vbr, true, replayFrames[rawStream])
					if !strings.HasPrefix(detail, "raw Q8 CELT matched") {
						t.Fatalf("raw stream %d replay: %s", rawStream, detail)
					}
					t.Log(detail)
				}
			}
		})
	}
}

func TestFixedPointSurroundAnalysisMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, tc := range []struct {
		name       string
		sampleRate int
		channels   int
		frameSize  int
	}{
		{name: "3ch_48k", sampleRate: 48000, channels: 3, frameSize: 480},
		{name: "4ch_48k", sampleRate: 48000, channels: 4, frameSize: 480},
		{name: "5ch_48k", sampleRate: 48000, channels: 5, frameSize: 480},
		{name: "5.1_48k", sampleRate: 48000, channels: 6, frameSize: 480},
		{name: "6.1_48k", sampleRate: 48000, channels: 7, frameSize: 480},
		{name: "7.1_48k_long", sampleRate: 48000, channels: 8, frameSize: 960},
		{name: "7.1_24k_upsampled_long", sampleRate: 24000, channels: 8, frameSize: 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const frameCount = 6
			pcm := makeFixedSurroundPCM(tc.sampleRate, tc.channels, tc.frameSize, frameCount)
			resetBefore := []bool{false, false, false, true, false, true}
			rawRef, ref, err := fixedSurroundAnalysisRef(tc.sampleRate, tc.channels, tc.frameSize, pcm, resetBefore)
			if err != nil {
				libopustest.HelperUnavailable(t, fixedSurroundReferenceLabel()+" surround analyzer", err)
			}

			enc, err := NewEncoderDefault(tc.sampleRate, tc.channels)
			if err != nil {
				t.Fatalf("NewEncoderDefault: %v", err)
			}
			floatPCM := make([]float32, tc.channels*tc.frameSize)
			rawWindowMem := make([]int32, tc.channels*surroundOverlap)
			rawPreemphMem := make([]int32, tc.channels)
			rawInputScratch := make([]int32, surroundOverlap+tc.frameSize*resamplingFactor(tc.sampleRate))
			rawEnergyScratch := make([]int32, tc.channels*surroundBands)
			rawLogScratch := make([]int32, surroundBands)
			rawMismatch := ""
			for frame := range pcm {
				if resetBefore[frame] {
					enc.surroundAnalysis.reset()
					clear(rawWindowMem)
					clear(rawPreemphMem)
				}
				if !fixedSurroundRawBandLogsGo(enc, tc.sampleRate, tc.frameSize, pcm[frame], rawWindowMem, rawPreemphMem,
					rawInputScratch, rawEnergyScratch, rawLogScratch) {
					t.Fatalf("frame %d raw surround analysis rejected valid input", frame)
				}
				for i, got := range rawEnergyScratch {
					if rawMismatch == "" && got != rawRef[frame][i] {
						rawMismatch = fmt.Sprintf("frame %d raw band log channel %d band %d Go/C=%08x/%08x", frame,
							i/surroundBands, i%surroundBands, got, rawRef[frame][i])
					}
				}
				for i, sample := range pcm[frame] {
					floatPCM[i] = float32(sample) * (1.0 / 32768)
				}
				if !enc.surroundAnalysis.computeBandSMRQ24(enc, tc.frameSize, encodeInput{f32: floatPCM, i16: pcm[frame]}) {
					t.Fatalf("frame %d surround analysis rejected valid input", frame)
				}
				for i, got := range enc.surroundAnalysis.bandSMRQ24[:tc.channels*surroundBands] {
					if got != ref[frame][i] {
						t.Fatalf("frame %d channel %d band %d Go/C=%08x/%08x (raw stage: %s)", frame, i/surroundBands, i%surroundBands, got, ref[frame][i], rawMismatch)
					}
				}
			}
			if rawMismatch != "" {
				t.Error(rawMismatch)
			}
		})
	}
}

func TestFixedPointSurroundMaskRoutingMatchesAnalyzer(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 480
	)
	for channels := 3; channels <= 8; channels++ {
		t.Run(fmt.Sprintf("%d_channels", channels), func(t *testing.T) {
			pcm := makeFixedSurroundPCM(sampleRate, channels, frameSize, 1)[0]
			floatPCM := make([]float32, len(pcm))
			for i, sample := range pcm {
				floatPCM[i] = float32(sample) / 32768
			}
			_, want, err := fixedSurroundAnalysisRef(sampleRate, channels, frameSize, [][]int16{pcm}, []bool{false})
			if err != nil {
				libopustest.HelperUnavailable(t, fixedSurroundReferenceLabel()+" surround analyzer", err)
			}

			enc, err := NewEncoderDefault(sampleRate, channels)
			if err != nil {
				t.Fatalf("NewEncoderDefault: %v", err)
			}
			enc.surroundAnalysis.applyEnergyMasks(enc, frameSize, encodeInput{f32: floatPCM, i16: pcm})
			for i, got := range enc.surroundAnalysis.bandSMRQ24[:channels*surroundBands] {
				if got != want[0][i] {
					t.Fatalf("analyzer channel %d band %d Go/C=%08x/%08x", i/surroundBands, i%surroundBands, got, want[0][i])
				}
			}

			for stream, streamEnc := range enc.encoders {
				got := streamEnc.CELTEnergyMask()
				if stream == enc.lfeStream {
					if len(got) != 0 {
						t.Fatalf("LFE stream %d mask length=%d, want 0", stream, len(got))
					}
					continue
				}
				c1, c2 := streamSourceChannels(enc.mapping, enc.coupledStreams, stream)
				wantChannels := 1
				if c2 >= 0 {
					wantChannels = 2
				}
				if len(got) != wantChannels*surroundBands {
					t.Fatalf("stream %d mask length=%d, want %d", stream, len(got), wantChannels*surroundBands)
				}
				for band := range surroundBands {
					wantFirst := float32(want[0][c1*surroundBands+band]) * (1.0 / float32(1<<24))
					if got[band] != wantFirst {
						t.Fatalf("stream %d channel %d band %d mask=%08x, want %08x", stream, c1, band,
							math.Float32bits(got[band]), math.Float32bits(wantFirst))
					}
					if c2 >= 0 {
						wantSecond := float32(want[0][c2*surroundBands+band]) * (1.0 / float32(1<<24))
						if got[surroundBands+band] != wantSecond {
							t.Fatalf("stream %d channel %d band %d mask=%08x, want %08x", stream, c2, band,
								math.Float32bits(got[surroundBands+band]), math.Float32bits(wantSecond))
						}
					}
				}
			}
		})
	}
}

func TestFixedPointFloatSurroundEncodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 6
		frameSize  = 480
		frameCount = 6
		bitrate    = 192000
		complexity = 10
	)
	pcm := makeFixedSurroundFloatPCM(sampleRate, channels, frameSize, frameCount)
	resetBefore := []bool{false, false, true, false, false, true}
	refMapping, refStreams, refCoupled, refPackets, refRanges, _, err := fixedSurroundEncodeRef(
		sampleRate, channels, frameSize, bitrate, complexity, true, true, 0, nil, pcm, resetBefore, false)
	if err != nil {
		libopustest.HelperUnavailable(t, fixedSurroundReferenceLabel()+" float multistream surround encode", err)
	}

	enc, err := NewEncoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault: %v", err)
	}
	if enc.Streams() != refStreams || enc.CoupledStreams() != refCoupled || !bytes.Equal(enc.mapping, refMapping) {
		t.Fatalf("layout Go=%d/%d/%v C=%d/%d/%v", enc.Streams(), enc.CoupledStreams(), enc.mapping, refStreams, refCoupled, refMapping)
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetComplexity(complexity)
	enc.SetBandwidthAuto()
	out := make([]byte, 4000)
	for frame := range pcm {
		if resetBefore[frame] {
			enc.Reset()
		}
		n, err := enc.Encode(pcm[frame], frameSize, out)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", frame, err)
		}
		got := out[:n]
		if !bytes.Equal(got, refPackets[frame]) || enc.GetFinalRange() != refRanges[frame] {
			traceMapping, traceStreams, traceCoupled, tracePackets, traceRanges, trace, traceErr := fixedSurroundEncodeRef(
				sampleRate, channels, frameSize, bitrate, complexity, true, true, 0, nil, pcm, resetBefore, true)
			if traceErr != nil {
				t.Fatalf("frame %d packet mismatch; rerun with elementary trace: %v", frame, traceErr)
			}
			if traceStreams != refStreams || traceCoupled != refCoupled || !bytes.Equal(traceMapping, refMapping) ||
				len(tracePackets) != len(refPackets) || !bytes.Equal(tracePackets[frame], refPackets[frame]) || traceRanges[frame] != refRanges[frame] {
				t.Fatalf("frame %d traced oracle differs from packet oracle", frame)
			}
			gotStreamPackets, splitErr := parseMultistreamPacket(got, enc.Streams())
			if splitErr != nil {
				t.Fatalf("frame %d split Go packet: %v", frame, splitErr)
			}
			cStreamPackets, splitErr := parseMultistreamPacket(tracePackets[frame], traceStreams)
			if splitErr != nil {
				t.Fatalf("frame %d split traced C packet: %v", frame, splitErr)
			}
			streamDetail := fixedSurroundStreamMismatch(enc, sampleRate, got, tracePackets[frame], enc.GetFinalRange(), traceRanges[frame], trace[frame])
			t.Fatalf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x; elementary: %s; Go/C stream counts=%d/%d",
				frame, firstByteMismatch(got, refPackets[frame]), len(got), len(refPackets[frame]), enc.GetFinalRange(), refRanges[frame],
				streamDetail, len(gotStreamPackets), len(cStreamPackets))
		}
	}
}
