package gopus

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
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
