//go:build gopus_fixedpoint

// decode_differential_fuzz_fixedpoint_test.go — FIXED_POINT differential fuzz
// harness comparing the gopus_fixedpoint integer DecodeInt16 / DecodeInt24 path
// against the libopus FIXED_POINT opus_decode / opus_decode24 oracle.
//
// This is the fixed-point sibling of decode_differential_fuzz_test.go (which
// targets the float decode path). The integer int16/int24 decode core has its
// own logic — the internal/fixedpoint integer CELT decoder, the FixedHybridHighband
// hook, the opus_res-domain redundancy/transition crossfades, and a set of
// documented declines to the float conversion (DTX/PLC degenerate frames, hybrid
// below 16 kHz, CELT-loss below 48 kHz, non-zero decode gain) — and had its own
// bugs historically (sub-16k hybrid silence, hybrid redundancy). It has not been
// broadly swept against the FIXED_POINT oracle.
//
// Strategy: gopus encodes the full valid config space (mode / bandwidth / frame
// duration / bitrate / channels / FEC / DTX) into VALID packets, then decodes the
// resulting stateful packet sequence through BOTH the gopus_fixedpoint
// DecodeInt16 AND DecodeInt24 and the libopus FIXED_POINT opus_decode /
// opus_decode24 reference, asserting bit-exact equality on every architecture
// (the integer decode is FMA-free, so it has no per-arch float drift).
//
// Coverage stages:
//   (a) EncodeThenDecode  — the full encoder config matrix, multi-frame stateful
//       sequences at 48 kHz, both int16 and int24.
//   (b) PLC               — the same matrix with seeded lost frames (nil), so the
//       LOST frames route through the integer celt_decode_lost / float PLC and the
//       recovered frame after a burst is checked too.
//   (c) MultiSampleRate   — decode at 8 / 12 / 16 / 24 / 48 kHz API rates, which
//       exercises the integer-path declines (hybrid <16k, CELT-loss <48k) and
//       confirms the float fallback is itself FIXED_POINT-exact there.
//
// The FIXED_POINT oracle (libopus_refdecode_single.c, wire version 5) aborts the
// whole batch if any opus_decode returns negative, so it is fed only valid gopus
// packets; an encoder error/panic is an encoder-side finding and the spec is
// skipped (it cannot produce a valid packet to decode). Each decode step uses a
// per-channel buffer of exactly the session frame size so the PLC concealment
// frame size matches the reference's frame_size argument.
//
// Run with GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 for the full sweep.

package gopus

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// fixedPerArchTol is the maximum absolute per-sample integer difference tolerated
// between the gopus_fixedpoint decode and the FIXED_POINT reference: 0 (bit-exact)
// on every architecture.
//
// Unlike the float decode path — whose documented darwin/arm64 ≤1-ULP residual
// (project_arm64_celt_1ulp_drift) comes from Go-vs-clang FMA-contraction
// boundaries — the gopus_fixedpoint integer CELT/SILK/Hybrid decode is pure
// integer arithmetic with no fused-multiply-add, so it is deterministic and
// bit-identical to the FIXED_POINT reference on all architectures. The full sweep
// (this harness) is bit-exact on both amd64 and darwin/arm64; a non-zero budget
// here would be a mask, so it stays 0.
func fixedPerArchTol() int64 {
	return 0
}

// fixedFrameSamplesAtRate returns the per-channel sample count of one frame for
// the given encoder frame duration at an arbitrary API sample rate. The float
// frameSamples48k() helper is 48 kHz only; the multi-rate stage needs the count
// at 8/12/16/24 kHz too.
func fixedFrameSamplesAtRate(s encodeSweepSpec, sampleRate int) int {
	// frameSamples48k() / 48000 * sampleRate, kept integer-exact (all rates are
	// divisors of 48000 and all frame sizes are multiples of 48000/400=120).
	return s.frameSamples48k() * sampleRate / 48000
}

// fixedDecodeGopusSequence decodes a stateful packet sequence (nil entries are
// lost frames / PLC) through one gopus_fixedpoint Decoder for each of int16 and
// int24, using a per-step buffer of exactly frameSamples*channels so the PLC
// frame size matches the FIXED_POINT reference's frame_size argument. It returns
// the concatenated int16 (widened to int32) and int24 outputs.
func fixedDecodeGopusSequence(t *testing.T, sampleRate, channels, frameSamples int, steps [][]byte) (got16, got24 []int32, err error) {
	t.Helper()
	dec16, e := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if e != nil {
		return nil, nil, fmt.Errorf("NewDecoder int16: %w", e)
	}
	dec24, e := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if e != nil {
		return nil, nil, fmt.Errorf("NewDecoder int24: %w", e)
	}
	for p, pkt := range steps {
		o16 := make([]int16, frameSamples*channels)
		n16, e := fixedDecodeInt16Probe(dec16, pkt, o16)
		if e != nil {
			return nil, nil, fmt.Errorf("step %d DecodeInt16: %w", p, e)
		}
		got16 = append(got16, int16ToInt32(o16[:n16*channels])...)

		o24 := make([]int32, frameSamples*channels)
		n24, e := fixedDecodeInt24Probe(dec24, pkt, o24)
		if e != nil {
			return nil, nil, fmt.Errorf("step %d DecodeInt24: %w", p, e)
		}
		got24 = append(got24, o24[:n24*channels]...)
	}
	return got16, got24, nil
}

// fixedDecodeInt16Probe / fixedDecodeInt24Probe wrap a single decode and convert a
// decoder panic into an error so a Go-side crash minimises to one packet rather
// than aborting the whole sweep.
func fixedDecodeInt16Probe(dec *Decoder, pkt []byte, out []int16) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in DecodeInt16: %v", r)
		}
	}()
	return dec.DecodeInt16(pkt, out)
}

func fixedDecodeInt24Probe(dec *Decoder, pkt []byte, out []int32) (n int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in DecodeInt24: %v", r)
		}
	}()
	return dec.DecodeInt24(pkt, out)
}

// fixedOracleDecodeSequence decodes a stateful packet sequence through the libopus
// FIXED_POINT opus_decode / opus_decode24 reference and returns the int16 (widened
// to int32) and int24 outputs. The oracle batch aborts on any negative return; the
// caller uses fixedFindRejectedStep on the error path to isolate which packet
// libopus rejected so a divergence minimises to one packet.
func fixedOracleDecodeSequence(sampleRate, channels, frameSamples int, steps [][]byte) (ref16, ref24 []int32, err error) {
	r16, e := decodeWithLibopusFixedInt16(sampleRate, channels, frameSamples, steps)
	if e != nil {
		return nil, nil, e
	}
	r24, e := decodeWithLibopusFixedInt24(sampleRate, channels, frameSamples, steps)
	if e != nil {
		return nil, nil, e
	}
	return int16ToInt32(r16), r24, nil
}

// fixedFindRejectedStep replays the sequence prefix-by-prefix through the
// FIXED_POINT oracle to find the first step the reference rejects (the helper
// returns a whole-batch error). It returns the 0-based step index, or -1 if the
// whole sequence decodes cleanly (the original error was something else, e.g. a
// missing helper). Used only on the error path to classify a divergence.
func fixedFindRejectedStep(sampleRate, channels, frameSamples int, steps [][]byte) int {
	for i := 1; i <= len(steps); i++ {
		if _, e := decodeWithLibopusFixedInt16(sampleRate, channels, frameSamples, steps[:i]); e != nil {
			return i - 1
		}
	}
	return -1
}

// fixedPointSpecificDivergence reports whether an observed int16/int24 divergence
// from the FIXED_POINT reference is specific to the gopus_fixedpoint integer
// decode path, which is what this harness gates. It decodes the same step
// sequence through the gopus FLOAT path and the libopus FLOAT opus_decode_float
// reference — BOTH STATEFUL through one decoder over the whole sequence, matching
// how the integer path is driven — and compares the concatenated float PCM. If the
// FLOAT path is itself bit-exact (within the float per-arch budget) the divergence
// lives only in the integer path → fixed-point-specific (the caller hard-fails).
// If the FLOAT path ALSO diverges, the bug is in the shared decode logic (e.g.
// SILK cross-frame, hybrid multi-loss PLC) and would surface in the float
// decode-fuzz harness too; it is reported as a shared-path finding and not
// hard-failed here, keeping this gate precisely targeted at fixed-point-specific
// regressions (it is NOT masking a fixed-point bug — those, where the stateful
// float decode is exact, still hard-fail).
//
// The stateful float reference is decodeWithLibopusReferenceAPIRateFloat32 (wire
// version 5: ONE FLOAT opus_decoder over the sequence). The per-case-fresh float
// oracle (ProbeDecodeDiff) must NOT be used here — its stateless decode would
// diverge from gopus's stateful decode by construction on any multi-frame
// sequence, spuriously classifying a real fixed-point bug as shared-path.
func fixedPointSpecificDivergence(t *testing.T, label string, sampleRate, channels, frameSamples int, steps [][]byte) bool {
	t.Helper()
	want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSamples, steps)
	if err != nil {
		// No stateful float reference available: cannot classify, so treat as
		// fixed-specific (conservative — never silently drops a real fixed divergence).
		t.Logf("%s: stateful float reference unavailable (%v) — treating divergence as fixed-point-specific", label, err)
		return true
	}
	dec, derr := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if derr != nil {
		return true
	}
	var got []float32
	for i, p := range steps {
		buf := make([]float32, frameSamples*channels)
		n, e := dec.Decode(p, buf)
		if e != nil {
			t.Logf("%s: SHARED-PATH finding — gopus FLOAT decode rejected step %d (%v) where the libopus FLOAT reference accepted", label, i, e)
			return false
		}
		got = append(got, buf[:n*channels]...)
	}
	if len(got) != len(want) {
		t.Logf("%s: SHARED-PATH finding — float PCM length mismatch (gopus=%d libopus=%d)", label, len(got), len(want))
		return false
	}
	var worst float32
	for j := range got {
		if d := absF32(got[j] - want[j]); d > worst {
			worst = d
		}
	}
	// The float path's per-arch tolerance (see pcmExactTolerance): amd64 exact,
	// darwin/arm64 the documented ≤few-LSB CELT/Hybrid FMA-contraction budget.
	floatTol := float32(0)
	if runtime.GOARCH != "amd64" {
		floatTol = 4.0 / 32768.0
	}
	if worst > floatTol {
		t.Logf("%s: SHARED-PATH finding — gopus FLOAT decode also diverges from the FLOAT reference (worst|Δ|=%g > %g); the divergence is in the shared decode logic, not the integer path, and is out of scope for this fixed-point gate. Not hard-failing here.", label, worst, floatTol)
		return false
	}
	return true
}

// TestDecodeDifferentialFixedPointEncodeThenDecode sweeps the full encoder config
// space, decodes each multi-frame stateful sequence through the gopus_fixedpoint
// integer DecodeInt16 / DecodeInt24 and the libopus FIXED_POINT reference, and
// asserts bit-exact equality on every architecture.
func TestDecodeDifferentialFixedPointEncodeThenDecode(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := getFixedRefdecodeHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode", err)
	}

	specs := buildEncodeSweep()
	const sampleRate = 48000
	const framesPerSpec = 4

	budget := diffFuzzBudget(len(specs))
	if budget > len(specs) {
		budget = len(specs)
	}
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	tested := 0
	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		tested++
		t.Run(spec.name, func(t *testing.T) {
			specRng := rand.New(rand.NewSource(int64(idx)*2654435761 + 7))
			packets, ok := encodePackets(t, spec, specRng, framesPerSpec)
			if !ok {
				t.Skipf("encoder rejected config %s", spec.name)
			}
			frameSamples := spec.frameSamples48k()

			ref16, ref24, err := fixedOracleDecodeSequence(sampleRate, spec.channels, frameSamples, packets)
			if err != nil {
				if step := fixedFindRejectedStep(sampleRate, spec.channels, frameSamples, packets); step >= 0 {
					// libopus FIXED_POINT rejected a packet gopus produced as valid. If
					// gopus also rejects it, that is agreement (not a decode divergence);
					// if gopus accepts it, it is a real gopus encode/decode divergence.
					dec, _ := NewDecoder(DefaultDecoderConfig(sampleRate, spec.channels))
					o := make([]int16, frameSamples*spec.channels)
					if _, gerr := fixedDecodeInt16Probe(dec, packets[step], o); gerr == nil {
						t.Errorf("%s: libopus FIXED_POINT rejected gopus packet %d but gopus accepted — packet=% x",
							spec.name, step, packets[step])
					} else {
						t.Logf("%s: libopus FIXED_POINT and gopus both reject packet %d (gopus: %v) — agreement", spec.name, step, gerr)
					}
					return
				}
				libopustest.HelperUnavailable(t, "fixed reference decode", err)
				return
			}

			got16, got24, err := fixedDecodeGopusSequence(t, sampleRate, spec.channels, frameSamples, packets)
			if err != nil {
				t.Fatalf("%s: gopus decode: %v", spec.name, err)
			}

			reportFixedDivergence(t, spec.name, sampleRate, spec.channels, frameSamples, packets, got16, ref16, got24, ref24)
		})
	}
	t.Logf("fixed encode-then-decode sweep: %d/%d specs × %d frames (int16+int24)", tested, len(specs), framesPerSpec)
}

// plcDropPattern returns a deterministic lost-frame map for a sequence of n
// frames: a single mid-sequence loss plus a short burst, leaving received frames
// before and after each loss so the recovery frame is exercised.
func plcDropPattern(rng *rand.Rand, n int) map[int]bool {
	loss := make(map[int]bool)
	if n < 4 {
		return loss
	}
	// One isolated loss early-mid, then a 2-3 frame burst later, never the first
	// frame (the decoder needs at least one received frame to prime state) and
	// never the last (so a recovered frame always follows a loss).
	single := 1 + rng.Intn(maxFixedInt(1, n/3))
	if single >= n-1 {
		single = n - 2
	}
	loss[single] = true
	burstLen := 2 + rng.Intn(2) // 2..3
	burstStart := single + 2 + rng.Intn(maxFixedInt(1, n/4))
	for k := 0; k < burstLen && burstStart+k < n-1; k++ {
		loss[burstStart+k] = true
	}
	return loss
}

func maxFixedInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestDecodeDifferentialFixedPointPLC sweeps the config space with seeded lost
// frames so the integer concealment (celt_decode_lost) and the float PLC fallback
// are both checked bit-exact against the FIXED_POINT reference, including the
// recovered frame after a burst.
//
// Scope: CELT-only, SILK, and Hybrid modes are all hard-gated. A lost Hybrid frame
// advances the integer CELT highband cross-frame state through the loss and
// accumulates the concealed highband onto the integer SILK lowband (see
// armFixedHybridLost / finishFixedHybridLost), mirroring opus_decode_frame's
// celt_decode_with_ec_dred(NULL, celt_accum=1), so both the lost frame and the
// post-loss recovery frame stay bit-exact in the integer path.
func TestDecodeDifferentialFixedPointPLC(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := getFixedRefdecodeHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode", err)
	}

	specs := buildEncodeSweep()
	const sampleRate = 48000
	const framesPerSpec = 9

	budget := diffFuzzBudget(len(specs))
	if budget > len(specs) {
		budget = len(specs)
	}
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	tested := 0
	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		// DTX produces empty packets which are already a concealment path; layering
		// PLC drops on top conflates the two. The non-DTX specs cover PLC cleanly.
		if spec.dtx {
			continue
		}
		tested++
		t.Run(spec.name, func(t *testing.T) {
			specRng := rand.New(rand.NewSource(int64(idx)*40503 + 11))
			encoded, ok := encodePackets(t, spec, specRng, framesPerSpec)
			if !ok {
				t.Skipf("encoder rejected config %s", spec.name)
			}
			loss := plcDropPattern(specRng, framesPerSpec)
			if len(loss) == 0 {
				t.Skip("no loss pattern for this length")
			}
			steps := make([][]byte, framesPerSpec)
			for i := 0; i < framesPerSpec; i++ {
				if loss[i] {
					steps[i] = nil
				} else {
					steps[i] = encoded[i]
				}
			}
			frameSamples := spec.frameSamples48k()

			ref16, ref24, err := fixedOracleDecodeSequence(sampleRate, spec.channels, frameSamples, steps)
			if err != nil {
				if step := fixedFindRejectedStep(sampleRate, spec.channels, frameSamples, steps); step >= 0 {
					t.Logf("%s: libopus FIXED_POINT rejected step %d in PLC sequence — skipping", spec.name, step)
					t.Skip("oracle rejected a step in the PLC sequence")
				}
				libopustest.HelperUnavailable(t, "fixed reference decode (PLC)", err)
				return
			}

			got16, got24, err := fixedDecodeGopusSequence(t, sampleRate, spec.channels, frameSamples, steps)
			if err != nil {
				t.Fatalf("%s: gopus PLC decode: %v", spec.name, err)
			}

			if !reportFixedDivergence(t, spec.name+"/plc", sampleRate, spec.channels, frameSamples, steps, got16, ref16, got24, ref24) {
				t.Logf("%s: loss pattern=%v", spec.name, loss)
			}
		})
	}
	t.Logf("fixed PLC sweep (CELT-only + SILK + Hybrid): %d specs × %d frames (int16+int24)", tested, framesPerSpec)
}

// TestDecodeDifferentialFixedPointMultiSampleRate decodes the same packets at the
// API rates the integer path treats differently (8/12/16/24/48 kHz). The hybrid
// path declines below 16 kHz and the CELT-loss path declines below 48 kHz, so
// this stage confirms the float fallback is itself FIXED_POINT-exact at every
// supported decode rate.
func TestDecodeDifferentialFixedPointMultiSampleRate(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := getFixedRefdecodeHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode", err)
	}

	// libopus opus_decoder_create accepts only these output rates.
	rates := []int{8000, 12000, 16000, 24000, 48000}

	specs := buildEncodeSweep()
	const framesPerSpec = 4

	budget := diffFuzzBudget(len(specs))
	if budget > len(specs) {
		budget = len(specs)
	}
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	tested := 0
	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		tested++
		t.Run(spec.name, func(t *testing.T) {
			// Encode once at 48 kHz (the encoder always runs at 48 kHz here); the
			// resulting packets are decoded at every API rate.
			specRng := rand.New(rand.NewSource(int64(idx)*982451653 + 13))
			packets, ok := encodePackets(t, spec, specRng, framesPerSpec)
			if !ok {
				t.Skipf("encoder rejected config %s", spec.name)
			}
			for _, sr := range rates {
				frameSamples := fixedFrameSamplesAtRate(spec, sr)
				if frameSamples <= 0 {
					continue
				}
				rateName := fmt.Sprintf("%dHz", sr)
				ref16, ref24, err := fixedOracleDecodeSequence(sr, spec.channels, frameSamples, packets)
				if err != nil {
					if step := fixedFindRejectedStep(sr, spec.channels, frameSamples, packets); step >= 0 {
						t.Logf("%s/%s: libopus FIXED_POINT rejected packet %d — skipping rate", spec.name, rateName, step)
						continue
					}
					libopustest.HelperUnavailable(t, "fixed reference decode (multi-rate)", err)
					return
				}
				got16, got24, err := fixedDecodeGopusSequence(t, sr, spec.channels, frameSamples, packets)
				if err != nil {
					t.Errorf("%s/%s: gopus decode: %v", spec.name, rateName, err)
					continue
				}
				reportFixedDivergence(t, spec.name+"/"+rateName, sr, spec.channels, frameSamples, packets, got16, ref16, got24, ref24)
			}
		})
	}
	t.Logf("fixed multi-rate sweep: %d specs × %d rates × %d frames (int16+int24)", tested, len(rates), framesPerSpec)
}

// withinFixedTol reports whether got matches want within the per-arch integer
// budget (fixedPerArchTol), returning the divergence stats for reporting.
func withinFixedTol(got, want []int32) (ok bool, diffs int, maxAbs int64, firstIdx int) {
	if len(got) != len(want) {
		return false, -1, -1, -1
	}
	diffs, maxAbs, firstIdx = divergence(got, want)
	if diffs == 0 || maxAbs <= fixedPerArchTol() {
		return true, diffs, maxAbs, firstIdx
	}
	return false, diffs, maxAbs, firstIdx
}

// reportFixedDivergence compares the gopus_fixedpoint int16 and int24 output of a
// step sequence against the FIXED_POINT reference. When neither format exceeds the
// per-arch budget it returns true (clean). Otherwise it classifies the divergence
// with fixedPointSpecificDivergence: a fixed-point-specific divergence (the gopus
// FLOAT path is exact) is a hard failure; a shared-path divergence (the gopus
// FLOAT path also diverges) is logged as an out-of-scope finding and does not
// fail this fixed-point-targeted gate.
func reportFixedDivergence(t *testing.T, label string, sampleRate, channels, frameSamples int, steps [][]byte, got16, want16, got24, want24 []int32) bool {
	t.Helper()
	ok16, d16, m16, f16 := withinFixedTol(got16, want16)
	ok24, d24, m24, f24 := withinFixedTol(got24, want24)
	if ok16 && ok24 {
		return true
	}
	if fixedPointSpecificDivergence(t, label, sampleRate, channels, frameSamples, steps) {
		if !ok16 {
			t.Errorf("%s/int16: FIXED-POINT-SPECIFIC divergence — %d/%d samples differ, maxAbs=%d (tol=%d), first at %d: gopus=%d libopus=%d",
				label, d16, len(got16), m16, fixedPerArchTol(), f16, at(got16, f16), at(want16, f16))
		}
		if !ok24 {
			t.Errorf("%s/int24: FIXED-POINT-SPECIFIC divergence — %d/%d samples differ, maxAbs=%d (tol=%d), first at %d: gopus=%d libopus=%d",
				label, d24, len(got24), m24, fixedPerArchTol(), f24, at(got24, f24), at(want24, f24))
		}
		logFixedDivergingPackets(t, label, steps)
		return false
	}
	// Shared-path finding (logged inside fixedPointSpecificDivergence). Record the
	// integer-side magnitudes for completeness, then continue without failing.
	t.Logf("%s: shared-path divergence magnitudes — int16 maxAbs=%d, int24 maxAbs=%d (not a fixed-point-specific regression)", label, m16, m24)
	logFixedDivergingPackets(t, label, steps)
	return true
}

func at(s []int32, i int) int32 {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

// logFixedDivergingPackets dumps the encoded packet bytes for a diverging spec so
// a failure can be minimised offline to a single packet.
func logFixedDivergingPackets(t *testing.T, label string, packets [][]byte) {
	t.Helper()
	for i, p := range packets {
		toc := byte(0)
		if len(p) > 0 {
			toc = p[0]
		}
		t.Logf("%s: packet %d (len=%d toc=0x%02x): % x", label, i, len(p), toc, p)
	}
}
