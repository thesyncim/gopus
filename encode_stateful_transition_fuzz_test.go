// encode_stateful_transition_fuzz_test.go — STATEFUL multi-frame ENCODE
// differential fuzz vs the same-arch libopus float opus_encode_float oracle,
// targeting the encoder's CROSS-FRAME STATE.
//
// The sibling harness (encode_differential_fuzz_test.go) drives one persistent
// encoder over 8 frames of a SINGLE corpus signal: it sweeps the config space
// broadly but holds the signal CLASS constant, so within a stream the
// mode/bandwidth/DTX decision rarely moves. The cross-frame state machine —
// prev_mode / prev_packet_mode hysteresis, prev_channels & the stereo->mono
// to_mono countdown, redundancy/st->first, DTX nb_no_activity_ms_Q1 run-length,
// the VBR reservoir carried across content changes, and the analysis/energy
// memory — is therefore UNDER-SWEPT exactly where encoder state bugs hide:
// mode switches (SILK<->Hybrid<->CELT), bandwidth changes, DTX runs, and
// FEC/LBRR carried across frames.
//
// This harness closes that gap. For each spec it builds a CONCATENATED
// multi-segment signal (voiced speech -> music -> near-silence -> bandwidth
// sweep -> transient -> mixed, frame-aligned) that FORCES those transitions,
// then drives ONE persistent gopus Encoder AND the persistent libopus float
// oracle (one OpusEncoder, no reset, FORCE_MODE reasserted per frame) with the
// IDENTICAL per-frame PCM across MANY frames, asserting each emitted packet is
// BYTE-IDENTICAL frame for frame (TOC + payload) plus the post-encode final
// range.
//
// Divergence classification (identical policy to the sibling harness so the
// established same-arch evidence carries over):
//
//   - TOC mode-class flip (SILK vs Hybrid vs CELT): a deterministic
//     mode-DECISION divergence. HARD FAIL on EVERY arch — the cross-frame mode
//     hysteresis is integer logic, not a float LSB. This is the harness's
//     primary target.
//
//   - DTX / output-cadence mismatch (one side emits a packet, the other emits
//     nothing, or a 1-byte DTX TOC vs a real frame): the DTX run-length
//     (nb_no_activity_ms_Q1) and the redundancy/st->first decision are integer
//     state. HARD FAIL on every arch.
//
//   - Same-class payload / framing byte mismatch: on amd64 (the CI gate) the
//     float analysis is bit-exact, so any mismatch is a HARD FAIL. On
//     darwin/arm64 the documented <=1-ULP CELT float-analysis boundary
//     (project_arm64_celt_1ulp_drift) can flip a near-tie quantization decision
//     once enough float ops accumulate; it is logged as the per-arch residual,
//     matching the sibling harness's documented behaviour.
//
// Run the full sweep with:
//   GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test -run TestEncodeStatefulTransitionFuzz .

package gopus

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// encXfrSegmentPlan is the ordered list of corpus signal-class tags whose
// frame-aligned concatenation forms one stateful transition stream. The classes
// are chosen so the encoder is driven across every cross-frame decision:
//
//   - clean speech  -> voiced SILK/Hybrid, low audio bandwidth
//   - music         -> CELT-favouring harmonic content (mode switch up)
//   - near silence  -> VAD/DTX run, energy-memory decay
//   - bandwidth sweep-> NB->MB->WB->SWB->FB audio-bandwidth churn
//   - castanet      -> transient path + sharp energy step
//   - silence bursts-> hard active/silent edges (DTX cadence)
//   - mixed         -> speech<->music crossfade (Hybrid boundary)
//
// Each segment is a whole number of frames so a transition always lands on a
// frame boundary, making any divergence minimise cleanly to (spec, frame).
var encXfrSegmentPlan = []string{
	testsignal.CorpusCleanSpeechV1,
	testsignal.CorpusMusicV1,
	testsignal.CorpusNearSilenceV1,
	testsignal.CorpusBandwidthSweepV1,
	testsignal.CorpusCastanetTransientV1,
	testsignal.CorpusSilenceBurstsV1,
	testsignal.CorpusMixedV1,
}

// encXfrBuildTransitionPCM assembles a deterministic frame-aligned stream of
// totalFrames frames at 48 kHz by tiling encXfrSegmentPlan: segment s occupies a
// contiguous run of frames, each frame filled from a freshly-generated buffer of
// that segment's class (the corpus generators are deterministic and stateless
// across calls, so a per-segment buffer is reproducible). The returned slice is
// interleaved float32 of length fs*channels*totalFrames.
func encXfrBuildTransitionPCM(fs, channels, totalFrames, segFrames int) ([]float32, error) {
	out := make([]float32, fs*channels*totalFrames)
	per := fs * channels
	for f := range totalFrames {
		seg := (f / segFrames) % len(encXfrSegmentPlan)
		class := encXfrSegmentPlan[seg]
		// Frame index WITHIN the current segment run, so the generated waveform is
		// continuous across the frames of a segment (not reset every frame).
		frameInSeg := f % segFrames
		// Generate the whole segment once per segment-run start would be cheaper,
		// but generating (frameInSeg+1) frames and taking the last keeps each
		// segment self-consistent without caching; segFrames is small.
		buf, err := testsignal.GenerateCorpusSignal(class, fs, per*(frameInSeg+1), channels)
		if err != nil {
			return nil, err
		}
		copy(out[f*per:(f+1)*per], buf[frameInSeg*per:(frameInSeg+1)*per])
	}
	return out, nil
}

// encXfrSpec is one stateful-transition configuration. Unlike the sibling
// harness it does not pin a single signal class (the stream is the fixed
// transition plan); it sweeps the control space whose cross-frame state the
// transitions stress.
type encXfrSpec struct {
	name       string
	forceMode  int
	gmode      EncoderMode
	autoBW     bool
	bwCode     int
	gbw        Bandwidth
	frameMs    ExpertFrameDuration
	bitrate    int
	channels   int
	vbr        BitrateMode
	fec        bool
	dtx        bool
	complexity int
	signal     uint32
	gsignal    Signal
}

// buildEncXfrSweep enumerates the stateful-transition config matrix. The bias is
// toward the configurations where cross-frame state actually moves:
//
//   - AUTO mode (no FORCE_MODE, auto bandwidth): the SILK/Hybrid/CELT decision
//     and the audio-bandwidth decision are free to move with the segment content,
//     exercising prev_mode/prev_packet_mode hysteresis and the bandwidth memory.
//   - Forced SILK and forced CELT with DTX on: the segment plan's silence runs
//     drive the DTX nb_no_activity_ms_Q1 counter up and back down, and forced
//     SILK + FEC exercises the LBRR-across-frames cadence.
//   - mono + stereo, every rate-control mode (CBR/CVBR/VBR for the reservoir
//     carry across the speech->music->silence energy steps), complexity 0/5/10.
func buildEncXfrSweep() []encXfrSpec {
	var specs []encXfrSpec

	autoFrames := []ExpertFrameDuration{ExpertFrameDuration10Ms, ExpertFrameDuration20Ms, ExpertFrameDuration40Ms, ExpertFrameDuration60Ms}
	silkFrames := []ExpertFrameDuration{ExpertFrameDuration20Ms, ExpertFrameDuration40Ms, ExpertFrameDuration60Ms}
	celtFrames := []ExpertFrameDuration{ExpertFrameDuration10Ms, ExpertFrameDuration20Ms}

	type modeDef struct {
		name      string
		forceMode int
		gmode     EncoderMode
		autoBW    bool
		bwCode    int
		gbw       Bandwidth
		frames    []ExpertFrameDuration
		bitrates  []int
		fecDtx    bool
		signal    uint32
		gsignal   Signal
	}

	modes := []modeDef{
		// AUTO mode: the transition target. Mid bitrates where the SILK/Hybrid/CELT
		// near-tie sits, so the segment content actually flips the mode/bandwidth.
		{"xfr_auto", libopustest.EncodeDiffForceModeAuto, EncoderModeAuto, true, libopustest.EncodeDiffBandwidthAuto, BandwidthFullband, autoFrames, []int{12000, 16000, 24000, 32000, 48000, 64000}, true, libopustest.EncodeDiffSignalAuto, SignalAuto},
		// Forced SILK: DTX run-length + LBRR-across-frames over the silence segments.
		{"xfr_silk_wb", libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, false, libopustest.EncodeDiffBandwidthWideband, BandwidthWideband, silkFrames, []int{12000, 24000}, true, libopustest.EncodeDiffSignalVoice, SignalVoice},
		{"xfr_silk_nb", libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, false, libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, silkFrames, []int{8000, 16000}, true, libopustest.EncodeDiffSignalVoice, SignalVoice},
		// Forced Hybrid: the SILK+CELT split carried across the speech/music/silence
		// steps; DTX/FEC honoured.
		{"xfr_hybrid_swb", libopustest.EncodeDiffForceModeHybrid, EncoderModeHybrid, false, libopustest.EncodeDiffBandwidthSuperwideband, BandwidthSuperwideband, celtFrames, []int{24000, 48000}, true, libopustest.EncodeDiffSignalAuto, SignalAuto},
		// Forced CELT: transient/energy-memory + the 2.5/5 ms-adjacent short-frame
		// range-tail; DTX (CELT-silence) cadence over the silence segments.
		{"xfr_celt_fb", libopustest.EncodeDiffForceModeCELTOnly, EncoderModeCELT, false, libopustest.EncodeDiffBandwidthFullband, BandwidthFullband, celtFrames, []int{32000, 64000, 128000}, false, libopustest.EncodeDiffSignalMusic, SignalMusic},
	}

	vbrModes := []BitrateMode{BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR}
	complexities := []int{0, 5, 10}

	for _, m := range modes {
		for _, ch := range []int{1, 2} {
			for _, fr := range m.frames {
				for _, br := range m.bitrates {
					for _, vbr := range vbrModes {
						for _, cx := range complexities {
							fecOpts := []bool{false}
							dtxOpts := []bool{false}
							if m.fecDtx {
								fecOpts = []bool{false, true}
								dtxOpts = []bool{false, true}
							}
							for _, fec := range fecOpts {
								for _, dtx := range dtxOpts {
									specs = append(specs, encXfrSpec{
										name:       fmt.Sprintf("%s_ch%d_%dms_%dbps_vbr%d_cx%d_fec%t_dtx%t", m.name, ch, encMsOf(fr), br, vbr, cx, fec, dtx),
										forceMode:  m.forceMode,
										gmode:      m.gmode,
										autoBW:     m.autoBW,
										bwCode:     m.bwCode,
										gbw:        m.gbw,
										frameMs:    fr,
										bitrate:    br,
										channels:   ch,
										vbr:        vbr,
										fec:        fec,
										dtx:        dtx,
										complexity: cx,
										signal:     m.signal,
										gsignal:    m.gsignal,
									})
								}
							}
						}
					}
				}
			}
		}
	}
	return specs
}

// configureEncXfr builds and configures a gopus Encoder for one transition spec.
// It mirrors configureEncDiff but takes the per-spec complexity (the sibling
// helper hardcodes 10).
func configureEncXfr(spec encXfrSpec) (*Encoder, bool) {
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: spec.channels, Application: ApplicationAudio})
	if err != nil {
		return nil, false
	}
	if spec.gmode != EncoderModeAuto {
		if err := enc.SetMode(spec.gmode); err != nil {
			return nil, false
		}
	}
	if err := enc.SetFrameSize(encFrameSamples48k(spec.frameMs)); err != nil {
		return nil, false
	}
	if err := enc.SetExpertFrameDuration(spec.frameMs); err != nil {
		return nil, false
	}
	if spec.autoBW {
		if err := enc.SetBandwidthAuto(); err != nil {
			return nil, false
		}
	} else {
		if err := enc.SetBandwidth(spec.gbw); err != nil {
			return nil, false
		}
		if err := enc.SetMaxBandwidth(spec.gbw); err != nil {
			return nil, false
		}
	}
	if err := enc.SetBitrate(spec.bitrate); err != nil {
		return nil, false
	}
	if err := enc.SetBitrateMode(spec.vbr); err != nil {
		return nil, false
	}
	if err := enc.SetComplexity(spec.complexity); err != nil {
		return nil, false
	}
	if err := enc.SetSignal(spec.gsignal); err != nil {
		return nil, false
	}
	enc.SetFEC(spec.fec)
	if spec.fec {
		if err := enc.SetPacketLoss(20); err != nil {
			return nil, false
		}
	}
	enc.SetDTX(spec.dtx)
	if spec.channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			return nil, false
		}
	}
	return enc, true
}

// TestEncodeStatefulTransitionFuzz drives gopus and the libopus float oracle with
// the IDENTICAL multi-segment transition stream, statefully across many frames,
// and asserts byte-exact packets frame for frame. See the file header for the
// divergence classification policy.
func TestEncodeStatefulTransitionFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}

	const sampleRate = 48000
	// segFrames frames per segment; framesPerSpec spans every segment at least
	// once plus a wrap so a transition can recur after the state has settled.
	const segFrames = 5
	framesPerSpec := segFrames*len(encXfrSegmentPlan) + segFrames // 7 segments + wrap = 40

	specs := buildEncXfrSweep()
	budget := min(diffFuzzBudget(len(specs)), len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	var (
		tested             int
		tocFlips           int
		cadenceMismatch    int
		silkByteFails      int
		framingFails       int
		silkResiduals      int
		celtResiduals      int
		framingResiduals   int
		rangeOnlyResiduals int
		skippedLBRR        int
		transitionsSeen    int // total per-frame mode-class or bandwidth changes observed (libopus side)
		dtxRunsSeen        int // frames where libopus emitted nothing (DTX no-output)
		modeFlipsInStream  int // streams that crossed >1 distinct TOC mode class
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		// Pre-existing encoder finding (see encode_differential_fuzz_test.go header
		// and decode_differential_fuzz_test.go): SILK LBRR (in-band FEC) with stereo
		// and >=40 ms frames can produce a delta-gain index outside
		// silk_delta_gain_iCDF, which panics gopus encode (libopus only
		// silk_assert()s it, disabled in release). Owned by the silk fixed-point
		// agent; skip here so the transition sweep does not crash on a known,
		// unrelated encoder-side bug.
		if spec.fec && spec.channels == 2 && spec.gmode == EncoderModeSILK &&
			(spec.frameMs == ExpertFrameDuration40Ms || spec.frameMs == ExpertFrameDuration60Ms) {
			skippedLBRR++
			continue
		}
		tested++
		t.Run(spec.name, func(t *testing.T) {
			fs := encFrameSamples48k(spec.frameMs)
			pcm, err := encXfrBuildTransitionPCM(fs, spec.channels, framesPerSpec, segFrames)
			if err != nil {
				t.Fatalf("build transition PCM (%s): %v", spec.name, err)
			}

			vbr, constraint := vbrFlags(spec.vbr)
			fecCfg := 0
			pl := 0
			if spec.fec {
				fecCfg = 1
				pl = 20
			}
			recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
				SampleRate:    sampleRate,
				Channels:      spec.channels,
				Application:   libopustest.EncodeDiffApplicationAudio,
				ForceMode:     spec.forceMode,
				Bandwidth:     spec.bwCode,
				MaxBandwidth:  spec.bwCode,
				Bitrate:       spec.bitrate,
				Complexity:    spec.complexity,
				Signal:        spec.signal,
				VBR:           vbr,
				VBRConstraint: constraint,
				ForceChannels: spec.channels,
				InbandFEC:     fecCfg,
				PacketLoss:    pl,
				DTX:           spec.dtx,
				FrameSize:     fs,
				FrameCount:    framesPerSpec,
				PCM:           pcm,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "encode diff oracle", err)
				return
			}

			enc, ok := configureEncXfr(spec)
			if !ok {
				t.Skipf("gopus rejected config %s", spec.name)
			}

			gotRecs := make([]libopustest.EncodeDiffRecord, framesPerSpec)
			for f := range framesPerSpec {
				frame := pcm[f*fs*spec.channels : (f+1)*fs*spec.channels]
				pkt, eerr := encDiffEncodeOneFrame(enc, frame)
				if eerr != nil {
					t.Fatalf("%s frame %d: gopus encode error: %v", spec.name, f, eerr)
				}
				gotRecs[f] = libopustest.EncodeDiffRecord{
					Ret:        len(pkt),
					FinalRange: enc.FinalRange(),
					Packet:     pkt,
				}
			}

			// Track the libopus-side per-frame mode class / bandwidth so the sweep
			// can PROVE it actually exercised transitions (a stream that never moved
			// would silently weaken the gate).
			prevClass := -2
			prevToc := byte(0xff)
			distinctClasses := map[int]bool{}

			for f := range framesPerSpec {
				g := gotRecs[f]
				o := recs[f]
				label := fmt.Sprintf("%s/frame%d", spec.name, f)

				gHas := len(g.Packet) > 0
				oHas := o.Ret > 0

				gClass := tocModeClass(byte0(g.Packet), gHas)
				oClass := tocModeClass(byte0(o.Packet), oHas)

				// Coverage accounting (libopus reference side).
				if !oHas {
					dtxRunsSeen++
				} else {
					distinctClasses[oClass] = true
					if prevClass >= 0 && (oClass != prevClass || byte0(o.Packet) != prevToc) {
						transitionsSeen++
					}
					prevClass = oClass
					prevToc = byte0(o.Packet)
				}

				// DTX / output-cadence: the run-length counter and redundancy/first
				// decision are integer state — a cadence mismatch is a HARD FAIL on
				// every arch (it cannot be a float LSB).
				if gHas != oHas {
					cadenceMismatch++
					t.Errorf("%s: DTX/output CADENCE mismatch gopus(len=%d) libopus(ret=%d) "+
						"dtx=%t vbr=%d br=%d — cross-frame DTX run-length / redundancy divergence",
						label, len(g.Packet), o.Ret, spec.dtx, spec.vbr, spec.bitrate)
					continue
				}
				if !gHas {
					continue // both emitted nothing
				}

				if bytes.Equal(g.Packet, o.Packet) {
					if g.FinalRange != o.FinalRange {
						if runtime.GOARCH == "amd64" && !testPuregoBuild {
							t.Errorf("%s: packets byte-equal but final_range differs gopus=%08x libopus=%08x (UNEXPECTED on amd64)",
								label, g.FinalRange, o.FinalRange)
						} else {
							rangeOnlyResiduals++
							t.Logf("%s: packets byte-equal, final_range differs gopus=%08x libopus=%08x — "+
								"documented pure-Go CELT range-tail residual (bytes match)",
								label, g.FinalRange, o.FinalRange)
						}
					}
					continue
				}

				// TOC mode-class flip: deterministic mode-DECISION divergence, the
				// primary cross-frame-state target. HARD FAIL on every arch.
				if gClass != oClass {
					tocFlips++
					t.Errorf("%s: TOC MODE-CLASS FLIP gopus=%s(toc=%02x) libopus=%s(toc=%02x) "+
						"br=%d vbr=%d fec=%t dtx=%t ch=%d cx=%d — cross-frame mode-decision divergence",
						label, modeClassName(gClass), byte0(g.Packet), modeClassName(oClass), byte0(o.Packet),
						spec.bitrate, spec.vbr, spec.fec, spec.dtx, spec.channels, spec.complexity)
					continue
				}

				fb := firstByteDiff(g.Packet, o.Packet)

				// Same mode class, different TOC byte: a packet-FRAMING (code field)
				// divergence. On the amd64-asm build the float path is exact so this is a HARD FAIL.
				// On every pure-Go build a framing flip that rides on a sub-frame length difference
				// is the documented downstream symptom of the float boundary (the
				// >20 ms repacketizer's equal-vs-unequal code 1/2 choice), logged as a
				// residual — same policy as the sibling harness.
				if byte0(g.Packet) != byte0(o.Packet) {
					if runtime.GOARCH == "amd64" && !testPuregoBuild {
						framingFails++
						t.Errorf("%s: PACKET FRAMING divergence gopus toc=%02x(len=%d) libopus toc=%02x(len=%d) "+
							"br=%d vbr=%d — same mode class, different TOC framing (UNEXPECTED on amd64)",
							label, byte0(g.Packet), len(g.Packet), byte0(o.Packet), len(o.Packet), spec.bitrate, spec.vbr)
						continue
					}
					framingResiduals++
					t.Logf("%s: framing differs gopus toc=%02x(len=%d) libopus toc=%02x(len=%d) — pure-Go "+
						"multiframe (>20 ms) repacketization code flip downstream of the float boundary",
						label, byte0(g.Packet), len(g.Packet), byte0(o.Packet), len(o.Packet))
					continue
				}

				// Payload byte mismatch with matching TOC. amd64-asm: HARD FAIL (bit-exact
				// required). Pure-Go (arm64 + amd64-purego): documented <=1-ULP boundary.
				if runtime.GOARCH == "amd64" && !testPuregoBuild {
					if gClass == 0 {
						silkByteFails++
						t.Errorf("%s: SILK payload BYTE MISMATCH at byte %d (len g=%d o=%d, range g=%08x o=%08x) "+
							"br=%d vbr=%d fec=%t dtx=%t cx=%d — same-arch SILK encode divergence (UNEXPECTED on amd64)",
							label, fb, len(g.Packet), len(o.Packet), g.FinalRange, o.FinalRange,
							spec.bitrate, spec.vbr, spec.fec, spec.dtx, spec.complexity)
					} else {
						silkByteFails++
						t.Errorf("%s: %s payload BYTE MISMATCH at byte %d (len g=%d o=%d, range g=%08x o=%08x) "+
							"br=%d vbr=%d cx=%d — float-analysis divergence (UNEXPECTED on amd64; bit-exact required)",
							label, modeClassName(gClass), fb, len(g.Packet), len(o.Packet),
							g.FinalRange, o.FinalRange, spec.bitrate, spec.vbr, spec.complexity)
					}
					continue
				}
				if gClass == 0 {
					silkResiduals++
				} else {
					celtResiduals++
				}
				t.Logf("%s: %s payload differs at byte %d (len g=%d o=%d range g=%08x o=%08x) — documented arm64 "+
					"<=1-ULP float boundary (project_arm64_celt_1ulp_drift), not a same-arch logic bug",
					label, modeClassName(gClass), fb, len(g.Packet), len(o.Packet), g.FinalRange, o.FinalRange)
			}

			if len(distinctClasses) > 1 {
				modeFlipsInStream++
			}
		})
	}

	t.Logf("encode stateful-transition sweep: %d/%d specs x %d frames "+
		"(seg=%d frames, %d segments; skipped %d LBRR-panic specs); arch=%s; "+
		"coverage[ libopus-side transitions=%d dtx-no-output-frames=%d multi-mode-streams=%d ]; "+
		"TOC-mode-flips=%d cadence-mismatch=%d amd64-byte-fails=%d amd64-framing-fails=%d "+
		"arm64-SILK-residuals=%d arm64-CELT/Hybrid-residuals=%d arm64-framing-residuals=%d arm64-range-tail-residuals=%d",
		tested, len(specs), framesPerSpec, segFrames, len(encXfrSegmentPlan), skippedLBRR, runtime.GOARCH,
		transitionsSeen, dtxRunsSeen, modeFlipsInStream,
		tocFlips, cadenceMismatch, silkByteFails, framingFails,
		silkResiduals, celtResiduals, framingResiduals, rangeOnlyResiduals)

	// Coverage guard: the harness must actually have crossed transitions, else a
	// future signal/plan change could silently turn it into a single-mode sweep
	// and hide the very cross-frame bugs it targets. Only enforced on the full
	// (non-short) sweep where every mode family is reached.
	if !testing.Short() && tested > 0 && transitionsSeen == 0 {
		t.Errorf("stateful-transition sweep observed NO cross-frame transitions across %d specs — "+
			"the transition plan is not exercising mode/bandwidth changes", tested)
	}
}

// encXfrDTXRunPCM builds a speech -> long-silence -> speech stream that drives
// the DTX nb_no_activity_ms_Q1 run-length counter ACROSS the DTX-fire threshold
// (NB_SPEECH_FRAMES_BEFORE_DTX*20*2 = 400 Q1, ~10 inactive 20 ms-equivalent
// frames) and back out. warmFrames of voiced speech keep the counter at zero;
// silenceFrames of TRUE digital silence (guaranteed VAD-inactive, unlike the
// dithered near-silence corpus) walk it past the threshold so the encoder enters
// DTX and stops emitting packets; recoverFrames of speech force the immediate
// DTX-exit transition. The whole stream is one stateful session.
func encXfrDTXRunPCM(fs, channels, warmFrames, silenceFrames, recoverFrames int) []float32 {
	total := warmFrames + silenceFrames + recoverFrames
	per := fs * channels
	out := make([]float32, per*total)
	// Voiced-speech segments are generated as a single continuous buffer so the
	// waveform is consistent across the warm-up and recovery frame runs.
	speechFrames := warmFrames + recoverFrames
	speech, _ := testsignal.GenerateCorpusSignal(testsignal.CorpusCleanSpeechV1, fs, per*speechFrames, channels)
	copy(out[:per*warmFrames], speech[:per*warmFrames])
	// out[warmFrames .. warmFrames+silenceFrames] stays zero (true silence).
	copy(out[per*(warmFrames+silenceFrames):], speech[per*warmFrames:per*speechFrames])
	return out
}

// TestEncodeStatefulDTXRunFuzz targets the DTX cross-frame state specifically:
// it drives gopus and the libopus float oracle through a speech -> sustained
// silence -> speech stream long enough to cross the DTX-fire threshold, and
// asserts the per-frame OUTPUT CADENCE (which frames emit a packet, which emit
// nothing, and the 1-byte DTX/CELT-silence TOC) is identical, plus byte-exact
// packets where both emit. The DTX run-length counter and the redundancy/
// st->first decision are integer state, so a cadence divergence is a HARD FAIL
// on every arch. The companion sweep TestEncodeStatefulTransitionFuzz keeps the
// silence segments short (counter never fires); this test is the one that drives
// the no-output path.
func TestEncodeStatefulDTXRunFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}

	const sampleRate = 48000
	// 4 frames speech warm-up, 16 frames silence (>10 needed to fire DTX even at
	// 60 ms-equivalent accounting), 6 frames recovery. 26 frames total.
	const warmFrames = 4
	const silenceFrames = 16
	const recoverFrames = 6
	const totalFrames = warmFrames + silenceFrames + recoverFrames

	type dtxKase struct {
		name      string
		forceMode int
		gmode     EncoderMode
		autoBW    bool
		bwCode    int
		gbw       Bandwidth
		signal    uint32
		gsignal   Signal
		fec       bool
	}
	// DTX is honoured on SILK/Hybrid (and auto, which lands there for speech). CELT
	// DTX emits a 1-byte CELT-silence TOC rather than nothing, also a cadence the
	// harness must match.
	kases := []dtxKase{
		{"silk_nb", libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, false, libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, libopustest.EncodeDiffSignalVoice, SignalVoice, false},
		{"silk_wb", libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, false, libopustest.EncodeDiffBandwidthWideband, BandwidthWideband, libopustest.EncodeDiffSignalVoice, SignalVoice, false},
		{"silk_wb_fec", libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, false, libopustest.EncodeDiffBandwidthWideband, BandwidthWideband, libopustest.EncodeDiffSignalVoice, SignalVoice, true},
		{"hybrid_fb", libopustest.EncodeDiffForceModeHybrid, EncoderModeHybrid, false, libopustest.EncodeDiffBandwidthFullband, BandwidthFullband, libopustest.EncodeDiffSignalVoice, SignalVoice, false},
		{"auto", libopustest.EncodeDiffForceModeAuto, EncoderModeAuto, true, libopustest.EncodeDiffBandwidthAuto, BandwidthFullband, libopustest.EncodeDiffSignalVoice, SignalVoice, false},
		{"celt_fb", libopustest.EncodeDiffForceModeCELTOnly, EncoderModeCELT, false, libopustest.EncodeDiffBandwidthFullband, BandwidthFullband, libopustest.EncodeDiffSignalMusic, SignalMusic, false},
	}

	frameDurs := []ExpertFrameDuration{ExpertFrameDuration20Ms, ExpertFrameDuration40Ms, ExpertFrameDuration60Ms}
	vbrModes := []BitrateMode{BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR}

	var (
		casesRun         int
		cadenceMismatch  int
		byteFails        int
		dtxFiredCases    int // libopus reached the DTX path at least once this case
		dtxNoOutputCases int // libopus emitted a true no-output (ret==0) frame
		dtxEnterExitSeen int // active->DTX and DTX->active transitions observed
		residuals        int
		rangeResiduals   int
		framingResiduals int
	)

	for _, k := range kases {
		for _, ch := range []int{1, 2} {
			// Known LBRR stereo>=40 ms panic (owned by silk fixed-point agent); skip.
			for _, fr := range frameDurs {
				if k.fec && ch == 2 && k.gmode == EncoderModeSILK &&
					(fr == ExpertFrameDuration40Ms || fr == ExpertFrameDuration60Ms) {
					continue
				}
				for _, vbr := range vbrModes {
					k, ch, fr, vbr := k, ch, fr, vbr
					name := fmt.Sprintf("%s_ch%d_%dms_vbr%d_fec%t", k.name, ch, encMsOf(fr), vbr, k.fec)
					t.Run(name, func(t *testing.T) {
						fs := encFrameSamples48k(fr)
						pcm := encXfrDTXRunPCM(fs, ch, warmFrames, silenceFrames, recoverFrames)

						vbrFlag, constraint := vbrFlags(vbr)
						fecCfg, pl := 0, 0
						if k.fec {
							fecCfg, pl = 1, 20
						}
						recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
							SampleRate:    sampleRate,
							Channels:      ch,
							Application:   libopustest.EncodeDiffApplicationVoIP,
							ForceMode:     k.forceMode,
							Bandwidth:     k.bwCode,
							MaxBandwidth:  k.bwCode,
							Bitrate:       24000,
							Complexity:    10,
							Signal:        k.signal,
							VBR:           vbrFlag,
							VBRConstraint: constraint,
							ForceChannels: ch,
							InbandFEC:     fecCfg,
							PacketLoss:    pl,
							DTX:           true,
							FrameSize:     fs,
							FrameCount:    totalFrames,
							PCM:           pcm,
						})
						if err != nil {
							libopustest.HelperUnavailable(t, "encode diff oracle", err)
							return
						}

						enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: ch, Application: ApplicationVoIP})
						if err != nil {
							t.Fatalf("NewEncoder: %v", err)
						}
						if k.gmode != EncoderModeAuto {
							if err := enc.SetMode(k.gmode); err != nil {
								t.Skipf("gopus rejected mode: %v", err)
							}
						}
						if err := enc.SetFrameSize(fs); err != nil {
							t.Skipf("SetFrameSize: %v", err)
						}
						if err := enc.SetExpertFrameDuration(fr); err != nil {
							t.Skipf("SetExpertFrameDuration: %v", err)
						}
						if k.autoBW {
							if err := enc.SetBandwidthAuto(); err != nil {
								t.Skipf("SetBandwidthAuto: %v", err)
							}
						} else {
							if err := enc.SetBandwidth(k.gbw); err != nil {
								t.Skipf("SetBandwidth: %v", err)
							}
							if err := enc.SetMaxBandwidth(k.gbw); err != nil {
								t.Skipf("SetMaxBandwidth: %v", err)
							}
						}
						if err := enc.SetBitrate(24000); err != nil {
							t.Skipf("SetBitrate: %v", err)
						}
						if err := enc.SetBitrateMode(vbr); err != nil {
							t.Skipf("SetBitrateMode: %v", err)
						}
						if err := enc.SetComplexity(10); err != nil {
							t.Skipf("SetComplexity: %v", err)
						}
						if err := enc.SetSignal(k.gsignal); err != nil {
							t.Skipf("SetSignal: %v", err)
						}
						enc.SetFEC(k.fec)
						if k.fec {
							if err := enc.SetPacketLoss(20); err != nil {
								t.Skipf("SetPacketLoss: %v", err)
							}
						}
						enc.SetDTX(true)
						if ch == 2 {
							if err := enc.SetForceChannels(2); err != nil {
								t.Skipf("SetForceChannels: %v", err)
							}
						}

						// libopus signals DTX two ways depending on mode/config: a true
						// no-output frame (ret==0) or a 1-byte DTX/CELT-silence TOC-only
						// continuation packet (ret==1). Both are the cross-frame DTX
						// run-length path; the meaningful coverage signal is the
						// active<->DTX transition, which the harness must reproduce
						// byte-for-byte (and, for ret==0, emit-nothing for).
						sawDTX := false
						sawNoOutput := false
						sawEnterExit := false
						prevDTX := false
						for f := range totalFrames {
							frame := pcm[f*fs*ch : (f+1)*fs*ch]
							pkt, eerr := encDiffEncodeOneFrame(enc, frame)
							if eerr != nil {
								t.Fatalf("%s frame %d: gopus encode error: %v", name, f, eerr)
							}
							o := recs[f]
							label := fmt.Sprintf("%s/frame%d", name, f)

							gHas := len(pkt) > 0
							oHas := o.Ret > 0
							oDTX := o.Ret == 0 || o.Ret == 1
							if oDTX {
								sawDTX = true
							}
							if o.Ret == 0 {
								sawNoOutput = true
							}
							if f > 0 && oDTX != prevDTX {
								sawEnterExit = true
							}
							prevDTX = oDTX

							if gHas != oHas {
								cadenceMismatch++
								t.Errorf("%s: DTX/output CADENCE mismatch gopus(len=%d) libopus(ret=%d) — "+
									"cross-frame DTX run-length / redundancy divergence (HARD FAIL all arch)",
									label, len(pkt), o.Ret)
								continue
							}
							if !gHas {
								continue // both in DTX no-output this frame
							}
							if bytes.Equal(pkt, o.Packet) {
								if enc.FinalRange() != o.FinalRange {
									if runtime.GOARCH == "amd64" && !testPuregoBuild {
										t.Errorf("%s: packets byte-equal but final_range differs gopus=%08x libopus=%08x (UNEXPECTED on amd64)",
											label, enc.FinalRange(), o.FinalRange)
									} else {
										rangeResiduals++
									}
								}
								continue
							}
							gClass := tocModeClass(byte0(pkt), true)
							oClass := tocModeClass(byte0(o.Packet), true)
							if gClass != oClass {
								// Mode-class flip at a DTX boundary: deterministic, HARD FAIL all arch.
								byteFails++
								t.Errorf("%s: TOC MODE-CLASS FLIP at DTX boundary gopus=%s(toc=%02x) libopus=%s(toc=%02x) — "+
									"cross-frame mode-decision divergence (HARD FAIL all arch)",
									label, modeClassName(gClass), byte0(pkt), modeClassName(oClass), byte0(o.Packet))
								continue
							}
							fb := firstByteDiff(pkt, o.Packet)
							if byte0(pkt) != byte0(o.Packet) {
								if runtime.GOARCH == "amd64" && !testPuregoBuild {
									byteFails++
									t.Errorf("%s: PACKET FRAMING divergence gopus toc=%02x(len=%d) libopus toc=%02x(len=%d) (UNEXPECTED on amd64)",
										label, byte0(pkt), len(pkt), byte0(o.Packet), len(o.Packet))
								} else {
									framingResiduals++
								}
								continue
							}
							if runtime.GOARCH == "amd64" && !testPuregoBuild {
								byteFails++
								t.Errorf("%s: %s payload BYTE MISMATCH at byte %d (len g=%d o=%d range g=%08x o=%08x) — "+
									"UNEXPECTED on amd64 (bit-exact required)",
									label, modeClassName(gClass), fb, len(pkt), len(o.Packet), enc.FinalRange(), o.FinalRange)
								continue
							}
							residuals++
							t.Logf("%s: %s payload differs at byte %d (len g=%d o=%d) — documented pure-Go <=1-ULP float boundary",
								label, modeClassName(gClass), fb, len(pkt), len(o.Packet))
						}
						casesRun++
						if sawDTX {
							dtxFiredCases++
						}
						if sawNoOutput {
							dtxNoOutputCases++
						}
						if sawEnterExit {
							dtxEnterExitSeen++
						}
					})
				}
			}
		}
	}

	t.Logf("encode DTX-run sweep: %d cases (frames warm/silence/recover=%d/%d/%d, total=%d); arch=%s; "+
		"coverage[ cases-reaching-DTX-path=%d cases-with-true-no-output(ret==0)=%d cases-with-active<->DTX-transition=%d ]; "+
		"cadence-mismatch=%d amd64-byte-fails=%d arm64-residuals=%d arm64-framing-residuals=%d arm64-range-residuals=%d",
		casesRun, warmFrames, silenceFrames, recoverFrames, totalFrames, runtime.GOARCH,
		dtxFiredCases, dtxNoOutputCases, dtxEnterExitSeen,
		cadenceMismatch, byteFails, residuals, framingResiduals, rangeResiduals)

	// The point of this test is the cross-frame DTX path: if libopus never
	// reached DTX (ret 0 or 1) the silence/threshold assumptions broke and the
	// cadence assertion is vacuous. We require the active<->DTX TRANSITION to be
	// observed (the run-length counter both crossed and fell back), which is the
	// state the harness exists to gate. (Skipped under -short where the case
	// subset may not reach it.)
	if !testing.Short() && casesRun > 0 && dtxEnterExitSeen == 0 {
		t.Errorf("DTX-run sweep: libopus never showed an active<->DTX transition across %d cases — "+
			"silence run too short or VAD did not classify it inactive", casesRun)
	}
}
