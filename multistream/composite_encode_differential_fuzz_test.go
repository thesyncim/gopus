// composite_encode_differential_fuzz_test.go — differential fuzz for the
// COMPOSITE encode paths (multistream surround + projection ambisonics) against
// the SAME-ARCH libopus oracles, asserting byte-exact packets across a broad,
// seeded configuration matrix.
//
// This is the multistream analog of the single-stream
// gopus/encode_differential_fuzz_test.go. It complements the fixed-config
// surround/projection parity tests (surround_encode_libopus_parity_test.go,
// projection_encode_libopus_parity_test.go) by sweeping many more
// layouts/orders × bitrates × frame-sizes × VBR/CVBR/CBR × complexity points
// with SEEDED pseudo-random multichannel PCM, so the composite rate-split /
// surround-masking / projection-mixing decisions are exercised on a wide range
// of inter-channel energy distributions rather than a single fixed tone bed.
//
// Divergence classification (identical policy to the single-stream encode
// fuzz and the existing composite parity tests):
//
//   - stream/coupled layout, demixing matrix and gain mismatch: HARD FAIL on
//     every arch. These are integer-derived composite-layer outputs with no
//     float boundary, so any divergence is a real composite bug.
//
//   - per-stream TOC config-list divergence (each stream's mode/bandwidth/frame
//     duration): a per-stream mode-DECISION difference. The single-stream encode
//     fuzz proved this is not a same-arch logic bug for the per-stream encoder
//     (0 TOC flips / 1660 specs); when it appears here on the moderate
//     per-stream rates surround/ambisonics streams receive, it is the same
//     near-tie classification residual, logged not failed.
//
//   - packet byte mismatch with MATCHING per-stream TOC configs: the documented
//     darwin/arm64 ≤1-ULP CELT float-analysis boundary
//     (project_arm64_celt_1ulp_drift). On amd64 (the CI gate) the float path is
//     exact, so this is a HARD FAIL there; on arm64 it is logged as the
//     documented per-arch residual.
//
// Run the full sweep with:
//   GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test \
//     -run 'TestSurroundEncodeDifferentialFuzz|TestProjectionEncodeDifferentialFuzz' ./multistream/

package multistream

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	compositeApplication    = 2049  // OPUS_APPLICATION_AUDIO
	compositeBandwidthAuto  = -1000 // OPUS_AUTO
	compositeMaxPacketBytes = 4000
	compositeSampleRate     = 48000
)

// fuzzBudget shrinks the sweep under -short so CI stays fast while keeping a
// substantial matrix otherwise.
func fuzzBudget(full int) int {
	if testing.Short() {
		b := full / 6
		if b < 8 {
			b = 8
		}
		return b
	}
	return full
}

// seededMultichannelPCM builds a deterministic pseudo-random multichannel PCM
// buffer: a per-channel tone bed (so the composite masking/rate-split sees
// structured inter-channel energy) plus a seeded noise floor and a slow
// amplitude drift, which together stress the near-tie quantization decisions far
// more than a pure tone bed. Amplitude is bounded to [-0.9, 0.9] to stay inside
// the float PCM range both encoders expect.
func seededMultichannelPCM(seed int64, channels, frameSize, frameCount int) []float32 {
	rng := rand.New(rand.NewSource(seed))
	baseFreqs := make([]float64, channels)
	noiseAmp := make([]float64, channels)
	phase := make([]float64, channels)
	for ch := 0; ch < channels; ch++ {
		baseFreqs[ch] = 90.0 + rng.Float64()*900.0
		noiseAmp[ch] = 0.02 + rng.Float64()*0.10
		phase[ch] = rng.Float64() * 2 * math.Pi
	}
	driftFreq := 0.7 + rng.Float64()*2.0
	total := channels * frameSize * frameCount
	pcm := make([]float32, total)
	n := frameSize * frameCount
	for s := 0; s < n; s++ {
		tt := float64(s) / float64(compositeSampleRate)
		amp := 0.22 + 0.12*math.Sin(2*math.Pi*driftFreq*tt)
		for ch := 0; ch < channels; ch++ {
			v := amp * math.Sin(2*math.Pi*baseFreqs[ch]*tt+phase[ch])
			v += noiseAmp[ch] * (rng.Float64()*2 - 1)
			if v > 0.9 {
				v = 0.9
			} else if v < -0.9 {
				v = -0.9
			}
			pcm[s*channels+ch] = float32(v)
		}
	}
	return pcm
}

// surroundFuzzSpec is one point in the surround composite-encode config space.
type surroundFuzzSpec struct {
	name          string
	channels      int
	frameSize     int
	frameCount    int
	bitrate       int
	complexity    int
	vbr           bool
	vbrConstraint bool
	seed          int64
}

// buildSurroundFuzzSweep enumerates the surround composite-encode matrix:
//   - layouts: mono, stereo, quad, 5.0, 5.1, 6.1, 7.1 (mapping family 1)
//   - frame sizes: 2.5/5/10/20/40/60 ms (120…2880 samples at 48 kHz)
//   - bitrates: low … high, including the per-channel-floor and high-rate caps
//   - rate control: CBR, constrained VBR, unconstrained VBR
//   - complexity: 0, 5, 10
//   - seeded PCM varied per spec
func buildSurroundFuzzSweep() []surroundFuzzSpec {
	layouts := []struct {
		name     string
		channels int
	}{
		{"mono", 1}, {"stereo", 2}, {"quad", 4},
		{"surround_5_0", 5}, {"surround_5_1", 6}, {"surround_6_1", 7}, {"surround_7_1", 8},
	}
	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	bitrates := []int{32000, 64000, 128000, 256000, 384000, 510000}
	type rc struct {
		vbr        bool
		constraint bool
	}
	rcModes := []rc{{false, false}, {true, true}, {true, false}}
	complexities := []int{0, 5, 10}

	var specs []surroundFuzzSpec
	var seed int64 = 0x5150
	for _, layout := range layouts {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, m := range rcModes {
					for _, cx := range complexities {
						seed++
						specs = append(specs, surroundFuzzSpec{
							name:          fmt.Sprintf("%s/fs%d/br%d/vbr%t/c%t/cx%d", layout.name, fs, br, m.vbr, m.constraint, cx),
							channels:      layout.channels,
							frameSize:     fs,
							frameCount:    5,
							bitrate:       br,
							complexity:    cx,
							vbr:           m.vbr,
							vbrConstraint: m.constraint,
							seed:          seed,
						})
					}
				}
			}
		}
	}
	return specs
}

// TestSurroundEncodeDifferentialFuzz drives gopus and the libopus surround
// oracle with identical seeded PCM across the broad config matrix and asserts
// byte-exact packets, classifying every divergence (see file header).
func TestSurroundEncodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	specs := buildSurroundFuzzSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	var (
		tested        int
		layoutFails   int
		byteResiduals int // arm64 documented float-boundary frames
		byteFails     int // amd64 hard failures
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		tested++
		t.Run(spec.name, func(t *testing.T) {
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)

			ref, err := encodeLibopusSurround(compositeSampleRate, spec.channels, 1, compositeApplication,
				spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
				spec.frameSize, spec.frameCount, compositeMaxPacketBytes, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream surround reference encode", err)
				return
			}

			enc, err := NewEncoderDefault(compositeSampleRate, spec.channels)
			if err != nil {
				t.Fatalf("NewEncoderDefault(%d): %v", spec.channels, err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				layoutFails++
				t.Fatalf("stream layout mismatch: gopus streams=%d coupled=%d libopus streams=%d coupled=%d",
					enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			enc.SetBitrate(spec.bitrate)
			enc.SetVBR(spec.vbr)
			enc.SetVBRConstraint(spec.vbrConstraint)
			enc.SetComplexity(spec.complexity)
			enc.SetBandwidthAuto()

			for i := 0; i < spec.frameCount; i++ {
				start := i * spec.frameSize * spec.channels
				frame := pcm[start : start+spec.frameSize*spec.channels]
				got, err := enc.EncodeFloat32WithAnalysisMaxBytes(frame, spec.frameSize, frame, compositeMaxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d: gopus Encode: %v", i, err)
				}
				want := ref.packets[i]
				if bytes.Equal(got, want) {
					continue
				}
				mismatch := firstByteMismatch(got, want)
				// Compare per-stream TOC configs to separate a mode-decision
				// residual from a pure float-boundary byte drift.
				gotCfgs := perStreamConfigs(got, enc.Streams())
				wantCfgs := perStreamConfigs(want, ref.streams)
				if gotCfgs != nil && wantCfgs != nil && !sameInts(gotCfgs, wantCfgs) {
					if runtime.GOARCH == "amd64" {
						byteFails++
						t.Errorf("frame %d: per-stream MODE-DECISION divergence gopus=%v libopus=%v (UNEXPECTED on amd64)",
							i, gotCfgs, wantCfgs)
						continue
					}
					byteResiduals++
					t.Logf("frame %d: per-stream mode-decision residual gopus=%v libopus=%v — arm64 near-tie", i, gotCfgs, wantCfgs)
					continue
				}
				if runtime.GOARCH == "amd64" {
					byteFails++
					t.Errorf("frame %d: surround packet BYTE MISMATCH at byte %d (len g=%d o=%d) — matching per-stream modes (UNEXPECTED on amd64; bit-exact required)",
						i, mismatch, len(got), len(want))
					continue
				}
				byteResiduals++
				t.Logf("frame %d: surround packet differs at byte %d (len g=%d o=%d) — documented arm64 ≤1-ULP CELT float boundary",
					i, mismatch, len(got), len(want))
			}
		})
	}
	t.Logf("surround encode differential sweep: %d/%d specs; arch=%s; layout-fails=%d amd64-byte-fails=%d arm64-float-residuals=%d",
		tested, len(specs), runtime.GOARCH, layoutFails, byteFails, byteResiduals)
}

// projectionFuzzSpec is one point in the projection composite-encode config space.
type projectionFuzzSpec struct {
	name          string
	channels      int // 4=FOA(order1), 9=SOA(order2), 16=TOA(order3)
	frameSize     int
	frameCount    int
	bitrate       int
	complexity    int
	vbr           bool
	vbrConstraint bool
	sampleFormat  int // 0=float32, 1=int16
	seed          int64
}

// buildProjectionFuzzSweep enumerates the projection composite-encode matrix:
//   - orders: FOA (4ch), SOA (9ch), TOA (16ch, family-3 third order)
//   - frame sizes: 2.5/5/10/20/40/60 ms
//   - bitrates: low … high
//   - rate control: CBR, constrained VBR, unconstrained VBR
//   - sample format: float32 + int16
//   - seeded PCM varied per spec
func buildProjectionFuzzSweep() []projectionFuzzSpec {
	orders := []struct {
		name     string
		channels int
	}{
		{"foa-4ch", 4}, {"soa-9ch", 9}, {"toa-16ch", 16},
	}
	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	bitrates := []int{64000, 128000, 256000, 384000}
	type rc struct {
		vbr        bool
		constraint bool
	}
	rcModes := []rc{{false, false}, {true, true}, {true, false}}
	formats := []int{0, 1}

	var specs []projectionFuzzSpec
	var seed int64 = 0x9001
	for _, order := range orders {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, m := range rcModes {
					for _, sf := range formats {
						seed++
						specs = append(specs, projectionFuzzSpec{
							name:          fmt.Sprintf("%s/fs%d/br%d/vbr%t/c%t/fmt%d", order.name, fs, br, m.vbr, m.constraint, sf),
							channels:      order.channels,
							frameSize:     fs,
							frameCount:    5,
							bitrate:       br,
							complexity:    10,
							vbr:           m.vbr,
							vbrConstraint: m.constraint,
							sampleFormat:  sf,
							seed:          seed,
						})
					}
				}
			}
		}
	}
	return specs
}

// TestProjectionEncodeDifferentialFuzz drives gopus and the libopus projection
// oracle with identical seeded ambisonics PCM across the broad config matrix and
// asserts byte-exact layout/demixing/packets, classifying every divergence.
func TestProjectionEncodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	specs := buildProjectionFuzzSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	var (
		tested        int
		layoutFails   int
		demixFails    int
		modeResiduals int
		byteResiduals int
		byteFails     int
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		tested++
		t.Run(spec.name, func(t *testing.T) {
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
			var pcm16 []int16
			gopusPCM := pcm
			if spec.sampleFormat == 1 {
				pcm16 = floatToInt16(pcm)
				gopusPCM = make([]float32, len(pcm16))
				for i, s := range pcm16 {
					gopusPCM[i] = float32(s) / 32768.0
				}
			}

			ref, err := encodeLibopusProjection(compositeSampleRate, spec.channels, compositeApplication,
				spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
				spec.frameSize, spec.frameCount, compositeMaxPacketBytes, spec.sampleFormat, pcm, pcm16)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference encode", err)
				return
			}

			enc, err := NewProjectionEncoder(compositeSampleRate, spec.channels)
			if err != nil {
				t.Fatalf("NewProjectionEncoder(%d): %v", spec.channels, err)
			}
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				layoutFails++
				t.Fatalf("stream layout mismatch: gopus streams=%d coupled=%d libopus streams=%d coupled=%d",
					enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			// Demixing matrix + gain are integer outputs — byte-exact on every arch.
			if !bytes.Equal(enc.GetDemixingMatrix(), ref.demixing) {
				demixFails++
				t.Fatalf("demixing matrix mismatch (gopus len=%d libopus len=%d)", len(enc.GetDemixingMatrix()), len(ref.demixing))
			}
			if enc.DemixingMatrixGain() != ref.demixingGain {
				demixFails++
				t.Fatalf("demixing gain mismatch: gopus=%d libopus=%d", enc.DemixingMatrixGain(), ref.demixingGain)
			}

			enc.SetBitrate(spec.bitrate)
			enc.SetVBR(spec.vbr)
			enc.SetVBRConstraint(spec.vbrConstraint)
			enc.SetComplexity(spec.complexity)
			enc.SetBandwidthAuto()

			for i := 0; i < spec.frameCount; i++ {
				start := i * spec.frameSize * spec.channels
				frame := gopusPCM[start : start+spec.frameSize*spec.channels]
				got, err := enc.EncodeFloat32WithAnalysisMaxBytes(frame, spec.frameSize, frame, compositeMaxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d: gopus Encode: %v", i, err)
				}
				want := ref.packets[i]
				if bytes.Equal(got, want) {
					continue
				}
				mismatch := firstByteMismatch(got, want)
				gotCfgs := perStreamConfigs(got, enc.Streams())
				wantCfgs := perStreamConfigs(want, ref.streams)
				if gotCfgs != nil && wantCfgs != nil && !sameInts(gotCfgs, wantCfgs) {
					if runtime.GOARCH == "amd64" && spec.sampleFormat == 0 {
						byteFails++
						t.Errorf("frame %d: per-stream MODE-DECISION divergence gopus=%v libopus=%v (UNEXPECTED on amd64 float path)",
							i, gotCfgs, wantCfgs)
						continue
					}
					modeResiduals++
					t.Logf("frame %d: per-stream mode-decision residual gopus=%v libopus=%v", i, gotCfgs, wantCfgs)
					continue
				}
				// int16 path: libopus applies the mixing matrix on the ~2^30 integer
				// products then divides; gopus mixes the ~2^15 pre-divided floats.
				// Equal in exact arithmetic, but the float32 accumulation order
				// differs, so a frame can drift even on amd64. Log it (the layout +
				// demixing assertions above already gate the projection layer).
				if runtime.GOARCH == "amd64" && spec.sampleFormat == 0 {
					byteFails++
					t.Errorf("frame %d: projection packet BYTE MISMATCH at byte %d (len g=%d o=%d) — matching per-stream modes (UNEXPECTED on amd64 float path)",
						i, mismatch, len(got), len(want))
					continue
				}
				byteResiduals++
				t.Logf("frame %d: projection packet differs at byte %d (len g=%d o=%d) — arm64 float boundary / int16 accumulation-order residual",
					i, mismatch, len(got), len(want))
			}
		})
	}
	t.Logf("projection encode differential sweep: %d/%d specs; arch=%s; layout-fails=%d demix-fails=%d mode-residuals=%d amd64-byte-fails=%d float-residuals=%d",
		tested, len(specs), runtime.GOARCH, layoutFails, demixFails, modeResiduals, byteFails, byteResiduals)
}
