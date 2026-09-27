//go:build gopus_fixed_point

// This fixed-point encode differential sweep drives public forced-CELT streams.
// For each frame it compares the complete inner CELT payload and final range
// against selected FIXED_POINT celt_encode_with_ec on the exact opus_res Q8
// samples, analysis, and output cap consumed by Go. It also compares the TOC
// against selected C opus_encode_float on the original public input.
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
//   - 48 kHz cases replay six consecutive frames in one C CELT encoder.
//     Sub-rate cases use a fresh encoder on each side.
//
// Run:
//   GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
//     go test -tags gopus_fixed_point -run TestEncodeDifferentialFuzzFixedPoint ./encoder/

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
		numFrames = 6 // stateful 48 kHz stream length
		celtStart = 0
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
		// The libopus low-rate "PLC frame" early-exit corner. When
		// st->bitrate_bps < 3*frame_rate*8 (opus_encoder.c:1340), libopus
		// opus_encode() short-circuits and emits a 1-2 byte minimal TOC-only
		// packet WITHOUT running the bandwidth selection or the inner CELT/SILK
		// encoder at all, using the encoder's stale/default st->mode/st->bandwidth
		// (MODE_HYBRID / FULLBAND on the first frame) for the TOC. gopus reproduces
		// this exact early-exit (emitLowSpacePacket), so the corner is now
		// hard-asserted: the gopus minimal packet must equal the FIXED opus_encode()
		// minimal packet BYTE-FOR-BYTE (no inner CELT payload is produced on either
		// side). This is an arch-independent gap shared with the float build, so the
		// assertion holds on every arch. The check runs in its own branch because
		// there is no inner-CELT payload to compare against the CELT sequence/rate
		// oracle — the whole packet is the comparison.
		if encFixLowRatePLC(spec.rate, spec.frameSize, spec.bitrate, spec.mode) {
			lowRatePLC++
			tested++
			t.Run(spec.name+"_plc", func(t *testing.T) {
				vbr := spec.mode != ModeCBR
				cvbr := spec.mode == ModeCVBR
				enc := configureFixCELT(spec)
				pcm := genFixFrame(spec, 0, 1)
				pkt, err := enc.Encode(pcm, spec.frameSize)
				if err != nil {
					t.Fatalf("public Encode: %v", err)
				}
				if len(pkt) < 1 {
					t.Fatalf("empty packet")
				}
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
					PCM:           make([]int16, spec.frameSize*spec.channels),
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "opus encode fixed", err)
					return
				}
				if len(topPackets) != 1 {
					t.Fatalf("FIXED opus_encode packet count=%d want 1", len(topPackets))
				}
				if !bytes.Equal(pkt, topPackets[0]) {
					payloadFails++
					fb := firstByteDiffFix(pkt, topPackets[0])
					t.Errorf("%s: LOW-RATE PLC MINIMAL-PACKET BYTE MISMATCH at byte %d "+
						"(len gopus=%d FIXED=%d) br=%d %v — early-exit divergence (HARD FAIL all arch)\n"+
						" gopus=% x\n FIXED=% x",
						spec.name, fb, len(pkt), len(topPackets[0]), spec.bitrate, spec.mode, pkt, topPackets[0])
				}
			})
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
				// Replay the same stateful sequence in the selected C CELT encoder,
				// using each frame's exact opus_res Q8 input and controls.
				enc := configureFixCELT(spec)
				type captured struct {
					packet []byte
					rangeV uint32
				}
				caps := make([]captured, 0, numFrames)
				frames := make([]libopustest.CELTFixedQ8Frame, 0, numFrames)
				topFrames := make([]libopustest.OpusEncodeFixedMixedFrame, 0, numFrames)
				innerBitrate, lsbDepth := 0, 0
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
					inQ8 := enc.LastFixedCELTInputQ8()
					if len(inQ8) != spec.channels*spec.frameSize {
						t.Fatalf("frame %d: LastFixedCELTInputQ8 len=%d want %d",
							f, len(inQ8), spec.channels*spec.frameSize)
					}
					rate, maxBytes, depth := enc.LastFixedCELTControls()
					if f == 0 {
						innerBitrate, lsbDepth = rate, depth
					} else if rate != innerBitrate || depth != lsbDepth {
						t.Fatalf("frame %d: inner bitrate/depth changed %d/%d %d/%d", f, rate, innerBitrate, depth, lsbDepth)
					}
					frame := fixedQ8OracleFrame(enc)
					frame.MaxBytes = maxBytes
					frames = append(frames, frame)
					topFrames = append(topFrames, libopustest.OpusEncodeFixedMixedFrame{
						Format: 1, FloatPCM: append([]float32(nil), pcm...),
					})
					caps = append(caps, captured{
						packet: append([]byte(nil), pkt...), rangeV: enc.FinalRange(),
					})
				}
				if !inScope {
					outOfScope++
					t.Skipf("%s: frame not routed through integer CELT (out of scope)", spec.name)
				}
				celtCases++
				stateful48k++

				// Compare TOC against the same raw float input through selected C.
				topPackets, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
					SampleRate:     spec.rate,
					Channels:       spec.channels,
					Application:    libopustest.OpusApplicationRestrictedLowDelay,
					MaxPacketBytes: 4000,
					ForceMode:      libopustest.OpusForceModeCELTOnly,
					Bandwidth:      spec.oracleBW,
					Bitrate:        spec.bitrate,
					Complexity:     spec.complexity,
					VBR:            vbr,
					VBRConstraint:  cvbr,
					ForceChannels:  spec.channels,
					FrameSize:      spec.frameSize,
					FrameCount:     numFrames,
				}, topFrames)
				if err != nil {
					libopustest.HelperUnavailable(t, "opus encode fixed", err)
					return
				}
				if len(topPackets) != len(caps) {
					t.Fatalf("FIXED opus_encode packet count=%d gopus=%d", len(topPackets), len(caps))
				}

				wantInner, err := libopustest.ProbeCELTFixedRawQ8(libopustest.CELTFixedQ8Params{
					SampleRate: spec.rate, Channels: spec.channels, FrameSize: spec.frameSize,
					Start: celtStart, End: end, Bitrate: innerBitrate, Complexity: spec.complexity,
					LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: cvbr, Frames: frames,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
					return
				}
				if len(wantInner) != len(caps) {
					t.Fatalf("FIXED celt_encode seq count=%d gopus=%d", len(wantInner), len(caps))
				}

				for f, c := range caps {
					subRateFrames++
					if topPackets[f].Status < 0 || len(topPackets[f].Packet) == 0 {
						t.Fatalf("%s/frame%d: selected C opus_encode_float status=%d", spec.name, f, topPackets[f].Status)
					}
					if c.packet[0] != topPackets[f].Packet[0] {
						tocFails++
						t.Errorf("%s/frame%d: TOC MISMATCH gopus=%02x FIXED opus_encode=%02x",
							spec.name, f, c.packet[0], topPackets[f].Packet[0])
						continue
					}
					got := c.packet[1:]
					want := wantInner[f]
					if !bytes.Equal(got, want.Packet) || c.rangeV != want.FinalRange {
						payloadFails++
						fb := firstByteDiffFix(got, want.Packet)
						t.Errorf("%s/frame%d: INNER CELT PAYLOAD BYTE MISMATCH at byte %d "+
							"(len got=%d want=%d range=%08x/%08x) br=%d cx=%d %v end=%d ch=%d — same Q8 input\n got=% x\nwant=% x",
							spec.name, f, fb, len(got), len(want.Packet), c.rangeV, want.FinalRange, spec.bitrate, spec.complexity,
							spec.mode, end, spec.channels, got, want.Packet)
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

			frame := fixedQ8OracleFrame(enc)
			if len(frame.PCM) != spec.channels*spec.frameSize {
				t.Fatalf("LastFixedCELTInputQ8 len=%d want %d", len(frame.PCM), spec.channels*spec.frameSize)
			}
			innerBitrate, maxBytes, lsbDepth := enc.LastFixedCELTControls()
			frame.MaxBytes = maxBytes

			// Compare TOC against the same raw float input through selected C.
			topPackets, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate:     spec.rate,
				Channels:       spec.channels,
				Application:    libopustest.OpusApplicationRestrictedLowDelay,
				MaxPacketBytes: 4000,
				ForceMode:      libopustest.OpusForceModeCELTOnly,
				Bandwidth:      spec.oracleBW,
				Bitrate:        spec.bitrate,
				Complexity:     spec.complexity,
				VBR:            vbr,
				VBRConstraint:  cvbr,
				ForceChannels:  spec.channels,
				FrameSize:      spec.frameSize,
				FrameCount:     1,
			}, []libopustest.OpusEncodeFixedMixedFrame{{Format: 1, FloatPCM: pcm}})
			if err != nil {
				libopustest.HelperUnavailable(t, "opus encode fixed", err)
				return
			}
			if len(topPackets) != 1 {
				t.Fatalf("FIXED opus_encode packet count=%d want 1", len(topPackets))
			}
			if topPackets[0].Status < 0 || len(topPackets[0].Packet) == 0 {
				t.Fatalf("selected C opus_encode_float status=%d", topPackets[0].Status)
			}
			if pkt[0] != topPackets[0].Packet[0] {
				tocFails++
				t.Errorf("%s: TOC MISMATCH gopus=%02x FIXED opus_encode=%02x",
					spec.name, pkt[0], topPackets[0].Packet[0])
				return
			}

			want, err := libopustest.ProbeCELTFixedRawQ8(libopustest.CELTFixedQ8Params{
				SampleRate: spec.rate, Channels: spec.channels, FrameSize: spec.frameSize,
				Start: celtStart, End: end, Bitrate: innerBitrate, Complexity: spec.complexity,
				LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: cvbr,
				Frames: []libopustest.CELTFixedQ8Frame{frame},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
				return
			}
			if len(want) != 1 {
				t.Fatalf("fixed raw Q8 oracle records=%d want 1", len(want))
			}
			got := pkt[1:]
			if !bytes.Equal(got, want[0].Packet) || enc.FinalRange() != want[0].FinalRange {
				payloadFails++
				fb := firstByteDiffFix(got, want[0].Packet)
				t.Errorf("%s: INNER CELT PAYLOAD BYTE MISMATCH at byte %d (len got=%d want=%d) "+
					"rate=%d br=%d cx=%d %v end=%d ch=%d range=%08x/%08x — same Q8 input\n got=% x\nwant=% x",
					spec.name, fb, len(got), len(want[0].Packet), spec.rate, spec.bitrate, spec.complexity,
					spec.mode, end, spec.channels, enc.FinalRange(), want[0].FinalRange, got, want[0].Packet)
			}
		})
	}

	t.Logf("fixed-point encode differential sweep: %d/%d specs tested "+
		"(CELT-in-scope=%d out-of-scope-skips=%d low-rate-PLC-asserted=%d; "+
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
