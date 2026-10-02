//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func fixedFrameSamplesAtRate(s encodeSweepSpec, sampleRate int) int {
	return s.frameSamples48k() * sampleRate / 48000
}

// assertFixedDecodeSequence compares independent, stateful float32, int16,
// and int24 decoders with the selected C FIXED_POINT build. Wire v8 records include each
// step's output count, final range, and offset into the complete PCM output.
func assertFixedDecodeSequence(t *testing.T, sampleRate, channels, frameSamples int, packets [][]byte) {
	assertFixedDecodeSequenceWithGain(t, sampleRate, channels, frameSamples, packets, 0)
}

func assertFixedDecodeSequenceWithGain(t *testing.T, sampleRate, channels, frameSamples int, packets [][]byte, gainQ8 int) {
	t.Helper()
	defer func() {
		if t.Failed() {
			for i, packet := range packets {
				t.Logf("replay rate=%d channels=%d samples=%d step=%d packet=%x", sampleRate, channels, frameSamples, i, packet)
			}
		}
	}()
	helper, err := getFixedRefdecodeHelperPath()
	if err != nil {
		t.Fatal(err)
	}
	samplesPerFrame := frameSamples * channels
	for _, format := range []uint32{libopusRefdecodeSingleFormatInt16, libopusRefdecodeSingleFormatInt24, libopusRefdecodeSingleFormatFloat32} {
		payload := libopustest.NewOraclePayloadVersion("GOSI", 8, format, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSamples), uint32(len(packets)))
		for _, packet := range packets {
			payload.U32(0)
			payload.U32(uint32(frameSamples))
			payload.U32(uint32(len(packet)))
			payload.Raw(packet)
		}
		reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "fixed stateful decode", "GOSO", 3)
		if err != nil {
			t.Fatalf("format%d C rejected valid packet history: %v", format, err)
		}
		want := make([]int32, samplesPerFrame*len(packets))
		if n := reader.Count(len(want)); n != len(want) {
			t.Fatalf("format%d C PCM count=%d want%d", format, n, len(want))
		}
		for i := range want {
			if format == libopusRefdecodeSingleFormatInt16 {
				want[i] = int32(reader.I16())
			} else {
				want[i] = reader.I32()
			}
		}
		if n := reader.Count(len(packets)); n != len(packets) {
			t.Fatalf("format%d C records=%d want%d", format, n, len(packets))
		}
		ranges := make([]uint32, len(packets))
		for i := range packets {
			status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
			if status != 0 || samples != uint32(frameSamples) || offset != uint32(i*samplesPerFrame) {
				t.Fatalf("format%d C step%d status=%d samples=%d offset=%d", format, i, status, samples, offset)
			}
			ranges[i] = finalRange
		}
		if err := reader.ExpectConsumed(); err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
		if err != nil {
			t.Fatal(err)
		}
		if err := dec.SetGain(gainQ8); err != nil {
			t.Fatal(err)
		}
		out16 := make([]int16, samplesPerFrame)
		out24 := make([]int32, samplesPerFrame)
		outFloat := make([]float32, samplesPerFrame)
		for frame, packet := range packets {
			var n int
			switch format {
			case libopusRefdecodeSingleFormatInt16:
				n, err = dec.DecodeInt16(packet, out16)
			case libopusRefdecodeSingleFormatInt24:
				n, err = dec.DecodeInt24(packet, out24)
			default:
				n, err = dec.Decode(packet, outFloat)
			}
			if err != nil || n != frameSamples {
				t.Fatalf("format%d step%d Go samples=%d want%d err=%v", format, frame, n, frameSamples, err)
			}
			if got := dec.FinalRange(); got != ranges[frame] {
				t.Fatalf("format%d step%d final range Go=%08x C=%08x", format, frame, got, ranges[frame])
			}
			for i := range samplesPerFrame {
				got := out24[i]
				switch format {
				case libopusRefdecodeSingleFormatInt16:
					got = int32(out16[i])
				case libopusRefdecodeSingleFormatFloat32:
					got = int32(math.Float32bits(outFloat[i]))
				}
				if ref := want[frame*samplesPerFrame+i]; got != ref {
					t.Fatalf("format%d step%d PCM[%d] Go=%d C=%d packet=%x", format, frame, i, got, ref, packet)
				}
			}
		}
	}
}

// TestDecodeDifferentialFixedPointEncodeThenDecode sweeps the full encoder config
// space, decodes each multi-frame stateful sequence through the gopus_fixed_point
// Decode / DecodeInt16 / DecodeInt24 and the libopus FIXED_POINT reference, and
// asserts bit-exact equality for the swept sequences against the selected
// FIXED_POINT reference build.
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

	selected, tested := 0, 0
	for idx := 0; idx < len(specs) && selected < budget; idx += stride {
		spec := specs[idx]
		selected++
		t.Run(spec.name, func(t *testing.T) {
			tested++
			specRng := rand.New(rand.NewSource(int64(idx)*2654435761 + 7))
			packets, ok := encodePackets(t, spec, specRng, framesPerSpec)
			if !ok {
				t.Fatalf("encoder rejected valid config %s", spec.name)
			}
			frameSamples := spec.frameSamples48k()

			assertFixedDecodeSequence(t, sampleRate, spec.channels, frameSamples, packets)
		})
	}
	t.Logf("fixed encode-then-decode sweep: %d/%d specs × %d frames (float32+int16+int24)", tested, len(specs), framesPerSpec)
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
	single := 1 + rng.Intn(max(1, n/3))
	if single >= n-1 {
		single = n - 2
	}
	loss[single] = true
	burstLen := 2 + rng.Intn(2) // 2..3
	burstStart := single + 2 + rng.Intn(max(1, n/4))
	for k := 0; k < burstLen && burstStart+k < n-1; k++ {
		loss[burstStart+k] = true
	}
	return loss
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

	type selectedPLCSpec struct {
		index int
		spec  encodeSweepSpec
	}
	selectedSpecs := make([]selectedPLCSpec, 0, budget)
	selected := 0
	for idx := 0; idx < len(specs) && selected < budget; idx += stride {
		spec := specs[idx]
		// DTX produces empty packets which are already a concealment path; layering
		// PLC drops on top conflates the two. The non-DTX specs cover PLC cleanly.
		if spec.dtx {
			continue
		}
		selected++
		selectedSpecs = append(selectedSpecs, selectedPLCSpec{index: idx, spec: spec})
	}

	type plcCaseResult struct {
		executed bool
	}
	results := make([]plcCaseResult, len(selectedSpecs))
	workerLimit := min(4, runtime.GOMAXPROCS(0), len(selectedSpecs))
	workers := make(chan struct{}, workerLimit)
	t.Cleanup(func() {
		tested := 0
		for _, result := range results {
			if result.executed {
				tested++
			}
		}
		t.Logf("fixed PLC sweep (CELT-only + SILK + Hybrid): %d specs × %d frames (float32+int16+int24)", tested, framesPerSpec)
	})

	for resultIndex, selected := range selectedSpecs {
		resultIndex, selected := resultIndex, selected
		t.Run(selected.spec.name, func(t *testing.T) {
			t.Parallel()
			workers <- struct{}{}
			defer func() { <-workers }()

			result := plcCaseResult{executed: true}
			defer func() { results[resultIndex] = result }()

			spec := selected.spec
			specRng := rand.New(rand.NewSource(int64(selected.index)*40503 + 11))
			encoded, ok := encodePackets(t, spec, specRng, framesPerSpec)
			if !ok {
				t.Fatalf("encoder rejected valid config %s", spec.name)
			}
			loss := plcDropPattern(specRng, framesPerSpec)
			if len(loss) == 0 {
				t.Fatal("missing loss pattern")
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

			assertFixedDecodeSequence(t, sampleRate, spec.channels, frameSamples, steps)
		})
	}
}

// TestDecodeDifferentialFixedPointMultiSampleRate checks received packets at
// 8/12/16/24/48 kHz, including output counts, final ranges, and all integer PCM.
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

	type selectedSpec struct {
		index int
		spec  encodeSweepSpec
	}
	type sweepResult struct {
		configs int
		rates   int
	}
	selectedSpecs := make([]selectedSpec, 0, budget)
	for idx := 0; idx < len(specs) && len(selectedSpecs) < budget; idx += stride {
		selectedSpecs = append(selectedSpecs, selectedSpec{index: idx, spec: specs[idx]})
	}
	results := make([]sweepResult, len(selectedSpecs))
	workerLimit := min(4, runtime.GOMAXPROCS(0), len(selectedSpecs))
	workers := make(chan struct{}, workerLimit)
	t.Cleanup(func() {
		tested, testedRates := 0, 0
		for _, result := range results {
			tested += result.configs
			testedRates += result.rates
		}
		t.Logf("fixed multi-rate sweep: %d specs / %d executed rate cases × %d frames (float32+int16+int24)", tested, testedRates, framesPerSpec)
	})

	for resultIdx, selected := range selectedSpecs {
		resultIdx, selected := resultIdx, selected
		t.Run(selected.spec.name, func(t *testing.T) {
			t.Parallel()
			workers <- struct{}{}
			defer func() { <-workers }()

			result := &results[resultIdx]
			result.configs++
			spec := selected.spec
			// Encode once at 48 kHz (the encoder always runs at 48 kHz here); the
			// resulting packets are decoded at every API rate.
			specRng := rand.New(rand.NewSource(int64(selected.index)*982451653 + 13))
			packets, ok := encodePackets(t, spec, specRng, framesPerSpec)
			if !ok {
				t.Fatalf("encoder rejected valid config %s", spec.name)
			}
			for _, sr := range rates {
				frameSamples := fixedFrameSamplesAtRate(spec, sr)
				if frameSamples <= 0 {
					t.Fatal("invalid output frame size")
				}
				t.Run(fmt.Sprintf("%dHz", sr), func(t *testing.T) {
					result.rates++
					assertFixedDecodeSequence(t, sr, spec.channels, frameSamples, packets)
				})
			}
		})
	}
}

// TestDecodeDifferentialFixedPointLossSampleRates exercises periodic PLC,
// sustained-loss noise synthesis, and recovery at every supported API rate.
func TestDecodeDifferentialFixedPointLossSampleRates(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, mode := range []struct {
		name    string
		app     Application
		mode    EncoderMode
		bw      Bandwidth
		bitrate int
	}{
		{"silk", ApplicationRestrictedSilk, EncoderModeSILK, BandwidthMediumband, 16000},
		{"hybrid", ApplicationVoIP, EncoderModeHybrid, BandwidthFullband, 48000},
		{"celt", ApplicationRestrictedCelt, EncoderModeCELT, BandwidthFullband, 96000},
	} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s_ch%d", mode.name, channels), func(t *testing.T) {
				spec := encodeSweepSpec{application: mode.app, mode: mode.mode, bandwidth: mode.bw, frameMs: ExpertFrameDuration20Ms, bitrate: mode.bitrate, channels: channels, vbr: BitrateModeVBR}
				packets, ok := encodePackets(t, spec, rand.New(rand.NewSource(90817+int64(channels))), 19)
				if !ok {
					t.Fatal("encoder rejected loss-rate probe")
				}
				// Prime, isolated loss and recovery, then cross the CELT periodic-to-noise
				// boundary with twelve consecutive losses before the final recovery.
				packets[3] = nil
				for i := 6; i < 18; i++ {
					packets[i] = nil
				}
				for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
					t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) { assertFixedDecodeSequence(t, rate, channels, rate/50, packets) })
				}
			})
		}
	}
}

// A received Hybrid frame must advance the integer CELT state even when the API
// rate omits its high band, so subsequent loss cannot reuse the preceding CELT
// frame's synthesis state.
func TestDecodeDifferentialFixedPointHybridTransitionLossSampleRates(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("ch%d", channels), func(t *testing.T) {
			celt := encodeFixedSingleModePacket(t, channels, 960, EncoderModeCELT, 0)
			hybrid := encodeFixedSingleModePacket(t, channels, 960, EncoderModeHybrid, 960)
			if ParseTOC(celt[0]).Mode != ModeCELT || ParseTOC(hybrid[0]).Mode != ModeHybrid {
				t.Fatal("transition probe does not contain CELT followed by Hybrid")
			}
			packets := [][]byte{celt, hybrid, nil, nil, hybrid, celt, hybrid, nil, hybrid}
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				for _, gain := range []int{0, 768, -768} {
					t.Run(fmt.Sprintf("%dHz_gain%d", rate, gain), func(t *testing.T) {
						assertFixedDecodeSequenceWithGain(t, rate, channels, rate/50, packets, gain)
					})
				}
			}
		})
	}
}
