//go:build gopus_fixed_point

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
	mspkg "github.com/thesyncim/gopus/multistream"
	"math"
	"math/rand"
	"runtime"
	"testing"
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

func encodeAPIRateHybridPacketFrameSizeVariant(t *testing.T, channels, frameSize, variant int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeHybrid); err != nil {
		t.Fatalf("SetMode(Hybrid): %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(64000 * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := float64(variant*frameSize+i) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*(220+float64(variant)*17)*tm)) +
			0.12*float32(math.Sin(2*math.Pi*(1300+float64(variant)*53)*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*(330+float64(variant)*23)*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*(1700+float64(variant)*41)*tm+0.31))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func apiRateReferenceHelperPath() (string, error) {
	return getFixedRefdecodeHelperPath()
}

var privateFloatCELTAPIRateRefdecodeHelper libopustest.HelperCache

// Fixed public builds still compile this package-private CELT decoder with
// float32 state. Select a float archive with matching optional CELT features
// rather than comparing it with the public FIXED_POINT decoder.
func privateFloatCELTAPIRateReferenceHelperPath() (string, error) {
	return privateFloatCELTAPIRateRefdecodeHelper.Path(func() (string, error) {
		cfg := libopustest.CHelperConfig{
			Label:      "private float CELT API-rate reference decode",
			OutputBase: "gopus_libopus_refdecode_private_float_api_rate",
			SourceFile: "libopus_refdecode_single.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		}
		switch {
		case privateFloatCELTCustomModesEnabled && extsupport.QEXT:
			cfg.CustomQEXTRef = true
			cfg.Libs = []string{libopustest.CustomQEXTRefPath(".libs", "libopus.a"), "-lm"}
		case privateFloatCELTCustomModesEnabled:
			cfg.CustomRef = true
			cfg.Libs = []string{libopustest.CustomRefPath(".libs", "libopus.a"), "-lm"}
		case extsupport.QEXT:
			cfg.QEXTRef = true
			cfg.Libs = []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"}
		default:
			cfg.Libs = []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}
		}
		return libopustest.BuildCHelper(cfg)
	})
}

func TestDecoderFixedPointActiveLossCycleZeroAlloc(t *testing.T) {
	for _, mode := range []EncoderMode{EncoderModeCELT, EncoderModeHybrid} {
		for _, channels := range []int{1, 2} {
			packet := encodeFixedSingleModePacket(t, channels, 960, mode, 0)
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				for _, format := range []string{"float32", "int16", "int24"} {
					t.Run(fmt.Sprintf("mode%d_ch%d_%dHz_%s", mode, channels, rate, format), func(t *testing.T) {
						d, err := NewDecoder(DefaultDecoderConfig(rate, channels))
						if err != nil {
							t.Fatal(err)
						}
						size := rate / 50
						floats := make([]float32, size*channels)
						ints16 := make([]int16, size*channels)
						ints24 := make([]int32, size*channels)
						active := false
						activeLoss := false
						cycle := func() {
							active, activeLoss = false, false
							for step := 0; step < 17; step++ {
								data := packet
								if step >= 3 && step < 15 {
									data = nil
								}
								var n int
								switch format {
								case "float32":
									n, err = d.Decode(data, floats)
								case "int16":
									n, err = d.DecodeInt16(data, ints16)
								default:
									n, err = d.DecodeInt24(data, ints24)
								}
								if err != nil || n != size {
									t.Fatalf("step%d n=%d err=%v", step, n, err)
								}
								if step == 2 {
									for i := 0; i < size*channels; i++ {
										active = active || floats[i] != 0 || ints16[i] != 0 || ints24[i] != 0
									}
								}
								if step == 3 {
									for i := 0; i < size*channels; i++ {
										activeLoss = activeLoss || floats[i] != 0 || ints16[i] != 0 || ints24[i] != 0
									}
								}
							}
						}
						for range 3 {
							cycle()
						}
						if !active {
							t.Fatal("warm received signal is silent")
						}
						if allocs := testing.AllocsPerRun(10, cycle); allocs != 0 {
							t.Fatalf("active received/loss/recovery cycle allocations=%g want0", allocs)
						}
						if !active || !activeLoss {
							t.Fatal("measured cycle does not contain active received audio and concealment")
						}
					})
				}
			}
		}
	}
}

func encodeFixedCELTSequence(t *testing.T, channels, frameSize, frames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	var packets [][]byte
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.28 * float32(math.Sin(2*math.Pi*1200*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.19 * float32(math.Sin(2*math.Pi*1900*tm))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		if toc := ParseTOC(pkt[0]); toc.Mode != ModeCELT {
			t.Fatalf("encoder produced mode %v, want CELT", toc.Mode)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointCELTPLCParity gates (A) and (B) at the public-API level:
// the public Decoder.DecodeInt16 / DecodeInt24 of a CELT-only stream containing
// LOST frames -- a single loss, a 3-frame burst, then the recovered good frame
// immediately after the loss -- is bit-exact with the libopus FIXED_POINT
// opus_decode / opus_decode24 reference.
//
// (A) the LOST frames route through the integer celt_decode_lost.
// (B) the first good frame after the burst enters celt_decode_with_ec with
// loss_duration != 0, exercising the coarse-energy prediction safety block.
//
// The tested integer outputs match the selected FIXED_POINT libopus reference;
// this assertion does not depend on a floating-point drift allowance.
func TestDecoderFixedPointCELTPLCParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		// lossAt[i] marks frame index i as a lost frame (PLC). Other frames are
		// the corresponding encoded packet.
		lossAt map[int]bool
		frames int
	}
	cases := []tc{
		{"mono_960_single", 1, 960, map[int]bool{2: true}, 6},
		{"mono_960_burst", 1, 960, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"mono_480_burst", 1, 480, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"mono_240_burst", 1, 240, map[int]bool{3: true, 4: true, 5: true}, 9},
		{"mono_120_burst", 1, 120, map[int]bool{3: true, 4: true, 5: true}, 9},
		{"stereo_960_single", 2, 960, map[int]bool{2: true}, 6},
		{"stereo_960_burst", 2, 960, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"stereo_480_burst", 2, 480, map[int]bool{2: true, 3: true, 4: true}, 8},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, c.channels, c.frameSize, c.frames)

			// Build the decode step sequence: nil for lost frames, the encoded
			// packet otherwise.
			steps := make([][]byte, c.frames)
			for i := 0; i < c.frames; i++ {
				if c.lossAt[i] {
					steps[i] = nil
				} else {
					steps[i] = encoded[i]
				}
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16 (PLC)", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24 (PLC)", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int24: %v", err)
			}

			var got16, got24 []int32
			for p, pkt := range steps {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("step %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)

				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("step %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

func decodeWithLibopusFixedInt16Gain(sampleRate, channels, frameSize, gainQ8 int, packets [][]byte) ([]int16, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt16, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int16 gain", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// decodeWithLibopusFixedInt24Gain drives FIXED_POINT opus_decode24() with a
// non-zero OPUS_SET_GAIN on the reference decoder.
func decodeWithLibopusFixedInt24Gain(sampleRate, channels, frameSize, gainQ8 int, packets [][]byte) ([]int32, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int24 gain", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// encodeFixedDecodeGainCELTSequence encodes a CELT-only multi-frame stream so the
// decode runs entirely through the integer CELT path; the decode-gain stage then
// applies to integer-exact accumulated opus_res samples.
func encodeFixedDecodeGainCELTSequence(t *testing.T, channels, frameSize, frames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	packets := make([][]byte, 0, frames)
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			// Large-amplitude tone so the decode-gain SATURATE clamp is exercised
			// for the higher positive gains.
			pcm[i*channels] = 0.7 * float32(math.Sin(2*math.Pi*440*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.65 * float32(math.Sin(2*math.Pi*523*tm+0.2))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointDecodeGainParity gates that the public DecodeInt16 /
// DecodeInt24 with a non-zero OPUS_SET_GAIN is bit-exact with the libopus
// FIXED_POINT opus_decode / opus_decode24 reference, for the integer CELT path.
// The gain stage (celt_exp2(MULT16_16_P15(...)) -> MULT32_32_Q16 -> SATURATE) is
// applied to the integer-exact opus_res accumulation rather than dropping to the
// float conversion. The selected fixed reference must match on every architecture.
func TestDecoderFixedPointDecodeGainParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	gains := []int{256, -256, 1536, -2048, 6144, -6144}
	type tc struct {
		name      string
		channels  int
		frameSize int
		frames    int
	}
	cases := []tc{
		{"mono_960", 1, 960, 4},
		{"stereo_960", 2, 960, 4},
		{"mono_480", 1, 480, 6},
	}

	for _, c := range cases {
		for _, g := range gains {
			c, g := c, g
			t.Run(fmt.Sprintf("%s_gain%d", c.name, g), func(t *testing.T) {
				packets := encodeFixedDecodeGainCELTSequence(t, c.channels, c.frameSize, c.frames)
				if toc := ParseTOC(packets[0][0]); toc.Mode != ModeCELT {
					t.Fatalf("first packet mode %v, want CELT", toc.Mode)
				}

				refInt16, err := decodeWithLibopusFixedInt16Gain(sampleRate, c.channels, c.frameSize, g, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference decode int16 gain", err)
					return
				}
				refInt24, err := decodeWithLibopusFixedInt24Gain(sampleRate, c.channels, c.frameSize, g, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference decode int24 gain", err)
					return
				}

				dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
				dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
				if err := dec16.SetGain(g); err != nil {
					t.Fatalf("dec16 SetGain: %v", err)
				}
				if err := dec24.SetGain(g); err != nil {
					t.Fatalf("dec24 SetGain: %v", err)
				}

				var got16, got24 []int32
				for p, pkt := range packets {
					o16 := make([]int16, c.frameSize*c.channels)
					if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
						t.Fatalf("packet %d DecodeInt16: %v", p, err)
					}
					got16 = append(got16, int16ToInt32(o16)...)
					o24 := make([]int32, c.frameSize*c.channels)
					if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
						t.Fatalf("packet %d DecodeInt24: %v", p, err)
					}
					got24 = append(got24, o24...)
				}

				assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
				assertFixedExact(t, "int24", got24, refInt24)
			})
		}
	}
}

func TestDecoderFixedPointFECMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := getFixedRefdecodeHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference FEC decode", err)
	}
	type fecStep struct {
		packet []byte
		fec    bool
	}
	for _, mode := range []EncoderMode{EncoderModeSILK, EncoderModeHybrid, EncoderModeCELT} {
		for _, channels := range []int{1, 2} {
			var seed, recovery []byte
			switch mode {
			case EncoderModeSILK:
				seed, recovery = encodeAPIRateFECSequence(t, mode, ModeSILK, BandwidthWideband, 24000, channels, 960)
			case EncoderModeHybrid:
				seed, recovery = encodeAPIRateFECSequence(t, mode, ModeHybrid, BandwidthFullband, 48000, channels, 960)
			default:
				seed = encodeFixedSingleModePacket(t, channels, 960, mode, 0)
				recovery = seed
			}
			if mode != EncoderModeCELT && !PacketHasLBRR(recovery) {
				t.Fatalf("mode %v ch %d recovery packet has no LBRR", mode, channels)
			}
			steps := []fecStep{
				{packet: seed},
				{packet: recovery, fec: true},
				{packet: recovery},
				{fec: true},
				{packet: recovery},
			}
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				for _, gain := range []int{0, -768, 768} {
					t.Run(fmt.Sprintf("mode%d_ch%d_rate%d_gain%d", mode, channels, rate, gain), func(t *testing.T) {
						frameSize := rate / 50
						stepPCM := frameSize * channels
						payload := libopustest.NewOraclePayloadVersion("GOSI", 8,
							libopusRefdecodeSingleFormatFloat32, uint32(rate), uint32(int32(gain)),
							uint32(channels), uint32(frameSize), uint32(len(steps)))
						for _, step := range steps {
							flag := uint32(0)
							if step.fec {
								flag = 1
							}
							payload.U32(flag)
							payload.U32(uint32(frameSize))
							payload.U32(uint32(len(step.packet)))
							payload.Raw(step.packet)
						}
						reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "fixed reference FEC decode", "GOSO", 3)
						if err != nil {
							t.Fatal(err)
						}
						if got := reader.Count(stepPCM * len(steps)); got != stepPCM*len(steps) {
							t.Fatalf("reference PCM count=%d want=%d", got, stepPCM*len(steps))
						}
						wantPCM := make([]uint32, stepPCM*len(steps))
						for i := range wantPCM {
							wantPCM[i] = reader.U32()
						}
						if got := reader.Count(len(steps)); got != len(steps) {
							t.Fatalf("reference record count=%d want=%d", got, len(steps))
						}
						wantRange := make([]uint32, len(steps))
						for i := range steps {
							status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
							if status != 0 || samples != uint32(frameSize) || offset != uint32(i*stepPCM) {
								t.Fatalf("reference step %d record=(%d,%d,%08x,%d)", i, status, samples, finalRange, offset)
							}
							wantRange[i] = finalRange
						}
						if err := reader.ExpectConsumed(); err != nil {
							t.Fatal(err)
						}

						dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gain); err != nil {
							t.Fatal(err)
						}
						frame := make([]float32, stepPCM)
						for pass := range 2 {
							if pass != 0 {
								dec.Reset()
							}
							for i, step := range steps {
								var n int
								if step.fec {
									n, err = dec.DecodeWithFEC(step.packet, frame, true)
								} else {
									n, err = dec.Decode(step.packet, frame)
								}
								if err != nil || n != frameSize {
									t.Fatalf("pass %d step %d decode=(%d,%v), want %d", pass, i, n, err, frameSize)
								}
								if got := dec.FinalRange(); got != wantRange[i] {
									t.Fatalf("pass %d step %d range=%08x want=%08x", pass, i, got, wantRange[i])
								}
								for j, sample := range frame {
									if got := math.Float32bits(sample); got != wantPCM[i*stepPCM+j] {
										t.Fatalf("pass %d step %d sample %d=%08x want=%08x", pass, i, j, got, wantPCM[i*stepPCM+j])
									}
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestDecoderFixedPointFECWarmZeroAllocs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
	}{
		{"silk", EncoderModeSILK, ModeSILK, BandwidthWideband, 24000},
		{"hybrid", EncoderModeHybrid, ModeHybrid, BandwidthFullband, 48000},
	} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s_ch%d", tc.name, channels), func(t *testing.T) {
				seed, recovery := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
				if !PacketHasLBRR(recovery) {
					t.Fatal("warm FEC packet has no LBRR")
				}
				dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
				if err != nil {
					t.Fatal(err)
				}
				frame := make([]float32, 960*channels)
				if _, err := dec.Decode(seed, frame); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					if _, err := dec.DecodeWithFEC(recovery, frame, true); err != nil {
						t.Fatal(err)
					}
				}
				allocs := testing.AllocsPerRun(20, func() {
					n, err := dec.DecodeWithFEC(recovery, frame, true)
					if err != nil || n != 960 {
						t.Fatalf("warm FEC decode=(%d,%v)", n, err)
					}
				})
				if allocs != 0 {
					t.Fatalf("warm FEC allocations=%g, want zero", allocs)
				}
				hasSignal := false
				for _, sample := range frame {
					if sample != 0 && !math.IsNaN(float64(sample)) {
						hasSignal = true
						break
					}
				}
				if !hasSignal {
					t.Fatal("warm FEC produced no active PCM")
				}
			})
		}
	}
}

// TestDecoderFixedPointFECMultiFrameMatchesLibopus exercises SILK's two- and
// three-frame LBRR cadence, including Hybrid integer CELT highband accumulation
// on the first 20 ms of the recovered frame.
func TestDecoderFixedPointFECMultiFrameMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := getFixedRefdecodeHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference multi-frame FEC decode", err)
	}
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		channels  int
		rate      int
		frameSize int
	}{
		{"silk_40ms_mono", EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, 16000, 1920},
		{"silk_60ms_stereo", EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 2, 48000, 2880},
		{"hybrid_40ms_mono", EncoderModeHybrid, ModeHybrid, BandwidthFullband, 48000, 1, 48000, 1920},
		{"hybrid_60ms_stereo", EncoderModeHybrid, ModeHybrid, BandwidthFullband, 48000, 2, 48000, 2880},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed, recovery := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, tc.channels, tc.frameSize)
			if !PacketHasLBRR(recovery) {
				t.Fatal("recovery packet has no LBRR")
			}
			frameSize := tc.frameSize * tc.rate / 48000
			pcmPerStep := frameSize * tc.channels
			packets := [][]byte{seed, recovery, recovery}
			payload := libopustest.NewOraclePayloadVersion("GOSI", 8,
				libopusRefdecodeSingleFormatFloat32, uint32(tc.rate), uint32(768),
				uint32(tc.channels), uint32(frameSize), uint32(len(packets)))
			for i, packet := range packets {
				fec := uint32(0)
				if i == 1 {
					fec = 1
				}
				payload.U32(fec)
				payload.U32(uint32(frameSize))
				payload.U32(uint32(len(packet)))
				payload.Raw(packet)
			}
			reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "fixed reference multi-frame FEC decode", "GOSO", 3)
			if err != nil {
				t.Fatal(err)
			}
			if got := reader.Count(len(packets) * pcmPerStep); got != len(packets)*pcmPerStep {
				t.Fatalf("reference PCM count=%d want=%d", got, len(packets)*pcmPerStep)
			}
			wantPCM := make([]uint32, len(packets)*pcmPerStep)
			for i := range wantPCM {
				wantPCM[i] = reader.U32()
			}
			if got := reader.Count(len(packets)); got != len(packets) {
				t.Fatalf("reference record count=%d want=%d", got, len(packets))
			}
			wantRange := make([]uint32, len(packets))
			for i := range packets {
				status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
				if status != 0 || samples != uint32(frameSize) || offset != uint32(i*pcmPerStep) {
					t.Fatalf("reference step %d record=(%d,%d,%08x,%d)", i, status, samples, finalRange, offset)
				}
				wantRange[i] = finalRange
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
			dec, err := NewDecoder(DefaultDecoderConfig(tc.rate, tc.channels))
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(768); err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, pcmPerStep)
			for i, packet := range packets {
				var n int
				if i == 1 {
					n, err = dec.DecodeWithFEC(packet, pcm, true)
				} else {
					n, err = dec.Decode(packet, pcm)
				}
				if err != nil || n != frameSize {
					t.Fatalf("step %d decode=(%d,%v), want=%d", i, n, err, frameSize)
				}
				if got := dec.FinalRange(); got != wantRange[i] {
					t.Fatalf("step %d range=%08x want=%08x", i, got, wantRange[i])
				}
				for j, sample := range pcm {
					if got := math.Float32bits(sample); got != wantPCM[i*pcmPerStep+j] {
						t.Fatalf("step %d sample %d=%08x want=%08x", i, j, got, wantPCM[i*pcmPerStep+j])
					}
				}
			}
		})
	}
}

// TestDecoderFixedPointFECNoLBRRCELTToSILKMatchesLibopus covers the 5 ms
// integer transition after a long no-LBRR request. The middle call conceals
// 40 or 60 ms, and the following SILK packet crossfades CELT PLC onto SILK.
func TestDecoderFixedPointFECNoLBRRCELTToSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := getFixedRefdecodeHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference no-LBRR FEC transition", err)
	}
	for _, channels := range []int{1, 2} {
		seed := encodeAPIRateCELTPacket(t, channels)
		recovery := encodeAPIRateSILKPacket(t, channels)
		if PacketHasLBRR(recovery) {
			t.Fatalf("channels=%d recovery packet unexpectedly carries LBRR", channels)
		}
		for _, rate := range []int{12000, 48000} {
			for _, duration := range []int{40, 60} {
				for _, gain := range []int{0, 768} {
					t.Run(fmt.Sprintf("ch%d_rate%d_ms%d_gain%d", channels, rate, duration, gain), func(t *testing.T) {
						requested := rate * duration / 1000
						packetSamples := rate / 50
						wantSamples := []int{packetSamples, requested, packetSamples}
						packets := [][]byte{seed, recovery, recovery}
						payload := libopustest.NewOraclePayloadVersion("GOSI", 8,
							libopusRefdecodeSingleFormatFloat32, uint32(rate), uint32(int32(gain)),
							uint32(channels), uint32(requested), uint32(len(packets)))
						for i, packet := range packets {
							fec := uint32(0)
							if i == 1 {
								fec = 1
							}
							payload.U32(fec)
							payload.U32(uint32(requested))
							payload.U32(uint32(len(packet)))
							payload.Raw(packet)
						}
						pcmCount := (2*packetSamples + requested) * channels
						reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "fixed reference no-LBRR FEC transition", "GOSO", 3)
						if err != nil {
							t.Fatal(err)
						}
						if got := reader.Count(pcmCount); got != pcmCount {
							t.Fatalf("reference PCM count=%d want=%d", got, pcmCount)
						}
						wantPCM := make([]uint32, pcmCount)
						for i := range wantPCM {
							wantPCM[i] = reader.U32()
						}
						if got := reader.Count(len(packets)); got != len(packets) {
							t.Fatalf("reference record count=%d want=%d", got, len(packets))
						}
						wantRange := make([]uint32, len(packets))
						offset := 0
						for i := range packets {
							status, samples, finalRange, pcmOffset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
							if status != 0 || samples != uint32(wantSamples[i]) || pcmOffset != uint32(offset) {
								t.Fatalf("reference step %d record=(%d,%d,%08x,%d), want samples=%d offset=%d", i, status, samples, finalRange, pcmOffset, wantSamples[i], offset)
							}
							wantRange[i] = finalRange
							offset += wantSamples[i] * channels
						}
						if err := reader.ExpectConsumed(); err != nil {
							t.Fatal(err)
						}
						dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gain); err != nil {
							t.Fatal(err)
						}
						frame := make([]float32, requested*channels)
						for pass := range 2 {
							if pass != 0 {
								dec.Reset()
							}
							offset = 0
							for i, packet := range packets {
								var n int
								if i == 1 {
									n, err = dec.DecodeWithFEC(packet, frame, true)
								} else {
									n, err = dec.Decode(packet, frame)
								}
								if err != nil || n != wantSamples[i] {
									t.Fatalf("pass %d step %d decode=(%d,%v), want=%d", pass, i, n, err, wantSamples[i])
								}
								if got := dec.FinalRange(); got != wantRange[i] {
									t.Fatalf("pass %d step %d range=%08x want=%08x", pass, i, got, wantRange[i])
								}
								for j, sample := range frame[:n*channels] {
									if got := math.Float32bits(sample); got != wantPCM[offset+j] {
										t.Fatalf("pass %d step %d sample %d=%08x want=%08x", pass, i, j, got, wantPCM[offset+j])
									}
								}
								offset += n * channels
							}
						}
					})
				}
			}
		}
	}
}

func TestDecoderFixedPointFECTransitionWarmZeroAllocs(t *testing.T) {
	const channels = 2
	seed := encodeAPIRateCELTPacket(t, channels)
	recovery := encodeAPIRateSILKPacket(t, channels)
	if PacketHasLBRR(recovery) {
		t.Fatal("transition packet unexpectedly carries LBRR")
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, 1920*channels)
	decodeCycle := func() {
		dec.Reset()
		if n, err := dec.Decode(seed, frame); err != nil || n != 960 {
			t.Fatalf("seed decode=(%d,%v)", n, err)
		}
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != 1920 {
			t.Fatalf("no-LBRR decode=(%d,%v)", n, err)
		}
		if n, err := dec.Decode(recovery, frame); err != nil || n != 960 {
			t.Fatalf("transition decode=(%d,%v)", n, err)
		}
	}
	for range 3 {
		decodeCycle()
	}
	if allocs := testing.AllocsPerRun(20, decodeCycle); allocs != 0 {
		t.Fatalf("warm FEC transition allocations=%g, want zero", allocs)
	}
	hasSignal := false
	for _, sample := range frame[:960*channels] {
		if sample != 0 && !math.IsNaN(float64(sample)) {
			hasSignal = true
			break
		}
	}
	if !hasSignal {
		t.Fatal("warm transition produced no active PCM")
	}
}

func TestFixedDecodeGainClampMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := minimalHybridTestPacket20ms()
	for _, gain := range []int{-256, 0, 256, 8 * 256} {
		t.Run(fmt.Sprintf("gain_%d", gain), func(t *testing.T) {
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(48000, 1, 960, gain,
				[]libopusAPIRateDecodeStep{{packet: packet}})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed gain", err)
			}
			d, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SetGain(gain); err != nil {
				t.Fatal(err)
			}
			got := make([]float32, 960)
			n, err := d.Decode(packet, got)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(want) {
				t.Fatalf("samples=%d want %d", n, len(want))
			}
			for i, sample := range got[:n] {
				if math.Float32bits(sample) != math.Float32bits(want[i]) {
					t.Fatalf("sample %d: Go=%08x C=%08x", i, math.Float32bits(sample), math.Float32bits(want[i]))
				}
			}
		})
	}
}

type fixedHybridToSILKStep struct {
	name   string
	packet []byte
	format uint32
}

func assertFixedHybridToSILKWarmZeroAllocs(t *testing.T, sampleRate, channels int, hybrid, silk []byte) {
	t.Helper()

	hybridSamples, err := packetSamplesAtRate(hybrid, sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	silkSamples, err := packetSamplesAtRate(silk, sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	out16 := make([]int16, hybridSamples*channels)
	out24 := make([]int32, silkSamples*channels)
	var hybridN, silkN int
	var decodeErr error
	decode := func() {
		dec.Reset()
		hybridN, decodeErr = dec.DecodeInt16(hybrid, out16)
		if decodeErr != nil {
			return
		}
		silkN, decodeErr = dec.DecodeInt24(silk, out24)
	}

	decode()
	if decodeErr != nil || hybridN != hybridSamples || silkN != silkSamples {
		t.Fatalf("warm Hybrid→SILK decode counts=(%d,%d) want=(%d,%d), err=%v", hybridN, silkN, hybridSamples, silkSamples, decodeErr)
	}
	allocs := testing.AllocsPerRun(20, decode)
	if decodeErr != nil || hybridN != hybridSamples || silkN != silkSamples {
		t.Fatalf("measured Hybrid→SILK decode counts=(%d,%d) want=(%d,%d), err=%v", hybridN, silkN, hybridSamples, silkSamples, decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm Hybrid→SILK decode allocated %g times per sequence", allocs)
	}
}

// assertFixedHybridToSILKSequence checks a persistent Go decoder against the
// selected fixed-point C decoder while changing public PCM APIs between frames.
func assertFixedHybridToSILKSequence(t *testing.T, dec *Decoder, sampleRate, channels int, steps []fixedHybridToSILKStep) {
	t.Helper()

	cases := make([]libopustest.DecodeDiffCase, len(steps))
	frameSizes := make([]int, len(steps))
	previousFrameSize := sampleRate / 50
	for i, step := range steps {
		frameSize := previousFrameSize
		if len(step.packet) > 0 {
			var err error
			frameSize, err = packetSamplesAtRate(step.packet, sampleRate)
			if err != nil {
				t.Fatalf("step %d %s packetSamplesAtRate: %v", i, step.name, err)
			}
		}
		frameSizes[i] = frameSize
		previousFrameSize = frameSize
		cases[i] = libopustest.DecodeDiffCase{
			Packet:    step.packet,
			Format:    step.format,
			FrameSize: uint32(frameSize),
		}
	}

	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		t.Fatal(err)
	}
	for i, step := range steps {
		frameSize := frameSizes[i]
		sampleCount := frameSize * channels
		var gotF32 []float32
		var gotI16 []int16
		var gotI24 []int32
		var n int
		switch step.format {
		case libopustest.DecodeDiffFormatFloat32:
			gotF32 = make([]float32, sampleCount)
			n, err = dec.Decode(step.packet, gotF32)
		case libopustest.DecodeDiffFormatInt16:
			gotI16 = make([]int16, sampleCount)
			n, err = dec.DecodeInt16(step.packet, gotI16)
		case libopustest.DecodeDiffFormatInt24:
			gotI24 = make([]int32, sampleCount)
			n, err = dec.DecodeInt24(step.packet, gotI24)
		default:
			t.Fatalf("step %d %s has unsupported PCM format %d", i, step.name, step.format)
		}
		if err != nil || n != int(want[i].Code) {
			t.Fatalf("step %d %s samples=%d err=%v C=%d", i, step.name, n, err, want[i].Code)
		}
		if got := dec.FinalRange(); got != want[i].FinalRange {
			t.Fatalf("step %d %s final range=%08x C=%08x", i, step.name, got, want[i].FinalRange)
		}

		bytesPerSample := 4
		if step.format == libopustest.DecodeDiffFormatInt16 {
			bytesPerSample = 2
		}
		if len(want[i].PCM) != sampleCount*bytesPerSample {
			t.Fatalf("step %d %s C PCM bytes=%d want=%d", i, step.name, len(want[i].PCM), sampleCount*bytesPerSample)
		}
		for j := 0; j < sampleCount; j++ {
			var got, expected int64
			switch step.format {
			case libopustest.DecodeDiffFormatFloat32:
				got = int64(math.Float32bits(gotF32[j]))
				expected = int64(binary.LittleEndian.Uint32(want[i].PCM[j*4:]))
			case libopustest.DecodeDiffFormatInt16:
				got = int64(gotI16[j])
				expected = int64(int16(binary.LittleEndian.Uint16(want[i].PCM[j*2:])))
			case libopustest.DecodeDiffFormatInt24:
				got = int64(gotI24[j])
				expected = int64(int32(binary.LittleEndian.Uint32(want[i].PCM[j*4:])))
			}
			if got != expected {
				t.Fatalf("step %d %s sample %d Go=%d C=%d", i, step.name, j, got, expected)
			}
		}
	}
}

// TestDecoderFixedPointHybridToSILKFadeParity covers the 2.5 ms CELT silence
// accumulation that opus_decode_frame applies after SILK when the previous mode
// is Hybrid. The test uses a 60 ms SILK packet so the captured fixed-point output
// starts after the Hybrid frame in the same persistent state and exercises the
// packet-tail offset used when adding the CELT overlap.
func TestDecoderFixedPointHybridToSILKFadeParity(t *testing.T) {
	libopustest.RequireOracle(t)

	hybrid := encodeAPIRateHybridPacketFrameSize(t, 1, 1920)
	silk := encodeAPIRateSILKPacketFrameSize(t, 2, 2880)
	celtShort := encodeAPIRateCELTPacketFrameSize(t, 1, 120)
	if got := ParseTOC(hybrid[0]).Mode; got != ModeHybrid {
		t.Fatalf("Hybrid witness TOC mode=%v", got)
	}
	if got := ParseTOC(silk[0]).Mode; got != ModeSILK {
		t.Fatalf("SILK witness TOC mode=%v", got)
	}

	sampleRates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		sampleRates = append(sampleRates, 96000)
	}
	for _, sampleRate := range sampleRates {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dHz/ch%d", sampleRate, channels), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				assertFixedHybridToSILKSequence(t, dec, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "hybrid-40ms-int16", packet: hybrid, format: libopustest.DecodeDiffFormatInt16},
					{name: "silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
					{name: "silk-60ms-float-followup", packet: silk, format: libopustest.DecodeDiffFormatFloat32},
					{name: "silk-plc-int16", format: libopustest.DecodeDiffFormatInt16},
					{name: "celt-2p5ms-int24-recovery", packet: celtShort, format: libopustest.DecodeDiffFormatInt24},
				})

				// A fresh SILK decode is the control: the one-unit mismatch requires
				// the preceding Hybrid state and its CELT overlap fade.
				fresh, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				assertFixedHybridToSILKSequence(t, fresh, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "fresh-silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
				})

				// Reset must clear the prior Hybrid/CELT history before comparing
				// the same sequence suffix with a newly initialized C decoder.
				dec.Reset()
				assertFixedHybridToSILKSequence(t, dec, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "reset-silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
					{name: "reset-hybrid-40ms-int16", packet: hybrid, format: libopustest.DecodeDiffFormatInt16},
					{name: "reset-hybrid-plc-float", format: libopustest.DecodeDiffFormatFloat32},
					{name: "reset-celt-2p5ms-int24", packet: celtShort, format: libopustest.DecodeDiffFormatInt24},
				})

				assertFixedHybridToSILKWarmZeroAllocs(t, sampleRate, channels, hybrid, silk)
			})
		}
	}
}

func encodeFixedModeSwitchSequence(t *testing.T, channels, frameSize int, modes []EncoderMode) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, len(modes))
	phase := 0.0
	for f, m := range modes {
		if err := enc.SetMode(m); err != nil {
			t.Fatalf("frame %d SetMode: %v", f, err)
		}
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
				0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
			if channels == 2 {
				pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
					0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// encodeFixedSingleModePacket encodes one independent frame in the given forced
// mode from a fresh encoder, so a concatenated CELT-then-Hybrid sequence produces
// a Hybrid frame whose previous frame was CELT but which carries no redundancy
// (a fresh encoder has no prior frame to prefill from), exercising the pure
// integer transition smooth_fade crossfade.
func encodeFixedSingleModePacket(t *testing.T, channels, frameSize int, mode EncoderMode, phase float64) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := (phase + float64(i)) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
			0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
		}
	}
	pkt, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return append([]byte(nil), pkt...)
}

// TestDecoderFixedPointHybridTransitionParity gates that the public DecodeInt16 /
// DecodeInt24 of a CELT-then-Hybrid transition (no redundancy) is bit-exact with
// the libopus FIXED_POINT opus_decode / opus_decode24 reference. The Hybrid frame
// after a CELT frame triggers the 5 ms CELT PLC pcm_transition decode and the
// integer opus_res smooth_fade crossfade (opus_decode_frame transition path).
// The tested integer outputs match the selected FIXED_POINT reference build.
func TestDecoderFixedPointHybridTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
	}
	cases := []tc{
		{"mono_960", 1, 960},
		{"stereo_960", 2, 960},
		{"mono_480", 1, 480},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			phase := 0.0
			celtPkt := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeCELT, phase)
			phase += float64(c.frameSize)
			hyb1 := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeHybrid, phase)
			phase += float64(c.frameSize)
			hyb2 := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeHybrid, phase)
			packets := [][]byte{celtPkt, hyb1, hyb2}

			if toc := ParseTOC(celtPkt[0]); toc.Mode != ModeCELT {
				t.Fatalf("first packet mode %v, want CELT", toc.Mode)
			}
			if toc := ParseTOC(hyb1[0]); toc.Mode != ModeHybrid {
				t.Fatalf("second packet mode %v, want Hybrid", toc.Mode)
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			if dec16.fixedTransitionApplied == 0 {
				t.Fatalf("stream did not exercise the integer transition crossfade")
			}
			t.Logf("integer transition applied=%d, redundancy applied=%d",
				dec16.fixedTransitionApplied, dec16.fixedRedundancyApplied)

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointHybridRedundancyTransitionParity gates that the public
// DecodeInt16 / DecodeInt24 of a mode-switching stream (Hybrid <-> CELT) remains
// bit-exact with the libopus FIXED_POINT opus_decode / opus_decode24 reference
// across the frames that carry CELT redundancy and the CELT->SILK/Hybrid
// transition smooth_fade. The integer redundancy frame is decoded in the opus_res
// domain on the same integer CELT decoder as the main hybrid highband, and the
// crossfade uses the integer smooth_fade (FIXED_POINT ENABLE_RES24), mirroring
// opus_decode_frame. The tested integer outputs match the selected FIXED_POINT
// reference build.
func TestDecoderFixedPointHybridRedundancyTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		modes     []EncoderMode
	}
	cases := []tc{
		{
			name:      "mono_hybrid_celt_hybrid",
			channels:  1,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_hybrid_celt_hybrid",
			channels:  2,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "mono_celt_hybrid_celt",
			channels:  1,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "mono_480_alternating",
			channels:  1,
			frameSize: 480,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, c.channels, c.frameSize, c.modes)

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			// Confirm the stream actually exercises the integer redundancy /
			// transition smooth_fade path so the gate is meaningful.
			if dec16.fixedRedundancyApplied == 0 && dec16.fixedTransitionApplied == 0 {
				t.Fatalf("stream did not exercise the integer redundancy/transition path "+
					"(redundancy=%d transition=%d)", dec16.fixedRedundancyApplied, dec16.fixedTransitionApplied)
			}
			t.Logf("integer redundancy applied=%d, transition applied=%d",
				dec16.fixedRedundancyApplied, dec16.fixedTransitionApplied)

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointRedundancyCodedOutputChannelParity keeps the CELT
// redundancy decode's packet-coded channel count separate from the public
// decoder output channel count. libopus passes stream_channels as C while
// retaining the decoder's channel count as CC during synthesis.
func TestDecoderFixedPointRedundancyCodedOutputChannelParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	const frameSize = 960
	modes := []EncoderMode{
		EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT,
		EncoderModeHybrid, EncoderModeHybrid,
	}

	for _, tc := range []struct {
		name           string
		codedChannels  int
		outputChannels int
	}{
		{name: "stereo_packet_mono_output", codedChannels: 2, outputChannels: 1},
		{name: "mono_packet_stereo_output", codedChannels: 1, outputChannels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, tc.codedChannels, frameSize, modes)

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, tc.outputChannels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, tc.outputChannels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.outputChannels))
			if err != nil {
				t.Fatal(err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.outputChannels))
			if err != nil {
				t.Fatal(err)
			}
			for p, packet := range packets {
				out16 := make([]int16, frameSize*tc.outputChannels)
				if _, err := dec16.DecodeInt16(packet, out16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				frame16 := int16ToInt32(out16)
				out24 := make([]int32, frameSize*tc.outputChannels)
				if _, err := dec24.DecodeInt24(packet, out24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				start := p * frameSize * tc.outputChannels
				assertFixedExact(t, fmt.Sprintf("int16 packet %d", p), frame16, int16ToInt32(refInt16[start:start+len(frame16)]))
				assertFixedExact(t, fmt.Sprintf("int24 packet %d", p), out24, refInt24[start:start+len(out24)])
			}

			if dec16.fixedRedundancyApplied == 0 {
				t.Fatalf("stream did not exercise integer redundancy decode")
			}
		})
	}
}

func decodeWithLibopusFixedPhaseControl(sampleFormat uint32, sampleRate, channels, frameSize int, disabled bool, packets [][]byte) (*libopustest.OracleReader, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	phase := uint32(0)
	if disabled {
		phase = 1
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 9,
		sampleFormat, uint32(sampleRate), 0, phase,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference phase-control decode", "GOSO")
}

func TestFixedPointPhaseInversionControlMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, channels, frameSize = 48000, 2, 960
	packets := encodeFixedModeSwitchSequence(t, channels, frameSize,
		[]EncoderMode{EncoderModeCELT, EncoderModeCELT, EncoderModeCELT})
	if len(packets) < 2 {
		t.Fatalf("encoder produced %d packets", len(packets))
	}
	for i, packet := range packets {
		if len(packet) == 0 || packet[0]&4 == 0 {
			t.Fatalf("packet %d is not stereo-coded: %x", i, packet[:min(len(packet), 1)])
		}
	}

	for _, phaseDisabled := range []bool{true, false} {
		name := "phase_enabled"
		if phaseDisabled {
			name = "phase_disabled"
		}
		t.Run(name, func(t *testing.T) {
			for _, sampleFormat := range []uint32{libopusRefdecodeSingleFormatInt16, libopusRefdecodeSingleFormatInt24} {
				refReader, err := decodeWithLibopusFixedPhaseControl(sampleFormat, sampleRate, channels, frameSize, phaseDisabled, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference phase-control decode", err)
					return
				}
				count := refReader.Count(-1)
				if count != len(packets)*frameSize*channels {
					t.Fatalf("reference samples=%d want %d", count, len(packets)*frameSize*channels)
				}
				want16 := make([]int16, count)
				want24 := make([]int32, count)
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					refReader.ExpectRemaining(count * 2)
					for i := range want16 {
						want16[i] = refReader.I16()
					}
				} else {
					refReader.ExpectRemaining(count * 4)
					for i := range want24 {
						want24[i] = refReader.I32()
					}
				}
				if err := refReader.ExpectConsumed(); err != nil {
					t.Fatal(err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				if dec.PhaseInversionDisabled() {
					t.Fatal("stereo decoder default disables phase inversion")
				}
				if phaseDisabled {
					// Set before the fixed CELT sidecar exists; its lazy creation
					// must inherit the public decoder control.
					dec.SetPhaseInversionDisabled(true)
				}
				got16 := make([]int16, 0, count)
				got24 := make([]int32, 0, count)
				out16 := make([]int16, frameSize*channels)
				out24 := make([]int32, frameSize*channels)
				for p, packet := range packets {
					if sampleFormat == libopusRefdecodeSingleFormatInt16 {
						if n, err := dec.DecodeInt16(packet, out16); err != nil || n != frameSize {
							t.Fatalf("packet %d DecodeInt16 samples=%d err=%v", p, n, err)
						}
						got16 = append(got16, out16...)
					} else {
						if n, err := dec.DecodeInt24(packet, out24); err != nil || n != frameSize {
							t.Fatalf("packet %d DecodeInt24 samples=%d err=%v", p, n, err)
						}
						got24 = append(got24, out24...)
					}
				}
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					assertFixedExact(t, "phase-control int16", int16ToInt32(got16), int16ToInt32(want16))
				} else {
					assertFixedExact(t, "phase-control int24", got24, want24)
				}

				// A live control update reaches an already initialized Q15 CELT
				// decoder. Reset clears codec history but preserves the control,
				// as OPUS_RESET_STATE does for disable_inv.
				dec.SetPhaseInversionDisabled(!phaseDisabled)
				dec.Reset()
				if got := dec.PhaseInversionDisabled(); got == phaseDisabled {
					t.Fatalf("phase-inversion setting after Reset=%t, want %t", got, !phaseDisabled)
				}
				refReader, err = decodeWithLibopusFixedPhaseControl(sampleFormat, sampleRate, channels, frameSize, !phaseDisabled, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference phase-control reset decode", err)
					return
				}
				count = refReader.Count(-1)
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					refReader.ExpectRemaining(count * 2)
					for i := range want16 {
						want16[i] = refReader.I16()
					}
				} else {
					refReader.ExpectRemaining(count * 4)
					for i := range want24 {
						want24[i] = refReader.I32()
					}
				}
				if err := refReader.ExpectConsumed(); err != nil {
					t.Fatal(err)
				}
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					got16 = got16[:0]
					for p, packet := range packets {
						if n, err := dec.DecodeInt16(packet, out16); err != nil || n != frameSize {
							t.Fatalf("post-reset packet %d DecodeInt16 samples=%d err=%v", p, n, err)
						}
						got16 = append(got16, out16...)
					}
					assertFixedExact(t, "post-reset phase-control int16", int16ToInt32(got16), int16ToInt32(want16))
				} else {
					got24 = got24[:0]
					for p, packet := range packets {
						if n, err := dec.DecodeInt24(packet, out24); err != nil || n != frameSize {
							t.Fatalf("post-reset packet %d DecodeInt24 samples=%d err=%v", p, n, err)
						}
						got24 = append(got24, out24...)
					}
					assertFixedExact(t, "post-reset phase-control int24", got24, want24)
				}

				// Measure the warmed fixed-point CELT path with caller-owned
				// output storage; helper setup and reset are outside the run.
				allocs := testing.AllocsPerRun(20, func() {
					if sampleFormat == libopusRefdecodeSingleFormatInt16 {
						if _, err := dec.DecodeInt16(packets[len(packets)-1], out16); err != nil {
							panic(err)
						}
					} else if _, err := dec.DecodeInt24(packets[len(packets)-1], out24); err != nil {
						panic(err)
					}
				})
				if allocs != 0 {
					t.Fatalf("warmed phase-control decode allocated %g times/run", allocs)
				}
			}
		})
	}
}

var fixedRefdecodeHelper libopustest.HelperCache

// getFixedRefdecodeHelperPath builds the libopus_refdecode_single.c full-pipeline
// opus_decode / opus_decode24 helper against the public fixed-point reference
// selected for this Go build, including ENABLE_QEXT when that tag is active.
func getFixedRefdecodeHelperPath() (string, error) {
	return fixedRefdecodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "fixed-point reference decode",
			OutputBase:  "gopus_libopus_refdecode_fixed",
			SourceFile:  "libopus_refdecode_single.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt", "silk"},
			DeadStrip:   true,
		})
	})
}

// decodeWithLibopusFixedInt16 drives FIXED_POINT opus_decode() for a sequence of
// packets through the refdecode_single helper.
func decodeWithLibopusFixedInt16(sampleRate, channels, frameSize int, packets [][]byte) ([]int16, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt16, uint32(sampleRate), 0,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int16", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// decodeWithLibopusFixedInt24 drives FIXED_POINT opus_decode24() for a sequence
// of packets through the refdecode_single helper.
func decodeWithLibopusFixedInt24(sampleRate, channels, frameSize int, packets [][]byte) ([]int32, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24, uint32(sampleRate), 0,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int24", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// encodeFixedSILKSequence encodes a multi-frame SILK-only stream at the given
// bandwidth so the decode exercises cross-frame SILK + resampler state.
func encodeFixedSILKSequence(t *testing.T, channels, frameSize, frames int, bw Bandwidth) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bw); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	var packets [][]byte
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.22 * float32(math.Sin(2*math.Pi*440*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.18 * float32(math.Sin(2*math.Pi*660*tm))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointSILKParity gates that the public DecodeInt16 / DecodeInt24
// of SILK-only packets is bit-exact with the libopus FIXED_POINT opus_decode /
// opus_decode24 reference. gopus' silk.Decoder is inherently integer (int16
// native samples + the int16 silk_resampler), and the int16/int24 output of a
// SILK-only frame round-trips through float32 without loss, so the existing
// public path is checked against the selected FIXED_POINT reference.
func TestDecoderFixedPointSILKParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		bw        Bandwidth
	}
	cases := []tc{
		{"mono_wb_960", 1, 960, BandwidthWideband},
		{"stereo_wb_960", 2, 960, BandwidthWideband},
		{"mono_wb_480", 1, 480, BandwidthWideband},
		{"mono_mb_960", 1, 960, BandwidthMediumband},
		{"mono_nb_960", 1, 960, BandwidthNarrowband},
	}

	const sampleRate = 48000
	const frames = 5
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedSILKSequence(t, c.channels, c.frameSize, frames, c.bw)
			for _, pkt := range packets {
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeSILK {
					t.Fatalf("encoder produced mode %v, want SILK", toc.Mode)
				}
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int24: %v", err)
			}

			var got16 []int32
			var got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointHybridParity gates that the public DecodeInt16 /
// DecodeInt24 of Hybrid packets is bit-exact with the libopus FIXED_POINT
// opus_decode / opus_decode24 reference. The integer SILK lowband
// (INT16TORES of the resampled int16 SILK output) is combined with the integer
// FIXED_POINT CELT highband (bands 17-21) via internal/fixedpoint.CELTDecoder
// driven from the shared range decoder with celt_accum, mirroring
// opus_decode_frame (start_band=17, celt_accum=1). Multi-frame packets exercise
// the cross-frame integer CELT state (decode_mem, energy histories, post-filter,
// preemph). Bit-exact on every architecture: the integer decode has no
// fused-multiply-add, so there is no per-arch float drift.
func TestDecoderFixedPointHybridParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		frames    int
	}
	cases := []tc{
		{"mono_960", 1, 960, 1},
		{"stereo_960", 2, 960, 1},
		{"mono_480", 1, 480, 1},
		{"mono_960_multi", 1, 960, 4},
		{"stereo_960_multi", 2, 960, 4},
		{"mono_480_multi", 1, 480, 6},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := make([][]byte, 0, c.frames)
			for f := 0; f < c.frames; f++ {
				pkt := encodeAPIRateHybridPacketFrameSize(t, c.channels, c.frameSize)
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeHybrid {
					t.Fatalf("encoder produced mode %v, want Hybrid", toc.Mode)
				}
				packets = append(packets, pkt)
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

func int16ToInt32(in []int16) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func divergence(got, want []int32) (diffs int, maxAbs int64, firstIdx int) {
	firstIdx = -1
	for i := range got {
		d := int64(got[i]) - int64(want[i])
		if d < 0 {
			d = -d
		}
		if d != 0 {
			diffs++
			if firstIdx < 0 {
				firstIdx = i
			}
		}
		if d > maxAbs {
			maxAbs = d
		}
	}
	return
}

// assertFixedExact requires bit-exact equality on every architecture. The
// gopus_fixed_point integer CELT/SILK/Hybrid decode is pure integer arithmetic
// with no fused-multiply-add, so it is deterministic and bit-identical to the
// FIXED_POINT reference on amd64 and darwin/arm64 alike — there is no per-arch
// float drift in this path.
func assertFixedExact(t *testing.T, label string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length got=%d want=%d", label, len(got), len(want))
	}
	diffs, maxAbs, firstIdx := divergence(got, want)
	if diffs == 0 {
		t.Logf("%s: bit-exact (%d samples)", label, len(got))
		return
	}
	t.Errorf("%s: %d/%d samples differ, maxAbs=%d, first at %d: gopus=%d libopus=%d",
		label, diffs, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
}

func decodeFixedStereoEdge(t *testing.T, sampleRate, channels, frameSize int, steps [][]byte) *Decoder {
	t.Helper()

	refInt16, err := decodeWithLibopusFixedInt16(sampleRate, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
		return nil
	}
	refInt24, err := decodeWithLibopusFixedInt24(sampleRate, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
		return nil
	}

	dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder int16: %v", err)
	}
	dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder int24: %v", err)
	}

	var got16, got24 []int32
	for p, pkt := range steps {
		o16 := make([]int16, frameSize*channels)
		if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
			t.Fatalf("step %d DecodeInt16: %v", p, err)
		}
		got16 = append(got16, int16ToInt32(o16)...)
		o24 := make([]int32, frameSize*channels)
		if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
			t.Fatalf("step %d DecodeInt24: %v", p, err)
		}
		got24 = append(got24, o24...)
	}

	assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
	assertFixedExact(t, "int24", got24, refInt24)
	return dec16
}

// TestDecoderFixedPointStereoCELTRecoveryParity hardens the STEREO CELT
// packet-loss recovery edge: a multi-frame loss burst followed by SEVERAL good
// received frames so the integer celt_decode_with_ec coarse-energy prediction
// safety block (loss_duration != 0) and the post-filter / decode_mem recovery
// are exercised under the integer path across the full recovery tail (not just
// the first frame after the loss). Bit-exact int16 + int24 vs the FIXED_POINT
// opus_decode / opus_decode24 oracle.
func TestDecoderFixedPointStereoCELTRecoveryParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		frames    int
		lossAt    map[int]bool
	}
	cases := []tc{
		{"stereo_960_burst_recover", 960, 10, map[int]bool{2: true, 3: true, 4: true}},
		{"stereo_480_burst_recover", 480, 12, map[int]bool{3: true, 4: true, 5: true}},
		{"stereo_240_burst_recover", 240, 12, map[int]bool{4: true, 5: true}},
		{"stereo_960_single_recover", 960, 8, map[int]bool{3: true}},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, channels, c.frameSize, c.frames)
			steps := make([][]byte, c.frames)
			for i := 0; i < c.frames; i++ {
				if c.lossAt[i] {
					steps[i] = nil
				} else {
					steps[i] = encoded[i]
				}
			}
			decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, steps)
		})
	}
}

// TestDecoderFixedPointStereoCELTTransitionParity gates the STEREO CELT<->SILK
// (here CELT->Hybrid) transition smooth_fade under the integer path: a CELT
// frame followed by Hybrid frames triggers the integer 5 ms CELT PLC
// pcm_transition decode and the integer opus_res smooth_fade crossfade. The
// stereo transition crossfade interleaves both channels, which the mono gate
// does not cover.
func TestDecoderFixedPointStereoCELTTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	const channels = 2
	for _, frameSize := range []int{960, 480} {
		t.Run("stereo_"+itoaSmall(frameSize), func(t *testing.T) {
			phase := 0.0
			celtPkt := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeCELT, phase)
			phase += float64(frameSize)
			hyb1 := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeHybrid, phase)
			phase += float64(frameSize)
			hyb2 := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeHybrid, phase)
			if toc := ParseTOC(celtPkt[0]); toc.Mode != ModeCELT {
				t.Fatalf("first packet mode %v, want CELT", toc.Mode)
			}
			if toc := ParseTOC(hyb1[0]); toc.Mode != ModeHybrid {
				t.Fatalf("second packet mode %v, want Hybrid", toc.Mode)
			}
			steps := [][]byte{celtPkt, hyb1, hyb2}
			dec := decodeFixedStereoEdge(t, sampleRate, channels, frameSize, steps)
			if dec == nil {
				return
			}
			if dec.fixedTransitionApplied == 0 {
				t.Fatalf("stereo stream did not exercise the integer transition crossfade")
			}
			t.Logf("integer transition applied=%d, redundancy applied=%d",
				dec.fixedTransitionApplied, dec.fixedRedundancyApplied)
		})
	}
}

// TestDecoderFixedPointStereoRedundancyBothDirectionsParity gates STEREO Hybrid
// CELT redundancy in BOTH directions on the integer path:
//   - SILK->CELT redundancy: a Hybrid (SILK lowband) frame followed by a CELT
//     frame carries a redundant CELT frame whose tail is smooth_faded into the
//     SILK-derived output (fixedApplyRedundancySilkToCelt).
//   - CELT->SILK redundancy: a CELT frame followed by a Hybrid frame prefills
//     the Hybrid frame's head from the redundant CELT decode
//     (fixedApplyRedundancyCeltToSilk).
//
// Mode-switch streams (Hybrid<->CELT) on a single encoder produce both. The
// stereo crossfade interleaves both channels. Bit-exact int16 + int24 vs the
// FIXED_POINT oracle.
func TestDecoderFixedPointStereoRedundancyBothDirectionsParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		modes     []EncoderMode
	}
	cases := []tc{
		{
			name:      "stereo_hybrid_celt_hybrid_960",
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_alternating_960",
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_alternating_480",
			frameSize: 480,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, channels, c.frameSize, c.modes)
			dec := decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, packets)
			if dec == nil {
				return
			}
			if dec.fixedRedundancyApplied == 0 && dec.fixedTransitionApplied == 0 {
				t.Fatalf("stereo stream did not exercise the integer redundancy/transition path "+
					"(redundancy=%d transition=%d)", dec.fixedRedundancyApplied, dec.fixedTransitionApplied)
			}
			t.Logf("integer redundancy applied=%d, transition applied=%d",
				dec.fixedRedundancyApplied, dec.fixedTransitionApplied)
		})
	}
}

// TestDecoderFixedPointStereoLongPLCChurnParity gates a LONG (100+ frame) STEREO
// CELT stream with periodic packet loss so the integer decoder's cross-frame
// state (decode_mem, per-band energy histories, post-filter taps/gains,
// preemph memory, loss_duration / fade decay) is churned over many frames. Each
// periodic loss + recovery re-enters the coarse-energy prediction safety block
// from a different accumulated state, and any per-frame state divergence between
// the integer path and the FIXED_POINT oracle compounds and surfaces. Bit-exact
// int16 + int24 over the whole sequence.
func TestDecoderFixedPointStereoLongPLCChurnParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		frames    int
		// lossPeriod marks every lossPeriod-th frame (after warmup) as lost.
		lossPeriod int
		warmup     int
	}
	cases := []tc{
		{"stereo_960_120f_p13", 960, 120, 13, 4},
		{"stereo_480_160f_p11", 480, 160, 11, 4},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, channels, c.frameSize, c.frames)
			steps := make([][]byte, c.frames)
			losses := 0
			for i := 0; i < c.frames; i++ {
				if i >= c.warmup && (i-c.warmup)%c.lossPeriod == 0 {
					steps[i] = nil
					losses++
				} else {
					steps[i] = encoded[i]
				}
			}
			if losses == 0 {
				t.Fatalf("no losses scheduled")
			}
			t.Logf("%d frames, %d periodic losses", c.frames, losses)
			decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, steps)
		})
	}
}

// TestDecoderFixedPointSubRateParity gates the integer int16 / int24 decode path
// at the sub-48k API output rates (24/16/12/8 kHz) for CELT, SILK and Hybrid
// streams. The packets are 48 kHz-domain frames; the public DecodeInt16 /
// DecodeInt24 decode them at the configured API rate (the integer CELT decoder
// runs at the core rate and decimates by 48000/Fs). Where the integer path
// declines a sub-48k case (it does for CELT-only loss frames, which decline so
// the float conversion conceals), the output still matches the FIXED_POINT
// oracle because the float fallback is itself FIXED_POINT-derived. Every step
// here is a RECEIVED frame, so the received CELT / Hybrid integer decimation and
// the inherently-integer SILK resampler are all exercised. Bit-exact int16 +
// int24 vs the FIXED_POINT opus_decode / opus_decode24 oracle.
func TestDecoderFixedPointSubRateParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const coreFrame = 960 // 20 ms at 48 kHz
	const frames = 5

	subRates := []int{24000, 16000, 12000, 8000}

	for _, channels := range []int{1, 2} {
		// CELT-only.
		t.Run("celt_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeFixedCELTSequence(t, channels, coreFrame, frames)
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})

		// SILK-only (wideband). The SILK decoder is inherently integer and its
		// int16 resampler reproduces the FIXED_POINT lowband at the API rate.
		t.Run("silk_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeFixedSILKSequence(t, channels, coreFrame, frames, BandwidthWideband)
			for _, pkt := range packets {
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeSILK {
					t.Fatalf("encoder produced mode %v, want SILK", toc.Mode)
				}
			}
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})

		// Hybrid. The integer SILK lowband + integer CELT highband combine and
		// then decimate to the API rate.
		t.Run("hybrid_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := make([][]byte, 0, frames)
			for f := 0; f < frames; f++ {
				pkt := encodeAPIRateHybridPacketFrameSizeVariant(t, channels, coreFrame, f)
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeHybrid {
					t.Fatalf("encoder produced mode %v, want Hybrid", toc.Mode)
				}
				packets = append(packets, append([]byte(nil), pkt...))
			}
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})
	}
}

func TestDecoderFixedPointHybridTransitionWarmZeroAlloc(t *testing.T) {
	for _, channels := range []int{1, 2} {
		celt := encodeFixedSingleModePacket(t, channels, 960, EncoderModeCELT, 0)
		hybrid := encodeFixedSingleModePacket(t, channels, 960, EncoderModeHybrid, 960)
		for _, rate := range []int{8000, 48000} {
			t.Run(fmt.Sprintf("ch%d_%dHz", channels, rate), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
				if err != nil {
					t.Fatal(err)
				}
				frameSize := rate / 50
				out := make([]int16, frameSize*channels)
				for i := 0; i < 3; i++ {
					if _, err := dec.DecodeInt16(celt, out); err != nil {
						t.Fatal(err)
					}
					if _, err := dec.DecodeInt16(hybrid, out); err != nil {
						t.Fatal(err)
					}
				}
				var celtN, hybridN int
				allocs := testing.AllocsPerRun(100, func() {
					celtN, err = dec.DecodeInt16(celt, out)
					if err != nil {
						return
					}
					hybridN, err = dec.DecodeInt16(hybrid, out)
				})
				if err != nil || celtN != frameSize || hybridN != frameSize {
					t.Fatalf("transition samples CELT=%d Hybrid=%d want %d: %v", celtN, hybridN, frameSize, err)
				}
				if allocs != 0 {
					t.Fatalf("warm CELT/Hybrid transition allocs=%g want 0", allocs)
				}
				active := false
				for _, sample := range out {
					active = active || sample != 0
				}
				if !active {
					t.Fatal("warm Hybrid output is silent")
				}
			})
		}
	}
}

func TestPublicFixedAutoSILKHybridSequencesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name, wantMode string
		sampleRate     int
		channels       int
		frameSize      int
		bitrate        int
		application    Application
		oracleApp      int
	}{
		{
			name: "voip-16k-mono-low-rate", wantMode: "silk",
			sampleRate: 16000, channels: 1, frameSize: 320, bitrate: 12000,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
		},
		{
			name: "voip-48k-mono-medium-rate", wantMode: "hybrid",
			sampleRate: 48000, channels: 1, frameSize: 960, bitrate: 32000,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
		},
	}
	const frameCount = 8
	const resetFrame = 5
	const maxPacketBytes = 4000
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pcm, err := testsignal.GenerateEncoderSignalVariant(
				testsignal.EncoderVariantSpeechLikeV1,
				tc.sampleRate,
				tc.frameSize*tc.channels*frameCount,
				tc.channels,
			)
			if err != nil {
				t.Fatal(err)
			}
			frames := make([]libopustest.OpusEncodeFixedMixedFrame, frameCount)
			for frame := range frameCount {
				start := frame * tc.frameSize * tc.channels
				frames[frame] = libopustest.OpusEncodeFixedMixedFrame{
					Format: 1, FloatPCM: pcm[start : start+tc.frameSize*tc.channels],
					ResetBefore: frame == resetFrame,
				}
			}
			params := libopustest.OpusEncodeFixedParams{
				SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.oracleApp,
				MaxPacketBytes: maxPacketBytes, Bitrate: tc.bitrate, Complexity: 10,
				VBR: true, VBRConstraint: false, LSBDepth: 24,
				FrameSize: tc.frameSize, FrameCount: frameCount,
			}
			var want []libopustest.OpusEncodeFixedRecord
			if extsupport.QEXT {
				want, err = libopustest.ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(params, frames)
			} else {
				want, err = libopustest.ProbeOpusEncodeFixedMixedRecords(params, frames)
			}
			if err != nil {
				libopustest.HelperUnavailable(t, "persistent fixed automatic SILK/Hybrid encoder", err)
				return
			}
			if len(want) != frameCount {
				t.Fatalf("selected fixed C records=%d, want %d", len(want), frameCount)
			}

			enc, err := NewEncoder(EncoderConfig{
				SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.application,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeAuto) },
				func() error { return enc.SetBandwidthAuto() },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetBitrate(tc.bitrate) },
				func() error { return enc.SetComplexity(10) },
				func() error { return enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return enc.SetLSBDepth(24) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}

			packet := make([]byte, maxPacketBytes)
			seen := map[string]bool{}
			encodeSequence := func(label string) {
				t.Helper()
				for frame, input := range frames {
					if input.ResetBefore {
						enc.Reset()
					}
					n, err := enc.Encode(input.FloatPCM, packet)
					if err != nil {
						t.Fatalf("%s frame %d Encode: %v", label, frame, err)
					}
					if want[frame].Status != 0 {
						t.Fatalf("%s frame %d selected fixed C status=%d", label, frame, want[frame].Status)
					}
					mode := fixedPacketMode(want[frame].Packet)
					seen[mode] = true
					if mode != "silk" && mode != "hybrid" {
						t.Fatalf("%s frame %d selected C mode=%s, want automatic SILK/Hybrid", label, frame, mode)
					}
					if n != len(want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange ||
						!bytes.Equal(packet[:n], want[frame].Packet) {
						first := 0
						for first < n && first < len(want[frame].Packet) && packet[first] == want[frame].Packet[first] {
							first++
						}
						t.Fatalf("%s frame %d auto %s packet/range mismatch: bytes Go/C=%d/%d first=%d range=%08x/%08x",
							label, frame, mode, n, len(want[frame].Packet), first, enc.FinalRange(), want[frame].FinalRange)
					}
				}
			}
			encodeSequence("initial")
			if !seen[tc.wantMode] {
				t.Fatalf("selected C sequence did not cover %s mode; modes=%v", tc.wantMode, seen)
			}

			enc.Reset()
			encodeSequence("reset replay")

			warmPCM := frames[frameCount-1].FloatPCM
			for range 2 {
				if _, err := enc.Encode(warmPCM, packet); err != nil {
					t.Fatalf("warm automatic %s encode: %v", tc.wantMode, err)
				}
			}
			var allocErr error
			allocs := testing.AllocsPerRun(20, func() {
				if _, err := enc.Encode(warmPCM, packet); err != nil {
					allocErr = err
				}
			})
			if allocErr != nil {
				t.Fatalf("warm automatic %s cycle: %v", tc.wantMode, allocErr)
			}
			if allocs != 0 {
				t.Fatalf("warmed automatic %s caller-buffer cycle allocated %g objects", tc.wantMode, allocs)
			}
		})
	}
}

func fixedPacketMode(packet []byte) string {
	if len(packet) == 0 {
		return "empty"
	}
	config := int(packet[0] >> 3)
	switch {
	case config < 12:
		return "silk"
	case config < 16:
		return "hybrid"
	default:
		return "celt"
	}
}

func TestPublicFixedShortEncodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	pcm := genPCMInt16Pub(6716, 1, frameSize, frames, false)
	ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return e.SetMode(EncoderModeCELT) },
		func() error { return e.SetBandwidth(BandwidthFullband) },
		func() error { return e.SetFrameSize(frameSize) },
		func() error { return e.SetComplexity(0) },
		func() error { return e.SetBitrate(64000) },
		func() error { return e.SetBitrateMode(BitrateModeCBR) },
		func() error { return e.SetForceChannels(1) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, 4000)
	for frame := range frames {
		if frame == 3 {
			if _, err := e.EncodeInt16(pcm[frame*frameSize:(frame+1)*frameSize], nil); err != ErrBufferTooSmall {
				t.Fatalf("short output buffer error=%v want=%v", err, ErrBufferTooSmall)
			}
		}
		n, err := e.EncodeInt16(pcm[frame*frameSize:(frame+1)*frameSize], packet)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if ref[frame].Status != 0 || n != len(ref[frame].Packet) ||
			e.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
			t.Fatalf("frame %d len=%d want=%d range=%08x want=%08x", frame,
				n, len(ref[frame].Packet), e.FinalRange(), ref[frame].FinalRange)
		}
	}
	last := pcm[(frames-1)*frameSize:]
	var lastN int
	var lastErr error
	for range 3 {
		lastN, lastErr = e.EncodeInt16(last, packet)
		if lastErr != nil || lastN < 2 {
			t.Fatalf("warm encode len=%d err=%v", lastN, lastErr)
		}
	}
	allocs := testing.AllocsPerRun(20, func() {
		lastN, lastErr = e.EncodeInt16(last, packet)
	})
	if lastErr != nil || lastN < 2 || allocs != 0 {
		t.Fatalf("active caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
	}
}

// TestPublicFixedVoIPShortCELTMatchesLibopus covers the VoIP high-pass filter
// on the public short-input path while CELT is forced for every packet.
func TestPublicFixedVoIPShortCELTMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 6
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels%d", channels), func(t *testing.T) {
			pcm := genPCMInt16Pub(uint32(53417+channels*31), channels, frameSize, frames, false)
			bitrate := 64000 * channels
			ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationVoIP,
				MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
				Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 0,
				ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
			})
			if err != nil {
				t.Fatal(err)
			}
			e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationVoIP})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return e.SetMode(EncoderModeCELT) },
				func() error { return e.SetBandwidth(BandwidthFullband) },
				func() error { return e.SetFrameSize(frameSize) },
				func() error { return e.SetComplexity(0) },
				func() error { return e.SetBitrate(bitrate) },
				func() error { return e.SetBitrateMode(BitrateModeCBR) },
				func() error { return e.SetForceChannels(channels) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			packet := make([]byte, 4000)
			stride := frameSize * channels
			for frame := range frames {
				n, err := e.EncodeInt16(pcm[frame*stride:(frame+1)*stride], packet)
				if err != nil || ref[frame].Status != 0 || n != len(ref[frame].Packet) ||
					e.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
					t.Fatalf("frame %d err=%v len=%d/%d range=%08x/%08x", frame, err,
						n, len(ref[frame].Packet), e.FinalRange(), ref[frame].FinalRange)
				}
			}

			activeInput := pcm[(frames-1)*stride:]
			var lastN int
			var lastErr error
			for range 3 {
				lastN, lastErr = e.EncodeInt16(activeInput, packet)
				if lastErr != nil || lastN < 2 {
					t.Fatalf("warm VoIP CELT encode len=%d err=%v", lastN, lastErr)
				}
			}
			allocs := testing.AllocsPerRun(20, func() {
				lastN, lastErr = e.EncodeInt16(activeInput, packet)
			})
			if lastErr != nil || lastN < 2 || allocs != 0 {
				t.Fatalf("active VoIP CELT caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
			}
		})
	}
}

func TestPublicFixedShortExpertFrameDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 5760, 3
	pcm := genPCMInt16Pub(6716, 1, frameSize, frames, false)
	ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, LSBDepth: 24, FrameSize: frameSize,
		ExpertFrameDuration: int(ExpertFrameDuration20Ms),
	}, []libopustest.OpusEncodeFixedMixedFrame{{
		Format: 0, ShortPCM: pcm[:frameSize],
	}, {
		Format: 0, ShortPCM: pcm[frameSize : 2*frameSize],
	}, {
		Format: 0, ShortPCM: pcm[2*frameSize:],
	}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return e.SetMode(EncoderModeCELT) },
		func() error { return e.SetBandwidth(BandwidthFullband) },
		func() error { return e.SetFrameSize(frameSize) },
		func() error { return e.SetExpertFrameDuration(ExpertFrameDuration20Ms) },
		func() error { return e.SetComplexity(0) },
		func() error { return e.SetBitrate(64000) },
		func() error { return e.SetBitrateMode(BitrateModeCBR) },
		func() error { return e.SetForceChannels(1) },
		func() error { return e.SetLSBDepth(24) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, 4000)
	for f := range frames {
		n, err := e.EncodeInt16(pcm[f*frameSize:(f+1)*frameSize], packet)
		if err != nil || ref[f].Status != 0 || n != len(ref[f].Packet) ||
			e.FinalRange() != ref[f].FinalRange || !bytes.Equal(packet[:n], ref[f].Packet) {
			t.Fatalf("frame %d err=%v len=%d/%d range=%08x/%08x", f, err,
				n, len(ref[f].Packet), e.FinalRange(), ref[f].FinalRange)
		}
	}
}

// TestPublicFixedLongCELTPacketsMatchLibopus exercises the 40/60 ms public
// packet splitter with a persistent Q8 DC/delay history across three packets.
func TestPublicFixedLongCELTPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, app := range []struct {
		pub Application
		ref int
	}{
		{ApplicationAudio, libopustest.OpusApplicationAudio},
		{ApplicationLowDelay, libopustest.OpusApplicationRestrictedLowDelay},
		{ApplicationVoIP, libopustest.OpusApplicationVoIP},
	} {
		for _, channels := range []int{1, 2} {
			for _, frameSize := range []int{1920, 2880} {
				for _, rateMode := range []struct {
					mode      BitrateMode
					vbr, cvbr bool
				}{
					{BitrateModeCBR, false, false},
					{BitrateModeCVBR, true, true},
					{BitrateModeVBR, true, false},
				} {
					t.Run(fmt.Sprintf("app%d/ch%d/fs%d/mode%d", app.ref, channels, frameSize, rateMode.mode), func(t *testing.T) {
						const frames = 3
						seed := uint32(6716 + channels*13 + frameSize*19 + int(rateMode.mode)*31 + app.ref)
						pcm := genPCMInt16Pub(seed, channels, frameSize, frames, false)
						bitrate := 64000
						if channels == 2 {
							bitrate = 128000
						}
						ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
							SampleRate: 48000, Channels: channels, Application: app.ref,
							MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
							Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: 0,
							VBR: rateMode.vbr, VBRConstraint: rateMode.cvbr,
							ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
						})
						if err != nil {
							t.Fatal(err)
						}
						e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: app.pub})
						if err != nil {
							t.Fatal(err)
						}
						for _, set := range []func() error{
							func() error { return e.SetMode(EncoderModeCELT) },
							func() error { return e.SetBandwidth(BandwidthFullband) },
							func() error { return e.SetFrameSize(frameSize) },
							func() error { return e.SetBitrate(bitrate) },
							func() error { return e.SetComplexity(0) },
							func() error { return e.SetBitrateMode(rateMode.mode) },
							func() error { return e.SetForceChannels(channels) },
						} {
							if err := set(); err != nil {
								t.Fatal(err)
							}
						}
						packet := make([]byte, 4000)
						stride := frameSize * channels
						for f := range frames {
							n, err := e.EncodeInt16(pcm[f*stride:(f+1)*stride], packet)
							if err != nil || ref[f].Status != 0 || n != len(ref[f].Packet) ||
								e.FinalRange() != ref[f].FinalRange || !bytes.Equal(packet[:n], ref[f].Packet) {
								t.Fatalf("frame %d err=%v len=%d/%d range=%08x/%08x", f, err,
									n, len(ref[f].Packet), e.FinalRange(), ref[f].FinalRange)
							}
						}
						last := pcm[(frames-1)*stride:]
						var lastN int
						var lastErr error
						for range 3 {
							lastN, lastErr = e.EncodeInt16(last, packet)
							if lastErr != nil || lastN < 2 {
								t.Fatalf("warm encode len=%d err=%v", lastN, lastErr)
							}
						}
						allocs := testing.AllocsPerRun(20, func() {
							lastN, lastErr = e.EncodeInt16(last, packet)
						})
						if lastErr != nil || lastN < 2 || allocs != 0 {
							t.Fatalf("active long caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
						}
					})
				}
			}
		}
	}
}

func TestPublicFixedInputAPIsShareQ8History(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 9
	short := genPCMInt16Pub(91247, 1, frameSize, frames, false)
	mixed := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
	for f := range frames {
		pcm16 := short[f*frameSize : (f+1)*frameSize]
		mixed[f].Format = uint32(f % 3)
		mixed[f].ResetBefore = f == 6
		switch mixed[f].Format {
		case 0:
			mixed[f].ShortPCM = pcm16
		case 1:
			mixed[f].FloatPCM = make([]float32, frameSize)
			for i, sample := range pcm16 {
				mixed[f].FloatPCM[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
			}
		case 2:
			mixed[f].PCM24 = make([]int32, frameSize)
			for i, sample := range pcm16 {
				mixed[f].PCM24[i] = int32(sample)<<8 + int32(i%17-8)
			}
		}
	}
	ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: 48000, Channels: 1, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
		Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0,
		ForceChannels: 1, FrameSize: frameSize,
	}, mixed)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return e.SetMode(EncoderModeCELT) },
		func() error { return e.SetBandwidth(BandwidthFullband) },
		func() error { return e.SetFrameSize(frameSize) },
		func() error { return e.SetComplexity(0) },
		func() error { return e.SetBitrate(64000) },
		func() error { return e.SetBitrateMode(BitrateModeCBR) },
		func() error { return e.SetForceChannels(1) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, 4000)
	for f, frame := range mixed {
		if frame.ResetBefore {
			e.Reset()
		}
		var n int
		switch frame.Format {
		case 0:
			n, err = e.EncodeInt16(frame.ShortPCM, packet)
		case 1:
			n, err = e.Encode(frame.FloatPCM, packet)
		case 2:
			n, err = e.EncodeInt24(frame.PCM24, packet)
		}
		if err != nil || ref[f].Status != 0 || n != len(ref[f].Packet) ||
			e.FinalRange() != ref[f].FinalRange || !bytes.Equal(packet[:n], ref[f].Packet) {
			first := 0
			for first < n && first < len(ref[f].Packet) && packet[first] == ref[f].Packet[first] {
				first++
			}
			t.Fatalf("frame %d format=%d err=%v len=%d/%d first=%d range=%08x/%08x", f,
				frame.Format, err, n, len(ref[f].Packet), first, e.FinalRange(), ref[f].FinalRange)
		}
	}
	var lastErr error
	var lastN int
	encodeCycle := func() {
		lastN, lastErr = e.EncodeInt16(mixed[0].ShortPCM, packet)
		if lastErr != nil {
			return
		}
		lastN, lastErr = e.Encode(mixed[1].FloatPCM, packet)
		if lastErr != nil {
			return
		}
		lastN, lastErr = e.EncodeInt24(mixed[2].PCM24, packet)
	}
	for range 3 {
		encodeCycle()
		if lastErr != nil || lastN < 2 {
			t.Fatalf("warm mixed encode len=%d err=%v", lastN, lastErr)
		}
	}
	allocs := testing.AllocsPerRun(20, encodeCycle)
	if lastErr != nil || lastN < 2 || allocs != 0 {
		t.Fatalf("active mixed caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
	}
}

func TestPublicFixedVoIPInputAPIsShareQ8History(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 9
	for _, channels := range []int{1, 2} {
		for _, complexity := range []int{0, 10} {
			t.Run(fmt.Sprintf("channels%d/complexity%d", channels, complexity), func(t *testing.T) {
				stride := frameSize * channels
				short := genPCMInt16Pub(uint32(91247+channels*13+complexity*31), channels, frameSize, frames, false)
				mixed := make([]libopustest.OpusEncodeFixedMixedFrame, frames)
				for f := range frames {
					pcm16 := short[f*stride : (f+1)*stride]
					mixed[f].Format = uint32(f % 3)
					mixed[f].ResetBefore = f == 6
					switch mixed[f].Format {
					case 0:
						mixed[f].ShortPCM = pcm16
					case 1:
						mixed[f].FloatPCM = make([]float32, stride)
						for i, sample := range pcm16 {
							mixed[f].FloatPCM[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
						}
					case 2:
						mixed[f].PCM24 = make([]int32, stride)
						for i, sample := range pcm16 {
							mixed[f].PCM24[i] = int32(sample)<<8 + int32(i%17-8)
						}
					}
				}
				bitrate := 64000 * channels
				ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
					SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationVoIP,
					MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
					Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: bitrate, Complexity: complexity,
					ForceChannels: channels, FrameSize: frameSize,
				}, mixed)
				if err != nil {
					t.Fatal(err)
				}
				e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationVoIP})
				if err != nil {
					t.Fatal(err)
				}
				for _, set := range []func() error{
					func() error { return e.SetMode(EncoderModeCELT) },
					func() error { return e.SetBandwidth(BandwidthFullband) },
					func() error { return e.SetFrameSize(frameSize) },
					func() error { return e.SetComplexity(complexity) },
					func() error { return e.SetBitrate(bitrate) },
					func() error { return e.SetBitrateMode(BitrateModeCBR) },
					func() error { return e.SetForceChannels(channels) },
				} {
					if err := set(); err != nil {
						t.Fatal(err)
					}
				}
				packet := make([]byte, 4000)
				for f, frame := range mixed {
					if frame.ResetBefore {
						e.Reset()
					}
					var n int
					switch frame.Format {
					case 0:
						n, err = e.EncodeInt16(frame.ShortPCM, packet)
					case 1:
						n, err = e.Encode(frame.FloatPCM, packet)
					case 2:
						n, err = e.EncodeInt24(frame.PCM24, packet)
					}
					if err != nil || ref[f].Status != 0 || n != len(ref[f].Packet) ||
						e.FinalRange() != ref[f].FinalRange || !bytes.Equal(packet[:n], ref[f].Packet) {
						first := 0
						for first < n && first < len(ref[f].Packet) && packet[first] == ref[f].Packet[first] {
							first++
						}
						t.Fatalf("frame %d format=%d err=%v len=%d/%d first=%d range=%08x/%08x", f,
							frame.Format, err, n, len(ref[f].Packet), first, e.FinalRange(), ref[f].FinalRange)
					}
				}

				var lastN int
				var lastErr error
				encodeCycle := func() {
					lastN, lastErr = e.EncodeInt16(mixed[0].ShortPCM, packet)
					if lastErr != nil {
						return
					}
					lastN, lastErr = e.Encode(mixed[1].FloatPCM, packet)
					if lastErr != nil {
						return
					}
					lastN, lastErr = e.EncodeInt24(mixed[2].PCM24, packet)
				}
				for range 3 {
					encodeCycle()
					if lastErr != nil || lastN < 2 {
						t.Fatalf("warm mixed VoIP encode len=%d err=%v", lastN, lastErr)
					}
				}
				allocs := testing.AllocsPerRun(20, encodeCycle)
				if lastErr != nil || lastN < 2 || allocs != 0 {
					t.Fatalf("active mixed VoIP caller-buffer encode len=%d err=%v allocs=%g", lastN, lastErr, allocs)
				}
			})
		}
	}
}

// TestPublicFixedStereoWidthFadeMatchesLibopus exercises the fixed Q15
// stereo_fade path that CELT uses when its effective rate narrows stereo width.
func TestPublicFixedStereoWidthFadeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames, channels = 960, 3, 2
	pcm := genPCMInt16Pub(11849, channels, frameSize, frames, false)
	for _, app := range []struct {
		pub Application
		ref int
	}{
		{ApplicationAudio, libopustest.OpusApplicationAudio},
		{ApplicationLowDelay, libopustest.OpusApplicationRestrictedLowDelay},
	} {
		ref, err := libopustest.ProbeOpusEncodeFixedRecords(libopustest.OpusEncodeFixedParams{
			SampleRate: 48000, Channels: channels, Application: app.ref,
			MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
			Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 32000, Complexity: 0,
			ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
		})
		if err != nil {
			t.Fatal(err)
		}
		e, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: app.pub})
		if err != nil {
			t.Fatal(err)
		}
		for _, set := range []func() error{
			func() error { return e.SetMode(EncoderModeCELT) },
			func() error { return e.SetBandwidth(BandwidthFullband) },
			func() error { return e.SetFrameSize(frameSize) },
			func() error { return e.SetComplexity(0) },
			func() error { return e.SetBitrate(32000) },
			func() error { return e.SetBitrateMode(BitrateModeCBR) },
			func() error { return e.SetForceChannels(channels) },
		} {
			if err := set(); err != nil {
				t.Fatal(err)
			}
		}
		packet := make([]byte, 4000)
		for frame := range frames {
			input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
			n, err := e.EncodeInt16(input, packet)
			if err != nil || ref[frame].Status != 0 || n != len(ref[frame].Packet) ||
				e.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
				t.Fatalf("app=%d frame=%d err=%v len=%d/%d range=%08x/%08x", app.ref, frame,
					err, n, len(ref[frame].Packet), e.FinalRange(), ref[frame].FinalRange)
			}
		}
		activeInput := pcm[:frameSize*channels]
		var lastN int
		var lastErr error
		for range 3 {
			lastN, lastErr = e.EncodeInt16(activeInput, packet)
			if lastErr != nil || lastN < 2 {
				t.Fatalf("warm stereo fade len=%d err=%v", lastN, lastErr)
			}
		}
		allocs := testing.AllocsPerRun(20, func() {
			lastN, lastErr = e.EncodeInt16(activeInput, packet)
		})
		if lastErr != nil || lastN < 2 || allocs != 0 {
			t.Fatalf("active stereo fade len=%d err=%v allocs=%g", lastN, lastErr, allocs)
		}
	}
}

type publicFixedOuterCase struct {
	name          string
	mode          EncoderMode
	oracleMode    int
	bandwidth     Bandwidth
	oracleBW      int
	sampleRate    int
	channels      int
	forceChannels int
	frameSize     int
	frames        int
	resetFrame    int
	application   Application
	oracleApp     int
	bitrate       int
	complexity    int
	bitrateMode   BitrateMode
}

func TestPublicFixedSILKHybridInputAPIsMatchLibopus(t *testing.T) {
	const frames = 12
	cases := []publicFixedOuterCase{
		{name: "silk-nb", mode: EncoderModeSILK, oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthNarrowband, oracleBW: libopustest.OpusBandwidthNarrowband},
		{name: "silk-mb", mode: EncoderModeSILK, oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthMediumband, oracleBW: libopustest.OpusBandwidthMediumband},
		{name: "silk-wb", mode: EncoderModeSILK, oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthWideband, oracleBW: libopustest.OpusBandwidthWideband},
		{name: "hybrid-swb", mode: EncoderModeHybrid, oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthSuperwideband, oracleBW: libopustest.OpusBandwidthSuperwideband},
		{name: "hybrid-fb", mode: EncoderModeHybrid, oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthFullband, oracleBW: libopustest.OpusBandwidthFullband},
	}
	for _, tc := range cases {
		for _, channels := range []int{1, 2} {
			tc, channels := tc, channels
			tc.sampleRate = 48000
			tc.channels = channels
			tc.forceChannels = channels
			tc.frameSize = 960
			tc.frames = frames
			tc.resetFrame = 9
			tc.application = ApplicationAudio
			tc.oracleApp = libopustest.OpusApplicationAudio
			tc.bitrate = 24000 * channels
			tc.complexity = 10
			tc.bitrateMode = BitrateModeCBR
			runPublicFixedOuterCase(t, tc)
		}
	}
}

func TestPublicFixedSILKHybridRatesDurationsAndDownmixMatchLibopus(t *testing.T) {
	cases := []publicFixedOuterCase{
		{
			name: "silk-16k-10ms-voip-downmix-vbr", mode: EncoderModeSILK,
			oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthWideband,
			oracleBW: libopustest.OpusBandwidthWideband, sampleRate: 16000, channels: 2,
			forceChannels: 1, frameSize: 160, frames: 6, resetFrame: 3,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
			bitrate: 24000, complexity: 0, bitrateMode: BitrateModeVBR,
		},
		{
			name: "hybrid-24k-10ms-audio-cvbr", mode: EncoderModeHybrid,
			oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthSuperwideband,
			oracleBW: libopustest.OpusBandwidthSuperwideband, sampleRate: 24000, channels: 2,
			forceChannels: 2, frameSize: 240, frames: 6, resetFrame: 3,
			application: ApplicationAudio, oracleApp: libopustest.OpusApplicationAudio,
			bitrate: 48000, complexity: 10, bitrateMode: BitrateModeCVBR,
		},
		{
			name: "hybrid-48k-40ms-audio-cvbr", mode: EncoderModeHybrid,
			oracleMode: libopustest.OpusForceModeHybrid, bandwidth: BandwidthFullband,
			oracleBW: libopustest.OpusBandwidthFullband, sampleRate: 48000, channels: 2,
			forceChannels: 2, frameSize: 1920, frames: 4, resetFrame: 2,
			application: ApplicationAudio, oracleApp: libopustest.OpusApplicationAudio,
			bitrate: 64000, complexity: 10, bitrateMode: BitrateModeCVBR,
		},
		{
			name: "silk-48k-60ms-voip-vbr", mode: EncoderModeSILK,
			oracleMode: libopustest.OpusForceModeSILKOnly, bandwidth: BandwidthWideband,
			oracleBW: libopustest.OpusBandwidthWideband, sampleRate: 48000, channels: 1,
			forceChannels: 1, frameSize: 2880, frames: 4, resetFrame: 2,
			application: ApplicationVoIP, oracleApp: libopustest.OpusApplicationVoIP,
			bitrate: 32000, complexity: 10, bitrateMode: BitrateModeVBR,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runPublicFixedOuterCase(t, tc) })
	}
}

func TestPublicFixedSILKHybridModeTransitionsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		frameSize  = 960
		channels   = 2
		bitrate    = 64000
		complexity = 10
		maxBytes   = 4000
	)
	type control struct {
		mode       EncoderMode
		oracleMode int
		bandwidth  Bandwidth
		oracleBW   int
	}
	controls := []control{
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeHybrid, libopustest.OpusForceModeHybrid, BandwidthSuperwideband, libopustest.OpusBandwidthSuperwideband},
		{EncoderModeHybrid, libopustest.OpusForceModeHybrid, BandwidthSuperwideband, libopustest.OpusBandwidthSuperwideband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
		{EncoderModeSILK, libopustest.OpusForceModeSILKOnly, BandwidthWideband, libopustest.OpusBandwidthWideband},
		{EncoderModeSILK, libopustest.OpusForceModeSILKOnly, BandwidthWideband, libopustest.OpusBandwidthWideband},
		{EncoderModeCELT, libopustest.OpusForceModeCELTOnly, BandwidthFullband, libopustest.OpusBandwidthFullband},
	}

	inputs := make([]libopustest.OpusEncodeFixedMixedFrame, len(controls))
	for frame := range controls {
		pcm := make([]float32, frameSize*channels)
		for i, sample := range genPCMInt16Pub(uint32(0x7911+frame*131), channels, frameSize, 1, false) {
			pcm[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
		}
		inputs[frame] = libopustest.OpusEncodeFixedMixedFrame{
			Format: 1, FloatPCM: pcm,
			ForceMode: controls[frame].oracleMode,
			Bandwidth: controls[frame].oracleBW,
		}
	}
	want, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
		MaxPacketBytes: maxBytes, Bitrate: bitrate, Complexity: complexity,
		VBR: false, VBRConstraint: false, ForceChannels: channels,
		FrameSize: frameSize, LSBDepth: 24,
	}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(controls) {
		t.Fatalf("selected fixed C records=%d want %d", len(want), len(controls))
	}

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return enc.SetBitrate(bitrate) },
		func() error { return enc.SetComplexity(complexity) },
		func() error { return enc.SetBitrateMode(BitrateModeCBR) },
		func() error { return enc.SetForceChannels(channels) },
		func() error { return enc.SetLSBDepth(24) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	packet := make([]byte, maxBytes)
	matched := 0
	for frame, ctl := range controls {
		if err := enc.SetBandwidth(ctl.bandwidth); err != nil {
			t.Fatalf("frame %d SetBandwidth: %v", frame, err)
		}
		if err := enc.SetMaxBandwidth(ctl.bandwidth); err != nil {
			t.Fatalf("frame %d SetMaxBandwidth: %v", frame, err)
		}
		if err := enc.SetMode(ctl.mode); err != nil {
			t.Fatalf("frame %d SetMode: %v", frame, err)
		}
		n, err := enc.Encode(inputs[frame].FloatPCM, packet)
		if err != nil {
			t.Fatalf("frame %d mode=%v: Encode: %v", frame, ctl.mode, err)
		}
		if want[frame].Status != 0 || n != len(want[frame].Packet) ||
			enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(packet[:n], want[frame].Packet) {
			first := 0
			for first < n && first < len(want[frame].Packet) && packet[first] == want[frame].Packet[first] {
				first++
			}
			t.Fatalf("frame %d mode=%v/%d bandwidth=%v/%d: packet/range mismatch status=%d len Go/C=%d/%d firstdiff=%d range Go/C=%08x/%08x",
				frame, ctl.mode, ctl.oracleMode, ctl.bandwidth, ctl.oracleBW,
				want[frame].Status, n, len(want[frame].Packet), first,
				enc.FinalRange(), want[frame].FinalRange)
		}
		matched++
	}
	t.Logf("persistent forced-mode transitions matched %d/%d packets and ranges", matched, len(controls))

	var allocErr error
	allocs := testing.AllocsPerRun(20, func() {
		for frame, ctl := range controls {
			if err := enc.SetBandwidth(ctl.bandwidth); err != nil {
				allocErr = err
				return
			}
			if err := enc.SetMaxBandwidth(ctl.bandwidth); err != nil {
				allocErr = err
				return
			}
			if err := enc.SetMode(ctl.mode); err != nil {
				allocErr = err
				return
			}
			if _, err := enc.Encode(inputs[frame].FloatPCM, packet); err != nil {
				allocErr = err
				return
			}
		}
	})
	if allocErr != nil {
		t.Fatalf("warm transition cycle: %v", allocErr)
	}
	if allocs != 0 {
		t.Fatalf("warm persistent forced-mode transition cycle allocated %.2f times, want 0", allocs)
	}
}

func runPublicFixedOuterCase(t *testing.T, tc publicFixedOuterCase) {
	t.Helper()
	libopustest.RequireOracle(t)
	stride := tc.frameSize * tc.channels
	short := genPCMInt16Pub(uint32(0x6a19+tc.channels*29+len(tc.name)*41+tc.sampleRate), tc.channels, tc.frameSize, tc.frames, false)
	mixed := make([]libopustest.OpusEncodeFixedMixedFrame, tc.frames)
	for frame := range tc.frames {
		pcm16 := short[frame*stride : (frame+1)*stride]
		mixed[frame].Format = uint32(frame % 3)
		mixed[frame].ResetBefore = frame == tc.resetFrame
		switch mixed[frame].Format {
		case 0:
			mixed[frame].ShortPCM = pcm16
		case 1:
			mixed[frame].FloatPCM = make([]float32, stride)
			for i, sample := range pcm16 {
				mixed[frame].FloatPCM[i] = float32(sample)*(1.0/32768.0) + float32(i%17-8)*(1.0/8388608.0)
			}
		case 2:
			mixed[frame].PCM24 = make([]int32, stride)
			for i, sample := range pcm16 {
				mixed[frame].PCM24[i] = int32(sample)*256 + int32(i%17-8)
			}
		}
	}

	vbr := tc.bitrateMode != BitrateModeCBR
	vbrConstraint := tc.bitrateMode == BitrateModeCVBR
	const maxPacketBytes = 4000
	ref, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.oracleApp,
		MaxPacketBytes: maxPacketBytes, ForceMode: tc.oracleMode, Bandwidth: tc.oracleBW,
		Bitrate: tc.bitrate, Complexity: tc.complexity, VBR: vbr, VBRConstraint: vbrConstraint,
		ForceChannels: tc.forceChannels, FrameSize: tc.frameSize, LSBDepth: 24,
	}, mixed)
	if err != nil {
		t.Fatal(err)
	}
	if len(ref) != tc.frames {
		t.Fatalf("selected fixed C records=%d want %d", len(ref), tc.frames)
	}

	enc := newConfiguredPublicFixedOuterEncoder(t, tc)
	packet := make([]byte, maxPacketBytes)
	divergent := 0
	firstDivergence := -1
	for frame, input := range mixed {
		if input.ResetBefore {
			enc.Reset()
		}
		n, callErr := encodePublicFixedOuterInput(enc, input, packet)
		if callErr != nil {
			t.Fatalf("frame %d format=%d Encode: %v", frame, input.Format, callErr)
		}
		if ref[frame].Status != 0 {
			t.Fatalf("frame %d C status=%d", frame, ref[frame].Status)
		}
		if n <= 0 {
			t.Fatalf("frame %d format=%d Go packet length=%d", frame, input.Format, n)
		}
		if n != len(ref[frame].Packet) || enc.FinalRange() != ref[frame].FinalRange || !bytes.Equal(packet[:n], ref[frame].Packet) {
			divergent++
			if firstDivergence < 0 {
				firstDivergence = frame
				first := 0
				for first < n && first < len(ref[frame].Packet) && packet[first] == ref[frame].Packet[first] {
					first++
				}
				gotTOC, wantTOC := byte(0), byte(0)
				if n > 0 {
					gotTOC = packet[0]
				}
				if len(ref[frame].Packet) > 0 {
					wantTOC = ref[frame].Packet[0]
				}
				t.Logf("first mismatch frame=%d format=%d reset=%t packet len Go/C=%d/%d first-diff=%d TOC Go/C=%02x/%02x range Go/C=%08x/%08x",
					frame, input.Format, input.ResetBefore, n, len(ref[frame].Packet), first,
					gotTOC, wantTOC, enc.FinalRange(), ref[frame].FinalRange)
			}
		}
	}
	t.Logf("%s frames=%d matched=%d divergent=%d reset-before=%d", tc.name, tc.frames, tc.frames-divergent, divergent, tc.resetFrame)
	if divergent != 0 {
		t.Errorf("%d/%d public packets or final ranges differ from selected fixed libopus", divergent, tc.frames)
	}
	assertPublicFixedOuterZeroAllocs(t, tc, mixed)
}

func newConfiguredPublicFixedOuterEncoder(t *testing.T, tc publicFixedOuterCase) *Encoder {
	t.Helper()
	enc, err := NewEncoder(EncoderConfig{SampleRate: tc.sampleRate, Channels: tc.channels, Application: tc.application})
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return enc.SetMode(tc.mode) },
		func() error { return enc.SetBandwidth(tc.bandwidth) },
		func() error { return enc.SetMaxBandwidth(tc.bandwidth) },
		func() error { return enc.SetFrameSize(tc.frameSize) },
		func() error { return enc.SetBitrate(tc.bitrate) },
		func() error { return enc.SetComplexity(tc.complexity) },
		func() error { return enc.SetBitrateMode(tc.bitrateMode) },
		func() error { return enc.SetForceChannels(tc.forceChannels) },
		func() error { return enc.SetLSBDepth(24) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	return enc
}

func encodePublicFixedOuterInput(enc *Encoder, input libopustest.OpusEncodeFixedMixedFrame, packet []byte) (int, error) {
	switch input.Format {
	case 0:
		return enc.EncodeInt16(input.ShortPCM, packet)
	case 1:
		return enc.Encode(input.FloatPCM, packet)
	case 2:
		return enc.EncodeInt24(input.PCM24, packet)
	default:
		return 0, fmt.Errorf("unknown input format %d", input.Format)
	}
}

func assertPublicFixedOuterZeroAllocs(t *testing.T, tc publicFixedOuterCase, mixed []libopustest.OpusEncodeFixedMixedFrame) {
	t.Helper()
	for format := uint32(0); format < 3; format++ {
		input := libopustest.OpusEncodeFixedMixedFrame{}
		for _, candidate := range mixed {
			if candidate.Format == format {
				input = candidate
				break
			}
		}
		if input.Format != format {
			t.Fatalf("case %s lacks format %d input for allocation check", tc.name, format)
		}
		enc := newConfiguredPublicFixedOuterEncoder(t, tc)
		packet := make([]byte, 4000)
		if _, err := encodePublicFixedOuterInput(enc, input, packet); err != nil {
			t.Fatalf("case %s format %d warm encode: %v", tc.name, format, err)
		}
		var callErr error
		allocs := testing.AllocsPerRun(20, func() {
			_, callErr = encodePublicFixedOuterInput(enc, input, packet)
		})
		if callErr != nil {
			t.Fatalf("case %s format %d measured encode: %v", tc.name, format, callErr)
		}
		if allocs != 0 {
			t.Errorf("case %s format %d steady-state allocations=%g want 0", tc.name, format, allocs)
		}
	}
}

func xorshift32Pub(state *uint32) uint32 {
	s := *state
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	*state = s
	return s
}

// genPCMInt16Pub builds nframes of interleaved int16 PCM with periodic transient
// spikes, matching the fixedpoint oracle generator's character.
func genPCMInt16Pub(seed uint32, channels, frameSize, nframes int, transient bool) []int16 {
	state := seed
	pcm := make([]int16, channels*frameSize*nframes)
	perFrame := channels * frameSize
	for f := 0; f < nframes; f++ {
		for i := 0; i < perFrame; i++ {
			v := int32(xorshift32Pub(&state))
			s := int16(v >> 19)
			if transient && f%2 == 1 && i > perFrame/2 && i < perFrame/2+40 {
				s = int16(v >> 12)
			}
			pcm[f*perFrame+i] = s
		}
	}
	return pcm
}

// TestPublicEncodeFixedCELTLibopusParity validates the gopus_fixed_point public
// encode seam: a CELT-mode (ApplicationRestrictedCelt, full-band 48 kHz) frame
// routed through Encoder.Encode produces a byte-exact CELT packet versus the
// FIXED_POINT libopus celt_encode_with_ec reference.
//
// The selected C reference receives the exact opus_res Q8 input and per-frame
// analysis consumed by the integer CELT encoder. The test keeps C state across
// the same five-frame sequence and compares every complete payload and range.
func TestPublicEncodeFixedCELTLibopusParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		start   = 0
		end     = 21
		nframes = 5
	)

	type kase struct {
		channels   int
		lm         int
		bitrate    int
		complexity int
		mode       BitrateMode
		transient  bool
	}
	var cases []kase
	for _, ch := range []int{1, 2} {
		for _, lm := range []int{0, 2, 3} { // 120, 480, 960 samples
			for _, br := range []int{32000, 64000, 128000} {
				for _, cx := range []int{0, 5, 10} {
					for _, m := range []BitrateMode{BitrateModeVBR, BitrateModeCBR, BitrateModeCVBR} {
						cases = append(cases, kase{channels: ch, lm: lm, bitrate: br,
							complexity: cx, mode: m, transient: lm == 3})
					}
				}
			}
		}
	}

	for _, c := range cases {
		frameSize := 120 << c.lm
		seed := uint32(0x51EED + c.lm*131 + c.bitrate + c.complexity*7 + c.channels*1009 + int(c.mode)*97)
		if c.transient {
			seed ^= 0x33333333
		}
		pcm16 := genPCMInt16Pub(seed, c.channels, frameSize, nframes, c.transient)

		enc, err := NewEncoder(EncoderConfig{
			SampleRate:  48000,
			Channels:    c.channels,
			Application: ApplicationRestrictedCelt,
		})
		if err != nil {
			t.Fatalf("NewEncoder: %v", err)
		}
		if err := enc.SetComplexity(c.complexity); err != nil {
			t.Fatalf("SetComplexity: %v", err)
		}
		if err := enc.SetBitrate(c.bitrate); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetBitrateMode(c.mode); err != nil {
			t.Fatalf("SetBitrateMode: %v", err)
		}
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize: %v", err)
		}
		// The standalone C CELT reference fixes its coded channels and end
		// band; use the same public controls before capturing the inner input.
		if err := enc.SetForceChannels(c.channels); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth: %v", err)
		}

		// Drive every frame through the public encoder, capturing the Q8 input,
		// analysis, and CELT payload before the next encode reuses scratch.
		perFrame := c.channels * frameSize
		fed := make([]libopustest.CELTFixedQ8Frame, nframes)
		payloads := make([][]byte, nframes)
		ranges := make([]uint32, nframes)
		f32 := make([]float32, perFrame)
		out := make([]byte, 4000)
		innerBitrate, lsbDepth := 0, 0
		for f := 0; f < nframes; f++ {
			for i := 0; i < perFrame; i++ {
				f32[i] = float32(pcm16[f*perFrame+i]) / 32768.0
			}
			n, err := enc.Encode(f32, out)
			if err != nil {
				t.Fatalf("Encode frame %d: %v", f, err)
			}
			input := enc.enc.LastFixedCELTInputQ8()
			if len(input) != perFrame {
				t.Fatalf("ch=%d lm=%d frame=%d: integer CELT Q8 input len=%d want %d",
					c.channels, c.lm, f, len(input), perFrame)
			}
			rate, maxBytes, depth := enc.enc.LastFixedCELTControls()
			if f == 0 {
				innerBitrate, lsbDepth = rate, depth
			} else if rate != innerBitrate || depth != lsbDepth {
				t.Fatalf("frame %d: inner controls changed bitrate=%d/%d depth=%d/%d", f, rate, innerBitrate, depth, lsbDepth)
			}
			analysis := enc.enc.LastFixedCELTAnalysis()
			fed[f] = libopustest.CELTFixedQ8Frame{
				PCM: append([]int32(nil), input...), MaxBytes: maxBytes,
				Analysis: libopustest.CELTFixedQ8Analysis{
					Valid: analysis.Valid, Tonality: analysis.Tonality,
					TonalitySlope: analysis.TonalitySlope, Noisiness: analysis.NoisySpeech,
					Activity: analysis.Activity, MusicProb: analysis.MusicProb,
					MusicProbMin: analysis.MusicProbMin, MusicProbMax: analysis.MusicProbMax,
					Bandwidth: analysis.BandwidthIndex, ActivityProbability: analysis.VADProb,
					MaxPitchRatio: analysis.MaxPitchRatio, LeakBoost: analysis.LeakBoost,
				},
			}
			if n < 1 {
				t.Fatalf("Encode frame %d: short packet %d", f, n)
			}
			payloads[f] = append([]byte(nil), out[1:n]...)
			ranges[f] = enc.FinalRange()
		}

		vbr := c.mode != BitrateModeCBR
		constrained := c.mode == BitrateModeCVBR

		probe := libopustest.ProbeCELTFixedRawQ8
		if extsupport.QEXT {
			probe = libopustest.ProbeCELTFixedQEXTQ8
		}
		want, err := probe(libopustest.CELTFixedQ8Params{
			SampleRate: 48000, Channels: c.channels, FrameSize: frameSize,
			Start: start, End: end, Bitrate: innerBitrate, Complexity: c.complexity,
			LSBDepth: lsbDepth, VBR: vbr, ConstrainedVBR: constrained, Frames: fed,
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "celt fixed encode raw Q8", err)
			return
		}
		if len(want) != nframes {
			t.Fatalf("raw Q8 oracle records=%d want %d", len(want), nframes)
		}

		for f := 0; f < nframes; f++ {
			label := fmt.Sprintf("ch=%d lm=%d br=%d cx=%d mode=%v frame=%d",
				c.channels, c.lm, c.bitrate, c.complexity, c.mode, f)
			got := payloads[f]
			if !bytes.Equal(got, want[f].Packet) || ranges[f] != want[f].FinalRange {
				mismatch := 0
				for mismatch < min(len(got), len(want[f].Packet)) && got[mismatch] == want[f].Packet[mismatch] {
					mismatch++
				}
				t.Errorf("%s: CELT payload/range mismatch at byte %d (gopus len=%d libopus len=%d range=%08x/%08x)",
					label, mismatch, len(got), len(want[f].Packet), ranges[f], want[f].FinalRange)
				break
			}
		}
	}
}

const robustFixedPointDecode = true

// celtIntegerPLCActive is true under -tags gopus_fixed_point, where a CELT-only
// packet-loss frame is concealed by the integer FIXED_POINT celt_decode_lost and
// emitted as the libopus-exact opus_res int16/int24 output. That concealment is a
// different (fixed) computation than the float32 PLC, so float-equality and
// float-oracle PLC assertions for CELT no longer hold there; their correctness is
// covered by TestDecoderFixedPointCELTPLCParity (FIXED_POINT opus_decode oracle).
const celtIntegerPLCActive = true

// hybridIntegerPLCActive is true under -tags gopus_fixed_point, where a lost Hybrid
// frame advances the integer FIXED_POINT CELT highband state through the loss and
// accumulates the concealed highband onto the integer SILK lowband (see
// armFixedHybridLost / finishFixedHybridLost), emitting the libopus FIXED_POINT
// opus_res int16/int24 output. Like the CELT-only integer PLC that is a different
// (fixed) computation than the float32 PLC, so the float-equality and float-oracle
// PLC assertions for Hybrid no longer hold there; their correctness is covered by
// the FIXED_POINT differential gate (TestDecodeDifferentialFixedPointPLC).
const hybridIntegerPLCActive = true

// decodeInt24TracksFloat32Skip is true under -tags gopus_fixed_point, where
// DecodeInt24 routes CELT-only frames to the integer decoder's libopus-exact
// opus_res int24 instead of the float32 decode.
const decodeInt24TracksFloat32Skip = true

const decodeInt16HotPathAllocBudget = 0

// SILK mono/stereo loss and recovery reuse decoder-owned working buffers.
const (
	silkPLCMonoHotPathAllocBudget   = 0
	silkPLCStereoHotPathAllocBudget = 0
)

// Multistream decode under -tags gopus_fixed_point uses fixed-point elementary
// decoders. This ceiling includes wrapper allocations for the default stereo
// configuration. The multistream encoder is zero-alloc in every build.
const multistreamDecodeHotPathAllocBudget = 8

func TestFixedMultistreamDecodeFloat32MatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, frameSize = 48000, 960
	for _, mode := range []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	} {
		for _, channels := range []int{1, 2} {
			t.Run(mode.name+"_ch_"+itoaSmall(channels), func(t *testing.T) {
				packet := mode.packet(t, channels)
				frameSizeAtRate, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packet duration: %v", err)
				}
				if frameSizeAtRate != frameSize {
					t.Fatalf("packet duration=%d want %d", frameSizeAtRate, frameSize)
				}
				coupled := 0
				mapping := []byte{0}
				if channels == 2 {
					coupled = 1
					mapping = []byte{0, 1}
				}
				want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, 1, coupled, frameSize, mapping, [][]byte{packet})
				if err != nil {
					libopustest.HelperUnavailable(t, "selected fixed-point multistream float decode", err)
				}

				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				pcm := make([]float32, frameSize*channels)
				if n, err := dec.Decode(packet, pcm); err != nil || n != frameSize {
					t.Fatalf("Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
				}
				assertMultistreamCallerFloatBits(t, pcm, want)

				// An undersized attempt must leave decoder state ready for retry.
				retry := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				short := make([]float32, frameSize*channels-channels)
				if _, err := retry.Decode(packet, short); err != ErrBufferTooSmall {
					t.Fatalf("Decode undersized buffer error=%v, want %v", err, ErrBufferTooSmall)
				}
				retryPCM := make([]float32, frameSize*channels)
				if n, err := retry.Decode(packet, retryPCM); err != nil || n != frameSize {
					t.Fatalf("Decode retry=(%d,%v), want (%d,nil)", n, err, frameSize)
				}
				assertMultistreamCallerFloatBits(t, retryPCM, want)

				for range 2 {
					if n, err := dec.Decode(packet, pcm); err != nil || n != frameSize {
						t.Fatalf("warmup Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
					}
				}
				if allocs := testing.AllocsPerRun(100, func() {
					if n, err := dec.Decode(packet, pcm); err != nil || n != frameSize {
						t.Fatalf("warm Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
					}
				}); allocs != 0 {
					t.Fatalf("warm fixed-point float multistream decode allocations=%g want 0", allocs)
				}
			})
		}
	}
}

func TestFixedMultistreamDecodeFloat32PLCRecoveryMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, frameSize = 48000, 960
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)
			coupled := channels - 1
			mapping := []byte{0}
			if channels == 2 {
				mapping = []byte{0, 1}
			}
			sequence := [][]byte{packet, nil, packet, nil, packet}
			want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, 1, coupled, frameSize, mapping, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed-point multistream Float32 PLC/recovery", err)
			}

			dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
			pcm := make([]float32, frameSize*channels)
			for i, input := range sequence {
				n, err := dec.Decode(input, pcm)
				if err != nil || n != frameSize {
					t.Fatalf("Decode sequence frame %d=(%d,%v), want (%d,nil)", i, n, err, frameSize)
				}
				start := i * len(pcm)
				assertMultistreamCallerFloatBits(t, pcm, want[start:start+len(pcm)])
			}

			warm := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
			warmPCM := make([]float32, frameSize*channels)
			decodeCycle := func() {
				for _, input := range sequence[:3] {
					if n, err := warm.Decode(input, warmPCM); err != nil || n != frameSize {
						t.Fatalf("warm Decode=(%d,%v), want (%d,nil)", n, err, frameSize)
					}
				}
			}
			decodeCycle()
			decodeCycle()
			if allocs := testing.AllocsPerRun(100, decodeCycle); allocs != 0 {
				t.Fatalf("warm fixed-point float multistream PLC/recovery allocations=%g want 0", allocs)
			}
		})
	}
}

func TestMultistreamFixedHybridPLCRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960} {
			t.Run(fmt.Sprintf("ch%d/fs%d", channels, frameSize), func(t *testing.T) {
				streams, coupled := 1, 0
				mapping := []byte{0}
				if channels == 2 {
					coupled = 1
					mapping = []byte{0, 1}
				}
				seed := encodeAPIRateHybridPacketFrameSize(t, channels, frameSize)
				recovery := encodeAPIRateHybridPacketFrameSizeVariant(t, channels, frameSize, frameSize+19)
				sequence := [][]byte{seed, nil, nil, recovery}
				want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize, mapping, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed Hybrid PLC/recovery int24", err)
					return
				}
				dec, err := mspkg.NewDecoder(sampleRate, channels, streams, coupled, mapping)
				if err != nil {
					t.Fatal(err)
				}
				got24 := make([]int32, 0, len(want24))
				for i, packet := range sequence {
					out, err := dec.DecodeToInt24(packet, frameSize)
					if err != nil || len(out) != frameSize*channels {
						t.Fatalf("packet %d DecodeToInt24 samples=%d err=%v, want %d", i, len(out), err, frameSize*channels)
					}
					got24 = append(got24, out...)
				}
				if len(got24) != len(want24) {
					t.Fatalf("Hybrid PLC and recovery int24 sample count=%d, want %d", len(got24), len(want24))
				}
				for i := range got24 {
					if got24[i] != want24[i] {
						t.Fatalf("Hybrid PLC and recovery int24 differs at sample %d: Go=%d C=%d", i, got24[i], want24[i])
					}
				}
			})
		}
	}
}

var multistreamFixedRefdecodeHelper libopustest.HelperCache

// runLibopusMultistreamFixedDecode drives the libopus multistream public API
// (opus_multistream_decode / opus_multistream_decode24) built against the
// fixed-point reference selected by the current Go build, including ENABLE_QEXT.
func runLibopusMultistreamFixedDecode(sampleRate, channels, streams, coupled, frameSize, sampleFormat int, mapping []byte, packets [][]byte) (*libopustest.OracleReader, error) {
	return runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, sampleFormat, 0, mapping, packets)
}

func runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, sampleFormat, gainQ8 int, mapping []byte, packets [][]byte) (*libopustest.OracleReader, error) {
	binPath, err := multistreamFixedDecodeHelperPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayloadVersion(
		"GMSI", 4,
		uint32(sampleRate),
		uint32(gainQ8),
		uint32(sampleFormat),
		1,
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		0,
	)
	payload.Raw(mapping)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "multistream fixed reference decode", "GMSO")
}

func multistreamFixedDecodeHelperPath() (string, error) {
	return multistreamFixedRefdecodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "multistream fixed reference decode",
			OutputBase: "gopus_libopus_refdecode_multistream_fixed",
			SourceFile: "libopus_refdecode_multistream.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
			DeadStrip:  true,
		})
	})
}

func decodeLibopusMultistreamFixedInt24WithPhase(sampleRate, channels, streams, coupled, frameSize int, phaseInversionDisabled bool, mapping []byte, packets [][]byte) ([]int32, error) {
	binPath, err := multistreamFixedDecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion(
		"GMSI", 5,
		uint32(sampleRate),
		0,
		libopusRefdecodeMSFormatInt24,
		1,
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		0,
	)
	phaseDisabled := uint32(0)
	if phaseInversionDisabled {
		phaseDisabled = 1
	}
	payload.U32(phaseDisabled)
	payload.Raw(mapping)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "multistream fixed phase-control reference decode", "GMSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	out := make([]int32, nSamples)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]int16, error) {
	return decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, 0, mapping, packets)
}

func decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int16, error) {
	reader, err := runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, libopusRefdecodeMSFormatInt16, gainQ8, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	out := make([]int16, nSamples)
	for i := range out {
		out[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]int32, error) {
	return decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, 0, mapping, packets)
}

func decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int32, error) {
	reader, err := runLibopusMultistreamFixedDecodeWithGain(sampleRate, channels, streams, coupled, frameSize, libopusRefdecodeMSFormatInt24, gainQ8, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	out := make([]int32, nSamples)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

const libopusRefdecodeMSFormatInt16 = 1

// packSelfDelimitedLength encodes an Opus self-delimited frame length (RFC 6716
// Appendix B): one byte when < 252, otherwise two bytes.
func packSelfDelimitedLength(dst []byte, n int) []byte {
	if n < 252 {
		return append(dst, byte(n))
	}
	return append(dst, byte(252+(n-252)&0x3), byte((n-252)>>2))
}

// buildMultistreamPacket concatenates per-stream single-frame (code 0) Opus
// packets into one multistream packet: the first N-1 streams use self-delimited
// framing (TOC, length, frame data) and the last stream uses standard framing
// (TOC, frame data), matching opus_multistream_packet framing.
func buildMultistreamPacket(t *testing.T, streamPackets [][]byte) []byte {
	t.Helper()
	var out []byte
	for i, pkt := range streamPackets {
		if len(pkt) < 1 {
			t.Fatalf("stream %d: empty packet", i)
		}
		if (pkt[0] & 0x03) != 0 {
			t.Fatalf("stream %d: expected code-0 (single frame) packet, toc=%#x", i, pkt[0])
		}
		toc := pkt[0]
		frame := pkt[1:]
		if i < len(streamPackets)-1 {
			out = append(out, toc)
			out = packSelfDelimitedLength(out, len(frame))
			out = append(out, frame...)
		} else {
			out = append(out, toc)
			out = append(out, frame...)
		}
	}
	return out
}

// TestMultistreamDecodeFixedPointParity gates that, under -tags gopus_fixed_point,
// MultistreamDecoder.DecodeInt16 / DecodeInt24 of CELT-only multistream packets
// are bit-exact with the libopus FIXED_POINT opus_multistream_decode /
// opus_multistream_decode24 reference. Each elementary stream is routed through
// the integer opus_res path and the surround channel mapping is applied in the
// integer domain (RES2INT16 / RES2INT24), matching
// opus_multistream_decode_native built FIXED_POINT (no soft clip).
//
// Layouts cover mono streams, coupled (stereo) streams, a 5.1-style
// 4-stream/2-coupled surround mapping, and Hybrid streams (a Hybrid stereo
// coupled stream and a coupled layout mixing Hybrid streams), multi-frame.
// The tested outputs match the selected FIXED_POINT reference build.
func TestMultistreamDecodeFixedPointParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate  = 48000
		frameSize48 = 960
		frames      = 4
	)

	type layout struct {
		name     string
		channels int
		streams  int
		coupled  int
		mapping  []byte
		// streamChans gives the channel count of each stream (2 for coupled).
		streamChans []int
		// streamMode selects the per-stream packet mode (ModeCELT default, or
		// ModeHybrid). nil means all CELT.
		streamMode []Mode
	}
	layouts := []layout{
		{"mono_2streams", 2, 2, 0, []byte{0, 1}, []int{1, 1}, nil},
		{"stereo_coupled", 2, 1, 1, []byte{0, 1}, []int{2}, nil},
		{"quad_2coupled", 4, 2, 2, []byte{0, 1, 2, 3}, []int{2, 2}, nil},
		{"surround51", 6, 4, 2, []byte{0, 4, 1, 2, 3, 5}, []int{2, 2, 1, 1}, nil},
		{"hybrid_stereo_coupled", 2, 1, 1, []byte{0, 1}, []int{2}, []Mode{ModeHybrid}},
		{"hybrid_mono_2streams", 2, 2, 0, []byte{0, 1}, []int{1, 1}, []Mode{ModeHybrid, ModeHybrid}},
		{"hybrid_quad_2coupled", 4, 2, 2, []byte{0, 1, 2, 3}, []int{2, 2}, []Mode{ModeHybrid, ModeHybrid}},
		{"mixed_hybrid_celt_coupled", 4, 2, 2, []byte{0, 1, 2, 3}, []int{2, 2}, []Mode{ModeHybrid, ModeCELT}},
	}

	for _, lo := range layouts {
		t.Run(lo.name, func(t *testing.T) {
			// Build `frames` multistream packets, each composed of one frame per
			// stream (distinct payloads per frame so cross-frame integer CELT/Hybrid
			// state is exercised).
			msPackets := make([][]byte, 0, frames)
			for f := 0; f < frames; f++ {
				streamPackets := make([][]byte, lo.streams)
				for s := 0; s < lo.streams; s++ {
					ch := lo.streamChans[s]
					mode := ModeCELT
					if lo.streamMode != nil {
						mode = lo.streamMode[s]
					}
					var pkt []byte
					if mode == ModeHybrid {
						pkt = encodeAPIRateHybridPacketFrameSizeVariant(t, ch, frameSize48, f*8+s+1)
					} else {
						pkt = encodeAPIRateCELTPacketFrameSizeVariant(t, ch, frameSize48, 128000, f*8+s+1)
					}
					if toc := ParseTOC(pkt[0]); toc.Mode != mode {
						t.Fatalf("stream %d frame %d: encoder produced mode %v, want %v", s, f, toc.Mode, mode)
					}
					streamPackets[s] = pkt
				}
				msPackets = append(msPackets, buildMultistreamPacket(t, streamPackets))
			}

			refInt16, err := decodeLibopusMultistreamFixedInt16(sampleRate, lo.channels, lo.streams, lo.coupled, frameSize48, lo.mapping, msPackets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeLibopusMultistreamFixedInt24(sampleRate, lo.channels, lo.streams, lo.coupled, frameSize48, lo.mapping, msPackets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed reference decode int24", err)
				return
			}

			dec16, err := NewMultistreamDecoder(sampleRate, lo.channels, lo.streams, lo.coupled, lo.mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder int16: %v", err)
			}
			dec24, err := NewMultistreamDecoder(sampleRate, lo.channels, lo.streams, lo.coupled, lo.mapping)
			if err != nil {
				t.Fatalf("NewMultistreamDecoder int24: %v", err)
			}

			var got16, got24 []int32
			for p, pkt := range msPackets {
				o16 := make([]int16, frameSize48*lo.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, frameSize48*lo.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)

			// Reset must rewind the integer CELT state alongside the float
			// decoder state. Replaying the same sequence must reproduce the
			// selected FIXED_POINT stream after reset.
			dec16.Reset()
			dec24.Reset()
			var replay16, replay24 []int32
			for p, pkt := range msPackets {
				o16 := make([]int16, frameSize48*lo.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("reset replay packet %d DecodeInt16: %v", p, err)
				}
				replay16 = append(replay16, int16ToInt32(o16)...)
				o24 := make([]int32, frameSize48*lo.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("reset replay packet %d DecodeInt24: %v", p, err)
				}
				replay24 = append(replay24, o24...)
			}
			assertFixedExact(t, "reset replay int16", replay16, int16ToInt32(refInt16))
			assertFixedExact(t, "reset replay int24", replay24, refInt24)
		})
	}
}

func TestMultistreamDecodeFixedPointGainMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, channels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	packets := make([][]byte, 4)
	for i := range packets {
		packets[i] = encodeAPIRateCELTPacketFrameSizeVariant(t, channels, frameSize, 128000, i+41)
		if toc := ParseTOC(packets[i][0]); toc.Mode != ModeCELT {
			t.Fatalf("packet %d mode = %v, want CELT", i, toc.Mode)
		}
	}

	for _, gainQ8 := range []int{512, -512, 2048, -2048, 8192, -8192, 32767, -32768} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			want16, err := decodeLibopusMultistreamFixedInt16WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed int16 decode with gain", err)
				return
			}
			want24, err := decodeLibopusMultistreamFixedInt24WithGain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream fixed int24 decode with gain", err)
				return
			}

			dec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec16.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			got16 := make([]int32, 0, len(want16))
			out16 := make([]int16, frameSize*channels)
			for i, packet := range packets {
				if n, err := dec16.DecodeInt16(packet, out16); err != nil || n != frameSize {
					t.Fatalf("DecodeInt16 packet %d: samples=%d err=%v", i, n, err)
				}
				got16 = append(got16, int16ToInt32(out16)...)
			}
			assertFixedExact(t, "gained int16", got16, int16ToInt32(want16))
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := dec16.DecodeInt16(packets[0], out16); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt16: samples=%d err=%v", n, err)
				}
			}); allocs != 0 {
				t.Fatalf("warm gained DecodeInt16 allocations=%g want 0", allocs)
			}

			dec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec24.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			got24 := make([]int32, 0, len(want24))
			out24 := make([]int32, frameSize*channels)
			for i, packet := range packets {
				if n, err := dec24.DecodeInt24(packet, out24); err != nil || n != frameSize {
					t.Fatalf("DecodeInt24 packet %d: samples=%d err=%v", i, n, err)
				}
				got24 = append(got24, out24...)
			}
			assertFixedExact(t, "gained int24", got24, want24)
			if allocs := testing.AllocsPerRun(100, func() {
				if n, err := dec24.DecodeInt24(packets[0], out24); err != nil || n != frameSize {
					t.Fatalf("warm gained DecodeInt24: samples=%d err=%v", n, err)
				}
			}); allocs != 0 {
				t.Fatalf("warm gained DecodeInt24 allocations=%g want 0", allocs)
			}
		})
	}
}

func TestMultistreamFixedCELTPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, channels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, frameSize, 128000, 73)
	if toc := ParseTOC(packet[0]); toc.Mode != ModeCELT {
		t.Fatalf("packet mode = %v, want CELT", toc.Mode)
	}
	const consecutiveLosses = 60
	packets := make([][]byte, consecutiveLosses+2)
	packets[0] = packet
	for i := 1; i <= consecutiveLosses; i++ {
		packets[i] = nil
	}
	packets[len(packets)-1] = packet

	want16, err := decodeLibopusMultistreamFixedInt16(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream fixed CELT PLC int16", err)
		return
	}
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, frameSize, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream fixed CELT PLC int24", err)
		return
	}

	dec16, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	dec24, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	got16 := make([]int32, 0, len(want16))
	got24 := make([]int32, 0, len(want24))
	out16 := make([]int16, frameSize*channels)
	out24 := make([]int32, frameSize*channels)
	for i, pkt := range packets {
		if n, err := dec16.DecodeInt16(pkt, out16); err != nil || n != frameSize {
			t.Fatalf("DecodeInt16 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got16 = append(got16, int16ToInt32(out16)...)
		if n, err := dec24.DecodeInt24(pkt, out24); err != nil || n != frameSize {
			t.Fatalf("DecodeInt24 packet %d returned samples=%d, err=%v; want %d", i, n, err, frameSize)
		}
		got24 = append(got24, out24...)
	}
	assertFixedExact(t, "CELT PLC int16", got16, int16ToInt32(want16))
	assertFixedExact(t, "CELT PLC int24", got24, want24)

	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec16.DecodeInt16(nil, out16); err != nil || n != frameSize {
			t.Fatalf("warm CELT PLC DecodeInt16 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm CELT PLC DecodeInt16 allocations=%g want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if n, err := dec24.DecodeInt24(nil, out24); err != nil || n != frameSize {
			t.Fatalf("warm CELT PLC DecodeInt24 returned samples=%d, err=%v; want %d", n, err, frameSize)
		}
	}); allocs != 0 {
		t.Fatalf("warm CELT PLC DecodeInt24 allocations=%g want 0", allocs)
	}
}

func TestMultistreamFixedCELTLongPLCMixedOutputsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, channels, streams, coupled = 48000, 2, 1, 1
	const packetFrameSize, plcFrameSize = 960, 5760
	mapping := []byte{0, 1}
	packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, packetFrameSize, 128000, 107)
	if toc := ParseTOC(packet[0]); toc.Mode != ModeCELT {
		t.Fatalf("packet mode = %v, want CELT", toc.Mode)
	}
	packets := [][]byte{packet, nil, nil, packet}

	// The same C decoder handles a 20 ms packet, two requested 120 ms PLC
	// intervals, then recovery. Int24 exposes its opus_res samples, which also
	// provide exact expected values for the mixed int16 and fixed-build float
	// API calls below.
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, channels, streams, coupled, plcFrameSize, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream fixed long CELT PLC int24", err)
		return
	}
	packetSamples := packetFrameSize * channels
	plcSamples := plcFrameSize * channels
	wantTotal := packetSamples*2 + plcSamples*2
	if len(want24) != wantTotal {
		t.Fatalf("selected C decoded %d samples, want %d", len(want24), wantTotal)
	}

	dec, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	packet16 := make([]int16, packetSamples)
	if n, err := dec.DecodeInt16(packet, packet16); err != nil || n != packetFrameSize {
		t.Fatalf("initial DecodeInt16 returned samples=%d, err=%v; want %d", n, err, packetFrameSize)
	}
	assertFixedExact(t, "mixed initial int16", int16ToInt32(packet16), convertResToInt16(want24[:packetSamples]))

	floatPLC := make([]float32, plcSamples)
	if n, err := dec.Decode(nil, floatPLC); err != nil || n != plcFrameSize {
		t.Fatalf("120 ms float PLC returned samples=%d, err=%v; want %d", n, err, plcFrameSize)
	}
	assertResAsFloat32(t, "120 ms float PLC", floatPLC, want24[packetSamples:packetSamples+plcSamples])

	int24PLC := make([]int32, plcSamples)
	if n, err := dec.DecodeInt24(nil, int24PLC); err != nil || n != plcFrameSize {
		t.Fatalf("120 ms int24 PLC returned samples=%d, err=%v; want %d", n, err, plcFrameSize)
	}
	int24Start := packetSamples + plcSamples
	assertFixedExact(t, "120 ms int24 PLC", int24PLC, want24[int24Start:int24Start+plcSamples])

	recovery := make([]int16, packetSamples)
	if n, err := dec.DecodeInt16(packet, recovery); err != nil || n != packetFrameSize {
		t.Fatalf("recovery DecodeInt16 returned samples=%d, err=%v; want %d", n, err, packetFrameSize)
	}
	assertFixedExact(t, "mixed recovery int16", int16ToInt32(recovery), convertResToInt16(want24[wantTotal-packetSamples:]))
}

func TestMultistreamFixedCELTPLCInvalidDurationDoesNotAdvance(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, frameSize = 48000, 2, 1, 1, 120
	mapping := []byte{0, 1}
	packet := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 1, frameSize, 96000, 239),
	})
	want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, outputChannels, streams, coupled, frameSize, mapping, [][]byte{packet, nil})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed CELT invalid duration reference", err)
		return
	}
	dec, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	seed := make([]int32, frameSize*outputChannels)
	if n, err := dec.DecodeInt24(packet, seed); err != nil || n != frameSize {
		t.Fatalf("seed DecodeInt24 returned samples=%d err=%v, want %d", n, err, frameSize)
	}
	if _, handled, err := dec.dec.DecodePLCToResFixed(frameSize + 1); err != nil || handled {
		t.Fatalf("invalid direct PLC preflight handled=%v err=%v, want unhandled without error", handled, err)
	}
	invalid := make([]int32, (frameSize+1)*outputChannels)
	if n, err := dec.DecodeInt24(nil, invalid); err == nil || n != 0 {
		t.Fatalf("invalid public PLC returned samples=%d err=%v, want frame-size error", n, err)
	}
	valid := make([]int32, frameSize*outputChannels)
	if n, err := dec.DecodeInt24(nil, valid); err != nil || n != frameSize {
		t.Fatalf("valid PLC after rejected request returned samples=%d err=%v, want %d", n, err, frameSize)
	}
	assertFixedExact(t, "PLC after invalid duration", valid, want24[frameSize*outputChannels:])
}

func TestMultistreamFixedCELTPLCValidDurationsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, seedFrameSize = 48000, 2, 1, 1, 120
	mapping := []byte{0, 1}
	packet := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 1, seedFrameSize, 96000, 211),
	})
	if toc := ParseTOC(packet[0]); toc.Mode != ModeCELT || toc.Stereo {
		t.Fatalf("seed packet mode/stereo=%v/%v, want mono-coded CELT", toc.Mode, toc.Stereo)
	}

	for lossFrameSize := sampleRate / 400; lossFrameSize <= sampleRate*3/25; lossFrameSize += sampleRate / 400 {
		t.Run(fmt.Sprintf("loss_%d_samples", lossFrameSize), func(t *testing.T) {
			packets := [][]byte{packet, nil}
			want24, err := decodeLibopusMultistreamFixedInt24(sampleRate, outputChannels, streams, coupled, lossFrameSize, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed CELT PLC duration matrix int24", err)
				return
			}
			seedSamples := seedFrameSize * outputChannels
			if len(want24) != seedSamples+lossFrameSize*outputChannels {
				t.Fatalf("selected C emitted %d samples, want seed+loss=%d", len(want24), seedSamples+lossFrameSize*outputChannels)
			}

			dec24, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			out24 := make([]int32, lossFrameSize*outputChannels)
			if n, err := dec24.DecodeInt24(packet, out24); err != nil || n != seedFrameSize {
				t.Fatalf("seed DecodeInt24 returned samples=%d err=%v, want %d", n, err, seedFrameSize)
			}
			if n, err := dec24.DecodeInt24(nil, out24); err != nil || n != lossFrameSize {
				t.Fatalf("PLC DecodeInt24 returned samples=%d err=%v, want %d", n, err, lossFrameSize)
			}
			assertFixedExact(t, "valid-duration int24 PLC", out24, want24[seedSamples:])

			dec16, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			out16 := make([]int16, lossFrameSize*outputChannels)
			if n, err := dec16.DecodeInt16(packet, out16); err != nil || n != seedFrameSize {
				t.Fatalf("seed DecodeInt16 returned samples=%d err=%v, want %d", n, err, seedFrameSize)
			}
			if n, err := dec16.DecodeInt16(nil, out16); err != nil || n != lossFrameSize {
				t.Fatalf("PLC DecodeInt16 returned samples=%d err=%v, want %d", n, err, lossFrameSize)
			}
			assertFixedExact(t, "valid-duration int16 PLC", int16ToInt32(out16), convertResToInt16(want24[seedSamples:]))
		})
	}
}

func TestMultistreamFixedCELTModeResetAndCodedChannelsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	celtStart := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 2, frameSize, 128000, 223),
	})
	silk := buildMultistreamPacket(t, [][]byte{encodeAPIRateSILKPacketFrameSize(t, 2, frameSize)})
	celtRecovery := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 2, frameSize, 128000, 227),
	})
	packets := [][]byte{celtStart, silk, celtRecovery, celtRecovery}
	for i, packet := range packets {
		if len(packet) == 0 {
			t.Fatalf("packet %d is empty", i)
		}
	}
	if got := ParseTOC(celtStart[0]).Mode; got != ModeCELT {
		t.Fatalf("first packet mode=%v, want CELT", got)
	}
	if got := ParseTOC(silk[0]).Mode; got != ModeSILK {
		t.Fatalf("second packet mode=%v, want SILK", got)
	}
	if got := ParseTOC(celtRecovery[0]).Mode; got != ModeCELT {
		t.Fatalf("recovery packet mode=%v, want CELT", got)
	}

	for _, gainQ8 := range []int{0, 768, -768} {
		t.Run(fmt.Sprintf("gain_%d", gainQ8), func(t *testing.T) {
			want24, err := decodeLibopusMultistreamFixedInt24WithGain(sampleRate, outputChannels, streams, coupled, frameSize, gainQ8, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed CELT mode transition int24", err)
				return
			}
			dec, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}
			got24 := make([]int32, 0, len(want24))
			frame := make([]int32, frameSize*outputChannels)
			for i, packet := range packets {
				if n, err := dec.DecodeInt24(packet, frame); err != nil || n != frameSize {
					t.Fatalf("packet %d DecodeInt24 returned samples=%d err=%v, want %d", i, n, err, frameSize)
				}
				got24 = append(got24, frame...)
			}
			assertFixedExact(t, "CELT/SILK/CELT mode reset", got24, want24)
		})
	}
}

func TestMultistreamFixedCELTAutoMonoCodedChannelsWithPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate, outputChannels, streams, coupled, frameSize = 48000, 2, 1, 1, 960
	mapping := []byte{0, 1}
	stereoPacket := buildMultistreamPacket(t, [][]byte{
		encodeAPIRateCELTPacketFrameSizeVariant(t, 2, frameSize, 128000, 229),
	})
	autoMonoPacket := buildMultistreamPacket(t, [][]byte{encodeAutoMonoCELTPacket(t, frameSize, 233)})
	if toc := ParseTOC(autoMonoPacket[0]); toc.Mode != ModeCELT || toc.Stereo {
		t.Fatalf("low-rate stereo-input packet TOC mode/stereo=%v/%v, want mono-coded CELT", toc.Mode, toc.Stereo)
	}
	packets := [][]byte{stereoPacket, autoMonoPacket, nil, autoMonoPacket}
	want24, err := decodeLibopusMultistreamFixedInt24WithPhase(sampleRate, outputChannels, streams, coupled, frameSize, true, mapping, packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed auto-mono CELT and PLC reference", err)
		return
	}
	dec, err := NewMultistreamDecoder(sampleRate, outputChannels, streams, coupled, mapping)
	if err != nil {
		t.Fatal(err)
	}
	dec.SetPhaseInversionDisabled(true)
	got24 := make([]int32, 0, len(want24))
	frame := make([]int32, frameSize*outputChannels)
	for i, packet := range packets {
		if n, err := dec.DecodeInt24(packet, frame); err != nil || n != frameSize {
			t.Fatalf("packet %d DecodeInt24 returned samples=%d err=%v, want %d", i, n, err, frameSize)
		}
		got24 = append(got24, frame...)
	}
	assertFixedExact(t, "stereo input auto-mono CELT with PLC/recovery", got24, want24)
}

func encodeAutoMonoCELTPacket(t *testing.T, frameSize, variant int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: 2, Application: ApplicationRestrictedCelt})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(6000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	pcm := make([]float32, frameSize*2)
	for i := 0; i < frameSize; i++ {
		tm := float64(variant*frameSize+i) / sampleRate
		pcm[i*2] = 0.23*float32(math.Sin(2*math.Pi*431*tm)) + 0.07*float32(math.Sin(2*math.Pi*1973*tm))
		pcm[i*2+1] = 0.17*float32(math.Sin(2*math.Pi*683*tm+0.2)) + 0.05*float32(math.Sin(2*math.Pi*2381*tm+0.1))
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("EncodeFloat32: %v", err)
	}
	return packet
}

func convertResToInt16(res []int32) []int32 {
	pcm := make([]int32, len(res))
	for i, sample := range res {
		pcm[i] = int32(fixedpoint.Res2Int16(sample))
	}
	return pcm
}

func assertResAsFloat32(t *testing.T, name string, got []float32, res []int32) {
	t.Helper()
	if len(got) != len(res) {
		t.Fatalf("%s: got %d float samples, want %d", name, len(got), len(res))
	}
	const resToFloat = float32(1.0 / 8388608.0)
	for i := range got {
		want := float32(res[i]) * resToFloat
		if got[i] != want {
			t.Fatalf("%s sample %d: got %08x, want %08x", name, i, got[i], want)
		}
	}
}
