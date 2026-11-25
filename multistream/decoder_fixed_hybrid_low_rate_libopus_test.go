//go:build gopus_fixed_point

package multistream

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

func TestFixedMultistreamHybridLowRateMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, channels := range []int{1, 2} {
		packets := fixedLowRateHybridPackets(t, channels)
		for _, sampleRate := range []int{8000, 12000} {
			t.Run(fmt.Sprintf("rate%d/ch%d", sampleRate, channels), func(t *testing.T) {
				frameSize := sampleRate / 50
				cases := []libopustest.DecodeDiffCase{
					{Packet: packets[0], Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Packet: packets[1], Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Packet: packets[2], Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
					{Format: libopustest.DecodeDiffFormatInt16, FrameSize: uint32(frameSize)},
				}
				want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
				if err != nil {
					t.Fatalf("selected FIXED_POINT decoder: %v", err)
				}

				mapping := []byte{0}
				coupled := 0
				if channels == 2 {
					mapping = []byte{0, 1}
					coupled = 1
				}

				// Assert that the public call is handled by the fixed integer path,
				// then compare its RES conversion and entropy range to selected C.
				resDecoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for opus_res: %v", err)
				}
				for i, decodeCase := range cases {
					res, handled, err := resDecoder.DecodeToResFixed(decodeCase.Packet, frameSize)
					if err != nil {
						t.Fatalf("case %d DecodeToResFixed: %v", i, err)
					}
					if !handled {
						t.Fatalf("case %d mode=%d was declined by fixed decode", i, parseStreamTOC(packets[min(i, len(packets)-1)][0]).mode)
					}
					got := make([]int16, len(res))
					for sample, value := range res {
						got[sample] = fixedpoint.Res2Int16(value)
					}
					wantPCM := want[i].Int16()
					if want[i].Code != int32(frameSize) || len(got) != len(wantPCM) {
						t.Fatalf("case %d samples Go=%d C=%d status=%d", i, len(got), len(wantPCM), want[i].Code)
					}
					for sample := range got {
						if got[sample] != wantPCM[sample] {
							t.Fatalf("case %d sample %d: Go=%d selected C=%d", i, sample, got[sample], wantPCM[sample])
						}
					}
					if gotRange := resDecoder.FinalRange(); gotRange != want[i].FinalRange {
						t.Fatalf("case %d final range Go=%08x selected C=%08x", i, gotRange, want[i].FinalRange)
					}
				}

				floatCases := append([]libopustest.DecodeDiffCase(nil), cases...)
				for i := range floatCases {
					floatCases[i].Format = libopustest.DecodeDiffFormatFloat32
				}
				wantFloat, err := libopustest.ProbeDecodeSequence(sampleRate, channels, floatCases)
				if err != nil {
					t.Fatalf("selected FIXED_POINT float decoder: %v", err)
				}
				floatDecoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for float output: %v", err)
				}
				for i, decodeCase := range cases {
					got, err := floatDecoder.DecodeToFloat32(decodeCase.Packet, frameSize)
					if err != nil {
						t.Fatalf("case %d DecodeToFloat32: %v", i, err)
					}
					wantPCM := wantFloat[i].Float32()
					if wantFloat[i].Code != int32(frameSize) || len(got) != len(wantPCM) {
						t.Fatalf("case %d float samples Go=%d C=%d status=%d", i, len(got), len(wantPCM), wantFloat[i].Code)
					}
					for sample := range got {
						if math.Float32bits(got[sample]) != math.Float32bits(wantPCM[sample]) {
							t.Fatalf("case %d float sample %d: Go=%08x selected C=%08x", i, sample, math.Float32bits(got[sample]), math.Float32bits(wantPCM[sample]))
						}
					}
					if gotRange := floatDecoder.FinalRange(); gotRange != wantFloat[i].FinalRange {
						t.Fatalf("case %d float final range Go=%08x selected C=%08x", i, gotRange, wantFloat[i].FinalRange)
					}
				}

				int24Cases := append([]libopustest.DecodeDiffCase(nil), cases...)
				for i := range int24Cases {
					int24Cases[i].Format = libopustest.DecodeDiffFormatInt24
				}
				wantInt24, err := libopustest.ProbeDecodeSequence(sampleRate, channels, int24Cases)
				if err != nil {
					t.Fatalf("selected FIXED_POINT int24 decoder: %v", err)
				}
				int24Decoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for int24 output: %v", err)
				}
				for i, decodeCase := range cases {
					got, err := int24Decoder.DecodeToInt24(decodeCase.Packet, frameSize)
					if err != nil {
						t.Fatalf("case %d DecodeToInt24: %v", i, err)
					}
					wantPCM := wantInt24[i].Int24()
					if wantInt24[i].Code != int32(frameSize) || len(got) != len(wantPCM) {
						t.Fatalf("case %d int24 samples Go=%d C=%d status=%d", i, len(got), len(wantPCM), wantInt24[i].Code)
					}
					for sample := range got {
						if got[sample] != wantPCM[sample] {
							t.Fatalf("case %d int24 sample %d: Go=%d selected C=%d", i, sample, got[sample], wantPCM[sample])
						}
					}
					if gotRange := int24Decoder.FinalRange(); gotRange != wantInt24[i].FinalRange {
						t.Fatalf("case %d int24 final range Go=%08x selected C=%08x", i, gotRange, wantInt24[i].FinalRange)
					}
				}

				allocDecoder, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
				if err != nil {
					t.Fatalf("NewDecoder for allocation check: %v", err)
				}
				if _, handled, err := allocDecoder.DecodeToResFixed(packets[0], frameSize); err != nil || !handled {
					t.Fatalf("warm received DecodeToResFixed handled=%v err=%v", handled, err)
				}
				if _, handled, err := allocDecoder.DecodeToResFixed(nil, frameSize); err != nil || !handled {
					t.Fatalf("warm PLC DecodeToResFixed handled=%v err=%v", handled, err)
				}
				var allocErr error
				allocs := testing.AllocsPerRun(10, func() {
					if _, handled, err := allocDecoder.DecodeToResFixed(packets[0], frameSize); err != nil || !handled {
						allocErr = fmt.Errorf("received DecodeToResFixed handled=%v err=%v", handled, err)
						return
					}
					if _, handled, err := allocDecoder.DecodeToResFixed(nil, frameSize); err != nil || !handled {
						allocErr = fmt.Errorf("PLC DecodeToResFixed handled=%v err=%v", handled, err)
						return
					}
				})
				if allocErr != nil {
					t.Fatal(allocErr)
				}
				if allocs != 0 {
					t.Fatalf("warmed low-rate Hybrid receive+PLC cycle allocated %.2f objects, want 0", allocs)
				}
			})
		}
	}
}

func fixedLowRateHybridPackets(t *testing.T, channels int) [][]byte {
	t.Helper()
	const (
		frameSize  = 960
		frameCount = 3
	)
	pcm := make([]int16, frameSize*channels*frameCount)
	for i := range pcm {
		pcm[i] = int16((i*7919+channels*251+37)%30001 - 15000)
	}
	packets, err := libopustest.ProbeOpusEncodeFixed(libopustest.OpusEncodeFixedParams{
		SampleRate:    48000,
		Channels:      channels,
		Application:   libopustest.OpusApplicationAudio,
		ForceMode:     libopustest.OpusForceModeHybrid,
		Bandwidth:     libopustest.OpusBandwidthFullband,
		Bitrate:       64000 * channels,
		Complexity:    10,
		ForceChannels: channels,
		FrameSize:     frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	})
	if err != nil {
		t.Fatalf("selected FIXED_POINT encoder: %v", err)
	}
	if len(packets) != frameCount {
		t.Fatalf("selected FIXED_POINT encoder returned %d packets, want %d", len(packets), frameCount)
	}
	for i, packet := range packets {
		config := packet[0] >> 3
		if config < 12 || config >= 16 {
			t.Fatalf("packet %d TOC=%02x is not Hybrid", i, packet[0])
		}
	}
	return packets
}

func TestFixedProjectionHybridRedundancyMatchesLibopusInt24(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 9
		frameSize  = 480
		frameCount = 6
		bitrate    = 256000
	)
	ref := projectionDecodeRef(t, channels, frameSize, frameCount, bitrate)
	parts, err := parseMultistreamPacket(ref.packets[1], ref.streams)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) <= 4 || len(parts[4]) == 0 || parseStreamTOC(parts[4][0]).mode != streamModeHybrid {
		t.Fatalf("frame 1 stream 4 does not carry the expected Hybrid frame")
	}
	streamPackets := make([][][]byte, ref.streams)
	for frame, packet := range ref.packets {
		parts, err := parseMultistreamPacket(packet, ref.streams)
		if err != nil {
			t.Fatalf("frame %d parse multistream packet: %v", frame, err)
		}
		for stream, part := range parts {
			streamPackets[stream] = append(streamPackets[stream], part)
		}
	}
	for stream, packets := range streamPackets {
		childChannels := streamChannels(stream, ref.coupledStreams)
		childCoupled := 0
		childMapping := []byte{0}
		if childChannels == 2 {
			childCoupled = 1
			childMapping = []byte{0, 1}
		}
		childWant, err := decodeWithLibopusReferencePacketsInt24Gain(1, sampleRate, childChannels,
			1, childCoupled, frameSize, 0, childMapping, nil, packets)
		if err != nil {
			libopustest.HelperUnavailable(t, "fixed Hybrid child int24 decode", err)
		}
		child, err := NewDecoder(sampleRate, childChannels, 1, childCoupled, childMapping)
		if err != nil {
			t.Fatal(err)
		}
		for frame, packet := range packets {
			got, err := child.DecodeToInt24(packet, frameSize)
			if err != nil {
				t.Fatalf("stream %d frame %d DecodeToInt24: %v", stream, frame, err)
			}
			for i, sample := range got {
				if expected := childWant[frame*len(got)+i]; sample != expected {
					t.Fatalf("stream %d frame %d sample %d Go=%d C=%d", stream, frame, i, sample, expected)
				}
			}
		}
	}
	probe, err := NewDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, trivialMapping(channels))
	if err != nil {
		t.Fatal(err)
	}
	for frame, packet := range ref.packets {
		if _, handled, err := probe.DecodeToResFixed(packet, frameSize); err != nil || !handled {
			t.Fatalf("frame %d fixed stream decode handled=%v err=%v", frame, handled, err)
		}
		if frame == 1 {
			st := probe.decoders[4].(*streamState)
			if !st.fixedHybridRedundant || !st.fixedHybridRedundantToSilk || len(st.fixedHybridRedundantData) != 15 || !st.fixedHybridRedundantValid {
				t.Fatalf("frame 1 stream 4 redundancy state: active=%v toSilk=%v bytes=%d decoded=%v",
					st.fixedHybridRedundant, st.fixedHybridRedundantToSilk, len(st.fixedHybridRedundantData), st.fixedHybridRedundantValid)
			}
		}
	}

	want, err := decodeWithLibopusReferencePacketsInt24Gain(3, sampleRate, channels,
		ref.streams, ref.coupledStreams, frameSize, 0, trivialMapping(channels), ref.demixing, ref.packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed Hybrid redundancy projection int24 decode", err)
	}
	dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int32, 0, len(want))
	for frame, packet := range ref.packets {
		out, err := dec.DecodeToInt24(packet, frameSize)
		if err != nil {
			t.Fatalf("frame %d DecodeToInt24: %v", frame, err)
		}
		got = append(got, out...)
	}
	if len(got) != len(want) {
		t.Fatalf("sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}

	allocDec, err := NewDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, trivialMapping(channels))
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, frameSize*channels)
	decodeCycle := func() {
		for frame, packet := range ref.packets {
			if n, err := allocDec.DecodeIntoFloat32(packet, output, frameSize); err != nil || n != frameSize {
				t.Fatalf("allocation cycle frame %d DecodeIntoFloat32=(%d,%v)", frame, n, err)
			}
		}
	}
	decodeCycle()
	if allocs := testing.AllocsPerRun(5, decodeCycle); allocs != 0 {
		t.Fatalf("warm projection Hybrid redundancy allocations=%g, want 0", allocs)
	}
}

func TestFixedHybridToCELTTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const frameSize48 = 480
	modes := []encoder.Mode{encoder.ModeHybrid, encoder.ModeCELT}
	for _, channels := range []int{1, 2} {
		packets := encodeModeSwitchSingleStreamPackets(t, channels, frameSize48, modes)
		if len(packets) != len(modes) {
			t.Fatalf("encoded %d packets, want %d", len(packets), len(modes))
		}
		if got := streamModeOfPacket(packets[0]); got != streamModeHybrid {
			t.Fatalf("preceding packet mode=%d, want Hybrid", got)
		}
		if got := streamModeOfPacket(packets[1]); got != streamModeCELT {
			t.Fatalf("target packet mode=%d, want CELT", got)
		}

		coupled := 0
		if channels == 2 {
			coupled = 1
		}
		mapping := trivialMapping(channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			frameSize := frameSize48 * sampleRate / 48000
			steps := []transitionDecodeStep{
				{packet: packets[0], frameSize: frameSize},
				{packet: packets[1], frameSize: frameSize},
			}
			for _, gainQ8 := range []int{0, 768, -768} {
				t.Run(fmt.Sprintf("ch%d/fs%d/g%d", channels, sampleRate, gainQ8), func(t *testing.T) {
					wantFixed := decodePublicTransitionSequenceWithLibopus(t, sampleRate, channels, gainQ8, frameSize, steps)
					if len(wantFixed) != len(steps) {
						t.Fatalf("C returned %d fixed frames, want %d", len(wantFixed), len(steps))
					}

					dec, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					if err := dec.SetGain(gainQ8); err != nil {
						t.Fatalf("SetGain(%d): %v", gainQ8, err)
					}
					pathProbe, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("NewDecoder path probe: %v", err)
					}
					if err := pathProbe.SetGain(gainQ8); err != nil {
						t.Fatalf("path probe SetGain(%d): %v", gainQ8, err)
					}
					for i, step := range steps {
						_, fixedHandled, err := pathProbe.DecodeToResFixed(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("fixed path probe frame %d: %v", i, err)
						}
						if !fixedHandled {
							t.Fatalf("frame %d did not use the public fixed path", i)
						}
						got, err := dec.DecodeToFloat32(step.packet, step.frameSize)
						if err != nil {
							t.Fatalf("Go frame %d: %v", i, err)
						}
						if wantFixed[i].samples != frameSize || len(got) != frameSize*channels {
							t.Fatalf("frame %d sample count Go=%d fixed C=%d want=%d",
								i, len(got), wantFixed[i].samples*channels, frameSize*channels)
						}
						if gotRange := dec.FinalRange(); gotRange != wantFixed[i].finalRange {
							t.Fatalf("frame %d final range Go=%08x fixed C=%08x", i, gotRange, wantFixed[i].finalRange)
						}
						assertTransitionStagePCMExact(t, got, wantFixed[i].pcm, fmt.Sprintf("frame %d", i))
					}

					allocDec, err := NewDecoder(sampleRate, channels, 1, coupled, mapping)
					if err != nil {
						t.Fatalf("allocation decoder: %v", err)
					}
					if err := allocDec.SetGain(gainQ8); err != nil {
						t.Fatalf("allocation SetGain(%d): %v", gainQ8, err)
					}
					output := make([]float32, frameSize*channels)
					decodeSequence := func() error {
						for _, step := range steps {
							if _, err := allocDec.DecodeIntoFloat32(step.packet, output, step.frameSize); err != nil {
								return err
							}
						}
						return nil
					}
					for range 2 {
						if err := decodeSequence(); err != nil {
							t.Fatalf("warm allocation sequence: %v", err)
						}
					}
					var allocErr error
					allocs := testing.AllocsPerRun(10, func() {
						allocErr = decodeSequence()
					})
					if allocErr != nil {
						t.Fatalf("allocation sequence: %v", allocErr)
					}
					if allocs != 0 {
						t.Fatalf("warm Hybrid-to-CELT decode allocated %g times/op", allocs)
					}
				})
			}
		}
	}
}

func TestFixedHybridMultiframeInt24MatchesLibopusAndZeroAllocs(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	for _, spec := range buildSurroundDecodeFuzzSweep() {
		if spec.channels != 1 || spec.frameSize < 1920 || spec.bitrate != 16000 || spec.int16Path {
			continue
		}
		t.Run(spec.name, func(t *testing.T) {
			pcm := seededMultichannelPCM(spec.seed, spec.channels, spec.frameSize, spec.frameCount)
			ref, err := encodeLibopusSurround(sampleRate, spec.channels, 1, 2049,
				spec.bitrate, spec.vbr, spec.vbrConstraint, 10, -1000,
				spec.frameSize, spec.frameCount, 4000, pcm, false)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed Hybrid multiframe encode", err)
			}
			for i, packet := range ref.packets {
				if parseStreamTOC(packet[0]).mode != streamModeHybrid || packet[0]&3 == 0 {
					t.Fatalf("packet %d does not exercise multi-frame Hybrid: TOC=%02x", i, packet[0])
				}
			}
			want, err := decodeWithLibopusReferencePacketsInt24Gain(1, sampleRate, spec.channels,
				ref.streams, ref.coupledStreams, spec.frameSize, spec.gainQ8, ref.mapping, nil, ref.packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed Hybrid multiframe int24 decode", err)
			}
			dec, err := NewDecoder(sampleRate, spec.channels, ref.streams, ref.coupledStreams, ref.mapping)
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(spec.gainQ8); err != nil {
				t.Fatal(err)
			}
			for frame, packet := range ref.packets {
				got, err := dec.DecodeToInt24(packet, spec.frameSize)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != spec.frameSize*spec.channels {
					t.Fatalf("frame %d samples=%d, want %d", frame, len(got), spec.frameSize*spec.channels)
				}
				for i, sample := range got {
					if expected := want[frame*len(got)+i]; sample != expected {
						t.Fatalf("frame %d sample %d Go=%d C=%d", frame, i, sample, expected)
					}
				}
			}
			output := make([]float32, spec.frameSize*spec.channels)
			decode := func() {
				for _, packet := range ref.packets {
					if n, err := dec.DecodeIntoFloat32(packet, output, spec.frameSize); err != nil || n != spec.frameSize {
						t.Fatalf("DecodeIntoFloat32=%d, %v", n, err)
					}
				}
			}
			decode()
			if allocs := testing.AllocsPerRun(20, decode); allocs != 0 {
				t.Fatalf("warm Hybrid multi-frame allocations=%g, want 0", allocs)
			}
		})
	}
}

func TestFixedPublicMultistreamHybridDecodeToInt16MatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		frameCount = 6
	)
	pcm := make([]float32, frameSize*frameCount)
	for i := range pcm {
		time := float64(i) / sampleRate
		pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*time) + 0.1*math.Sin(2*math.Pi*1900*time))
	}
	records, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate: sampleRate, Channels: channels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     libopustest.EncodeDiffForceModeHybrid,
		Bandwidth:     libopustest.EncodeDiffBandwidthFullband,
		Bitrate:       24000,
		Complexity:    10,
		Signal:        libopustest.EncodeDiffSignalMusic,
		VBR:           true,
		VBRConstraint: false,
		ForceChannels: channels,
		FrameSize:     frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	})
	if err != nil {
		t.Fatalf("selected libopus encode: %v", err)
	}
	var cases []libopustest.DecodeDiffCase
	for _, record := range records {
		if record.Ret <= 1 || len(record.Packet) == 0 {
			continue
		}
		cfg := record.Packet[0] >> 3
		if cfg < 12 || cfg > 15 {
			continue
		}
		cases = append(cases, libopustest.DecodeDiffCase{
			Packet: record.Packet, Format: libopustest.DecodeDiffFormatInt16, FrameSize: frameSize,
		})
	}
	if len(cases) == 0 {
		t.Fatal("selected C encoder produced no Hybrid packets")
	}
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		t.Fatalf("selected libopus decode sequence: %v", err)
	}
	dec, err := NewDecoder(sampleRate, channels, 1, 0, []byte{0})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	for frame, decodeCase := range cases {
		got, err := dec.DecodeToInt16(decodeCase.Packet, frameSize)
		if err != nil {
			t.Fatalf("frame %d DecodeToInt16: %v", frame, err)
		}
		if want[frame].Code != frameSize {
			t.Fatalf("frame %d selected C returned %d samples, want %d", frame, want[frame].Code, frameSize)
		}
		wantPCM := want[frame].Int16()
		if len(got) != len(wantPCM) {
			t.Fatalf("frame %d sample count Go=%d C=%d", frame, len(got), len(wantPCM))
		}
		for i := range got {
			if got[i] != wantPCM[i] {
				t.Fatalf("frame %d sample %d: Go=%d selected C=%d", frame, i, got[i], wantPCM[i])
			}
		}
	}
}

func TestFixedSILKMultiframeRedundancyMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := testSILKRedundancyTwoFramePacket(t)

	want, err := libopustest.ProbeDecodeSequence(48000, 2, []libopustest.DecodeDiffCase{{
		Packet: packet, Format: libopustest.DecodeDiffFormatInt24, FrameSize: 960,
	}})
	if err != nil {
		t.Fatalf("selected C decoder: %v", err)
	}
	if want[0].Code != 960 {
		t.Fatalf("selected C samples=%d, want 960", want[0].Code)
	}
	decoder, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	res, handled, err := decoder.DecodeToResFixed(packet, 960)
	if err != nil || !handled {
		t.Fatalf("fixed decode handled=%v err=%v", handled, err)
	}
	wantRes := want[0].Int24()
	if len(res) != len(wantRes) {
		t.Fatalf("opus_res samples Go=%d C=%d", len(res), len(wantRes))
	}
	for i, sample := range res {
		if sample != wantRes[i] {
			t.Fatalf("opus_res sample %d: Go=%d C=%d", i, sample, wantRes[i])
		}
	}
	if got := decoder.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("final range Go=%08x C=%08x", got, want[0].FinalRange)
	}
}

func TestFixedSILKRedundancyDecodeWarmZeroAllocs(t *testing.T) {
	packet := []byte{
		0x40, 0x82, 0x2e, 0x68, 0x51, 0x73, 0xfb, 0x43,
		0x3c, 0xec, 0xdf, 0xa7, 0xe6, 0xca, 0xd8, 0xbc,
		0xa7, 0xa4, 0x7a, 0x58, 0x4b, 0x92, 0x89, 0x7d,
		0x80, 0x1c, 0x65, 0xfc, 0x40, 0xaf, 0xa6, 0x1d,
		0x51, 0x63, 0x52, 0x57, 0x7a,
	}
	decoder, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if res, handled, err := decoder.DecodeToResFixed(packet, 960); err != nil || !handled || len(res) != 480 {
			t.Fatalf("warm decode samples=%d handled=%v err=%v", len(res), handled, err)
		}
	}
	var decodeErr error
	var decodeFailed bool
	allocs := testing.AllocsPerRun(100, func() {
		res, handled, err := decoder.DecodeToResFixed(packet, 960)
		if err != nil || !handled || len(res) != 480 {
			decodeErr = err
			decodeFailed = true
		}
	})
	if decodeFailed {
		t.Fatalf("warm decode failed: %v", decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm fixed SILK redundancy decode allocs=%g, want 0", allocs)
	}
}

func TestFixedPLCUsesCELTAfterRedundancySequence(t *testing.T) {
	packets := encodePrevRedundancyPLCSequence(t)
	sequence := [][]byte{packets[0], packets[1], packets[2], nil, nil, packets[3]}
	want, err := decodeWithLibopusReferencePacketsInt24Gain(1, prevRedundancySampleRate, 1, 1, 0,
		prevRedundancyFrameSize, 0, []byte{0}, nil, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed CELT redundancy PLC sequence", err)
	}

	dec, err := NewDecoder(prevRedundancySampleRate, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]int32, 0, len(want))
	for frame, packet := range sequence {
		out, err := dec.DecodeToInt24(packet, prevRedundancyFrameSize)
		if err != nil {
			t.Fatalf("frame %d DecodeToInt24: %v", frame, err)
		}
		if frame == 2 {
			assertPreviousRedundancySelectsCELT(t, dec)
			st := dec.decoders[0].(*streamState)
			if !st.fixedHybridRedundant || st.fixedHybridRedundantToSilk || len(st.fixedHybridRedundantData) <= 1 {
				t.Fatalf("frame 2 fixed redundancy state active=%v toSilk=%v bytes=%d",
					st.fixedHybridRedundant, st.fixedHybridRedundantToSilk, len(st.fixedHybridRedundantData))
			}
		}
		if frame == 3 {
			assertPLCConsumedPreviousRedundancy(t, dec)
		}
		got = append(got, out...)
	}
	if len(got) != len(want) {
		t.Fatalf("int24 sample count Go=%d C=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample %d Go=%d C=%d", i, got[i], want[i])
		}
	}

	allocDec, err := NewDecoder(prevRedundancySampleRate, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	decodeCycle := func() {
		for frame, packet := range sequence {
			if _, handled, err := allocDec.DecodeToResFixed(packet, prevRedundancyFrameSize); err != nil || !handled {
				t.Fatalf("allocation cycle frame %d DecodeToResFixed=(handled=%v, err=%v)", frame, handled, err)
			}
		}
	}
	decodeCycle()
	if allocs := testing.AllocsPerRun(10, decodeCycle); allocs != 0 {
		t.Fatalf("warm CELT redundancy PLC allocations=%g, want 0", allocs)
	}
}
