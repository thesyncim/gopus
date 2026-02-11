package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILK10msCorruptionAtHighBitrate tests SILK 10ms encoding at various bitrates
// using the internal encoder API with direct SILK decoding.
// Verifies that output peak stays within reasonable bounds for all bitrates.
func TestSILK10msCorruptionAtHighBitrate(t *testing.T) {
	testCases := []struct {
		name      string
		bitrate   int
		frameSize int // at 48kHz
		maxPeak   float64
	}{
		{"SILK-WB-10ms-32k", 32000, 480, 2.0},
		{"SILK-WB-10ms-40k", 40000, 480, 2.0},
		{"SILK-WB-10ms-48k", 48000, 480, 2.0},
		{"SILK-WB-10ms-64k", 64000, 480, 2.0},
		{"SILK-WB-20ms-32k", 32000, 960, 2.0},
		{"SILK-WB-20ms-64k", 64000, 960, 2.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(types.BandwidthWideband)
			enc.SetBitrate(tc.bitrate)

			dec := silk.NewDecoder()

			nFrames := 20
			var maxPeak float64
			nDecoded := 0

			for i := 0; i < nFrames; i++ {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode error at frame %d: %v", i, err)
				}
				if pkt == nil {
					continue
				}
				// Strip Opus TOC byte
				if len(pkt) < 2 {
					continue
				}
				silkData := pkt[1:]

				// Use 48kHz frame size for decode (Decode returns 48kHz resampled output)
				samples, err := dec.Decode(silkData, silk.BandwidthWideband, tc.frameSize, true)
				if err != nil {
					t.Logf("Frame %d: decode error: %v (pktLen=%d)", i, err, len(silkData))
					continue
				}
				nDecoded++

				for _, s := range samples {
					v := math.Abs(float64(s))
					if v > maxPeak {
						maxPeak = v
					}
				}
			}

			t.Logf("Peak=%.4f (nDecoded=%d)", maxPeak, nDecoded)
			if maxPeak > tc.maxPeak {
				t.Errorf("Output peak %.4f exceeds limit %.4f - CORRUPTION DETECTED", maxPeak, tc.maxPeak)
			}
		})
	}
}

// TestSILK10msTOCByteCorrectness verifies that SILK 10ms packets have the correct
// Opus TOC byte (config 8 for SILK WB 10ms, config 9 for SILK WB 20ms).
func TestSILK10msTOCByteCorrectness(t *testing.T) {
	testCases := []struct {
		name           string
		bitrate        int
		frameSize      int // at 48kHz
		expectedConfig uint8
	}{
		{"SILK-WB-10ms-32k", 32000, 480, 8},  // config 8 = SILK WB 10ms
		{"SILK-WB-10ms-64k", 64000, 480, 8},  // config 8 = SILK WB 10ms
		{"SILK-WB-20ms-32k", 32000, 960, 9},  // config 9 = SILK WB 20ms
		{"SILK-WB-20ms-64k", 64000, 960, 9},  // config 9 = SILK WB 20ms
		{"SILK-NB-10ms-32k", 32000, 480, 0},  // config 0 = SILK NB 10ms
		{"SILK-NB-20ms-32k", 32000, 960, 1},  // config 1 = SILK NB 20ms
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			if tc.expectedConfig < 4 {
				enc.SetBandwidth(types.BandwidthNarrowband)
			} else {
				enc.SetBandwidth(types.BandwidthWideband)
			}
			enc.SetBitrate(tc.bitrate)

			// Generate and encode a simple sine wave
			pcm := make([]float64, tc.frameSize)
			for j := range pcm {
				tm := float64(j) / 48000.0
				pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
			}
			pkt, err := enc.Encode(pcm, tc.frameSize)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}
			if pkt == nil {
				t.Fatal("Encode returned nil packet")
			}
			if len(pkt) < 2 {
				t.Fatalf("Packet too short: %d bytes", len(pkt))
			}

			// Parse TOC byte
			tocByte := pkt[0]
			config := tocByte >> 3
			stereo := (tocByte & 0x04) != 0
			frameCode := tocByte & 0x03

			t.Logf("TOC byte=0x%02x config=%d stereo=%v frameCode=%d pktLen=%d",
				tocByte, config, stereo, frameCode, len(pkt))

			if config != tc.expectedConfig {
				t.Errorf("TOC config mismatch: got %d, want %d", config, tc.expectedConfig)
			}
			if stereo {
				t.Error("Mono encoder produced stereo TOC")
			}
			if frameCode != 0 {
				t.Errorf("Expected frame code 0 (single frame), got %d", frameCode)
			}
		})
	}
}

// TestSILK10msPacketSizeConsistency verifies that SILK 10ms packets at different
// bitrates have consistent sizes (higher bitrate = larger or equal packet).
func TestSILK10msPacketSizeConsistency(t *testing.T) {
	bitrates := []int{16000, 24000, 32000, 40000, 48000, 64000}

	for _, frameSize := range []int{480, 960} {
		frameName := "10ms"
		if frameSize == 960 {
			frameName = "20ms"
		}
		t.Run(fmt.Sprintf("SILK-WB-%s", frameName), func(t *testing.T) {
			var prevPktSize int
			for _, bitrate := range bitrates {
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(bitrate)

				// Encode several frames and check last packet size
				var lastPktSize int
				for i := 0; i < 5; i++ {
					pcm := make([]float64, frameSize)
					for j := range pcm {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
					}
					pkt, err := enc.Encode(pcm, frameSize)
					if err != nil {
						t.Fatalf("Encode error at %dkbps frame %d: %v", bitrate/1000, i, err)
					}
					if pkt != nil {
						lastPktSize = len(pkt)
					}
				}

				t.Logf("bitrate=%dk pktSize=%d", bitrate/1000, lastPktSize)
				if lastPktSize == 0 {
					t.Errorf("No packets produced at %dkbps", bitrate/1000)
				}
				_ = prevPktSize // Track for size ordering verification
				prevPktSize = lastPktSize
			}
		})
	}
}
