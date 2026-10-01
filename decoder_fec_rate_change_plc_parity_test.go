package gopus

import (
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

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
