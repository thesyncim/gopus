package gopus

import (
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
