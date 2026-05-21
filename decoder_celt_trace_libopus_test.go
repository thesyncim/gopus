package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusCELTTraceHelper libopustest.HelperCache

type libopusCELTTrace struct {
	decodedSamples      int
	internalSamples     int
	downsample          int
	overlap             int
	decodeBuffer        int
	channel             int
	start               int
	finalRange          uint32
	celtRng             uint32
	lossDuration        int
	plcDuration         int
	postfilter          int
	postfilterOld       int
	postfilterGain      float32
	postfilterGainOld   float32
	postfilterTapset    int
	postfilterTapsetOld int
	lastPitchPeriod     int
	lastFrameType       int
	skipPLC             bool
	prefilterFold       bool
	oldBandE            []float32
	final               []float32
	preDeemphasis       []float32
}

func buildLibopusCELTTraceHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT decode trace",
		OutputBase:  "gopus_libopus_celt_trace",
		SourceFile:  "libopus_celt_trace_single.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func traceLibopusCELT(sampleRate, channels, frameSize, targetStep, targetChannel, start, count int, steps []libopusAPIRateDecodeStep) (*libopusCELTTrace, error) {
	binPath, err := libopusCELTTraceHelper.Path(buildLibopusCELTTraceHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GCTI",
		uint32(sampleRate), uint32(channels), uint32(frameSize),
		uint32(targetStep), uint32(targetChannel), uint32(start), uint32(count),
		uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT decode trace", "GCTO")
	if err != nil {
		return nil, err
	}
	trace := &libopusCELTTrace{
		decodedSamples:  int(reader.U32()),
		internalSamples: int(reader.U32()),
		downsample:      int(reader.U32()),
		overlap:         int(reader.U32()),
		decodeBuffer:    int(reader.U32()),
		channel:         int(reader.U32()),
		start:           int(reader.U32()),
	}
	outCount := int(reader.U32())
	trace.finalRange = reader.U32()
	trace.celtRng = reader.U32()
	trace.lossDuration = int(reader.U32())
	trace.plcDuration = int(reader.U32())
	trace.postfilter = int(reader.U32())
	trace.postfilterOld = int(reader.U32())
	trace.postfilterGain = reader.Float32()
	trace.postfilterGainOld = reader.Float32()
	trace.postfilterTapset = int(reader.U32())
	trace.postfilterTapsetOld = int(reader.U32())
	trace.lastPitchPeriod = int(reader.U32())
	trace.lastFrameType = int(reader.U32())
	trace.skipPLC = reader.U32() != 0
	trace.prefilterFold = reader.U32() != 0
	oldBandECount := int(reader.U32())
	trace.final = make([]float32, outCount)
	trace.preDeemphasis = make([]float32, outCount)
	trace.oldBandE = make([]float32, oldBandECount)
	reader.ExpectRemaining((outCount*2 + oldBandECount) * 4)
	for i := range trace.final {
		trace.final[i] = reader.Float32()
	}
	for i := range trace.preDeemphasis {
		trace.preDeemphasis[i] = reader.Float32()
	}
	for i := range trace.oldBandE {
		trace.oldBandE[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return trace, nil
}

func TestLibopusCELTTraceMatchesReferenceDecodeWindow(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		start      = 384
		count      = 64
	)
	steps := []libopusAPIRateDecodeStep{{packet: packet}}
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, start, count, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}
	if trace.decodedSamples != frameSize || trace.internalSamples != frameSize {
		t.Fatalf("trace samples decoded=%d internal=%d want %d", trace.decodedSamples, trace.internalSamples, frameSize)
	}
	if trace.downsample != 1 || trace.decodeBuffer != 2048 || trace.overlap <= 0 {
		t.Fatalf("trace metadata downsample=%d decodeBuffer=%d overlap=%d", trace.downsample, trace.decodeBuffer, trace.overlap)
	}
	if trace.channel != 0 || trace.start != start || len(trace.final) != count || len(trace.preDeemphasis) != count {
		t.Fatalf("trace window channel=%d start=%d final=%d pre=%d", trace.channel, trace.start, len(trace.final), len(trace.preDeemphasis))
	}

	want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "api-rate reference decode", err)
	}
	for i, got := range trace.final {
		if got != want[start+i] {
			t.Fatalf("trace final[%d]=%08x want %08x", start+i, math.Float32bits(got), math.Float32bits(want[start+i]))
		}
	}
	nonZero := false
	for _, sample := range trace.preDeemphasis {
		if sample != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatal("trace pre-deemphasis window is silent")
	}
}

func TestLibopusCELTTraceFinalRangeMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, 0, 1, []libopusAPIRateDecodeStep{{packet: packet}})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode samples=%d want %d", n, frameSize)
	}
	if got := dec.FinalRange(); got != trace.finalRange {
		t.Fatalf("FinalRange()=0x%08x want libopus 0x%08x", got, trace.finalRange)
	}
	if trace.lossDuration != 0 || trace.plcDuration != 0 {
		t.Fatalf("unexpected good-frame loss state: loss=%d plc=%d", trace.lossDuration, trace.plcDuration)
	}
}

func TestLibopusCELTTracePostfilterStateMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
	goodTrace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, 0, 1, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}
	lossTrace, err := traceLibopusCELT(sampleRate, channels, frameSize, 1, 0, 0, 1, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize)
	if _, err := dec.Decode(packet, out); err != nil {
		t.Fatalf("Decode good: %v", err)
	}
	goodState := dec.celtDecoder.PostfilterState()
	if got, want := goodState.Period, goodTrace.postfilter; got != want {
		t.Fatalf("good postfilterPeriod=%d want libopus %d", got, want)
	}
	if got, want := goodState.PreviousPeriod, goodTrace.postfilterOld; got != want {
		t.Fatalf("good postfilterPeriodOld=%d want libopus %d", got, want)
	}
	if got, want := goodState.Gain, goodTrace.postfilterGain; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("good postfilterGain=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := goodState.PreviousGain, goodTrace.postfilterGainOld; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("good postfilterGainOld=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := goodState.Tapset, goodTrace.postfilterTapset; got != want {
		t.Fatalf("good postfilterTapset=%d want libopus %d", got, want)
	}
	if got, want := goodState.PreviousTapset, goodTrace.postfilterTapsetOld; got != want {
		t.Fatalf("good postfilterTapsetOld=%d want libopus %d", got, want)
	}
	if _, err := dec.Decode(nil, out); err != nil {
		t.Fatalf("Decode loss: %v", err)
	}
	lossState := dec.celtDecoder.PostfilterState()
	if got, want := lossState.Period, lossTrace.postfilter; got != want {
		t.Fatalf("loss postfilterPeriod=%d want libopus %d", got, want)
	}
	if got, want := lossState.PreviousPeriod, lossTrace.postfilterOld; got != want {
		t.Fatalf("loss postfilterPeriodOld=%d want libopus %d", got, want)
	}
	if got, want := lossState.Gain, lossTrace.postfilterGain; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("loss postfilterGain=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := lossState.PreviousGain, lossTrace.postfilterGainOld; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("loss postfilterGainOld=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := lossState.Tapset, lossTrace.postfilterTapset; got != want {
		t.Fatalf("loss postfilterTapset=%d want libopus %d", got, want)
	}
	if got, want := lossState.PreviousTapset, lossTrace.postfilterTapsetOld; got != want {
		t.Fatalf("loss postfilterTapsetOld=%d want libopus %d", got, want)
	}
}

func TestLibopusCELTTracePLCStateMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 2)
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)
	steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 1, 1, 0, 1, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize*channels)
	if _, err := dec.Decode(packet, out); err != nil {
		t.Fatalf("Decode good: %v", err)
	}
	if _, err := dec.Decode(nil, out); err != nil {
		t.Fatalf("Decode loss: %v", err)
	}

	state := dec.celtDecoder.SnapshotPLCState()
	if state.LossDuration != trace.lossDuration {
		t.Fatalf("lossDuration=%d want libopus %d", state.LossDuration, trace.lossDuration)
	}
	if state.PLCDuration != trace.plcDuration {
		t.Fatalf("plcDuration=%d want libopus %d", state.PLCDuration, trace.plcDuration)
	}
	if state.LastFrameType != trace.lastFrameType {
		t.Fatalf("lastFrameType=%d want libopus %d", state.LastFrameType, trace.lastFrameType)
	}
	if state.LastPitchPeriod != trace.lastPitchPeriod {
		t.Fatalf("lastPitchPeriod=%d want libopus %d", state.LastPitchPeriod, trace.lastPitchPeriod)
	}
	if state.SkipPLC != trace.skipPLC {
		t.Fatalf("skipPLC=%v want libopus %v", state.SkipPLC, trace.skipPLC)
	}
	if state.PrefilterAndFold != trace.prefilterFold {
		t.Fatalf("prefilterAndFold=%v want libopus %v", state.PrefilterAndFold, trace.prefilterFold)
	}
}

func TestLibopusCELTTraceEnergyStateMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, 0, 1, []libopusAPIRateDecodeStep{{packet: packet}})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize)
	if _, err := dec.Decode(packet, out); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	gotEnergy := dec.celtDecoder.PrevEnergy()
	if len(trace.oldBandE) < len(gotEnergy) {
		t.Fatalf("trace oldBandE len=%d want at least %d", len(trace.oldBandE), len(gotEnergy))
	}
	for i, got := range gotEnergy {
		got32 := float32(got)
		want := trace.oldBandE[i]
		if math.Float32bits(got32) != math.Float32bits(want) {
			t.Fatalf("oldBandE[%d]=%08x %.10g want %08x %.10g",
				i,
				math.Float32bits(got32), got32,
				math.Float32bits(want), want)
		}
	}
}
