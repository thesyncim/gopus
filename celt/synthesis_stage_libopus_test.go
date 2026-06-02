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
// sines at 128 kbit/s. Its first decoded frame is the documented seed of the
// host-only float parity cluster: gopus vs libopus float output differs at
// ~510/1920 samples by ~1 ULP. The byte sequence is deterministic; it is pinned
// here so the celt-package oracle can replay the real failing payload without
// importing the root package.
const seedCELTStereoPacketHex = "fcb52acea9460bf0f037b801bba616f25e64ee93308b76ffafd560323e000da7fc11f90f02bbeb74b0d323bb3757a80b07ff6a3662530a2a7684031612213febb0f406cc33a605d2c3f771e110c36e4465d5b3450c5362c186b6fa9ca5361e7906af2e832d47e7b284654db214e11a63889b5930ce1561cae5bac9a04dec4158f6092fd4f42abd3b41f175937f3b7caab8c6a41eb8ae300ce0ce5c1a4f48742a424acc462db116a3b0d996bb727ebe70f572eb2b1853dc88d09a725a0c4e5a71f6d18e88e0336b4aa90398377ebb8000000000000000000000000000000000000000000001bb81415651f9678f5488f0053852650da0867f176a95a558f7ea62decbc67f1bea95a54b4ac562decbc59e87de2c53de9de4daa728c6d9636f1629ef4ef26d53946037628da0b8c17781bb146d05c60bbd65537be253da9aeb12b0da74aa8982d3a55450808631ae70ef8b4d12cd9235c3a36efc8721e9ea0367180473b5230efd4724043dc30a33816adb22d9f3b585e45f6b2011838efc60006400d03149fc0298238a76b558c4210e49afe5e366b5d6a9e2e10c"

var libopusCELTSynthesisTraceHelper libopustest.HelperCache

func buildLibopusCELTSynthesisTraceHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT synthesis stage trace",
		OutputBase:  "gopus_libopus_celt_synthesis_trace",
		SourceFile:  "libopus_celt_synthesis_trace.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

type libopusCELTSynthesisTrace struct {
	n        int
	channels int
	// freq[ch] and imdct[ch] each hold n samples scaled by 1/CELT_SIG_SCALE;
	// final holds n*channels interleaved post-deemphasis PCM. For the seed's
	// zero-gain frame comb_filter is an in-place no-op, so imdct[] is both the
	// post-IMDCT and post-comb_filter buffer.
	freq  [][]float32
	imdct [][]float32
	final []float32
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
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT synthesis stage trace", "GCSO")
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT synthesis stage trace", err)
	}
	n := int(reader.U32())
	cc := int(reader.U32())
	_ = reader.U32() // frame_size echo
	trace := &libopusCELTSynthesisTrace{n: n, channels: cc}
	trace.freq = make([][]float32, cc)
	trace.imdct = make([][]float32, cc)
	trace.final = make([]float32, n*cc)
	reader.ExpectRemaining((n*cc*2 + n*cc) * 4)
	for ch := 0; ch < cc; ch++ {
		trace.freq[ch] = make([]float32, n)
		for i := range trace.freq[ch] {
			trace.freq[ch][i] = reader.Float32()
		}
	}
	for ch := 0; ch < cc; ch++ {
		trace.imdct[ch] = make([]float32, n)
		for i := range trace.imdct[ch] {
			trace.imdct[ch][i] = reader.Float32()
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

// assertFloat32BitExact fails on the first sample whose IEEE-754 bits differ,
// reporting the index, both bit patterns, decimal values, and magnitude. The
// host-only parity drift this test localises is ~1 ULP, so the comparison must
// be strictly bit-exact rather than tolerance-based.
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
	requireBitExactFloat(t)

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
	for ch := 0; ch < channels; ch++ {
		assertFloat32BitExact(t, "spec/ch"+itoaCh(ch), stage.Spec(ch), trace.freq[ch])
	}

	// Stage 3: post-deemphasis interleaved PCM — the decoder's actual output.
	// Bit-exact vs libopus opus_decode_float() for the seed frame; this is the
	// stage that user-visible parity depends on.
	assertFloat32BitExact(t, "final", got, trace.final)

	// Stage 2: post-IMDCT / overlap-add raw CELT_SIG buffer, per channel,
	// captured from the comb_filter input before the (non-zero gain) postfilter
	// rewrites it in place. The seed frame is transient (8 short blocks); this is
	// the stage that diverged by ~1 ULP before the IMDCT pre-rotation and TDAC
	// windowing were aligned with the libopus clang -ffp-contract=on float path.
	for ch := 0; ch < channels; ch++ {
		assertFloat32BitExact(t, "imdct/ch"+itoaCh(ch), stage.IMDCT(ch), trace.imdct[ch])
	}
}

func itoaCh(ch int) string {
	if ch == 0 {
		return "0"
	}
	return "1"
}
