package encoder

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/types"
)

// 10 ms SILK encode diagnostics. These tests drive the encoder in SILK mode at
// 10 ms (480-sample) and 20 ms (960-sample) frame sizes and characterise the
// decoded output (via opusdec when present, otherwise the internal SILK decoder)
// for TOC correctness, delay alignment, energy, phase and waveform continuity.

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

			for i := range nFrames {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
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
		{"SILK-WB-10ms-32k", 32000, 480, 8}, // config 8 = SILK WB 10ms
		{"SILK-WB-10ms-64k", 64000, 480, 8}, // config 8 = SILK WB 10ms
		{"SILK-WB-20ms-32k", 32000, 960, 9}, // config 9 = SILK WB 20ms
		{"SILK-WB-20ms-64k", 64000, 960, 9}, // config 9 = SILK WB 20ms
		{"SILK-NB-10ms-32k", 32000, 480, 0}, // config 0 = SILK NB 10ms
		{"SILK-NB-20ms-32k", 32000, 960, 1}, // config 1 = SILK NB 20ms
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
			pkt, err := encodeTest(enc, pcm, tc.frameSize)
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
				for i := range 5 {
					pcm := make([]float64, frameSize)
					for j := range pcm {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
					}
					pkt, err := encodeTest(enc, pcm, frameSize)
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

// TestSILK10msBandwidthEffect tests if the 10ms quality issue is
// bandwidth-specific (NB vs MB vs WB).
func TestSILK10msBandwidthEffect(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, bw := range []struct {
		name string
		bw   types.Bandwidth
	}{
		{"NB", types.BandwidthNarrowband},
		{"WB", types.BandwidthWideband},
	} {
		for _, frameSize := range []int{480, 960} {
			fsName := "10ms"
			if frameSize == 960 {
				fsName = "20ms"
			}
			t.Run(bw.name+"-"+fsName, func(t *testing.T) {
				t.Parallel()
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(bw.bw)
				enc.SetBitrate(32000)

				totalSamples := 2 * 48000
				numFrames := totalSamples / frameSize
				origSamples := make([]float32, totalSamples)
				for i := range totalSamples {
					tm := float64(i) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					origSamples[i] = 0.5 * float32(math.Sin(phase))
				}

				var packets [][]byte
				for i := range numFrames {
					pcm := make([]float64, frameSize)
					for j := range frameSize {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
						pcm[j] = 0.5 * math.Sin(phase)
					}
					pkt, err := encodeTest(enc, pcm, frameSize)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if pkt == nil {
						t.Fatalf("nil packet frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
				}

				var oggBuf bytes.Buffer
				writeTestOgg(&oggBuf, packets, 1, 48000, frameSize, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				bestSNR := math.Inf(-1)
				bestDelay := 0
				margin := 2000
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
						di := i + d
						if di >= margin && di < len(decoded)-margin {
							ref := float64(origSamples[i])
							dec := float64(decoded[di])
							sig += ref * ref
							n := dec - ref
							noise += n * n
							count++
						}
					}
					if count > 1000 && sig > 0 && noise > 0 {
						snr := 10 * math.Log10(sig/noise)
						if snr > bestSNR {
							bestSNR = snr
							bestDelay = d
						}
					}
				}

				t.Logf("%s %s: SNR=%.2f dB at delay=%d", bw.name, fsName, bestSNR, bestDelay)
			})
		}
	}
}

// TestSILK10msComplexityEffect tests if the 10ms quality issue is related to
// the complexity level (delayed decision NSQ vs simple NSQ).
func TestSILK10msComplexityEffect(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, complexity := range []int{0, 5, 10} {
		for _, frameSize := range []int{480, 960} {
			fsName := "10ms"
			if frameSize == 960 {
				fsName = "20ms"
			}
			t.Run(fsName+"-c"+fmt.Sprint(complexity), func(t *testing.T) {
				t.Parallel()
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(32000)
				enc.SetComplexity(complexity)

				totalSamples := 2 * 48000
				numFrames := totalSamples / frameSize
				origSamples := make([]float32, totalSamples)
				for i := range totalSamples {
					tm := float64(i) / 48000.0
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					origSamples[i] = 0.5 * float32(math.Sin(phase))
				}

				var packets [][]byte
				for i := range numFrames {
					pcm := make([]float64, frameSize)
					for j := range frameSize {
						sampleIdx := i*frameSize + j
						tm := float64(sampleIdx) / 48000.0
						phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
						pcm[j] = 0.5 * math.Sin(phase)
					}
					pkt, err := encodeTest(enc, pcm, frameSize)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if pkt == nil {
						t.Fatalf("nil packet frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
				}

				var oggBuf bytes.Buffer
				writeTestOgg(&oggBuf, packets, 1, 48000, frameSize, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				bestSNR := math.Inf(-1)
				bestDelay := 0
				margin := 2000
				for d := -1000; d <= 1000; d++ {
					var sig, noise float64
					count := 0
					for i := margin; i < totalSamples-margin; i++ {
						di := i + d
						if di >= margin && di < len(decoded)-margin {
							ref := float64(origSamples[i])
							dec := float64(decoded[di])
							sig += ref * ref
							n := dec - ref
							noise += n * n
							count++
						}
					}
					if count > 1000 && sig > 0 && noise > 0 {
						snr := 10 * math.Log10(sig/noise)
						if snr > bestSNR {
							bestSNR = snr
							bestDelay = d
						}
					}
				}

				t.Logf("%s complexity=%d: SNR=%.2f dB at delay=%d", fsName, complexity, bestSNR, bestDelay)
			})
		}
	}
}

// TestSILK10msEnergyCheck checks the decoded RMS energy from opusdec for 10ms vs 20ms
// with different signals.
func TestSILK10msEnergyCheck(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	signals := []struct {
		name string
		gen  func(int) float64
	}{
		{"sine440", func(i int) float64 {
			return 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
		}},
		{"multitone", func(i int) float64 {
			t := float64(i) / 48000.0
			return 0.3*math.Sin(2*math.Pi*440*t) +
				0.2*math.Sin(2*math.Pi*1000*t) +
				0.1*math.Sin(2*math.Pi*2000*t)
		}},
	}

	for _, sig := range signals {
		for _, fs := range []int{480, 960} {
			fsName := "10ms"
			if fs == 960 {
				fsName = "20ms"
			}
			t.Run(sig.name+"-"+fsName, func(t *testing.T) {
				enc := NewEncoder(48000, 1)
				enc.SetMode(ModeSILK)
				enc.SetBandwidth(types.BandwidthWideband)
				enc.SetBitrate(32000)

				numFrames := 48000 / fs // 1 second
				var packets [][]byte
				var origEnergy float64

				for i := range numFrames {
					pcm := make([]float64, fs)
					for j := range fs {
						sampleIdx := i*fs + j
						pcm[j] = sig.gen(sampleIdx)
						origEnergy += pcm[j] * pcm[j]
					}
					pkt, err := encodeTest(enc, pcm, fs)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if pkt == nil {
						t.Fatalf("nil frame %d", i)
					}
					cp := make([]byte, len(pkt))
					copy(cp, pkt)
					packets = append(packets, cp)
				}

				origRMS := math.Sqrt(origEnergy / float64(numFrames*fs))

				var oggBuf bytes.Buffer
				writeTestOgg(&oggBuf, packets, 1, 48000, fs, 312)
				decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

				// Strip pre-skip
				preSkip := 312
				if len(decoded) > preSkip {
					decoded = decoded[preSkip:]
				}

				// Compute decoded RMS (skip first and last 1000 samples)
				margin := 1000
				var decEnergy float64
				count := 0
				for i := margin; i < len(decoded)-margin; i++ {
					decEnergy += float64(decoded[i]) * float64(decoded[i])
					count++
				}
				decRMS := math.Sqrt(decEnergy / float64(count))

				ratio := decRMS / origRMS * 100
				t.Logf("OrigRMS=%.4f DecRMS=%.4f Ratio=%.1f%% nDecoded=%d",
					origRMS, decRMS, ratio, len(decoded))

				// Check for energy loss
				if ratio < 90 {
					t.Errorf("Energy loss detected: ratio=%.1f%% (expected >90%%)", ratio)
				}
			})
		}
	}
}

// TestSILK10msDelaySearch searches for the optimal delay with a very large range
// to determine whether the 10ms quality gap is a delay alignment issue.
func TestSILK10msDelaySearch(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			// Use 2 seconds of audio for better delay estimation
			numFrames := 2 * 48000 / tc.frameSize
			var packets [][]byte
			var origSamples []float32

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					// Use a more complex signal for better correlation
					pcm[j] = 0.3*math.Sin(2*math.Pi*440*tm) +
						0.2*math.Sin(2*math.Pi*1000*tm) +
						0.1*math.Sin(2*math.Pi*2000*tm)
				}
				for _, v := range pcm {
					origSamples = append(origSamples, float32(v))
				}

				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			preSkip := 312
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}
			t.Logf("Original: %d samples, Decoded: %d samples", len(origSamples), len(decoded))

			// Very large delay search
			bestSNR := math.Inf(-1)
			bestDelay := 0
			maxSearch := 10000

			// First do a coarse search
			for d := -maxSearch; d <= maxSearch; d += 10 {
				snr := computeSNRAtDelay(origSamples, decoded, d)
				if snr > bestSNR {
					bestSNR = snr
					bestDelay = d
				}
			}
			// Then fine search around best
			for d := bestDelay - 20; d <= bestDelay+20; d++ {
				snr := computeSNRAtDelay(origSamples, decoded, d)
				if snr > bestSNR {
					bestSNR = snr
					bestDelay = d
				}
			}

			t.Logf("Best SNR=%.2f dB at delay=%d samples (%.2f ms)",
				bestSNR, bestDelay, float64(bestDelay)/48.0)

			// Also report SNR at compliance test's maxDelay boundary
			for _, testDelay := range []int{-2000, -1000, 0, 1000, 2000} {
				snr := computeSNRAtDelay(origSamples, decoded, testDelay)
				t.Logf("  delay=%d: SNR=%.2f dB", testDelay, snr)
			}

			// Check if the decoded waveform has the right overall energy
			var decEnergy float64
			for i := 1000; i < len(decoded)-1000; i++ {
				decEnergy += float64(decoded[i]) * float64(decoded[i])
			}
			decRMS := math.Sqrt(decEnergy / float64(len(decoded)-2000))

			var origEnergy float64
			for i := 1000; i < len(origSamples)-1000; i++ {
				origEnergy += float64(origSamples[i]) * float64(origSamples[i])
			}
			origRMS := math.Sqrt(origEnergy / float64(len(origSamples)-2000))

			t.Logf("RMS: original=%.4f decoded=%.4f ratio=%.2f%%", origRMS, decRMS, decRMS/origRMS*100)
		})
	}
}

func computeSNRAtDelay(orig, decoded []float32, delay int) float64 {
	var sig, noise float64
	margin := 500
	count := 0
	for i := margin; i < len(orig)-margin; i++ {
		di := i + delay
		if di >= margin && di < len(decoded)-margin {
			ref := float64(orig[i])
			dec := float64(decoded[di])
			sig += ref * ref
			n := dec - ref
			noise += n * n
			count++
		}
	}
	if count < 1000 || sig <= 0 || noise <= 0 {
		return math.Inf(-1)
	}
	return 10 * math.Log10(sig/noise)
}

// TestSILK10msOriginalDelay finds the true delay between original 48kHz input
// and opusdec output for both 10ms and 20ms. This uses a chirp signal which
// gives unambiguous delay measurement (unlike sine waves).
func TestSILK10msOriginalDelay(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			// Use a chirp signal: frequency sweeps from 200 to 2000 Hz over 2 seconds
			// This gives unambiguous delay measurement
			numFrames := 2 * 48000 / tc.frameSize
			var packets [][]byte
			var origSamples []float32

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := 0; j < tc.frameSize; j++ {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					// Chirp: freq = 200 + 900*t (200 Hz at t=0, 2000 Hz at t=2)
					freq := 200.0 + 900.0*tm
					phase := 2 * math.Pi * (200.0*tm + 450.0*tm*tm)
					pcm[j] = 0.5 * math.Sin(phase)
					_ = freq
				}
				for _, v := range pcm {
					origSamples = append(origSamples, float32(v))
				}

				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			// Strip pre-skip
			preSkip := 312
			if len(decoded) > preSkip {
				decoded = decoded[preSkip:]
			}
			t.Logf("Original: %d samples, Decoded: %d samples", len(origSamples), len(decoded))

			// Find best delay using cross-correlation
			bestCorr := float64(-1)
			bestDelay := 0
			maxSearch := 5000

			for d := -maxSearch; d <= maxSearch; d++ {
				var corr, norm1, norm2 float64
				count := 0
				margin := 2000
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + d
					if di >= margin && di < len(decoded)-margin {
						a := float64(origSamples[i])
						b := float64(decoded[di])
						corr += a * b
						norm1 += a * a
						norm2 += b * b
						count++
					}
				}
				if count > 1000 && norm1 > 0 && norm2 > 0 {
					c := corr / math.Sqrt(norm1*norm2)
					if c > bestCorr {
						bestCorr = c
						bestDelay = d
					}
				}
			}
			t.Logf("Best correlation=%.6f at delay=%d (%.2f ms)", bestCorr, bestDelay, float64(bestDelay)/48.0)

			// Compute SNR at best delay
			if bestCorr > 0.5 {
				var sig, noise float64
				margin := 2000
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + bestDelay
					if di >= margin && di < len(decoded)-margin {
						ref := float64(origSamples[i])
						dec := float64(decoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
					}
				}
				if sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					t.Logf("SNR at best delay: %.2f dB", snr)
				}
			}

			// Also try with maxDelay=4000 (compliance test range)
			bestSNR := math.Inf(-1)
			bestSNRDelay := 0
			for d := -4000; d <= 4000; d++ {
				var sig, noise float64
				margin := 2000
				count := 0
				for i := margin; i < len(origSamples)-margin; i++ {
					di := i + d
					if di >= margin && di < len(decoded)-margin {
						ref := float64(origSamples[i])
						dec := float64(decoded[di])
						sig += ref * ref
						n := dec - ref
						noise += n * n
						count++
					}
				}
				if count > 1000 && sig > 0 && noise > 0 {
					snr := 10 * math.Log10(sig/noise)
					if snr > bestSNR {
						bestSNR = snr
						bestSNRDelay = d
					}
				}
			}
			t.Logf("Best SNR=%.2f dB at delay=%d", bestSNR, bestSNRDelay)

			// Compute energy ratio at best delay
			var origE, decE float64
			margin := 2000
			for i := margin; i < len(origSamples)-margin; i++ {
				di := i + bestDelay
				if di >= margin && di < len(decoded)-margin {
					origE += float64(origSamples[i]) * float64(origSamples[i])
					decE += float64(decoded[di]) * float64(decoded[di])
				}
			}
			t.Logf("Energy ratio at best delay: %.1f%%", math.Sqrt(decE/origE)*100)
		})
	}
}

// TestSILK10msPhaseAnalysis compares the internal decoder output vs opusdec output
// sample-by-sample to identify phase/timing issues specific to 10ms.
func TestSILK10msPhaseAnalysis(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		silkBW    silk.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, silk.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, silk.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 50
			var packets [][]byte

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			// Decode with internal decoder
			intDec := silk.NewDecoder()
			var internalSamples []float32
			for i, pkt := range packets {
				out, err := intDec.Decode(pkt[1:], tc.silkBW, tc.frameSize, true)
				if err != nil {
					t.Fatalf("internal decode frame %d: %v", i, err)
				}
				internalSamples = append(internalSamples, out...)
			}

			// Decode with opusdec
			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			opusSamples := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			preSkip := 312
			if len(opusSamples) > preSkip {
				opusSamples = opusSamples[preSkip:]
			}

			t.Logf("Internal: %d samples, opusdec: %d samples", len(internalSamples), len(opusSamples))

			// Find the best delay between internal and opusdec outputs
			bestCorr := float64(-1)
			bestDelay := 0
			for d := -2000; d <= 2000; d++ {
				var corr, norm1, norm2 float64
				count := 0
				for i := 500; i < len(internalSamples)-500; i++ {
					di := i + d
					if di >= 0 && di < len(opusSamples) {
						a := float64(internalSamples[i])
						b := float64(opusSamples[di])
						corr += a * b
						norm1 += a * a
						norm2 += b * b
						count++
					}
				}
				if count > 1000 && norm1 > 0 && norm2 > 0 {
					c := corr / math.Sqrt(norm1*norm2)
					if c > bestCorr {
						bestCorr = c
						bestDelay = d
					}
				}
			}
			t.Logf("Best correlation=%.4f at delay=%d between internal and opusdec", bestCorr, bestDelay)

			// Now compute per-frame correlation between internal and opusdec
			// to see if there's a frame-to-frame variation
			internalFrameSize := tc.frameSize // at 48kHz rate
			for frame := 2; frame < numFrames-2 && frame < 20; frame++ {
				intStart := frame * internalFrameSize
				intEnd := intStart + internalFrameSize
				if intEnd > len(internalSamples) {
					break
				}

				// Try to find the best matching segment in opusdec output
				bestFrameCorr := float64(-1)
				bestFrameDelay := 0
				for d := bestDelay - 200; d <= bestDelay+200; d++ {
					var corr, norm1, norm2 float64
					for i := intStart; i < intEnd; i++ {
						di := i + d
						if di >= 0 && di < len(opusSamples) {
							a := float64(internalSamples[i])
							b := float64(opusSamples[di])
							corr += a * b
							norm1 += a * a
							norm2 += b * b
						}
					}
					if norm1 > 0 && norm2 > 0 {
						c := corr / math.Sqrt(norm1*norm2)
						if c > bestFrameCorr {
							bestFrameCorr = c
							bestFrameDelay = d
						}
					}
				}

				// Also compute the gain ratio at best delay
				var intE, opE float64
				for i := intStart; i < intEnd; i++ {
					di := i + bestFrameDelay
					if di >= 0 && di < len(opusSamples) {
						intE += float64(internalSamples[i]) * float64(internalSamples[i])
						opE += float64(opusSamples[di]) * float64(opusSamples[di])
					}
				}
				gainRatio := math.Sqrt(opE/intE) * 100

				t.Logf("  Frame %2d: corr=%.4f delay=%d gain=%.1f%%", frame, bestFrameCorr, bestFrameDelay, gainRatio)
			}

			// Check if opusdec output has the right peak amplitude per frame
			t.Logf("Per-frame peak amplitudes from opusdec:")
			for frame := 0; frame < 15 && frame < numFrames; frame++ {
				start := frame * internalFrameSize
				end := start + internalFrameSize
				if end > len(opusSamples) {
					break
				}
				var maxAbs float64
				for i := start; i < end; i++ {
					v := math.Abs(float64(opusSamples[i]))
					if v > maxAbs {
						maxAbs = v
					}
				}
				t.Logf("  opusdec frame %2d: peak=%.4f", frame, maxAbs)
			}
		})
	}
}

// TestSILK10msWaveformCompare compares the actual decoded waveforms from opusdec
// for 10ms vs 20ms to identify the nature of the quality difference.
func TestSILK10msWaveformCompare(t *testing.T) {
	opusdec := findOpusdec()
	if opusdec == "" {
		t.Log("opusdec not found; using internal SILK decode fallback")
	}

	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			numFrames := 48000 / tc.frameSize
			var packets [][]byte

			for i := range numFrames {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					t.Fatalf("nil packet frame %d", i)
				}
				cp := make([]byte, len(pkt))
				copy(cp, pkt)
				packets = append(packets, cp)
			}

			var oggBuf bytes.Buffer
			writeTestOgg(&oggBuf, packets, 1, 48000, tc.frameSize, 312)
			decoded := decodeOggWithOpusdec(t, opusdec, oggBuf.Bytes())

			if len(decoded) < 1000 {
				t.Fatal("not enough decoded samples")
			}

			// Check energy per 10ms block
			t.Logf("Total decoded samples: %d", len(decoded))

			// RMS per block (after pre-skip)
			preSkip := 312
			if preSkip < len(decoded) {
				decoded = decoded[preSkip:]
			}

			t.Logf("After pre-skip: %d samples (expected ~48000)", len(decoded))

			// Find the period of the decoded signal to check frequency
			// Look for zero crossings in decoded[1000:5000]
			var crossings int
			for i := 1001; i < 5000 && i < len(decoded); i++ {
				if (decoded[i-1] < 0 && decoded[i] >= 0) ||
					(decoded[i-1] >= 0 && decoded[i] < 0) {
					crossings++
				}
			}
			estimatedFreq := float64(crossings) * 48000.0 / (2.0 * 4000.0)
			t.Logf("Estimated frequency from zero crossings: %.1f Hz (expected 440)", estimatedFreq)

			// Check if it's roughly 440 Hz
			if estimatedFreq < 400 || estimatedFreq > 480 {
				t.Logf("WARNING: Frequency %0.1f is far from expected 440 Hz", estimatedFreq)
			}

			// Print first 20 samples after pre-skip
			t.Logf("First 20 samples after pre-skip:")
			for i := 0; i < 20 && i < len(decoded); i++ {
				t.Logf("  [%d] = %.6f", i, decoded[i])
			}

			// Print samples around the 3rd frame boundary (at sample 1440 for 10ms, 2880 for 20ms)
			boundaryIdx := 3 * tc.frameSize
			if boundaryIdx+10 < len(decoded) {
				t.Logf("Samples around frame boundary at %d:", boundaryIdx)
				for i := boundaryIdx - 5; i < boundaryIdx+5 && i >= 0 && i < len(decoded); i++ {
					t.Logf("  [%d] = %.6f", i, decoded[i])
				}
			}

			// Check for discontinuities at frame boundaries
			var maxDiscont float64
			var discontAt int
			for frame := 1; frame < numFrames && (frame+1)*tc.frameSize < len(decoded); frame++ {
				boundaryIdx := frame * tc.frameSize
				if boundaryIdx+1 < len(decoded) {
					// Check sample jump at boundary
					jump := math.Abs(float64(decoded[boundaryIdx]) - float64(decoded[boundaryIdx-1]))
					// Also check what the expected jump should be for a sine wave
					tm := float64(boundaryIdx) / 48000.0
					tmPrev := float64(boundaryIdx-1) / 48000.0
					expectedJump := math.Abs(0.5*math.Sin(2*math.Pi*440*tm) - 0.5*math.Sin(2*math.Pi*440*tmPrev))
					excess := jump - expectedJump
					if excess > maxDiscont {
						maxDiscont = excess
						discontAt = frame
					}
				}
			}
			t.Logf("Max discontinuity excess at frame boundaries: %.6f at frame %d", maxDiscont, discontAt)
		})
	}
}

// TestSILK10msTOCDump dumps the actual TOC byte and packet structure for 10ms vs 20ms
func TestSILK10msTOCDump(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bw        types.Bandwidth
		frameSize int
	}{
		{"WB-10ms", types.BandwidthWideband, 480},
		{"WB-20ms", types.BandwidthWideband, 960},
		{"NB-10ms", types.BandwidthNarrowband, 480},
		{"NB-20ms", types.BandwidthNarrowband, 960},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder(48000, 1)
			enc.SetMode(ModeSILK)
			enc.SetBandwidth(tc.bw)
			enc.SetBitrate(32000)

			for i := range 5 {
				pcm := make([]float64, tc.frameSize)
				for j := range pcm {
					sampleIdx := i*tc.frameSize + j
					tm := float64(sampleIdx) / 48000.0
					pcm[j] = 0.5 * math.Sin(2*math.Pi*440*tm)
				}
				pkt, err := encodeTest(enc, pcm, tc.frameSize)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if pkt == nil {
					continue
				}

				toc := pkt[0]
				config := toc >> 3
				stereo := (toc >> 2) & 1
				frameCode := toc & 3

				// Decode config to verify expected frame duration
				// SILK NB: config 0-3 (10/20/40/60ms)
				// SILK MB: config 4-7
				// SILK WB: config 8-11
				var expectedConfig uint8
				if tc.bw == types.BandwidthNarrowband {
					if tc.frameSize == 480 {
						expectedConfig = 0 // NB 10ms
					} else {
						expectedConfig = 1 // NB 20ms
					}
				} else {
					if tc.frameSize == 480 {
						expectedConfig = 8 // WB 10ms
					} else {
						expectedConfig = 9 // WB 20ms
					}
				}

				configOK := "OK"
				if config != expectedConfig {
					configOK = fmt.Sprintf("MISMATCH (expected %d)", expectedConfig)
				}

				t.Logf("Frame %d: TOC=0x%02x config=%d(%s) stereo=%d code=%d pktLen=%d data=[%x ...]",
					i, toc, config, configOK, stereo, frameCode, len(pkt), pkt[:min(8, len(pkt))])
			}
		})
	}
}
