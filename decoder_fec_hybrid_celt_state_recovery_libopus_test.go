package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
