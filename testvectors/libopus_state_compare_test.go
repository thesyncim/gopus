//go:build cgo_libopus

package testvectors

import (
	"os"
	"testing"

	cgowrap "github.com/thesyncim/gopus/celt/cgo_test"
	"github.com/thesyncim/gopus"
)

// TestDecoderLibopusStateMatch compares decoder state (prev_mode, prev_redundancy)
// against libopus to pinpoint state divergence.
func TestDecoderLibopusStateMatch(t *testing.T) {
	if os.Getenv("DEBUG_STATE") == "" {
		t.Skip("set DEBUG_STATE=1 to enable")
		return
	}
	if err := ensureTestVectors(t); err != nil {
		t.Skipf("Skipping: %v", err)
		return
	}

	const (
		sampleRate = 48000
		channels   = 2
		vector     = "testvector02"
	)

	packets, err := ReadBitstreamFile(testVectorPath(vector + ".bit"))
	if err != nil {
		t.Fatalf("read bitstream: %v", err)
	}
	if len(packets) == 0 {
		t.Fatalf("no packets")
	}

	goDec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("new gopus decoder: %v", err)
	}
	libDec, err := cgowrap.NewLibopusDecoder(sampleRate, channels)
	if err != nil || libDec == nil {
		t.Fatalf("new libopus decoder failed")
	}
	defer libDec.Destroy()

	maxSamples := gopus.DefaultDecoderConfig(sampleRate, channels).MaxPacketSamples

	for pktIdx, pkt := range packets {
		_, err := decodeInt16(goDec, pkt.Data)
		if err != nil {
			t.Fatalf("gopus decode pkt %d: %v", pktIdx, err)
		}
		_, nLib := libDec.DecodeInt16(pkt.Data, maxSamples)
		if nLib < 0 {
			t.Fatalf("libopus decode pkt %d failed: %d", pktIdx, nLib)
		}

		goMode := int(goDec.DebugPrevMode())
		libMode := libDec.GetPrevMode()
		if goMode != libMode {
			t.Fatalf("prev_mode mismatch at pkt %d: gopus=%d libopus=%d", pktIdx, goMode, libMode)
		}

		goRed := goDec.DebugPrevRedundancy()
		libRed := libDec.GetPrevRedundancy() != 0
		if goRed != libRed {
			t.Fatalf("prev_redundancy mismatch at pkt %d: gopus=%v libopus=%v", pktIdx, goRed, libRed)
		}
	}
}
