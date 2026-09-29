package celt

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// seedCELTStereoPacket is the exact Opus packet produced by the root-package
// helper encodeAPIRateCELTPacket(t, 2) (decoder_api_rate_test.go): a single
// 20 ms fullband stereo CELT-only frame (TOC 0xfc) encoding 1200 Hz / 1900 Hz
// sines at 128 kbit/s. The pinned packet exercises exact synthesis-stage and
// public PCM comparisons without importing the root package.
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
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "CELT synthesis stage trace", "GCSO", 3)
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
