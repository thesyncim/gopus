package testvectors

// corpus_signal_quality_parity_test.go — live (blob-free) decoder-parity quality
// gate for the broader synthetic signal classes added to internal/testsignal.
//
// Unlike corpus_decoder_parity_test.go (which replays frozen libopus-decoded
// fixtures), this test is fully self-contained: for each synthetic signal class it
//
//   1. synthesizes deterministic 48 kHz PCM (internal/testsignal),
//   2. encodes it with the gopus encoder (forcing a target mode/bitrate),
//   3. decodes the SAME packets with BOTH gopus and the libopus reference
//      decoder (built live from the pinned libopus tree), and
//   4. gates gopus vs libopus with qualitycompare.AssertParity at the trusted
//      IntentNearExact bar.
//
// Because both decoders consume identical packets the streams are sample-aligned,
// so this measures gopus-vs-libopus decode parity (not lossy-codec quality), which
// is exactly what the near-exact bar is anchored to. No new thresholds are
// introduced and no committed binary fixtures are used.

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/internal/testsignal"
)

type corpusQualityModeCfg struct {
	name    string
	mode    gopus.EncoderMode
	app     gopus.Application
	bitrate int
}

// corpusQualityModeConfigs drives one SILK, one Hybrid, and one CELT config so the
// new classes are exercised across all three codec modes at both channel counts,
// plus the two extreme-bitrate endpoints (lowest practical SILK rate and maximum
// CELT rate). Every config decodes identical packets on the gopus and libopus
// sides, so the same near-exact bar holds across the whole matrix.
var corpusQualityModeConfigs = []corpusQualityModeCfg{
	{name: "silk_16k", mode: gopus.EncoderModeSILK, app: gopus.ApplicationVoIP, bitrate: 16000},
	{name: "hybrid_48k", mode: gopus.EncoderModeHybrid, app: gopus.ApplicationAudio, bitrate: 48000},
	{name: "celt_96k", mode: gopus.EncoderModeCELT, app: gopus.ApplicationAudio, bitrate: 96000},
	{name: "silk_6k", mode: gopus.EncoderModeSILK, app: gopus.ApplicationVoIP, bitrate: 6000},
	{name: "celt_510k", mode: gopus.EncoderModeCELT, app: gopus.ApplicationAudio, bitrate: 510000},
}

// TestCorpusSignalQualityParity gates gopus-vs-libopus decode parity on the newly
// added synthetic signal classes, encoded live with gopus and decoded by both
// gopus and the libopus reference. No fixtures, no new thresholds.
func TestCorpusSignalQualityParity(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	const frameSize = 960 // 20 ms @ 48 kHz
	const frames = 25     // 500 ms of audio: plenty for opus_compare's per-band model

	for _, class := range testsignal.CorpusExtendedSignalClasses() {
		for _, channels := range []int{1, 2} {
			for _, cfg := range corpusQualityModeConfigs {
				name := class + "/" + chanName(channels) + "/" + cfg.name
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					pcm, err := testsignal.GenerateCorpusSignal(class, sampleRate, frameSize*frames*channels, channels)
					if err != nil {
						t.Fatalf("generate %s: %v", class, err)
					}

					packets := encodeCorpusQualityPackets(t, pcm, channels, frameSize, cfg)

					gopusDecoded := decodeWithInternalDecoder(t, packets, channels)
					if len(gopusDecoded) == 0 {
						t.Fatal("gopus decoded empty output")
					}

					// Tier-matched reference: SIMD libopus for the asm gopus
					// build, scalar libopus for the pure-Go build, so the Q
					// comparison is like-with-like.
					refDecoded, err := decodeWithMatchedTierReferencePacketsSingle(channels, frameSize, packets)
					if err != nil {
						libopustest.HelperUnavailable(t, "matched-tier reference decode", err)
					}
					if len(refDecoded) == 0 {
						t.Fatal("libopus reference decoded empty output")
					}

					n := len(gopusDecoded)
					if len(refDecoded) < n {
						n = len(refDecoded)
					}
					profile := qualitycompare.CodedProfile(sampleRate, channels, n)
					qualitycompare.AssertParity(t, gopusDecoded[:n], refDecoded[:n], profile,
						qualitycompare.IntentNearExact, name)
				})
			}
		}
	}
}

func chanName(channels int) string {
	if channels == 2 {
		return "stereo"
	}
	return "mono"
}

// encodeCorpusQualityPackets encodes interleaved 48 kHz PCM into one packet per
// frame with the gopus encoder configured for the requested mode/bitrate.
func encodeCorpusQualityPackets(t *testing.T, pcm []float32, channels, frameSize int, cfg corpusQualityModeCfg) [][]byte {
	t.Helper()
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  48000,
		Channels:    channels,
		Application: cfg.app,
	})
	if err != nil {
		t.Fatalf("create encoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("set frame size: %v", err)
	}
	if err := enc.SetMode(cfg.mode); err != nil {
		t.Fatalf("set mode %v: %v", cfg.mode, err)
	}
	if err := enc.SetBitrate(cfg.bitrate); err != nil {
		t.Fatalf("set bitrate %d: %v", cfg.bitrate, err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("force stereo: %v", err)
		}
	}

	frameSamples := frameSize * channels
	packet := make([]byte, 4000)
	var packets [][]byte
	for off := 0; off+frameSamples <= len(pcm); off += frameSamples {
		nBytes, err := enc.Encode(pcm[off:off+frameSamples], packet)
		if err != nil {
			t.Fatalf("encode frame at %d: %v", off, err)
		}
		if nBytes > 0 {
			packets = append(packets, append([]byte(nil), packet[:nBytes]...))
		}
	}
	if len(packets) == 0 {
		t.Fatal("encoder produced no packets")
	}
	return packets
}
