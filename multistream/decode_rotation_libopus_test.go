package multistream

import (
	"encoding/hex"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestCELTActualRotationPacketsMatchLibopus keeps complete decoder output and
// range paired with C for packets whose PVQ rotations use rounded theta and
// Q15ONE-theta coefficients (celt/vq.c:exp_rotation).
func TestCELTActualRotationPacketsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name   string
		packet string
	}{
		{
			name:   "n48_k5_b4",
			packet: "f07f137d7ff28e19b21aca710a241a44818a6b51b98c23211c3682a673bad7cb89e030fd8c3a6464",
		},
		{
			name:   "n18_k2_b2",
			packet: "f07eadd49cb81c906978f65c278eef6446fef8303c98637011f8d4002f741650983d3f37b2db3b67d1458444635d3fdcd459182d26c9d4b48ade0c41e5cffc8a59bc8af875ada322090eb9caae135e5812c8ade61192a8977454b9d43efff1b66bb6e352be3ea21757e89fed0b149e0e75b596d636176c776a1c64ed3b",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet, err := hex.DecodeString(tc.packet)
			if err != nil {
				t.Fatal(err)
			}
			if mode := streamModeOfPacket(packet); mode != streamModeCELT {
				t.Fatalf("packet mode=%d want CELT", mode)
			}
			const frameSize = 480
			want := decodeTransitionSequenceWithLibopus(t, 48000, 1, 0, frameSize, []transitionDecodeStep{{packet: packet, frameSize: frameSize}})
			if len(want) != 1 {
				t.Fatalf("C step count=%d want 1", len(want))
			}
			if want[0].samples != frameSize {
				t.Fatalf("C sample count=%d want %d", want[0].samples, frameSize)
			}
			dec := newStreamDecoder(48000, 1)
			got, err := dec.Decode(packet, frameSize)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != frameSize || dec.FinalRange() != want[0].finalRange {
				t.Fatalf("Go length/range=(%d,%08x), C=(%d,%08x)", len(got), dec.FinalRange(), want[0].samples, want[0].finalRange)
			}
			assertTransitionStagePCMExact(t, got, want[0].pcm, tc.name)
		})
	}
}
