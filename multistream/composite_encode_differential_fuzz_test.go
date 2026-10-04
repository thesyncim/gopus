// The composite encode sweeps compare complete packets and entropy-coder final
// ranges with the matching libopus 1.6.1 public entry on the same machine.
// Every selected layout, duration, rate-control mode, sample format, and seeded
// PCM sequence is a strict byte-exact case.

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
		b := max(full/6, 8)
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
	for ch := range channels {
		baseFreqs[ch] = 90.0 + rng.Float64()*900.0
		noiseAmp[ch] = 0.02 + rng.Float64()*0.10
		phase[ch] = rng.Float64() * 2 * math.Pi
	}
	driftFreq := 0.7 + rng.Float64()*2.0
	total := channels * frameSize * frameCount
	pcm := make([]float32, total)
	n := frameSize * frameCount
	for s := range n {
		tt := float64(s) / float64(compositeSampleRate)
		amp := 0.22 + 0.12*math.Sin(2*math.Pi*driftFreq*tt)
		for ch := range channels {
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

// TestSurroundEncodeDifferentialFuzz checks every packet and final range from
// the selected surround matrix against opus_multistream_encode_float.
func TestSurroundEncodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	specs := buildSurroundFuzzSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}
	limit := min(4, runtime.GOMAXPROCS(0))
	semaphore := make(chan struct{}, limit)
	for idx, tested := 0, 0; idx < len(specs) && tested < budget; idx, tested = idx+stride, tested+1 {
		spec := specs[idx]
		t.Run(spec.name, func(t *testing.T) {
			t.Parallel()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			runSurroundFuzzSpecParity(t, spec, false)
		})
	}
}

func runSurroundFuzzSpecParity(t *testing.T, spec surroundFuzzSpec, failFast bool) {
	t.Helper()
	pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
	ref, err := encodeLibopusSurround(compositeSampleRate, spec.channels, 1, compositeApplication,
		spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
		spec.frameSize, spec.frameCount, compositeMaxPacketBytes, pcm, false)
	if err != nil {
		t.Fatalf("live C surround encode: %v", err)
	}
	if len(ref.packets) != spec.frameCount || len(ref.ranges) != spec.frameCount {
		t.Fatalf("live C records: packets=%d ranges=%d want=%d", len(ref.packets), len(ref.ranges), spec.frameCount)
	}
	enc, err := NewEncoderDefault(compositeSampleRate, spec.channels)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}
	enc.SetBitrate(spec.bitrate)
	enc.SetVBR(spec.vbr)
	enc.SetVBRConstraint(spec.vbrConstraint)
	enc.SetComplexity(spec.complexity)
	enc.SetBandwidthAuto()
	for frame := range spec.frameCount {
		start := frame * spec.frameSize * spec.channels
		input := pcm[start : start+spec.frameSize*spec.channels]
		got, err := encodePacketMax(enc, input, spec.frameSize, input, compositeMaxPacketBytes)
		if err != nil {
			t.Fatalf("frame %d Go encode: %v", frame, err)
		}
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			message := fmt.Sprintf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x configs Go/C=%v/%v", frame,
				firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame], perStreamConfigs(got, enc.Streams()), perStreamConfigs(ref.packets[frame], ref.streams))
			if failFast {
				t.Fatal(message)
			}
			t.Error(message)
		}
	}
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

// TestProjectionEncodeDifferentialFuzz compares the float and short projection
// entries with their matching libopus 1.6.1 public entries for every selected
// frame, packet byte, and final range.
func TestProjectionEncodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)
	specs := buildProjectionFuzzSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}
	limit := min(4, runtime.GOMAXPROCS(0))
	semaphore := make(chan struct{}, limit)
	for idx, tested := 0, 0; idx < len(specs) && tested < budget; idx, tested = idx+stride, tested+1 {
		spec := specs[idx]
		t.Run(spec.name, func(t *testing.T) {
			t.Parallel()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			runProjectionFuzzSpecParity(t, spec, false)
		})
	}
}

func runProjectionFuzzSpecParity(t *testing.T, spec projectionFuzzSpec, failFast bool) {
	t.Helper()
	pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
	var pcm16 []int16
	if spec.sampleFormat == 1 {
		pcm16 = floatToInt16(pcm)
	}
	ref, err := encodeLibopusProjection(compositeSampleRate, spec.channels, compositeApplication,
		spec.bitrate, spec.vbr, spec.vbrConstraint, spec.complexity, compositeBandwidthAuto,
		spec.frameSize, spec.frameCount, compositeMaxPacketBytes, spec.sampleFormat, pcm, pcm16)
	if err != nil {
		t.Fatalf("live C projection encode: %v", err)
	}
	if len(ref.packets) != spec.frameCount || len(ref.ranges) != spec.frameCount {
		t.Fatalf("live C records: packets=%d ranges=%d want=%d", len(ref.packets), len(ref.ranges), spec.frameCount)
	}
	enc, err := NewProjectionEncoder(compositeSampleRate, spec.channels)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
		t.Fatalf("layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
	}
	if !bytes.Equal(enc.GetDemixingMatrix(), ref.demixing) || enc.DemixingMatrixGain() != ref.demixingGain {
		t.Fatal("demixing matrix or gain differs from C")
	}
	enc.SetBitrate(spec.bitrate)
	enc.SetVBR(spec.vbr)
	enc.SetVBRConstraint(spec.vbrConstraint)
	enc.SetComplexity(spec.complexity)
	enc.SetBandwidthAuto()
	out := make([]byte, compositeMaxPacketBytes)
	for frame := range spec.frameCount {
		start := frame * spec.frameSize * spec.channels
		var got []byte
		if spec.sampleFormat == 1 {
			input := pcm16[start : start+spec.frameSize*spec.channels]
			n, err := enc.EncodeInt16WithAnalysis(input, spec.frameSize, input, out)
			if err != nil {
				t.Fatalf("frame %d Go short encode: %v", frame, err)
			}
			got = out[:n]
		} else {
			input := pcm[start : start+spec.frameSize*spec.channels]
			got, err = encodePacketMax(enc, input, spec.frameSize, input, compositeMaxPacketBytes)
			if err != nil {
				t.Fatalf("frame %d Go float encode: %v", frame, err)
			}
		}
		if !bytes.Equal(got, ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			message := fmt.Sprintf("frame %d: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x configs Go/C=%v/%v", frame,
				firstByteMismatch(got, ref.packets[frame]), len(got), len(ref.packets[frame]),
				enc.GetFinalRange(), ref.ranges[frame], perStreamConfigs(got, enc.Streams()), perStreamConfigs(ref.packets[frame], ref.streams))
			if failFast {
				t.Fatal(message)
			}
			t.Error(message)
		}
	}
}
