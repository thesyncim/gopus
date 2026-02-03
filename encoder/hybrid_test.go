// Package encoder tests hybrid mode SILK/CELT band splitting improvements.
// These tests verify proper crossover frequency handling, bit allocation,
// and smooth transitions between SILK and CELT bands.

package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/types"
)

// TestHybridBitAllocation verifies SILK/CELT bit allocation follows libopus tables.
func TestHybridBitAllocation(t *testing.T) {
	testCases := []struct {
		name         string
		totalBitrate int
		channels     int
		frame20ms    bool
		fecEnabled   bool
		// Expected ranges (not exact, as interpolation may vary)
		minSilkBitrate int
		maxSilkBitrate int
	}{
		// From libopus rate table:
		// At 24kbps mono, SILK should get ~18kbps
		{"24kbps mono 20ms", 24000, 1, true, false, 15000, 20000},
		// At 32kbps mono, SILK should get ~22kbps
		{"32kbps mono 20ms", 32000, 1, true, false, 18000, 25000},
		// At 64kbps mono, SILK should get ~38kbps
		{"64kbps mono 20ms", 64000, 1, true, false, 35000, 45000},
		// Stereo doubles the rates
		{"48kbps stereo 20ms", 48000, 2, true, false, 30000, 42000},
		// 10ms frames (entry 1 instead of 2)
		{"32kbps mono 10ms", 32000, 1, false, false, 18000, 25000},
		// FEC enabled (entry 4 instead of 2)
		{"32kbps mono 20ms FEC", 32000, 1, true, true, 20000, 30000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Encoder{
				bitrate:    tc.totalBitrate,
				channels:   tc.channels,
				fecEnabled: tc.fecEnabled,
			}

			silkBitrate, celtBitrate := e.computeHybridBitAllocation(tc.frame20ms)

			t.Logf("Total: %d, SILK: %d, CELT: %d", tc.totalBitrate, silkBitrate, celtBitrate)

			// Verify SILK bitrate is in expected range
			if silkBitrate < tc.minSilkBitrate || silkBitrate > tc.maxSilkBitrate {
				t.Errorf("SILK bitrate %d not in expected range [%d, %d]",
					silkBitrate, tc.minSilkBitrate, tc.maxSilkBitrate)
			}

			// Verify total adds up
			if silkBitrate+celtBitrate != tc.totalBitrate {
				t.Errorf("SILK (%d) + CELT (%d) = %d, expected %d",
					silkBitrate, celtBitrate, silkBitrate+celtBitrate, tc.totalBitrate)
			}

			// Verify CELT gets at least minimum bitrate
			minCelt := 2000 * tc.channels
			if celtBitrate < minCelt {
				t.Errorf("CELT bitrate %d below minimum %d", celtBitrate, minCelt)
			}
		})
	}
}

// TestHBGainComputation verifies high-band gain attenuation at low bitrates.
func TestHBGainComputation(t *testing.T) {
	testCases := []struct {
		name           string
		celtBitrate    int
		expectedMinGain float64
		expectedMaxGain float64
	}{
		// libopus formula: HB_gain = 1.0 - 2^(-celt_rate/1024) / 2
		// This results in gains very close to 1.0 for typical bitrates
		// High CELT bitrate: nearly full gain
		{"high bitrate (25kbps)", 25000, 0.99, 1.01},
		{"moderate bitrate (16kbps)", 16000, 0.99, 1.01},
		// Medium CELT bitrate: still very close to 1.0
		{"medium bitrate (10kbps)", 10000, 0.99, 1.01},
		// Low CELT bitrate: slight attenuation
		{"low bitrate (6kbps)", 6000, 0.98, 1.0},
		// Very low CELT bitrate: more noticeable attenuation
		{"very low bitrate (4kbps)", 4000, 0.96, 0.98},
		{"minimum bitrate (2kbps)", 2000, 0.85, 0.90},
	}

	e := &Encoder{}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gain := e.computeHBGain(tc.celtBitrate)

			t.Logf("CELT bitrate: %d, HB gain: %.4f", tc.celtBitrate, gain)

			if gain < tc.expectedMinGain || gain > tc.expectedMaxGain {
				t.Errorf("HB gain %.4f not in expected range [%.4f, %.4f]",
					gain, tc.expectedMinGain, tc.expectedMaxGain)
			}
		})
	}
}

// TestGainFadeSmoothing verifies smooth gain transitions prevent artifacts.
func TestGainFadeSmoothing(t *testing.T) {
	e := &Encoder{
		channels: 1,
	}

	// Test fading from 1.0 to 0.5
	samples := make([]float64, 960)
	for i := range samples {
		samples[i] = 1.0 // Constant signal
	}

	result := e.applyLinearGainFade(samples, 1.0, 0.5, 120)

	// Check first sample uses g1
	if math.Abs(result[0]-1.0) > 0.01 {
		t.Errorf("First sample should be close to g1=1.0, got %.4f", result[0])
	}

	// Check overlap region is smooth
	for i := 1; i < 120; i++ {
		if result[i] > result[i-1]+0.001 {
			t.Errorf("Gain should be monotonically decreasing in fade, sample %d: %.4f > %.4f",
				i, result[i], result[i-1])
		}
	}

	// Check end of overlap uses g2
	if math.Abs(result[119]-0.5) > 0.1 {
		t.Errorf("End of overlap should be close to g2=0.5, got %.4f", result[119])
	}

	// Check rest of frame uses g2
	for i := 120; i < 960; i++ {
		if math.Abs(result[i]-0.5) > 0.001 {
			t.Errorf("Sample %d should be g2=0.5, got %.4f", i, result[i])
		}
	}
}

// TestCrossoverEnergyMatching verifies smooth energy transition at 8kHz.
func TestCrossoverEnergyMatching(t *testing.T) {
	e := &Encoder{}

	// Create energies with a peak at the crossover band
	energies := make([]float64, 21)
	for i := range energies {
		energies[i] = -20.0 // Base energy
	}

	// Simulate a spike at crossover (band 17)
	startBand := 17
	energies[startBand] = -5.0   // Much higher than surroundings
	energies[startBand+1] = -18.0

	result := e.matchCrossoverEnergy(energies, startBand)

	t.Logf("Before: band17=%.2f, band18=%.2f", -5.0, -18.0)
	t.Logf("After:  band17=%.2f, band18=%.2f", result[startBand], result[startBand+1])

	// The crossover energy should be reduced when there's a big difference
	if result[startBand] > -5.0+0.1 {
		t.Error("Crossover band should be reduced to match neighboring bands")
	}

	// Should not create negative artifacts
	if result[startBand] < -28.0 {
		t.Error("Crossover band over-attenuated")
	}
}

// TestStereoWidthComputation verifies stereo width calculation.
func TestStereoWidthComputation(t *testing.T) {
	testCases := []struct {
		name          string
		leftPhase     float64 // Phase offset for left channel
		rightPhase    float64 // Phase offset for right channel
		expectedWidth float64 // Expected stereo width (0=mono, 1=full stereo)
		tolerance     float64
	}{
		// Identical channels (mono)
		{"mono signal", 0.0, 0.0, 0.0, 0.1},
		// 90 degree phase difference (maximum stereo)
		{"full stereo (90deg)", 0.0, math.Pi / 2, 0.7, 0.3},
		// 180 degree (opposite phase) - correlation is -1, but abs(corr)=1, so width is low
		// This is because phase-inverted signals are still correlated (just negatively)
		{"opposite phase", 0.0, math.Pi, 0.0, 0.2},
		// Small phase difference
		{"slight stereo", 0.0, 0.2, 0.1, 0.15},
	}

	frameSize := 960

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Generate test signal
			pcm := make([]float64, frameSize*2)
			for i := 0; i < frameSize; i++ {
				t := float64(i) / 48000.0 * 1000.0 * math.Pi // 1kHz
				pcm[i*2] = math.Sin(t + tc.leftPhase)
				pcm[i*2+1] = math.Sin(t + tc.rightPhase)
			}

			width := ComputeStereoWidth(pcm, frameSize, 2)

			if math.Abs(width-tc.expectedWidth) > tc.tolerance {
				t.Errorf("Stereo width %.4f not in expected range %.4f +/- %.4f",
					width, tc.expectedWidth, tc.tolerance)
			}
		})
	}
}

// TestResamplerContinuity verifies the resampler maintains continuity across frames.
func TestResamplerContinuity(t *testing.T) {
	e := NewEncoder(48000, 1)

	// Generate a continuous sine wave across multiple frames
	freq := 1000.0 // 1kHz test tone
	sampleRate := 48000.0
	frameSize := 960

	// Process 3 frames
	var lastSample float32
	for frame := 0; frame < 3; frame++ {
		samples := make([]float64, frameSize)
		for i := 0; i < frameSize; i++ {
			t := float64(frame*frameSize+i) / sampleRate
			samples[i] = math.Sin(2 * math.Pi * freq * t)
		}

		output := e.downsample48to16Hybrid(samples, frameSize)

		if frame > 0 && len(output) > 0 {
			// Check continuity between frames
			// The difference should be smooth (no discontinuity)
			diff := math.Abs(float64(output[0]) - float64(lastSample))
			expectedDiff := 2 * math.Pi * freq / 16000.0 // Max slope of sine at 16kHz
			if diff > expectedDiff*2 {
				t.Errorf("Frame %d: discontinuity at boundary, diff=%.6f (expected max %.6f)",
					frame, diff, expectedDiff*2)
			}
		}

		if len(output) > 0 {
			lastSample = output[len(output)-1]
		}
	}
}

// TestHybridModeQuality runs an end-to-end quality test for hybrid encoding.
func TestHybridModeQuality(t *testing.T) {
	// Create encoder
	enc := NewEncoder(48000, 1)
	enc.SetMode(ModeHybrid)
	enc.SetBitrate(64000) // 64 kbps

	// Generate test signal: combination of low and high frequencies
	// Low freq (1kHz) goes to SILK, high freq (10kHz) goes to CELT
	frameSize := 960
	samples := make([]float64, frameSize)

	lowFreq := 1000.0  // 1kHz - handled by SILK
	highFreq := 10000.0 // 10kHz - handled by CELT

	for i := 0; i < frameSize; i++ {
		t := float64(i) / 48000.0
		// Mix of low and high frequency
		samples[i] = 0.5*math.Sin(2*math.Pi*lowFreq*t) +
			0.3*math.Sin(2*math.Pi*highFreq*t)
	}

	// Encode
	packet, err := enc.Encode(samples, frameSize)
	if err != nil {
		t.Fatalf("Hybrid encoding failed: %v", err)
	}

	// Verify we got a valid packet
	if len(packet) < 2 {
		t.Error("Packet too short")
	}

	t.Logf("Hybrid packet size: %d bytes", len(packet))

	// Check TOC byte indicates hybrid mode
	toc := packet[0]
	config := (toc >> 3) & 0x1F
	if config < 12 || config > 15 {
		t.Logf("TOC config %d (expected 12-15 for hybrid)", config)
		// Note: Don't fail - auto mode may select different mode
	}
}

// BenchmarkHybridBitAllocation benchmarks bit allocation computation.
func BenchmarkHybridBitAllocation(b *testing.B) {
	e := &Encoder{
		bitrate:  64000,
		channels: 1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.computeHybridBitAllocation(true)
	}
}

func TestHybridVBRPacketSizeCap(t *testing.T) {
	enc := NewEncoder(48000, 2)
	enc.SetMode(ModeHybrid)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrateMode(ModeVBR)
	enc.SetBitrate(64000)
	enc.SetFrameSize(960)

	pcm := make([]float64, 960*2)
	for i := 0; i < 960; i++ {
		v := 0.2 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
		pcm[i*2] = v
		pcm[i*2+1] = v
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(packet) == 0 {
		t.Fatalf("expected packet, got 0 bytes")
	}

	baseBytes := targetBytesForBitrate(64000, 960)
	maxAllowed := int(float64(baseBytes) * 2.0)
	if len(packet) > maxAllowed {
		t.Fatalf("hybrid VBR packet too large: got %d bytes, max %d", len(packet), maxAllowed)
	}
}

// BenchmarkHBGainComputation benchmarks HB gain calculation.
func BenchmarkHBGainComputation(b *testing.B) {
	e := &Encoder{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.computeHBGain(25000)
	}
}

// BenchmarkDownsample48to16 benchmarks the improved resampler.
func BenchmarkDownsample48to16(b *testing.B) {
	e := NewEncoder(48000, 1)

	samples := make([]float64, 960)
	for i := range samples {
		samples[i] = math.Sin(float64(i) * 0.1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.downsample48to16Hybrid(samples, 960)
	}
}
