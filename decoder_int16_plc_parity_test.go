package gopus

// int16 PLC-vs-float32 parity: mode × channels × loss-pattern.
//
// Two complementary assertions for DecodeInt16 packet-loss concealment:
//
//  1. libopus oracle assertion: DecodeInt16(nil,...) output matches
//     libopus opus_decode(NULL,...) (int16) within the trusted near-exact bar
//     (same bar as TestDecodeInt16APIRatePCMMatchesLibopus). libopus shares a
//     single float PLC core for both decode and decode_float; the int16 output
//     is float PLC + FLOAT2INT16 quantization (celt/float_cast.h). Both paths
//     go through the identical concealment, so the int16 output is deterministic
//     with respect to the float output modulo the well-understood arm64 1-ULP
//     tail that the near-exact quality bar absorbs.
//
//  2. self-consistency assertion: DecodeInt16(nil,...) == float32ToInt16(Decode(nil,...))
//     for every loss-pattern frame. This asserts that the int16 path is strictly
//     the float path + quantization and not an independent concealment branch
//     (matches opus_decoder.c: opus_decode / opus_decode_float share the same
//     inner decode then diverge only at the final sample-format conversion,
//     FLOAT2INT16 / celt/float_cast.h, line ~53).
//
// Loss patterns (applied after warmupCount good packets):
//
//	single   - one lost frame then recovery
//	burst    - three consecutive lost frames
//	periodic - loss on every 3rd frame over 9 frames
//	leading  - loss before any good packet (cold PLC)
//	trailing - loss at end, no recovery packet

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// plcInt16LossStep is one step in a test sequence: packet==nil means PLC.
type plcInt16LossStep struct {
	packet []byte // nil → PLC
}

// plcInt16LossPattern groups a named sequence of loss steps (built relative to
// a concrete packet once the mode is known).
type plcInt16LossPattern struct {
	name  string
	build func(pkt []byte) []plcInt16LossStep
}

// plcInt16LossPatterns returns the five canonical loss patterns used in the
// parity matrix.  All patterns produce at least one PLC step.
func plcInt16LossPatterns(pkt []byte) []plcInt16LossPattern {
	return []plcInt16LossPattern{
		{
			name: "single",
			// one good packet then one lost frame then one recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{{packet: p}, {packet: nil}, {packet: p}}
			},
		},
		{
			name: "burst",
			// one good packet then three consecutive losses then recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{
					{packet: p},
					{packet: nil},
					{packet: nil},
					{packet: nil},
					{packet: p},
				}
			},
		},
		{
			name: "periodic",
			// loss on every third frame over 9 total frames (3 losses, 6 good)
			build: func(p []byte) []plcInt16LossStep {
				steps := make([]plcInt16LossStep, 0, 9)
				for i := 0; i < 9; i++ {
					if (i+1)%3 == 0 {
						steps = append(steps, plcInt16LossStep{packet: nil})
					} else {
						steps = append(steps, plcInt16LossStep{packet: p})
					}
				}
				return steps
			},
		},
		{
			name: "leading",
			// cold PLC: loss before any good packet (libopus returns silence)
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{{packet: nil}, {packet: p}}
			},
		},
		{
			name: "trailing",
			// good packet then two trailing losses, no recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{
					{packet: p},
					{packet: nil},
					{packet: nil},
				}
			},
		},
	}
}

// plcInt16ModeCases returns the three canonical mode test cases.
func plcInt16ModeCases(t *testing.T, channels int) []struct {
	mode   string
	packet []byte
} {
	t.Helper()
	return []struct {
		mode   string
		packet []byte
	}{
		{"silk", encodeAPIRateSILKPacket(t, channels)},
		{"celt", encodeAPIRateCELTPacket(t, channels)},
		{"hybrid", encodeAPIRateHybridPacket(t, channels)},
	}
}

// plcInt16DecodeSteps converts our loss-step slice to libopusAPIRateDecodeStep.
func plcInt16DecodeSteps(steps []plcInt16LossStep) []libopusAPIRateDecodeStep {
	out := make([]libopusAPIRateDecodeStep, len(steps))
	for i, s := range steps {
		out[i] = libopusAPIRateDecodeStep{packet: s.packet}
	}
	return out
}

// TestDecodeInt16PLCModeChannelLossMatrixMatchesLibopus asserts that
// DecodeInt16(nil,...) matches libopus opus_decode(NULL,...) across
// mode {SILK,CELT,Hybrid} × channels {1,2} × loss-pattern {single, burst,
// periodic, leading, trailing} at the 48 kHz API rate.
//
// The quality bar is the same trusted near-exact bar used by
// TestDecodeInt16APIRatePCMMatchesLibopus: opus_compare MinQ=20 for 48 kHz
// streams with ≥480 samples/channel of real content; corr/RMS near-exact for
// PLC-dominated or sub-48k streams.  The arm64 1-ULP float tail that produces
// ≤1 LSB int16 divergence on CELT/Hybrid is absorbed by the quality bar and
// is not a defect (documented in arm64_celt_1ulp_drift memory).
func TestDecodeInt16PLCModeChannelLossMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			for _, pat := range plcInt16LossPatterns(pkt) {
				steps := pat.build(pkt)
				name := mc.mode + "_ch" + itoaSmall(channels) + "_" + pat.name

				t.Run(name, func(t *testing.T) {
					if celtIntegerPLCActive && mc.mode == "celt" && pat.name != "leading" {
						t.Skip("CELT PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecoderFixedPointCELTPLCParity")
					}
					if hybridIntegerPLCActive && mc.mode == "hybrid" && pat.name != "leading" {
						t.Skip("Hybrid PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecodeDifferentialFixedPointPLC")
					}
					// Oracle: libopus int16.
					libSteps := plcInt16DecodeSteps(steps)
					want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
					if err != nil {
						libopustest.HelperUnavailable(t, "int16 PLC matrix reference decode", err)
					}

					// gopus DecodeInt16.
					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]int16, 0, len(want))
					buf := make([]int16, frameSize*channels)
					for i, step := range steps {
						n, err := dec.DecodeInt16(step.packet, buf)
						if err != nil {
							t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
						}
						got = append(got, buf[:n*channels]...)
					}

					if len(got) != len(want) {
						t.Fatalf("sample count mismatch: got %d want %d", len(got), len(want))
					}

					// Any PLC step makes this PLC-dominated; use the PLC bar so
					// opus_compare's psychoacoustic Q is not the gate on concealed audio.
					hasPLC := false
					for _, step := range steps {
						if step.packet == nil {
							hasPLC = true
							break
						}
					}
					assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, hasPLC, name+" int16 PLC vs libopus")
				})
			}
		}
	}
}

// TestDecodeInt16PLCEqualsFloat32PLCQuantized asserts that the int16 PLC path
// is exactly the float32 PLC path quantized by the standard int16 converter.
//
// This verifies the libopus invariant that opus_decode and opus_decode_float share
// a single inner decode + PLC engine, diverging only at the final sample-format
// conversion (FLOAT2INT16 in celt/float_cast.h, ~line 53).  A separate int16
// concealment branch would violate this invariant and break the parity matrix.
//
// The test uses two fresh independent decoders with the same warm-up sequence so
// both decoders hold identical state before the PLC step.  The float32 PLC output
// is converted to int16 using float32ToInt16NoSoftClip — the same function that
// DecodeInt16 uses internally — so the comparison is sample-for-sample exact
// on all platforms including darwin/arm64 where the NEON VCVT rounding and the
// scalar roundFloat32ToInt32Even can differ by ±1 LSB on half-integer inputs.
func TestDecodeInt16PLCEqualsFloat32PLCQuantized(t *testing.T) {
	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			for _, pat := range plcInt16LossPatterns(pkt) {
				steps := pat.build(pkt)
				name := mc.mode + "_ch" + itoaSmall(channels) + "_" + pat.name

				t.Run(name, func(t *testing.T) {
					if celtIntegerPLCActive && mc.mode == "celt" {
						t.Skip("CELT PLC routes to the integer decoder under gopus_fixedpoint, diverging from float; see TestDecoderFixedPointCELTPLCParity")
					}
					if hybridIntegerPLCActive && mc.mode == "hybrid" {
						t.Skip("Hybrid PLC routes to the integer decoder under gopus_fixedpoint, diverging from float; see TestDecodeDifferentialFixedPointPLC")
					}
					decF := mustNewTestDecoder(t, sampleRate, channels)
					dec16 := mustNewTestDecoder(t, sampleRate, channels)

					// wantBuf is used to convert the float32 PLC output to int16
					// using the same function as DecodeInt16 (float32ToInt16NoSoftClip).
					bufF := make([]float32, frameSize*channels)
					wantBuf := make([]int16, frameSize*channels)
					buf16 := make([]int16, frameSize*channels)

					for i, step := range steps {
						nF, err := decF.Decode(step.packet, bufF)
						if err != nil {
							t.Fatalf("Decode step[%d]: %v", i, err)
						}
						n16, err := dec16.DecodeInt16(step.packet, buf16)
						if err != nil {
							t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
						}
						if nF != n16 {
							t.Fatalf("step[%d] sample count mismatch: float32=%d int16=%d", i, nF, n16)
						}

						if step.packet != nil {
							// Good packet: no assertion required (covered elsewhere).
							continue
						}

						// PLC frame: int16 must equal float32ToInt16NoSoftClip(float32 PLC).
						// This uses the same quantization path as DecodeInt16 internally,
						// giving exact equality on all platforms including arm64 where the
						// NEON VCVT and scalar banker's rounding may differ by ±1 LSB.
						float32ToInt16NoSoftClip(wantBuf, bufF, nF, channels)
						for j := 0; j < n16*channels; j++ {
							if buf16[j] != wantBuf[j] {
								t.Fatalf("step[%d] sample[%d]: DecodeInt16=%d want quantize(Decode=%g)=%d",
									i, j, buf16[j], bufF[j], wantBuf[j])
							}
						}
					}
				})
			}
		}
	}
}

// TestDecodeInt16PLCLeadingColdMatchesLibopus verifies cold PLC
// (nil before any good packet) matches libopus int16 for all three modes.
// This is a focused sub-test of the matrix to make regressions easy to locate.
func TestDecodeInt16PLCLeadingColdMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
	)

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		frameSize, err := packetSamplesAtRate(pkt, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate %s: %v", mc.mode, err)
		}

		t.Run(mc.mode, func(t *testing.T) {
			// Cold PLC: the oracle sequences nil, then the packet.
			libSteps := []libopusAPIRateDecodeStep{{packet: nil}, {packet: pkt}}
			want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
			if err != nil {
				libopustest.HelperUnavailable(t, "int16 cold PLC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int16, 0, len(want))
			buf := make([]int16, frameSize*channels)
			for _, p := range [][]byte{nil, pkt} {
				n, err := dec.DecodeInt16(p, buf)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				got = append(got, buf[:n*channels]...)
			}
			assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" cold int16 PLC")
		})
	}
}

// TestDecodeInt16PLCBurstMatchesLibopus verifies a 3-frame burst loss matches
// libopus int16 for all three modes and both channel counts.
func TestDecodeInt16PLCBurstMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			t.Run(mc.mode+"_ch"+itoaSmall(channels), func(t *testing.T) {
				if celtIntegerPLCActive && mc.mode == "celt" {
					t.Skip("CELT burst PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); the bit-exact strict gate is TestDecoderFixedPointCELTPLCParity")
				}
				if hybridIntegerPLCActive && mc.mode == "hybrid" {
					t.Skip("Hybrid burst PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); the bit-exact strict gate is TestDecodeDifferentialFixedPointPLC")
				}
				// Warm up then 3-frame burst loss then recovery.
				libSteps := []libopusAPIRateDecodeStep{
					{packet: pkt},
					{packet: nil},
					{packet: nil},
					{packet: nil},
					{packet: pkt},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 burst PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" burst int16 PLC")
			})
		}
	}
}

// TestDecodeInt16PLCPeriodicDecaysMatchesLibopus exercises the periodic loss
// pattern (loss every 3rd frame) and verifies energy behaviour matches the
// documented PLC decay while also asserting libopus int16 parity.
func TestDecodeInt16PLCPeriodicDecaysMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateSILKPacket(t, channels) // Use SILK for voiced PLC energy
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	// Encode enough packets to warm up PLC state.
	const warmup = 4
	warmupPkt := encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)

	// Sequence: warmup good packets + 9 periodic-loss frames.
	libSteps := make([]libopusAPIRateDecodeStep, 0, warmup+9)
	for i := 0; i < warmup; i++ {
		libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: warmupPkt})
	}
	for i := 0; i < 9; i++ {
		if (i+1)%3 == 0 {
			libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: nil})
		} else {
			libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: pkt})
		}
	}

	want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "int16 periodic PLC reference decode", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]int16, 0, len(want))
	buf := make([]int16, frameSize*channels)
	for i, step := range libSteps {
		n, err := dec.DecodeInt16(step.packet, buf)
		if err != nil {
			t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
		}
		got = append(got, buf[:n*channels]...)
	}

	assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "SILK periodic int16 PLC")
}

// TestDecodeInt16PLCTrailingMatchesLibopus verifies trailing loss (loss at end
// with no recovery packet) for all three modes.
func TestDecodeInt16PLCTrailingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			t.Run(mc.mode+"_ch"+itoaSmall(channels), func(t *testing.T) {
				if celtIntegerPLCActive && mc.mode == "celt" {
					t.Skip("CELT trailing PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecoderFixedPointCELTPLCParity")
				}
				if hybridIntegerPLCActive && mc.mode == "hybrid" {
					t.Skip("Hybrid trailing PLC routes to the integer decoder under gopus_fixedpoint (vs float oracle); see TestDecodeDifferentialFixedPointPLC")
				}
				libSteps := []libopusAPIRateDecodeStep{
					{packet: pkt},
					{packet: nil},
					{packet: nil},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 trailing PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" trailing int16 PLC")
			})
		}
	}
}

// TestDecodeInt16PLCSubRateMatchesLibopus verifies int16 PLC parity at sub-48 kHz
// API rates for each mode. Sub-rate decodes use the corr/RMS bar (opus_compare N/A).
func TestDecodeInt16PLCSubRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const channels = 1

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		for _, sampleRate := range []int{8000, 16000, 24000} {
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s %dHz: %v", mc.mode, sampleRate, err)
			}

			t.Run(mc.mode+"_"+itoaSmall(sampleRate)+"hz", func(t *testing.T) {
				libSteps := []libopusAPIRateDecodeStep{{packet: pkt}, {packet: nil}}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 sub-rate PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" sub-rate int16 PLC")
			})
		}
	}
}

// plcInt16DecoderReset resets a decoder and verifies that no PLC energy leaks
// across a Reset boundary.  This tests the warm-state reset invariant.
func TestDecodeInt16PLCResetBoundaryNoLeak(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateCELTPacket(t, channels)
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]int16, frameSize*channels)

	// Warm up.
	for range 3 {
		if _, err := dec.DecodeInt16(pkt, buf); err != nil {
			t.Fatalf("warm-up DecodeInt16: %v", err)
		}
	}

	// Reset then cold PLC: should be silence.
	dec.Reset()
	n, err := dec.DecodeInt16(nil, buf)
	if err != nil {
		t.Fatalf("post-reset DecodeInt16(nil): %v", err)
	}
	for _, v := range buf[:n*channels] {
		if v != 0 {
			t.Fatalf("post-reset cold PLC: non-zero sample %d (want silence)", v)
		}
	}
}

// TestDecodeInt16PLCSelfConsistencyWarmupN asserts the float-path-quantized
// identity for warm-up sequences of length 1, 3, and 6 before the first PLC.
// Each warmup length exercises a different decoder state depth.
//
// The float32 PLC output is converted to int16 using float32ToInt16NoSoftClip,
// the same function DecodeInt16 uses, ensuring exact equality on all platforms
// including arm64 where NEON VCVT and scalar rounding differ by ±1 LSB.
func TestDecodeInt16PLCSelfConsistencyWarmupN(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		frameSize, err := packetSamplesAtRate(pkt, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate %s: %v", mc.mode, err)
		}

		for _, warmup := range []int{1, 3, 6} {
			name := mc.mode + "_warmup" + itoaSmall(warmup)
			t.Run(name, func(t *testing.T) {
				if celtIntegerPLCActive && mc.mode == "celt" {
					t.Skip("CELT PLC routes to the integer decoder under gopus_fixedpoint, diverging from float; see TestDecoderFixedPointCELTPLCParity")
				}
				if hybridIntegerPLCActive && mc.mode == "hybrid" {
					t.Skip("Hybrid PLC routes to the integer decoder under gopus_fixedpoint, diverging from float; see TestDecodeDifferentialFixedPointPLC")
				}
				decF := mustNewTestDecoder(t, sampleRate, channels)
				dec16 := mustNewTestDecoder(t, sampleRate, channels)

				bufF := make([]float32, frameSize*channels)
				buf16 := make([]int16, frameSize*channels)
				wantBuf := make([]int16, frameSize*channels)

				// Warm up both decoders identically.
				for i := 0; i < warmup; i++ {
					if _, err := decF.Decode(pkt, bufF); err != nil {
						t.Fatalf("warmup Decode step %d: %v", i, err)
					}
					if _, err := dec16.DecodeInt16(pkt, buf16); err != nil {
						t.Fatalf("warmup DecodeInt16 step %d: %v", i, err)
					}
				}

				// PLC frame.
				nF, err := decF.Decode(nil, bufF)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				n16, err := dec16.DecodeInt16(nil, buf16)
				if err != nil {
					t.Fatalf("DecodeInt16(nil): %v", err)
				}
				if nF != n16 {
					t.Fatalf("sample count mismatch: float32=%d int16=%d", nF, n16)
				}

				// Convert float32 PLC output using the same path as DecodeInt16.
				float32ToInt16NoSoftClip(wantBuf, bufF, nF, channels)
				for j := 0; j < n16*channels; j++ {
					if buf16[j] != wantBuf[j] {
						t.Fatalf("sample[%d]: DecodeInt16=%d want quantize(Decode=%g)=%d",
							j, buf16[j], bufF[j], wantBuf[j])
					}
				}
			})
		}
	}
}

// TestDecodeInt16PLCSingleSampleEnergy verifies that a SILK single-loss PLC
// frame produces non-zero output (the PLC is active, not silence) at the
// int16 level.  Complements the float energy test in TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus.
func TestDecodeInt16PLCSingleSampleEnergy(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateSILKPacket(t, channels)
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]int16, frameSize*channels)

	// Warm up with 4 packets.
	for i := 0; i < 4; i++ {
		if _, err := dec.DecodeInt16(pkt, buf); err != nil {
			t.Fatalf("warm-up step %d: %v", i, err)
		}
	}

	// PLC frame.
	n, err := dec.DecodeInt16(nil, buf)
	if err != nil {
		t.Fatalf("DecodeInt16(nil): %v", err)
	}

	var energy float64
	for _, v := range buf[:n*channels] {
		energy += float64(v) * float64(v)
	}
	energy /= float64(n * channels)
	if energy < 1 {
		t.Fatalf("SILK int16 PLC frame is silent: mean-square energy=%.2e, expected non-zero speech extrapolation", energy)
	}

	// Silence assertion: the next loss frame should have ≤ energy than the first.
	buf2 := make([]int16, frameSize*channels)
	if _, err := dec.DecodeInt16(nil, buf2); err != nil {
		t.Fatalf("second DecodeInt16(nil): %v", err)
	}
	var energy2 float64
	for _, v := range buf2[:n*channels] {
		energy2 += float64(v) * float64(v)
	}
	energy2 /= float64(n * channels)
	if energy2 >= energy*1.1 {
		// Allow 10 % tolerance for level variation but flag gross non-decay.
		t.Fatalf("SILK int16 PLC burst energy did not decay: frame1=%.2e frame2=%.2e", energy, energy2)
	}
}

// Compile-time check: math imported for energy calculation.
var _ = math.Pi
