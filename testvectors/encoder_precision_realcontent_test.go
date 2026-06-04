package testvectors

// Real-content signal source for the encoder precision guard.
//
// The precision guard measures encode QUALITY (gopus Q minus libopus Q). The
// am_multisine synthetic that drives the byte-parity stress lanes is a CELT
// float-order knife-edge: its steady AM sines sit on band-decision boundaries,
// so a <=1-ULP-per-op difference in summation order flips many CELT band
// decisions and amplifies into ~9 Q. gopus has one portable float order; it
// byte-matches whichever libopus toolchain shares that order (Apple-NEON /
// scalar) but not gcc-NEON or amd64-SSE, even on the SAME arch. A direct probe
// confirmed this: on am_multisine the NEON-libopus and scalar-libopus builds
// disagree by up to 2.55 Q WITH EACH OTHER on one machine, while on real
// recordings their self-spread collapses to 0.00 and gopus tracks both to
// <=0.10 Q.
//
// The precision guard therefore runs on representative real audio, where Q is a
// genuine quality measure rather than float-order noise, so the tight base
// floors hold on every toolchain with no per-arch budgets. The source is the
// committed real-content corpus (testdata/realcontent_corpus_fixture.json:
// speech and music clips extracted from the RFC 6716/8251 Opus test vectors) --
// no new binary asset. The byte-exact LOGIC stress (am_multisine / chirp /
// impulse byte parity, the celt PVQ grid) is untouched on its synthetic
// signals; only this QUALITY guard moves to real audio.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/types"
)

// precisionGuardSignalName labels the real-content source in logs.
const precisionGuardSignalName = "realcontent_speech_music"

// precisionGuardRealContentSignal builds the deterministic real-content signal
// the precision guard encodes: the speech clip "speech_talker_b" concatenated
// with the broadband-music clip "music_dense_wide" (both real RFC recordings),
// looped to fill totalSamples. The concatenation gives a single 1 s window that
// spans voiced speech (SILK) and dense wideband music (CELT/Hybrid), so one
// source exercises every mode meaningfully. For mono it uses the equal-power
// downmix; for stereo it uses the interleaved clips directly. The construction
// is byte-deterministic, so the gopus-encoded input and the live libopus
// reference input are identical samples.
func precisionGuardRealContentSignal(totalSamples, channels int) ([]float32, error) {
	clips, err := loadRealcontentSourceClips()
	if err != nil {
		return nil, fmt.Errorf("load real-content corpus source: %w", err)
	}
	speech := clips["speech_talker_b"]
	music := clips["music_dense_wide"]
	if len(speech) == 0 || len(music) == 0 {
		return nil, fmt.Errorf("real-content corpus missing required clips (speech_talker_b/music_dense_wide)")
	}

	// Concatenate as interleaved stereo, then downmix to mono if requested.
	stereo := make([]float32, 0, len(speech)+len(music))
	stereo = append(stereo, speech...)
	stereo = append(stereo, music...)

	var src []float32
	if channels == 1 {
		src = realcontentMono(stereo)
	} else {
		src = stereo
	}
	if len(src) == 0 {
		return nil, fmt.Errorf("real-content source produced empty signal")
	}

	out := make([]float32, totalSamples)
	for i := 0; i < totalSamples; i++ {
		out[i] = src[i%len(src)]
	}
	return out, nil
}

var (
	realContentSourceOnce  sync.Once
	realContentSourceClips map[string][]float32
	realContentSourceErr   error
)

// loadRealcontentSourceClips loads the real-content corpus SOURCE PCM keyed by
// clip name (interleaved stereo float32). It reads the generic committed fixture
// directly: the source recordings are platform-independent, so it does not apply
// the libopus-build provenance/platform gate that loadRealcontentFixture enforces
// (that gate guards the fixture's FROZEN libopus encodes, which the precision
// guard does not consume -- the precision reference is produced live by the
// native same-arch opus_demo). Each clip's sha256 integrity is still verified, so
// a drifted source recording is caught.
func loadRealcontentSourceClips() (map[string][]float32, error) {
	realContentSourceOnce.Do(func() {
		raw, err := os.ReadFile(realcontentFixturePath)
		if err != nil {
			realContentSourceErr = err
			return
		}
		var fixture realcontentFixtureFileData
		if err := json.Unmarshal(raw, &fixture); err != nil {
			realContentSourceErr = err
			return
		}
		if fixture.SampleRate != 48000 {
			realContentSourceErr = fmt.Errorf("real-content source: unsupported sample rate %d", fixture.SampleRate)
			return
		}
		clips := make(map[string][]float32, len(fixture.Cases))
		for i := range fixture.Cases {
			c := &fixture.Cases[i]
			pcmBytes, err := base64.StdEncoding.DecodeString(c.PCMS16LEB64)
			if err != nil {
				realContentSourceErr = fmt.Errorf("real-content source: clip %s base64: %w", c.Name, err)
				return
			}
			if len(pcmBytes)%2 != 0 {
				realContentSourceErr = fmt.Errorf("real-content source: clip %s PCM length not even", c.Name)
				return
			}
			n := len(pcmBytes) / 2
			samples := make([]float32, n)
			for j := 0; j < n; j++ {
				s := int16(binary.LittleEndian.Uint16(pcmBytes[j*2 : j*2+2]))
				samples[j] = float32(s) / 32768.0
			}
			if got := realcontentClipSHA256(samples); got != c.PCMSHA256 {
				realContentSourceErr = fmt.Errorf("real-content source: clip %s sha256 mismatch (fixture=%s got=%s)", c.Name, c.PCMSHA256, got)
				return
			}
			clips[c.Name] = samples
		}
		realContentSourceClips = clips
	})
	return realContentSourceClips, realContentSourceErr
}

var (
	realContentGopusRunCache   sync.Map // encoderComplianceCaseKey -> *encoderComplianceRunEntry
	realContentLibopusRefCache sync.Map // encoderComplianceCaseKey -> *libopusComplianceReferenceEntry
)

func realContentGopusRunEntry(key encoderComplianceCaseKey) *encoderComplianceRunEntry {
	v, _ := realContentGopusRunCache.LoadOrStore(key, &encoderComplianceRunEntry{})
	return v.(*encoderComplianceRunEntry)
}

func realContentLibopusRefEntry(key encoderComplianceCaseKey) *libopusComplianceReferenceEntry {
	v, _ := realContentLibopusRefCache.LoadOrStore(key, &libopusComplianceReferenceEntry{})
	return v.(*libopusComplianceReferenceEntry)
}

// runRealContentPrecisionGopus encodes the real-content signal with gopus and
// returns its opus_compare Q (decoded by the libopus reference decoder, scored
// against the original real-content input). It mirrors runEncoderComplianceTest
// but on the real-content source and with its own cache.
func runRealContentPrecisionGopus(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) float64 {
	t.Helper()
	key := encoderComplianceKey(mode, bandwidth, frameSize, channels, bitrate)
	entry := realContentGopusRunEntry(key)
	entry.once.Do(func() {
		numFrames := 48000 / frameSize
		totalSamples := numFrames * frameSize * channels
		signal, err := precisionGuardRealContentSignal(totalSamples, channels)
		if err != nil {
			entry.err = err
			return
		}
		entry.result, entry.err = computeEncoderComplianceResultForSignal(mode, bandwidth, frameSize, channels, bitrate, signal)
	})
	if entry.err != nil {
		t.Fatalf("real-content gopus encode (%s): %v", precisionGuardSignalName, entry.err)
	}
	t.Logf("RealContent gopus Q=%.2f (source=%s)", entry.result.q, precisionGuardSignalName)
	return entry.result.q
}

// runRealContentPrecisionLibopusReference encodes the SAME real-content signal
// with the native same-arch libopus opus_demo (live on this runner), decodes the
// reference packets the same way as the gopus side, and returns the reference Q.
// The reference is always libopus, built and run natively, so the comparison is
// same-arch-and-toolchain. When opus_demo is unavailable it returns ok=false.
func runRealContentPrecisionLibopusReference(t *testing.T, mode encoder.Mode, bandwidth types.Bandwidth, frameSize, channels, bitrate int) (float64, bool) {
	t.Helper()
	key := encoderComplianceKey(mode, bandwidth, frameSize, channels, bitrate)
	entry := realContentLibopusRefEntry(key)
	entry.once.Do(func() {
		opusDemo, ok := getFixtureOpusDemoPathForEncoder()
		if !ok {
			entry.result.warning = "opus_demo not available for real-content precision reference"
			return
		}
		modeName := fixtureModeName(mode)
		bwName := fixtureBandwidthName(bandwidth)
		app, err := modeToOpusDemoApp(modeName)
		if err != nil {
			entry.result.warning = err.Error()
			return
		}
		bwArg, err := bandwidthToOpusDemoArg(bwName)
		if err != nil {
			entry.result.warning = err.Error()
			return
		}
		frameArg, err := frameSizeSamplesToArg(frameSize)
		if err != nil {
			entry.result.warning = err.Error()
			return
		}

		numFrames := 48000 / frameSize
		totalSamples := numFrames * frameSize * channels
		signal, err := precisionGuardRealContentSignal(totalSamples, channels)
		if err != nil {
			entry.result.warning = err.Error()
			return
		}

		tmpDir := t.TempDir()
		rawPath := filepath.Join(tmpDir, "realcontent.f32")
		bitPath := filepath.Join(tmpDir, "realcontent.bit")
		if err := writeFloat32LEFile(rawPath, signal); err != nil {
			entry.result.warning = fmt.Sprintf("write real-content input: %v", err)
			return
		}
		packets, _, err := runOpusDemoCELTEncode(opusDemo, app, bwArg, frameArg, bitrate, channels, rawPath, bitPath)
		if err != nil {
			entry.result.warning = fmt.Sprintf("opus_demo real-content encode: %v", err)
			return
		}
		cmp, _, err := qualityOfPackets(packets, signal, channels, frameSize)
		if err != nil {
			entry.result.warning = fmt.Sprintf("real-content reference quality: %v", err)
			return
		}
		entry.result.q = cmp.Q
		entry.result.ok = true
	})
	if entry.result.warning != "" {
		t.Log(entry.result.warning)
	}
	return entry.result.q, entry.result.ok
}
