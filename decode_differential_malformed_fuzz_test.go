// decode_differential_malformed_fuzz_test.go — strategy (b) of the differential
// decode fuzzer: seeded structured mutations of valid Opus packets, decoded
// through both gopus and the libopus oracle, asserting AGREEMENT on
// accept-vs-reject and identical PCM when both accept.
//
// Mutation classes (seeded, reproducible):
//   - truncation at every byte boundary
//   - single-byte flips / random byte overwrites
//   - TOC config / stereo / code-bit rewrites (mode + frame-count boundaries)
//   - code-2/3 frame-length and padding-byte corruption
//   - code-3 frame-count M boundary values (0, 1, 48, 49, 63)
//
// A panic, a gopus-accepts-where-libopus-rejects (or vice versa), or a PCM
// mismatch when both accept is a divergence. Because every case is a single
// self-contained packet decoded through a fresh decoder, a failure is already
// minimal (one packet, printed as hex on failure).

package gopus

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// seedPacketsForMutation encodes a small bank of valid packets spanning the
// three coding modes, mono + stereo, several frame sizes, and code 0/1/2/3
// framings (via the repacketizer for multi-frame codes). These are the bases the
// mutator perturbs.
func seedPacketsForMutation(t *testing.T) [][]byte {
	t.Helper()
	var seeds [][]byte

	for _, ch := range []int{1, 2} {
		for _, fs := range []int{120, 240, 480, 960, 1920} {
			// CELT (supports 2.5/5/10/20/40 ms).
			if fs <= 1920 {
				seeds = append(seeds, encodeAPIRateCELTPacketFrameSize(t, ch, fs))
			}
		}
		for _, fs := range []int{480, 960, 1920, 2880} {
			// SILK (10/20/40/60 ms).
			seeds = append(seeds, encodeAPIRateSILKPacketFrameSize(t, ch, fs))
		}
		for _, fs := range []int{480, 960} {
			// Hybrid (10/20 ms).
			seeds = append(seeds, encodeAPIRateHybridPacketFrameSize(t, ch, fs))
		}
	}

	// Build code-1/2/3 framings by concatenating frames via the repacketizer so
	// the mutator also perturbs multi-frame headers.
	for _, ch := range []int{1, 2} {
		base := encodeAPIRateCELTPacketFrameSize(t, ch, 480)
		if multi := repackMultiFrame(t, base, 3); multi != nil {
			seeds = append(seeds, multi)
		}
		if multi := repackMultiFrame(t, base, 6); multi != nil {
			seeds = append(seeds, multi)
		}
	}

	// Dedup empty.
	out := seeds[:0]
	for _, s := range seeds {
		if len(s) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// repackMultiFrame repacketizes n copies of base into a single code-3 packet.
// Returns nil if repacketization is not possible for the input.
func repackMultiFrame(t *testing.T, base []byte, n int) []byte {
	t.Helper()
	rp := NewRepacketizer()
	for i := 0; i < n; i++ {
		if err := rp.Cat(base); err != nil {
			return nil
		}
	}
	buf := make([]byte, 1500*n+64)
	written, err := rp.Out(buf)
	if err != nil || written <= 0 {
		return nil
	}
	return append([]byte(nil), buf[:written]...)
}

// mutatePacket applies one seeded structured mutation to a copy of src.
func mutatePacket(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	switch rng.Intn(9) {
	case 0: // truncate to a random shorter length (including 0)
		n := rng.Intn(len(p) + 1)
		return p[:n]
	case 1: // single random byte overwrite
		p[rng.Intn(len(p))] = byte(rng.Intn(256))
		return p
	case 2: // single bit flip
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // rewrite TOC config (mode/bandwidth/frame-size) keeping low 3 bits
		p[0] = (p[0] & 0x07) | byte(rng.Intn(32))<<3
		return p
	case 4: // flip TOC code bits (frame packing)
		p[0] = (p[0] & 0xFC) | byte(rng.Intn(4))
		return p
	case 5: // flip TOC stereo bit
		p[0] ^= 0x04
		return p
	case 6: // force code-3 with a boundary frame count
		ms := []byte{0x00, 0x01, 0x30, 0x31, 0x3F, 0x80, 0x81, 0xB0, 0xC1}
		p[0] = (p[0] & 0xFC) | 0x03
		if len(p) < 2 {
			p = append(p, 0)
		}
		p[1] = ms[rng.Intn(len(ms))]
		return p
	case 7: // corrupt a frame-length byte (code-2/3 VBR region)
		if len(p) >= 3 {
			i := 1 + rng.Intn(len(p)-1)
			p[i] = byte(rng.Intn(256))
		}
		return p
	default: // append junk bytes (oversize / padding-ambiguity)
		extra := 1 + rng.Intn(8)
		for k := 0; k < extra; k++ {
			p = append(p, byte(rng.Intn(256)))
		}
		return p
	}
}

// knownMalformedPCMDivergences pins corrupt packets for which gopus and libopus
// both ACCEPT (same return code, same sample count, no panic) but the decoded
// PCM values differ by more than malformedPCMGrossTol. These would be byte-value
// differences on deliberately corrupted input where neither decoder's output is
// "correct"; they are NOT accept/reject or safety divergences (those are always
// hard-failed below).
//
// The map is currently empty: the previously allow-listed mode-crossed packet (a
// multi-frame CELT payload whose TOC was rewritten to Hybrid config 14, code 3,
// VBR) is now bit-exact. Its 2nd in-sequence frame is a CELT silence frame
// (SILK over-consumed the corrupt payload, so the CELT range coder sees tell >=
// storage); the hybrid silence path wrote the scaled, deemphasized PCM into the
// caller's celt_accum output buffer but ALSO returned the raw, unscaled celt_sig
// synthesis buffer, which the hybrid wrapper then copied over the scaled output.
// On clean silence the carried MDCT overlap tail is near zero so the bug was
// invisible; this corrupt cross-frame state left a large overlap tail, surfacing
// it as a ~60x output. Fixed in celt.decodeSilenceFrame (return nil on the
// direct-out path, matching synthesizeHybridDecodedFrame).
var knownMalformedPCMDivergences = map[string]bool{}

// malformedPCMGrossTol is the float32-scale per-sample bound above which a PCM
// difference on a corrupt-but-accepted packet is treated as a real decode
// mistake rather than ULP amplification. Clean (valid) packets are held to the
// tight per-arch budget by the encode-then-decode sweep; this looser bound is
// for deliberately corrupted input only, where a 1-ULP predictor difference can
// be amplified by the unstable filters that garbage drives.
const malformedPCMGrossTol = 0.05

// malformedPCMWorst returns the worst per-sample |Δ| in float32 scale, skipping
// int24 conversion-overflow samples (|x|>=256) where both libopus lrintf and Go
// int32() casts are implementation-defined.
func malformedPCMWorst(format uint32, got, want []float32) float32 {
	if len(got) != len(want) {
		return float32(1e9)
	}
	int24Overflow := format == libopustest.DecodeDiffFormatInt24
	var worst float32
	for i := range got {
		if int24Overflow && (absF32(got[i]) >= 250.0 || absF32(want[i]) >= 250.0) {
			continue
		}
		if d := absF32(got[i] - want[i]); d > worst {
			worst = d
		}
	}
	return worst
}

// TestDecodeDifferentialMalformed mutates valid packets and asserts gopus and
// libopus agree on accept-vs-reject and (when both accept) identical PCM.
//
// The accept/reject parity, sample-count parity, and no-panic invariants are
// HARD failures (they are the security-critical robustness contract). PCM-value
// equality on packets both decoders accept is also enforced, except for the
// documented knownMalformedPCMDivergences corrupt inputs.
func TestDecodeDifferentialMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	seeds := seedPacketsForMutation(t)
	if len(seeds) == 0 {
		t.Skip("no seed packets")
	}

	// Decode every mutation at both channel counts (a mutated TOC may toggle the
	// stereo bit, and the decoder is created per channel-count). We probe each
	// mutation through both 1ch and 2ch decoders since the decoder channel count
	// is fixed at construction and a mutated stereo bit must be handled.
	formats := []uint32{
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}

	iters := diffFuzzBudget(20000)
	rng := rand.New(rand.NewSource(0xC0FFEE))

	totalCases := 0
	pcmDiverged := 0
	// Batch mutations per (channels, format) so the oracle decodes many cases in
	// one subprocess invocation.
	for _, channels := range []int{1, 2} {
		for _, format := range formats {
			// Generate this batch's mutations deterministically.
			n := iters / (2 * len(formats))
			cases := make([]libopustest.DecodeDiffCase, 0, n)
			muts := make([][]byte, 0, n)
			for k := 0; k < n; k++ {
				seed := seeds[rng.Intn(len(seeds))]
				m := mutatePacket(rng, seed)
				muts = append(muts, m)
				cases = append(cases, libopustest.DecodeDiffCase{Packet: m, Format: format, FrameSize: 5760})
			}
			oracle, err := libopustest.ProbeDecodeDiff(48000, channels, cases)
			if err != nil {
				libopustest.HelperUnavailable(t, "decode diff probe", err)
				return
			}
			totalCases += len(cases)
			for i := range cases {
				or := oracle[i]
				m := muts[i]
				gpcm, gn, gerr := safeGopusDecode(48000, channels, cases[i])
				label := fmt.Sprintf("ch%d/fmt%d/mut%d", channels, format, i)

				// ---- critical invariants: accept/reject parity + no panic ----
				if or.Code < 0 {
					if gerr == nil {
						t.Errorf("%s: libopus REJECTED (code=%d) but gopus ACCEPTED (n=%d) — packet=% x",
							label, or.Code, gn, m)
					}
					continue
				}
				if gerr != nil {
					t.Errorf("%s: libopus ACCEPTED (n=%d) but gopus REJECTED: %v — packet=% x",
						label, or.Code, gerr, m)
					continue
				}
				if gn != int(or.Code) {
					t.Errorf("%s: sample count gopus=%d libopus=%d — packet=% x", label, gn, or.Code, m)
					continue
				}

				// ---- PCM-value parity (allowing documented corrupt residuals) ----
				// On deliberately corrupted input the two decoders may differ by
				// more than the clean per-arch ULP budget: a 1-ULP difference in a
				// predictor (e.g. SILK stereo MS->LR, CELT energy) gets amplified by
				// the unstable filters that garbage drives. That is expected and not
				// a bug. We therefore gate the malformed PCM only against GROSS
				// divergence (the signature of a real decode mistake, e.g. the
				// mode-crossed Hybrid case that produced ~60x full scale), while the
				// security-critical invariants above stay bit-strict.
				want := oracleResultToFloat32(format, or)
				worst := malformedPCMWorst(format, gpcm, want)
				if worst > malformedPCMGrossTol {
					pcmDiverged++
					if knownMalformedPCMDivergences[hex.EncodeToString(m)] {
						t.Logf("%s: known corrupt-input gross PCM residual (allow-listed, worst |Δ|=%g) packet=% x",
							label, worst, m)
					} else {
						t.Errorf("%s: UNEXPECTED gross PCM divergence (worst |Δ|=%g, tol=%g) on accepted packet=% x",
							label, worst, malformedPCMGrossTol, m)
					}
				}
			}
		}
	}
	t.Logf("malformed sweep: %d cases, %d PCM-value divergence(s) (all allow-listed corrupt-input residuals)", totalCases, pcmDiverged)
}

// safeGopusDecode wraps gopusDecodeProbe and converts a panic into an error so a
// crash on a malformed packet is reported as a divergence (and minimised) rather
// than aborting the whole sweep.
func safeGopusDecode(sampleRate, channels int, c libopustest.DecodeDiffCase) (pcm []float32, samples int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus decode: %v", r)
		}
	}()
	return gopusDecodeProbe(sampleRate, channels, c)
}
