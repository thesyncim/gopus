package gopus

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math"
	"os"
	"strconv"
	"testing"
)

func TestDecodeWithFECFallbackUsesActiveSILKStateAfterDTX(t *testing.T) {
	libopustest.RequireOracle(t)
	packets := []struct {
		name string
		hex  string
	}{
		{"mediumband", "2c886c8fa475063df63e6542b4ce9f05336f72206289fea7969d38df05574daee769acbfb80969ae842bc66b7c899baed97f5f567e16cf66543eaf4b57bfed16ff4c788b0d2fe3fdcf505fa2f2f019f0a5eecc6525b878f33f366c019488098b917f3d927910602f569282dc523180"},
		{"wideband", "4c886bdd439a389a6d1adfa4b0f4676d21b8be7e8fadb6595ae40dd22868cf1ac68f79b23eba441b9c1566addf3ecd6e230f512b52a599418013841f59bbde9f010ac0d9315e55524fb9883f021dbd2a7d22ba628301297eabc89a2744b3d211a25d0afd66eb9d94d18733ea9760fdbc6eb475882cbb13e0932df6065c85427141650526d2566aacfeb63cfc0559e06340948af9bd842d7d13c0"},
		{"hybrid", fecRateChangePrimeHex},
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, fixture := range packets {
				for _, concealment := range []struct {
					name      string
					packet    []byte
					decodeFEC bool
				}{
					{name: "plc"},
					{name: "fec_missing", decodeFEC: true},
					{name: "fec_celt", packet: []byte{0x80}, decodeFEC: true},
				} {
					t.Run(fmt.Sprintf("rate%d/ch%d/%s/%s", sampleRate, channels, fixture.name, concealment.name), func(t *testing.T) {
						packet := mustDecodeFECTransitionPacket(t, fixture.hex)
						frameSize := sampleRate / 50
						// DTX updates the packet TOC but keeps SILK's active internal
						// rate and stereo layout. A CELT carrier cannot supply FEC
						// and takes the same path as ordinary concealment.
						steps := []libopustest.DecodeDiffCase{
							{Packet: packet, FrameSize: uint32(frameSize)},
							{Packet: []byte{0x80}, FrameSize: uint32(sampleRate / 400)},
							{Packet: concealment.packet, FrameSize: uint32(frameSize), DecodeFEC: concealment.decodeFEC},
							{Packet: packet, FrameSize: uint32(frameSize)},
						}
						want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "FEC fallback after DTX", err)
						}
						if len(want) != len(steps) {
							t.Fatalf("oracle returned %d calls, want %d", len(want), len(steps))
						}
						wantPCM := make([][]float32, len(want))
						for i := range want {
							if want[i].Code != int32(steps[i].FrameSize) {
								t.Fatalf("oracle call %d returned %d, want %d", i, want[i].Code, steps[i].FrameSize)
							}
							wantPCM[i] = want[i].Float32()
						}
						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatal(err)
						}
						pcm := make([]float32, frameSize*channels)
						runSequence := func() {
							dec.Reset()
							for i, step := range steps {
								out := pcm[:int(step.FrameSize)*channels]
								var n int
								var err error
								if step.DecodeFEC {
									n, err = dec.DecodeWithFEC(step.Packet, out, true)
								} else {
									n, err = dec.Decode(step.Packet, out)
								}
								if err != nil || n != int(want[i].Code) {
									t.Fatalf("call %d returned (%d,%v), C=%d", i, n, err, want[i].Code)
								}
								for j, sample := range out {
									if got, expected := math.Float32bits(sample), math.Float32bits(wantPCM[i][j]); got != expected {
										t.Fatalf("call %d PCM[%d]=%08x C=%08x", i, j, got, expected)
									}
								}
								if got := dec.FinalRange(); got != want[i].FinalRange {
									t.Fatalf("call %d range=%08x C=%08x", i, got, want[i].FinalRange)
								}
								if fixture.name == "hybrid" && (dec.prevMode != ModeHybrid || dec.prevRedundancy) {
									t.Fatalf("call %d must retain Hybrid concealment: mode=%v redundancy=%t", i, dec.prevMode, dec.prevRedundancy)
								}
							}
						}
						runSequence()
						if allocs := testing.AllocsPerRun(10, runSequence); allocs != 0 {
							t.Fatalf("warmed FEC/DTX sequence allocations=%g, want zero", allocs)
						}
					})
				}
			}
		}
	}
}

func TestDecodeFECHybridCELTStateRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	// Literal WB stereo SILK 20 ms with LBRR, mono FB Hybrid 10 ms, and mono
	// CELT 2.5 ms packets keep this transition independent of Go encoder changes.
	stereoSILK20 := mustDecodeFECTransitionPacket(t, "4cf4b570ea239b5f266d24663ae2018a2bad20194a98c83bf66e5ec83862f90768bb45b8e0bbc425dd1108971a382be20259bfbdae586b222ee1ed5ce7d224a6996468ef220390695d90df41c9435ac102b33f0aa0244c329f763ca5f04799b2bfd140bac3adb2d933b3e6f4ce18e44320bb0ff32b30")
	monoHybrid10 := mustDecodeFECTransitionPacket(t, "70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13ef63a92")
	monoCELT2p5 := mustDecodeFECTransitionPacket(t, "e0709a5e94881db77455c8f4a5cee95b6a6371fa6535a8228e472c23f406a21af682798a46ab92e97cf6dc6b419353bc82f2277591ad")
	if toc := ParseTOC(stereoSILK20[0]); toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("stereo SILK fixture TOC=(%v, stereo=%t)", toc.Mode, toc.Stereo)
	}
	if toc := ParseTOC(monoHybrid10[0]); toc.Mode != ModeHybrid || toc.Stereo {
		t.Fatalf("mono Hybrid fixture TOC=(%v, stereo=%t)", toc.Mode, toc.Stereo)
	}
	if toc := ParseTOC(monoCELT2p5[0]); toc.Mode != ModeCELT || toc.Stereo {
		t.Fatalf("mono CELT fixture TOC=(%v, stereo=%t)", toc.Mode, toc.Stereo)
	}

	formats := []struct {
		name   string
		format uint32
	}{
		{name: "float32", format: libopustest.DecodeDiffFormatFloat32},
		{name: "int16", format: libopustest.DecodeDiffFormatInt16},
		{name: "int24", format: libopustest.DecodeDiffFormatInt24},
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, outputFormat := range formats {
			t.Run(itoaSmall(sampleRate)+"/"+outputFormat.name, func(t *testing.T) {
				frames := [5]int{
					sampleRate / 50,        // 20 ms stereo SILK prime
					sampleRate / 400,       // 2.5 ms loss
					sampleRate / 100,       // 10 ms mono Hybrid transition
					sampleRate * 30 / 1000, // 10 ms recursive PLC + 20 ms stereo SILK LBRR
					sampleRate / 400,       // 2.5 ms mono CELT recovery
				}
				packets := [5][]byte{stereoSILK20, nil, monoHybrid10, stereoSILK20, monoCELT2p5}
				cases := make([]libopustest.DecodeDiffCase, len(frames))
				for i := range cases {
					format := outputFormat.format
					if i == 3 {
						// The public Go API exposes DecodeWithFEC only for float32.
						format = libopustest.DecodeDiffFormatFloat32
					}
					cases[i] = libopustest.DecodeDiffCase{
						Packet:    packets[i],
						Format:    format,
						FrameSize: uint32(frames[i]),
					}
				}
				cases[3].DecodeFEC = true
				want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, cases)
				if err != nil {
					libopustest.HelperUnavailable(t, "stateful selected-libopus SILK recovery sequence", err)
				}
				if len(want) != len(cases) {
					t.Fatalf("libopus returned %d calls, want %d", len(want), len(cases))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				for i, packet := range packets {
					var n int
					var gotF32 []float32
					var gotI16 []int16
					var gotI24 []int32
					if i == 3 || outputFormat.format == libopustest.DecodeDiffFormatFloat32 {
						gotF32 = make([]float32, frames[i]*2)
						if i == 3 {
							n, err = dec.DecodeWithFEC(packet, gotF32, true)
						} else {
							n, err = dec.Decode(packet, gotF32)
						}
					} else if outputFormat.format == libopustest.DecodeDiffFormatInt16 {
						gotI16 = make([]int16, frames[i]*2)
						n, err = dec.DecodeInt16(packet, gotI16)
					} else {
						gotI24 = make([]int32, frames[i]*2)
						n, err = dec.DecodeInt24(packet, gotI24)
					}
					if err != nil {
						t.Fatalf("Go call %d: %v", i, err)
					}
					if n != int(want[i].Code) {
						t.Fatalf("call %d returned %d samples, C=%d", i, n, want[i].Code)
					}
					if gotRange, wantRange := dec.FinalRange(), want[i].FinalRange; gotRange != wantRange {
						t.Fatalf("call %d final range=%08x, C=%08x", i, gotRange, wantRange)
					}

					switch {
					case i == 3 || outputFormat.format == libopustest.DecodeDiffFormatFloat32:
						ref := want[i].Float32()
						for j := range ref {
							if gotBits, wantBits := math.Float32bits(gotF32[j]), math.Float32bits(ref[j]); gotBits != wantBits {
								t.Fatalf("call %d float32 PCM[%d]=%08x, C=%08x", i, j, gotBits, wantBits)
							}
						}
					case outputFormat.format == libopustest.DecodeDiffFormatInt16:
						ref := want[i].Int16()
						for j := range ref {
							if gotI16[j] != ref[j] {
								t.Fatalf("call %d int16 PCM[%d]=%d, C=%d", i, j, gotI16[j], ref[j])
							}
						}
					default:
						ref := want[i].Int24()
						for j := range ref {
							if gotI24[j] != ref[j] {
								t.Fatalf("call %d int24 PCM[%d]=%d, C=%d", i, j, gotI24[j], ref[j])
							}
						}
					}
				}
			})
		}
	}

	// Repeated Reset plus the same recovery sequence stays allocation-free once
	// the decoder's buffers and per-channel state have been warmed.
	const sampleRate = 48000
	frames := [5]int{sampleRate / 50, sampleRate / 400, sampleRate / 100, sampleRate * 30 / 1000, sampleRate / 400}
	packets := [5][]byte{stereoSILK20, nil, monoHybrid10, stereoSILK20, monoCELT2p5}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
	if err != nil {
		t.Fatalf("NewDecoder for allocation check: %v", err)
	}
	outputs := [5][]float32{
		make([]float32, frames[0]*2),
		make([]float32, frames[1]*2),
		make([]float32, frames[2]*2),
		make([]float32, frames[3]*2),
		make([]float32, frames[4]*2),
	}
	var sequenceErr error
	badCountStep := -1
	badCount := 0
	runSequence := func() {
		dec.Reset()
		for i, packet := range packets {
			var n int
			var callErr error
			if i == 3 {
				n, callErr = dec.DecodeWithFEC(packet, outputs[i], true)
			} else {
				n, callErr = dec.Decode(packet, outputs[i])
			}
			if callErr != nil {
				sequenceErr = callErr
				return
			}
			if n != frames[i] {
				badCountStep = i
				badCount = n
				return
			}
		}
	}
	for range 3 {
		runSequence()
		if sequenceErr != nil {
			t.Fatalf("warm Reset/recovery sequence: %v", sequenceErr)
		}
		if badCountStep >= 0 {
			t.Fatalf("warm Reset/recovery call %d returned %d samples, want %d", badCountStep, badCount, frames[badCountStep])
		}
	}
	allocs := testing.AllocsPerRun(10, runSequence)
	if sequenceErr != nil {
		t.Fatalf("measured Reset/recovery sequence: %v", sequenceErr)
	}
	if badCountStep >= 0 {
		t.Fatalf("measured Reset/recovery call %d returned %d samples, want %d", badCountStep, badCount, frames[badCountStep])
	}
	if allocs != 0 {
		t.Fatalf("warmed Reset/recovery sequence allocations=%g, want 0", allocs)
	}
}

func TestDecodeWithFECHybridToSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	for _, sampleRate := range []int{16000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, gainQ8 := range []int{0, 768} {
				for _, hasLBRR := range []bool{false, true} {
					name := "no_lbrr"
					if hasLBRR {
						name = "lbrr"
					}
					t.Run(strconv.Itoa(sampleRate)+"hz/ch"+strconv.Itoa(channels)+"/gain"+strconv.Itoa(gainQ8)+"/"+name, func(t *testing.T) {
						const primeFrameSize48 = 960
						frameSize := sampleRate * 3 / 25 // 120 ms, including the PLC prefix.
						primeFrameSize := primeFrameSize48 * sampleRate / 48000
						prime := encodeAPIRateHybridPacketFrameSize(t, channels, primeFrameSize48)
						var recovery []byte
						if hasLBRR {
							_, recovery = encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, 960)
							if !PacketHasLBRR(recovery) {
								t.Fatal("recovery packet does not carry LBRR")
							}
						} else {
							recovery = encodeAPIRateSILKPacketFrameSize(t, channels, 480)
							if len(recovery) <= 1 || PacketHasLBRR(recovery) {
								t.Fatalf("no-LBRR SILK packet length=%d hasLBRR=%v", len(recovery), PacketHasLBRR(recovery))
							}
						}
						if got := ParseTOC(recovery[0]).Mode; got != ModeSILK {
							t.Fatalf("recovery packet mode=%v want SILK", got)
						}

						steps := []libopusAPIRateDecodeStep{
							{packet: prime},
							{packet: recovery, fec: true},
						}
						want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsGainRanges(sampleRate, channels, frameSize, gainQ8, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "Hybrid-to-SILK FEC transition reference", err)
						}
						stride := frameSize * channels
						if len(want) != (primeFrameSize+frameSize)*channels || len(ranges) != len(steps) {
							t.Fatalf("reference samples/ranges=%d/%d want=%d/%d", len(want), len(ranges), (primeFrameSize+frameSize)*channels, len(steps))
						}

						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gainQ8); err != nil {
							t.Fatal(err)
						}
						out := make([]float32, stride)
						if n, err := dec.Decode(prime, out); err != nil || n != primeFrameSize {
							t.Fatalf("prime decode=(%d,%v), want %d", n, err, primeFrameSize)
						}
						assertHybridSILKFECPCM(t, out[:primeFrameSize*channels], want[:primeFrameSize*channels], "prime")
						if got := dec.FinalRange(); got != ranges[0] {
							t.Fatalf("prime range=%08x want %08x", got, ranges[0])
						}
						n, err := dec.DecodeWithFEC(recovery, out, true)
						if err != nil || n != frameSize {
							t.Fatalf("FEC decode=(%d,%v), want %d", n, err, frameSize)
						}
						assertHybridSILKFECPCM(t, out, want[primeFrameSize*channels:], "FEC")
						if got := dec.FinalRange(); got != ranges[1] {
							t.Fatalf("FEC range=%08x want %08x", got, ranges[1])
						}
					})
				}
			}
		}
	}
}

func assertHybridSILKFECPCM(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM length=%d want=%d", label, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s PCM[%d]=%08x want=%08x", label, i, gotBits, wantBits)
		}
	}
}

func TestDecodeWithFECHybridToSILKWarmZeroAllocs(t *testing.T) {
	_, recovery := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, 960)
	if !PacketHasLBRR(recovery) {
		t.Fatal("warm SILK recovery packet has no LBRR")
	}
	prime := encodeAPIRateHybridPacketFrameSize(t, 1, 960)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetGain(768); err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, 5760)
	decodeSequence := func() {
		dec.Reset()
		if n, err := dec.Decode(prime, frame); err != nil || n != 960 {
			t.Fatalf("prime decode=(%d,%v)", n, err)
		}
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != 5760 {
			t.Fatalf("FEC decode=(%d,%v)", n, err)
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(20, decodeSequence); allocs != 0 {
		t.Fatalf("warm Hybrid-to-SILK FEC allocations=%g, want zero", allocs)
	}
}

func TestDecodeWithFECMultiFrameSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	cases := []struct {
		name      string
		frameSize int // at 48 kHz: 1920 = 40 ms, 2880 = 60 ms
		bw        Bandwidth
		decRate   int // decoder API rate (native rate keeps the check resampler-noise free)
		channels  int
	}{
		{"NB_40ms", 1920, BandwidthNarrowband, 8000, 1},
		{"MB_40ms", 1920, BandwidthMediumband, 12000, 1},
		{"WB_40ms", 1920, BandwidthWideband, 16000, 1},
		{"NB_60ms", 2880, BandwidthNarrowband, 8000, 1},
		{"MB_60ms", 2880, BandwidthMediumband, 12000, 1},
		{"WB_60ms", 2880, BandwidthWideband, 16000, 1},
		{"WB_60ms_48k", 2880, BandwidthWideband, 48000, 1},
		{"WB_60ms_stereo", 2880, BandwidthWideband, 16000, 2},
		{"WB_40ms_stereo", 1920, BandwidthWideband, 16000, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channels := tc.channels
			packets := encodeFECBurstyStreamForTest(t, tc.bw, 24000, channels, tc.frameSize, 40)

			fs, err := packetSamplesAtRate(packets[0], tc.decRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			// Collect every LBRR-carrying recovery packet (index >= 2 so the
			// decoder is warmed up) and verify each as an FEC recovery step.
			recovered := 0
			partial := 0
			missingPrefix := 0
			replayLogged := false
			logReplay := func(recovery int) {
				if replayLogged {
					return
				}
				replayLogged = true
				// The encoder's native arithmetic can change the input packets.
				// Preserve the first failing history for cross-host decoder replay.
				t.Logf("FEC replay: rate=%d channels=%d frame_size=%d recovery=%d", tc.decRate, channels, fs, recovery)
				for i, packet := range packets[:recovery+1] {
					t.Logf("FEC replay packet %d: %x", i, packet)
				}
			}
			for r := 2; r < len(packets); r++ {
				if !packetHasInBandFEC(t, packets[r]) {
					continue
				}

				// Plan: decode packets[0..r-1] normally, then FEC-recover packets[r].
				steps := make([]libopusAPIRateDecodeStep, 0, r+1)
				for i := 0; i < r; i++ {
					steps = append(steps, libopusAPIRateDecodeStep{packet: packets[i]})
				}
				steps = append(steps, libopusAPIRateDecodeStep{packet: packets[r], fec: true})
				fecIdx := len(steps) - 1

				want, wantRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(tc.decRate, channels, fs, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "multi-frame FEC reference", err)
				}
				if len(want) != len(steps)*fs*channels || len(wantRanges) != len(steps) {
					t.Fatalf("reference geometry: samples=%d ranges=%d, want samples=%d ranges=%d",
						len(want), len(wantRanges), len(steps)*fs*channels, len(steps))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(tc.decRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				buf := make([]float32, fs*channels)
				got := make([]float32, 0, len(want))
				var gotRange uint32
				fecStart := 0
				for i, s := range steps {
					if i == fecIdx {
						fecStart = len(got)
					}
					var n int
					var de error
					if s.fec {
						n, de = dec.DecodeWithFEC(s.packet, buf, true)
					} else {
						n, de = dec.Decode(s.packet, buf)
					}
					if de != nil {
						t.Fatalf("recovery packet %d step %d: %v", r, i, de)
					}
					if n != fs {
						logReplay(r)
						t.Fatalf("recovery packet %d step %d: samples=%d want=%d", r, i, n, fs)
					}
					if i < fecIdx {
						if dec.FinalRange() != wantRanges[i] {
							logReplay(r)
							t.Fatalf("recovery packet %d prefix step %d: range=%08x want=%08x", r, i, dec.FinalRange(), wantRanges[i])
						}
						for j, sample := range buf[:n*channels] {
							expected := want[i*fs*channels+j]
							if math.Float32bits(sample) != math.Float32bits(expected) {
								logReplay(r)
								t.Fatalf("recovery packet %d prefix step %d sample %d: bits=%08x want=%08x", r, i, j, math.Float32bits(sample), math.Float32bits(expected))
							}
						}
					}
					got = append(got, buf[:n*channels]...)
					if i == fecIdx {
						gotRange = dec.FinalRange()
					}
				}

				// Final-range must be bit-exact on the FEC step.
				if gotRange != wantRanges[fecIdx] {
					logReplay(r)
					t.Errorf("recovery packet %d: FEC final-range mismatch gopus=0x%08x libopus=0x%08x",
						r, gotRange, wantRanges[fecIdx])
				}

				// FEC-recovered frame must be sample-exact.
				fecEnd := fecStart + fs*channels
				maxDiff := 0.0
				worst := -1
				firstMismatch := -1
				for j := fecStart; j < fecEnd; j++ {
					if firstMismatch < 0 && math.Float32bits(got[j]) != math.Float32bits(want[j]) {
						firstMismatch = j - fecStart
					}
					d := math.Abs(float64(got[j] - want[j]))
					if d > maxDiff {
						maxDiff = d
						worst = j - fecStart
					}
				}
				// SILK synthesis and MS-to-LR operate on integers. At the native
				// API rate, converting int16 PCM to float32 / 32768 is exact.
				// Matching entropy ranges alone does not prove matching PLC state.
				if firstMismatch >= 0 {
					logReplay(r)
					t.Errorf("recovery packet %d: FEC PCM bits differ at sample %d: got=%08x want=%08x; worst per-sample diff=%.6f at sample %d (fs=%d)",
						r, firstMismatch, math.Float32bits(got[fecStart+firstMismatch]), math.Float32bits(want[fecStart+firstMismatch]), maxDiff, worst, fs)
				}

				recovered++
				if channels == 1 && isPartialMultiFrameLBRR(t, packets[r]) {
					partial++
				}
				if channels == 1 && hasMissingLBRRPrefixBeforePresentFrame(t, packets[r]) {
					missingPrefix++
				}
			}

			if recovered == 0 {
				t.Fatalf("no LBRR-carrying recovery packets emitted for %s", tc.name)
			}
			// The partial-LBRR gate (the multi-LBRR-frame mix this regression
			// targets) is asserted for mono, where the first-payload flag layout is
			// unambiguous. Stereo packets still exercise the same DecodeFEC path.
			if channels == 1 && partial == 0 {
				t.Fatalf("no PARTIAL multi-frame LBRR packets emitted for %s; "+
					"test does not exercise the multi-LBRR-frame mix it targets", tc.name)
			}
			if channels == 1 && missingPrefix == 0 {
				t.Fatalf("no multi-frame LBRR packet begins with a missing frame followed by present LBRR for %s", tc.name)
			}
			t.Logf("%s: verified %d FEC recoveries (%d partial, %d missing-prefix multi-frame LBRR)", tc.name, recovered, partial, missingPrefix)
		})
	}
}

// encodeFECBurstyStreamForTest encodes a SILK stream with FEC enabled and a
// bursty (variable speech-activity) signal so the encoder emits PARTIAL
// multi-frame LBRR symbols: the ~2.3 Hz amplitude-modulated envelope has
// near-silent troughs, so within a 40/60 ms packet only some 20 ms sub-frames
// exceed the encoder's LBRR speech-activity threshold and carry LBRR.
func encodeFECBurstyStreamForTest(t *testing.T, bandwidth Bandwidth, bitrate, channels, frameSize, nFrames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, set := range []struct {
		name string
		fn   func() error
	}{
		{"SetMode", func() error { return enc.SetMode(EncoderModeSILK) }},
		{"SetFrameSize", func() error { return enc.SetFrameSize(frameSize) }},
		{"SetBandwidth", func() error { return enc.SetBandwidth(bandwidth) }},
		{"SetBitrate", func() error { return enc.SetBitrate(bitrate * channels) }},
		{"SetSignal", func() error { return enc.SetSignal(SignalVoice) }},
		{"SetPacketLoss", func() error { return enc.SetPacketLoss(25) }},
	} {
		if err := set.fn(); err != nil {
			t.Fatalf("%s: %v", set.name, err)
		}
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	enc.SetFEC(true)

	packets := make([][]byte, 0, nFrames)
	for frameIndex := range nFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			env := 0.5 + 0.5*math.Sin(2*math.Pi*2.3*tm)
			env = env * env // sharpen troughs to push some sub-frames below the LBRR threshold
			f0 := 200.0 + 40.0*math.Sin(2*math.Pi*0.7*tm)
			s := env * (0.5*math.Sin(2*math.Pi*f0*tm) + 0.2*math.Sin(2*math.Pi*2*f0*tm+0.3))
			pcm[i*channels] = float32(s)
			if channels == 2 {
				pcm[i*channels+1] = float32(s * 0.9)
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil || len(packet) == 0 {
			t.Fatalf("Encode frame %d: %v len=%d", frameIndex, err, len(packet))
		}
		if toc := ParseTOC(packet[0]); toc.Mode != ModeSILK {
			t.Fatalf("Encode frame %d mode=%v want SILK", frameIndex, toc.Mode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

// isPartialMultiFrameLBRR reports whether a multi-frame SILK packet carries LBRR
// for some but not all of its internal 20 ms sub-frames (a partial LBRR_flags
// symbol). These are the packets that mix real LBRR sub-frames with concealed
// sub-frames inside one FEC recovery decode.
func isPartialMultiFrameLBRR(t *testing.T, packet []byte) bool {
	t.Helper()
	toc := ParseTOC(packet[0])
	if toc.Mode == ModeCELT || toc.FrameSize <= 960 {
		return false
	}
	first, err := extractFirstFramePayload(packet, toc)
	if err != nil || len(first) == 0 {
		return false
	}
	nFrames := toc.FrameSize / 960
	if nFrames < 2 || nFrames > 3 {
		return false
	}
	flags, ok := decodeMonoLBRRFlagsForTest(first, nFrames)
	if !ok {
		return false
	}
	set := 0
	for i := range nFrames {
		set += flags[i]
	}
	return set > 0 && set < nFrames
}

// hasMissingLBRRPrefixBeforePresentFrame reports a FEC packet whose first
// internal 20 ms frame has no LBRR while a later frame does. The PLC prefix
// resets that channel's OSCE state before the later LBRR frame runs its
// postfilter within the same DecodeWithFEC call.
func hasMissingLBRRPrefixBeforePresentFrame(t *testing.T, packet []byte) bool {
	t.Helper()
	toc := ParseTOC(packet[0])
	if toc.Mode == ModeCELT || toc.FrameSize <= 960 {
		return false
	}
	first, err := extractFirstFramePayload(packet, toc)
	if err != nil || len(first) == 0 {
		return false
	}
	nFrames := toc.FrameSize / 960
	if nFrames < 2 || nFrames > 3 {
		return false
	}
	flags, ok := decodeMonoLBRRFlagsForTest(first, nFrames)
	if !ok || flags[0] != 0 {
		return false
	}
	for i := 1; i < nFrames; i++ {
		if flags[i] != 0 {
			return true
		}
	}
	return false
}

// silk_LBRR_flags_iCDF for 2- and 3-frame packets (silk/tables_other.c). Used
// to replay the LBRR_flags symbol decode for the partial-LBRR test gate.
var lbrrFlagsICDFForTest = map[int][]uint8{
	2: {203, 150, 0},
	3: {215, 195, 166, 125, 110, 82, 0},
}

// decodeMonoLBRRFlagsForTest replays the libopus mono VAD + LBRR flag decode on a
// first-frame SILK payload and returns the per-sub-frame LBRR flags. Mirrors
// silk/dec_API.c: nFrames VAD bits, one LBRR-present bit, then (if present) the
// LBRR_flags symbol = ec_dec_icdf(silk_LBRR_flags_iCDF_ptr[nFrames-2]) + 1.
func decodeMonoLBRRFlagsForTest(firstFrame []byte, nFrames int) ([3]int, bool) {
	var out [3]int
	icdf, ok := lbrrFlagsICDFForTest[nFrames]
	if !ok || len(firstFrame) == 0 {
		return out, false
	}
	var rd rangecoding.Decoder
	rd.Init(firstFrame)
	for range nFrames {
		_ = rd.DecodeBit(1) // VAD flag
	}
	lbrr := rd.DecodeBit(1) // LBRR-present flag
	if lbrr == 0 {
		return out, true
	}
	symbol := rd.DecodeICDF8Unchecked(icdf) + 1
	for i := range nFrames {
		out[i] = (symbol >> uint(i)) & 1
	}
	return out, true
}

func TestDecodeWithFECMonoFirstPacketLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 1
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	for _, sampleRate := range []int{16000, 48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			// Libopus reference: seed → FEC → normal.
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
				{packet: recoveryPacket},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "mono first-packet LBRR reference", err)
			}

			// gopus.
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			got := make([]float32, 0, len(want))

			if n, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.DecodeWithFEC(recoveryPacket, buf, true); err != nil {
				t.Fatalf("DecodeWithFEC recovery: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.Decode(recoveryPacket, buf); err != nil {
				t.Fatalf("Decode recovery packet: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels,
				"mono first-packet LBRR FEC decode")
			assertAPIRateFloat32BitsExact(t, got, want, "mono first-packet LBRR FEC decode")
		})
	}
}

// TestDecodeWithFECStereoWarmLBRRMatchesLibopus checks that a stereo SILK WB
// packet carrying LBRR (both mid and side channels) is correctly recovered by
// DecodeWithFEC, matching the libopus oracle. This is the "stereo warm" case:
// the encoder has processed enough frames to warm up the stereo predictor and
// LBRR gain state for both channels.
func TestDecodeWithFECStereoWarmLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 2
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	for _, sampleRate := range []int{16000, 48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
				{packet: recoveryPacket},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo warm LBRR reference", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			got := make([]float32, 0, len(want))

			if n, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.DecodeWithFEC(recoveryPacket, buf, true); err != nil {
				t.Fatalf("DecodeWithFEC recovery: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.Decode(recoveryPacket, buf); err != nil {
				t.Fatalf("Decode recovery packet: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels,
				"stereo warm LBRR FEC decode")
			assertAPIRateFloat32BitsExact(t, got, want, "stereo warm LBRR FEC decode")
		})
	}
}

// TestDecodeWithFECMonoFirstPacketNoLBRRFallbackMatchesPLC verifies that when
// a mono packet carries no LBRR data, DecodeWithFEC(packet, true) falls back to
// the same PLC output as Decode(nil, buf). This ensures the LBRR gate is correct
// and there is no spurious FEC decoding on non-LBRR packets.
func TestDecodeWithFECMonoFirstPacketNoLBRRFallbackMatchesPLC(t *testing.T) {
	const (
		channels   = 1
		frameSize  = 960
		sampleRate = 48000
	)

	// Encode a SILK mono packet without FEC.
	seedPacket := encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)
	if packetHasInBandFEC(t, seedPacket) {
		t.Fatal("FEC-disabled SILK packet should not carry LBRR")
	}

	// Decoder A: Decode seed then Decode(nil) → PLC path.
	decPLC, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder PLC: %v", err)
	}
	bufSeed := make([]float32, frameSize*channels)
	if _, err := decPLC.Decode(seedPacket, bufSeed); err != nil {
		t.Fatalf("decPLC Decode seed: %v", err)
	}
	wantBuf := make([]float32, frameSize*channels)
	nWant, err := decPLC.Decode(nil, wantBuf)
	if err != nil {
		t.Fatalf("decPLC Decode(nil): %v", err)
	}

	// Decoder B: Decode seed then DecodeWithFEC(seed, true) on a packet without LBRR.
	decFEC, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder FEC: %v", err)
	}
	if _, err := decFEC.Decode(seedPacket, bufSeed); err != nil {
		t.Fatalf("decFEC Decode seed: %v", err)
	}
	gotBuf := make([]float32, frameSize*channels)
	nGot, err := decFEC.DecodeWithFEC(seedPacket, gotBuf, true)
	if err != nil {
		t.Fatalf("decFEC DecodeWithFEC(no-LBRR): %v", err)
	}

	if nWant != nGot {
		t.Fatalf("sample count mismatch: PLC=%d FEC-fallback=%d", nWant, nGot)
	}
	for i := 0; i < nWant*channels; i++ {
		if wantBuf[i] != gotBuf[i] {
			t.Fatalf("FEC fallback diverged from PLC at sample %d: got=%v want=%v",
				i, gotBuf[i], wantBuf[i])
		}
	}
}

// TestDecodeWithFECStereoWarmLBRRNonSilent verifies (without oracle) that stereo
// LBRR recovery produces a non-silent output, confirming the FEC decoder path
// activates and produces audible signal.
func TestDecodeWithFECStereoWarmLBRRNonSilent(t *testing.T) {
	const (
		channels  = 2
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	if _, err := dec.Decode(seedPacket, buf); err != nil {
		t.Fatalf("Decode seed: %v", err)
	}

	recovered := make([]float32, frameSize*channels)
	n, err := dec.DecodeWithFEC(recoveryPacket, recovered, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC: %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeWithFEC samples=%d want %d", n, frameSize)
	}

	e := computeEnergyFloat32(recovered[:n*channels])
	if e < 1e-8 {
		t.Fatalf("stereo LBRR recovery energy=%.2e, expected non-silent FEC output", e)
	}
}

// TestDecodeWithFECMonoFirstPacketByteExact asserts that the encoder-side mono
// first-packet LBRR bytes (packet index 0, before any LBRR carry-over) are
// identical between gopus and libopus. This is the encoding side of the
// mono-first-packet case. The check is oracle-gated.
func TestDecodeWithFECMonoFirstPacketByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		channels  = 1
		frameSize = 960
	)

	// Build a single-frame mono LBRR-FEC packet pair from both gopus and libopus.
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet missing LBRR")
	}

	// Decode the LBRR-recovered frame and compare gopus vs libopus output.
	for _, sampleRate := range []int{48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "mono first-packet byte-exact reference", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			if _, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			}
			fecBuf := make([]float32, fs*channels)
			if _, err := dec.DecodeWithFEC(recoveryPacket, fecBuf, true); err != nil {
				t.Fatalf("DecodeWithFEC: %v", err)
			}

			wantFEC := want[fs*channels:]
			assertAPIRateQualityFloat32(t, fecBuf, wantFEC, sampleRate, channels,
				"mono first-packet LBRR byte-exact")
			assertAPIRateFloat32BitsExact(t, fecBuf, wantFEC, "mono first-packet LBRR byte-exact")
		})
	}
}

// TestDecodeWithFECStereoHybridAfterLongLossRangeExact pins the final
// range-coder state on a stereo Hybrid in-band FEC (decode_fec=1) recovery that
// follows a long packet-loss burst.
//
// In a Hybrid FEC step libopus decodes the SILK LBRR (lost_flag=2) and then runs
// the CELT decoder in PLC mode (celt_decode_with_ec(NULL); opus_decoder.c:606),
// reporting the CELT decoder's post-PLC st->rng as the frame's final range
// (opus_decoder.c:612, 697-700). celt_decode_lost advances that range-coder
// state (the noise LCG) on EVERY lost Hybrid frame, no matter how far the
// concealment energy has decayed. A long loss burst before the FEC step must not
// freeze that state: the hybrid PLC path must keep advancing the CELT rng even
// once its loss-fade is exhausted, or the FEC final range desyncs from libopus.
//
// This regression covers a fade-exhausted FEC recovery; the bit-exact per-step
// final-range assertion is the load-bearing entropy check (a desync here means
// gopus consumed a different number of bits than libopus on the FEC path).
func TestDecodeWithFECStereoHybridAfterLongLossRangeExact(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 480 // 10 ms
		bitrate    = 64000
		warmUp     = 4
		lossBurst  = 16 // long enough to exhaust the PLC loss-fade before recovery
	)

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, set := range []struct {
		name string
		fn   func() error
	}{
		{"SetMode", func() error { return enc.SetMode(EncoderModeHybrid) }},
		{"SetFrameSize", func() error { return enc.SetFrameSize(frameSize) }},
		{"SetExpertFrameDuration", func() error { return enc.SetExpertFrameDuration(ExpertFrameDuration10Ms) }},
		{"SetBandwidth", func() error { return enc.SetBandwidth(BandwidthFullband) }},
		{"SetBitrate", func() error { return enc.SetBitrate(bitrate * channels) }},
		{"SetSignal", func() error { return enc.SetSignal(SignalVoice) }},
		{"SetPacketLoss", func() error { return enc.SetPacketLoss(30) }},
		{"SetForceChannels", func() error { return enc.SetForceChannels(2) }},
	} {
		if err := set.fn(); err != nil {
			t.Fatalf("%s: %v", set.name, err)
		}
	}
	enc.SetFEC(true)

	// Encode a stateful stream and locate an LBRR-carrying recovery packet that
	// arrives after the loss burst (so SILK LBRR state is warm).
	const nFrames = 30
	packets := make([][]byte, 0, nFrames)
	recoveryIdx := -1
	for f := range nFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(f*frameSize+i) / sampleRate
			f0 := 180.0 * (1.0 + 0.02*math.Sin(2*math.Pi*3.0*tm))
			pcm[i*channels] = 0.40*float32(math.Sin(2*math.Pi*f0*tm)) +
				0.16*float32(math.Sin(2*math.Pi*2*f0*tm+0.21))
			pcm[i*channels+1] = 0.36*float32(math.Sin(2*math.Pi*f0*tm+0.13)) +
				0.14*float32(math.Sin(2*math.Pi*2*f0*tm+0.34))
		}
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil || len(pkt) == 0 {
			t.Fatalf("encode frame %d: %v len=%d", f, err, len(pkt))
		}
		if ParseTOC(pkt[0]).Mode != ModeHybrid {
			t.Fatalf("frame %d not hybrid", f)
		}
		packets = append(packets, append([]byte(nil), pkt...))
		if recoveryIdx < 0 && f > warmUp+lossBurst && packetHasInBandFEC(t, pkt) {
			recoveryIdx = f
		}
	}
	if recoveryIdx < 0 {
		t.Fatal("no warm LBRR-carrying hybrid recovery packet emitted")
	}

	// Decode plan: warm-up normal decodes, a long PLC burst (Decode(nil)) that
	// drives the loss-fade to zero, then the FEC recovery (decode_fec=1) of the
	// most recent lost frame, then a normal decode of the recovery packet. This
	// mirrors the opus_demo loss-recovery model for a long burst.
	type step struct {
		packet []byte
		fec    bool
	}
	plan := make([]step, 0, warmUp+lossBurst+2)
	for i := range warmUp {
		plan = append(plan, step{packet: packets[i]})
	}
	for range lossBurst {
		plan = append(plan, step{packet: nil}) // PLC
	}
	plan = append(plan, step{packet: packets[recoveryIdx], fec: true}) // FEC after fade exhaustion
	plan = append(plan, step{packet: packets[recoveryIdx]})            // normal decode

	oracleSteps := make([]libopusAPIRateDecodeStep, len(plan))
	for i, s := range plan {
		oracleSteps[i] = libopusAPIRateDecodeStep{packet: s.packet, fec: s.fec}
	}
	_, wantRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize, oracleSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid long-loss FEC range reference", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	fecStep := -1
	for i, s := range plan {
		var de error
		switch {
		case s.fec:
			fecStep = i
			_, de = dec.DecodeWithFEC(s.packet, buf, true)
		case s.packet == nil:
			_, de = dec.Decode(nil, buf)
		default:
			_, de = dec.Decode(s.packet, buf)
		}
		if de != nil {
			t.Fatalf("step %d (fec=%t plc=%t): %v", i, s.fec, s.packet == nil, de)
		}
		got := dec.FinalRange()
		if got != wantRanges[i] {
			t.Errorf("step %d final-range mismatch: gopus=0x%08x libopus=0x%08x (fec=%t plc=%t)",
				i, got, wantRanges[i], s.fec, s.packet == nil)
		}
	}
	if fecStep < 0 {
		t.Fatal("no FEC step in plan; test setup error")
	}
	// The FEC step must report a non-zero range (it decodes real LBRR/CELT-PLC
	// state, not a PLC sentinel) and must equal libopus exactly.
	if wantRanges[fecStep] == 0 {
		t.Fatalf("libopus reported zero final range on the FEC step %d; test setup error", fecStep)
	}
}

func TestDecodeWithFECMonoToStereoTransitionMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	monoPrime, err := hex.DecodeString("4882015d6c4724ea0378fb4d130329a3bb118c3dcf94d3ab4fb99572856213c0715e2741d0afda05128fa3b48962aa234661fc0c0bfff29c89c20353c28a7d31bc415a9d01542ceca42a3c54da9332d8490e84c2876d3367d4b1")
	if err != nil {
		t.Fatal(err)
	}
	stereoFEC, err := hex.DecodeString("4ca4d1b5615c6e332ec95d782f854c08750e1c9f0ba0fced4f8e882cb41e264e465928bf6a420a2f940502fe422a2276728c440eeb0a6078836098d456e63f9f7cee50a3ed7e4c184a22644e54c015eac5a1bd7048efd40ab9f65c4b7f80")
	if err != nil {
		t.Fatal(err)
	}
	if ParseTOC(monoPrime[0]).Stereo || !ParseTOC(stereoFEC[0]).Stereo {
		t.Fatalf("fixture channel modes: prime stereo=%t, FEC stereo=%t", ParseTOC(monoPrime[0]).Stereo, ParseTOC(stereoFEC[0]).Stereo)
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		frameSizes := []int{sampleRate / 50}
		if sampleRate == 48000 {
			frameSizes = append(frameSizes, 5760)
		}
		for _, frameSize := range frameSizes {
			t.Run(fmt.Sprintf("api_%dk/%d_samples", sampleRate/1000, frameSize), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
				if err != nil {
					t.Fatal(err)
				}
				primePCM := make([]float32, frameSize*2)
				fecPCM := make([]float32, frameSize*2)
				recoveryPCM := make([]float32, frameSize*2)
				primeSamples, err := dec.Decode(monoPrime, primePCM)
				if err != nil {
					t.Fatalf("Go prime: %v", err)
				}
				primeRange := dec.FinalRange()
				fecSamples, err := dec.DecodeWithFEC(stereoFEC, fecPCM, true)
				if err != nil {
					t.Fatalf("Go FEC: %v", err)
				}
				fecRange := dec.FinalRange()
				recoverySamples, err := dec.Decode(stereoFEC, recoveryPCM)
				if err != nil {
					t.Fatalf("Go recovery: %v", err)
				}
				recoveryRange := dec.FinalRange()
				want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, []libopustest.DecodeDiffCase{
					{Packet: monoPrime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
					{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: true},
					{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "decode diff probe", err)
					return
				}
				if len(want) != 3 {
					t.Fatalf("selected C returned %d records, want prime, FEC, and recovery", len(want))
				}
				actuals := []struct {
					name   string
					count  int
					range_ uint32
					pcm    []float32
				}{
					{name: "prime", count: primeSamples, range_: primeRange, pcm: primePCM},
					{name: "FEC", count: fecSamples, range_: fecRange, pcm: fecPCM},
					{name: "recovery", count: recoverySamples, range_: recoveryRange, pcm: recoveryPCM},
				}
				for i, actual := range actuals {
					if want[i].Code < 0 {
						t.Fatalf("selected C rejected %s: %d", actual.name, want[i].Code)
					}
					if actual.count != int(want[i].Code) {
						t.Fatalf("%s sample count Go=%d C=%d", actual.name, actual.count, want[i].Code)
					}
					if actual.range_ != want[i].FinalRange {
						t.Fatalf("%s final range Go=%08x C=%08x", actual.name, actual.range_, want[i].FinalRange)
					}
					actualLen := actual.count * 2
					if actualLen > len(actual.pcm) {
						t.Fatalf("%s Go PCM buffer length=%d, decoded samples require %d", actual.name, len(actual.pcm), actualLen)
					}
					actualPCM := actual.pcm[:actualLen]
					wantPCM := want[i].Float32()
					if len(actualPCM) != len(wantPCM) {
						t.Fatalf("%s PCM length Go=%d C=%d", actual.name, len(actualPCM), len(wantPCM))
					}
					if mismatch := fecRobustPCMFirstMismatch(actualPCM, wantPCM); mismatch >= 0 {
						t.Fatalf("%s PCM[%d] Go=%08x C=%08x", actual.name, mismatch, math.Float32bits(actualPCM[mismatch]), math.Float32bits(wantPCM[mismatch]))
					}
				}
			})
		}
	}

	t.Run("api_48k_after_loss", func(t *testing.T) {
		const sampleRate, frameSize = 48000, 960
		dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
		if err != nil {
			t.Fatal(err)
		}
		primePCM := make([]float32, frameSize*2)
		lossPCM := make([]float32, frameSize*2)
		fecPCM := make([]float32, frameSize*2)
		recoveryPCM := make([]float32, frameSize*2)
		primeSamples, err := dec.Decode(monoPrime, primePCM)
		if err != nil {
			t.Fatalf("Go prime: %v", err)
		}
		primeRange := dec.FinalRange()
		lossSamples, err := dec.Decode(nil, lossPCM)
		if err != nil {
			t.Fatalf("Go loss: %v", err)
		}
		lossRange := dec.FinalRange()
		fecSamples, err := dec.DecodeWithFEC(stereoFEC, fecPCM, true)
		if err != nil {
			t.Fatalf("Go FEC: %v", err)
		}
		fecRange := dec.FinalRange()
		recoverySamples, err := dec.Decode(stereoFEC, recoveryPCM)
		if err != nil {
			t.Fatalf("Go recovery: %v", err)
		}
		recoveryRange := dec.FinalRange()
		want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, []libopustest.DecodeDiffCase{
			{Packet: monoPrime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
			{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
			{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: true},
			{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "decode diff probe", err)
			return
		}
		actuals := []struct {
			name   string
			count  int
			range_ uint32
			pcm    []float32
		}{
			{name: "prime", count: primeSamples, range_: primeRange, pcm: primePCM},
			{name: "loss", count: lossSamples, range_: lossRange, pcm: lossPCM},
			{name: "FEC", count: fecSamples, range_: fecRange, pcm: fecPCM},
			{name: "recovery", count: recoverySamples, range_: recoveryRange, pcm: recoveryPCM},
		}
		if len(want) != len(actuals) {
			t.Fatalf("selected C returned %d records, want %d", len(want), len(actuals))
		}
		for i, actual := range actuals {
			if want[i].Code < 0 || actual.count != int(want[i].Code) {
				t.Fatalf("%s sample count Go=%d C=%d", actual.name, actual.count, want[i].Code)
			}
			if actual.range_ != want[i].FinalRange {
				t.Fatalf("%s final range Go=%08x C=%08x", actual.name, actual.range_, want[i].FinalRange)
			}
			actualLen := actual.count * 2
			if actualLen > len(actual.pcm) {
				t.Fatalf("%s Go PCM buffer length=%d, decoded samples require %d", actual.name, len(actual.pcm), actualLen)
			}
			actualPCM := actual.pcm[:actualLen]
			wantPCM := want[i].Float32()
			if len(actualPCM) != len(wantPCM) {
				t.Fatalf("%s PCM length Go=%d C=%d", actual.name, len(actualPCM), len(wantPCM))
			}
			if mismatch := fecRobustPCMFirstMismatch(actualPCM, wantPCM); mismatch >= 0 {
				t.Fatalf("%s PCM[%d] Go=%08x C=%08x", actual.name, mismatch, math.Float32bits(actualPCM[mismatch]), math.Float32bits(wantPCM[mismatch]))
			}
		}
	})

	// Warm the mono→stereo FEC transition and verify the complete recurring
	// mono packet → stereo FEC → stereo recovery sequence stays allocation-free.
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, 960*2)
	fecPCM := make([]float32, 960*2)
	recoveryPCM := make([]float32, 960*2)
	step := func() error {
		if _, err := dec.Decode(monoPrime, primePCM); err != nil {
			return err
		}
		if _, err := dec.DecodeWithFEC(stereoFEC, fecPCM, true); err != nil {
			return err
		}
		_, err := dec.Decode(stereoFEC, recoveryPCM)
		return err
	}
	for range 3 {
		if err := step(); err != nil {
			t.Fatalf("warm FEC transition: %v", err)
		}
	}
	var stepErr error
	allocs := testing.AllocsPerRun(50, func() {
		if err := step(); err != nil && stepErr == nil {
			stepErr = err
		}
	})
	if stepErr != nil {
		t.Fatalf("measured FEC transition: %v", stepErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed mono→stereo FEC/recovery allocs = %v, want 0", allocs)
	}
}

func TestDecodeWithFECPreviousCELTRedundancyFallbackMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, channels, packetFrameSize = 48000, 1, 960
	packets := encodePreviousRedundancyFECFallbackPackets(t)
	if ParseTOC(packets[0][0]).Mode != ModeSILK ||
		ParseTOC(packets[1][0]).Mode != ModeSILK ||
		ParseTOC(packets[2][0]).Mode != ModeHybrid ||
		ParseTOC(packets[3][0]).Mode != ModeCELT {
		t.Fatalf("packet modes=%v, want SILK/SILK/Hybrid/CELT", []Mode{
			ParseTOC(packets[0][0]).Mode, ParseTOC(packets[1][0]).Mode,
			ParseTOC(packets[2][0]).Mode, ParseTOC(packets[3][0]).Mode,
		})
	}

	type scenario struct {
		name             string
		fecPacketIndex   int
		requestedSamples int
	}
	scenarios := []scenario{
		{name: "CELT_packet_full_request", fecPacketIndex: 3, requestedSamples: packetFrameSize},
		{name: "CELT_packet_short_request", fecPacketIndex: 3, requestedSamples: packetFrameSize / 2},
		{name: "Hybrid_packet_short_request", fecPacketIndex: 2, requestedSamples: packetFrameSize / 2},
		{name: "SILK_packet_short_request", fecPacketIndex: 0, requestedSamples: packetFrameSize / 2},
	}
	formats := []struct {
		name string
		kind uint32
	}{
		{name: "float32_continuation", kind: libopustest.DecodeDiffFormatFloat32},
		{name: "int16_continuation", kind: libopustest.DecodeDiffFormatInt16},
		{name: "int24_continuation", kind: libopustest.DecodeDiffFormatInt24},
	}
	for _, format := range formats {
		format := format
		t.Run(format.name, func(t *testing.T) {
			for _, tc := range scenarios {
				t.Run(tc.name, func(t *testing.T) {
					steps := []libopustest.DecodeDiffCase{
						{Packet: packets[0], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[1], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[2], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[tc.fecPacketIndex], Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(tc.requestedSamples), DecodeFEC: true},
						{Format: format.kind, FrameSize: uint32(tc.requestedSamples)},
						{Packet: packets[tc.fecPacketIndex], Format: format.kind, FrameSize: packetFrameSize},
					}
					want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
					if err != nil {
						t.Fatalf("probe selected libopus sequence: %v", err)
					}
					if len(want) != len(steps) {
						t.Fatalf("selected libopus records=%d, want %d", len(want), len(steps))
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatal(err)
					}
					pcm32 := make([]float32, packetFrameSize*channels)
					pcm16 := make([]int16, packetFrameSize*channels)
					pcm24 := make([]int32, packetFrameSize*channels)
					for step := range steps {
						if step == 3 && (!dec.haveDecoded || dec.prevMode != ModeHybrid || !dec.prevRedundancy) {
							t.Fatalf("redundancy seed state haveDecoded=%v prevMode=%v prevRedundancy=%v; want decoded Hybrid with CELT redundancy", dec.haveDecoded, dec.prevMode, dec.prevRedundancy)
						}
						frameSize := int(steps[step].FrameSize)
						outputLength := frameSize * channels
						var n int
						var decodeErr error
						switch step {
						case 3:
							n, decodeErr = dec.DecodeWithFEC(packets[tc.fecPacketIndex], pcm32[:outputLength], true)
						case 4:
							switch format.kind {
							case libopustest.DecodeDiffFormatFloat32:
								n, decodeErr = dec.Decode(nil, pcm32[:outputLength])
							case libopustest.DecodeDiffFormatInt16:
								n, decodeErr = dec.DecodeInt16(nil, pcm16[:outputLength])
							default:
								n, decodeErr = dec.DecodeInt24(nil, pcm24[:outputLength])
							}
						default:
							packetIndex := step
							if step == 5 {
								packetIndex = tc.fecPacketIndex
							}
							switch format.kind {
							case libopustest.DecodeDiffFormatFloat32:
								n, decodeErr = dec.Decode(packets[packetIndex], pcm32[:outputLength])
							case libopustest.DecodeDiffFormatInt16:
								n, decodeErr = dec.DecodeInt16(packets[packetIndex], pcm16[:outputLength])
							default:
								n, decodeErr = dec.DecodeInt24(packets[packetIndex], pcm24[:outputLength])
							}
						}
						if decodeErr != nil || n != int(want[step].Code) {
							t.Fatalf("step%d decode=(%d,%v), selected C=%d", step, n, decodeErr, want[step].Code)
						}
						if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
							t.Fatalf("step%d FinalRange=%08x selected C=%08x", step, got, expected)
						}
						if step == 3 || format.kind == libopustest.DecodeDiffFormatFloat32 {
							wantPCM := want[step].Float32()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if gotBits, wantBits := math.Float32bits(pcm32[i]), math.Float32bits(wantPCM[i]); gotBits != wantBits {
									t.Fatalf("step%d PCM[%d]=%08x selected C=%08x", step, i, gotBits, wantBits)
								}
							}
						} else if format.kind == libopustest.DecodeDiffFormatInt16 {
							wantPCM := want[step].Int16()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if pcm16[i] != wantPCM[i] {
									t.Fatalf("step%d PCM[%d]=%d selected C=%d", step, i, pcm16[i], wantPCM[i])
								}
							}
						} else {
							wantPCM := want[step].Int24()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if pcm24[i] != wantPCM[i] {
									t.Fatalf("step%d PCM[%d]=%d selected C=%d", step, i, pcm24[i], wantPCM[i])
								}
							}
						}
						if step == 3 && (dec.prevMode != ModeCELT || dec.prevRedundancy) {
							t.Fatalf("post-fallback state prevMode=%v prevRedundancy=%v, want CELT with redundancy consumed", dec.prevMode, dec.prevRedundancy)
						}
					}
				})
			}
		})
	}

	// Repeat the real history on one decoder so the new fallback remains
	// allocation-free after warmup without adding resets to the measured path.
	allocDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	allocPCM := make([]float32, packetFrameSize*channels)
	decodeHistoryAndFallback := func() error {
		for _, packet := range packets[:3] {
			if _, err := allocDec.Decode(packet, allocPCM); err != nil {
				return err
			}
		}
		_, err := allocDec.DecodeWithFEC(packets[3], allocPCM, true)
		return err
	}
	for range 3 {
		if err := decodeHistoryAndFallback(); err != nil {
			t.Fatalf("warm fallback history: %v", err)
		}
	}
	var allocErr error
	if allocs := testing.AllocsPerRun(20, func() {
		if err := decodeHistoryAndFallback(); err != nil && allocErr == nil {
			allocErr = err
		}
	}); allocs != 0 {
		t.Fatalf("warmed previous-redundancy FEC fallback allocations=%g, want zero", allocs)
	}
	if allocErr != nil {
		t.Fatalf("measured fallback history: %v", allocErr)
	}
}

func encodePreviousRedundancyFECFallbackPackets(t *testing.T) [][]byte {
	t.Helper()
	libopustest.RequireOracle(t)
	const sampleRate, channels, frameSize, frameCount = 48000, 1, 960, 4
	pcm := make([]int16, frameSize*channels*frameCount)
	for frame := range frameCount {
		for i := range frameSize {
			tm := float64(frame*frameSize+i) / sampleRate
			v := 0.25*math.Sin(2*math.Pi*220*tm) + 0.08*math.Sin(2*math.Pi*1700*tm)
			pcm[frame*frameSize+i] = int16(v * 32767)
		}
	}
	frames := []libopustest.OpusEncodeFixedMixedFrame{
		{ShortPCM: pcm[:frameSize], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[frameSize : 2*frameSize], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[2*frameSize : 3*frameSize], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband},
		{ShortPCM: pcm[3*frameSize:], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband},
	}
	records, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
		Bitrate: 128000, Complexity: 10, ForceChannels: channels, LSBDepth: 16,
		FrameSize: frameSize, FrameCount: frameCount, PCM: pcm,
	}, frames)
	if err != nil {
		t.Fatalf("encode selected libopus sequence: %v", err)
	}
	if len(records) != frameCount {
		t.Fatalf("selected libopus packets=%d, want %d", len(records), frameCount)
	}
	packets := make([][]byte, frameCount)
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) < 2 {
			t.Fatalf("selected libopus frame%d status=%d packetBytes=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	return packets
}

const fecRateChangePrimeHex = "7ca8f77c8cc3f35deee4276d31f766a6229b39420e4de1833ada01f092a99b77115b86294824303ab15623a03c8cb633a6210ad53be7541daceee61a5ca26b9e73726d9eab35e36b8d0960b4d9984e910321eba71767fc055ab49334a8fca13a35cc93f18488a94edec051a9d65617d5b486f80d0f26b377101e08b722ea782993bc1c7c9badefaee9e2668abf010a2430a682dd9461349665d541ce01537c857474ca9c648de04280af15a16cbf0f50a1524875df7d022e085449f28fe60a04414d73c103f6ce3f63566e4c3ef0ed5bd2360d4f434b7e153d438affbe7a65110fb46f8c437df2c86e56571638aaee78110479e9c9834d08afbbb00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002d3f56e3bf44d8468cd0d91644b70cf6e04ec90f65e0ef6ba4c5ad6617169954bc744e65f2085fef6992a66ddb44142892"
const fecRateChangePacketHex = "219ecc3c1abc39107aac7ce5ce18793519666ce6d2503fd70b3adf02a51812d57da03d547d792672cc9f7f808d0be1b4cb109a8ff8a5c3d7442690fb306450df69e14aa80d030a7671a2c2da3496d21c540e38a611329fcf9e364a4a50bf8e0b97187cd5c4e27a5e6d1420e967fa3dedcf2c0c9ebb89002f9ead280000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000036749914e1499436820b31dda2decbc62a4a7cefdd97a77596e8b3157b56c7182e3b8fa6bd1487f550f61fa22e0512fd2fd5641acfae94416cfc213656ba83d23cb0a3da8bbeec13ee1fd52bf0234c173baba139c99e8dec9f5df96dcb21c68073d31d3056bbf06a651845f7aa47b3bcee2f6f3bdbd242873470de6cda4a0605f7abcff78fcb09a158aab56423d075d6f55028996d9533513151df8592b280dac28205816e8488d94b5d7cc90615a4530698cae50a3103df86d3290c40012d528fc0822827f81a5cbec3bae89183a1bbbaeb7e92548400f8175afb855026afc6e99594504d6eb31c3534f6c35b87d1018c9ea5f770503f20ab0e952059b294a8ed4d9e59817703c96683cff1eec8be4206f25706a463f268586a6c4e4d2dd15387c95a5da23022bbb472ef4ff9d8dd2f3e7b13a5825bea03d1291644ae6b8830462124a501e32490a5cea2e09dadfed26215a9a0338511a5230231aac06e4406daa8acf7a72db28f2d675220ff1cdb3860e40c72741ab481f103e0fdfaeee27f3019a63626b03d73724783707a98998b26c3fdc9"

func decodeFECTransitionFixture(t *testing.T) (prime, packet []byte) {
	t.Helper()
	var err error
	prime, err = hex.DecodeString(fecRateChangePrimeHex)
	if err != nil {
		t.Fatal(err)
	}
	packet, err = hex.DecodeString(fecRateChangePacketHex)
	if err != nil {
		t.Fatal(err)
	}
	return prime, packet
}

func TestDecodeWithFECSILKPLCResetsOnRateChange(t *testing.T) {
	libopustest.RequireOracle(t)
	prime, basePacket := decodeFECTransitionFixture(t)
	if got := ParseTOC(prime[0]).Mode; got != ModeHybrid {
		t.Fatalf("prime mode=%v, want Hybrid", got)
	}

	for _, tc := range []struct {
		name string
		toc  byte
	}{
		{name: "narrowband", toc: 0x01},
		{name: "mediumband", toc: 0x21},
		{name: "wideband", toc: 0x41},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recovery := append([]byte(nil), basePacket...)
			recovery[0] = tc.toc
			if got := ParseTOC(recovery[0]).Mode; got != ModeSILK {
				t.Fatalf("recovery mode=%v, want SILK", got)
			}
			if PacketHasLBRR(recovery) {
				t.Fatal("fixture recovery packet unexpectedly carries LBRR")
			}

			for _, requested := range []int{480, 5760} {
				t.Run(fmt.Sprintf("request_%d", requested), func(t *testing.T) {
					want, err := libopustest.ProbeDecodeSequence(48000, 1, []libopustest.DecodeDiffCase{
						{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 5760},
						{Packet: recovery, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(requested), DecodeFEC: true},
					})
					if err != nil {
						t.Fatalf("libopus sequence probe: %v", err)
					}
					if len(want) != 2 || want[0].Code != 960 || want[1].Code != int32(requested) {
						t.Fatalf("libopus counts=%v, want [960 %d]", decodeFECTransitionCounts(want), requested)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
					if err != nil {
						t.Fatal(err)
					}
					primePCM := make([]float32, 5760)
					if n, err := dec.Decode(prime, primePCM); err != nil || n != 960 {
						t.Fatalf("prime decode=(%d,%v), want 960", n, err)
					}
					assertFECTransitionPCM(t, "prime", primePCM[:960], want[0].Float32())
					if got := dec.FinalRange(); got != want[0].FinalRange {
						t.Fatalf("prime range=%08x want=%08x", got, want[0].FinalRange)
					}

					fecPCM := make([]float32, requested)
					if n, err := dec.DecodeWithFEC(recovery, fecPCM, true); err != nil || n != requested {
						t.Fatalf("FEC decode=(%d,%v), want %d", n, err, requested)
					}
					assertFECTransitionPCM(t, "FEC", fecPCM, want[1].Float32())
					if got := dec.FinalRange(); got != want[1].FinalRange {
						t.Fatalf("FEC range=%08x want=%08x", got, want[1].FinalRange)
					}
				})
			}
		})
	}
}

func TestDecodeWithFECSILKPLCResetsOnRateChangeWarmZeroAllocs(t *testing.T) {
	prime, basePacket := decodeFECTransitionFixture(t)
	for _, tc := range []struct {
		name string
		toc  byte
	}{
		{name: "narrowband", toc: 0x01},
		{name: "mediumband", toc: 0x21},
		{name: "wideband", toc: 0x41},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recovery := append([]byte(nil), basePacket...)
			recovery[0] = tc.toc
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatal(err)
			}
			primePCM := make([]float32, 5760)
			fecPCM := make([]float32, 5760)
			decodeSequence := func() {
				dec.Reset()
				if n, err := dec.Decode(prime, primePCM); err != nil || n != 960 {
					t.Fatalf("prime decode=(%d,%v)", n, err)
				}
				if n, err := dec.DecodeWithFEC(recovery, fecPCM, true); err != nil || n != 5760 {
					t.Fatalf("FEC decode=(%d,%v)", n, err)
				}
			}
			for range 3 {
				decodeSequence()
			}
			if allocs := testing.AllocsPerRun(20, decodeSequence); allocs != 0 {
				t.Fatalf("warm Hybrid-to-%s FEC allocations=%g, want zero", tc.name, allocs)
			}
		})
	}
}

func assertFECTransitionPCM(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM length=%d want=%d", label, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s PCM[%d]=%08x want=%08x", label, i, gotBits, wantBits)
		}
	}
}

func decodeFECTransitionCounts(results []libopustest.DecodeDiffResult) []int32 {
	counts := make([]int32, len(results))
	for i := range results {
		counts[i] = results[i].Code
	}
	return counts
}

func TestDecoderPitchAfterSILKRateResetMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	prime, packet := decodeFECTransitionFixture(t)
	packet[0] = 0x01 // NB SILK; the fixture has no LBRR.
	if PacketHasLBRR(packet) {
		t.Fatal("rate-reset packet unexpectedly carries LBRR")
	}

	const sampleRate, channels, frameSize = 48000, 1, 5760
	cases := []libopustest.DecodeDiffCase{
		{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize, DecodeFEC: true},
	}
	want, err := probeSelectedLibopusPitch(t, sampleRate, channels, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder pitch sequence probe", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("selected C returned %d pitch records, want %d", len(want), len(cases))
	}
	if want[1].decoded != frameSize || want[1].pitch != 0 {
		t.Fatalf("selected C rate-reset FEC=(samples=%d,pitch=%d), want (%d,0)", want[1].decoded, want[1].pitch, frameSize)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, frameSize)
	fecPCM := make([]float32, frameSize)
	primeSamples, err := dec.Decode(prime, primePCM)
	if err != nil {
		t.Fatalf("prime decode: %v", err)
	}
	if primeSamples != int(want[0].decoded) {
		t.Fatalf("prime samples=%d C=%d", primeSamples, want[0].decoded)
	}
	if got := dec.Pitch(); got != int(want[0].pitch) {
		t.Fatalf("prime pitch=%d C=%d", got, want[0].pitch)
	}

	fecSamples, err := dec.DecodeWithFEC(packet, fecPCM, true)
	if err != nil {
		t.Fatalf("rate-reset FEC decode: %v", err)
	}
	if fecSamples != int(want[1].decoded) {
		t.Fatalf("rate-reset FEC samples=%d C=%d", fecSamples, want[1].decoded)
	}
	if got := dec.Pitch(); got != int(want[1].pitch) {
		t.Fatalf("rate-reset FEC pitch=%d C=%d", got, want[1].pitch)
	}
	if got := dec.silkDecoder.GetLastSignalType(); got != 0 {
		t.Fatalf("SILK last signal type after rate-reset PLC=%d, want inactive", got)
	}
}

type selectedLibopusPitchStep struct {
	decoded int32
	pitch   int32
}

func probeSelectedLibopusPitch(t *testing.T, sampleRate, channels int, cases []libopustest.DecodeDiffCase) ([]selectedLibopusPitchStep, error) {
	t.Helper()
	bin, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:      "decoder pitch sequence probe",
		OutputBase: "gopus_decoder_pitch_sequence",
		SourceFile: "libopus_decoder_pitch_sequence.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
	})
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GPI1", 1,
		uint32(sampleRate), uint32(channels), uint32(len(cases)))
	for _, step := range cases {
		decodeFEC := uint32(0)
		if step.DecodeFEC {
			decodeFEC = 1
		}
		payload.U32(step.FrameSize)
		payload.U32(decodeFEC)
		payload.U32(uint32(len(step.Packet)))
		payload.Raw(step.Packet)
	}
	wire, err := libopustest.RunHelper(bin, payload.Bytes())
	if err != nil {
		return nil, err
	}
	r, err := libopustest.NewOracleReader("decoder pitch sequence probe", "GPO1", wire)
	if err != nil {
		return nil, err
	}
	count := r.Count(len(cases))
	out := make([]selectedLibopusPitchStep, count)
	for i := range out {
		out[i].decoded = r.I32()
		if ctl := r.I32(); ctl != 0 {
			return nil, fmt.Errorf("selected C OPUS_GET_PITCH step %d returned %d", i, ctl)
		}
		out[i].pitch = r.I32()
	}
	if err := r.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestDecodeWithFECRateSwitchRecoveryAndLossMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name     string
		channels int
		prime    string
		fec      string
	}{
		{
			name:     "api-mono-stereo-wb-to-mb",
			channels: 1,
			prime:    "54d9346d58758b87d1bbdb39e481b0cdf58959ea38e20f1255003666bb4633195ac9bd2f52ab993093f3d6a120f4a95b13b0163db9c0c3e21f8df1f4931a487ecb827357889078eea89ef7153b012ab77fde135545bbb90e58afbf954b78aa0edd9f4de4c08cef748c8873b86c33787710dfa3af84f3eb72bbb3e6700d1468896caf3a1e475ff74451cb22580a3c189f092391240c7160421137e84a80effbb35c2e0f817a",
			fec:      "34a8f77ca6242c85d1dfe89250f7bcd05a152957cab69d04b1d02d2dfdcd23b79593e89ff61d1dcedaa4fb8ad2155e16052b21c563cc828769374b9334e79f124655123c13ca005d9c048c566b1cc473ccef7467dda10aa8d9612fd230288c14f3f1106a98d19a1b22c89b867b7027648f65e0ef6ba4cdcd2320bbd578e89ccbe410ebf9b12176d174ac92",
		},
		{
			name:     "api-stereo-stereo-wb-to-nb",
			channels: 2,
			prime:    "5cee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80",
			fec:      "14ee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prime, err := hex.DecodeString(tc.prime)
			if err != nil {
				t.Fatal(err)
			}
			fec, err := hex.DecodeString(tc.fec)
			if err != nil {
				t.Fatal(err)
			}
			const sampleRate = 48000
			fecSamples, err := packetSamplesAtRate(fec, sampleRate)
			if err != nil {
				t.Fatal(err)
			}
			want, err := libopustest.ProbeDecodeSequence(sampleRate, tc.channels, []libopustest.DecodeDiffCase{
				{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 5760},
				{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(fecSamples), DecodeFEC: true},
				{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(fecSamples)},
				{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(fecSamples)},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "decode sequence probe", err)
			}
			if len(want) != 4 {
				t.Fatalf("selected C returned %d sequence records, want 4", len(want))
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.channels))
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, 5760*tc.channels)
			assertStep := func(label string, idx int, n int, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s decode: %v", label, err)
				}
				if want[idx].Code != int32(n) {
					t.Fatalf("%s samples=%d, C=%d", label, n, want[idx].Code)
				}
				assertFECTransitionPCM(t, label, pcm[:n*tc.channels], want[idx].Float32())
				if got := dec.FinalRange(); got != want[idx].FinalRange {
					t.Fatalf("%s final range=%08x, C=%08x", label, got, want[idx].FinalRange)
				}
			}

			primeN, err := dec.Decode(prime, pcm)
			assertStep("prime", 0, primeN, err)
			fecN, err := dec.DecodeWithFEC(fec, pcm[:fecSamples*tc.channels], true)
			assertStep("FEC", 1, fecN, err)
			recoveryN, err := dec.Decode(fec, pcm[:fecSamples*tc.channels])
			assertStep("normal recovery", 2, recoveryN, err)
			lossN, err := dec.Decode(nil, pcm[:fecSamples*tc.channels])
			assertStep("following PLC", 3, lossN, err)
		})
	}
}

func TestDecodeWithFECSideResetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	data, err := os.ReadFile("testdata/fec_side_reset_packets.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name       string   `json:"name"`
			SampleRate int      `json:"sample_rate"`
			Channels   int      `json:"channels"`
			FrameSize  int      `json:"frame_size"`
			Packets    []string `json:"packets"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 2 {
		t.Fatalf("packet histories=%d want=2", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if len(tc.Packets) < 3 {
				t.Fatal("missing decoder warmup history")
			}
			steps := make([]libopusAPIRateDecodeStep, len(tc.Packets))
			for i, encoded := range tc.Packets {
				packet, err := hex.DecodeString(encoded)
				if err != nil {
					t.Fatalf("packet %d: %v", i, err)
				}
				steps[i] = libopusAPIRateDecodeStep{packet: packet, fec: i == len(steps)-1}
			}
			if !packetHasInBandFEC(t, steps[len(steps)-1].packet) {
				t.Fatal("recovery packet does not carry LBRR")
			}
			want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(tc.SampleRate, tc.Channels, tc.FrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "side reset FEC reference", err)
			}
			stride := tc.FrameSize * tc.Channels
			if len(want) != len(steps)*stride || len(ranges) != len(steps) {
				t.Fatalf("reference samples/ranges=%d/%d want=%d/%d", len(want), len(ranges), len(steps)*stride, len(steps))
			}
			dec, err := NewDecoder(DefaultDecoderConfig(tc.SampleRate, tc.Channels))
			if err != nil {
				t.Fatal(err)
			}
			out := make([]float32, stride)
			for i, step := range steps {
				n, err := dec.DecodeWithFEC(step.packet, out, step.fec)
				if err != nil || n != tc.FrameSize {
					t.Fatalf("step %d fec=%v: samples=%d want=%d err=%v", i, step.fec, n, tc.FrameSize, err)
				}
				if dec.FinalRange() != ranges[i] {
					t.Fatalf("step %d fec=%v: range=%08x want=%08x", i, step.fec, dec.FinalRange(), ranges[i])
				}
				for j, sample := range out {
					gotBits, wantBits := math.Float32bits(sample), math.Float32bits(want[i*stride+j])
					if gotBits != wantBits {
						t.Fatalf("step %d fec=%v sample %d: PCM=%08x want=%08x", i, step.fec, j, gotBits, wantBits)
					}
				}
			}
		})
	}
}

func TestDecodeWithFECStereoToMonoTransitionMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	prime := mustDecodeFECTransitionPacket(t, "5cee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80")
	fec := mustDecodeFECTransitionPacket(t, "70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13e023a92")
	if !ParseTOC(prime[0]).Stereo || ParseTOC(fec[0]).Stereo {
		t.Fatalf("fixture channel modes: prime stereo=%t, FEC stereo=%t", ParseTOC(prime[0]).Stereo, ParseTOC(fec[0]).Stereo)
	}

	const sampleRate, channels, frameSize = 48000, 2, 5760
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
		{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize, DecodeFEC: true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}
	if len(want) != 2 {
		t.Fatalf("selected C returned %d records, want prime and FEC", len(want))
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, frameSize*channels)
	fecPCM := make([]float32, frameSize*channels)
	primeSamples, err := dec.Decode(prime, primePCM)
	if err != nil {
		t.Fatalf("prime decode: %v", err)
	}
	if want[0].Code < 0 || primeSamples != int(want[0].Code) {
		t.Fatalf("prime samples=%d C=%d", primeSamples, want[0].Code)
	}
	if got, wantRange := dec.FinalRange(), want[0].FinalRange; got != wantRange {
		t.Fatalf("prime range=%08x C=%08x", got, wantRange)
	}
	assertDecodeFECTransitionPCM(t, "prime", primePCM[:primeSamples*channels], want[0].Float32())

	fecSamples, err := dec.DecodeWithFEC(fec, fecPCM, true)
	if err != nil {
		t.Fatalf("FEC decode: %v", err)
	}
	if want[1].Code < 0 || fecSamples != int(want[1].Code) {
		t.Fatalf("FEC samples=%d C=%d", fecSamples, want[1].Code)
	}
	if got, wantRange := dec.FinalRange(), want[1].FinalRange; got != wantRange {
		t.Fatalf("FEC range=%08x C=%08x", got, wantRange)
	}
	assertDecodeFECTransitionPCM(t, "FEC", fecPCM[:fecSamples*channels], want[1].Float32())

	// Keep the public prime→FEC sequence allocation-free after setup and warmup.
	step := func() error {
		if _, err := dec.Decode(prime, primePCM); err != nil {
			return err
		}
		_, err := dec.DecodeWithFEC(fec, fecPCM, true)
		return err
	}
	for range 3 {
		if err := step(); err != nil {
			t.Fatalf("warm FEC transition: %v", err)
		}
	}
	var stepErr error
	allocs := testing.AllocsPerRun(50, func() {
		if err := step(); err != nil && stepErr == nil {
			stepErr = err
		}
	})
	if stepErr != nil {
		t.Fatalf("measured FEC transition: %v", stepErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed stereo→mono FEC allocations=%g, want zero", allocs)
	}
}

func TestDecodeWithFECMonoToStereoLongFrameMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	prime := mustDecodeFECTransitionPacket(t, "70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13e023a92")
	fec := mustDecodeFECTransitionPacket(t, "54d9346d58758b87d1bbdb39e481b0cdf58959ea38e20f1255003666bb4633195ac9bd2f52ab993093f3d6a120f4a95b13b0163db9c0c3e21f8df1f4931a487ecb827357889078eea89ef7153b012ab77fde135545bbb90e58afbf954b78aa0edd9f4de4c08cef748c8873b86c33787710dfa3af84f3eb72bbb3e6700d1468896caf3a1e475ff74451cb22580a3c189f092391240c7160421137e84a80effbb35c2e0f817a")
	if ParseTOC(prime[0]).Stereo || !ParseTOC(fec[0]).Stereo {
		t.Fatalf("fixture channel modes: prime stereo=%t, FEC stereo=%t", ParseTOC(prime[0]).Stereo, ParseTOC(fec[0]).Stereo)
	}

	const sampleRate, channels, frameSize = 48000, 2, 5760
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
		{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize, DecodeFEC: true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, frameSize*channels)
	fecPCM := make([]float32, frameSize*channels)
	primeSamples, err := dec.Decode(prime, primePCM)
	if err != nil {
		t.Fatalf("prime decode: %v", err)
	}
	fecSamples, err := dec.DecodeWithFEC(fec, fecPCM, true)
	if err != nil {
		t.Fatalf("FEC decode: %v", err)
	}
	if len(want) != 2 || want[0].Code < 0 || want[1].Code < 0 {
		t.Fatalf("selected C returned invalid sequence records: %+v", want)
	}
	if primeSamples != int(want[0].Code) || fecSamples != int(want[1].Code) {
		t.Fatalf("sample counts Go=%d/%d C=%d/%d", primeSamples, fecSamples, want[0].Code, want[1].Code)
	}
	assertDecodeFECTransitionPCM(t, "prime", primePCM[:primeSamples*channels], want[0].Float32())
	assertDecodeFECTransitionPCM(t, "FEC", fecPCM[:fecSamples*channels], want[1].Float32())
}

func mustDecodeFECTransitionPacket(t *testing.T, encoded string) []byte {
	t.Helper()
	packet, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode packet fixture: %v", err)
	}
	return packet
}

func assertDecodeFECTransitionPCM(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM length=%d C=%d", label, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s PCM[%d]=%08x C=%08x", label, i, gotBits, wantBits)
		}
	}
}

func TestDecodeWithFECRejectsOver120msBeforeCELTPLCFallback(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Keep the FEC request itself valid (a multiple of the 2.5 ms quantum) so
	// this test reaches packet framing instead of returning OPUS_BAD_ARG first.
	pcm := make([]float32, 6960)
	_, err = dec.DecodeWithFEC([]byte{GenerateTOC(31, false, 3), 0x07}, pcm, true)
	if err != ErrInvalidPacket {
		t.Fatalf("DecodeWithFEC error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeWithFECEnforcesMaxPacketBytesWithoutChangingState(t *testing.T) {
	packet := encodeAPIRateSILKPacket(t, 1)
	if len(packet) < 2 {
		t.Fatalf("test packet length=%d, need a multi-byte packet", len(packet))
	}

	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketBytes = len(packet) - 1
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	fresh, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder fresh: %v", err)
	}
	pcm := make([]float32, 960)
	if n, err := dec.DecodeWithFEC(packet, pcm, true); n != 0 || err != ErrPacketTooLarge {
		t.Fatalf("DecodeWithFEC oversized packet=(%d,%v), want (0,%v)", n, err, ErrPacketTooLarge)
	}
	if dec.lastPacketMode != fresh.lastPacketMode || dec.lastBandwidth != fresh.lastBandwidth ||
		dec.lastPacketDuration != fresh.lastPacketDuration || dec.lastFrameSize != fresh.lastFrameSize ||
		dec.FinalRange() != fresh.FinalRange() || dec.hasFEC {
		t.Fatal("rejected oversized FEC packet changed decoder state")
	}

	// A one-byte code-0 SILK packet is an empty/DTX frame accepted under this
	// byte limit. Its PLC output must still match a fresh decoder after rejection.
	shortPacket := []byte{GenerateTOC(0, false, 0)}
	got := make([]float32, len(pcm))
	want := make([]float32, len(pcm))
	gotN, gotErr := dec.DecodeWithFEC(shortPacket, got, true)
	wantN, wantErr := fresh.DecodeWithFEC(shortPacket, want, true)
	if gotN != wantN || gotErr != wantErr {
		t.Fatalf("post-rejection DecodeWithFEC=(%d,%v), fresh=(%d,%v)", gotN, gotErr, wantN, wantErr)
	}
	if gotErr != nil {
		t.Fatalf("DecodeWithFEC(short packet): %v", gotErr)
	}
	for i := 0; i < gotN; i++ {
		if got[i] != want[i] {
			t.Fatalf("post-rejection PCM[%d]=%08x, fresh=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	if dec.FinalRange() != fresh.FinalRange() || dec.LastPacketDuration() != fresh.LastPacketDuration() {
		t.Fatal("post-rejection decoder history differs from fresh decoder")
	}
}

// TestDecodeWithFEC_FallbackToPLC verifies that DecodeWithFEC falls back to PLC
// when no FEC data is available (e.g., when no previous packet was decoded,
// or the previous packet was CELT-only mode).
func TestDecodeWithFEC_FallbackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Attempt FEC decode without any previous packet - should fall back to PLC
	frameSize := 960
	pcm := make([]float32, frameSize)

	// First FEC decode with no prior data - should use PLC and produce silence/zeros
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC fell back to PLC successfully, produced %d samples", n)
}

// TestDecodeWithFEC_CELTNoFEC verifies that CELT-only packets don't have FEC data.
// DecodeWithFEC should fall back to PLC after decoding a CELT packet.
func TestDecodeWithFEC_CELTNoFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a CELT packet (config 31 = CELT FB 20ms)
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the CELT packet normally
	_, err = dec.Decode(celtPacket, pcm)
	if err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// Check that no FEC data was stored (CELT doesn't have LBRR)
	if dec.hasFEC {
		t.Error("hasFEC should be false after CELT packet decode")
	}

	// Attempt FEC decode - should fall back to PLC since no FEC available
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC correctly fell back to PLC for CELT mode")
}

// TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC verifies that SILK packets only
// arm FEC state when their LBRR flag is present.
func TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a SILK packet (config 9 = SILK WB 20ms)
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the SILK packet normally
	_, err = dec.Decode(silkPacket, pcm)
	if err != nil {
		t.Fatalf("Decode SILK packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after SILK packet without LBRR")
	}

	t.Log("DecodeWithFEC correctly left FEC state clear for SILK without LBRR")
}

func TestStoreFECData_ReusesBackingBuffer(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	initialCap := cap(dec.fecData)
	if initialCap == 0 {
		t.Fatal("expected preallocated fecData backing buffer")
	}

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		Stereo:    false,
	}

	packetSmall := make([]byte, 32)
	packetLarge := make([]byte, 512)
	for i := range packetSmall {
		packetSmall[i] = byte(i)
	}
	for i := range packetLarge {
		packetLarge[i] = byte(255 - (i % 255))
	}
	packetSmall[0] |= 0x40
	packetLarge[0] |= 0x40

	dec.storeFECData(packetSmall, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after small packet: got %d want %d", cap(dec.fecData), initialCap)
	}

	dec.storeFECData(packetLarge, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after large packet: got %d want %d", cap(dec.fecData), initialCap)
	}
	if len(dec.fecData) != len(packetLarge) {
		t.Fatalf("fecData len = %d, want %d", len(dec.fecData), len(packetLarge))
	}
	if dec.fecData[0] != packetLarge[0] || dec.fecData[len(dec.fecData)-1] != packetLarge[len(packetLarge)-1] {
		t.Fatal("fecData content mismatch after copy")
	}
}

func TestStoreFECData_NoLBRRClearsState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		FrameSize: 960,
	}
	dec.storeFECData([]byte{0x00, 0x01}, toc, 1, 960)
	if dec.hasFEC {
		t.Fatal("storeFECData kept FEC armed for packet without LBRR")
	}
}

func TestDecodeFECFrame_BufferSizingUsesSingleFrame(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Simulate stored FEC metadata from a 2-frame packet. decode_fec should still
	// only require one frame of output buffer.
	dec.hasFEC = true
	dec.fecData = []byte{0x01, 0x02, 0x03, 0x04}
	dec.fecMode = ModeSILK
	dec.fecBandwidth = BandwidthWideband
	dec.fecStereo = false
	dec.fecFrameSize = 960
	dec.fecFrameCount = 2

	pcm := make([]float32, 960)
	_, err = dec.decodeFECFrame(pcm, 0)
	if err == ErrBufferTooSmall {
		t.Fatal("decodeFECFrame rejected single-frame output buffer for multi-frame packet metadata")
	}
}

// TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC verifies that normal
// decode skips LBRR instead of caching it for a later nil decode_fec call.
func TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Use the minimal hybrid test packet
	packet := minimalHybridTestPacket20ms()
	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the Hybrid packet normally
	_, err = dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode Hybrid packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after normal Hybrid decode")
	}
}

// TestDecodeWithFEC_Recovery tests FEC recovery flow.
// This test simulates packet loss and FEC recovery.
func TestDecodeWithFEC_Recovery(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Decode a series of SILK packets to build up state
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i * 3)
	}

	frameSize := 960
	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode packet 1
	_, err = dec.Decode(silkPacket, pcm1)
	if err != nil {
		t.Fatalf("Decode packet 1 error: %v", err)
	}

	// Simulate packet 2 is lost - use FEC to recover
	// In real usage, we'd use the NEXT packet's LBRR to recover the lost one.
	// For this test, we just verify DecodeWithFEC works without crashing.
	n, err := dec.DecodeWithFEC(nil, pcm2, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error: %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("FEC recovery produced %d samples", n)
}

func TestDecodeWithFEC_UsesProvidedPacketAndPreservesNormalDecode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost" in decode sequence
	packet2 := encodeFrame(makeFrame(1920))

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing packet using packet2's LBRR.
	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}

	// The same packet must still decode normally after FEC recovery.
	pcm2 := make([]float32, frameSize)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != frameSize {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=%d", n2, frameSize)
	}
}

func TestDecodeWithFEC_FrameSizeTransitionUsesProvidedPacketGranularity(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	makeFrame := func(frameSize int, phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*330*tm) + 0.2*math.Sin(2*math.Pi*660*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(frameSize int, phase float64) []byte {
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize(%d) error: %v", frameSize, err)
		}
		n, err := enc.Encode(makeFrame(frameSize, phase), pktBuf)
		if err != nil {
			t.Fatalf("Encode(%d) error: %v", frameSize, err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating frame-size transition packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(1920, 0)
	_ = encodeFrame(960, 1920) // packet 1 intentionally "lost"
	packet2 := encodeFrame(960, 2880)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, 1920)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing 20ms packet from packet2 LBRR while previous decode
	// state still reflects a 40ms frame. The output buffer length is the
	// caller's requested decode_fec frame size, matching libopus' frame_size.
	pcmFEC := make([]float32, 960)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != 960 {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=960", nFEC)
	}

	pcm2 := make([]float32, 960)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != 960 {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=960", n2)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketFallsBackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Seed last frame size/state with a normal decode.
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	pcm := make([]float32, 960)
	if _, err := dec.Decode(celtPacket, pcm); err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// CELT has no LBRR, so this should transparently fall back to PLC.
	n, err := dec.DecodeWithFEC(celtPacket, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if n != 960 {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) samples=%d want=%d", n, 960)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketClearsStoredFECState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960
	pcm := make([]float32, 960)

	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0)
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}
	if _, err := dec.DecodeWithFEC(celtPacket, pcm, true); err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if dec.hasFEC {
		t.Fatalf("hasFEC should be false after CELT-based decode_fec fallback")
	}
}

func TestDecodeWithFEC_ProvidedPacketUsesPacketModeForCELTGate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))
	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}
	if dec.lastPacketMode == ModeCELT {
		t.Fatalf("test setup invalid: first packet mode resolved to CELT")
	}

	// Simulate transient prevMode=CELLT (e.g., after redundancy/PLC path).
	// decode_fec gating should still use packet-mode state (lastPacketMode).
	dec.prevMode = ModeCELT

	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}
	if dec.prevMode != toc.Mode {
		t.Fatalf("prevMode=%v want %v (provided packet mode must override transient PLC mode)", dec.prevMode, toc.Mode)
	}
}

func TestDecodeWithFEC_ProvidedPacketWithoutLBRRUsesDirectPLCFallback(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(false)
	if err := enc.SetBitrate(12000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}

	const frameSize = 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.4*math.Sin(2*math.Pi*220*tm) + 0.1*math.Sin(2*math.Pi*440*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating no-LBRR test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))

	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}
	firstFrameData, err := extractFirstFramePayload(packet2, toc)
	if err != nil {
		t.Fatalf("extractFirstFramePayload(packet2) error: %v", err)
	}
	if packetHasLBRR(firstFrameData, toc) {
		t.Fatalf("test setup invalid: packet2 unexpectedly has LBRR")
	}

	decExpected, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(expected) error: %v", err)
	}
	decActual, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(actual) error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := decExpected.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (expected) error: %v", err)
	}
	if _, err := decActual.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (actual) error: %v", err)
	}

	pcmExpected := make([]float32, frameSize)
	nExpected, err := decExpected.decodePLCForFECWithState(pcmExpected, frameSize, plcDecodeState{
		packetFrameSize: frameSize,
		mode:            toc.Mode,
		bandwidth:       toc.Bandwidth,
		packetStereo:    toc.Stereo,
	})
	if err != nil {
		t.Fatalf("decodePLCForFECWithState(expected) error: %v", err)
	}
	if nExpected != frameSize {
		t.Fatalf("decodePLCForFECWithState(expected) samples=%d want=%d", nExpected, frameSize)
	}

	pcmActual := make([]float32, frameSize)
	nActual, err := decActual.DecodeWithFEC(packet2, pcmActual, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nActual != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nActual, frameSize)
	}

	const tol = 1e-7
	for i := range frameSize {
		if math.Abs(float64(pcmExpected[i]-pcmActual[i])) > tol {
			t.Fatalf("sample %d mismatch: expected=%v actual=%v", i, pcmExpected[i], pcmActual[i])
		}
	}
}

func TestDecodeWithFEC_PLCWithProvidedStateUsesProvidedMode(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	packet := minimalHybridTestPacket20ms()
	pcmPrime := make([]float32, frameSize)
	if _, err := dec.Decode(packet, pcmPrime); err != nil {
		t.Fatalf("Decode prime packet error: %v", err)
	}

	// Force decoder transient PLC mode to CELT and verify provided state wins.
	dec.prevMode = ModeCELT
	dec.prevRedundancy = false
	dec.haveDecoded = true

	pcmPLC := make([]float32, frameSize)
	n, err := dec.decodePLCForFECWithState(pcmPLC, frameSize, plcDecodeState{
		packetFrameSize: frameSize,
		mode:            ModeHybrid,
		bandwidth:       BandwidthFullband,
	})
	if err != nil {
		t.Fatalf("decodePLCForFECWithState error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("decodePLCForFECWithState samples=%d want=%d", n, frameSize)
	}
	if dec.prevMode != ModeHybrid {
		t.Fatalf("prevMode=%v want=%v (provided PLC mode must be honored)", dec.prevMode, ModeHybrid)
	}
}

// TestDecodeWithFEC_NoFECRequested verifies that when fec=false, DecodeWithFEC
// behaves identically to Decode.
func TestDecodeWithFEC_NoFECRequested(t *testing.T) {
	dec1, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	dec2, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode with Decode
	n1, err1 := dec1.Decode(packet, pcm1)
	// Decode with DecodeWithFEC(fec=false)
	n2, err2 := dec2.DecodeWithFEC(packet, pcm2, false)

	if err1 != err2 {
		t.Errorf("Errors differ: Decode=%v, DecodeWithFEC=%v", err1, err2)
	}
	if n1 != n2 {
		t.Errorf("Sample counts differ: Decode=%d, DecodeWithFEC=%d", n1, n2)
	}

	// Verify samples are identical
	for i := 0; i < n1*1; i++ {
		if pcm1[i] != pcm2[i] {
			t.Errorf("Sample %d differs: Decode=%v, DecodeWithFEC=%v", i, pcm1[i], pcm2[i])
			break
		}
	}

	t.Log("DecodeWithFEC(fec=false) behaves identically to Decode")
}

// TestDecodeWithFEC_ResetClearsFEC verifies that Reset clears FEC state.
func TestDecodeWithFEC_ResetClearsFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960

	// Reset the decoder
	dec.Reset()

	// FEC state should be cleared
	if dec.hasFEC {
		t.Error("hasFEC should be false after Reset")
	}
	if dec.fecFrameSize != 0 {
		t.Error("fecFrameSize should be 0 after Reset")
	}

	t.Log("Reset correctly clears FEC state")
}
