package gopus_test

import (
	"fmt"
	"math"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestNB10msGainDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name      string
		frameSize int
	}{
		{"NB-10ms", 480},
		{"NB-20ms", 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := encoder.NewEncoder(48000, 1)
			enc.SetMode(encoder.ModeSILK)
			enc.SetBandwidth(types.BandwidthNarrowband)
			enc.SetBitrate(32000)

			dec, err := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: 1})
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}

			// Generate 1 second of chirp signal
			numFrames := 48000 / tc.frameSize
			totalSamples := numFrames * tc.frameSize
			signal := make([]float64, totalSamples)
			for i := range signal {
				ti := float64(i) / 48000.0
				freq := 200.0 + 1800.0*ti
				signal[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
			}

			decodeBuf := make([]float32, 5760)
			var totalInE, totalOutE float64
			var totalIn, totalOut int

			for i := 0; i < numFrames; i++ {
				start := i * tc.frameSize
				end := start + tc.frameSize
				pcm := signal[start:end]

				// Input energy
				var inE float64
				for _, v := range pcm {
					inE += v * v
				}

				pkt, err := enc.Encode(pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("Encode frame %d: %v", i, err)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)

				n, err := dec.Decode(cp, decodeBuf)
				if err != nil {
					t.Fatalf("Decode frame %d: %v", i, err)
				}

				// Output energy
				var outE float64
				for j := 0; j < n; j++ {
					outE += float64(decodeBuf[j]) * float64(decodeBuf[j])
				}

				totalInE += inE
				totalIn += tc.frameSize
				totalOutE += outE
				totalOut += n

				if i >= 3 && i < 8 {
					inRMS := math.Sqrt(inE / float64(tc.frameSize))
					outRMS := math.Sqrt(outE / float64(n))
					t.Logf("Frame %d: TOC=0x%02x, len=%d, n=%d, inRMS=%.4f, outRMS=%.4f, ratio=%.1f%%",
						i, cp[0], len(cp), n, inRMS, outRMS, outRMS/inRMS*100)
				}
			}

			overallInRMS := math.Sqrt(totalInE / float64(totalIn))
			overallOutRMS := math.Sqrt(totalOutE / float64(totalOut))
			t.Logf("Overall: inRMS=%.4f, outRMS=%.4f, ratio=%.1f%%",
				overallInRMS, overallOutRMS, overallOutRMS/overallInRMS*100)

			// Also check TOC byte
			pkt, _ := enc.Encode(signal[:tc.frameSize], tc.frameSize)
			if len(pkt) > 0 {
				toc := pkt[0]
				config := (toc >> 3) & 0x1f
				t.Logf("TOC: 0x%02x, config=%d, stereo=%v, code=%d",
					toc, config, (toc&4) != 0, toc&3)
			}

			// Check if blow-up exceeds threshold
			if overallOutRMS/overallInRMS > 1.5 {
				t.Logf("WARNING: Amplitude blow-up detected: %.1f%%", overallOutRMS/overallInRMS*100)
			}
		})
	}
}

func TestNB10msPacketDecode(t *testing.T) {
	// Encode NB-10ms and decode - check packet structure
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeSILK)
	enc.SetBandwidth(types.BandwidthNarrowband)
	enc.SetBitrate(32000)

	// Simple sine
	frameSize := 480 // 10ms
	pcm := make([]float64, frameSize)
	for i := range pcm {
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440.0*float64(i)/48000.0)
	}

	// Warm up
	for i := 0; i < 5; i++ {
		pkt, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode warmup %d: %v", i, err)
		}
		if len(pkt) > 0 {
			toc := pkt[0]
			config := (toc >> 3) & 0x1f
			frameCode := toc & 3
			t.Logf("Pkt %d: len=%d, TOC=0x%02x (config=%d, code=%d)", i, len(pkt), toc, config, frameCode)
			if config > 15 {
				t.Errorf("Expected SILK config (0-11), got %d", config)
			}
			// Dump first few payload bytes
			payloadLen := len(pkt) - 1
			preview := 8
			if preview > payloadLen {
				preview = payloadLen
			}
			t.Logf("  Payload[0:%d]: %v", preview, pkt[1:1+preview])
		}
	}
	
	// Now encode 10 frames and look at packet sizes 
	fmt.Println("NB-10ms packet sizes after warmup:")
	for i := 0; i < 10; i++ {
		pkt, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode %d: %v", i, err)
		}
		fmt.Printf("  frame %d: %d bytes\n", i+5, len(pkt))
	}
}
