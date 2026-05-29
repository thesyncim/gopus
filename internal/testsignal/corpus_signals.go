package testsignal

import "math"

// Corpus signal generators for the broader signal-class corpus.
//
// Each generator is deterministic (PRNG-based where noise is needed) and
// produces float32 interleaved PCM at the requested sample rate and channel
// count. They are designed to exercise distinct codec behaviours:
//
//   - CorpusCleanSpeechV1: voiced speech with formant shaping, SILK/Hybrid
//   - CorpusMusicV1: harmonic music with vibrato and beat, CELT
//   - CorpusMixedV1: voiced speech cross-faded with music, Hybrid
//   - CorpusWhiteNoiseV1: flat-spectrum noise, stresses entropy coder
//   - CorpusCastanetTransientV1: sharp percussive bursts at ~10 Hz, transient path
//   - CorpusPureToneV1: single sinusoid at 1 kHz, cleanest possible signal
//   - CorpusNearSilenceV1: -60 dBFS dithered noise, near-silence path

const (
	CorpusCleanSpeechV1      = "corpus_clean_speech_v1"
	CorpusMusicV1            = "corpus_music_v1"
	CorpusMixedV1            = "corpus_mixed_v1"
	CorpusWhiteNoiseV1       = "corpus_white_noise_v1"
	CorpusCastanetTransientV1 = "corpus_castanet_transient_v1"
	CorpusPureToneV1         = "corpus_pure_tone_v1"
	CorpusNearSilenceV1      = "corpus_near_silence_v1"
)

// CorpusSignalClasses returns all distinct signal-class tags used in the corpus
// fixture. Each entry maps to at least one fixture case.
func CorpusSignalClasses() []string {
	return []string{
		CorpusCleanSpeechV1,
		CorpusMusicV1,
		CorpusMixedV1,
		CorpusWhiteNoiseV1,
		CorpusCastanetTransientV1,
		CorpusPureToneV1,
		CorpusNearSilenceV1,
	}
}

// GenerateCorpusSignal generates one of the corpus signal class waveforms.
func GenerateCorpusSignal(class string, sampleRate, samples, channels int) ([]float32, error) {
	switch class {
	case CorpusCleanSpeechV1:
		return generateCorpusCleanSpeech(sampleRate, samples, channels), nil
	case CorpusMusicV1:
		return generateCorpusMusic(sampleRate, samples, channels), nil
	case CorpusMixedV1:
		return generateCorpusMixed(sampleRate, samples, channels), nil
	case CorpusWhiteNoiseV1:
		return generateCorpusWhiteNoise(sampleRate, samples, channels), nil
	case CorpusCastanetTransientV1:
		return generateCorpusCastanetTransient(sampleRate, samples, channels), nil
	case CorpusPureToneV1:
		return generateCorpusPureTone(sampleRate, samples, channels), nil
	case CorpusNearSilenceV1:
		return generateCorpusNearSilence(sampleRate, samples, channels), nil
	default:
		return GenerateEncoderSignalVariant(class, sampleRate, samples, channels)
	}
}

// generateCorpusCleanSpeech produces a voiced-speech-like signal with
// formant-shaped harmonics and natural voiced/unvoiced transitions.
func generateCorpusCleanSpeech(sampleRate, samples, channels int) []float32 {
	out := make([]float32, samples)
	phase := make([]float64, channels)
	prev := make([]float64, channels)
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		t := float64(si) / float64(sampleRate)

		// Pitch: 120–180 Hz glide with natural variation
		pitch := 150.0 + 30.0*math.Sin(2*math.Pi*0.5*t) + 12.0*math.Sin(2*math.Pi*1.3*t)
		pitch *= 1.0 + 0.008*float64(ch)
		phase[ch] += 2 * math.Pi * pitch / float64(sampleRate)
		if phase[ch] > 2*math.Pi {
			phase[ch] -= 2 * math.Pi
		}

		// Formant-shaped harmonics (F1≈700 Hz, F2≈1400 Hz, F3≈2800 Hz)
		p := phase[ch]
		voiced := 0.6*math.Sin(p) + 0.25*math.Sin(2*p) + 0.15*math.Sin(3*p) +
			0.08*math.Sin(4*p) + 0.04*math.Sin(5*p)

		// Voicing envelope: 80 ms on / 20 ms pause
		syllablePhase := math.Mod(t, 0.1) / 0.1
		voicing := 1.0
		if syllablePhase > 0.8 {
			voicing = 0.0
		}

		// Unvoiced fricative noise for consonants
		noise := deterministicNoise(si, ch, 137)
		filtered := noise - 0.7*prev[ch]
		prev[ch] = noise

		val := voicing*voiced + (1.0-voicing)*0.3*filtered

		// Syllable amplitude envelope
		env := 0.5 + 0.5*math.Pow(0.5+0.5*math.Sin(2*math.Pi*4.2*t-0.3*float64(ch)), 1.5)
		out[i] = float32(clipSample(0.75 * env * val))
	}
	return out
}

// generateCorpusMusic produces music-like content: chord of harmonics with
// vibrato, amplitude modulation, and a rhythmic beat.
func generateCorpusMusic(sampleRate, samples, channels int) []float32 {
	out := make([]float32, samples)
	// A-major chord: A3=220, C#4=277.18, E4=329.63
	freqs := []float64{220.0, 277.18, 329.63}
	// Add overtones for each note
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		t := float64(si) / float64(sampleRate)

		// Vibrato: 5 Hz, ±0.5%
		vibrato := 1.0 + 0.005*math.Sin(2*math.Pi*5.0*t)
		channelDetune := 1.0 + 0.003*float64(ch)

		var val float64
		for fi, f := range freqs {
			// Fundamental + 2nd + 3rd harmonic per string
			fd := f * vibrato * channelDetune
			amp := 0.22 / float64(len(freqs))
			// Pluck envelope: each "string" plucked at slightly different times
			pluckOffset := float64(fi) * 0.05
			pluckT := math.Mod(t+pluckOffset, 1.0)
			stringEnv := math.Exp(-pluckT * 2.5)
			val += amp * stringEnv * (
				math.Sin(2*math.Pi*fd*t) +
				0.4*math.Sin(4*math.Pi*fd*t) +
				0.2*math.Sin(6*math.Pi*fd*t))
		}
		// Rhythmic beat at 2 Hz (modulates amplitude)
		beat := 0.7 + 0.3*math.Pow(0.5+0.5*math.Cos(2*math.Pi*2.0*t), 3)
		out[i] = float32(clipSample(val * beat))
	}
	return out
}

// generateCorpusMixed cross-fades clean speech with music over time, producing
// a signal that exercises both SILK and CELT code paths in Hybrid mode.
func generateCorpusMixed(sampleRate, samples, channels int) []float32 {
	speech := generateCorpusCleanSpeech(sampleRate, samples, channels)
	music := generateCorpusMusic(sampleRate, samples, channels)
	out := make([]float32, samples)
	totalPerChannel := samples / channels
	for i := 0; i < samples; i++ {
		si := i / channels
		// Slow cross-fade: first half speech-dominant, second half music-dominant
		t := float64(si) / float64(totalPerChannel)
		musicFrac := 0.5 * (1.0 + math.Sin(math.Pi*(t-0.5)))
		speechFrac := 1.0 - musicFrac
		out[i] = float32(clipSample(0.7*float64(speech[i])*speechFrac + 0.7*float64(music[i])*musicFrac))
	}
	return out
}

// generateCorpusWhiteNoise produces flat-spectrum pseudo-random noise at ~-12 dBFS.
func generateCorpusWhiteNoise(sampleRate, samples, channels int) []float32 {
	out := make([]float32, samples)
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		// Two PRNG seeds per sample for independence between L/R
		n := deterministicNoise(si, ch, 251)
		out[i] = float32(clipSample(0.25 * n))
	}
	return out
}

// generateCorpusCastanetTransient produces sharp percussive bursts (castanet-like)
// at ~10 Hz with fast decay (< 5 ms).
func generateCorpusCastanetTransient(sampleRate, samples, channels int) []float32 {
	out := make([]float32, samples)
	// Burst period: ~100 ms (10 Hz), with some jitter
	period := int(0.100 * float64(sampleRate))
	if period < 1 {
		period = 1
	}
	decayT := 0.003 * float64(sampleRate) // 3 ms decay
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		pos := si % period
		// Jitter: alternate period length slightly for stereo
		if ch == 1 {
			jitterPeriod := int(0.097 * float64(sampleRate))
			if jitterPeriod < 1 {
				jitterPeriod = 1
			}
			pos = si % jitterPeriod
		}
		val := 0.0
		if pos == 0 {
			val = 0.90 // sharp click onset
		}
		// Fast resonant decay
		if pos < int(0.008*float64(sampleRate)) {
			resonance := math.Exp(-float64(pos)/decayT) *
				math.Sin(2*math.Pi*3500*float64(pos)/float64(sampleRate))
			val += 0.85 * resonance
		}
		// Low-level noise floor between bursts
		noise := deterministicNoise(si, ch, 313)
		val += 0.005 * noise
		out[i] = float32(clipSample(val))
	}
	return out
}

// generateCorpusPureTone produces a single sinusoid at 1 kHz at ~-12 dBFS.
func generateCorpusPureTone(sampleRate, samples, channels int) []float32 {
	out := make([]float32, samples)
	const freq = 1000.0
	const amp = 0.25
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		t := float64(si) / float64(sampleRate)
		// Tiny channel phase offset so stereo isn't identical
		phase := 2 * math.Pi * freq * t + 0.1*float64(ch)
		out[i] = float32(amp * math.Sin(phase))
	}
	return out
}

// generateCorpusNearSilence produces a near-silence signal: low-amplitude
// dithered noise at -60 dBFS.
func generateCorpusNearSilence(sampleRate, samples, channels int) []float32 {
	const silenceAmp = 0.001 // -60 dBFS
	out := make([]float32, samples)
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		n := deterministicNoise(si, ch, 379)
		out[i] = float32(silenceAmp * n)
	}
	return out
}
