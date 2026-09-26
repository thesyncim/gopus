package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestProjectionDecodeIntoPrefilledBuffer(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 4
		frameSize = 960
		sentinel  = uint32(0x7fc12345)
	)
	ref := projectionDecodeRef(t, channels, frameSize, 2, 128000)
	if !allPerStreamCELT(ref.packets, ref.streams) {
		t.Fatal("projection packet did not exercise CELT decoding")
	}
	ownedDec, err := NewProjectionDecoder(48000, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	intoDec, err := NewProjectionDecoder(48000, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, 5760*channels+1)
	var firstOwned, firstSnapshot []float32
	for packetIndex, packet := range ref.packets {
		for i := range output {
			output[i] = math.Float32frombits(sentinel)
		}
		owned, err := ownedDec.DecodeToFloat32(packet, frameSize)
		if err != nil {
			t.Fatalf("packet %d owned decode: %v", packetIndex, err)
		}
		if packetIndex == 0 {
			firstOwned = owned
			firstSnapshot = append([]float32(nil), owned...)
		}
		n, err := intoDec.DecodeIntoFloat32(packet, output, 5760)
		if err != nil || n != frameSize {
			t.Fatalf("packet %d DecodeIntoFloat32=(%d,%v) want (%d,nil)", packetIndex, n, err, frameSize)
		}
		if len(owned) != n*channels {
			t.Fatalf("packet %d owned length=%d want %d", packetIndex, len(owned), n*channels)
		}
		for i, want := range owned {
			if math.Float32bits(output[i]) != math.Float32bits(want) {
				t.Fatalf("packet %d PCM[%d] bits=%08x want %08x", packetIndex, i, math.Float32bits(output[i]), math.Float32bits(want))
			}
		}
		for i := n * channels; i < len(output); i++ {
			if math.Float32bits(output[i]) != sentinel {
				t.Fatalf("packet %d output tail[%d] was overwritten", packetIndex, i)
			}
		}
	}
	if got := testing.AllocsPerRun(50, func() {
		if n, err := intoDec.DecodeIntoFloat32(ref.packets[0], output, frameSize); err != nil || n != frameSize {
			t.Fatalf("warm projection DecodeIntoFloat32=(%d,%v)", n, err)
		}
	}); got != 0 {
		t.Fatalf("warm projection DecodeIntoFloat32 allocations=%v want 0", got)
	}
	for i, want := range firstSnapshot {
		if math.Float32bits(firstOwned[i]) != math.Float32bits(want) {
			t.Fatalf("owned PCM[%d] changed after later decodes", i)
		}
	}
}
