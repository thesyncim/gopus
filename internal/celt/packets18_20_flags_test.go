package celt

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestPackets18to20Flags checks if packets 18-20 are transient.
func TestPackets18to20Flags(t *testing.T) {
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

	// Check transient flag for packets 17-25
	transientPackets := []int{}
	for i := 17; i <= 25 && i < len(packets); i++ {
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

		// Skip silence check
		if tell >= totalBits {
			continue
		}
		if tell == 1 {
			if rd.DecodeBit(15) == 1 {
				continue
			}
		}

		// Skip postfilter check
		if tell+16 <= totalBits {
			if rd.DecodeBit(1) == 1 {
				// Skip postfilter params
				octave := int(rd.DecodeUniform(6))
				_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
				_ = int(rd.DecodeRawBits(3))
				if rd.Tell()+2 <= totalBits {
					_ = rd.DecodeICDF(tapsetICDF, 2)
				}
			}
		}
		tell = rd.Tell()

		// Check transient
		transient := false
		if mode.LM > 0 && tell+3 <= totalBits {
			transient = rd.DecodeBit(3) == 1
		}

		if transient {
			transientPackets = append(transientPackets, i)
		}
		fmt.Printf("Packet %d: transient=%v\n", i, transient)
	}

	fmt.Printf("\nTransient packets in range 17-25: %v\n", transientPackets)

	// Now scan all packets to find transient ones
	fmt.Printf("\n=== Scanning ALL packets for transients ===\n")
	allTransient := []int{}
	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		if len(pkt) <= 1 {
			continue
		}

		toc := pkt[0]
		cfg := toc >> 3
		frameSize := getFrameSize(cfg)
		mode := GetModeConfig(frameSize)

		if mode.LM == 0 {
			// Can't have transient for 2.5ms frames
			continue
		}

		// Initialize range decoder
		rd := &rangecoding.Decoder{}
		rd.Init(pkt[1:])

		totalBits := (len(pkt) - 1) * 8
		tell := rd.Tell()

		// Skip silence check
		if tell >= totalBits {
			continue
		}
		if tell == 1 {
			if rd.DecodeBit(15) == 1 {
				continue
			}
		}

		// Skip postfilter check
		if tell+16 <= totalBits {
			if rd.DecodeBit(1) == 1 {
				// Skip postfilter params
				octave := int(rd.DecodeUniform(6))
				_ = (16 << octave) + int(rd.DecodeRawBits(uint(4+octave))) - 1
				_ = int(rd.DecodeRawBits(3))
				if rd.Tell()+2 <= totalBits {
					_ = rd.DecodeICDF(tapsetICDF, 2)
				}
			}
		}
		tell = rd.Tell()

		// Check transient
		if tell+3 <= totalBits {
			if rd.DecodeBit(3) == 1 {
				allTransient = append(allTransient, i)
			}
		}
	}

	fmt.Printf("Total transient packets found: %d\n", len(allTransient))
	if len(allTransient) > 0 {
		fmt.Printf("First 20 transient packet indices: ")
		for j := 0; j < 20 && j < len(allTransient); j++ {
			fmt.Printf("%d ", allTransient[j])
		}
		fmt.Printf("\n")
	}
}
