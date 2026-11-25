package celt

import (
	"encoding/hex"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"testing"
)

func equalFloat32Bits(a, b []float32) int {
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return i
		}
	}
	return -1
}

func randSpectrum(rng *rand.Rand, n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(6))))
	}
	return x
}

// TestIMDCTPreRotateFFTStridedMatchesGather checks the strided pre-rotation
// and FFT of an interleaved short block against gathering the block into a
// contiguous spectrum first.
func TestIMDCTPreRotateFFTStridedMatchesGather(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5b10))
	for _, n := range []int{240, 480} {
		n2, n4 := n/2, n/4
		trig := getMDCTTrigF32(n)
		for _, st := range []*kissFFTState{getKissFFTState(n4), nil} {
			for _, stride := range []int{1, 2, 4, 8} {
				coeffs := randSpectrum(rng, n2*stride)
				for first := range stride {
					got := imdctPreRotateFFTStrided(make([]float32, 2*n4), make([]float32, 2*n4), coeffs, first, stride, trig, n2, n4, st, make([]float32, n2))
					block := make([]float32, n2)
					for i := range block {
						block[i] = coeffs[first+i*stride]
					}
					want := imdctPreRotateFFT(make([]float32, 2*n4), make([]float32, 2*n4), block, trig, n2, n4, getKissFFTState(n4))
					for i := range want {
						if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
							t.Fatalf("n=%d stride=%d first=%d st=%v: out[%d]=%v want %v", n, stride, first, st != nil, i, got[i], want[i])
						}
					}
				}
			}
		}
	}
}

// synthesizeTransientReference is the short-block synthesis written the way
// celt_synthesis reads it block by block: the previous overlap, a cleared
// buffer, each block gathered and passed through imdctInPlaceScratchF32Spectrum.
func synthesizeTransientReference(coeffs []float32, prevOverlap []celtSig, overlap, shortBlocks int) []float32 {
	frameSize := len(coeffs)
	out := make([]float32, frameSize+overlap)
	for i := range overlap {
		out[i] = float32(prevOverlap[i])
	}
	shortSize := frameSize / shortBlocks
	block := make([]float32, shortSize)
	var scratch imdctScratchF32
	for b := range shortBlocks {
		for i := range block {
			block[i] = coeffs[b+i*shortBlocks]
		}
		imdctInPlaceScratchF32Spectrum(block, out, b*shortSize, overlap, &scratch)
	}
	return out
}

// TestSynthesizeChannelDirectMatchesReference checks the transient short-block
// synthesis that writes each IMDCT straight into out, and the long-block
// synthesis into out, against the gather-and-copy reference paths.
func TestSynthesizeChannelDirectMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x7e2d))
	const overlap = 120
	for _, frameSize := range []int{120, 240, 480, 960} {
		for iter := range 8 {
			coeffs := randSpectrum(rng, frameSize)
			prev := make([]celtSig, overlap)
			for i := range prev {
				prev[i] = celtSig(rng.NormFloat64() * 1e3)
			}
			shortCoeffs := make([]float32, frameSize)
			var scratch imdctScratchF32
			// Garbage in out must not reach the result.
			out := make([]float32, frameSize+overlap)
			for i := range out {
				out[i] = float32(rng.NormFloat64())
			}
			for _, shortBlocks := range []int{2, 4, 8} {
				if frameSize%(shortBlocks*120) != 0 {
					continue
				}
				got := synthesizeChannelWithOverlapScratchF32(coeffs, prev, overlap, true, shortBlocks, out, &scratch, shortCoeffs)
				want := synthesizeTransientReference(coeffs, prev, overlap, shortBlocks)
				if i := equalFloat32Bits(got, want); i >= 0 {
					t.Fatalf("frame=%d B=%d iter %d: out[%d]=%v want %v", frameSize, shortBlocks, iter, i, got[i], want[i])
				}
			}
			got := synthesizeChannelWithOverlapScratchF32(coeffs, prev, overlap, false, 1, out, &scratch, shortCoeffs)
			want := imdctOverlapWithPrevScratchF32Output32(coeffs, prev, overlap, nil)
			if i := equalFloat32Bits(got, want); i >= 0 {
				t.Fatalf("frame=%d long iter %d: out[%d]=%v want %v", frameSize, iter, i, got[i], want[i])
			}
		}
	}
}

// TestDecodeBandTablesMatchInitCaps checks the precomputed per-LM caps,
// dynalloc quanta, allocation thresholds and scaled allocation vectors
// against init_caps and the celt_decode_with_ec and clt_compute_allocation
// formulas.
func TestDecodeBandTablesMatchInitCaps(t *testing.T) {
	for lm := range 4 {
		for c := range 2 {
			channels := c + 1
			var caps [MaxBands]int32
			initCapsInto(caps[:], MaxBands, lm, channels)
			tab := &decodeBandTables[lm][c]
			for i := range MaxBands {
				width := channels * (EBands[i+1] - EBands[i]) << uint(lm)
				quanta := int32(min(width<<bitRes, max(6<<bitRes, width)))
				if tab.caps[i] != caps[i] || tab.quanta[i] != quanta {
					t.Fatalf("lm=%d C=%d band %d: caps %d quanta %d, want %d %d", lm, channels, i, tab.caps[i], tab.quanta[i], caps[i], quanta)
				}
				n := EBands[i+1] - EBands[i]
				if thresh := int32(max(channels<<bitRes, (3*n<<uint(lm)<<bitRes)>>4)); tab.thresh[i] != thresh {
					t.Fatalf("lm=%d C=%d band %d: thresh %d, want %d", lm, channels, i, tab.thresh[i], thresh)
				}
				for q := range BandAlloc {
					if alloc := int32(channels * n * BandAlloc[q][i] << uint(lm) >> 2); tab.alloc[q][i] != alloc {
						t.Fatalf("lm=%d C=%d vector %d band %d: alloc %d, want %d", lm, channels, q, i, tab.alloc[q][i], alloc)
					}
				}
			}
		}
	}
}

const seedCELTStereoPacketHex = "fcb52acea9460bf0f037b801bba616f25e64ee93308b76ffafd560323e000da7fc11f90f02bbeb74b0d323bb3757a80b07ff6a3662530a2a7684031612213febb0f406cc33a605d2c3f771e110c36e4465d5b3450c5362c186b6fa9ca5361e7906af2e832d47e7b284654db214e11a63889b5930ce1561cae5bac9a04dec4158f6092fd4f42abd3b41f175937f3b7caab8c6a41eb8ae300ce0ce5c1a4f48742a424acc462db116a3b0d996bb727ebe70f572eb2b1853dc88d09a725a0c4e5a71f6d18e88e0336b4aa90398377ebb8000000000000000000000000000000000000000000001bb81415651f9678f5488f0053852650da0867f176a95a558f7ea62decbc67f1bea95a54b4ac562decbc59e87de2c53de9de4daa728c6d9636f1629ef4ef26d53946037628da0b8c17781bb146d05c60bbd65537be253da9aeb12b0da74aa8982d3a55450808631ae70ef8b4d12cd9235c3a36efc8721e9ea0367180473b5230efd4724043dc30a33816adb22d9f3b585e45f6b2011838efc60006400d03149fc0298238a76b558c4210e49afe5e366b5d6a9e2e10c"

var libopusCELTSynthesisTraceHelper libopustest.HelperCache

func buildLibopusCELTSynthesisTraceHelper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "CELT synthesis stage trace",
		OutputBase:  "gopus_libopus_celt_synthesis_trace",
		SourceFile:  "libopus_celt_synthesis_trace.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return libopustest.BuildCHelper(cfg)
}

type libopusCELTSynthesisTrace struct {
	n        int
	channels int
	// freq[ch] and imdct[ch] each hold n samples scaled by 1/CELT_SIG_SCALE;
	// final holds n*channels interleaved post-deemphasis PCM.
	freq                  [][]float32
	imdct                 [][]float32
	postComb              [][]float32
	final                 []float32
	qextEnergy            [][]float32
	qextNorm              [][]float32
	baseEnergy            [][]float32
	baseNorm              [][]float32
	antiCollapseCalls     int
	antiCollapseSeed      uint32
	antiCollapseNormPre   [][]float32
	antiCollapseNormPost  [][]float32
	antiCollapseMaskBands int
	antiCollapseMaskPre   []byte
	antiCollapseMaskPost  []byte
	combCalls             []libopusCELTCombFilterTraceCall
}

type libopusCELTCombFilterTraceCall struct {
	callIndex, channel int
	n, t0, t1          int
	tapset0, tapset1   int
	overlap, arch      int
	g0, g1             float32
	tapCoefficients    [6]float32
	history            []float32
	input              []float32
	window             []float32
	windowSq           []float32
	output             []float32
}

func consumeCELTSynthesisTraceV3Tail(t *testing.T, reader *libopustest.OracleReader, trace *libopusCELTSynthesisTrace) {
	t.Helper()
	for _, section := range []struct {
		name string
		max  uint32
		dst  *[][]float32
	}{
		{name: "QEXT energy", max: 64, dst: &trace.qextEnergy},
		{name: "QEXT normalized coefficients", max: 2048, dst: &trace.qextNorm},
		{name: "base energy", max: 64, dst: &trace.baseEnergy},
		{name: "base normalized coefficients", max: 2048, dst: &trace.baseNorm},
	} {
		count := reader.U32()
		if err := reader.Err(); err != nil {
			t.Fatal(err)
		}
		if count > section.max {
			t.Fatalf("selected C %s count=%d exceeds %d", section.name, count, section.max)
		}
		*section.dst = make([][]float32, trace.channels)
		for ch := range trace.channels {
			values := make([]float32, int(count))
			for i := range values {
				values[i] = reader.Float32()
			}
			(*section.dst)[ch] = values
		}
		if err := reader.Err(); err != nil {
			t.Fatalf("selected C %s: %v", section.name, err)
		}
	}
	trace.antiCollapseCalls = int(reader.U32())
	trace.antiCollapseSeed = reader.U32()
	normCount := reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C anti-collapse trace header: %v", err)
	}
	if trace.antiCollapseCalls > 1 {
		t.Fatalf("selected C anti-collapse calls=%d exceeds one frame call", trace.antiCollapseCalls)
	}
	if normCount > 2048 {
		t.Fatalf("selected C anti-collapse norm count=%d exceeds 2048", normCount)
	}
	trace.antiCollapseNormPre = make([][]float32, trace.channels)
	for ch := range trace.channels {
		values := make([]float32, int(normCount))
		for i := range values {
			values[i] = reader.Float32()
		}
		trace.antiCollapseNormPre[ch] = values
	}
	trace.antiCollapseNormPost = make([][]float32, trace.channels)
	for ch := range trace.channels {
		values := make([]float32, int(normCount))
		for i := range values {
			values[i] = reader.Float32()
		}
		trace.antiCollapseNormPost[ch] = values
	}
	trace.antiCollapseMaskBands = int(reader.U32())
	maskCount := reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C anti-collapse norm trace: %v", err)
	}
	if maskCount > 128 {
		t.Fatalf("selected C anti-collapse mask count=%d exceeds 128", maskCount)
	}
	if trace.antiCollapseCalls == 0 {
		if normCount != 0 || trace.antiCollapseMaskBands != 0 || maskCount != 0 || trace.antiCollapseSeed != 0 {
			t.Fatalf("selected C absent anti-collapse trace has calls/norms/bands/masks/seed=%d/%d/%d/%d/%08x",
				trace.antiCollapseCalls, normCount, trace.antiCollapseMaskBands, maskCount, trace.antiCollapseSeed)
		}
	} else {
		if normCount != uint32(trace.n) {
			t.Fatalf("selected C anti-collapse norm count=%d, want trace N=%d", normCount, trace.n)
		}
		if trace.antiCollapseMaskBands <= 0 || trace.antiCollapseMaskBands > 64 || maskCount != uint32(trace.channels*trace.antiCollapseMaskBands) {
			t.Fatalf("selected C anti-collapse mask bands/count=%d/%d, want 1..64 bands and channels*bands=%d",
				trace.antiCollapseMaskBands, maskCount, trace.channels*trace.antiCollapseMaskBands)
		}
	}
	trace.antiCollapseMaskPre = append([]byte(nil), reader.Bytes(int(maskCount))...)
	trace.antiCollapseMaskPost = append([]byte(nil), reader.Bytes(int(maskCount))...)
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C anti-collapse masks: %v", err)
	}
}

func consumeCELTSynthesisTraceCombV4(t *testing.T, reader *libopustest.OracleReader, trace *libopusCELTSynthesisTrace) {
	t.Helper()
	countValue := reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C comb trace count: %v", err)
	}
	if countValue < uint32(trace.channels) || countValue > uint32(trace.channels*2) {
		t.Fatalf("selected C comb trace has %d calls for %d channels; want 1..2 per channel", countValue, trace.channels)
	}
	count := int(countValue)
	trace.combCalls = make([]libopusCELTCombFilterTraceCall, count)
	perChannel := [2]int{}
	maxPeriod := combFilterMaxPeriod
	if extsupport.QEXT {
		maxPeriod *= 2 // libopus QEXT_SCALE(COMBFILTER_MAXPERIOD).
	}
	for i := range count {
		call := &trace.combCalls[i]
		call.callIndex = int(reader.U32())
		call.channel = int(reader.U32())
		call.n = int(reader.U32())
		call.t0 = int(reader.U32())
		call.t1 = int(reader.U32())
		call.tapset0 = int(reader.U32())
		call.tapset1 = int(reader.U32())
		call.overlap = int(reader.U32())
		call.arch = int(reader.U32())
		historyCount := int(reader.U32())
		inputCount := int(reader.U32())
		windowCount := int(reader.U32())
		outputCount := int(reader.U32())
		call.g0 = reader.Float32()
		call.g1 = reader.Float32()
		if err := reader.Err(); err != nil {
			t.Fatalf("selected C comb trace record %d header: %v", i, err)
		}
		if call.channel < 0 || call.channel >= trace.channels {
			t.Fatalf("selected C comb trace call %d channel=%d, want [0,%d)", i, call.channel, trace.channels)
		}
		if call.callIndex != perChannel[call.channel] {
			t.Fatalf("selected C comb trace call %d channel=%d index=%d, want %d", i, call.channel, call.callIndex, perChannel[call.channel])
		}
		perChannel[call.channel]++
		if call.n <= 0 || call.n > trace.n || call.t0 < 0 || call.t0 > maxPeriod ||
			call.t1 < 0 || call.t1 > maxPeriod ||
			call.tapset0 < 0 || call.tapset0 >= len(combFilterGains) ||
			call.tapset1 < 0 || call.tapset1 >= len(combFilterGains) ||
			call.overlap < 0 || call.overlap > 240 || historyCount != combFilterHistory ||
			inputCount != call.n || windowCount != call.overlap || outputCount != call.n {
			t.Fatalf("selected C comb trace call %d has invalid shape/params: N=%d T=%d/%d tap=%d/%d overlap=%d counts history/input/window/output=%d/%d/%d/%d",
				i, call.n, call.t0, call.t1, call.tapset0, call.tapset1, call.overlap,
				historyCount, inputCount, windowCount, outputCount)
		}
		for tap := range call.tapCoefficients {
			call.tapCoefficients[tap] = reader.Float32()
		}
		call.history = make([]float32, historyCount)
		for j := range call.history {
			call.history[j] = reader.Float32()
		}
		call.input = make([]float32, inputCount)
		for j := range call.input {
			call.input[j] = reader.Float32()
		}
		call.window = make([]float32, windowCount)
		for j := range call.window {
			call.window[j] = reader.Float32()
		}
		call.windowSq = make([]float32, windowCount)
		for j := range call.windowSq {
			call.windowSq[j] = reader.Float32()
		}
		call.output = make([]float32, outputCount)
		for j := range call.output {
			call.output[j] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			t.Fatalf("selected C comb trace call %d arrays: %v", i, err)
		}
	}
	for ch := range trace.channels {
		if perChannel[ch] < 1 || perChannel[ch] > 2 {
			t.Fatalf("selected C comb trace channel %d has %d calls, want 1..2", ch, perChannel[ch])
		}
		if ch > 0 && perChannel[ch] != perChannel[0] {
			t.Fatalf("selected C comb trace channel %d has %d calls, channel 0 has %d", ch, perChannel[ch], perChannel[0])
		}
	}
}

func traceLibopusCELTSynthesis(t *testing.T, sampleRate, channels, frameSize, targetStep int, packets [][]byte) *libopusCELTSynthesisTrace {
	t.Helper()
	binPath, err := libopusCELTSynthesisTraceHelper.Path(buildLibopusCELTSynthesisTraceHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT synthesis stage trace", err)
	}
	payload := libopustest.NewOraclePayload("GCSI",
		uint32(sampleRate), uint32(channels), uint32(frameSize),
		uint32(targetStep), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec = 0
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "CELT synthesis stage trace", "GCSO", 4)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT synthesis stage trace", err)
	}
	n := int(reader.U32())
	cc := int(reader.U32())
	frameEcho := int(reader.U32())
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C CELT synthesis trace header: %v", err)
	}
	if n <= 0 || n > 2048 || cc < 1 || cc > 2 || frameEcho != frameSize {
		t.Fatalf("selected C CELT synthesis trace dimensions N/channels/frame=%d/%d/%d, want N 1..2048, channels 1..2, frame=%d",
			n, cc, frameEcho, frameSize)
	}
	trace := &libopusCELTSynthesisTrace{n: n, channels: cc}
	trace.freq = make([][]float32, cc)
	trace.imdct = make([][]float32, cc)
	trace.postComb = make([][]float32, cc)
	trace.final = make([]float32, n*cc)
	for ch := range cc {
		trace.freq[ch] = make([]float32, n)
		for i := range trace.freq[ch] {
			trace.freq[ch][i] = reader.Float32()
		}
	}
	for ch := range cc {
		trace.imdct[ch] = make([]float32, n)
		for i := range trace.imdct[ch] {
			trace.imdct[ch][i] = reader.Float32()
		}
	}
	for ch := range cc {
		trace.postComb[ch] = make([]float32, n)
		for i := range trace.postComb[ch] {
			trace.postComb[ch][i] = reader.Float32()
		}
	}
	for i := range trace.final {
		trace.final[i] = reader.Float32()
	}
	consumeCELTSynthesisTraceV3Tail(t, reader, trace)
	consumeCELTSynthesisTraceCombV4(t, reader, trace)
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return trace
}

// assertFloat32BitExact fails on the first sample whose IEEE-754 bits differ,
// reporting the index, both bit patterns, decimal values, and magnitude.
func assertFloat32BitExact(t *testing.T, label string, got, want []float32) (firstDiff int, ok bool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Errorf("%s[%d]: gopus=%08x %.9g libopus=%08x %.9g |delta|=%g",
				label, i,
				math.Float32bits(got[i]), got[i],
				math.Float32bits(want[i]), want[i],
				math.Abs(float64(got[i]-want[i])))
			return i, false
		}
	}
	return -1, true
}

// TestCELTSynthesisStagesMatchLibopusC decodes the documented seed payload and
// compares each CELT synthesis stage (post-denormalise spectrum, post-IMDCT
// time buffer, post-deemphasis PCM) against the libopus C reference bit-exactly,
// pinpointing the first stage that diverges on darwin/arm64.
func TestCELTSynthesisStagesMatchLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)
	packet, err := hex.DecodeString(seedCELTStereoPacketHex)
	if err != nil {
		t.Fatalf("decode seed packet hex: %v", err)
	}
	if len(packet) < 2 || packet[0] != 0xfc {
		t.Fatalf("unexpected seed packet TOC=%#x len=%d", packet[0], len(packet))
	}
	celtPayload := packet[1:]

	trace := traceLibopusCELTSynthesis(t, sampleRate, channels, frameSize, 0, [][]byte{packet})
	if trace.n != frameSize || trace.channels != channels {
		t.Fatalf("trace n=%d channels=%d want %d/%d", trace.n, trace.channels, frameSize, channels)
	}

	dec := NewDecoder(channels)
	if err := dec.SetAPISampleRate(sampleRate); err != nil {
		t.Fatalf("SetAPISampleRate: %v", err)
	}
	dec.SetBandwidth(CELTFullband)
	stage := dec.EnableSynthesisStageTrace()

	got := make([]float32, frameSize*channels)
	if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(celtPayload, frameSize, true, got); err != nil {
		t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate: %v", err)
	}
	if !stage.Captured() {
		t.Fatal("gopus synthesis-stage trace did not capture (decode path mismatch)")
	}
	if stage.Channels() != channels || stage.N() != frameSize {
		t.Fatalf("gopus trace channels=%d n=%d want %d/%d", stage.Channels(), stage.N(), channels, frameSize)
	}

	// Stage 1: post-denormalise spectrum (frequency-domain CELT_SIG buffer),
	// per channel. Bit-exact vs libopus denormalise_bands().
	for ch := range channels {
		assertFloat32BitExact(t, "spec/ch"+itoaCh(ch), stage.Spec(ch), trace.freq[ch])
	}

	// Stage 3: post-deemphasis interleaved PCM — the decoder's actual output.
	// Bit-exact vs libopus opus_decode_float() for the seed frame; this is the
	// stage that user-visible parity depends on.
	assertFloat32BitExact(t, "final", got, trace.final)

	// Stage 2: post-IMDCT / overlap-add raw CELT_SIG buffer, per channel,
	// captured from the comb_filter input before the (non-zero gain) postfilter
	// rewrites it in place. The seed frame is transient (8 short blocks) and checks
	// the IMDCT pre-rotation and TDAC windowing against the selected C float path.
	for ch := range channels {
		assertFloat32BitExact(t, "imdct/ch"+itoaCh(ch), stage.IMDCT(ch), trace.imdct[ch])
	}
}

// seedCELTNBStereoPostfilterPacketHex is a 20 ms narrowband stereo CELT-only
// packet (TOC 0x9c) whose postfilter splits comb_filter_const's delay line
// between the carried history and the current frame. libopus x86 SIMD runs the
// whole constant-gain range through comb_filter_const_sse, so the SSE
// accumulation order must hold across that split up to the frame's last sample.
const seedCELTNBStereoPostfilterPacketHex = "9ccaa168c720b2eda2f9d0102f05f084636cb0cdd55a7a3944133fe8e689bcaf4cd139a6317387d459da697b4af86fd2f0793d992f64b5bf348697259b2ec8002ce58920b3ec80e49dfdff08751f063aad142f56b1bb10acac28554945aad9a67cf505e08126cfdfadc7a3e79466f22a7885715da4074d5f434fb211e0dc5e4c78cd1d09522423840e877bf30e51b5c53502d10dd98959e4654ff6c4fa413fbc049ab805851c17a38ade008fe1a4f48c5aee9bec9fdb45cdf25f550f9788c779093cbb7c2e8c948092ee58376cbba662eae84ad8e85b1ef5e7ee081e7fecb138707700108e10192487c35c7a093fb6d1a6a1f6080a86ec85ba75fefdae0f8fceb24b53caeac7c6468f58fc398c2cb969fe76873f2de67263274c72b8550e14d620f8c64c4e0adbd1067e"

// TestCELTPostfilterStagesMatchLibopusC compares the spectrum, IMDCT,
// postfilter and de-emphasis stages of seedCELTNBStereoPostfilterPacketHex
// against libopus bit-exactly.
func TestCELTPostfilterStagesMatchLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		channels  = 2
		frameSize = 960
	)
	packet, err := hex.DecodeString(seedCELTNBStereoPostfilterPacketHex)
	if err != nil {
		t.Fatalf("decode seed packet hex: %v", err)
	}
	trace := traceLibopusCELTSynthesis(t, 48000, channels, frameSize, 0, [][]byte{packet})
	dec := NewDecoder(channels)
	if err := dec.SetAPISampleRate(48000); err != nil {
		t.Fatalf("SetAPISampleRate: %v", err)
	}
	dec.SetBandwidth(CELTNarrowband)
	stage := dec.EnableSynthesisStageTrace()
	got := make([]float32, frameSize*channels)
	if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(packet[1:], frameSize, true, got); err != nil {
		t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate: %v", err)
	}
	if !stage.Captured() {
		t.Fatal("gopus synthesis-stage trace did not capture (decode path mismatch)")
	}
	for ch := range channels {
		assertFloat32BitExact(t, "spec/ch"+itoaCh(ch), stage.Spec(ch), trace.freq[ch])
		assertFloat32BitExact(t, "imdct/ch"+itoaCh(ch), stage.IMDCT(ch), trace.imdct[ch])
		assertFloat32BitExact(t, "postcomb/ch"+itoaCh(ch), stage.PostComb(ch), trace.postComb[ch])
	}
	assertFloat32BitExact(t, "final", got, trace.final)
}

func itoaCh(ch int) string {
	if ch == 0 {
		return "0"
	}
	return "1"
}

const seedCELTMonoSWBPacketHex = "d89f87b2aece4cbf34b16796cca502229c79dd139415a20a9761b8922f10bb6ed2d54bb8fb19e5cc4370e72d69fb76392d1f3a348c44ba3627aed687623b788c55bd5b89762cac9a35c1058bf3cb862233d52024aeb24a0d257d67db41f38fca20d34703ead30328b795f6b10a2afe1cc5a74ea492035aab3fc97f22702c139ebb5bc7b16527f7d26b5b97c20f3cf47e54598b7a85a57b60bb22536c1048da80b57c079b2857f9f6c6252fe2f73c32cb490cbca6a70426ef591b3aeebf30caa03d44da9f0c8fed90be55f9f2e9ec05115184d277896b0b66cbf7c56ba380beceab585d24b16d0b114d777a9c57174797e2b2c9ef814868a000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001d4cca76c6159af548942f8aff3064491596092403b7b88f1fc5c874b808c16c6a6e837a00baf41dbf867dd166432be801cca100a00dc9010775c32b1e8eae2ebb2a7caefcbb023c494fbf5c3044766370a3996d666d8c707a0580db"

// TestCELTSynthesisStagesSWBMatchLibopusC decodes a superwideband mono CELT
// frame and compares each synthesis stage (post-denormalise spectrum,
// post-IMDCT time buffer, post-deemphasis PCM) against libopus bit-exactly,
// localising the SWB-specific decode drift that seeds the DRED PLC history.
func TestCELTSynthesisStagesSWBMatchLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	packet, err := hex.DecodeString(seedCELTMonoSWBPacketHex)
	if err != nil {
		t.Fatalf("decode seed packet hex: %v", err)
	}
	if len(packet) < 2 || packet[0] != 0xd8 {
		t.Fatalf("unexpected seed packet TOC=%#x len=%d", packet[0], len(packet))
	}
	celtPayload := packet[1:]

	trace := traceLibopusCELTSynthesis(t, sampleRate, channels, frameSize, 0, [][]byte{packet})
	if trace.n != frameSize || trace.channels != channels {
		t.Fatalf("trace n=%d channels=%d want %d/%d", trace.n, trace.channels, frameSize, channels)
	}

	dec := NewDecoder(channels)
	if err := dec.SetAPISampleRate(sampleRate); err != nil {
		t.Fatalf("SetAPISampleRate: %v", err)
	}
	dec.SetBandwidth(CELTSuperwideband)
	stage := dec.EnableSynthesisStageTrace()

	got := make([]float32, frameSize*channels)
	if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(celtPayload, frameSize, false, got); err != nil {
		t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate: %v", err)
	}
	if !stage.Captured() {
		t.Fatal("gopus synthesis-stage trace did not capture (decode path mismatch)")
	}
	if stage.Channels() != channels || stage.N() != frameSize {
		t.Fatalf("gopus trace channels=%d n=%d want %d/%d", stage.Channels(), stage.N(), channels, frameSize)
	}

	for ch := range channels {
		assertFloat32BitExact(t, "spec/ch"+itoaCh(ch), stage.Spec(ch), trace.freq[ch])
	}
	assertFloat32BitExact(t, "final", got, trace.final)
	for ch := range channels {
		assertFloat32BitExact(t, "imdct/ch"+itoaCh(ch), stage.IMDCT(ch), trace.imdct[ch])
	}
}

func TestSynthesize_SampleCount(t *testing.T) {
	testCases := []int{120, 240, 480, 960}

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			d := NewDecoder(1)

			// Create frameSize coefficients (as DecodeBands returns)
			coeffs := make([]float32, frameSize)

			// IMDCT produces 2*frameSize samples
			// After 14-02 fix, OverlapAdd produces frameSize samples
			samples := d.synthesizeTest(coeffs, false, 1)

			// Output should be exactly frameSize samples
			if len(samples) != frameSize {
				t.Errorf("First frame: got %d samples, want %d", len(samples), frameSize)
			}

			// Second frame should also produce frameSize samples
			samples2 := d.synthesizeTest(coeffs, false, 1)
			if len(samples2) != frameSize {
				t.Errorf("Second frame: got %d samples, want %d", len(samples2), frameSize)
			}

			t.Logf("frameSize=%d: IMDCT produces %d samples, overlap-add yields %d samples",
				frameSize, 2*frameSize, frameSize)
		})
	}
}

// TestSynthesizeStereo_SampleCount verifies stereo synthesis produces correct sample counts.
// After 14-02 fix, stereo synthesis produces 2*frameSize interleaved samples.
func TestSynthesizeStereo_SampleCount(t *testing.T) {
	testCases := []int{120, 240, 480, 960}

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			d := NewDecoder(2) // Stereo

			// Create frameSize coefficients per channel
			coeffsL := make([]float32, frameSize)
			coeffsR := make([]float32, frameSize)

			// Call SynthesizeStereo
			samples := d.synthesizeStereoTest(coeffsL, coeffsR, false, 1)

			// Stereo output is interleaved [L0, R0, L1, R1, ...]
			// Per channel: frameSize samples (after 14-02 fix)
			// Interleaved: 2 * frameSize samples
			expectedStereo := frameSize * 2

			if len(samples) != expectedStereo {
				t.Errorf("Stereo synthesis: got %d samples, want %d", len(samples), expectedStereo)
			}
		})
	}
}

// TestOverlapAdd_Properties verifies overlap-add produces correct output length.
// After the 14-02 fix, OverlapAdd produces frameSize = inputLen/2 samples.
func TestOverlapAdd_Properties(t *testing.T) {
	testCases := []struct {
		name        string
		inputLen    int // IMDCT output: 2*frameSize
		overlap     int
		expectedOut int // frameSize = inputLen/2
	}{
		{"standard 960", 1920, 120, 960}, // 20ms: 1920/2 = 960
		{"standard 480", 960, 120, 480},  // 10ms: 960/2 = 480
		{"standard 240", 480, 120, 240},  // 5ms: 480/2 = 240
		{"standard 120", 240, 120, 120},  // 2.5ms: 240/2 = 120
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			current := make([]float32, tc.inputLen)
			prevOverlap := make([]float32, tc.overlap)

			output, newOverlap := OverlapAdd(current, prevOverlap, tc.overlap)

			if len(output) != tc.expectedOut {
				t.Errorf("OverlapAdd output len = %d, want %d", len(output), tc.expectedOut)
			}

			if len(newOverlap) != tc.overlap {
				t.Errorf("OverlapAdd newOverlap len = %d, want %d", len(newOverlap), tc.overlap)
			}
		})
	}
}

// TestOverlapAdd_OutputSize verifies OverlapAdd produces frameSize samples for all frame sizes.
// This is the key test for the 14-02 fix: IMDCT of N coefficients produces 2N samples,
// and OverlapAdd should produce exactly N output samples (frameSize).
func TestOverlapAdd_OutputSize(t *testing.T) {
	testCases := []int{120, 240, 480, 960}
	overlap := 120

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			imdctOut := make([]float32, 2*frameSize)
			prevOverlap := make([]float32, overlap)

			output, newOverlap := OverlapAdd(imdctOut, prevOverlap, overlap)

			if len(output) != frameSize {
				t.Errorf("frameSize %d: got output %d, want %d", frameSize, len(output), frameSize)
			}
			if len(newOverlap) != overlap {
				t.Errorf("frameSize %d: got newOverlap %d, want %d", frameSize, len(newOverlap), overlap)
			}
		})
	}
}

// TestIMDCT_OutputSize verifies IMDCT produces 2*N samples from N coefficients.
func TestIMDCT_OutputSize(t *testing.T) {
	testCases := []int{120, 240, 480, 960}

	for _, n := range testCases {
		t.Run(fmt.Sprintf("%d", n), func(t *testing.T) {
			coeffs := make([]float32, n)

			output := IMDCT(coeffs)

			expectedLen := 2 * n
			if len(output) != expectedLen {
				t.Errorf("IMDCT(%d coeffs) produced %d samples, want %d", n, len(output), expectedLen)
			}
		})
	}
}
