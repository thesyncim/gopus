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
//   - CorpusPinkNoiseV1: 1/f-shaped (pink) noise, spectrally tilted vs white
//   - CorpusFormantSweepV1: band-limited diphthong vowel glide (speech-like)
//   - CorpusSpeechInNoiseV1: voiced speech mixed with band noise at ~10 dB SNR
//   - CorpusStereoDecorrelatedV1: independent per-channel content (wide image)
//   - CorpusBellClusterV1: dense inharmonic partials (metallic / bell-like)

const (
	CorpusCleanSpeechV1      = "corpus_clean_speech_v1"
	CorpusMusicV1            = "corpus_music_v1"
	CorpusMixedV1            = "corpus_mixed_v1"
	CorpusWhiteNoiseV1       = "corpus_white_noise_v1"
	CorpusCastanetTransientV1 = "corpus_castanet_transient_v1"
	CorpusPureToneV1         = "corpus_pure_tone_v1"
	CorpusNearSilenceV1      = "corpus_near_silence_v1"
	CorpusPinkNoiseV1        = "corpus_pink_noise_v1"
	CorpusFormantSweepV1     = "corpus_formant_sweep_v1"
	CorpusSpeechInNoiseV1    = "corpus_speech_in_noise_v1"
	CorpusStereoDecorrelatedV1 = "corpus_stereo_decorrelated_v1"
	CorpusBellClusterV1      = "corpus_bell_cluster_v1"
)

// CorpusSignalClasses returns the signal-class tags backed by the committed
// decoder-parity fixture. Each entry maps to at least one fixture case, so this
// list is the fixture-coverage contract (TestCorpusSignalClassCoverage).
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

// CorpusExtendedSignalClasses returns the additional signal classes that fill
// coverage gaps left by CorpusSignalClasses (spectral tilt, moving formants,
// degraded speech, inter-channel decorrelation, inharmonic partials). These are
// generated on the fly for the live blob-free quality gate and are NOT part of the
// committed fixture, so they are intentionally separate from CorpusSignalClasses.
func CorpusExtendedSignalClasses() []string {
	return []string{
		CorpusPinkNoiseV1,
		CorpusFormantSweepV1,
		CorpusSpeechInNoiseV1,
		CorpusStereoDecorrelatedV1,
		CorpusBellClusterV1,
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
	case CorpusPinkNoiseV1:
		return generateCorpusPinkNoise(sampleRate, samples, channels), nil
	case CorpusFormantSweepV1:
		return generateCorpusFormantSweep(sampleRate, samples, channels), nil
	case CorpusSpeechInNoiseV1:
		return generateCorpusSpeechInNoise(sampleRate, samples, channels), nil
	case CorpusStereoDecorrelatedV1:
		return generateCorpusStereoDecorrelated(sampleRate, samples, channels), nil
	case CorpusBellClusterV1:
		return generateCorpusBellCluster(sampleRate, samples, channels), nil
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

// generateCorpusPinkNoise produces 1/f-shaped (pink) noise via the Voss-McCartney
// octave-summing approximation. Its falling spectral tilt (vs flat white noise)
// exercises the codec's spectral envelope coding differently from CorpusWhiteNoiseV1.
func generateCorpusPinkNoise(sampleRate, samples, channels int) []float32 {
	const rows = 8 // octave bands summed
	out := make([]float32, samples)
	// Per-channel running octave-band rows and their accumulated sum.
	rowsState := make([][]float64, channels)
	sums := make([]float64, channels)
	for ch := range rowsState {
		rowsState[ch] = make([]float64, rows)
	}
	totalPerChannel := samples / channels
	for ch := 0; ch < channels; ch++ {
		for si := 0; si < totalPerChannel; si++ {
			// Each row updates every 2^row samples; the lowest row updates every
			// sample. Salt by channel so L/R are independent.
			for r := 0; r < rows; r++ {
				if si&((1<<uint(r))-1) == 0 {
					sums[ch] -= rowsState[ch][r]
					rowsState[ch][r] = deterministicNoise(si, ch, 401+r)
					sums[ch] += rowsState[ch][r]
				}
			}
			val := sums[ch] / float64(rows)
			out[si*channels+ch] = float32(clipSample(0.6 * val))
		}
	}
	return out
}

// generateCorpusFormantSweep produces a band-limited diphthong: a steady glottal
// pulse train whose two lowest formants glide between two vowel targets (a -> i),
// like a sung "ah-ee". Distinct from CorpusCleanSpeechV1 (steady formants): the
// moving resonances stress SILK/Hybrid LSF tracking.
func generateCorpusFormantSweep(sampleRate, samples, channels int) []float32 {
	out := make([]float32, samples)
	phase := make([]float64, channels)
	// Formant targets: /a/ (F1=730,F2=1090) -> /i/ (F1=270,F2=2290).
	const f1a, f2a = 730.0, 1090.0
	const f1i, f2i = 270.0, 2290.0
	totalPerChannel := samples / channels
	for ch := 0; ch < channels; ch++ {
		for si := 0; si < totalPerChannel; si++ {
			t := float64(si) / float64(sampleRate)
			frac := 0.5 * (1.0 - math.Cos(2*math.Pi*0.4*t)) // 0..1 sweep at 0.4 Hz

			pitch := 130.0 * (1.0 + 0.006*float64(ch))
			phase[ch] += 2 * math.Pi * pitch / float64(sampleRate)
			if phase[ch] > 2*math.Pi {
				phase[ch] -= 2 * math.Pi
			}
			f1 := f1a + (f1i-f1a)*frac
			f2 := f2a + (f2i-f2a)*frac

			// Sum of harmonics weighted by proximity to the two formant peaks.
			var val float64
			for h := 1; h <= 20; h++ {
				fh := pitch * float64(h)
				if fh > float64(sampleRate)/2 {
					break
				}
				w := formantWeight(fh, f1, 90) + 0.8*formantWeight(fh, f2, 110)
				val += w * math.Sin(float64(h)*phase[ch])
			}
			env := 0.5 + 0.5*math.Sin(2*math.Pi*3.0*t-0.2*float64(ch))
			out[si*channels+ch] = float32(clipSample(0.35 * (0.6 + 0.4*env) * val))
		}
	}
	return out
}

// formantWeight is a single-pole resonance magnitude (Lorentzian) at center freq
// fc with half-bandwidth bw, used to shape harmonic amplitudes into formants.
func formantWeight(f, fc, bw float64) float64 {
	d := (f - fc) / bw
	return 1.0 / (1.0 + d*d)
}

// generateCorpusSpeechInNoise mixes the voiced-speech generator with band-limited
// noise at ~10 dB SNR, a common real-world degraded-speech condition that stresses
// SILK's noise-shaping and VAD differently from clean speech or speech+music.
func generateCorpusSpeechInNoise(sampleRate, samples, channels int) []float32 {
	speech := generateCorpusCleanSpeech(sampleRate, samples, channels)
	out := make([]float32, samples)
	prev := make([]float64, channels)
	const snrLin = 0.316 // 10*log10(1/0.316^2) ~= 10 dB SNR (noise rms ~0.316x speech)
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		// Low-pass-ish band noise (one-pole) so it sits in the speech band.
		raw := deterministicNoise(si, ch, 433)
		band := 0.5*raw + 0.5*prev[ch]
		prev[ch] = band
		out[i] = float32(clipSample(float64(speech[i]) + snrLin*0.4*band))
	}
	return out
}

// generateCorpusStereoDecorrelated produces independent content per channel: a
// tone cluster in L and band-limited noise in R (and a phase-inverted blend when
// mono is requested). This maximizes inter-channel decorrelation, stressing the
// stereo coupling / mid-side decision that the correlated corpus cases do not.
func generateCorpusStereoDecorrelated(sampleRate, samples, channels int) []float32 {
	out := make([]float32, samples)
	prev := 0.0
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		t := float64(si) / float64(sampleRate)
		var val float64
		if ch == 0 {
			// Left: detuned two-tone cluster.
			val = 0.35*math.Sin(2*math.Pi*440.0*t) + 0.25*math.Sin(2*math.Pi*523.25*t)
		} else {
			// Right: independent band-limited noise (uncorrelated with left).
			raw := deterministicNoise(si, ch, 457)
			band := 0.6*raw + 0.4*prev
			prev = band
			val = 0.5 * band
		}
		out[i] = float32(clipSample(0.7 * val))
	}
	return out
}

// generateCorpusBellCluster produces dense inharmonic partials (a struck-metal /
// bell timbre): partials at non-integer frequency ratios with independent decays.
// Inharmonicity defeats pitch-based modeling and stresses CELT's PVQ allocation
// differently from the harmonic CorpusMusicV1 chord.
func generateCorpusBellCluster(sampleRate, samples, channels int) []float32 {
	// Inharmonic partial ratios typical of a struck bar/bell, with per-partial decay.
	ratios := []float64{1.00, 2.76, 5.40, 8.93, 13.34, 18.64}
	decays := []float64{1.2, 1.8, 2.6, 3.4, 4.5, 6.0}
	const fundamental = 261.63 // C4
	const strikePeriod = 0.75  // s between strikes
	out := make([]float32, samples)
	for i := 0; i < samples; i++ {
		ch := i % channels
		si := i / channels
		t := float64(si) / float64(sampleRate)
		strikeT := math.Mod(t+0.13*float64(ch), strikePeriod)
		var val float64
		for pi, r := range ratios {
			env := math.Exp(-strikeT * decays[pi])
			amp := 0.5 / float64(pi+1)
			f := fundamental * r * (1.0 + 0.004*float64(ch))
			val += amp * env * math.Sin(2*math.Pi*f*t)
		}
		out[i] = float32(clipSample(0.6 * val))
	}
	return out
}
