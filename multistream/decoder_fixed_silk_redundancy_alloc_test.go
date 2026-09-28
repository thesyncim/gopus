//go:build gopus_fixed_point

package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Each of the two 10 ms SILK children carries redundancy. opus_decode_native
// applies the integer-domain fade to each child before decoding the next one.
func TestFixedSILKMultiframeRedundancyMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := testSILKRedundancyTwoFramePacket(t)

	want, err := libopustest.ProbeDecodeSequence(48000, 2, []libopustest.DecodeDiffCase{{
		Packet: packet, Format: libopustest.DecodeDiffFormatInt24, FrameSize: 960,
	}})
	if err != nil {
		t.Fatalf("selected C decoder: %v", err)
	}
	if want[0].Code != 960 {
		t.Fatalf("selected C samples=%d, want 960", want[0].Code)
	}
	decoder, err := NewDecoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	res, handled, err := decoder.DecodeToResFixed(packet, 960)
	if err != nil || !handled {
		t.Fatalf("fixed decode handled=%v err=%v", handled, err)
	}
	wantRes := want[0].Int24()
	if len(res) != len(wantRes) {
		t.Fatalf("opus_res samples Go=%d C=%d", len(res), len(wantRes))
	}
	for i, sample := range res {
		if sample != wantRes[i] {
			t.Fatalf("opus_res sample %d: Go=%d C=%d", i, sample, wantRes[i])
		}
	}
	if got := decoder.FinalRange(); got != want[0].FinalRange {
		t.Fatalf("final range Go=%08x C=%08x", got, want[0].FinalRange)
	}
}

func TestFixedSILKRedundancyDecodeWarmZeroAllocs(t *testing.T) {
	packet := []byte{
		0x40, 0x82, 0x2e, 0x68, 0x51, 0x73, 0xfb, 0x43,
		0x3c, 0xec, 0xdf, 0xa7, 0xe6, 0xca, 0xd8, 0xbc,
		0xa7, 0xa4, 0x7a, 0x58, 0x4b, 0x92, 0x89, 0x7d,
		0x80, 0x1c, 0x65, 0xfc, 0x40, 0xaf, 0xa6, 0x1d,
		0x51, 0x63, 0x52, 0x57, 0x7a,
	}
	decoder, err := NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if res, handled, err := decoder.DecodeToResFixed(packet, 960); err != nil || !handled || len(res) != 480 {
			t.Fatalf("warm decode samples=%d handled=%v err=%v", len(res), handled, err)
		}
	}
	var decodeErr error
	var decodeFailed bool
	allocs := testing.AllocsPerRun(100, func() {
		res, handled, err := decoder.DecodeToResFixed(packet, 960)
		if err != nil || !handled || len(res) != 480 {
			decodeErr = err
			decodeFailed = true
		}
	})
	if decodeFailed {
		t.Fatalf("warm decode failed: %v", decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm fixed SILK redundancy decode allocs=%g, want 0", allocs)
	}
}
