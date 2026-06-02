//go:build gopus_libopus_oracle

// encode_decode_longstream_soak_test.go — LONG-STREAM SOAK differential parity
// vs the SAME-ARCH libopus, targeting CROSS-FRAME STATE DRIFT that the existing
// short (<=40-frame) differential fuzzers cannot reach.
//
// Why a separate soak: TestEncodeDifferentialFuzz / TestDecodeDifferential* drive
// only a handful of frames per config. A divergence that accumulates slowly —
// energy/analysis memory, the VBR bit reservoir, prev-frame LPC/pitch predictors,
// the PLC loss_duration counter, redundancy/DTX run counters, or a range-coder
// carry that only desyncs after thousands of renormalisations — stays invisible
// at 8 frames. This harness drives ONE persistent gopus encoder/decoder and the
// SAME persistent libopus encoder/decoder over a LONG (hundreds to thousands of
// 20 ms frames = tens of seconds) varied signal and asserts byte/sample parity at
// EVERY frame, recording the FIRST divergence index. A late first divergence is
// the signature of slow state drift; frame 0 would be an ordinary single-frame
// bug already caught elsewhere.
//
// Three phases per config (all share the same persistent-state contract):
//
//   (1) ENCODE soak: feed identical float PCM to the gopus Encoder and the
//       persistent libopus opus_encode_float oracle; assert every emitted packet
//       is byte-identical and the post-encode final_range matches. amd64 is the
//       bit-exact CI gate; the documented darwin/arm64 <=1-ULP CELT/Hybrid float
//       boundary (project_arm64_celt_1ulp_drift) is logged, not failed, on arm64.
//
//   (2) DECODE soak of the gopus-encoded stream: decode the WHOLE gopus packet
//       stream through one persistent gopus Decoder AND one persistent libopus
//       decoder (libopus_refdecode_single.c v6, which also reports per-frame
//       OPUS_GET_FINAL_RANGE); assert per-frame PCM parity and decoder final_range
//       parity. This locks decoder cross-frame state (CELT overlap/energy memory,
//       SILK LPC/LTP history, PLC loss_duration, stereo predictor) over the run.
//
//   (3) DECODE soak of a libopus-encoded stream: the same persistent-decoder PCM
//       comparison, but on the libopus-encoded packets (phase 1's oracle output),
//       so the gopus decoder is exercised on a bitstream it did not produce.
//
// The build tag matches decoder_api_rate_test.go because the persistent-decoder
// oracle wrapper (decodeWithLibopusReferenceAPIRateFloat32StepsRanges) lives
// there. The encode helpers (configureEncDiff, encDiffEncodeOneFrame) are reused
// from the untagged encode_differential_fuzz_test.go.
//
// Run the full sweep with:
//   GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
//     go test -tags gopus_libopus_oracle -timeout 3600s -run TestEncodeDecodeLongStreamSoak .

package gopus

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// soakResidualLogCap bounds the per-frame arm64 <=1-ULP residual log lines per
// phase so a thousands-frame soak does not flood the output; the aggregate
// residual count and the first-divergence index carry the full signal.
const soakResidualLogCap = 5

// soakFrames returns the per-config frame count: a long stream by default
// (cross-frame drift needs depth), shrunk under -short so CI stays fast. At
// 20 ms/frame, 2500 frames = 50 s of audio; 300 frames = 6 s.
func soakFrames() int {
	if testing.Short() {
		return 300
	}
	return 2500
}

// soakSpec is one persistent-stream config. The matrix is deliberately small and
// representative (not the full TestEncodeDifferentialFuzz cross-product): each
// spec runs thousands of frames, so breadth is traded for DEPTH. Coverage spans
// CELT / SILK / Hybrid x mono / stereo x CBR / CVBR / VBR x FEC / DTX, all at
// 48 kHz 20 ms (the canonical Opus frame), each paired with a content class that
// keeps the encoder state moving (voiced/unvoiced transitions, transients, energy
// ramps) rather than settling into a steady state that would hide drift.
type soakSpec struct {
	name     string
	enc      encDiffSpec
	sigClass string
}

// buildSoakSweep enumerates the representative long-stream configs.
func buildSoakSweep() []soakSpec {
	const ms20 = ExpertFrameDuration20Ms
	mk := func(name string, forceMode int, gmode EncoderMode, bwCode int, gbw Bandwidth, autoBW bool,
		bitrate, channels int, vbr BitrateMode, fec, dtx bool, signal uint32, gsignal Signal, sigClass string) soakSpec {
		return soakSpec{
			name:     name,
			sigClass: sigClass,
			enc: encDiffSpec{
				name:      name,
				forceMode: forceMode,
				gmode:     gmode,
				bwCode:    bwCode,
				gbw:       gbw,
				autoBW:    autoBW,
				frameMs:   ms20,
				bitrate:   bitrate,
				channels:  channels,
				vbr:       vbr,
				fec:       fec,
				dtx:       dtx,
				signal:    signal,
				gsignal:   gsignal,
				sigClass:  sigClass,
			},
		}
	}

	silk := func(name string, bwCode int, gbw Bandwidth, br, ch int, vbr BitrateMode, fec, dtx bool, sig string) soakSpec {
		return mk(name, libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, bwCode, gbw, false,
			br, ch, vbr, fec, dtx, libopustest.EncodeDiffSignalVoice, SignalVoice, sig)
	}
	hybrid := func(name string, bwCode int, gbw Bandwidth, br, ch int, vbr BitrateMode, fec, dtx bool, sig string) soakSpec {
		return mk(name, libopustest.EncodeDiffForceModeHybrid, EncoderModeHybrid, bwCode, gbw, false,
			br, ch, vbr, fec, dtx, libopustest.EncodeDiffSignalVoice, SignalVoice, sig)
	}
	celt := func(name string, bwCode int, gbw Bandwidth, br, ch int, vbr BitrateMode, sig string) soakSpec {
		return mk(name, libopustest.EncodeDiffForceModeCELTOnly, EncoderModeCELT, bwCode, gbw, false,
			br, ch, vbr, false, false, libopustest.EncodeDiffSignalMusic, SignalMusic, sig)
	}
	auto := func(name string, br, ch int, vbr BitrateMode, fec, dtx bool, sig uint32, gsig Signal, cls string) soakSpec {
		return mk(name, libopustest.EncodeDiffForceModeAuto, EncoderModeAuto, libopustest.EncodeDiffBandwidthAuto, BandwidthFullband, true,
			br, ch, vbr, fec, dtx, sig, gsig, cls)
	}

	bwNB := libopustest.EncodeDiffBandwidthNarrowband
	bwWB := libopustest.EncodeDiffBandwidthWideband
	bwSWB := libopustest.EncodeDiffBandwidthSuperwideband
	bwFB := libopustest.EncodeDiffBandwidthFullband

	specs := []soakSpec{
		// SILK: exercises LPC/LTP/gain prediction memory, NLSF interpolation, the
		// stereo predictor, and (with FEC) the LBRR redundancy counter over a long
		// voiced/unvoiced stream.
		silk("silk_wb_mono_cbr", bwWB, BandwidthWideband, 24000, 1, BitrateModeCBR, false, false, testsignal.CorpusCleanSpeechV1),
		silk("silk_wb_stereo_cvbr", bwWB, BandwidthWideband, 32000, 2, BitrateModeCVBR, false, false, testsignal.CorpusCleanSpeechV1),
		silk("silk_nb_mono_vbr_dtx", bwNB, BandwidthNarrowband, 12000, 1, BitrateModeVBR, false, true, testsignal.CorpusSpeechInNoiseV1),
		silk("silk_wb_mono_vbr_fec", bwWB, BandwidthWideband, 20000, 1, BitrateModeVBR, true, false, testsignal.CorpusSpeechInNoiseV1),

		// Hybrid: SILK + CELT cross-band state simultaneously, plus the redundancy
		// (CELT->SILK handover) path over the run.
		hybrid("hybrid_swb_mono_cvbr", bwSWB, BandwidthSuperwideband, 32000, 1, BitrateModeCVBR, false, false, testsignal.CorpusMixedV1),
		hybrid("hybrid_fb_stereo_vbr", bwFB, BandwidthFullband, 64000, 2, BitrateModeVBR, false, false, testsignal.CorpusMusicV1),
		hybrid("hybrid_fb_mono_vbr_dtx", bwFB, BandwidthFullband, 32000, 1, BitrateModeVBR, false, true, testsignal.CorpusMixedV1),

		// CELT: MDCT overlap memory, band-energy prediction, the prefilter/pitch
		// gain memory, and the VBR reservoir over a long transient-rich music
		// stream.
		celt("celt_fb_mono_cbr", bwFB, BandwidthFullband, 96000, 1, BitrateModeCBR, testsignal.CorpusMusicV1),
		celt("celt_fb_stereo_vbr", bwFB, BandwidthFullband, 128000, 2, BitrateModeVBR, testsignal.CorpusBellClusterV1),
		celt("celt_wb_stereo_cvbr", bwWB, BandwidthWideband, 64000, 2, BitrateModeCVBR, testsignal.CorpusMusicV1),

		// AUTO: the SILK/Hybrid/CELT mode-classification + bandwidth-selection
		// decision driven over a long stream — a sticky misclassification or a
		// drifting analysis state would surface as a late mode flip.
		auto("auto_voice_mono_vbr", 20000, 1, BitrateModeVBR, false, false, libopustest.EncodeDiffSignalVoice, SignalVoice, testsignal.CorpusCleanSpeechV1),
		auto("auto_music_stereo_vbr", 48000, 2, BitrateModeVBR, false, false, libopustest.EncodeDiffSignalMusic, SignalMusic, testsignal.CorpusMusicV1),
		auto("auto_mixed_stereo_cvbr", 32000, 2, BitrateModeCVBR, false, false, libopustest.EncodeDiffSignalAuto, SignalAuto, testsignal.CorpusMixedV1),
	}
	return specs
}

// soakEncodeResult holds the gopus + libopus encode output for one config plus
// the first encode-divergence frame index (-1 if none).
type soakEncodeResult struct {
	gopusPackets   [][]byte
	libopusPackets [][]byte
	firstDiverge   int
}

// runEncodeSoak drives the gopus encoder and the persistent libopus encoder over
// the long stream, asserting byte + final_range parity per frame, and returns
// both packet streams for the decode phases. It tracks the first divergence index.
func runEncodeSoak(t *testing.T, spec soakSpec, pcm []float32, frames, fs int) soakEncodeResult {
	t.Helper()
	const sampleRate = 48000
	ch := spec.enc.channels

	vbr, constraint := vbrFlags(spec.enc.vbr)
	fecCfg, pl := 0, 0
	if spec.enc.fec {
		fecCfg, pl = 1, 20
	}
	recs, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate:    sampleRate,
		Channels:      ch,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     spec.enc.forceMode,
		Bandwidth:     spec.enc.bwCode,
		MaxBandwidth:  spec.enc.bwCode,
		Bitrate:       spec.enc.bitrate,
		Complexity:    10,
		Signal:        spec.enc.signal,
		VBR:           vbr,
		VBRConstraint: constraint,
		ForceChannels: ch,
		InbandFEC:     fecCfg,
		PacketLoss:    pl,
		DTX:           spec.enc.dtx,
		FrameSize:     fs,
		FrameCount:    frames,
		PCM:           pcm,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
		return soakEncodeResult{firstDiverge: -1}
	}
	if len(recs) != frames {
		t.Fatalf("%s: oracle returned %d frames, want %d", spec.name, len(recs), frames)
	}

	enc, ok := configureEncDiff(t, spec.enc)
	if !ok {
		t.Skipf("gopus rejected config %s", spec.name)
	}

	res := soakEncodeResult{
		gopusPackets:   make([][]byte, frames),
		libopusPackets: make([][]byte, frames),
		firstDiverge:   -1,
	}

	var (
		byteFails  int
		rangeFails int
		residuals  int // arm64 documented float-boundary frames
		emitMis    int
	)

	for f := 0; f < frames; f++ {
		frame := pcm[f*fs*ch : (f+1)*fs*ch]
		pkt, err := encDiffEncodeOneFrame(enc, frame)
		if err != nil {
			t.Fatalf("%s frame %d: gopus encode error: %v", spec.name, f, err)
		}
		gPkt := append([]byte(nil), pkt...)
		oPkt := recs[f].Packet
		res.gopusPackets[f] = gPkt
		res.libopusPackets[f] = append([]byte(nil), oPkt...)

		gHas := len(gPkt) > 0
		oHas := recs[f].Ret > 0
		if gHas != oHas {
			emitMis++
			if res.firstDiverge < 0 {
				res.firstDiverge = f
			}
			t.Errorf("%s frame %d: DTX emission mismatch gopus(len=%d) libopus(ret=%d) — output cadence drift",
				spec.name, f, len(gPkt), recs[f].Ret)
			continue
		}
		if !gHas {
			continue // both emitted nothing (DTX no-output)
		}

		if bytes.Equal(gPkt, oPkt) {
			if enc.FinalRange() != recs[f].FinalRange {
				if runtime.GOARCH == "amd64" {
					rangeFails++
					if res.firstDiverge < 0 {
						res.firstDiverge = f
					}
					t.Errorf("%s frame %d: packets byte-equal but final_range differs gopus=%08x libopus=%08x (UNEXPECTED on amd64)",
						spec.name, f, enc.FinalRange(), recs[f].FinalRange)
				} else {
					residuals++
					if res.firstDiverge < 0 {
						res.firstDiverge = f
					}
				}
			}
			continue
		}

		// Byte divergence. Classify per the established arch policy.
		fb := firstByteDiff(gPkt, oPkt)
		gClass := tocModeClass(byte0(gPkt), true)
		oClass := tocModeClass(byte0(oPkt), true)
		if res.firstDiverge < 0 {
			res.firstDiverge = f
		}
		if runtime.GOARCH == "amd64" {
			byteFails++
			// Classify so a maintainer can tell a genuine slow state drift apart from
			// the documented platform-divergent CELT/Hybrid float pipeline. SILK is
			// integer/range-coded and cross-arch deterministic, so a SILK byte
			// mismatch on amd64 is unambiguously a real bug. CELT/Hybrid float
			// analysis is known arch-unstable on knife-edge signals
			// (project_amd64_encoder_precision_regression: amd64-libopus and
			// arm64-libopus produce different bitstreams for such content, diverging
			// at the same early byte every frame); a CELT/Hybrid amd64 mismatch is
			// therefore EITHER a real slow drift (late first-divergence, varying
			// byte) OR that maintainer-owned arch instability. Both are surfaced as a
			// hard failure here — the soak's job is to flag them; adjudication is the
			// maintainer's. A LATE first-divergence frame is the slow-drift tell.
			diag := "SILK is integer/cross-arch-deterministic — this is a real same-arch encoder state bug"
			if gClass != 0 || oClass != 0 {
				diag = "CELT/Hybrid float path — distinguish slow drift (late first-divergence) from " +
					"documented amd64-libopus knife-edge arch instability (project_amd64_encoder_precision_regression)"
			}
			t.Errorf("%s frame %d: PACKET BYTE MISMATCH at byte %d gopus=%s(toc=%02x,len=%d) libopus=%s(toc=%02x,len=%d) "+
				"range g=%08x o=%08x first-divergence=%s — %s (UNEXPECTED on amd64; bit-exact required)",
				spec.name, f, fb, modeClassName(gClass), byte0(gPkt), len(gPkt),
				modeClassName(oClass), byte0(oPkt), len(oPkt), enc.FinalRange(), recs[f].FinalRange,
				divergenceLabel(res.firstDiverge), diag)
		} else {
			residuals++
			// arm64: the documented float boundary; log only the first few diverging
			// frames (the aggregate count + first-divergence index carry the signal)
			// to avoid flooding a thousands-frame soak.
			if residuals <= soakResidualLogCap {
				t.Logf("%s frame %d: payload differs at byte %d (g=%s len=%d, o=%s len=%d) — documented arm64 "+
					"<=1-ULP float boundary (project_arm64_celt_1ulp_drift), not a same-arch state bug",
					spec.name, f, fb, modeClassName(gClass), len(gPkt), modeClassName(oClass), len(oPkt))
			}
		}
	}

	t.Logf("%s ENCODE soak: %d frames; arch=%s; byte-fails=%d range-fails=%d emit-mismatch=%d arm64-residual-frames=%d first-divergence=%s",
		spec.name, frames, runtime.GOARCH, byteFails, rangeFails, emitMis, residuals, divergenceLabel(res.firstDiverge))
	return res
}

// runDecodeSoak decodes the supplied packet stream through one persistent gopus
// Decoder and one persistent libopus decoder (with per-frame final_range), then
// asserts per-frame PCM + final_range parity, tracking the first divergence index.
// label names the source of the packets ("gopus-encoded" / "libopus-encoded").
func runDecodeSoak(t *testing.T, spec soakSpec, packets [][]byte, label string) {
	t.Helper()
	const sampleRate = 48000
	ch := spec.enc.channels
	fs := encFrameSamples48k(spec.enc.frameMs) // uniform 20 ms

	// Build oracle steps from exactly the emitted packets (skip DTX no-output
	// empty packets so both decoders step in lockstep). The persistent libopus
	// decoder oracle (v6) requires a uniform requested frame_size.
	steps := make([]libopusAPIRateDecodeStep, 0, len(packets))
	stepIdx := make([]int, 0, len(packets)) // map step -> original frame index
	for i, p := range packets {
		if len(p) == 0 {
			continue
		}
		steps = append(steps, libopusAPIRateDecodeStep{packet: p})
		stepIdx = append(stepIdx, i)
	}
	if len(steps) == 0 {
		t.Logf("%s DECODE soak (%s): no non-empty packets to decode", spec.name, label)
		return
	}

	refPCM, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, ch, fs, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "persistent decode oracle", err)
		return
	}
	if len(ranges) != len(steps) {
		t.Fatalf("%s DECODE soak (%s): oracle returned %d ranges, want %d", spec.name, label, len(ranges), len(steps))
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, ch))
	if err != nil {
		t.Fatalf("%s DECODE soak (%s): NewDecoder: %v", spec.name, label, err)
	}
	buf := make([]float32, fs*ch)

	firstDiverge := -1
	var (
		pcmFails   int
		rangeFails int
		cntFails   int
		residuals  int
	)
	// refPCM is the concatenation of every step's decoded samples; walk it with a
	// running offset using each step's gopus-decoded sample count (which must
	// match the oracle count, asserted below).
	refOff := 0
	for s, step := range steps {
		clear(buf)
		gn, derr := dec.Decode(step.packet, buf)
		if derr != nil {
			if firstDiverge < 0 {
				firstDiverge = stepIdx[s]
			}
			t.Errorf("%s DECODE soak (%s) frame %d: gopus decode error: %v (packet=% x)",
				spec.name, label, stepIdx[s], derr, step.packet)
			// Cannot advance refOff reliably without the sample count; stop.
			break
		}
		gRange := dec.FinalRange()
		if gRange != ranges[s] {
			rangeFails++
			if firstDiverge < 0 {
				firstDiverge = stepIdx[s]
			}
			if runtime.GOARCH == "amd64" {
				t.Errorf("%s DECODE soak (%s) frame %d: decoder final_range gopus=%08x libopus=%08x (UNEXPECTED on amd64) — decoder state drift",
					spec.name, label, stepIdx[s], gRange, ranges[s])
			}
		}
		// The oracle PCM segment for this step is gn*ch samples (the oracle and
		// gopus must agree on the count; the range/PCM checks below catch a
		// mismatch). Guard the slice in case a count divergence shortens refPCM.
		segLen := gn * ch
		if refOff+segLen > len(refPCM) {
			cntFails++
			if firstDiverge < 0 {
				firstDiverge = stepIdx[s]
			}
			t.Errorf("%s DECODE soak (%s) frame %d: gopus sample count %d overruns oracle PCM (refOff=%d, total=%d) — count drift",
				spec.name, label, stepIdx[s], gn, refOff, len(refPCM))
			break
		}
		want := refPCM[refOff : refOff+segLen]
		refOff += segLen

		toc := byte0(step.packet)
		worst, worstIdx, tol, ok := pcmDiffWorst(toc, libopustest.DecodeDiffFormatFloat32, buf[:segLen], want)
		if !ok {
			if firstDiverge < 0 {
				firstDiverge = stepIdx[s]
			}
			if runtime.GOARCH == "amd64" {
				pcmFails++
				t.Errorf("%s DECODE soak (%s) frame %d: PCM diverges worst|Δ|=%g at %d (tol=%g, toc=%02x mode=%d) — decoder state drift (UNEXPECTED on amd64)",
					spec.name, label, stepIdx[s], worst, worstIdx, tol, toc, tocMode(toc))
			} else {
				residuals++
				if residuals <= soakResidualLogCap {
					t.Logf("%s DECODE soak (%s) frame %d: PCM worst|Δ|=%g at %d (tol=%g, toc=%02x) — documented arm64 <=1-ULP boundary",
						spec.name, label, stepIdx[s], worst, worstIdx, tol, toc)
				}
			}
		}
	}
	if refOff != len(refPCM) && pcmFails == 0 && cntFails == 0 && firstDiverge < 0 {
		t.Errorf("%s DECODE soak (%s): consumed %d of %d oracle PCM samples — sample-count drift",
			spec.name, label, refOff, len(refPCM))
	}

	t.Logf("%s DECODE soak (%s): %d steps; arch=%s; pcm-fails=%d range-fails=%d count-fails=%d arm64-residual-frames=%d first-divergence=%s",
		spec.name, label, len(steps), runtime.GOARCH, pcmFails, rangeFails, cntFails, residuals, divergenceLabel(firstDiverge))
}

func divergenceLabel(idx int) string {
	if idx < 0 {
		return "none(clean)"
	}
	return fmt.Sprintf("frame %d", idx)
}

// TestEncodeDecodeLongStreamSoak runs the three-phase long-stream parity soak
// across the representative config matrix. See the file header for the policy.
func TestEncodeDecodeLongStreamSoak(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}
	requireLibopusAPIRateRefdecodeHelper(t)

	const sampleRate = 48000
	frames := soakFrames()
	specs := buildSoakSweep()

	for _, spec := range specs {
		spec := spec
		t.Run(spec.name, func(t *testing.T) {
			fs := encFrameSamples48k(spec.enc.frameMs)
			ch := spec.enc.channels
			pcm, err := testsignal.GenerateCorpusSignal(spec.sigClass, sampleRate, fs*frames*ch, ch)
			if err != nil {
				t.Fatalf("GenerateCorpusSignal(%s): %v", spec.sigClass, err)
			}

			// Phase 1: encode soak (byte + final_range parity).
			res := runEncodeSoak(t, spec, pcm, frames, fs)

			// Phase 2: decode soak of the gopus-encoded stream (PCM + final_range).
			runDecodeSoak(t, spec, res.gopusPackets, "gopus-encoded")

			// Phase 3: decode soak of the libopus-encoded stream (gopus decoder on a
			// bitstream it did not produce).
			runDecodeSoak(t, spec, res.libopusPackets, "libopus-encoded")
		})
	}
	t.Logf("long-stream soak: %d configs x %d frames (%d ms each) at %d Hz; arch=%s",
		len(specs), frames, encMsOf(ExpertFrameDuration20Ms), sampleRate, runtime.GOARCH)
}
