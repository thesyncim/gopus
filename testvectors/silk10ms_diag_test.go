package testvectors

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestSilk10msDiag(t *testing.T) {
	frameSize10ms := 480
	frameSize20ms := 960
	channels := 1
	bitrate := 32000

	// Generate test signal
	numFrames10ms := 48000 / frameSize10ms
	numFrames20ms := 48000 / frameSize20ms
	totalSamples10ms := numFrames10ms * frameSize10ms * channels
	totalSamples20ms := numFrames20ms * frameSize20ms * channels
	signal10ms := generateEncoderTestSignal(totalSamples10ms, channels)
	signal20ms := generateEncoderTestSignal(totalSamples20ms, channels)

	// Test NB 10ms
	enc10 := encoder.NewEncoder(48000, channels)
	enc10.SetMode(encoder.ModeSILK)
	enc10.SetBandwidth(types.BandwidthNarrowband)
	enc10.SetBitrate(bitrate)

	// Test NB 20ms
	enc20 := encoder.NewEncoder(48000, channels)
	enc20.SetMode(encoder.ModeSILK)
	enc20.SetBandwidth(types.BandwidthNarrowband)
	enc20.SetBitrate(bitrate)

	var totalBytes10ms, totalBytes20ms int
	var maxPkt10ms, minPkt10ms int
	minPkt10ms = math.MaxInt32
	var maxPkt20ms, minPkt20ms int
	minPkt20ms = math.MaxInt32

	for i := 0; i < numFrames10ms; i++ {
		start := i * frameSize10ms * channels
		end := start + frameSize10ms * channels
		pcm := float32ToFloat64(signal10ms[start:end])
		pkt, err := enc10.Encode(pcm, frameSize10ms)
		if err != nil {
			t.Fatalf("10ms encode frame %d: %v", i, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("10ms empty packet at frame %d", i)
		}
		totalBytes10ms += len(pkt)
		if len(pkt) > maxPkt10ms {
			maxPkt10ms = len(pkt)
		}
		if len(pkt) < minPkt10ms {
			minPkt10ms = len(pkt)
		}
		if i < 5 || i == numFrames10ms-1 {
			t.Logf("10ms frame %d: %d bytes, TOC=0x%02x", i, len(pkt), pkt[0])
		}
	}

	for i := 0; i < numFrames20ms; i++ {
		start := i * frameSize20ms * channels
		end := start + frameSize20ms * channels
		pcm := float32ToFloat64(signal20ms[start:end])
		pkt, err := enc20.Encode(pcm, frameSize20ms)
		if err != nil {
			t.Fatalf("20ms encode frame %d: %v", i, err)
		}
		if len(pkt) == 0 {
			t.Fatalf("20ms empty packet at frame %d", i)
		}
		totalBytes20ms += len(pkt)
		if len(pkt) > maxPkt20ms {
			maxPkt20ms = len(pkt)
		}
		if len(pkt) < minPkt20ms {
			minPkt20ms = len(pkt)
		}
		if i < 5 || i == numFrames20ms-1 {
			t.Logf("20ms frame %d: %d bytes, TOC=0x%02x", i, len(pkt), pkt[0])
		}
	}

	avgBitrate10ms := totalBytes10ms * 8 * (48000 / frameSize10ms)
	avgBitrate20ms := totalBytes20ms * 8 * (48000 / frameSize20ms)

	t.Logf("--- 10ms NB ---")
	t.Logf("Packets: %d, Total bytes: %d, Avg bitrate: %d bps", numFrames10ms, totalBytes10ms, avgBitrate10ms)
	t.Logf("Pkt size: min=%d, max=%d", minPkt10ms, maxPkt10ms)

	t.Logf("--- 20ms NB ---")
	t.Logf("Packets: %d, Total bytes: %d, Avg bitrate: %d bps", numFrames20ms, totalBytes20ms, avgBitrate20ms)
	t.Logf("Pkt size: min=%d, max=%d", minPkt20ms, maxPkt20ms)

	// Check if 10ms packets have reasonable TOC
	t.Logf("Expected TOC for SILK NB 10ms mono: config=0, stereo=0, code=0 => 0x%02x", (0 << 3))
	t.Logf("Expected TOC for SILK NB 20ms mono: config=1, stereo=0, code=0 => 0x%02x", (1 << 3))

	_ = fmt.Sprintf("test")
}
