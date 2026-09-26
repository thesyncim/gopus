package multistream

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	transitionStageSampleRate = 48000
	transitionStageGainQ8     = 768
	transitionStageF10        = transitionStageSampleRate / 100
	transitionStageF5         = transitionStageF10 / 2
)

var transitionStageRefHelper libopustest.HelperCache

type transitionStageStep struct {
	packet    []byte
	frameSize int
}

func encodeTransitionGainRegressionInput(t *testing.T) (msDecodeFuzzSpec, *surroundEncodeRef) {
	t.Helper()
	const caseName = "stereo/fs480/br32000/vbrfalse/cfalse/g768/f32"
	var spec msDecodeFuzzSpec
	found := false
	for _, candidate := range buildSurroundDecodeFuzzSweep() {
		if candidate.name == caseName {
			spec = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("surround decode regression %q is missing", caseName)
	}
	pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
	ref, err := encodeLibopusSurround(
		transitionStageSampleRate,
		spec.channels,
		spec.mappingFamily,
		2049,
		spec.bitrate,
		spec.vbr,
		spec.vbrConstraint,
		10,
		-1000,
		spec.frameSize,
		spec.frameCount,
		4000,
		pcm,
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream transition PLC input packets", err)
	}
	if ref.streams != 1 || ref.coupledStreams != 1 {
		t.Fatalf("stereo transition regression stream layout=(%d,%d), want (1,1)", ref.streams, ref.coupledStreams)
	}
	if len(ref.packets) < 2 {
		t.Fatalf("transition regression packets=%d, want at least 2", len(ref.packets))
	}
	return spec, ref
}

func firstTransitionStreamPackets(t *testing.T, packets [][]byte) ([]byte, []byte, streamTOC, streamTOC) {
	t.Helper()
	prevStreams, err := parseMultistreamPacket(packets[0], 1)
	if err != nil {
		t.Fatalf("parse preceding multistream packet: %v", err)
	}
	nextStreams, err := parseMultistreamPacket(packets[1], 1)
	if err != nil {
		t.Fatalf("parse transition multistream packet: %v", err)
	}
	if len(prevStreams) != 1 || len(prevStreams[0]) == 0 || len(nextStreams) != 1 || len(nextStreams[0]) == 0 {
		t.Fatalf("transition packets have elementary stream counts %d and %d", len(prevStreams), len(nextStreams))
	}
	prevPacket, nextPacket := prevStreams[0], nextStreams[0]
	prevTOC, nextTOC := parseStreamTOC(prevPacket[0]), parseStreamTOC(nextPacket[0])
	if (prevTOC.mode == streamModeCELT) == (nextTOC.mode == streamModeCELT) {
		t.Fatalf("first two packet modes do not cross the CELT boundary: %d -> %d", prevTOC.mode, nextTOC.mode)
	}
	return prevPacket, nextPacket, prevTOC, nextTOC
}

// decodeTransitionStageWithLibopus runs a packet followed by a public
// NULL-packet PLC request through the paired single-stream libopus decoder.
// The v7 helper supports a distinct output size for each step, so the PLC
// request uses the same F5 capacity as opus_decode_frame's recursive
// transition call. This is a candidate-equivalent stage probe; the separate
// full-transition comparison verifies the recursive transition context.
func decodeTransitionStageWithLibopus(t *testing.T, channels, gainQ8, maxFrameSize int, steps []transitionStageStep) []float32 {
	t.Helper()
	binPath, err := transitionStageRefHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "multistream transition PLC stage reference",
		OutputBase: "gopus_multistream_transition_plc_stage",
		SourceFile: "libopus_refdecode_single.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream transition PLC stage reference", err)
	}

	const sampleFormatFloat32 = uint32(0)
	payload := libopustest.NewOraclePayloadVersion(
		"GOSI",
		7,
		sampleFormatFloat32,
		transitionStageSampleRate,
		uint32(int32(gainQ8)),
		uint32(channels),
		uint32(maxFrameSize),
		uint32(len(steps)),
	)
	for _, step := range steps {
		if step.frameSize <= 0 || step.frameSize > maxFrameSize {
			t.Fatalf("invalid transition stage frame size %d (maximum %d)", step.frameSize, maxFrameSize)
		}
		payload.U32(0) // decode_fec
		payload.U32(uint32(step.frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "multistream transition PLC stage reference", "GOSO")
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream transition PLC stage reference", err)
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	pcm := make([]float32, nSamples)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("multistream transition PLC stage reference: %v", err)
	}
	return pcm
}

func assertTransitionStagePCMExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s sample count=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s first difference at sample %d: Go=%08x (%g) C=%08x (%g)",
				label, i, math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
}

// TestTransitionPLCStageGainMatchesLibopus uses the exact previous stream
// packet from the strict surround decode regression and compares the entire
// transition PLC buffer against a paired C decode of that packet followed by a
// 5 ms NULL-packet PLC request. Matching the preceding PCM and the complete
// PLC buffer checks this candidate-equivalent C stage; it does not by itself
// establish that the recursive transition call has identical surrounding
// decoder context. TestCELTTransitionPLCStageHasInnerAndOuterGainChecks keeps
// that full-transition comparison strict.
func TestTransitionPLCStageGainMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const channels = 2
	spec, ref := encodeTransitionGainRegressionInput(t)
	prevPacket, nextPacket, prevTOC, nextTOC := firstTransitionStreamPackets(t, ref.packets)
	prevDuration := getFrameDuration(prevPacket)
	nextDuration := getFrameDuration(nextPacket)
	if prevDuration != spec.frameSize || nextDuration != spec.frameSize {
		t.Fatalf("transition packet durations=(%d,%d), want both %d samples/channel", prevDuration, nextDuration, spec.frameSize)
	}
	if prevTOC.stereo != (channels == 2) || nextTOC.stereo != (channels == 2) {
		t.Fatalf("transition packet stereo flags=(%t,%t), want both true for %d-channel stream", prevTOC.stereo, nextTOC.stereo, channels)
	}
	t.Logf("strict transition cohort: prior(mode=%d bw=%d stereo=%t duration=%d), target(mode=%d bw=%d stereo=%t duration=%d), channels=%d",
		prevTOC.mode, prevTOC.bandwidth, prevTOC.stereo, prevDuration,
		nextTOC.mode, nextTOC.bandwidth, nextTOC.stereo, nextDuration, channels)

	for _, gainQ8 := range []int{0, transitionStageGainQ8} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			state := newStreamDecoder(transitionStageSampleRate, channels)
			if err := state.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			previous, err := state.Decode(prevPacket, spec.frameSize)
			if err != nil {
				t.Fatalf("decode preceding elementary packet: %v", err)
			}
			if int(state.lastMode) != prevTOC.mode || int(state.lastBandwidth) != prevTOC.bandwidth || state.lastPacketStereo != prevTOC.stereo {
				t.Fatalf("Go previous decode state=(mode=%d bw=%d stereo=%t), TOC=(mode=%d bw=%d stereo=%t)",
					state.lastMode, state.lastBandwidth, state.lastPacketStereo, prevTOC.mode, prevTOC.bandwidth, prevTOC.stereo)
			}
			if state.prevRedundancy {
				t.Fatal("previous stream packet reports redundancy; standalone NULL decode would not match the recursive transition call")
			}

			got, err := state.transitionPLCToFloat32(transitionStageF5, prevTOC.mode, prevTOC.bandwidth, prevTOC.stereo)
			if err != nil {
				t.Fatalf("decode Go transition PLC stage: %v", err)
			}
			refPCM := decodeTransitionStageWithLibopus(t, channels, gainQ8, spec.frameSize, []transitionStageStep{
				{packet: prevPacket, frameSize: spec.frameSize},
				{packet: nil, frameSize: transitionStageF5},
			})
			previousSamples := spec.frameSize * channels
			stageSamples := transitionStageF5 * channels
			if len(previous) != previousSamples || len(refPCM) != previousSamples+stageSamples {
				t.Fatalf("paired C/Go sequence lengths: preceding Go=%d, C=%d; want %d + %d samples (C previous packet + PLC step)",
					len(previous), len(refPCM), previousSamples, stageSamples)
			}
			assertTransitionStagePCMExact(t, previous, refPCM[:previousSamples], fmt.Sprintf("gain=%d preceding packet mode=%d", gainQ8, prevTOC.mode))
			assertTransitionStagePCMExact(t, got, refPCM[previousSamples:], fmt.Sprintf("gain=%d previous-mode=%d", gainQ8, prevTOC.mode))
		})
	}
}

// TestCELTTransitionPLCStageHasInnerAndOuterGainChecks each complete frame of a
// CELT boundary and its recovery sequence against the paired multistream C
// decoder. The recursive PLC stage applies gain before the crossfade, and the
// outer frame applies gain afterward.
func TestCELTTransitionPLCStageHasInnerAndOuterGainChecks(t *testing.T) {
	libopustest.RequireOracle(t)

	const channels = 2
	spec, ref := encodeTransitionGainRegressionInput(t)
	prevPacket, nextPacket, _, _ := firstTransitionStreamPackets(t, ref.packets)
	if len(prevPacket) == 0 || len(nextPacket) == 0 {
		t.Fatal("transition packets are empty")
	}
	for _, gainQ8 := range []int{0, transitionStageGainQ8, -transitionStageGainQ8} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			want, err := decodeWithLibopusReferencePacketsGain(
				1, transitionStageSampleRate, channels, ref.streams, ref.coupledStreams,
				spec.frameSize, gainQ8, ref.mapping, nil, ref.packets,
			)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream transition output reference", err)
			}
			perFrame := spec.frameSize * channels
			if len(want) != len(ref.packets)*perFrame {
				t.Fatalf("C decoded %d samples, want %d packets x %d samples", len(want), len(ref.packets), perFrame)
			}
			dec, err := NewDecoder(transitionStageSampleRate, channels, ref.streams, ref.coupledStreams, ref.mapping)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			for i, packet := range ref.packets {
				got, err := dec.DecodeToFloat32(packet, spec.frameSize)
				if err != nil {
					t.Fatalf("frame %d Go decode: %v", i, err)
				}
				if len(got) != perFrame {
					t.Fatalf("frame %d Go decoded %d samples, want %d", i, len(got), perFrame)
				}
				assertTransitionStagePCMExact(t, got, want[i*perFrame:(i+1)*perFrame],
					fmt.Sprintf("frame %d with gain %d", i, gainQ8))
			}
		})
	}
}

// TestCELTTransitionFadeReplaysMatchedLibopus applies both multiplication
// orders to the actual transition PLC and main Hybrid buffers. The C frame
// selects the ordering used by src/opus_decoder.c smooth_fade on this build.
func TestCELTTransitionFadeReplaysMatchedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels = 2
	spec, ref := encodeTransitionGainRegressionInput(t)
	prevPacket, nextPacket, _, nextTOC := firstTransitionStreamPackets(t, ref.packets)
	if nextTOC.mode != streamModeHybrid {
		t.Fatalf("target mode=%d, want Hybrid", nextTOC.mode)
	}
	want, err := decodeWithLibopusReferencePackets(
		1, transitionStageSampleRate, channels, ref.streams, ref.coupledStreams,
		spec.frameSize, ref.mapping, nil, ref.packets[:2],
	)
	if err != nil {
		libopustest.HelperUnavailable(t, "transition fade replay", err)
	}
	perFrame := spec.frameSize * channels
	if len(want) != 2*perFrame {
		t.Fatalf("C returned %d samples, want %d", len(want), 2*perFrame)
	}
	state := newStreamDecoder(transitionStageSampleRate, channels)
	if _, err := state.Decode(prevPacket, spec.frameSize); err != nil {
		t.Fatal(err)
	}
	parsed, err := parseOpusPacket(nextPacket, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.frames) != 1 {
		t.Fatalf("target has %d frames, want one", len(parsed.frames))
	}
	state.recordDecodeCall(spec.frameSize, len(nextPacket))
	ts := transitionState{
		active:           true,
		prevMode:         int(state.lastMode),
		prevBW:           int(state.lastBandwidth),
		prevStereo:       state.lastPacketStereo,
		pendingTransSize: transitionStageF5,
	}
	main, err := state.decodeHybridToFloat32(parsed.frames[0], spec.frameSize, nextTOC, &ts)
	if err != nil {
		t.Fatal(err)
	}
	if len(main) != perFrame || len(ts.pcm) != transitionStageF5*channels {
		t.Fatalf("actual main/transition buffers have lengths %d/%d", len(main), len(ts.pcm))
	}
	f2_5 := transitionStageF5 / 2
	window := celt.GetWindowBufferF32(f2_5)
	fused := append([]float32(nil), main...)
	rounded := append([]float32(nil), main...)
	copy(fused[:f2_5*channels], ts.pcm[:f2_5*channels])
	copy(rounded[:f2_5*channels], ts.pcm[:f2_5*channels])
	for i := range f2_5 {
		w := streamSmoothFadeMul(window[i], window[i])
		oneMinus := streamSmoothFadeSub(1, w)
		for ch := range channels {
			idx := (f2_5+i)*channels + ch
			other := streamSmoothFadeMul(oneMinus, ts.pcm[idx])
			fused[idx] = streamSmoothFadeMulAdd(w, main[idx], other)
			rounded[idx] = streamSmoothFadeMul(w, main[idx]) + other
		}
	}
	cFrame := want[perFrame:]
	assertTransitionStagePCMExact(t, fused[:transitionStageF5*channels], cFrame[:transitionStageF5*channels], "actual-buffer fused transition window")
	roundedDifferences := 0
	for i := f2_5 * channels; i < transitionStageF5*channels; i++ {
		if math.Float32bits(rounded[i]) != math.Float32bits(cFrame[i]) {
			roundedDifferences++
		}
	}
	t.Logf("crossfade samples differing under separately rounded first product: %d", roundedDifferences)
}
