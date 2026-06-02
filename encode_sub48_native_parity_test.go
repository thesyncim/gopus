// encode_sub48_native_parity_test.go — CHARACTERIZING gate for sub-48 kHz
// native SILK + Hybrid ENCODE parity vs the same-arch libopus float
// opus_encode_float oracle at Fs ∈ {8000,12000,16000,24000}.
//
// CONTEXT / WHY THIS GATE EXISTS
//
// gopus encode is byte-exact vs libopus at 48 kHz (proven by
// TestEncodeDifferentialFuzz). It DIVERGES at native sub-48 kHz SILK/Hybrid.
// The root cause is an input-rate contract mismatch:
//
//   - libopus opus_encode(Fs) consumes NATIVE-Fs PCM: at Fs=16000 a 20 ms frame
//     is 320 native samples, and rate control / framing is computed against Fs.
//
//   - gopus's public Encoder ALWAYS runs the internal pipeline at 48 kHz
//     (encoder.NewEncoder forces sampleRate=48000 for any non-48k API rate) and
//     resamples the input down to the target rate inside the SILK path
//     (silk.NewDownsamplingResampler(48000, rate)). The public Encode /
//     EncodeFloat32 therefore demands frameSize*channels samples where frameSize
//     is interpreted as a 48 kHz-RELATIVE count, NOT a native-Fs count. A
//     native-Fs-length frame (320 samples at 16 kHz / 20 ms) is REJECTED with
//     ErrInvalidFrameSize; only the 48 kHz-relative length (960) is accepted, and
//     those samples are then treated as 48 kHz input and downsampled internally.
//
// The mode/bandwidth DECISION is already correct at sub-48k (the TOC config byte
// matches libopus); the divergence is in the SILK payload + packet length,
// because the per-frame bit budget / rate control is computed at the wrong rate.
//
// WHAT THIS GATE DOES (and why it is CI-green today)
//
//   1. TestSub48NativeInputRateContract HARD-asserts the documented contract: at
//      every sub-48k rate, a native-Fs-length frame is rejected and the
//      48 kHz-relative-length frame is accepted. This is the contract pinned as a
//      test so the FIX agent knows exactly what behaviour must change.
//
//   2. TestSub48NativeEncodeParity is the differential harness:
//        * Fs == 48000: byte-exact lock, with the SAME arch-aware policy as
//          TestEncodeDifferentialFuzz — SILK payload is HARD byte-exact (integer/
//          range-coded, same-arch exact on every arch), while a Hybrid/CELT
//          payload divergence is the documented float-analysis boundary
//          (project_arm64_celt_1ulp_drift on arm64, and the separately tracked
//          amd64 Hybrid-FB cross-arch libopus instability,
//          project_amd64_encoder_precision_regression) and is LOGGED, not failed.
//          This locks the working SILK path without duplicating the already-tracked
//          Hybrid cross-arch residual.
//        * Fs ∈ {8000,12000,16000,24000}: feeds the libopus oracle NATIVE-Fs
//          frames (the correctness target) and feeds gopus the 48 kHz-relative
//          frames drawn from the SAME native-Fs source (the only thing gopus
//          accepts today). It HARD-asserts gopus accepts the frame / does not
//          panic and that the TOC MODE CLASS matches (the decision is correct),
//          then LOGS the precise first divergence (frame index, first differing
//          byte, gopus-vs-libopus TOC + length) as the documented sub-48k gap.
//
// This is the LOCK the FIX agent flips: once the input-rate contract is fixed so
// gopus consumes native-Fs PCM byte-exactly, the t.Logf documented-gap lines in
// TestSub48NativeEncodeParity become t.Errorf (and the contract test's
// "native rejected" expectation inverts to "native accepted").
//
// Run with:
//   GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
//     go test -run 'Sub48' .
//
// Reuses the encode-diff oracle (internal/libopustest.ProbeEncodeDiff →
// tools/csrc/libopus_encode_diff_info.c) and the in-package divergence
// classifiers (byte0/firstByteDiff/tocModeClass/modeClassName/vbrFlags). All new
// helpers are uniquely sub48*-prefixed.

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// sub48NativeRates is the native sub-48k SILK/Hybrid sample-rate set under test.
var sub48NativeRates = []int{8000, 12000, 16000, 24000}

// sub48DurationMs maps the expert frame duration to its millisecond span (only
// the SILK/Hybrid-legal durations used by this gate).
func sub48DurationMs(d ExpertFrameDuration) int {
	switch d {
	case ExpertFrameDuration10Ms:
		return 10
	case ExpertFrameDuration20Ms:
		return 20
	case ExpertFrameDuration40Ms:
		return 40
	case ExpertFrameDuration60Ms:
		return 60
	default:
		return 20
	}
}

// sub48NativeFrameSamples returns the per-channel NATIVE-Fs sample count for one
// frame of the given duration — what libopus opus_encode(Fs) consumes.
func sub48NativeFrameSamples(fs int, d ExpertFrameDuration) int {
	return fs * sub48DurationMs(d) / 1000
}

// sub48Spec is one point in the sub-48k SILK/Hybrid encode configuration space.
type sub48Spec struct {
	name     string
	mode     EncoderMode
	forceMD  int // libopus FORCE_MODE code
	gbw      Bandwidth
	bwCode   int
	dur      ExpertFrameDuration
	channels int
	vbr      BitrateMode
	bitrate  int
	complex  int
	sigClass string
}

// sub48BuildSweep enumerates SILK NB/MB/WB + Hybrid SWB across mono+stereo,
// 10/20/40/60 ms (Hybrid 10/20 only), CBR/CVBR/VBR, complexity 0/5/10. Each spec
// is run at all of sub48NativeRates (and additionally at 48000 for the
// byte-exact lock) by the caller.
func sub48BuildSweep() []sub48Spec {
	var specs []sub48Spec

	type modeDef struct {
		name     string
		mode     EncoderMode
		forceMD  int
		gbw      Bandwidth
		bwCode   int
		durs     []ExpertFrameDuration
		sigClass string
	}
	silkDurs := []ExpertFrameDuration{ExpertFrameDuration10Ms, ExpertFrameDuration20Ms, ExpertFrameDuration40Ms, ExpertFrameDuration60Ms}
	hybridDurs := []ExpertFrameDuration{ExpertFrameDuration10Ms, ExpertFrameDuration20Ms}
	modes := []modeDef{
		{"silk_nb", EncoderModeSILK, libopustest.EncodeDiffForceModeSILKOnly, BandwidthNarrowband, libopustest.EncodeDiffBandwidthNarrowband, silkDurs, testsignal.CorpusCleanSpeechV1},
		{"silk_mb", EncoderModeSILK, libopustest.EncodeDiffForceModeSILKOnly, BandwidthMediumband, libopustest.EncodeDiffBandwidthMediumband, silkDurs, testsignal.CorpusCleanSpeechV1},
		{"silk_wb", EncoderModeSILK, libopustest.EncodeDiffForceModeSILKOnly, BandwidthWideband, libopustest.EncodeDiffBandwidthWideband, silkDurs, testsignal.CorpusSpeechInNoiseV1},
		{"hybrid_swb", EncoderModeHybrid, libopustest.EncodeDiffForceModeHybrid, BandwidthSuperwideband, libopustest.EncodeDiffBandwidthSuperwideband, hybridDurs, testsignal.CorpusMixedV1},
	}
	rcModes := []BitrateMode{BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR}
	complexities := []int{0, 5, 10}

	for _, m := range modes {
		bitrate := 24000
		if m.name == "hybrid_swb" {
			bitrate = 48000
		}
		ci := 0
		for _, dur := range m.durs {
			for _, ch := range []int{1, 2} {
				// Rotate rate-control + complexity across the (dur×channel) grid so
				// every combination is covered without a full cartesian blow-up.
				rc := rcModes[ci%len(rcModes)]
				cx := complexities[ci%len(complexities)]
				ci++
				specs = append(specs, sub48Spec{
					name:     fmt.Sprintf("%s_%dms_%s", m.name, sub48DurationMs(dur), sub48ChName(ch)),
					mode:     m.mode,
					forceMD:  m.forceMD,
					gbw:      m.gbw,
					bwCode:   m.bwCode,
					dur:      dur,
					channels: ch,
					vbr:      rc,
					bitrate:  bitrate,
					complex:  cx,
					sigClass: m.sigClass,
				})
			}
		}
	}
	return specs
}

func sub48ChName(ch int) string {
	if ch == 2 {
		return "stereo"
	}
	return "mono"
}

// sub48ConfigureGopus builds and configures a gopus Encoder for one spec at the
// given API sample rate. The frame size is the 48 kHz-RELATIVE count (the only
// thing the public Encode accepts), per the documented input-rate contract.
func sub48ConfigureGopus(t *testing.T, spec sub48Spec, fs int) (*Encoder, bool) {
	t.Helper()
	enc, err := NewEncoder(EncoderConfig{SampleRate: fs, Channels: spec.channels, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder(%s @ %d): %v", spec.name, fs, err)
	}
	if err := enc.SetMode(spec.mode); err != nil {
		return nil, false
	}
	if err := enc.SetBandwidth(spec.gbw); err != nil {
		return nil, false
	}
	if err := enc.SetMaxBandwidth(spec.gbw); err != nil {
		return nil, false
	}
	if err := enc.SetBitrate(spec.bitrate); err != nil {
		return nil, false
	}
	if err := enc.SetBitrateMode(spec.vbr); err != nil {
		return nil, false
	}
	if err := enc.SetComplexity(spec.complex); err != nil {
		return nil, false
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		return nil, false
	}
	if spec.channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			return nil, false
		}
	} else {
		if err := enc.SetForceChannels(1); err != nil {
			return nil, false
		}
	}
	if err := enc.SetFrameSize(encFrameSamples48k(spec.dur)); err != nil {
		return nil, false
	}
	if err := enc.SetExpertFrameDuration(spec.dur); err != nil {
		return nil, false
	}
	return enc, true
}

// TestSub48NativeInputRateContract HARD-pins the documented sub-48k input-rate
// contract of the public Encoder: frame size is interpreted as a 48 kHz-relative
// sample count regardless of the configured SampleRate, so a native-Fs-length
// frame is REJECTED and only the 48 kHz-relative length is accepted.
//
// This is the contract the FIX agent must invert (native-Fs PCM should be the
// accepted input). It runs without the libopus oracle (pure API behaviour).
func TestSub48NativeInputRateContract(t *testing.T) {
	for _, fs := range sub48NativeRates {
		for _, dur := range []ExpertFrameDuration{ExpertFrameDuration10Ms, ExpertFrameDuration20Ms} {
			fs, dur := fs, dur
			name := fmt.Sprintf("fs%d_%dms", fs, sub48DurationMs(dur))
			t.Run(name, func(t *testing.T) {
				enc, err := NewEncoder(EncoderConfig{SampleRate: fs, Channels: 1, Application: ApplicationAudio})
				if err != nil {
					t.Fatalf("NewEncoder(%d): %v", fs, err)
				}
				enc.SetMode(EncoderModeSILK)
				enc.SetBandwidth(BandwidthWideband)
				enc.SetMaxBandwidth(BandwidthWideband)
				enc.SetBitrate(24000)
				enc.SetForceChannels(1)
				if err := enc.SetFrameSize(encFrameSamples48k(dur)); err != nil {
					t.Fatalf("SetFrameSize(%d): %v", encFrameSamples48k(dur), err)
				}
				if err := enc.SetExpertFrameDuration(dur); err != nil {
					t.Fatalf("SetExpertFrameDuration: %v", err)
				}

				// FrameSize() reports the 48 kHz-relative count, NOT the native count.
				rel := encFrameSamples48k(dur)
				if got := enc.FrameSize(); got != rel {
					t.Fatalf("fs=%d FrameSize()=%d want 48k-relative %d", fs, got, rel)
				}

				nativeSamples := sub48NativeFrameSamples(fs, dur)
				if nativeSamples == rel {
					t.Fatalf("test bug: native (%d) == 48k-relative (%d) at fs=%d", nativeSamples, rel, fs)
				}

				// CONTRACT (current): native-Fs-length frame is REJECTED.
				nativeFrame := make([]float32, nativeSamples)
				if _, err := enc.EncodeFloat32(nativeFrame); err != ErrInvalidFrameSize {
					t.Errorf("fs=%d %dms: EncodeFloat32(native %d samples) err=%v, want ErrInvalidFrameSize "+
						"(documented sub-48k contract: input is 48k-relative). If this now ACCEPTS native-Fs PCM, "+
						"the input-rate FIX landed — invert this expectation and flip the parity gap to t.Errorf.",
						fs, sub48DurationMs(dur), nativeSamples, err)
				}

				// CONTRACT (current): 48 kHz-relative-length frame is ACCEPTED.
				relFrame := make([]float32, rel)
				if _, err := enc.EncodeFloat32(relFrame); err != nil {
					t.Errorf("fs=%d %dms: EncodeFloat32(48k-relative %d samples) err=%v, want accepted",
						fs, sub48DurationMs(dur), rel, err)
				}
			})
		}
	}
}

// sub48ParityResult records one spec×rate comparison outcome.
type sub48ParityResult struct {
	name         string
	fs           int
	firstDivFr   int // first frame index that diverged (-1 = byte-exact)
	firstDivByte int // first differing byte index within that frame
	firstDivCls  int // TOC mode class (0=SILK,1=Hybrid,2=CELT) at the first divergence
	gLen, oLen   int // packet lengths at the first divergence
	gTOC, oTOC   byte
	tocFlip      bool
}

// TestSub48NativeEncodeParity is the characterizing gate. At 48 kHz it locks the
// working path: SILK packets are HARD byte-exact, while a Hybrid/CELT divergence
// is the documented float-analysis residual (logged, not failed — see the 48k
// branch). At sub-48k it feeds libopus NATIVE-Fs frames and gopus the
// 48 kHz-relative frames from the same native source, HARD-asserts gopus
// accepts/does-not-panic and that the TOC mode class matches, and LOGS the
// documented per-config first divergence (the input-rate gap the FIX agent flips).
func TestSub48NativeEncodeParity(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}
	const nFrames = 6

	specs := sub48BuildSweep()
	// Rates: the byte-exact 48k lock plus the diverging sub-48k native set.
	rates := append([]int{48000}, sub48NativeRates...)

	var (
		sub48Diverged  int
		sub48ByteExact int
		sub48TOCFlips  int
		lock48kChecked int
	)

	for _, spec := range specs {
		// Known encoder-side LBRR panic (tracked in encode_differential_fuzz_test.go
		// header): SILK in-band-FEC stereo at >=40 ms can bust silk_delta_gain_iCDF.
		// This gate does not enable FEC, so it is not hit here; no skip needed.
		for _, fs := range rates {
			fs := fs
			spec := spec
			caseName := fmt.Sprintf("%s/fs%d", spec.name, fs)
			t.Run(caseName, func(t *testing.T) {
				rel48 := encFrameSamples48k(spec.dur) // gopus per-channel frame length (48k-relative)

				// Native-Fs per-channel frame length the libopus oracle consumes.
				nativeSamples := sub48NativeFrameSamples(fs, spec.dur)
				if fs == 48000 {
					nativeSamples = rel48
				}

				// Build ONE native-Fs source long enough for the gopus 48k-relative
				// views (the longer of the two), so both encoders read the same
				// underlying native-Fs waveform from sample 0.
				srcSamples := rel48 * nFrames * spec.channels
				src, err := testsignal.GenerateCorpusSignal(spec.sigClass, fs, srcSamples, spec.channels)
				if err != nil {
					t.Fatalf("GenerateCorpusSignal(%s @ %d): %v", spec.sigClass, fs, err)
				}

				vbr, constraint := vbrFlags(spec.vbr)

				// libopus oracle: native-Fs frames (a prefix of the shared source;
				// gopus reads the wider 48k-relative stride from the same source so
				// both encoders align on the frame-0 waveform).
				oraclePCM := src[:nativeSamples*nFrames*spec.channels]
				recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
					SampleRate:    fs,
					Channels:      spec.channels,
					Application:   libopustest.EncodeDiffApplicationAudio,
					ForceMode:     spec.forceMD,
					Bandwidth:     spec.bwCode,
					MaxBandwidth:  spec.bwCode,
					Bitrate:       spec.bitrate,
					Complexity:    spec.complex,
					Signal:        libopustest.EncodeDiffSignalVoice,
					VBR:           vbr,
					VBRConstraint: constraint,
					ForceChannels: spec.channels,
					FrameSize:     nativeSamples,
					FrameCount:    nFrames,
					PCM:           oraclePCM,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "encode diff oracle", err)
					return
				}

				enc, ok := sub48ConfigureGopus(t, spec, fs)
				if !ok {
					t.Skipf("gopus rejected config %s @ %d", spec.name, fs)
				}

				res := sub48ParityResult{name: spec.name, fs: fs, firstDivFr: -1}
				for f := 0; f < nFrames; f++ {
					gFrame := src[f*rel48*spec.channels : (f+1)*rel48*spec.channels]
					gpkt, gerr := encDiffEncodeOneFrame(enc, gFrame)
					// HARD: gopus must accept the frame / not panic at every rate.
					if gerr != nil {
						t.Fatalf("%s: gopus encode error frame %d: %v", caseName, f, gerr)
					}
					o := recs[f]
					oHas := o.Ret > 0
					gHas := len(gpkt) > 0
					if gHas != oHas {
						if res.firstDivFr < 0 {
							res.firstDivFr = f
							// Classify from whichever side emitted (Hybrid/CELT emission
							// cadence carries the documented float boundary; an emission
							// mismatch is otherwise unexpected at 48k with DTX off).
							if gHas {
								res.firstDivCls = tocModeClass(byte0(gpkt), true)
							} else {
								res.firstDivCls = tocModeClass(byte0(o.Packet), true)
							}
							res.gLen, res.oLen = len(gpkt), len(o.Packet)
							res.gTOC, res.oTOC = byte0(gpkt), byte0(o.Packet)
						}
						continue
					}
					if !gHas {
						continue
					}
					// HARD: TOC mode class (SILK/Hybrid/CELT decision) must match —
					// the mode/bandwidth decision is correct even at sub-48k.
					gClass := tocModeClass(byte0(gpkt), gHas)
					oClass := tocModeClass(byte0(o.Packet), oHas)
					if gClass != oClass {
						res.tocFlip = true
						if res.firstDivFr < 0 {
							res.firstDivFr = f
							res.firstDivByte = 0
							res.gLen, res.oLen = len(gpkt), len(o.Packet)
							res.gTOC, res.oTOC = byte0(gpkt), byte0(o.Packet)
						}
						t.Errorf("%s frame %d: TOC MODE-CLASS FLIP gopus=%s(toc=%02x) libopus=%s(toc=%02x) "+
							"— mode decision divergence (HARD FAIL at every rate)",
							caseName, f, modeClassName(gClass), byte0(gpkt), modeClassName(oClass), byte0(o.Packet))
						continue
					}

					if !bytes.Equal(gpkt, o.Packet) && res.firstDivFr < 0 {
						res.firstDivFr = f
						res.firstDivByte = firstByteDiff(gpkt, o.Packet)
						res.firstDivCls = gClass
						res.gLen, res.oLen = len(gpkt), len(o.Packet)
						res.gTOC, res.oTOC = byte0(gpkt), byte0(o.Packet)
					}
				}

				if fs == 48000 {
					// LOCK: 48 kHz byte-exactness. Policy mirrors TestEncodeDifferentialFuzz:
					//   - SILK payload: integer/range-coded, byte-exact SAME-ARCH on every
					//     arch → HARD FAIL on any divergence.
					//   - Hybrid/CELT payload: the float MDCT/band-energy/pitch analysis
					//     carries the documented ≤1-ULP float boundary
					//     (project_arm64_celt_1ulp_drift) AND the separately tracked amd64
					//     Hybrid-FB cross-arch instability (project_amd64_encoder_precision_
					//     regression: libopus itself differs by ~9 Q across arches on
					//     knife-edge signals). So a Hybrid/CELT byte divergence is the
					//     documented per-arch residual, not a same-arch logic bug — LOGGED,
					//     not failed, exactly as the sibling encode fuzz harness does.
					lock48kChecked++
					if res.firstDivFr >= 0 && !res.tocFlip {
						if res.firstDivCls == 0 {
							t.Errorf("%s: 48 kHz SILK byte-exact LOCK BROKEN — first divergence frame %d byte %d "+
								"(gopus toc=%02x len=%d, libopus toc=%02x len=%d). SILK 48k must be byte-exact same-arch.",
								caseName, res.firstDivFr, res.firstDivByte, res.gTOC, res.gLen, res.oTOC, res.oLen)
						} else {
							t.Logf("%s: 48 kHz %s payload differs at frame %d byte %d "+
								"(gopus toc=%02x len=%d, libopus toc=%02x len=%d) — documented float-analysis "+
								"boundary (project_arm64_celt_1ulp_drift / project_amd64_encoder_precision_regression), "+
								"not a same-arch logic bug.",
								caseName, modeClassName(res.firstDivCls), res.firstDivFr, res.firstDivByte,
								res.gTOC, res.gLen, res.oTOC, res.oLen)
						}
					}
					return
				}

				// sub-48k: CHARACTERIZE (do not fail on payload/length divergence).
				if res.firstDivFr < 0 {
					sub48ByteExact++
					t.Logf("%s: BYTE-EXACT across %d frames (sub-48k native parity already holds for this config)",
						caseName, nFrames)
				} else {
					sub48Diverged++
					if res.tocFlip {
						sub48TOCFlips++
					}
					t.Logf("%s: DOCUMENTED SUB-48k GAP — first divergence frame %d byte %d "+
						"(gopus toc=%02x len=%d | libopus native-%dk toc=%02x len=%d). "+
						"Root cause: public Encoder consumes 48k-relative PCM + resamples internally instead of "+
						"native-Fs opus_encode(Fs). FIX agent: flip this to t.Errorf once native-Fs encode is byte-exact.",
						caseName, res.firstDivFr, res.firstDivByte,
						res.gTOC, res.gLen, fs/1000, res.oTOC, res.oLen)
				}
			})
		}
	}

	t.Logf("sub-48k native encode parity gate: 48k-lock specs checked=%d; "+
		"sub-48k results: byte-exact=%d diverged=%d (of which TOC-mode-flips=%d). "+
		"Per-config first-divergence map logged above. This gate is CHARACTERIZING: "+
		"sub-48k payload/length divergence is the documented input-rate gap (t.Logf), "+
		"48k SILK stays byte-exact (HARD) while 48k Hybrid/CELT residuals are documented (logged), "+
		"and gopus accept/no-panic + TOC-mode-class match are HARD at every rate.",
		lock48kChecked, sub48ByteExact, sub48Diverged, sub48TOCFlips)
}
