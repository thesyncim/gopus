//go:build gopus_qext && !gopus_fixed_point

package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var qextCELTSynthesisTraceHelper libopustest.HelperCache

type qextCELTSynthesisTrace struct {
	n        int
	channels int
	freq     [][]float32
	imdct    [][]float32
	postComb [][]float32
	final    []float32
}

func buildQEXTCELTSynthesisTraceHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "QEXT CELT synthesis stage trace",
		OutputBase:  "gopus_libopus_celt_qext_synthesis_trace",
		SourceFile:  "libopus_celt_synthesis_trace.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	})
}

func traceQEXTCELTSynthesisSequence(t *testing.T, packets [][]byte, targetStep, channels, frameSize int) qextCELTSynthesisTrace {
	t.Helper()
	bin, err := qextCELTSynthesisTraceHelper.Path(buildQEXTCELTSynthesisTraceHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT CELT synthesis trace", err)
	}
	payload := libopustest.NewOraclePayload("GCSI", 48000, uint32(channels), uint32(frameSize), uint32(targetStep), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0)
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "QEXT CELT synthesis trace", "GCSO")
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT CELT synthesis trace", err)
	}
	n := int(reader.U32())
	gotChannels := int(reader.U32())
	_ = reader.U32() // requested frame-size echo
	if n <= 0 || gotChannels != channels {
		t.Fatalf("selected C trace dimensions=%d/%d want positive/%d", n, gotChannels, channels)
	}
	trace := qextCELTSynthesisTrace{
		n:        n,
		channels: gotChannels,
		freq:     make([][]float32, gotChannels),
		imdct:    make([][]float32, gotChannels),
		postComb: make([][]float32, gotChannels),
		final:    make([]float32, n*gotChannels),
	}
	reader.ExpectRemaining(n * gotChannels * 4 * 4)
	for ch := range gotChannels {
		trace.freq[ch] = make([]float32, n)
		for i := range n {
			trace.freq[ch][i] = reader.Float32()
		}
	}
	for ch := range gotChannels {
		trace.imdct[ch] = make([]float32, n)
		for i := range n {
			trace.imdct[ch][i] = reader.Float32()
		}
	}
	for ch := range gotChannels {
		trace.postComb[ch] = make([]float32, n)
		for i := range n {
			trace.postComb[ch][i] = reader.Float32()
		}
	}
	for i := range trace.final {
		trace.final[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return trace
}

func traceQEXTCELTSynthesis(t *testing.T, packet []byte) qextCELTSynthesisTrace {
	t.Helper()
	return traceQEXTCELTSynthesisSequence(t, [][]byte{packet}, 0, 2, 960)
}

func firstQEXTFloat32Difference(got, want []float32) (int, uint32, uint32) {
	for i := range min(len(got), len(want)) {
		gotBits := math.Float32bits(got[i])
		wantBits := math.Float32bits(want[i])
		if gotBits != wantBits {
			return i, gotBits, wantBits
		}
	}
	if len(got) != len(want) {
		return min(len(got), len(want)), uint32(len(got)), uint32(len(want))
	}
	return -1, 0, 0
}

func TestQEXTSynthesisStagesMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	pcm := make([]float32, 960*2)
	for i := 0; i < 960; i++ {
		tm := float64(i) / 48000.0
		pcm[2*i] = float32(0.45 * math.Sin(2*math.Pi*440*tm))
		pcm[2*i+1] = float32(0.35 * math.Sin(2*math.Pi*660*tm+0.37))
	}
	packet := encodeLibopusQEXTPacketForMultistreamTest(t, opusDemo, 2, pcm)
	frame := parseQEXTStreamFrameForTest(t, "synthesis-stage", packet)
	stream := newStreamDecoder(48000, 2)
	stage := stream.celtDec.EnableSynthesisStageTrace()
	got, err := stream.decodeFramePayload(frame.rawFrame, 960, frame.toc, frame.qextPayload)
	if err != nil {
		t.Fatalf("Go decode: %v", err)
	}
	if !stage.Captured() || stage.Channels() != 2 || stage.N() != 960 {
		t.Fatalf("Go trace capture=%t channels=%d n=%d", stage.Captured(), stage.Channels(), stage.N())
	}
	want := traceQEXTCELTSynthesis(t, packet)
	for ch := range 2 {
		for _, pair := range []struct {
			name string
			got  []float32
			want []float32
		}{
			{"spectrum", stage.Spec(ch), want.freq[ch]},
			{"imdct", stage.IMDCT(ch), want.imdct[ch]},
			{"postcomb", stage.PostComb(ch), want.postComb[ch]},
		} {
			if i, gotBits, wantBits := firstQEXTFloat32Difference(pair.got, pair.want); i >= 0 {
				t.Errorf("channel %d %s first difference at %d: Go=%08x C=%08x", ch, pair.name, i, gotBits, wantBits)
			}
		}
	}
	if i, gotBits, wantBits := firstQEXTFloat32Difference(got, want.final); i >= 0 {
		t.Errorf("final PCM first difference at %d: Go=%08x C=%08x", i, gotBits, wantBits)
	}
	if t.Failed() {
		t.Fatal("QEXT synthesis trace diverges from the selected public C decoder")
	}
}
