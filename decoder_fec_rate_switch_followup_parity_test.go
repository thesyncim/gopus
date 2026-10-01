package gopus

import (
	"encoding/hex"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
