package gopus

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
