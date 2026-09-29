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
	n          int
	channels   int
	freq       [][]float32
	imdct      [][]float32
	postComb   [][]float32
	qextEnergy [][]float32
	qextNorm   [][]float32
	baseEnergy [][]float32
	baseNorm   [][]float32
	final      []float32
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
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "QEXT CELT synthesis trace", "GCSO", 4)
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
	energyCount := int(reader.U32())
	if energyCount < 0 || energyCount > 64 {
		t.Fatalf("invalid selected-C QEXT energy count %d", energyCount)
	}
	trace.qextEnergy = make([][]float32, gotChannels)
	for ch := range gotChannels {
		trace.qextEnergy[ch] = make([]float32, energyCount)
		for i := range energyCount {
			trace.qextEnergy[ch][i] = reader.Float32()
		}
	}
	qextNormCount := int(reader.U32())
	if qextNormCount < 0 || qextNormCount > n {
		t.Fatalf("invalid selected-C QEXT normalized coefficient count %d", qextNormCount)
	}
	trace.qextNorm = make([][]float32, gotChannels)
	for ch := range gotChannels {
		trace.qextNorm[ch] = make([]float32, qextNormCount)
		for i := range qextNormCount {
			trace.qextNorm[ch][i] = reader.Float32()
		}
	}
	baseEnergyCount := int(reader.U32())
	if baseEnergyCount < 0 || baseEnergyCount > 64 {
		t.Fatalf("invalid selected-C base energy count %d", baseEnergyCount)
	}
	trace.baseEnergy = make([][]float32, gotChannels)
	for ch := range gotChannels {
		trace.baseEnergy[ch] = make([]float32, baseEnergyCount)
		for i := range baseEnergyCount {
			trace.baseEnergy[ch][i] = reader.Float32()
		}
	}
	baseNormCount := int(reader.U32())
	if baseNormCount < 0 || baseNormCount > n {
		t.Fatalf("invalid selected-C base normalized coefficient count %d", baseNormCount)
	}
	trace.baseNorm = make([][]float32, gotChannels)
	for ch := range gotChannels {
		trace.baseNorm[ch] = make([]float32, baseNormCount)
		for i := range baseNormCount {
			trace.baseNorm[ch][i] = reader.Float32()
		}
	}
	skipCELTSynthesisAntiCollapseTrace(t, reader, n, gotChannels)
	skipCELTSynthesisCombTrace(t, reader, n, gotChannels)
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return trace
}

// skipCELTSynthesisCombTrace validates the bounded v4 comb-filter capture.
// QEXT scales COMBFILTER_MAXPERIOD by at most two; the trace carries only the
// standard 1026-sample history window, even when a QEXT period is longer.
func skipCELTSynthesisCombTrace(t *testing.T, reader *libopustest.OracleReader, n, channels int) {
	t.Helper()
	const maxPeriod = 2 * 1024 // QEXT_SCALE(COMBFILTER_MAXPERIOD), with COMBFILTER_MAXPERIOD=1024.
	countValue := reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C comb trace count: %v", err)
	}
	if countValue < uint32(channels) || countValue > uint32(channels*2) {
		t.Fatalf("selected C comb trace has %d calls for %d channels; want 1..2 per channel", countValue, channels)
	}
	perChannel := make([]int, channels)
	for i := range int(countValue) {
		callIndex := int(reader.U32())
		channel := int(reader.U32())
		callN := int(reader.U32())
		t0 := int(reader.U32())
		t1 := int(reader.U32())
		tapset0 := int(reader.U32())
		tapset1 := int(reader.U32())
		overlap := int(reader.U32())
		_ = reader.U32() // C arch selector
		historyCount := int(reader.U32())
		inputCount := int(reader.U32())
		windowCount := int(reader.U32())
		outputCount := int(reader.U32())
		_ = reader.Float32() // g0
		_ = reader.Float32() // g1
		if err := reader.Err(); err != nil {
			t.Fatalf("selected C comb trace call %d header: %v", i, err)
		}
		if channel < 0 || channel >= channels {
			t.Fatalf("selected C comb trace call %d channel=%d, want [0,%d)", i, channel, channels)
		}
		if callIndex != perChannel[channel] {
			t.Fatalf("selected C comb trace call %d channel=%d index=%d, want %d", i, channel, callIndex, perChannel[channel])
		}
		perChannel[channel]++
		if callN <= 0 || callN > n || t0 < 0 || t0 > maxPeriod || t1 < 0 || t1 > maxPeriod ||
			tapset0 < 0 || tapset0 >= 3 || tapset1 < 0 || tapset1 >= 3 || overlap < 0 || overlap > 240 ||
			historyCount != 1026 || inputCount != callN || windowCount != overlap || outputCount != callN {
			t.Fatalf("selected C comb trace call %d has invalid shape/params: N=%d T=%d/%d tap=%d/%d overlap=%d counts history/input/window/output=%d/%d/%d/%d",
				i, callN, t0, t1, tapset0, tapset1, overlap, historyCount, inputCount, windowCount, outputCount)
		}
		for range 6 + historyCount + inputCount + 2*windowCount + outputCount {
			_ = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			t.Fatalf("selected C comb trace call %d arrays: %v", i, err)
		}
	}
	for ch, calls := range perChannel {
		if calls < 1 || calls > 2 {
			t.Fatalf("selected C comb trace channel %d has %d calls, want 1..2", ch, calls)
		}
		if ch > 0 && calls != perChannel[0] {
			t.Fatalf("selected C comb trace channel %d has %d calls, channel 0 has %d", ch, calls, perChannel[0])
		}
	}
}

// skipCELTSynthesisAntiCollapseTrace validates and consumes the helper's
// anti-collapse section, which the QEXT stage comparisons do not use.
func skipCELTSynthesisAntiCollapseTrace(t *testing.T, reader *libopustest.OracleReader, n, channels int) {
	t.Helper()
	calls := reader.U32()
	seed := reader.U32()
	normCount := reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C anti-collapse trace header: %v", err)
	}
	if calls > 1 {
		t.Fatalf("selected C anti-collapse calls=%d exceeds one frame call", calls)
	}
	if calls == 1 && normCount != uint32(n) {
		t.Fatalf("selected C anti-collapse norm count=%d, want trace N=%d", normCount, n)
	}
	if calls == 0 && (normCount != 0 || seed != 0) {
		t.Fatalf("selected C absent anti-collapse trace has norms/seed=%d/%08x", normCount, seed)
	}
	for range 2 * channels * int(normCount) {
		_ = reader.Float32()
	}
	maskBands := reader.U32()
	maskCount := reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C anti-collapse norm trace: %v", err)
	}
	if calls == 0 && (maskBands != 0 || maskCount != 0) {
		t.Fatalf("selected C absent anti-collapse trace has bands/masks=%d/%d", maskBands, maskCount)
	}
	if calls == 1 && (maskBands == 0 || maskBands > 64 || maskCount != uint32(channels)*maskBands) {
		t.Fatalf("selected C anti-collapse mask bands/count=%d/%d, want 1..64 bands and channels*bands", maskBands, maskCount)
	}
	_ = reader.Bytes(int(maskCount))
	_ = reader.Bytes(int(maskCount))
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
	qextStart := 100 * (want.n / 120)
	for ch := range 2 {
		if i, gotBits, wantBits := firstQEXTFloat32Difference(stage.QEXTEnergy(ch), want.qextEnergy[ch]); i >= 0 {
			t.Errorf("channel %d QEXT energy first difference at %d: Go=%08x C=%08x", ch, i, gotBits, wantBits)
		}
		if i, gotBits, wantBits := firstQEXTFloat32Difference(stage.QEXTNorm(ch)[qextStart:], want.qextNorm[ch][qextStart:]); i >= 0 {
			t.Errorf("channel %d QEXT normalized coefficient first difference at %d: Go=%08x C=%08x", ch, i+qextStart, gotBits, wantBits)
		}
		if i, gotBits, wantBits := firstQEXTFloat32Difference(stage.BaseEnergy(ch), want.baseEnergy[ch]); i >= 0 {
			t.Errorf("channel %d base energy first difference at %d: Go=%08x C=%08x", ch, i, gotBits, wantBits)
		}
		if i, gotBits, wantBits := firstQEXTFloat32Difference(stage.BaseNorm(ch)[:qextStart], want.baseNorm[ch][:qextStart]); i >= 0 {
			t.Errorf("channel %d base normalized coefficient first difference at %d: Go=%08x C=%08x", ch, i, gotBits, wantBits)
		}
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
