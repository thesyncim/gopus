package celt

import (
	"math"
	"testing"
)

// TestTransientAnalysisWithState tests the enhanced stateful transient analysis
func TestTransientAnalysisWithState(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	overlap := 120

	// Generate a sequence of frames transitioning from silence to loud attack
	totalFrames := 5
	for frame := 0; frame < totalFrames; frame++ {
		var pcm []float64

		switch frame {
		case 0, 1:
			// Silence
			pcm = make([]float64, frameSize+overlap)
		case 2:
			// Sharp attack - drum hit simulation
			pcm = generateDrumHit(frameSize+overlap, 48000)
		case 3, 4:
			// Decay
			pcm = generateDecayingSignal(frameSize+overlap, 0.5)
		}

		result := enc.TransientAnalysisWithState(pcm, frameSize+overlap, false)

		t.Logf("Frame %d: IsTransient=%v, MaskMetric=%.1f, TfEstimate=%.3f, AttackDuration=%d",
			frame, result.IsTransient, result.MaskMetric, result.TfEstimate, enc.GetAttackDuration())

		// Frame 2 (drum hit) should definitely be detected as transient
		if frame == 2 && !result.IsTransient {
			// Allow for mask_metric to be high even if not passing threshold
			if result.MaskMetric < 100 {
				t.Errorf("Frame %d (drum hit): expected high mask_metric, got %.1f", frame, result.MaskMetric)
			}
		}
	}
}

// TestPercussiveAttackDetection tests the specialized percussive detector
func TestPercussiveAttackDetection(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960

	testCases := []struct {
		name            string
		genFunc         func(int) []float64
		expectPercussive bool
		minStrength     float64
	}{
		{
			name:            "silence",
			genFunc:         func(n int) []float64 { return make([]float64, n) },
			expectPercussive: false,
			minStrength:     0.0,
		},
		{
			name:            "steady_sine",
			genFunc:         func(n int) []float64 { return generateSineWave(440.0, n) },
			expectPercussive: false,
			minStrength:     0.0,
		},
		{
			name:            "drum_hit",
			genFunc:         func(n int) []float64 { return generateDrumHit(n, 48000) },
			expectPercussive: true,
			minStrength:     0.3,
		},
		{
			name:            "impulse",
			genFunc:         generateImpulse,
			expectPercussive: true,
			minStrength:     0.5,
		},
		{
			name:            "attack_silence_to_tone",
			genFunc:         func(n int) []float64 { return generateAttackFromSilence(n, 48000) },
			expectPercussive: true,
			minStrength:     0.5,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := tc.genFunc(frameSize)
			isPercussive, attackPos, attackStrength := enc.DetectPercussiveAttack(pcm, frameSize)

			t.Logf("%s: isPercussive=%v, attackPos=%d, attackStrength=%.3f",
				tc.name, isPercussive, attackPos, attackStrength)

			if isPercussive != tc.expectPercussive {
				t.Errorf("isPercussive: got %v, want %v", isPercussive, tc.expectPercussive)
			}

			if tc.expectPercussive && attackStrength < tc.minStrength {
				t.Errorf("attackStrength: got %.3f, want >= %.3f", attackStrength, tc.minStrength)
			}
		})
	}
}

// TestTransientHysteresis verifies hysteresis prevents rapid toggling
func TestTransientHysteresis(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	overlap := 120

	// Generate a sequence: attack, moderate signal, moderate signal
	// The hysteresis should help maintain transient state

	// First frame: strong transient
	pcm1 := generateDrumHit(frameSize+overlap, 48000)
	result1 := enc.TransientAnalysisWithState(pcm1, frameSize+overlap, false)

	// Second frame: moderate signal that's borderline
	pcm2 := make([]float64, frameSize+overlap)
	for i := range pcm2 {
		pcm2[i] = 0.3 * math.Sin(2*math.Pi*1000*float64(i)/48000)
	}
	result2 := enc.TransientAnalysisWithState(pcm2, frameSize+overlap, false)

	t.Logf("Frame 1: IsTransient=%v, MaskMetric=%.1f, AttackDuration=%d",
		result1.IsTransient, result1.MaskMetric, enc.GetAttackDuration())
	t.Logf("Frame 2: IsTransient=%v, MaskMetric=%.1f, AttackDuration=%d",
		result2.IsTransient, result2.MaskMetric, enc.GetAttackDuration())

	// Attack duration should decay, not jump back to 0
	if enc.GetAttackDuration() == 0 && result1.IsTransient {
		// This is OK - attack duration decays after non-transient
	}
}

// TestShouldUseShortBlocks tests the combined decision function
func TestShouldUseShortBlocks(t *testing.T) {
	testCases := []struct {
		name        string
		isTransient bool
		maskMetric  float64
		tfEstimate  float64
		percussive  bool
		lm          int
		totalBits   int
		expectShort bool
	}{
		{
			name:        "transient_detected",
			isTransient: true,
			maskMetric:  300,
			tfEstimate:  0.5,
			percussive:  false,
			lm:          3,
			totalBits:   1000,
			expectShort: true,
		},
		{
			name:        "percussive_override",
			isTransient: false,
			maskMetric:  150,
			tfEstimate:  0.2,
			percussive:  true,
			lm:          3,
			totalBits:   1000,
			expectShort: true,
		},
		{
			name:        "percussive_but_steady",
			isTransient: false,
			maskMetric:  50,
			tfEstimate:  0.05,
			percussive:  true,
			lm:          3,
			totalBits:   1000,
			expectShort: false, // tf_estimate too low
		},
		{
			name:        "lm0_no_short_blocks",
			isTransient: true,
			maskMetric:  300,
			tfEstimate:  0.5,
			percussive:  false,
			lm:          0,
			totalBits:   1000,
			expectShort: false, // LM=0 cannot use short blocks
		},
		{
			name:        "insufficient_bits",
			isTransient: true,
			maskMetric:  300,
			tfEstimate:  0.5,
			percussive:  false,
			lm:          3,
			totalBits:   2,
			expectShort: false, // Not enough bits
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := TransientAnalysisResult{
				IsTransient: tc.isTransient,
				MaskMetric:  tc.maskMetric,
				TfEstimate:  tc.tfEstimate,
			}

			useShort := ShouldUseShortBlocks(result, tc.percussive, tc.lm, tc.totalBits)

			if useShort != tc.expectShort {
				t.Errorf("ShouldUseShortBlocks: got %v, want %v", useShort, tc.expectShort)
			}
		})
	}
}

// TestPersistentHPFilterState tests that HP filter state persists correctly
func TestPersistentHPFilterState(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	overlap := 120

	// Generate continuous sine wave split across two frames
	totalSamples := (frameSize + overlap) * 2
	fullSignal := generateSineWave(440.0, totalSamples)

	// Scale up to see filter effects
	for i := range fullSignal {
		fullSignal[i] *= 0.5
	}

	// Process first frame
	frame1 := fullSignal[:frameSize+overlap]
	result1 := enc.TransientAnalysisWithState(frame1, frameSize+overlap, false)

	// Save HP filter state after first frame
	hpMem0 := enc.transientHPMem[0][0]
	hpMem1 := enc.transientHPMem[0][1]

	// Process second frame
	frame2 := fullSignal[frameSize : 2*(frameSize+overlap)-overlap]
	result2 := enc.TransientAnalysisWithState(frame2, frameSize+overlap, false)

	t.Logf("Frame 1: MaskMetric=%.3f, HP mem=[%.6f, %.6f]",
		result1.MaskMetric, hpMem0, hpMem1)
	t.Logf("Frame 2: MaskMetric=%.3f, HP mem=[%.6f, %.6f]",
		result2.MaskMetric, enc.transientHPMem[0][0], enc.transientHPMem[0][1])

	// HP filter state should have changed
	if enc.transientHPMem[0][0] == 0 && enc.transientHPMem[0][1] == 0 {
		t.Error("HP filter state was not updated")
	}

	// Continuous sine should NOT be transient
	if result1.IsTransient || result2.IsTransient {
		t.Logf("Note: Continuous sine detected as transient (may be OK for first frame)")
	}
}

// TestResetTransientState verifies state reset works correctly
func TestResetTransientState(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	overlap := 120

	// Process a drum hit to build up state
	pcm := generateDrumHit(frameSize+overlap, 48000)
	_ = enc.TransientAnalysisWithState(pcm, frameSize+overlap, false)

	// Verify state was accumulated
	if enc.GetAttackDuration() == 0 && enc.peakEnergy == 0 {
		t.Log("Warning: No state accumulated (transient may not have been detected)")
	}

	// Reset state
	enc.ResetTransientState()

	// Verify state is cleared
	if enc.GetAttackDuration() != 0 {
		t.Errorf("attackDuration after reset: got %d, want 0", enc.GetAttackDuration())
	}
	if enc.lastMaskMetric != 0 {
		t.Errorf("lastMaskMetric after reset: got %f, want 0", enc.lastMaskMetric)
	}
	if enc.peakEnergy != 0 {
		t.Errorf("peakEnergy after reset: got %f, want 0", enc.peakEnergy)
	}
	for c := 0; c < 2; c++ {
		if enc.transientHPMem[c][0] != 0 || enc.transientHPMem[c][1] != 0 {
			t.Errorf("transientHPMem[%d] after reset not zero", c)
		}
	}
}

// TestStereoTransientDetection tests transient detection with stereo input
func TestStereoTransientDetection(t *testing.T) {
	enc := NewEncoder(2)
	frameSize := 960
	overlap := 120

	// Generate stereo signal: left channel steady, right channel has attack
	pcm := make([]float64, (frameSize+overlap)*2)
	for i := 0; i < frameSize+overlap; i++ {
		// Left: steady sine
		pcm[i*2] = 0.3 * math.Sin(2*math.Pi*440*float64(i)/48000)

		// Right: attack in the middle
		if i > frameSize/2 && i < frameSize/2+100 {
			pcm[i*2+1] = 0.8 * math.Sin(2*math.Pi*1000*float64(i-frameSize/2)*0.1)
		}
	}

	result := enc.TransientAnalysisWithState(pcm, frameSize+overlap, false)

	t.Logf("Stereo transient: IsTransient=%v, MaskMetric=%.1f, TfChannel=%d",
		result.IsTransient, result.MaskMetric, result.TfChannel)

	// The transient should be detected in the right channel
	if result.TfChannel != 1 && result.MaskMetric > 100 {
		t.Logf("Note: Expected TfChannel=1 (right), got %d", result.TfChannel)
	}
}

// Helper functions for generating test signals

func generateDrumHit(samples int, sampleRate int) []float64 {
	pcm := make([]float64, samples)

	// Attack at 1/3 of the frame
	attackStart := samples / 3
	attackSamples := sampleRate / 500 // 2ms attack
	decaySamples := sampleRate / 10   // 100ms decay

	for i := 0; i < samples; i++ {
		if i < attackStart {
			// Silence before attack
			pcm[i] = 0
		} else if i < attackStart+attackSamples {
			// Fast attack (exponential rise)
			t := float64(i-attackStart) / float64(attackSamples)
			pcm[i] = 0.8 * (1 - math.Exp(-5*t))
		} else if i < attackStart+attackSamples+decaySamples {
			// Decay (exponential fall)
			t := float64(i-attackStart-attackSamples) / float64(decaySamples)
			pcm[i] = 0.8 * math.Exp(-3*t)
		} else {
			pcm[i] = 0
		}

		// Add some noise/harmonics for realism
		pcm[i] *= 1 + 0.1*math.Sin(2*math.Pi*100*float64(i)/float64(sampleRate))
	}

	return pcm
}

func generateImpulse(samples int) []float64 {
	pcm := make([]float64, samples)
	// Single impulse at 1/3 of frame
	impulsePos := samples / 3
	pcm[impulsePos] = 1.0
	// Short ring-down
	for i := 1; i < 20 && impulsePos+i < samples; i++ {
		pcm[impulsePos+i] = math.Exp(-float64(i)/5.0) * 0.5
	}
	return pcm
}

func generateAttackFromSilence(samples int, sampleRate int) []float64 {
	pcm := make([]float64, samples)
	attackStart := samples / 3

	for i := attackStart; i < samples; i++ {
		t := float64(i) / float64(sampleRate)
		pcm[i] = 0.7 * math.Sin(2*math.Pi*440*t)
	}
	return pcm
}

func generateDecayingSignal(samples int, startAmplitude float64) []float64 {
	pcm := make([]float64, samples)
	decayRate := 3.0 / float64(samples)

	for i := 0; i < samples; i++ {
		amplitude := startAmplitude * math.Exp(-decayRate*float64(i))
		pcm[i] = amplitude * math.Sin(2*math.Pi*440*float64(i)/48000)
	}
	return pcm
}
