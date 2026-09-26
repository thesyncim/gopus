// ms_decode_differential_fuzz_test.go — broad DECODE differential fuzz for the
// multistream (surround + discrete) and projection (ambisonics) decode paths
// against the SAME-ARCH libopus decode oracles, asserting sample-exact PCM
// across a wide, seeded multichannel configuration matrix on CLEAN packets
// (no packet loss / FEC / PLC).
//
// This is the decode-side analog of composite_encode_differential_fuzz_test.go.
// Whereas the encode fuzz proves gopus emits the same packets, this proves gopus
// DECODES the same PCM as libopus from byte-identical input. Both decoders
// consume the SAME packets (produced once by the libopus surround/projection
// encode oracle, or — in the gopus-emitted variant — by the gopus encoder), so
// the only thing under test is the decode path:
//
//   - per-stream Opus decode (SILK / CELT / Hybrid),
//   - inter-stream channel mapping + stereo coupling (mapping families 1/255),
//   - the surround per-stream decode gain, and
//   - the projection demixing-matrix application (family 3).
//
// All CLEAN-packet frames, including per-stream mode transitions, are compared
// strictly. Transition classification is diagnostic context only: every float
// output must have the same raw bits as the matched libopus reference, and every
// integer sample must match exactly. Packet-loss / PLC / FEC concealment is owned
// by a sibling single-stream-loss harness and is not exercised here.
//
// Run the full sweep with:
//   GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test \
//     -run 'TestMultistreamSurroundDecodeDifferentialFuzz|TestMultistreamDiscreteDecodeDifferentialFuzz|TestProjectionDecodeDifferentialFuzz|TestMultistreamGopusEncodedDecodeDifferentialFuzz' \
//     ./multistream/

package multistream

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// streamModeClass classifies an Opus TOC config (TOC>>3) into the coding-mode
// family used for transition detection and diagnostics.
func streamModeClass(cfg int) string {
	switch {
	case cfg < 0:
		return "none"
	case cfg < 12:
		return "SILK"
	case cfg < 16:
		return "Hybrid"
	default:
		return "CELT"
	}
}

// isCELTConfig reports whether a TOC config is pure-CELT (>=16).
func isCELTConfig(cfg int) bool { return cfg >= 16 }

// transitionFrameMask returns, for each packet index, whether decoding that
// packet is a per-stream mode-transition frame: at least one stream crosses the
// CELT_ONLY boundary versus that stream's previous frame. libopus applies its
// pcm_transition crossfade on those frames. The classification labels failures
// and reports mode counts; it does not alter strict PCM comparisons.
func transitionFrameMask(packets [][]byte, streams int) (mask []bool, modeCounts map[string]int) {
	mask = make([]bool, len(packets))
	modeCounts = map[string]int{}
	prevCELT := make([]int, streams) // -1 unknown, 0 non-CELT, 1 CELT
	for i := range prevCELT {
		prevCELT[i] = -1
	}
	for pi, pkt := range packets {
		sp, err := parseMultistreamPacket(pkt, streams)
		if err != nil {
			continue
		}
		for s := 0; s < streams && s < len(sp); s++ {
			cfg := -1
			if len(sp[s]) > 0 {
				cfg = int(sp[s][0] >> 3)
			}
			modeCounts[streamModeClass(cfg)]++
			if cfg < 0 {
				continue // empty per-stream frame (DTX/PLC): no mode change recorded
			}
			cur := 0
			if isCELTConfig(cfg) {
				cur = 1
			}
			if prevCELT[s] != -1 && prevCELT[s] != cur {
				mask[pi] = true
			}
			prevCELT[s] = cur
		}
	}
	return mask, modeCounts
}

// assertMSDecodeFloatFrameAware compares every interleaved float32 sample by
// raw bits. Transition classification only labels failures and summary stats.
func assertMSDecodeFloatFrameAware(t *testing.T, got, want []float32, channels, frameSize int, transition []bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	perFrame := frameSize * channels
	for f := 0; f*perFrame < len(got); f++ {
		isTransition := f < len(transition) && transition[f]
		base := f * perFrame
		frameMax := 0.0
		firstIdx := -1
		mism := 0
		for i := base; i < base+perFrame && i < len(got); i++ {
			if math.Float32bits(got[i]) == math.Float32bits(want[i]) {
				continue
			}
			d := math.Abs(float64(got[i] - want[i]))
			if d > frameMax {
				frameMax = d
			}
			if firstIdx < 0 {
				firstIdx = i
			}
			mism++
		}
		if mism == 0 {
			continue
		}
		region := "steady"
		if isTransition {
			region = "transition"
		}
		t.Fatalf("%s frame %d (%s): float bits differ: %d/%d samples, maxAbs=%g (firstIdx=%d got=%08x %.10g want=%08x %.10g)",
			label, f, region, mism, perFrame, frameMax, firstIdx, math.Float32bits(got[firstIdx]), got[firstIdx], math.Float32bits(want[firstIdx]), want[firstIdx])
	}
}

// assertMSDecodeInt16FrameAware is the int16 analog of
// assertMSDecodeFloatFrameAware.
func assertMSDecodeInt16FrameAware(t *testing.T, got, want []int16, channels, frameSize int, transition []bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: sample count mismatch gopus=%d libopus=%d", label, len(got), len(want))
	}
	perFrame := frameSize * channels
	for f := 0; f*perFrame < len(got); f++ {
		isTransition := f < len(transition) && transition[f]
		base := f * perFrame
		frameMax := 0
		firstIdx := -1
		mism := 0
		for i := base; i < base+perFrame && i < len(got); i++ {
			if got[i] == want[i] {
				continue
			}
			d := int(got[i]) - int(want[i])
			if d < 0 {
				d = -d
			}
			if d > frameMax {
				frameMax = d
			}
			if firstIdx < 0 {
				firstIdx = i
			}
			mism++
		}
		if mism == 0 {
			continue
		}
		region := "steady"
		if isTransition {
			region = "transition"
		}
		t.Fatalf("%s frame %d (%s): int16 differs: %d/%d samples, maxAbs=%d (firstIdx=%d got=%d want=%d)",
			label, f, region, mism, perFrame, frameMax, firstIdx, got[firstIdx], want[firstIdx])
	}
}

// decodeSurroundGopusFloat32 decodes every packet through a fresh gopus
// multistream decoder (optionally with a decode gain) and concatenates the
// interleaved float32 output.
func decodeSurroundGopusFloat32(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]float32, error) {
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		return nil, fmt.Errorf("NewDecoder: %w", err)
	}
	if gainQ8 != 0 {
		if err := dec.SetGain(gainQ8); err != nil {
			return nil, fmt.Errorf("SetGain(%d): %w", gainQ8, err)
		}
	}
	var out []float32
	for i, pkt := range packets {
		frame, err := dec.DecodeToFloat32(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// decodeSurroundGopusInt16 decodes every packet through a fresh gopus
// multistream decoder (optionally with a decode gain) and concatenates the
// interleaved int16 output.
func decodeSurroundGopusInt16(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int16, error) {
	dec, err := NewDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		return nil, fmt.Errorf("NewDecoder: %w", err)
	}
	if gainQ8 != 0 {
		if err := dec.SetGain(gainQ8); err != nil {
			return nil, fmt.Errorf("SetGain(%d): %w", gainQ8, err)
		}
	}
	var out []int16
	for i, pkt := range packets {
		frame, err := dec.DecodeToInt16(pkt, frameSize)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		out = append(out, frame...)
	}
	return out, nil
}

// msDecodeFuzzSpec is one point in the multistream (family 1/255) decode config
// space.
type msDecodeFuzzSpec struct {
	name          string
	mappingFamily int
	channels      int
	frameSize     int
	frameCount    int
	bitrate       int
	vbr           bool
	vbrConstraint bool
	gainQ8        int
	int16Path     bool
	seed          int64
}

// buildSurroundDecodeFuzzSweep enumerates the surround (mapping family 1)
// CLEAN-packet decode matrix. The libopus surround encoder picks the
// stream/coupled layout + mapping; both decoders then consume identical bytes.
//
//   - layouts: mono, stereo, quad, 5.0, 5.1, 6.1, 7.1 (coupling + surround gain)
//   - frame sizes: 2.5/5/10/20/40/60 ms (120…2880 samples at 48 kHz)
//   - bitrates: low → high so per-stream coding spans SILK / Hybrid / CELT
//   - rate control: CBR, constrained VBR, unconstrained VBR
//   - decode gain: 0 and a non-zero Q8 gain (exercises OPUS_SET_GAIN parity)
//   - sample format: float32 + int16
func buildSurroundDecodeFuzzSweep() []msDecodeFuzzSpec {
	layouts := []struct {
		name     string
		channels int
	}{
		{"mono", 1}, {"stereo", 2}, {"quad", 4},
		{"surround_5_0", 5}, {"surround_5_1", 6}, {"surround_6_1", 7}, {"surround_7_1", 8},
	}
	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	bitrates := []int{16000, 32000, 64000, 128000, 256000, 384000}
	type rc struct {
		vbr        bool
		constraint bool
	}
	rcModes := []rc{{false, false}, {true, true}, {true, false}}
	gains := []int{0, 768} // 0 dB and +3 dB (Q8)
	formats := []bool{false, true}

	var specs []msDecodeFuzzSpec
	var seed int64 = 0xD3C0
	for _, layout := range layouts {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, m := range rcModes {
					for _, g := range gains {
						for _, i16 := range formats {
							seed++
							fmtTag := "f32"
							if i16 {
								fmtTag = "i16"
							}
							specs = append(specs, msDecodeFuzzSpec{
								name:          fmt.Sprintf("%s/fs%d/br%d/vbr%t/c%t/g%d/%s", layout.name, fs, br, m.vbr, m.constraint, g, fmtTag),
								mappingFamily: 1,
								channels:      layout.channels,
								frameSize:     fs,
								frameCount:    5,
								bitrate:       br,
								vbr:           m.vbr,
								vbrConstraint: m.constraint,
								gainQ8:        g,
								int16Path:     i16,
								seed:          seed,
							})
						}
					}
				}
			}
		}
	}
	return specs
}

// buildDiscreteDecodeFuzzSweep enumerates the discrete (mapping family 255)
// CLEAN-packet decode matrix: N independent mono streams, identity mapping, no
// stereo coupling and no surround masking gain — a distinct decode routing path
// from the coupled surround layouts.
func buildDiscreteDecodeFuzzSweep() []msDecodeFuzzSpec {
	channelCounts := []int{2, 3, 4, 6, 8}
	frameSizes := []int{120, 480, 960, 1920, 2880}
	bitrates := []int{24000, 64000, 128000, 256000}
	type rc struct {
		vbr        bool
		constraint bool
	}
	rcModes := []rc{{false, false}, {true, false}}
	formats := []bool{false, true}

	var specs []msDecodeFuzzSpec
	var seed int64 = 0xFF00
	for _, ch := range channelCounts {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, m := range rcModes {
					for _, i16 := range formats {
						seed++
						fmtTag := "f32"
						if i16 {
							fmtTag = "i16"
						}
						specs = append(specs, msDecodeFuzzSpec{
							name:          fmt.Sprintf("discrete_%dch/fs%d/br%d/vbr%t/c%t/%s", ch, fs, br, m.vbr, m.constraint, fmtTag),
							mappingFamily: 255,
							channels:      ch,
							frameSize:     fs,
							frameCount:    5,
							bitrate:       br,
							vbr:           m.vbr,
							vbrConstraint: m.constraint,
							gainQ8:        0,
							int16Path:     i16,
							seed:          seed,
						})
					}
				}
			}
		}
	}
	return specs
}

// runMSDecodeFuzz drives a multistream (family 1/255) decode sweep: it encodes
// each seeded multichannel PCM buffer through the libopus surround oracle, then
// decodes the SAME packets through gopus and the libopus decode oracle, asserting
// sample-exact PCM, including mode-transition frames.
func runMSDecodeFuzz(t *testing.T, specs []msDecodeFuzzSpec, label string) {
	libopustest.RequireOracle(t)

	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	var (
		tested      int
		failedSpecs int
		transFrames int
		modeTotals  = map[string]int{}
	)

	const (
		application    = 2049  // OPUS_APPLICATION_AUDIO
		bandwidthAuto  = -1000 // OPUS_AUTO
		maxPacketBytes = 4000
		sampleRate     = 48000
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		if !t.Run(spec.name, func(t *testing.T) {
			tested++
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)

			ref, err := encodeLibopusSurround(sampleRate, spec.channels, spec.mappingFamily, application,
				spec.bitrate, spec.vbr, spec.vbrConstraint, 10, bandwidthAuto,
				spec.frameSize, spec.frameCount, maxPacketBytes, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream surround reference encode", err)
				return
			}

			transition, modes := transitionFrameMask(ref.packets, ref.streams)
			for k, v := range modes {
				modeTotals[k] += v
			}
			for _, tf := range transition {
				if tf {
					transFrames++
				}
			}

			if spec.int16Path {
				got, err := decodeSurroundGopusInt16(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, ref.packets)
				if err != nil {
					t.Fatalf("gopus int16 decode: %v", err)
				}
				want, err := decodeWithLibopusReferencePacketsInt16Gain(1, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, nil, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "multistream reference decode", err)
					return
				}
				assertMSDecodeInt16FrameAware(t, got, want, spec.channels, spec.frameSize, transition, "int16/"+spec.name)
				return
			}

			got, err := decodeSurroundGopusFloat32(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, ref.packets)
			if err != nil {
				t.Fatalf("gopus float decode: %v", err)
			}
			want, err := decodeWithLibopusReferencePacketsGain(1, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, nil, ref.packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream reference decode", err)
				return
			}
			assertMSDecodeFloatFrameAware(t, got, want, spec.channels, spec.frameSize, transition, "float32/"+spec.name)
		}) {
			failedSpecs++
		}
	}
	t.Logf("%s decode differential sweep: tested=%d/%d specs; failed=%d; arch=%s; modes=%v; transition-frames=%d (all frames strict)",
		label, tested, len(specs), failedSpecs, runtime.GOARCH, modeTotals, transFrames)
}

// TestMultistreamSurroundDecodeDifferentialFuzz locks gopus multistream surround
// (mapping family 1) DECODE to sample-exact parity against the libopus
// opus_multistream_decode_float / opus_multistream_decode oracle across a broad
// layout × frame-size × bitrate × rate-control × decode-gain × sample-format
// matrix on CLEAN packets. Every decoded sample, including per-stream mode
// transitions, must match the paired libopus output exactly.
func TestMultistreamSurroundDecodeDifferentialFuzz(t *testing.T) {
	runMSDecodeFuzz(t, buildSurroundDecodeFuzzSweep(), "surround")
}

// TestMultistreamDiscreteDecodeDifferentialFuzz locks gopus multistream discrete
// (mapping family 255: N independent mono streams, identity mapping, no coupling)
// DECODE to sample-exact parity against the libopus oracle across the
// channel-count × frame-size × bitrate × rate-control × sample-format matrix on
// CLEAN packets.
func TestMultistreamDiscreteDecodeDifferentialFuzz(t *testing.T) {
	runMSDecodeFuzz(t, buildDiscreteDecodeFuzzSweep(), "discrete")
}

// projectionDecodeFuzzSpec is one point in the projection (mapping family 3)
// decode config space.
type projectionDecodeFuzzSpec struct {
	name       string
	channels   int // 4=FOA(order1), 9=SOA(order2), 16=TOA(order3)
	frameSize  int
	frameCount int
	bitrate    int
	int16Path  bool
	seed       int64
}

// buildProjectionDecodeFuzzSweep enumerates the projection (mapping family 3)
// CLEAN-packet decode matrix. The libopus projection encoder picks the
// stream/coupled layout + demixing matrix; both decoders then consume identical
// bytes and apply the (locked bit-exact) demix. Spans FOA/SOA/TOA × frame sizes
// × bitrates × float32/int16, so per-stream coding ranges over CELT (high rate /
// low order) through Hybrid/SILK (low rate / high order).
func buildProjectionDecodeFuzzSweep() []projectionDecodeFuzzSpec {
	orders := []struct {
		name     string
		channels int
	}{
		{"foa-4ch", 4}, {"soa-9ch", 9}, {"toa-16ch", 16},
	}
	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	bitrates := []int{64000, 128000, 256000, 384000}
	formats := []bool{false, true}

	var specs []projectionDecodeFuzzSpec
	var seed int64 = 0x9A03
	for _, order := range orders {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				for _, i16 := range formats {
					seed++
					fmtTag := "f32"
					if i16 {
						fmtTag = "i16"
					}
					specs = append(specs, projectionDecodeFuzzSpec{
						name:       fmt.Sprintf("%s/fs%d/br%d/%s", order.name, fs, br, fmtTag),
						channels:   order.channels,
						frameSize:  fs,
						frameCount: 5,
						bitrate:    br,
						int16Path:  i16,
						seed:       seed,
					})
				}
			}
		}
	}
	return specs
}

// TestProjectionDecodeDifferentialFuzz locks gopus projection (mapping family 3)
// DECODE — per-stream decode plus the demixing-matrix application — to
// sample-exact parity against the libopus opus_projection_decode_float /
// opus_projection_decode oracle across the order × frame-size × bitrate ×
// sample-format matrix on CLEAN packets. The demixing application is itself
// locked bit-exact in projection_matrix_libopus_test.go. Every decoded sample,
// including per-stream mode transitions, must match the paired libopus output.
func TestProjectionDecodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	specs := buildProjectionDecodeFuzzSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	const (
		application    = 2049
		bandwidthAuto  = -1000
		maxPacketBytes = 4000
		sampleRate     = 48000
	)

	var (
		tested      int
		failedSpecs int
		transFrames int
		modeTotals  = map[string]int{}
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		if !t.Run(spec.name, func(t *testing.T) {
			tested++
			pcm := generateAmbisonicsSweep(spec.channels, spec.frameSize, spec.frameCount)
			// Encode through the libopus projection oracle (float input path) so
			// both decoders consume byte-identical family-3 packets.
			ref, err := encodeLibopusProjection(sampleRate, spec.channels, application,
				spec.bitrate, false, false, 10, bandwidthAuto, spec.frameSize, spec.frameCount,
				maxPacketBytes, 0, pcm, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference encode", err)
				return
			}

			transition, modes := transitionFrameMask(ref.packets, ref.streams)
			for k, v := range modes {
				modeTotals[k] += v
			}
			for _, tf := range transition {
				if tf {
					transFrames++
				}
			}
			mapping := trivialMapping(spec.channels)

			if spec.int16Path {
				got, err := decodeProjectionGopusInt16(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, ref.demixing, ref.packets)
				if err != nil {
					t.Fatalf("gopus projection int16 decode: %v", err)
				}
				want, err := decodeWithLibopusReferencePacketsInt16Gain(3, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, 0, mapping, ref.demixing, ref.packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "projection reference decode", err)
					return
				}
				assertMSDecodeInt16FrameAware(t, got, want, spec.channels, spec.frameSize, transition, "int16/"+spec.name)
				return
			}

			got, err := decodeProjectionGopusFloat32(sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, ref.demixing, ref.packets)
			if err != nil {
				t.Fatalf("gopus projection float decode: %v", err)
			}
			want, err := decodeWithLibopusReferencePackets(3, sampleRate, spec.channels, ref.streams, ref.coupledStreams, spec.frameSize, mapping, ref.demixing, ref.packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference decode", err)
				return
			}
			assertMSDecodeFloatFrameAware(t, got, want, spec.channels, spec.frameSize, transition, "float32/"+spec.name)
		}) {
			failedSpecs++
		}
	}
	t.Logf("projection decode differential sweep: tested=%d/%d specs; failed=%d; arch=%s; modes=%v; transition-frames=%d (all frames strict)",
		tested, len(specs), failedSpecs, runtime.GOARCH, modeTotals, transFrames)
}

// gopusEncodedDecodeSpec is one point in the gopus-emitted decode config space.
type gopusEncodedDecodeSpec struct {
	name       string
	projection bool
	channels   int
	frameSize  int
	frameCount int
	bitrate    int
	seed       int64
}

// buildGopusEncodedDecodeSweep enumerates a matrix where the gopus encoder
// produces the packets, which are then decoded through BOTH gopus and the
// libopus decode oracle. This closes the loop: it proves the packets gopus emits
// decode to the same PCM under libopus as under gopus, across surround (family 1)
// and projection (family 3) layouts.
func buildGopusEncodedDecodeSweep() []gopusEncodedDecodeSpec {
	type layout struct {
		name       string
		projection bool
		channels   int
	}
	layouts := []layout{
		{"stereo", false, 2}, {"quad", false, 4}, {"surround_5_1", false, 6}, {"surround_7_1", false, 8},
		{"foa-4ch", true, 4}, {"soa-9ch", true, 9},
	}
	frameSizes := []int{240, 480, 960, 1920}
	bitrates := []int{64000, 128000, 256000}

	var specs []gopusEncodedDecodeSpec
	var seed int64 = 0x6E00
	for _, l := range layouts {
		for _, fs := range frameSizes {
			for _, br := range bitrates {
				seed++
				specs = append(specs, gopusEncodedDecodeSpec{
					name:       fmt.Sprintf("%s/fs%d/br%d", l.name, fs, br),
					projection: l.projection,
					channels:   l.channels,
					frameSize:  fs,
					frameCount: 5,
					bitrate:    br,
					seed:       seed,
				})
			}
		}
	}
	return specs
}

// TestMultistreamGopusEncodedDecodeDifferentialFuzz drives the gopus
// multistream/projection ENCODER to produce CLEAN packets, then decodes the SAME
// packets through gopus and the libopus decode oracle, asserting sample-exact
// PCM. This guards the decode path against gopus-emitted bitstreams (not just
// libopus-emitted ones), across surround (family 1) and projection (family 3).
func TestMultistreamGopusEncodedDecodeDifferentialFuzz(t *testing.T) {
	libopustest.RequireOracle(t)

	specs := buildGopusEncodedDecodeSweep()
	budget := fuzzBudget(len(specs))
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	const (
		maxPacketBytes = 4000
		sampleRate     = 48000
	)

	var (
		tested      int
		failedSpecs int
		transFrames int
	)

	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		spec := specs[idx]
		if !t.Run(spec.name, func(t *testing.T) {
			tested++
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)

			var (
				enc      *Encoder
				err      error
				mapping  []byte
				demixing []byte
				family   = 1
			)
			if spec.projection {
				enc, err = NewProjectionEncoder(sampleRate, spec.channels)
				family = 3
			} else {
				enc, err = NewEncoderDefault(sampleRate, spec.channels)
			}
			if err != nil {
				t.Fatalf("encoder create (proj=%t ch=%d): %v", spec.projection, spec.channels, err)
			}
			enc.SetBitrate(spec.bitrate)
			enc.SetVBR(true)
			enc.SetVBRConstraint(false)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()

			streams, coupled := enc.Streams(), enc.CoupledStreams()
			if spec.projection {
				demixing = enc.GetDemixingMatrix()
				mapping = trivialMapping(spec.channels)
			} else {
				var derr error
				_, _, mapping, derr = DefaultMapping(spec.channels)
				if derr != nil {
					t.Fatalf("DefaultMapping(%d): %v", spec.channels, derr)
				}
			}

			packets := make([][]byte, 0, spec.frameCount)
			for i := 0; i < spec.frameCount; i++ {
				start := i * spec.frameSize * spec.channels
				frame := pcm[start : start+spec.frameSize*spec.channels]
				p, err := enc.EncodeFloat32WithAnalysisMaxBytes(frame, spec.frameSize, frame, maxPacketBytes)
				if err != nil {
					t.Fatalf("frame %d: gopus encode: %v", i, err)
				}
				packets = append(packets, append([]byte(nil), p...))
			}

			transition, _ := transitionFrameMask(packets, streams)
			for _, tf := range transition {
				if tf {
					transFrames++
				}
			}

			var got []float32
			if spec.projection {
				got, err = decodeProjectionGopusFloat32(sampleRate, spec.channels, streams, coupled, spec.frameSize, demixing, packets)
			} else {
				got, err = decodeSurroundGopusFloat32(sampleRate, spec.channels, streams, coupled, spec.frameSize, 0, mapping, packets)
			}
			if err != nil {
				t.Fatalf("gopus decode: %v", err)
			}
			want, err := decodeWithLibopusReferencePackets(family, sampleRate, spec.channels, streams, coupled, spec.frameSize, mapping, demixing, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream reference decode", err)
				return
			}
			assertMSDecodeFloatFrameAware(t, got, want, spec.channels, spec.frameSize, transition, "gopus-enc/"+spec.name)
		}) {
			failedSpecs++
		}
	}
	t.Logf("gopus-encoded decode differential sweep: tested=%d/%d specs; failed=%d; arch=%s; transition-frames=%d (all frames strict)",
		tested, len(specs), failedSpecs, runtime.GOARCH, transFrames)
}
