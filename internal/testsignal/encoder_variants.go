package testsignal

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

const (
	EncoderVariantAMMultisineV1  = "am_multisine_v1"
	EncoderVariantChirpSweepV1   = "chirp_sweep_v1"
	EncoderVariantImpulseTrainV1 = "impulse_train_v1"
	EncoderVariantSpeechLikeV1   = "speech_like_v1"
)

var encoderSignalVariants = []string{
	EncoderVariantAMMultisineV1,
	EncoderVariantChirpSweepV1,
	EncoderVariantImpulseTrainV1,
	EncoderVariantSpeechLikeV1,
}

func EncoderSignalVariants() []string {
	out := make([]string, len(encoderSignalVariants))
	copy(out, encoderSignalVariants)
	return out
}

func GenerateEncoderSignalVariant(variant string, sampleRate, samples, channels int) ([]float32, error) {
	if sampleRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate: %d", sampleRate)
	}
	if channels <= 0 {
		return nil, fmt.Errorf("invalid channel count: %d", channels)
	}
	if samples <= 0 {
		return nil, fmt.Errorf("invalid sample count: %d", samples)
	}
	if samples%channels != 0 {
		return nil, fmt.Errorf("sample count %d must be divisible by channels %d", samples, channels)
	}

	switch variant {
	case EncoderVariantAMMultisineV1:
		return generateAMMultisine(sampleRate, samples, channels), nil
	case EncoderVariantChirpSweepV1:
		return generateChirpSweep(sampleRate, samples, channels), nil
	case EncoderVariantImpulseTrainV1:
		return generateImpulseTrain(sampleRate, samples, channels), nil
	case EncoderVariantSpeechLikeV1:
		return generateSpeechLike(sampleRate, samples, channels), nil
	default:
		return nil, fmt.Errorf("unknown signal variant %q", variant)
	}
}

func HashFloat32LE(samples []float32) string {
	h := sha256.New()
	var b [4]byte
	for _, s := range samples {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(s))
		_, _ = h.Write(b[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func generateAMMultisine(sampleRate, samples, channels int) []float32 {
	signal := make([]float32, samples)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	modFreqs := []float64{1.3, 2.7, 0.9}
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / float64(sampleRate)
		var val float64
		for fi, freq := range freqs {
			f := freq
			if channels == 2 && ch == 1 {
				f *= 1.01
			}
			modDepth := 0.5 + 0.5*math.Sin(2*math.Pi*modFreqs[fi]*t)
			val += amp * modDepth * math.Sin(2*math.Pi*f*t)
		}
		onsetSamples := int(0.010 * float64(sampleRate))
		if sampleIdx < onsetSamples {
			frac := float64(sampleIdx) / float64(onsetSamples)
			val *= frac * frac * frac
		}
		signal[i] = float32(clipSample(val))
	}
	return signal
}

func generateChirpSweep(sampleRate, samples, channels int) []float32 {
	signal := make([]float32, samples)
	totalPerChannel := samples / channels
	duration := float64(totalPerChannel) / float64(sampleRate)
	if duration <= 0 {
		return signal
	}
	f0 := 60.0
	f1 := 12000.0
	k := math.Log(f1/f0) / duration
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / float64(sampleRate)
		channelScale := 1.0 + 0.006*float64(ch)
		phase := 2 * math.Pi * f0 * (math.Exp(k*t) - 1) / k
		env := 0.2 + 0.8*(0.5+0.5*math.Sin(2*math.Pi*0.41*t+0.3*float64(ch)))
		val := 0.85 * env * math.Sin(channelScale*phase)
		if sampleIdx < int(0.005*float64(sampleRate)) {
			frac := float64(sampleIdx) / (0.005 * float64(sampleRate))
			val *= frac
		}
		signal[i] = float32(clipSample(val))
	}
	return signal
}

func generateImpulseTrain(sampleRate, samples, channels int) []float32 {
	signal := make([]float32, samples)
	period := int(0.035 * float64(sampleRate))
	if period < 4 {
		period = 4
	}
	decayT := 0.0035 * float64(sampleRate)
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / float64(sampleRate)
		pos := sampleIdx % period
		val := 0.0
		if pos == 0 {
			val = 0.92
		}
		if pos < int(0.015*float64(sampleRate)) {
			ring := math.Exp(-float64(pos)/decayT) * math.Sin(2*math.Pi*(540+80*float64(ch))*float64(pos)/float64(sampleRate))
			val += 0.75 * ring
		}
		noise := deterministicNoise(sampleIdx, ch, 17)
		val += 0.02 * noise
		env := 0.6 + 0.4*math.Sin(2*math.Pi*0.19*t+0.4*float64(ch))
		signal[i] = float32(clipSample(val * env))
	}
	return signal
}

func generateSpeechLike(sampleRate, samples, channels int) []float32 {
	signal := make([]float32, samples)
	phase := make([]float64, channels)
	prevNoise := make([]float64, channels)
	for i := 0; i < samples; i++ {
		ch := i % channels
		sampleIdx := i / channels
		t := float64(sampleIdx) / float64(sampleRate)

		pitchHz := 95.0 + 28.0*math.Sin(2*math.Pi*0.63*t) + 16.0*math.Sin(2*math.Pi*0.17*t)
		pitchHz *= 1.0 + 0.01*float64(ch)
		phase[ch] += 2 * math.Pi * pitchHz / float64(sampleRate)
		if phase[ch] > 2*math.Pi {
			phase[ch] -= 2 * math.Pi
		}
		voiced := math.Sin(phase[ch]) + 0.35*math.Sin(2*phase[ch]) + 0.2*math.Sin(3*phase[ch])

		voicing := 0.5 + 0.5*math.Sin(2*math.Pi*0.78*t+0.25)
		syllable := 0.25 + 0.75*math.Pow(0.5+0.5*math.Sin(2*math.Pi*3.2*t), 2)

		noise := deterministicNoise(sampleIdx, ch, 71)
		high := noise - 0.86*prevNoise[ch]
		prevNoise[ch] = noise
		mix := voicing*voiced + (1.0-voicing)*(0.38*high+0.22*math.Sin(2*math.Pi*3200*t))

		signal[i] = float32(clipSample(0.82 * syllable * mix))
	}
	return signal
}

func deterministicNoise(sampleIdx, channel, salt int) float64 {
	x := uint32(sampleIdx*1664525 + channel*1013904223 + salt*2246822519)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	return (float64(int32(x)) / 2147483647.0)
}

func clipSample(v float64) float64 {
	if v > 0.98 {
		return 0.98
	}
	if v < -0.98 {
		return -0.98
	}
	return v
}
