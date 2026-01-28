package hybrid

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/testvectors"
)

// TestHybridStartEndBandState validates that hybrid decoding clears CELT energy
// outside [HybridCELTStartBand, end) after a decode.
func TestHybridStartEndBandState(t *testing.T) {
	bitFile := filepath.Join("..", "testvectors", "testdata", "opus_testvectors", "testvector06.bit")
	if _, err := os.Stat(bitFile); err != nil {
		t.Skipf("testvector not available: %v", err)
	}

	packets, err := testvectors.ReadBitstreamFile(bitFile)
	if err != nil {
		t.Skipf("could not read testvector06: %v", err)
	}
	if len(packets) == 0 {
		t.Skip("no packets in testvector06")
	}

	dec := NewDecoder(2)
	const eps = 1e-9

	for _, pkt := range packets {
		if len(pkt.Data) == 0 {
			continue
		}
		frameSize, bw, ok, stereo := parseHybridTOC(pkt.Data[0])
		if !ok {
			continue
		}
		dec.SetBandwidth(bw)
		if _, err := dec.DecodeWithPacketStereo(pkt.Data[1:], frameSize, stereo); err != nil {
			t.Fatalf("hybrid decode failed: %v", err)
		}

		start := HybridCELTStartBand
		end := celt.EffectiveBandsForFrameSize(bw, frameSize)
		energies := dec.celtDecoder.PrevEnergy()

		for c := 0; c < dec.channels; c++ {
			base := c * celt.MaxBands
			for b := 0; b < start; b++ {
				if math.Abs(energies[base+b]) > eps {
					t.Fatalf("band %d (ch %d) not cleared below start=%d: %.6f", b, c, start, energies[base+b])
				}
			}
			for b := end; b < celt.MaxBands; b++ {
				if math.Abs(energies[base+b]) > eps {
					t.Fatalf("band %d (ch %d) not cleared above end=%d: %.6f", b, c, end, energies[base+b])
				}
			}
		}
		return
	}

	t.Skip("no hybrid packets found")
}

func parseHybridTOC(toc byte) (frameSize int, bw celt.CELTBandwidth, ok bool, stereo bool) {
	config := toc >> 3
	stereo = (toc & 0x04) != 0

	switch config {
	case 12:
		return 480, celt.CELTSuperwideband, true, stereo
	case 13:
		return 960, celt.CELTSuperwideband, true, stereo
	case 14:
		return 480, celt.CELTFullband, true, stereo
	case 15:
		return 960, celt.CELTFullband, true, stereo
	default:
		return 0, 0, false, stereo
	}
}
