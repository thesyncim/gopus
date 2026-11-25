package gopus

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

func TestSILKPLCUsesActiveRateAfterCELTDTXMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	stereoSILKMB20 := mustDecodeFECTransitionPacket(t, "2c886c8fa475063df63e6542b4ce9f05336f72206289fea7969d38df05574daee769acbfb80969ae842bc66b7c899baed97f5f567e16cf66543eaf4b57bfed16ff4c788b0d2fe3fdcf505fa2f2f019f0a5eecc6525b878f33f366c019488098b917f3d927910602f569282dc523180")
	stereoSILKWB20 := mustDecodeFECTransitionPacket(t, "4c886bdd439a389a6d1adfa4b0f4676d21b8be7e8fadb6595ae40dd22868cf1ac68f79b23eba441b9c1566addf3ecd6e230f512b52a599418013841f59bbde9f010ac0d9315e55524fb9883f021dbd2a7d22ba628301297eabc89a2744b3d211a25d0afd66eb9d94d18733ea9760fdbc6eb475882cbb13e0932df6065c85427141650526d2566aacfeb63cfc0559e06340948af9bd842d7d13c0")
	celtNarrowbandDTX, err := hex.DecodeString("80")
	if err != nil {
		t.Fatal(err)
	}
	if toc := ParseTOC(stereoSILKMB20[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthMediumband || !toc.Stereo {
		t.Fatalf("SILK mediumband fixture TOC=(%v,%v,stereo=%t)", toc.Mode, toc.Bandwidth, toc.Stereo)
	}
	if toc := ParseTOC(stereoSILKWB20[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || !toc.Stereo {
		t.Fatalf("SILK wideband fixture TOC=(%v,%v,stereo=%t)", toc.Mode, toc.Bandwidth, toc.Stereo)
	}
	if toc := ParseTOC(celtNarrowbandDTX[0]); toc.Mode != ModeCELT || toc.Bandwidth != BandwidthNarrowband {
		t.Fatalf("one-byte DTX fixture TOC=(%v,%v), want CELT narrowband", toc.Mode, toc.Bandwidth)
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
		for _, outputChannels := range []int{1, 2} {
			for _, outputFormat := range formats {
				t.Run(fmt.Sprintf("rate%d/ch%d/%s", sampleRate, outputChannels, outputFormat.name), func(t *testing.T) {
					frames := [4]int{
						sampleRate / 50,        // 20 ms SILK mediumband packet
						sampleRate / 50,        // 20 ms FEC request
						sampleRate / 400,       // 2.5 ms one-byte CELT narrowband DTX
						sampleRate * 40 / 1000, // 40 ms SILK PLC
					}
					packets := [4][]byte{stereoSILKMB20, stereoSILKWB20, celtNarrowbandDTX, nil}
					cases := make([]libopustest.DecodeDiffCase, len(frames))
					for i := range cases {
						format := outputFormat.format
						if i == 1 {
							format = libopustest.DecodeDiffFormatFloat32
						}
						cases[i] = libopustest.DecodeDiffCase{
							Packet:    packets[i],
							Format:    format,
							FrameSize: uint32(frames[i]),
							DecodeFEC: i == 1,
						}
					}
					want, err := libopustest.ProbeDecodeSequence(sampleRate, outputChannels, cases)
					if err != nil {
						libopustest.HelperUnavailable(t, "selected-libopus SILK DTX recovery sequence", err)
					}
					if len(want) != len(cases) {
						t.Fatalf("libopus returned %d calls, want %d", len(want), len(cases))
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, outputChannels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					maxOutput := frames[3] * outputChannels
					pcmFloat := make([]float32, maxOutput)
					pcm16 := make([]int16, maxOutput)
					pcm24 := make([]int32, maxOutput)
					type output struct {
						float32 []float32
						int16   []int16
						int24   []int32
					}
					decode := func(i int) (int, output, error) {
						samples := frames[i] * outputChannels
						var got output
						var n int
						var err error
						switch cases[i].Format {
						case libopustest.DecodeDiffFormatFloat32:
							got.float32 = pcmFloat[:samples]
							if i == 1 {
								n, err = dec.DecodeWithFEC(packets[i], got.float32, true)
							} else {
								n, err = dec.Decode(packets[i], got.float32)
							}
						case libopustest.DecodeDiffFormatInt16:
							got.int16 = pcm16[:samples]
							n, err = dec.DecodeInt16(packets[i], got.int16)
						case libopustest.DecodeDiffFormatInt24:
							got.int24 = pcm24[:samples]
							n, err = dec.DecodeInt24(packets[i], got.int24)
						}
						return n, got, err
					}
					failedStep := -1
					var failure error
					runSequence := func() {
						dec.Reset()
						failedStep, failure = -1, nil
						for i := range cases {
							n, got, err := decode(i)
							if err != nil || n != int(want[i].Code) || n != frames[i] {
								failedStep, failure = i, err
								if failure == nil {
									failure = fmt.Errorf("returned %d samples, C returned %d; want %d", n, want[i].Code, frames[i])
								}
								return
							}
							if gotRange, wantRange := dec.FinalRange(), want[i].FinalRange; gotRange != wantRange {
								failedStep = i
								failure = fmt.Errorf("final range=%08x, C=%08x", gotRange, wantRange)
								return
							}
							if mismatch := assertDecodeMalformedFramingPCM(cases[i].Format, n, outputChannels, want[i].PCM, got.float32, got.int16, got.int24); mismatch != nil {
								failedStep, failure = i, mismatch
								return
							}
							if i == 2 && (dec.Bandwidth() != BandwidthNarrowband || dec.prevMode != ModeSILK || dec.lastPacketMode != ModeCELT || dec.silkDecoder.GetSampleRateKHz() != 16) {
								failedStep = i
								failure = fmt.Errorf("after CELT DTX public bandwidth=%v, previous mode=%v, packet mode=%v, active SILK rate=%d kHz", dec.Bandwidth(), dec.prevMode, dec.lastPacketMode, dec.silkDecoder.GetSampleRateKHz())
								return
							}
						}
						if dec.Bandwidth() != BandwidthNarrowband || dec.silkDecoder.GetSampleRateKHz() != 16 {
							failedStep = len(cases) - 1
							failure = fmt.Errorf("after PLC public bandwidth=%v, active SILK rate=%d kHz", dec.Bandwidth(), dec.silkDecoder.GetSampleRateKHz())
						}
					}
					runSequence()
					if failedStep >= 0 {
						t.Fatalf("call %d: %v", failedStep, failure)
					}
					if allocs := testing.AllocsPerRun(10, runSequence); allocs != 0 {
						t.Fatalf("warmed Reset/recovery sequence allocs=%g, want 0", allocs)
					}
					if failedStep >= 0 {
						t.Fatalf("warmed call %d: %v", failedStep, failure)
					}
				})
			}
		}
	}
}

func TestSILKLongStereoToMonoRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	// Fixed wire packets keep this recovery witness independent of encoder behavior.
	mono40, err := hex.DecodeString("50c100aeb623947d81bcc7980058ef2539604e53c2529c15b9499cb7d7dc318eccc9b705c399330c3ba8c4a8f9dd83081768b4c5cfe84c6ca76f3f89e6834b595ae4cbee4165a7eef36c43d23de0cd3520664548400d75e22dda891378b920b5ea9956717905f4692f36ed646edcaa643398c37d36849e9508490f3d6300ae1cc34150d371c3e713840ef0")
	if err != nil {
		t.Fatal(err)
	}
	stereo40, err := hex.DecodeString("54d9346d58758b87d1bbdb39e481b0cdf58959ea38e20f1255003666bb4633195ac9bd2f52ab993093f3d6a120f4a95b13b0163db9c0c3e21f8df1f4931a487ecb827357889078eea89ef7153b012ab77fde135545bbb90e58afbf954b78aa0edd9f4de4c08cef748c8873b86c33787710dfa3af84f3eb72bbb3e6700d1468896caf3a1e475ff74451cb22580a3c099f092391240c7160421137e84a80effbb35c2e0f817a")
	if err != nil {
		t.Fatal(err)
	}
	if mono40[0]&4 != 0 || stereo40[0]&4 == 0 {
		t.Fatalf("packet channel flags are mono=%02x stereo=%02x", mono40[0], stereo40[0])
	}

	type outputFormat struct {
		name string
		id   uint32
	}
	formats := []outputFormat{
		{name: "float32", id: libopustest.DecodeDiffFormatFloat32},
		{name: "int16", id: libopustest.DecodeDiffFormatInt16},
		{name: "int24", id: libopustest.DecodeDiffFormatInt24},
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		packetFrameSize := sampleRate * 40 / 1000
		plcFrameSize := sampleRate * 5 / 2000
		packets := [][]byte{mono40, nil, stereo40, nil, mono40}
		frameSizes := []int{packetFrameSize, plcFrameSize, packetFrameSize, plcFrameSize, packetFrameSize}

		for _, format := range formats {
			format := format
			t.Run(fmt.Sprintf("rate%d/%s", sampleRate, format.name), func(t *testing.T) {
				cases := make([]libopustest.DecodeDiffCase, len(packets))
				for step, packet := range packets {
					cases[step] = libopustest.DecodeDiffCase{
						Packet:    packet,
						Format:    format.id,
						FrameSize: uint32(frameSizes[step]),
					}
				}
				want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, cases)
				if err != nil {
					t.Fatalf("ProbeDecodeSequence: %v", err)
				}
				if len(want) != len(packets) {
					t.Fatalf("oracle returned %d calls, want %d", len(want), len(packets))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				maxOutputSamples := packetFrameSize * 2
				pcmFloat := make([]float32, maxOutputSamples)
				pcm16 := make([]int16, maxOutputSamples)
				pcm24 := make([]int32, maxOutputSamples)
				decode := func(packet []byte, frameSize int) (int, error) {
					samples := frameSize * 2
					switch format.id {
					case libopustest.DecodeDiffFormatFloat32:
						return dec.Decode(packet, pcmFloat[:samples])
					case libopustest.DecodeDiffFormatInt16:
						return dec.DecodeInt16(packet, pcm16[:samples])
					default:
						return dec.DecodeInt24(packet, pcm24[:samples])
					}
				}

				for step, packet := range packets {
					n, err := decode(packet, frameSizes[step])
					if err != nil || int32(n) != want[step].Code {
						t.Fatalf("step%d returned (%d,%v), C returned %d", step, n, err, want[step].Code)
					}
					if want[step].Code != int32(frameSizes[step]) {
						t.Fatalf("C step%d returned %d samples, want %d", step, want[step].Code, frameSizes[step])
					}
					if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
						t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
					}

					sampleCount := n * 2
					bytesPerSample := 4
					if format.id == libopustest.DecodeDiffFormatInt16 {
						bytesPerSample = 2
					}
					if got, expected := len(want[step].PCM), sampleCount*bytesPerSample; got != expected {
						t.Fatalf("step%d oracle PCM bytes=%d, want %d", step, got, expected)
					}
					switch format.id {
					case libopustest.DecodeDiffFormatFloat32:
						for sample := 0; sample < sampleCount; sample++ {
							got := math.Float32bits(pcmFloat[sample])
							expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
							if got != expected {
								t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
							}
						}
					case libopustest.DecodeDiffFormatInt16:
						for sample := 0; sample < sampleCount; sample++ {
							got := pcm16[sample]
							expected := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:]))
							if got != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
							}
						}
					default:
						for sample := 0; sample < sampleCount; sample++ {
							got := pcm24[sample]
							expected := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:]))
							if got != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
							}
						}
					}
				}

				runSequence := func() error {
					dec.Reset()
					for step, packet := range packets {
						n, err := decode(packet, frameSizes[step])
						if err != nil {
							return err
						}
						if n != frameSizes[step] {
							return fmt.Errorf("step%d returned %d samples, want %d", step, n, frameSizes[step])
						}
					}
					return nil
				}
				if err := runSequence(); err != nil {
					t.Fatalf("warm reset sequence: %v", err)
				}
				var runErr error
				allocs := testing.AllocsPerRun(10, func() {
					runErr = runSequence()
				})
				if runErr != nil {
					t.Fatalf("measured reset sequence: %v", runErr)
				}
				if allocs != 0 {
					t.Fatalf("warm reset sequence allocations=%g", allocs)
				}
			})
		}
	}
}

const (
	silkNoLBRRMB60PrimeHex  = "38e0b8a342302116dd95c61fbe30c235931e69e7742082a44e6dc89a69a9a1f2d5b943108c758641fc3f9c71c10dc9fdc63df9b7972a9ac0e419fd9adaa535d01bfd4dbe68784c9e3e8378446d083b3f6ad7d7ac2785df66e7943d58a5f6118b30f89396e218a13af0e4e52811a4f217657d8a4c0b40f067a3a45f4f0498c7d3d74ecbd894bfabdd3eebba9f9c48e93818adb799103dc9b60dc7895092370005992e18723ff8cd733a61f0c03083c40ff6086988fe15c30f420eb1662088ade1d549fe6b5fdb2441cf7d57bf60012532bfd82259ecbefd041aa197bb087b6d8297b048ed4369352e1b2cbebf909121ca44fde1916396ec35b1f104582de3d33fd748ec1a19bbe7338fac7cafa3a7623729f322469ea51f51209196702497d20706976437d5ba82ae865935af9a2e1f25bb0f6d9440051aa77b2b3099a9e95528c45f43c4e0e0ff7671f2e7d542ebcc60c1304959"
	silkNoLBRRWB40TargetHex = "50c188124388cac7114d36facf91e805441def8f83919ee7973f31d72f53a7c8dc8bc330a3a90608a96a027be78b9299bd3171df6293a6e48081c778be91c2feec6bcab6de26dce0f4c5be569a692e436b46f01f38762513f7761245ed2552f25750013f57ce60321ed96093c1e348bfb3dfd43daf5bc08a7814a726b8b66119990ea3c59259f8cd7ab6cbf0d9e295adc680b46d6cd9b233aecf22758b0ce775c5e02f9d36e47ffd24d591eed2813b188921b3f7f27493908c7089cab41f694cb977ea9d01b843a574c9db3aa0a660f0d50a1d1c2aaac2e72e72397cecebe419b438aa2e697f05960392de46c1815d72c1ccdfda5959f91d57dd7d9797e0e3b7028099539e8707d90a4a042508a6cf0101582f6862e4f656ff2019d7eea02fe3f1a8b7d334c98024c0"
)

func TestSILKNoLBRRMultiFrameFECPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	prime, err := hex.DecodeString(silkNoLBRRMB60PrimeHex)
	if err != nil {
		t.Fatal(err)
	}
	target, err := hex.DecodeString(silkNoLBRRWB40TargetHex)
	if err != nil {
		t.Fatal(err)
	}
	for name, packet := range map[string][]byte{"MB60 prime": prime, "WB40 target": target} {
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK {
			t.Fatalf("%s mode=%v, want SILK", name, toc.Mode)
		}
		if PacketHasLBRR(packet) {
			t.Fatalf("%s unexpectedly has LBRR", name)
		}
	}
	lbrr, err := probeLibopusPacketLBRR([]libopusPacketLBRRCase{
		{name: "MB60 prime", packet: prime},
		{name: "WB40 target", packet: target},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "no-LBRR FEC witness packet probe", err)
		return
	}
	for i, got := range lbrr {
		if got != 0 {
			t.Fatalf("libopus reports LBRR=%d for packet %d, want none", got, i)
		}
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dHz_%dch", sampleRate, channels), func(t *testing.T) {
				primeCapacity := sampleRate * 120 / 1000
				primeSamples := sampleRate * 60 / 1000
				fecSamples := sampleRate * 40 / 1000
				want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
					{
						Packet: prime, Format: libopustest.DecodeDiffFormatInt16,
						FrameSize: uint32(primeCapacity),
					},
					{
						Packet: target, Format: libopustest.DecodeDiffFormatFloat32,
						FrameSize: uint32(fecSamples), DecodeFEC: true,
					},
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "MB60→WB40 no-LBRR FEC sequence", err)
					return
				}
				if len(want) != 2 || want[0].Code != int32(primeSamples) || want[1].Code != int32(fecSamples) {
					t.Fatalf("libopus counts=%v, want [%d %d]", decodeFECTransitionCounts(want), primeSamples, fecSamples)
				}
				wantPrime := want[0].Int16()
				wantFEC := want[1].Float32()

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				pcm16 := make([]int16, primeCapacity*channels)
				pcmFloat := make([]float32, fecSamples*channels)
				decodeSequence := func() error {
					dec.Reset()
					n, err := dec.DecodeInt16(prime, pcm16)
					if err != nil {
						return fmt.Errorf("MB60 decode: %w", err)
					}
					if n != primeSamples {
						return fmt.Errorf("MB60 samples=%d, want %d", n, primeSamples)
					}
					if got, expected := dec.FinalRange(), want[0].FinalRange; got != expected {
						return fmt.Errorf("MB60 range=%08x C=%08x", got, expected)
					}
					assertSILKNoLBRRInt16PCM(t, "MB60", pcm16[:n*channels], wantPrime)

					n, err = dec.DecodeWithFEC(target, pcmFloat, true)
					if err != nil {
						return fmt.Errorf("WB40 FEC decode: %w", err)
					}
					if n != fecSamples {
						return fmt.Errorf("WB40 FEC samples=%d, want %d", n, fecSamples)
					}
					if got, expected := dec.FinalRange(), want[1].FinalRange; got != expected {
						return fmt.Errorf("WB40 FEC range=%08x C=%08x", got, expected)
					}
					assertSILKNoLBRRFloat32PCM(t, "WB40 FEC", pcmFloat[:n*channels], wantFEC)
					return nil
				}
				if err := decodeSequence(); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					if err := decodeSequence(); err != nil {
						t.Fatal(err)
					}
				}
				var measuredErr error
				allocs := testing.AllocsPerRun(20, func() {
					measuredErr = decodeSequence()
				})
				if measuredErr != nil {
					t.Fatal(measuredErr)
				}
				if allocs != 0 {
					t.Fatalf("warm MB60→WB40 sequence allocations=%g, want 0", allocs)
				}
			})
		}
	}
}

func assertSILKNoLBRRInt16PCM(t *testing.T, stage string, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s int16 PCM length=%d, C=%d", stage, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s int16 PCM[%d]=%d, C=%d", stage, i, got[i], want[i])
		}
	}
}

func assertSILKNoLBRRFloat32PCM(t *testing.T, stage string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s float32 PCM length=%d, C=%d", stage, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s float32 PCM[%d]=%08x, C=%08x", stage, i, gotBits, wantBits)
		}
	}
}
