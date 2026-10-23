package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
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
