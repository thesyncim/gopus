package celt

import (
	"encoding/hex"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// seedCELTMonoSWBPacketHex is a clean single-frame 20 ms superwideband mono
// CELT-only Opus packet (TOC 0xd8, config 27). It was produced by the root
// helper makeValidMonoCELTPacketForFrameSizeBandwidthForDREDTest(t, 960,
// BandwidthSuperwideband) (gopus encoder) and pinned here so the celt-package
// oracle can replay the SWB decode path that seeds the DRED PLC history.
const seedCELTMonoSWBPacketHex = "d89f87b2aece4cbf34b16796cca502229c79dd139415a20a9761b8922f10bb6ed2d54bb8fb19e5cc4370e72d69fb76392d1f3a348c44ba3627aed687623b788c55bd5b89762cac9a35c1058bf3cb862233d52024aeb24a0d257d67db41f38fca20d34703ead30328b795f6b10a2afe1cc5a74ea492035aab3fc97f22702c139ebb5bc7b16527f7d26b5b97c20f3cf47e54598b7a85a57b60bb22536c1048da80b57c079b2857f9f6c6252fe2f73c32cb490cbca6a70426ef591b3aeebf30caa03d44da9f0c8fed90be55f9f2e9ec05115184d277896b0b66cbf7c56ba380beceab585d24b16d0b114d777a9c57174797e2b2c9ef814868a000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001d4cca76c6159af548942f8aff3064491596092403b7b88f1fc5c874b808c16c6a6e837a00baf41dbf867dd166432be801cca100a00dc9010775c32b1e8eae2ebb2a7caefcbb023c494fbf5c3044766370a3996d666d8c707a0580db"

// TestCELTSynthesisStagesSWBMatchLibopusC decodes a superwideband mono CELT
// frame and compares each synthesis stage (post-denormalise spectrum,
// post-IMDCT time buffer, post-deemphasis PCM) against libopus bit-exactly,
// localising the SWB-specific decode drift that seeds the DRED PLC history.
func TestCELTSynthesisStagesSWBMatchLibopusC(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)

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
