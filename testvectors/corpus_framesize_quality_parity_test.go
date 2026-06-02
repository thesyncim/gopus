package testvectors

// corpus_framesize_quality_parity_test.go — live (blob-free) decoder-parity
// quality gate that broadens the synthetic real-content corpus along the FRAME
// DURATION axis.
//
// The existing live corpus gate (corpus_signal_quality_parity_test.go) exercises
// signal class × channel × mode/bitrate, but only at a single 20 ms frame size.
// Frame duration is an orthogonal dimension that drives substantially different
// code paths in every mode:
//
//   - CELT short frames (2.5/5/10 ms) hit the short-MDCT / transient block path
//     instead of the long block used at 20 ms.
//   - SILK/Hybrid long frames (40/60 ms) are coded as multi-subframe / code-3
//     packets rather than a single sub-frame, exercising the framing and inner
//     SILK frame-count logic on the decode side.
//
// For each (signal class × channel × mode × frame duration) this test:
//
//   1. synthesizes deterministic 48 kHz PCM (internal/testsignal),
//   2. encodes it with the gopus encoder forcing the mode/bitrate/frame size,
//   3. decodes the SAME packets with BOTH gopus and the live libopus reference
//      decoder, and
//   4. gates gopus vs libopus with qualitycompare.AssertParity at the trusted
//      IntentNearExact bar.
//
// Because both decoders consume identical packets the streams are sample-aligned,
// so this measures gopus-vs-libopus decode parity (not lossy-codec quality),
// which is exactly what the near-exact bar is anchored to. No new thresholds are
// introduced and no committed binary fixtures are used. The harness (signal
// generators, decode helpers, AssertParity bar) is shared with the 20 ms gate; the
// only new dimension is the frame size.

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// corpusFrameSizeCase names a single frame duration and its 48 kHz sample count.
type corpusFrameSizeCase struct {
	name    string
	samples int // per-channel frame size at 48 kHz
}

// corpusFrameModeCfg pairs a forced codec mode with the frame durations that mode
// can legally encode, plus the bitrate to use. Durations that a mode rejects
// (e.g. SILK/Hybrid cannot do 2.5/5 ms) are simply omitted so only honest,
// encodable cases are gated.
type corpusFrameModeCfg struct {
	name       string
	mode       gopus.EncoderMode
	app        gopus.Application
	bitrate    int
	frameSizes []corpusFrameSizeCase
}

var (
	corpusFS2_5ms = corpusFrameSizeCase{"2.5ms", 120}
	corpusFS5ms   = corpusFrameSizeCase{"5ms", 240}
	corpusFS10ms  = corpusFrameSizeCase{"10ms", 480}
	corpusFS40ms  = corpusFrameSizeCase{"40ms", 1920}
	corpusFS60ms  = corpusFrameSizeCase{"60ms", 2880}
)

// corpusFrameModeConfigs covers every (mode × legal frame duration) combination
// outside the 20 ms point already gated by TestCorpusSignalQualityParity:
//
//   - SILK:   10/40/60 ms (short voice frames + 40/60 ms multi-frame packets)
//   - Hybrid: 10/40/60 ms (10 ms hybrid + 40/60 ms multi-frame packets)
//   - CELT:   2.5/5/10/40/60 ms (short-MDCT/transient blocks + 40/60 ms multi-frame)
var corpusFrameModeConfigs = []corpusFrameModeCfg{
	{
		name:    "silk_16k",
		mode:    gopus.EncoderModeSILK,
		app:     gopus.ApplicationVoIP,
		bitrate: 16000,
		frameSizes: []corpusFrameSizeCase{
			corpusFS10ms, corpusFS40ms, corpusFS60ms,
		},
	},
	{
		name:    "hybrid_48k",
		mode:    gopus.EncoderModeHybrid,
		app:     gopus.ApplicationAudio,
		bitrate: 48000,
		frameSizes: []corpusFrameSizeCase{
			corpusFS10ms, corpusFS40ms, corpusFS60ms,
		},
	},
	{
		name:    "celt_64k",
		mode:    gopus.EncoderModeCELT,
		app:     gopus.ApplicationAudio,
		bitrate: 64000,
		frameSizes: []corpusFrameSizeCase{
			corpusFS2_5ms, corpusFS5ms, corpusFS10ms, corpusFS40ms, corpusFS60ms,
		},
	},
}

// corpusFrameSizeSignalClasses is a representative slice of the broader corpus
// classes (one harmonic, one transient, one noise-like, one stereo-decorrelated)
// to keep the frame-size cross-product tractable while still covering distinct
// spectral/temporal regimes against every frame duration.
func corpusFrameSizeSignalClasses() []string {
	return []string{
		testsignal.CorpusMusicV1,            // harmonic / tonal
		testsignal.CorpusCastanetTransientV1, // sharp transients (short-block path)
		testsignal.CorpusPinkNoiseV1,         // tilted broadband noise
		testsignal.CorpusStereoDecorrelatedV1, // wide stereo image
	}
}

// TestCorpusFrameSizeQualityParity gates gopus-vs-libopus decode parity across the
// frame-duration axis for the broader corpus signal classes. Encoded live with
// gopus and decoded by both gopus and the libopus reference. No fixtures, no new
// thresholds.
func TestCorpusFrameSizeQualityParity(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	// Enough total audio per case for opus_compare's per-band model: at least
	// ~500 ms regardless of frame size.
	const totalSamplesPerChannel = 24000 // 500 ms @ 48 kHz

	for _, class := range corpusFrameSizeSignalClasses() {
		class := class
		for _, channels := range []int{1, 2} {
			channels := channels
			for _, cfg := range corpusFrameModeConfigs {
				cfg := cfg
				for _, fs := range cfg.frameSizes {
					fs := fs
					name := class + "/" + chanName(channels) + "/" + cfg.name + "/" + fs.name
					t.Run(name, func(t *testing.T) {
						t.Parallel()

						frameSamples := fs.samples * channels
						// Round total up to a whole number of frames.
						frames := (totalSamplesPerChannel + fs.samples - 1) / fs.samples
						totalInterleaved := frames * frameSamples

						pcm, err := testsignal.GenerateCorpusSignal(class, sampleRate, totalInterleaved, channels)
						if err != nil {
							t.Fatalf("generate %s: %v", class, err)
						}

						packets := encodeCorpusFrameSizePackets(t, pcm, channels, fs.samples, cfg)

						gopusDecoded := decodeWithInternalDecoder(t, packets, channels)
						if len(gopusDecoded) == 0 {
							t.Fatal("gopus decoded empty output")
						}

						// Tier-matched reference: SIMD libopus for the asm
						// gopus build, scalar libopus for the pure-Go build,
						// so the Q comparison is like-with-like.
						refDecoded, err := decodeWithMatchedTierReferencePacketsSingle(channels, fs.samples, packets)
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
}

// encodeCorpusFrameSizePackets encodes interleaved 48 kHz PCM into one packet per
// frame with the gopus encoder configured for the requested mode/bitrate/frame
// size. It mirrors encodeCorpusQualityPackets but takes the frame size from the
// caller so the frame-duration axis can be swept.
func encodeCorpusFrameSizePackets(t *testing.T, pcm []float32, channels, frameSize int, cfg corpusFrameModeCfg) [][]byte {
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
		t.Fatalf("set frame size %d: %v", frameSize, err)
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
			t.Fatalf("encode frame at %d (frameSize=%d): %v", off, frameSize, err)
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
