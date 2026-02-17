package main

import (
	"math"
	"strings"
)

const sampleRate = 48000

// signalGenerator produces continuous PCM audio for streaming.
// Unlike the batch version in encode-play, this uses a running sampleOffset
// so it can generate indefinitely without a fixed total sample count.
type signalGenerator struct {
	signal       string
	channels     int
	sampleOffset int
	seed         uint32

	// Source-filter speech state.
	glottalPhase float64
	biquads      [5]biquadState
	radHPState   [2]float64
	prevF0       float64
}

type biquadState struct {
	y1, y2 float64
}

func (b *biquadState) process(x float64, freq, bw float64) float64 {
	r := math.Exp(-math.Pi * bw / float64(sampleRate))
	theta := 2.0 * math.Pi * freq / float64(sampleRate)
	a1 := -2.0 * r * math.Cos(theta)
	a2 := r * r
	y := x - a1*b.y1 - a2*b.y2
	b.y2 = b.y1
	b.y1 = y
	return y
}

func newSignalGenerator(signal string, channels int) *signalGenerator {
	return &signalGenerator{
		signal:   strings.ToLower(strings.TrimSpace(signal)),
		channels: channels,
		seed:     12345,
	}
}

func (g *signalGenerator) setSignal(signal string) {
	g.signal = strings.ToLower(strings.TrimSpace(signal))
}

// fillFrame fills pcm with frameSize samples of the current signal.
// pcm must have length frameSize * channels.
func (g *signalGenerator) fillFrame(pcm []float32, frameSize int) {
	if len(pcm) == 0 {
		return
	}
	if g.channels < 1 {
		g.channels = 1
	}

	for i := 0; i < frameSize; i++ {
		sampleIndex := g.sampleOffset + i
		t := float64(sampleIndex) / float64(sampleRate)

		var left, right float32

		switch g.signal {
		case "sine":
			left = float32(0.5 * math.Sin(2*math.Pi*440*t))
			right = float32(0.5 * math.Sin(2*math.Pi*554.37*t+0.1))
		case "sweep":
			// Continuous sweep: cycle through 100-8000 Hz every 4 seconds
			period := 4.0
			progress := math.Mod(t, period) / period
			freq := 100.0 + (8000.0-100.0)*progress
			left = float32(0.5 * math.Sin(2*math.Pi*freq*t))
			right = float32(0.5 * math.Sin(2*math.Pi*(freq*1.05)*t))
		case "noise":
			left = g.nextNoiseSample(0.4)
			right = g.nextNoiseSample(0.4)
		case "speech":
			left = g.speechSample(t, sampleIndex)
			right = left
		case "chord":
			left, right = g.chordSample(t)
		default:
			left = float32(0.5 * math.Sin(2*math.Pi*440*t))
			right = left
		}

		if g.channels == 1 {
			pcm[i] = left
			continue
		}
		pcm[i*g.channels] = left
		pcm[i*g.channels+1] = right
	}

	g.sampleOffset += frameSize
}

func (g *signalGenerator) chordSample(t float64) (float32, float32) {
	freqs := []float64{261.63, 329.63, 392.0}
	vibrato := 1.0 + 0.05*math.Sin(2*math.Pi*5*t)
	var sample float64
	for i, freq := range freqs {
		detune := 1.0 + 0.002*math.Sin(2*math.Pi*0.1*t+float64(i))
		sample += 0.15 * math.Sin(2*math.Pi*freq*detune*t)
	}
	sample *= vibrato
	pan := 0.5 + 0.4*math.Sin(2*math.Pi*0.2*t)
	left := float32(sample * pan)
	right := float32(sample * (1.0 - pan))
	return left, right
}

func (g *signalGenerator) nextNoiseSample(scale float32) float32 {
	g.seed = g.seed*1103515245 + 12345
	val := float32((g.seed>>16)&0x7FFF)/32768.0 - 0.5
	return val * scale
}

func (g *signalGenerator) speechSample(t float64, _ int) float32 {
	f0 := 120.0 + 15.0*math.Sin(2*math.Pi*0.35*t) +
		4.0*math.Sin(2*math.Pi*5.5*t)
	if f0 < 60 {
		f0 = 60
	}
	if g.prevF0 == 0 {
		g.prevF0 = f0
	}
	f0 = g.prevF0 + 0.1*(f0-g.prevF0)
	g.prevF0 = f0

	syllableRate := 3.0
	syllablePhase := math.Mod(t*syllableRate, 1.0)
	var syllableAmp float64
	switch {
	case syllablePhase < 0.10:
		syllableAmp = 0.5 - 0.5*math.Cos(math.Pi*syllablePhase/0.10)
	case syllablePhase < 0.55:
		syllableAmp = 1.0
	case syllablePhase < 0.75:
		syllableAmp = 0.5 + 0.5*math.Cos(math.Pi*(syllablePhase-0.55)/0.20)
	default:
		syllableAmp = 0.0
	}

	g.glottalPhase += f0 / float64(sampleRate)
	if g.glottalPhase >= 1.0 {
		g.glottalPhase -= math.Floor(g.glottalPhase)
	}

	var source float64
	tp := 0.40
	tn := 0.16
	phase := g.glottalPhase
	if phase < tp {
		source = 0.5 - 0.5*math.Cos(math.Pi*phase/tp)
	} else if phase < tp+tn {
		source = math.Cos(0.5 * math.Pi * (phase - tp) / tn)
	}

	if syllableAmp > 0.05 {
		aspiration := float64(g.nextNoiseSample(1.0))
		if phase < tp+tn {
			source += 0.04 * aspiration
		} else {
			source += 0.01 * aspiration
		}
	}

	source *= syllableAmp

	type fmtSet struct{ f, bw [5]float64 }
	vowels := [5]fmtSet{
		{f: [5]float64{730, 1090, 2440, 3300, 3750}, bw: [5]float64{90, 110, 170, 250, 300}},
		{f: [5]float64{270, 2290, 3010, 3500, 4100}, bw: [5]float64{60, 100, 150, 200, 280}},
		{f: [5]float64{300, 870, 2240, 3200, 3700}, bw: [5]float64{65, 100, 140, 220, 280}},
		{f: [5]float64{530, 1840, 2480, 3300, 3900}, bw: [5]float64{70, 110, 150, 230, 290}},
		{f: [5]float64{570, 840, 2410, 3250, 3750}, bw: [5]float64{80, 105, 155, 240, 300}},
	}

	vowelPos := math.Mod(t*syllableRate, 5.0)
	idx0 := int(vowelPos) % 5
	idx1 := (idx0 + 1) % 5
	frac := vowelPos - math.Floor(vowelPos)
	alpha := 0.5 - 0.5*math.Cos(math.Pi*frac)

	sample := source
	for i := 0; i < 5; i++ {
		freq := vowels[idx0].f[i] + alpha*(vowels[idx1].f[i]-vowels[idx0].f[i])
		bw := vowels[idx0].bw[i] + alpha*(vowels[idx1].bw[i]-vowels[idx0].bw[i])
		gain := 1.0
		switch i {
		case 1:
			gain = 0.5
		case 2:
			gain = 0.25
		case 3:
			gain = 0.12
		case 4:
			gain = 0.06
		}
		sample = gain * g.biquads[i].process(sample, freq, bw)
	}

	radiated := sample - g.radHPState[0]
	g.radHPState[0] = sample
	radiated *= 0.0003
	radiated = math.Tanh(radiated)
	return float32(radiated)
}
