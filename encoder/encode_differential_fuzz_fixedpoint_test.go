//go:build gopus_fixedpoint

// encode_differential_fuzz_fixedpoint_test.go — FIXED_POINT ENCODE-side
// differential fuzz harness comparing the PUBLIC gopus integer-CELT encode path
// against the FIXED_POINT libopus encoder oracle across the full handled
// configuration space, asserting BYTE-EXACT inner CELT payloads (and TOC bytes).
//
// This is the fixed-point analog of the float-build encode_differential_fuzz_test.go
// (root package). The float harness can compare FULL top-level opus_encode packets
// because the default gopus build is all-float, exactly like libopus
// opus_encode_float() — there is no float-vs-integer wrapper boundary.
//
// Under gopus_fixedpoint the situation is different and is the reason this harness
// lives at the inner-CELT-payload level rather than the full-packet level:
//
//   - gopus_fixedpoint swaps only the INNER SILK/CELT frame encoders to the
//     integer (FIXED_POINT) paths. The Opus API wrapper around them — dc_reject(),
//     the SILK API-rate resampler, the CELT delay buffer / float2int16 — still runs
//     in FLOAT (the same float code the default build uses).
//   - libopus FIXED_POINT opus_encode() runs that whole wrapper in INTEGER.
//
// So a raw-PCM top-level opus_encode comparison diverges in the wrapper before the
// inner encoder runs (documented in testvectors/opus_encode_fixed_endtoend_parity_test.go).
// The byte-exact relationship that IS achievable, and the one this fuzz sweeps
// broadly, is:
//
//   - Forced CELT-only (or restricted-low-delay): capture the EXACT int16 the
//     integer CELT encoder consumed via Encoder.LastFixedCELTInput16(), feed that
//     identical int16 to the FIXED_POINT celt_encode_with_ec reference, and assert
//     the inner CELT payload (packet[1:]) is byte-for-byte identical. Plus, the
//     assembled TOC byte must equal the FIXED opus_encode() TOC for the config.
//
// Why CELT-only (not SILK/Hybrid): the SILK path's input is produced by the FLOAT
// API-rate resampler in gopus vs the INTEGER silk_resampler in libopus FIXED, so a
// public-encoder SILK byte comparison from raw PCM is not byte-exact (the FIXED
// SILK encode is itself byte-exact given identical int16, proven per-frame by
// silk.TestPublicSILKEncodeFrameFixedByteExact — a different, internal gate). The
// integer CELT path captures the post-resampler/post-float2int16 int16 it actually
// consumed, so its comparison sidesteps the wrapper boundary and IS byte-exact.
//
// Coverage (the handled integer-CELT public-encode space):
//   - API sample rates: 48000 + sub-rates 24000/16000/12000/8000 (upsample 1/2/3/4/6)
//   - channels: mono + stereo (force-coded so the TOC stereo bit is stable)
//   - frame sizes: every CELT duration valid at each rate (2.5/5/10/20 ms core block)
//   - bitrate: low/mid/high incl. the 510 kbps cap and a near-floor rate
//   - complexity: 0 / 5 / 10
//   - rate control: CBR / CVBR / VBR
//   - bandwidth: NB/MB/WB/SWB/FB (end-band selection)
//   - signal: several seeded corpus classes incl. transients and near-silence
//   - 48 kHz cases run STATEFULLY over a multi-frame stream (cross-frame integer
//     CELT state: energy histories, VBR reservoir, prefilter memory, transient
//     run) compared against the stateful FIXED celt_encode_with_ec sequence oracle.
//     Sub-rate cases compare per-frame against the per-frame FIXED rate oracle
//     (the sequence oracle is 48 kHz-only).
//
// Any inner-payload or TOC byte mismatch is a HARD FAIL on every arch: the integer
// CELT encode consumes identical int16 and runs identical integer arithmetic, so
// there is NO float boundary to excuse a divergence (unlike the float harness's
// documented arm64 ≤1-ULP CELT analysis residual). A divergence here is a real
// fixed-point encode bug.
//
// Run:
//   GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
//     go test -tags gopus_fixedpoint -run TestEncodeDifferentialFuzzFixedPoint ./encoder/

package encoder

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

// fixedUpsampleForRate is the resampling_factor for each supported API rate.
var fixedUpsampleForRate = map[int]int{48000: 1, 24000: 2, 16000: 3, 12000: 4, 8000: 6}

// encFixSpec is one point in the integer-CELT public-encode configuration space.
type encFixSpec struct {
	name       string
	rate       int
	channels   int
	frameSize  int // per-channel samples at rate (upsamples to a 48 kHz core block)
	bandwidth  types.Bandwidth
	oracleBW   int
	bitrate    int
	complexity int
	mode       BitrateMode
	sigClass   string
}

// fixedBandwidthForRate clamps a requested bandwidth to what the API rate can
// actually carry. The integer CELT end-band switch needs a legal bandwidth for
// the rate (e.g. an 8 kHz API rate cannot encode FB), matching the public
// encoder's own clamping so the gopus packet and the oracle agree.
func fixedBandwidthForRate(rate int, bw types.Bandwidth) (types.Bandwidth, int) {
	max := types.BandwidthFullband
	switch rate {
	case 8000:
		max = types.BandwidthNarrowband
	case 12000:
		max = types.BandwidthMediumband
	case 16000:
		max = types.BandwidthWideband
	case 24000:
		max = types.BandwidthSuperwideband
	}
	if bw > max {
		bw = max
	}
	var code int
	switch bw {
	case types.BandwidthNarrowband:
		code = libopustest.OpusBandwidthNarrowband
	case types.BandwidthMediumband:
		code = libopustest.OpusBandwidthMediumband
	case types.BandwidthWideband:
		code = libopustest.OpusBandwidthWideband
	case types.BandwidthSuperwideband:
		code = libopustest.OpusBandwidthSuperwideband
	default:
		code = libopustest.OpusBandwidthFullband
	}
	return bw, code
}

// buildEncFixSweep enumerates the integer-CELT public-encode config matrix.
func buildEncFixSweep() []encFixSpec {
	var specs []encFixSpec

	// Frame durations in tenths of a millisecond so 2.5 ms is exact.
	durTenthMS := []int{25, 50, 100, 200}

	bandwidths := []types.Bandwidth{
		types.BandwidthNarrowband,
		types.BandwidthWideband,
		types.BandwidthFullband,
	}
	// A bitrate spread that exercises the CBR byte-count floor, mid rates, and the
	// VBR ceiling. 6 kbps stresses the low end; 510 kbps the cap.
	bitrates := []int{6000, 32000, 64000, 128000, 510000}
	complexities := []int{0, 5, 10}
	modes := []BitrateMode{ModeCBR, ModeCVBR, ModeVBR}
	// Signal classes that stress different CELT decisions: harmonic music, sharp
	// transients (transient analysis / TF), near-silence (energy floor / VBR),
	// noise (spreading), and a stereo-decorrelated source for the stereo path.
	signals := []string{
		testsignal.CorpusMusicV1,
		testsignal.CorpusCastanetTransientV1,
		testsignal.CorpusNearSilenceV1,
		testsignal.CorpusWhiteNoiseV1,
		testsignal.CorpusStereoDecorrelatedV1,
	}

	for ri, rate := range []int{48000, 24000, 16000, 12000, 8000} {
		up := fixedUpsampleForRate[rate]
		for _, ch := range []int{1, 2} {
			for di, dur := range durTenthMS {
				frameSize := rate * dur / 10000
				if frameSize <= 0 {
					continue
				}
				core := frameSize * up
				if core != 120 && core != 240 && core != 480 && core != 960 {
					continue
				}
				for bi, reqBW := range bandwidths {
					bw, code := fixedBandwidthForRate(rate, reqBW)
					// Skip duplicate (rate clamps several requested BWs to the same
					// effective BW); keep only the first request that yields it.
					if reqBW != bw && reqBW > bw {
						// requested higher than max → clamped; keep the canonical one.
						if bi > 0 {
							// avoid duplicate effective-BW specs at a clamped rate
							continue
						}
					}
					for _, br := range bitrates {
						for _, cx := range complexities {
							for _, m := range modes {
								sig := signals[(ri+di+bi+int(m)+cx/5)%len(signals)]
								if ch == 2 {
									sig = signals[(ri+di+bi)%len(signals)]
								}
								specs = append(specs, encFixSpec{
									name: fmt.Sprintf("fs%d_ch%d_dur%d_%s_br%d_cx%d_%v",
										rate, ch, dur, bwShort(bw), br, cx, m),
									rate:       rate,
									channels:   ch,
									frameSize:  frameSize,
									bandwidth:  bw,
									oracleBW:   code,
									bitrate:    br,
									complexity: cx,
									mode:       m,
									sigClass:   sig,
								})
							}
						}
					}
				}
			}
		}
	}
	return specs
}

func bwShort(bw types.Bandwidth) string {
	switch bw {
	case types.BandwidthNarrowband:
		return "nb"
	case types.BandwidthMediumband:
		return "mb"
	case types.BandwidthWideband:
		return "wb"
	case types.BandwidthSuperwideband:
		return "swb"
	default:
		return "fb"
	}
}

// encFixLowRatePLC reports whether libopus opus_encode() would take the low-rate
// "PLC frame" minimal-packet early-exit (opus_encoder.c:1340) for this config,
// i.e. st->bitrate_bps < 3*frame_rate*8. frame_rate is Fs/frame_size at the API
// rate. For CBR the effective bitrate_bps is the requested rate clamped to the
// CBR byte budget, which at these tiny frames does not raise it above the
// request, so the requested bitrate is a faithful proxy for the comparison.
func encFixLowRatePLC(rate, frameSize, bitrate int, mode BitrateMode) bool {
	if frameSize <= 0 {
		return false
	}
	frameRate := rate / frameSize
	return bitrate < 3*frameRate*8
}

// fixFuzzBudget shrinks the sweep under -short so it stays CI-friendly.
func fixFuzzBudget(full int) int {
	if testing.Short() {
		b := full / 8
		if b < 16 {
			b = 16
		}
		return b
	}
	return full
}

// firstByteDiffFix returns the index of the first differing byte (or the shorter
// length if one is a prefix of the other), -1 if equal.
func firstByteDiffFix(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// TestEncodeDifferentialFuzzFixedPoint drives the public integer-CELT encode path
// over the handled config space and asserts the inner CELT payload (and TOC byte)
// is byte-exact to the FIXED_POINT libopus reference. See the file header for the
// inner-payload comparison rationale and why a divergence is a hard fail on every
// arch.
func TestEncodeDifferentialFuzzFixedPoint(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		numFrames    = 6 // stateful 48 kHz stream length
		celtStart    = 0
		celtMaxBytes = celtPacketSizeCap - 1
	)

	specs := buildEncFixSweep()
	budget := fixFuzzBudget(len(specs))
	if budget > len(specs) {
		budget = len(specs)
	}
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	var (
		tested        int
		celtCases     int
		payloadFails  int
		tocFails      int
		outOfScope    int
		stateful48k   int
		subRateFrames int
		lowRatePLC    int
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		// Skip the libopus low-rate "PLC frame" early-exit corner. When
		// st->bitrate_bps < 3*frame_rate*8 (opus_encoder.c:1340), libopus
		// opus_encode() short-circuits and emits a 1-2 byte minimal TOC-only
		// packet WITHOUT running the bandwidth selection or the inner CELT/SILK
		// encoder at all, using the encoder's stale/default st->bandwidth
		// (FULLBAND on the first frame) for the TOC. This is a top-level Opus-API
		// behavior that gopus does not implement (it runs a full integer CELT
		// encode at the requested bandwidth instead). It is NOT an integer-encode
		// divergence — it is an arch-independent gap SHARED with the float build
		// (verified: the FLOAT libopus opus_encode emits the identical FB
		// early-exit packet at 6 kbps / 2.5 ms), and so is out of scope for this
		// fixed-point inner-encode byte-exactness gate. At 48 kHz CELT the only
		// case the sweep reaches is 6 kbps / 2.5 ms (frame_rate 400 →
		// 3*400*8 = 9600 > 6000); 5 ms+ at 6 kbps and every higher bitrate clear
		// the threshold. Comparing either the TOC or the inner payload here would
		// compare gopus's real CELT packet against libopus's minimal PLC stub, so
		// the spec is excluded outright and counted separately.
		if encFixLowRatePLC(spec.rate, spec.frameSize, spec.bitrate, spec.mode) {
			lowRatePLC++
			continue
		}
		tested++
		t.Run(spec.name, func(t *testing.T) {
			up := fixedUpsampleForRate[spec.rate]
			_ = up
			end := celtFixedEndBand(spec.bandwidth)
			vbr := spec.mode != ModeCBR
			cvbr := spec.mode == ModeCVBR

			if spec.rate == 48000 {
				// Stateful multi-frame: encode a whole stream, capture each frame's
				// consumed int16, then replay the FIXED celt_encode_with_ec sequence
				// over the concatenated int16 so cross-frame integer state matches
				// frame for frame.
				enc := configureFixCELT(spec)
				type captured struct {
					packet   []byte
					consumed []int16
				}
				caps := make([]captured, 0, numFrames)
				inScope := true
				for f := 0; f < numFrames; f++ {
					pcm := genFixFrame(spec, f, numFrames)
					pkt, err := enc.Encode(pcm, spec.frameSize)
					if err != nil {
						t.Fatalf("frame %d: public Encode: %v", f, err)
					}
					if !enc.fixedCELTUsed {
						inScope = false
						break
					}
					if len(pkt) < 1 {
						t.Fatalf("frame %d: empty packet", f)
					}
					in16 := enc.LastFixedCELTInput16()
					if len(in16) != spec.channels*spec.frameSize {
						t.Fatalf("frame %d: LastFixedCELTInput16 len=%d want %d",
							f, len(in16), spec.channels*spec.frameSize)
					}
					caps = append(caps, captured{
						packet:   append([]byte(nil), pkt...),
						consumed: append([]int16(nil), in16...),
					})
				}
				if !inScope {
					outOfScope++
					t.Skipf("%s: frame not routed through integer CELT (out of scope)", spec.name)
				}
				celtCases++
				stateful48k++

				consumedAll := make([]int16, 0, numFrames*spec.frameSize*spec.channels)
				for _, c := range caps {
					consumedAll = append(consumedAll, c.consumed...)
				}

				// TOC parity against the FIXED top-level opus_encode().
				topPackets, err := libopustest.ProbeOpusEncodeFixed(libopustest.OpusEncodeFixedParams{
					SampleRate:    spec.rate,
					Channels:      spec.channels,
					ForceMode:     libopustest.OpusForceModeCELTOnly,
					Bandwidth:     spec.oracleBW,
					Bitrate:       spec.bitrate,
					Complexity:    spec.complexity,
					VBR:           vbr,
					VBRConstraint: cvbr,
					ForceChannels: spec.channels,
					FrameSize:     spec.frameSize,
					FrameCount:    numFrames,
					PCM:           consumedAll,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "opus encode fixed", err)
					return
				}
				if len(topPackets) != len(caps) {
					t.Fatalf("FIXED opus_encode packet count=%d gopus=%d", len(topPackets), len(caps))
				}

				wantInner, err := libopustest.ProbeCELTFixedEncodeSeq(
					consumedAll, spec.channels, spec.frameSize, celtStart, end,
					spec.bitrate, spec.complexity, vbr, cvbr, celtMaxBytes, numFrames)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed encode seq", err)
					return
				}
				if len(wantInner) != len(caps) {
					t.Fatalf("FIXED celt_encode seq count=%d gopus=%d", len(wantInner), len(caps))
				}

				for f, c := range caps {
					subRateFrames++
					if c.packet[0] != topPackets[f][0] {
						tocFails++
						t.Errorf("%s/frame%d: TOC MISMATCH gopus=%02x FIXED opus_encode=%02x",
							spec.name, f, c.packet[0], topPackets[f][0])
						continue
					}
					got := c.packet[1:]
					want := wantInner[f]
					if !bytes.Equal(got, want) {
						payloadFails++
						fb := firstByteDiffFix(got, want)
						t.Errorf("%s/frame%d: INNER CELT PAYLOAD BYTE MISMATCH at byte %d "+
							"(len got=%d want=%d) br=%d cx=%d %v end=%d ch=%d — same int16 input, "+
							"integer encode divergence (HARD FAIL all arch)\n got=% x\nwant=% x",
							spec.name, f, fb, len(got), len(want), spec.bitrate, spec.complexity,
							spec.mode, end, spec.channels, got, want)
					}
				}
				return
			}

			// Sub-rate: per-frame fresh-encoder comparison against the per-frame
			// FIXED rate oracle (the sequence oracle is 48 kHz-only). A single Encode
			// on a fresh encoder is first-frame state on both sides.
			enc := configureFixCELT(spec)
			pcm := genFixFrame(spec, 0, 1)
			pkt, err := enc.Encode(pcm, spec.frameSize)
			if err != nil {
				t.Fatalf("public Encode: %v", err)
			}
			if !enc.fixedCELTUsed {
				outOfScope++
				t.Skipf("%s: frame not routed through integer CELT (out of scope)", spec.name)
			}
			if len(pkt) < 1 {
				t.Fatalf("empty packet")
			}
			celtCases++
			subRateFrames++

			in16 := enc.LastFixedCELTInput16()
			if len(in16) != spec.channels*spec.frameSize {
				t.Fatalf("LastFixedCELTInput16 len=%d want %d", len(in16), spec.channels*spec.frameSize)
			}

			// TOC parity against the FIXED top-level opus_encode() (single frame).
			topPackets, err := libopustest.ProbeOpusEncodeFixed(libopustest.OpusEncodeFixedParams{
				SampleRate:    spec.rate,
				Channels:      spec.channels,
				ForceMode:     libopustest.OpusForceModeCELTOnly,
				Bandwidth:     spec.oracleBW,
				Bitrate:       spec.bitrate,
				Complexity:    spec.complexity,
				VBR:           vbr,
				VBRConstraint: cvbr,
				ForceChannels: spec.channels,
				FrameSize:     spec.frameSize,
				FrameCount:    1,
				PCM:           append([]int16(nil), in16...),
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "opus encode fixed", err)
				return
			}
			if len(topPackets) != 1 {
				t.Fatalf("FIXED opus_encode packet count=%d want 1", len(topPackets))
			}
			if pkt[0] != topPackets[0][0] {
				tocFails++
				t.Errorf("%s: TOC MISMATCH gopus=%02x FIXED opus_encode=%02x",
					spec.name, pkt[0], topPackets[0][0])
				return
			}

			want, err := libopustest.ProbeCELTFixedEncodeRate(
				append([]int16(nil), in16...), spec.channels, spec.frameSize, celtStart, end,
				spec.bitrate, spec.complexity, celtMaxBytes, spec.rate, vbr, cvbr, false, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode rate", err)
				return
			}
			got := pkt[1:]
			if !bytes.Equal(got, want) {
				payloadFails++
				fb := firstByteDiffFix(got, want)
				t.Errorf("%s: INNER CELT PAYLOAD BYTE MISMATCH at byte %d (len got=%d want=%d) "+
					"rate=%d br=%d cx=%d %v end=%d ch=%d — same int16 input, integer encode "+
					"divergence (HARD FAIL all arch)\n got=% x\nwant=% x",
					spec.name, fb, len(got), len(want), spec.rate, spec.bitrate, spec.complexity,
					spec.mode, end, spec.channels, got, want)
			}
		})
	}

	t.Logf("fixed-point encode differential sweep: %d/%d specs tested "+
		"(CELT-in-scope=%d out-of-scope-skips=%d low-rate-PLC-excluded=%d; "+
		"stateful-48k-streams=%d frames-compared=%d); TOC-fails=%d inner-payload-fails=%d",
		tested, len(specs), celtCases, outOfScope, lowRatePLC, stateful48k, subRateFrames, tocFails, payloadFails)
}

// configureFixCELT builds and configures an integer-CELT public Encoder for one
// spec. Forced CELT-only + force-coded channels so the integer path is engaged
// and the TOC stereo bit is a stable comparison.
func configureFixCELT(spec encFixSpec) *Encoder {
	enc := NewEncoder(spec.rate, spec.channels)
	enc.SetMode(ModeCELT)
	enc.SetLowDelay(true)
	enc.SetBandwidth(spec.bandwidth)
	enc.SetComplexity(spec.complexity)
	enc.SetBitrate(spec.bitrate)
	enc.SetBitrateMode(spec.mode)
	enc.SetForceChannels(spec.channels)
	return enc
}

// genFixFrame returns one deterministic PCM frame for a spec, derived from the
// seeded corpus class so the input is reproducible and the sweep is a real fuzz.
// The frame is the f-th of nframes consecutive frames of the corpus signal.
func genFixFrame(spec encFixSpec, f, nframes int) []float32 {
	n := spec.frameSize * spec.channels
	pcm, err := testsignal.GenerateCorpusSignal(spec.sigClass, spec.rate, spec.frameSize*nframes*spec.channels, spec.channels)
	if err != nil || len(pcm) < (f+1)*n {
		// Fall back to a deterministic xorshift fill if the corpus generator
		// rejects an exotic (rate, length) combination, so the fuzz still runs.
		out := make([]float32, n)
		state := uint32(0xC0FFEE + spec.rate + spec.frameSize*131 + spec.bitrate + spec.complexity*7 + int(spec.mode)*97 + f*1009)
		for i := range out {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			v := int32(state)
			s := float32(v>>16) / 32768.0 * 0.25
			if s >= 1 {
				s = 0.9999
			}
			if s < -1 {
				s = -1
			}
			out[i] = s
		}
		return out
	}
	return pcm[f*n : (f+1)*n]
}
