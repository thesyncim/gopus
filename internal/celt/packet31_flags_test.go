package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestPacket31Flags decodes and prints the flags from packet 31.
func TestPacket31Flags(t *testing.T) {
	testDir := filepath.Join("..", "testvectors", "testdata", "opus_testvectors")
	bitFile := filepath.Join(testDir, "testvector07.bit")

	bitData, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Test data not available: %v", err)
	}

	var packets [][]byte
	offset := 0
	for offset+8 <= len(bitData) {
		packetLen := binary.BigEndian.Uint32(bitData[offset:])
		offset += 8
		if offset+int(packetLen) > len(bitData) {
			break
		}
		packets = append(packets, bitData[offset:offset+int(packetLen)])
		offset += int(packetLen)
	}

	// Analyze flags for packets 29-35
	for i := 29; i <= 35 && i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) <= 1 {
			continue
		}

		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		mode := GetModeConfig(frameSize)

		// Initialize range decoder
		rd := &rangecoding.Decoder{}
		rd.Init(pkt[1:])

		totalBits := (len(pkt) - 1) * 8
		tell := rd.Tell()

		fmt.Printf("\n=== PACKET %d FLAGS ===\n", i)
		fmt.Printf("Payload: %d bytes, %d bits\n", len(pkt)-1, totalBits)
		fmt.Printf("FrameSize: %d, LM: %d, ShortBlocks: %d\n", frameSize, mode.LM, mode.ShortBlocks)

		// Check silence
		silence := false
		if tell >= totalBits {
			silence = true
		} else if tell == 1 {
			silence = rd.DecodeBit(15) == 1
		}
		tell = rd.Tell()
		fmt.Printf("Silence: %v (tell=%d)\n", silence, tell)

		if silence {
			continue
		}

		// Check postfilter
		postfilter := false
		if tell+16 <= totalBits {
			postfilter = rd.DecodeBit(1) == 1
			tell = rd.Tell()
		}
		fmt.Printf("Postfilter: %v (tell=%d)\n", postfilter, tell)

		if postfilter {
			// Skip postfilter params
			octave := int(rd.DecodeUniform(6))
			_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
			_ = int(rd.DecodeRawBits(3))
			if rd.Tell()+2 <= totalBits {
				_ = rd.DecodeICDF(tapsetICDF, 2)
			}
			tell = rd.Tell()
			fmt.Printf("  (skipped postfilter params, tell=%d)\n", tell)
		}

		// Check transient (only for LM > 0)
		transient := false
		if mode.LM > 0 && tell+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
			tell = rd.Tell()
		}
		fmt.Printf("Transient: %v (tell=%d)\n", transient, tell)

		// Check intra
		intra := false
		if tell+3 <= totalBits {
			intra = rd.DecodeBit(3) == 1
			tell = rd.Tell()
		}
		fmt.Printf("Intra: %v (tell=%d)\n", intra, tell)

		// Calculate short blocks
		shortBlocks := 1
		if transient {
			shortBlocks = mode.ShortBlocks
		}
		fmt.Printf("ShortBlocks (actual): %d\n", shortBlocks)

		// Calculate B and NB
		M := 1 << mode.LM
		B := shortBlocks
		NB := 120 // shortMdctSize
		if !transient {
			B = 1
			NB = NB << mode.LM
		}
		fmt.Printf("M=%d, B=%d, NB=%d, N=%d\n", M, B, NB, M*120)
	}
}
