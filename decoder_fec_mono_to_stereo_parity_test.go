package gopus

import (
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
