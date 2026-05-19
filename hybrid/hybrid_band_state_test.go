package hybrid

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/testvectors"
)

// TestHybridStartEndBandState validates that hybrid decoding clears CELT energy
// outside [HybridCELTStartBand, end) after a decode.
func TestHybridStartEndBandState(t *testing.T) {
	type candidate struct {
		name      string
		payload   []byte
		frameSize int
		bw        celt.CELTBandwidth
		stereo    bool
	}

	var candidates []candidate
	bitFile := filepath.Join("..", "testvectors", "testdata", "opus_testvectors", "testvector06.bit")
	if _, err := os.Stat(bitFile); err == nil {
		packets, err := testvectors.ReadBitstreamFile(bitFile)
		if err != nil {
			t.Fatalf("could not read testvector06: %v", err)
		}
		for i, pkt := range packets {
			if len(pkt.Data) == 0 {
				continue
			}
			frameSize, bw, ok, stereo := parseHybridTOC(pkt.Data[0])
			if !ok {
				continue
			}
			candidates = append(candidates, candidate{
				name:      fmt.Sprintf("testvector06_packet_%d", i),
				payload:   pkt.Data[1:],
				frameSize: frameSize,
				bw:        bw,
				stereo:    stereo,
			})
		}
	}
	candidates = append(candidates, candidate{
		name:      "minimal_fullband_20ms_stereo",
		payload:   createMinimalHybridPacket(960),
		frameSize: 960,
		bw:        celt.CELTFullband,
		stereo:    true,
	})

	dec := NewDecoder(2)
	const eps = 1e-9

	for _, pkt := range candidates {
		dec.Reset()
		dec.SetBandwidth(pkt.bw)
		if _, err := dec.DecodeWithPacketStereo(pkt.payload, pkt.frameSize, pkt.stereo); err != nil {
			t.Fatalf("%s hybrid decode failed: %v", pkt.name, err)
		}

		start := HybridCELTStartBand
		end := celt.EffectiveBandsForFrameSize(pkt.bw, pkt.frameSize)
		energies := dec.celtDecoder.PrevEnergy()

		for c := 0; c < dec.channels; c++ {
			base := c * celt.MaxBands
			for b := 0; b < start; b++ {
				if math.Abs(energies[base+b]) > eps {
					t.Fatalf("%s band %d (ch %d) not cleared below start=%d: %.6f", pkt.name, b, c, start, energies[base+b])
				}
			}
			for b := end; b < celt.MaxBands; b++ {
				if math.Abs(energies[base+b]) > eps {
					t.Fatalf("%s band %d (ch %d) not cleared above end=%d: %.6f", pkt.name, b, c, end, energies[base+b])
				}
			}
		}
		return
	}
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
