//go:build gopus_dred && gopus_osce && !gopus_fixed_point

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/multistream"
)

var osceModelReloadHelper libopustest.HelperCache
var osceFECHistorySwitchHelper libopustest.HelperCache

func TestOSCEModelReloadPreservesActiveStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper := requireOSCEModelReloadHelper(t)

	model := dredHistoryDecoderModelBlob(t)
	parsedModel, err := dnnblob.Clone(model)
	if err != nil {
		t.Fatalf("parse OSCE model blob: %v", err)
	}
	packets := makeOSCEModelReloadPackets(t)
	for _, complexity := range []int{6, 7} {
		for _, useMultistream := range []bool{false, true} {
			name := "root"
			if useMultistream {
				name = "multistream"
			}
			t.Run(fmt.Sprintf("%s/complexity%d", name, complexity), func(t *testing.T) {
				const reloadStep = 3
				const frameSize = 960
				want := probeOSCEModelSequence(t, helper, model, packets, 48000, frameSize,
					complexity, useMultistream, true, uint32(reloadStep), ^uint32(0))

				var decode func([]byte, []float32) (int, error)
				var reload func() error
				var finalRange func() uint32
				if useMultistream {
					d, err := multistream.NewDecoder(48000, 1, 1, 0, []byte{0})
					if err != nil {
						t.Fatal(err)
					}
					if err := d.SetComplexity(complexity); err != nil {
						t.Fatalf("SetComplexity: %v", err)
					}
					d.SetOSCEBWE(true)
					d.SetDNNBlob(parsedModel)
					decode = func(packet []byte, out []float32) (int, error) {
						return d.DecodeIntoFloat32(packet, out, frameSize)
					}
					reload = func() error { d.SetDNNBlob(parsedModel); return nil }
					finalRange = d.FinalRange
				} else {
					d, err := NewDecoder(DefaultDecoderConfig(48000, 1))
					if err != nil {
						t.Fatal(err)
					}
					if err := d.SetComplexity(complexity); err != nil {
						t.Fatalf("SetComplexity: %v", err)
					}
					if err := d.SetOSCEBWE(true); err != nil {
						t.Fatalf("SetOSCEBWE: %v", err)
					}
					if err := d.SetDNNBlob(model); err != nil {
						t.Fatalf("SetDNNBlob: %v", err)
					}
					decode = d.Decode
					reload = func() error { return d.SetDNNBlob(model) }
					finalRange = d.FinalRange
				}
				out := make([]float32, frameSize)
				for step, packet := range packets {
					if step == reloadStep {
						if err := reload(); err != nil {
							t.Fatalf("SetDNNBlob(reload): %v", err)
						}
					}
					n, err := decode(packet, out)
					if err != nil || n != want[step].samples || finalRange() != want[step].finalRange {
						t.Fatalf("step%d count/range=%d/%08x C=%d/%08x err=%v", step, n, finalRange(), want[step].samples, want[step].finalRange, err)
					}
					for i := range n {
						if got := math.Float32bits(out[i]); got != want[step].pcmBits[i] {
							t.Fatalf("step%d sample%d Go=%08x C=%08x", step, i, got, want[step].pcmBits[i])
						}
					}
				}
			})
		}
	}
}

func TestOSCEFECMissingPrefixThenLBRRUsesResetChannelState(t *testing.T) {
	libopustest.RequireOracle(t)
	helper := requireOSCEModelReloadHelper(t)
	model := dredHistoryDecoderModelBlob(t)
	for _, tc := range []struct {
		name      string
		frameSize int
	}{
		{name: "40ms", frameSize: 1920},
		{name: "60ms", frameSize: 2880},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packets := encodeFECBurstyStreamForTest(t, BandwidthWideband, 24000, 1, tc.frameSize, 40)
			recovery := -1
			for i := 2; i < len(packets); i++ {
				if packetHasInBandFEC(t, packets[i]) && hasMissingLBRRPrefixBeforePresentFrame(t, packets[i]) {
					recovery = i
					break
				}
			}
			if recovery < 0 {
				t.Fatalf("encoder emitted no missing-prefix→present-LBRR FEC packet for %s", tc.name)
			}
			apiRate := 16000
			frameSize, err := packetSamplesAtRate(packets[0], apiRate)
			if err != nil {
				t.Fatal(err)
			}
			sequence := packets[:recovery+1]
			for _, prefix := range []struct {
				name  string
				extra int
			}{
				{name: "packet_duration"},
				{name: "outer_20ms_PLC", extra: apiRate / 50},
			} {
				t.Run(prefix.name, func(t *testing.T) {
					requestedSize := frameSize + prefix.extra
					for _, complexity := range []int{0, 7} {
						t.Run(fmt.Sprintf("complexity%d", complexity), func(t *testing.T) {
							want := probeOSCEModelSequence(t, helper, model, sequence, apiRate, requestedSize,
								complexity, false, false, ^uint32(0), uint32(recovery))

							dec, err := NewDecoder(DefaultDecoderConfig(apiRate, 1))
							if err != nil {
								t.Fatal(err)
							}
							if err := dec.SetComplexity(complexity); err != nil {
								t.Fatal(err)
							}
							if err := dec.SetDNNBlob(model); err != nil {
								t.Fatalf("SetDNNBlob: %v", err)
							}
							out := make([]float32, requestedSize)
							for step, packet := range sequence {
								var n int
								if step == recovery {
									n, err = dec.DecodeWithFEC(packet, out, true)
								} else {
									n, err = dec.Decode(packet, out)
								}
								if err != nil || n != want[step].samples || dec.FinalRange() != want[step].finalRange {
									t.Fatalf("step%d count/range=%d/%08x C=%d/%08x err=%v", step, n, dec.FinalRange(), want[step].samples, want[step].finalRange, err)
								}
								for i := range n {
									if got := math.Float32bits(out[i]); got != want[step].pcmBits[i] {
										t.Fatalf("step%d sample%d Go=%08x C=%08x", step, i, got, want[step].pcmBits[i])
									}
								}
							}
							if complexity >= 5 {
								decode := func() {
									if _, err := dec.DecodeWithFEC(sequence[recovery], out, true); err != nil {
										panic(err)
									}
								}
								decode()
								decode()
								if allocs := testing.AllocsPerRun(20, decode); allocs != 0 {
									t.Fatalf("warmed DecodeWithFEC allocations=%g want 0", allocs)
								}
							}
						})
					}
				})
			}
		})
	}
}

func TestOSCEFECTinyLBRRUsesMainModelLossPath(t *testing.T) {
	libopustest.RequireOracle(t)
	helper := requireOSCEModelReloadHelper(t)
	model := dredHistoryDecoderModelBlob(t)
	seed := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	tiny := []byte{seed[0] & 0xfc, 0}
	sequence := [][]byte{seed, tiny}
	const (
		apiRate   = 48000
		frameSize = 960
	)
	for _, complexity := range []int{0, 7} {
		t.Run(fmt.Sprintf("complexity%d", complexity), func(t *testing.T) {
			want := probeOSCEModelSequence(t, helper, model, sequence, apiRate, frameSize,
				complexity, false, false, ^uint32(0), 1)
			dec, err := NewDecoder(DefaultDecoderConfig(apiRate, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetComplexity(complexity); err != nil {
				t.Fatal(err)
			}
			if err := dec.SetDNNBlob(model); err != nil {
				t.Fatalf("SetDNNBlob: %v", err)
			}
			out := make([]float32, frameSize)
			for step, packet := range sequence {
				var n int
				if step == 1 {
					n, err = dec.DecodeWithFEC(packet, out, true)
				} else {
					n, err = dec.Decode(packet, out)
				}
				if err != nil || n != want[step].samples || dec.FinalRange() != want[step].finalRange {
					t.Fatalf("step%d count/range=%d/%08x C=%d/%08x err=%v", step, n, dec.FinalRange(), want[step].samples, want[step].finalRange, err)
				}
				for i := range n {
					if got := math.Float32bits(out[i]); got != want[step].pcmBits[i] {
						t.Fatalf("step%d sample%d Go=%08x C=%08x", step, i, got, want[step].pcmBits[i])
					}
				}
			}
		})
	}
}

func TestOSCEFECFallbackPreservesClassicalLossHistory(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := osceFECHistorySwitchHelper.Path(func() (string, error) {
		root, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		return libopustest.BuildDREDWeightsFileHelper(root,
			"libopus_decoder_fec_history_switch.c", "gopus_fec_history_switch", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "FEC PLC history switch", err)
	}
	model := dredHistoryDecoderModelBlob(t)
	packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	const (
		rate      = 16000
		frameSize = 320
	)

	var input bytes.Buffer
	input.WriteString("GHFS")
	put := func(value uint32) { _ = binary.Write(&input, binary.LittleEndian, value) }
	put(1)
	put(rate)
	put(frameSize)
	put(uint32(len(packet)))
	put(uint32(len(model)))
	put(2) // switch to complexity 5 before the second received packet
	put(5)
	input.Write(model)
	input.Write(packet)
	oracle, err := libopustest.RunOracleVersion(helper, input.Bytes(), "FEC PLC history switch", "GHFO", 2)
	if err != nil {
		t.Fatal(err)
	}
	features, arch := oracle.U32(), oracle.U32()
	rtcdEnabled, presumeNEON := oracle.U32() != 0, oracle.U32() != 0
	wantFeatures := uint32(3)
	if extsupport.QEXT {
		wantFeatures |= 4
	}
	if features != wantFeatures {
		t.Fatalf("C feature identity=%03b want=%03b", features, wantFeatures)
	}
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		libopustest.HelperUnavailable(t, "selected C reference variant", err)
	}
	switch variant {
	case libopustooling.LibopusReferenceSIMD:
		switch runtime.GOARCH {
		case "amd64":
			if !rtcdEnabled || arch == 0 || presumeNEON {
				t.Fatalf("selected C SIMD identity: RTCD=%t arch=%d presumeNEON=%t", rtcdEnabled, arch, presumeNEON)
			}
		case "arm64":
			if rtcdEnabled || arch != 0 || !presumeNEON {
				t.Fatalf("selected C NEON identity: RTCD=%t arch=%d presumeNEON=%t", rtcdEnabled, arch, presumeNEON)
			}
		default:
			t.Fatalf("no selected SIMD oracle identity contract for %s", runtime.GOARCH)
		}
	case libopustooling.LibopusReferenceScalar:
		if rtcdEnabled || arch != 0 || presumeNEON {
			t.Fatalf("selected C scalar identity: RTCD=%t arch=%d presumeNEON=%t", rtcdEnabled, arch, presumeNEON)
		}
	default:
		t.Fatalf("unsupported selected C variant %q", variant)
	}
	t.Logf("selected C variant=%s arch=%d RTCD=%t presumeNEON=%t features=%03b",
		variant, arch, rtcdEnabled, presumeNEON, features)
	oracle.Count(4)
	want := make([]osceModelSequenceFrame, 4)
	for step := range want {
		want[step].samples = int(oracle.U32())
		want[step].finalRange = oracle.U32()
		want[step].pcmBits = make([]uint32, want[step].samples)
		for i := range want[step].pcmBits {
			want[step].pcmBits[i] = oracle.U32()
		}
	}
	if err := oracle.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(rate, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetComplexity(0); err != nil {
		t.Fatal(err)
	}
	if err := dec.SetDNNBlob(model); err != nil {
		t.Fatalf("SetDNNBlob: %v", err)
	}
	out := make([]float32, frameSize)
	for step := range want {
		if step == 2 {
			if err := dec.SetComplexity(5); err != nil {
				t.Fatalf("SetComplexity(5): %v", err)
			}
		}
		var n int
		if step == 1 || step == 3 {
			n, err = dec.DecodeWithFEC(nil, out, true)
		} else {
			n, err = dec.Decode(packet, out)
		}
		if err != nil || n != want[step].samples || dec.FinalRange() != want[step].finalRange {
			t.Fatalf("step%d count/range=%d/%08x C=%d/%08x err=%v", step, n, dec.FinalRange(), want[step].samples, want[step].finalRange, err)
		}
		for i := range n {
			if got := math.Float32bits(out[i]); got != want[step].pcmBits[i] {
				t.Fatalf("step%d sample%d Go=%08x C=%08x", step, i, got, want[step].pcmBits[i])
			}
		}
		if step == 1 && dec.rawSILKHistoryFill != 2*320 {
			t.Fatalf("retained history after classical FEC loss=%d want %d", dec.rawSILKHistoryFill, 2*320)
		}
	}
	decodeLoss := func() {
		if _, err := dec.DecodeWithFEC(nil, out, true); err != nil {
			panic(err)
		}
	}
	decodeLoss()
	decodeLoss()
	if allocs := testing.AllocsPerRun(20, decodeLoss); allocs != 0 {
		t.Fatalf("warmed DecodeWithFEC(nil) allocations=%g want 0", allocs)
	}
}

type osceModelSequenceFrame struct {
	samples    int
	finalRange uint32
	pcmBits    []uint32
}

func requireOSCEModelReloadHelper(t *testing.T) string {
	t.Helper()
	path, err := osceModelReloadHelper.Path(func() (string, error) {
		root, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
		return libopustest.BuildDREDWeightsFileHelper(root,
			"libopus_osce_model_reload_sequence.c", "gopus_osce_model_reload", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE model reload/FEC sequence", err)
	}
	return path
}

func probeOSCEModelSequence(t *testing.T, helper string, model []byte, packets [][]byte,
	rate, frameSize, complexity int, multistream, enableBWE bool, reloadStep, fecStep uint32,
) []osceModelSequenceFrame {
	t.Helper()
	var input bytes.Buffer
	input.WriteString("GORL")
	put := func(v uint32) { _ = binary.Write(&input, binary.LittleEndian, v) }
	put(2)
	put(uint32(rate))
	put(1)
	put(uint32(complexity))
	if multistream {
		put(1)
	} else {
		put(0)
	}
	if enableBWE {
		put(1)
	} else {
		put(0)
	}
	put(uint32(frameSize))
	put(uint32(len(packets)))
	put(reloadStep)
	put(fecStep)
	put(uint32(len(model)))
	input.Write(model)
	for _, packet := range packets {
		put(uint32(len(packet)))
		input.Write(packet)
	}

	r, err := libopustest.RunOracleVersion(helper, input.Bytes(), "OSCE model sequence", "GORO", 1)
	if err != nil {
		t.Fatal(err)
	}
	features, arch := r.U32(), r.U32()
	r.Count(len(packets))
	wantFeatures := uint32(3)
	if extsupport.QEXT {
		wantFeatures |= 4
	}
	if features != wantFeatures {
		t.Fatalf("selected C features=%03b want %03b", features, wantFeatures)
	}
	t.Logf("selected C arch=%d", arch)

	want := make([]osceModelSequenceFrame, len(packets))
	for step := range want {
		want[step].samples = int(r.U32())
		want[step].finalRange = r.U32()
		want[step].pcmBits = make([]uint32, want[step].samples)
		for i := range want[step].pcmBits {
			want[step].pcmBits[i] = r.U32()
		}
	}
	if err := r.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return want
}

func makeOSCEModelReloadPackets(t *testing.T) [][]byte {
	t.Helper()
	packet := makeValidMonoSILKPacketForFrameSizeBandwidthForDREDTest(t, 960, BandwidthWideband)
	packets := make([][]byte, 6)
	for i := range packets {
		packets[i] = append([]byte(nil), packet...)
	}
	return packets
}
