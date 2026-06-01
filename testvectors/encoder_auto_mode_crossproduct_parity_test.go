// encoder_auto_mode_crossproduct_parity_test.go verifies that gopus selects the
// same SILK/Hybrid/CELT mode and bandwidth as libopus 1.6.1 across the full
// cross-product of:
//   - application:  VOIP, AUDIO, RESTRICTED_LOWDELAY
//   - bitrate:      representative low / mid / high values
//   - frame size:   2.5 ms / 5 ms / 10 ms / 20 ms at 48 kHz
//   - signal class: OPUS_SIGNAL_VOICE / OPUS_SIGNAL_MUSIC / OPUS_AUTO
//
// The C oracle (tools/csrc/libopus_encoder_mode_crossproduct.c) encodes each
// combination with a stateful libopus 1.6.1 encoder and returns the TOC byte
// for each frame. The Go side creates a matching encoder and asserts the same
// mode label (silk / hybrid / celt) for every frame.
//
// Reference: libopus 1.6.1 src/opus_encoder.c lines 1466–1695.
package testvectors

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

// ── oracle protocol constants ────────────────────────────────────────────────

const (
	crossProductInputMagic  = "GCPI"
	crossProductOutputMagic = "GCPO"
)

// OPUS_APPLICATION_* numeric values from opus_defines.h.
const (
	opusApplicationVoIP              = 2048
	opusApplicationAudio             = 2049
	opusApplicationRestrictedLowDelay = 2051
)

// OPUS_SIGNAL_* numeric values from opus_defines.h.
const (
	opusSignalAuto  = uint32(0xFFFFFC18) // -1000 as uint32 (OPUS_AUTO)
	opusSignalVoice = uint32(3001)
	opusSignalMusic = uint32(3002)
)

// ── oracle binary protocol ───────────────────────────────────────────────────

type crossProductCase struct {
	sampleRate   int
	channels     int
	frameSize    int
	bitrate      int
	application  int    // opusApplication*
	signal       uint32 // opusSignal*
	numFrames    int
	maxDataBytes int
	pcm          []float32 // len = numFrames * frameSize * channels
}

type crossProductFrameResult struct {
	ret int32
	toc byte
}

type crossProductCaseResult struct {
	frames []crossProductFrameResult
}

var crossProductHelperCache libopustest.HelperCache

func getCrossProductHelperPath() (string, error) {
	return crossProductHelperCache.Path(func() (string, error) {
		return libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:      "encoder mode crossproduct",
			OutputBase: "gopus_libopus_encoder_mode_crossproduct",
			SourceFile: "libopus_encoder_mode_crossproduct.c",
			CFlags:     []string{"-DHAVE_CONFIG_H"},
			Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		})
	})
}

func runCrossProductOracle(cases []crossProductCase) ([]crossProductCaseResult, error) {
	binPath, err := getCrossProductHelperPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayload(crossProductInputMagic, uint32(len(cases)))
	for _, c := range cases {
		payload.U32(uint32(c.sampleRate))
		payload.U32(uint32(c.channels))
		payload.U32(uint32(c.frameSize))
		payload.U32(uint32(c.bitrate))
		payload.U32(uint32(c.application))
		payload.U32(c.signal)
		payload.U32(uint32(c.numFrames))
		payload.U32(uint32(c.maxDataBytes))
		for _, s := range c.pcm {
			payload.Float32(s)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "encoder mode crossproduct", crossProductOutputMagic)
	if err != nil {
		return nil, err
	}

	count := reader.Count(len(cases))
	results := make([]crossProductCaseResult, count)
	for i := range results {
		nf := int(reader.U32())
		results[i].frames = make([]crossProductFrameResult, nf)
		for f := range results[i].frames {
			results[i].frames[f].ret = reader.I32()
			results[i].frames[f].toc = byte(reader.U32())
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return results, nil
}

// ── PCM generation ───────────────────────────────────────────────────────────

// generateCrossProductPCM produces deterministic test PCM with three distinct
// signal classes embedded via an argument. The PCM matches what libopus sees.
//
//   - "voice": 110 Hz fundamental + harmonics (voiced speech character)
//   - "music": 440 Hz + 880 Hz AM modulated (musical character)
//   - "auto":  chirp sweep (no a-priori bias for the analysis frontend)
func generateCrossProductPCM(numFrames, frameSize, channels int, signalClass string) []float32 {
	totalSamples := numFrames * frameSize * channels
	pcm := make([]float32, totalSamples)
	sampleRate := 48000.0
	for i := 0; i < numFrames*frameSize; i++ {
		t := float64(i) / sampleRate
		var mono float64
		switch signalClass {
		case "voice":
			// voiced speech: 110 Hz + harmonics with slow amplitude envelope
			env := 0.5 + 0.5*math.Sin(2*math.Pi*3.0*t+0.1)
			mono = 0.6 * env * (math.Sin(2*math.Pi*110*t) +
				0.4*math.Sin(2*math.Pi*220*t) +
				0.2*math.Sin(2*math.Pi*330*t) +
				0.1*math.Sin(2*math.Pi*440*t))
		case "music":
			// AM-modulated 440 Hz + 880 Hz (music-like)
			mod := 0.5 + 0.5*math.Sin(2*math.Pi*2.5*t)
			mono = 0.55 * mod * math.Sin(2*math.Pi*440*t)
			mono += 0.35 * math.Sin(2*math.Pi*880*t)
		default: // "auto" / mixed
			// Linear chirp 80–8000 Hz over 1 s period
			period := 1.0
			phase := 2 * math.Pi * (80*t + 0.5*(8000-80)*math.Mod(t, period)*math.Mod(t, period)/period)
			mono = 0.5 * math.Sin(phase)
		}
		if mono > 0.98 {
			mono = 0.98
		}
		if mono < -0.98 {
			mono = -0.98
		}
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = float32(mono)
		}
	}
	return pcm
}

// ── TOC decoding ─────────────────────────────────────────────────────────────

// modeLabelFromTOC returns "silk", "hybrid", or "celt" from a TOC byte.
// Reference: RFC 6716 §3.1, opus_encoder.c gen_toc() lines 330-358.
func modeLabelFromTOC(toc byte) string {
	cfg := toc >> 3
	switch {
	case cfg <= 11:
		return "silk"
	case cfg <= 15:
		return "hybrid"
	default:
		return "celt"
	}
}

// ── gopus encoder construction ───────────────────────────────────────────────

// newCrossProductEncoder creates a gopus encoder that mirrors the C oracle's
// setup for a given application integer.
//
// Parameter mapping (libopus opus_encoder.c init + CTL calls in C oracle):
//   - OPUS_SET_BITRATE / VBR / VBR_CONSTRAINT / BANDWIDTH / SIGNAL /
//     COMPLEXITY / PACKET_LOSS_PERC / INBAND_FEC / DTX
//
// For RESTRICTED_LOWDELAY we set lowDelay=true and mode=ModeCELT, which mirrors
// what libopus does internally (opus_encoder.c line 1470):
//
//	} else if (st->application == OPUS_APPLICATION_RESTRICTED_LOWDELAY …) {
//	    st->mode = MODE_CELT_ONLY;
func newCrossProductEncoder(sampleRate, channels, bitrate, application int, signal uint32) *encoder.Encoder {
	enc := encoder.NewEncoder(sampleRate, channels)
	switch application {
	case opusApplicationVoIP:
		enc.SetVoIPApplication(true)
		enc.SetMode(encoder.ModeAuto)
		enc.SetBandwidth(types.BandwidthFullband)
	case opusApplicationRestrictedLowDelay:
		enc.SetLowDelay(true)
		enc.SetMode(encoder.ModeCELT)
		enc.SetBandwidth(types.BandwidthFullband)
	default: // AUDIO
		enc.SetMode(encoder.ModeAuto)
		enc.SetBandwidth(types.BandwidthFullband)
	}
	enc.SetBitrate(bitrate)
	enc.SetBitrateMode(encoder.ModeVBR)
	enc.SetVBR(true)
	enc.SetVBRConstraint(false)
	enc.SetComplexity(10)
	enc.SetLSBDepth(24)
	enc.SetPacketLoss(0)
	enc.SetFEC(false)
	enc.SetDTX(false)

	// Signal type.
	switch signal {
	case opusSignalVoice:
		enc.SetSignalType(types.SignalVoice)
	case opusSignalMusic:
		enc.SetSignalType(types.SignalMusic)
	default:
		enc.SetSignalType(types.SignalAuto)
	}

	return enc
}

// ── test matrix ──────────────────────────────────────────────────────────────

type crossProductKey struct {
	application  int
	bitrate      int
	frameSize    int
	signal       uint32
	channels     int
	signalClass  string
}

var (
	crossProductOnce    sync.Once
	crossProductResults []crossProductCaseResult
	crossProductCases   []crossProductCase
	crossProductKeys    []crossProductKey
	crossProductErr     error
)

// buildCrossProductMatrix constructs and (lazily) runs all oracle cases.
func buildCrossProductMatrix(t *testing.T) ([]crossProductCase, []crossProductKey, []crossProductCaseResult) {
	t.Helper()
	crossProductOnce.Do(func() {
		crossProductCases, crossProductKeys = makeCrossProductCases()
		if len(crossProductCases) == 0 {
			crossProductErr = fmt.Errorf("empty cross-product matrix")
			return
		}
		var err error
		crossProductResults, err = runCrossProductOracle(crossProductCases)
		if err != nil {
			crossProductErr = err
		}
	})
	if crossProductErr != nil {
		libopustest.HelperUnavailable(t, "encoder mode crossproduct", crossProductErr)
	}
	return crossProductCases, crossProductKeys, crossProductResults
}

func makeCrossProductCases() ([]crossProductCase, []crossProductKey) {
	const (
		sampleRate = 48000
		numFrames  = 10 // 10 frames per case gives enough stateful history
	)

	applications := []struct {
		id   int
		name string
	}{
		{opusApplicationVoIP, "voip"},
		{opusApplicationAudio, "audio"},
		{opusApplicationRestrictedLowDelay, "lowdelay"},
	}

	// Representative bitrates covering low / mid / high decision regions.
	// libopus mode_thresholds: mono-voice=64000, mono-music=10000.
	bitrates := []int{
		6000,   // below music threshold → always CELT (low-rate fallback)
		12000,  // near music threshold
		24000,  // SILK territory for voice
		40000,  // hybrid boundary for mono-voice
		64000,  // CELT territory for most signals
		96000,  // high-rate CELT
	}

	// Frame sizes at 48 kHz: 2.5 ms (120), 5 ms (240), 10 ms (480), 20 ms (960).
	frameSizes := []int{120, 240, 480, 960}

	signals := []struct {
		id          uint32
		name        string
		signalClass string
	}{
		{opusSignalVoice, "voice", "voice"},
		{opusSignalMusic, "music", "music"},
		{opusSignalAuto, "auto", "auto"},
	}

	channelCounts := []int{1, 2}

	var cases []crossProductCase
	var keys []crossProductKey

	for _, app := range applications {
		for _, br := range bitrates {
			for _, fs := range frameSizes {
				for _, sig := range signals {
					for _, ch := range channelCounts {
						pcm := generateCrossProductPCM(numFrames, fs, ch, sig.signalClass)
						maxDB := (br*fs/sampleRate + 255) / 8
						if maxDB < 3 {
							maxDB = 3
						}
						if maxDB > 1275 {
							maxDB = 1275
						}
						cases = append(cases, crossProductCase{
							sampleRate:   sampleRate,
							channels:     ch,
							frameSize:    fs,
							bitrate:      br,
							application:  app.id,
							signal:       sig.id,
							numFrames:    numFrames,
							maxDataBytes: maxDB,
							pcm:          pcm,
						})
						keys = append(keys, crossProductKey{
							application: app.id,
							bitrate:     br,
							frameSize:   fs,
							signal:      sig.id,
							channels:    ch,
							signalClass: sig.signalClass,
						})
					}
				}
			}
		}
	}
	return cases, keys
}

// ── test ─────────────────────────────────────────────────────────────────────

// TestEncoderAutoModeCrossProductParity encodes identical PCM through gopus and
// the pinned libopus 1.6.1 C encoder for every cell of the
//
//	application × bitrate × frame-size × signal-class × channels
//
// cross-product, then asserts that the mode label (silk/hybrid/celt) decoded
// from the TOC byte matches libopus on every frame.
//
// A maximum 2% per-stream mode-mismatch budget is allowed to tolerate
// single-frame hysteresis at decision boundaries; the first-frame decision
// must be exact.
func TestEncoderAutoModeCrossProductParity(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)
	libopustest.RequireOracle(t)

	cases, keys, results := buildCrossProductMatrix(t)

	appName := func(app int) string {
		switch app {
		case opusApplicationVoIP:
			return "voip"
		case opusApplicationAudio:
			return "audio"
		case opusApplicationRestrictedLowDelay:
			return "lowdelay"
		default:
			return fmt.Sprintf("app%d", app)
		}
	}
	sigName := func(sig uint32) string {
		switch sig {
		case opusSignalVoice:
			return "voice"
		case opusSignalMusic:
			return "music"
		default:
			return "auto"
		}
	}

	type failInfo struct {
		frame     int
		got, want string
		gotTOC    byte
		wantTOC   byte
	}

	for idx := range cases {
		c := cases[idx]
		k := keys[idx]
		r := results[idx]

		name := fmt.Sprintf("%s/br%d/fs%d/%sch%d/%s",
			appName(k.application), k.bitrate, k.frameSize,
			sigName(k.signal), k.channels, k.signalClass)

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Build the gopus encoder with the same settings as the C oracle.
			enc := newCrossProductEncoder(c.sampleRate, c.channels, c.bitrate,
				c.application, c.signal)

			samplesPerFrame := c.frameSize * c.channels
			var mismatches []failInfo

			for f, wantFrame := range r.frames {
				if wantFrame.ret <= 0 {
					// libopus returned an error for this frame; skip comparison.
					continue
				}

				start := f * samplesPerFrame
				end := start + samplesPerFrame
				frame32 := c.pcm[start:end]

				gotPacket, err := enc.Encode(frame32, c.frameSize)
				if err != nil {
					t.Errorf("frame %d: encode error: %v", f, err)
					continue
				}
				if len(gotPacket) == 0 {
					t.Errorf("frame %d: empty packet", f)
					continue
				}

				gotLabel := modeLabelFromTOC(gotPacket[0])
				wantLabel := modeLabelFromTOC(wantFrame.toc)

				if gotLabel != wantLabel {
					mismatches = append(mismatches, failInfo{
						frame:   f,
						got:     gotLabel,
						want:    wantLabel,
						gotTOC:  gotPacket[0],
						wantTOC: wantFrame.toc,
					})
				}
			}

			nFrames := len(r.frames)
			if nFrames == 0 {
				t.Skip("no valid frames from oracle")
				return
			}

			// First-frame mode must be exact (no hysteresis on frame 0).
			if len(mismatches) > 0 && mismatches[0].frame == 0 {
				fi := mismatches[0]
				t.Errorf("first-frame mode mismatch: got=%s want=%s (go_toc=0x%02x lib_toc=0x%02x)",
					fi.got, fi.want, fi.gotTOC, fi.wantTOC)
			}

			// Allow ≤2% mismatch across the stream (hysteresis tolerance).
			mismatchRatio := float64(len(mismatches)) / float64(nFrames)
			const maxMismatchRatio = 0.02
			if mismatchRatio > maxMismatchRatio {
				fi := mismatches[0]
				t.Errorf("mode mismatch ratio %.1f%% (%d/%d) exceeds %.0f%% budget; first mismatch frame=%d got=%s want=%s (go_toc=0x%02x lib_toc=0x%02x)",
					mismatchRatio*100, len(mismatches), nFrames, maxMismatchRatio*100,
					fi.frame, fi.got, fi.want, fi.gotTOC, fi.wantTOC)
			}
			t.Logf("frames=%d mismatches=%d (%.1f%%)", nFrames, len(mismatches), mismatchRatio*100)
		})
	}
}
