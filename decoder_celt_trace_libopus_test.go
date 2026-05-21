package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusCELTTraceHelper libopustest.HelperCache

type libopusCELTTrace struct {
	decodedSamples  int
	internalSamples int
	downsample      int
	overlap         int
	decodeBuffer    int
	channel         int
	start           int
	final           []float32
	preDeemphasis   []float32
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
	trace.final = make([]float32, outCount)
	trace.preDeemphasis = make([]float32, outCount)
	reader.ExpectRemaining(outCount * 2 * 4)
	for i := range trace.final {
		trace.final[i] = reader.Float32()
	}
	for i := range trace.preDeemphasis {
		trace.preDeemphasis[i] = reader.Float32()
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
