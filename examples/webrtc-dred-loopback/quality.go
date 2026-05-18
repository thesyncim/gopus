package main

import "math"

func intelligibilityScore(reference, decoded []float32, mask []bool, lag, sampleRate int) float64 {
	if sampleRate <= 0 || lag < 0 || len(reference) <= lag || len(decoded) == 0 {
		return 0
	}
	limit := len(decoded)
	if len(reference)-lag < limit {
		limit = len(reference) - lag
	}
	if limit <= 0 {
		return 0
	}

	ref := make([]float32, 0, limit)
	deg := make([]float32, 0, limit)
	for i := 0; i < limit; i++ {
		if mask != nil && (i >= len(mask) || !mask[i]) {
			continue
		}
		ref = append(ref, reference[i+lag])
		deg = append(deg, decoded[i])
	}
	if len(ref) < sampleRate/2 {
		return 0
	}

	const metricRate = 10000
	ref10 := resampleLinear(ref, sampleRate, metricRate)
	deg10 := resampleLinear(deg, sampleRate, metricRate)
	if len(ref10) < 512 || len(deg10) < 512 {
		return 0
	}

	refEnv := thirdOctaveEnvelopes(ref10, metricRate)
	degEnv := thirdOctaveEnvelopes(deg10, metricRate)
	if len(refEnv) == 0 || len(refEnv) != len(degEnv) {
		return 0
	}
	return stoiStyleCorrelation(refEnv, degEnv)
}

func resampleLinear(in []float32, fromRate, toRate int) []float32 {
	if len(in) == 0 || fromRate <= 0 || toRate <= 0 {
		return nil
	}
	if fromRate == toRate {
		return append([]float32(nil), in...)
	}
	outLen := int(float64(len(in)) * float64(toRate) / float64(fromRate))
	if outLen <= 1 {
		return nil
	}
	out := make([]float32, outLen)
	scale := float64(fromRate) / float64(toRate)
	for i := range out {
		pos := float64(i) * scale
		j := int(pos)
		frac := pos - float64(j)
		if j >= len(in)-1 {
			out[i] = in[len(in)-1]
			continue
		}
		out[i] = float32((1-frac)*float64(in[j]) + frac*float64(in[j+1]))
	}
	return out
}

func thirdOctaveEnvelopes(samples []float32, sampleRate int) [][]float64 {
	const (
		winSize = 256
		hop     = 128
		bands   = 15
	)
	if len(samples) < winSize || sampleRate <= 0 {
		return nil
	}
	window := make([]float64, winSize)
	for i := range window {
		window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(winSize-1))
	}
	binFreq := float64(sampleRate) / float64(winSize)
	bandBins := make([][]int, bands)
	for band := 0; band < bands; band++ {
		center := 150.0 * math.Pow(2, float64(band)/3)
		lo := center / math.Pow(2, 1.0/6)
		hi := center * math.Pow(2, 1.0/6)
		for bin := 1; bin <= winSize/2; bin++ {
			freq := float64(bin) * binFreq
			if freq >= lo && freq < hi {
				bandBins[band] = append(bandBins[band], bin)
			}
		}
	}

	frames := 1 + (len(samples)-winSize)/hop
	env := make([][]float64, bands)
	for band := range env {
		env[band] = make([]float64, frames)
	}
	for frame := 0; frame < frames; frame++ {
		start := frame * hop
		power := framePowerSpectrum(samples[start:start+winSize], window)
		for band, bins := range bandBins {
			var energy float64
			for _, bin := range bins {
				energy += power[bin]
			}
			env[band][frame] = math.Sqrt(energy)
		}
	}
	return env
}

func framePowerSpectrum(frame []float32, window []float64) []float64 {
	n := len(window)
	power := make([]float64, n/2+1)
	for bin := 0; bin <= n/2; bin++ {
		var re, im float64
		for i := 0; i < n; i++ {
			angle := -2 * math.Pi * float64(bin*i) / float64(n)
			v := float64(frame[i]) * window[i]
			re += v * math.Cos(angle)
			im += v * math.Sin(angle)
		}
		power[bin] = re*re + im*im
	}
	return power
}

func stoiStyleCorrelation(refEnv, degEnv [][]float64) float64 {
	const segmentFrames = 30
	if len(refEnv) == 0 || len(refEnv) != len(degEnv) {
		return 0
	}
	frames := len(refEnv[0])
	if frames < segmentFrames {
		return 0
	}
	maxEnergy := 0.0
	frameEnergy := make([]float64, frames)
	for frame := 0; frame < frames; frame++ {
		for band := range refEnv {
			frameEnergy[frame] += refEnv[band][frame] * refEnv[band][frame]
		}
		if frameEnergy[frame] > maxEnergy {
			maxEnergy = frameEnergy[frame]
		}
	}
	threshold := maxEnergy * 1e-4
	var sum float64
	var count int
	for band := range refEnv {
		for end := segmentFrames; end <= frames; end++ {
			segEnergy := 0.0
			for frame := end - segmentFrames; frame < end; frame++ {
				segEnergy += frameEnergy[frame]
			}
			if segEnergy/segmentFrames < threshold {
				continue
			}
			if c, ok := clippedEnvelopeCorrelation(refEnv[band][end-segmentFrames:end], degEnv[band][end-segmentFrames:end]); ok {
				sum += c
				count++
			}
		}
	}
	if count == 0 {
		return 0
	}
	score := sum / float64(count)
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func clippedEnvelopeCorrelation(ref, deg []float64) (float64, bool) {
	if len(ref) == 0 || len(ref) != len(deg) {
		return 0, false
	}
	var refEnergy, degEnergy float64
	for i := range ref {
		refEnergy += ref[i] * ref[i]
		degEnergy += deg[i] * deg[i]
	}
	if refEnergy == 0 || degEnergy == 0 {
		return 0, false
	}
	scale := math.Sqrt(refEnergy / degEnergy)
	clipped := make([]float64, len(deg))
	clipLimit := math.Pow(10, -15.0/20.0)
	for i := range deg {
		v := deg[i] * scale
		limit := ref[i] * (1 + clipLimit)
		if v > limit {
			v = limit
		}
		clipped[i] = v
	}
	return pearson(ref, clipped)
}

func pearson(x, y []float64) (float64, bool) {
	if len(x) == 0 || len(x) != len(y) {
		return 0, false
	}
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx /= float64(len(x))
	my /= float64(len(y))
	var num, vx, vy float64
	for i := range x {
		dx := x[i] - mx
		dy := y[i] - my
		num += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx == 0 || vy == 0 {
		return 0, false
	}
	return num / math.Sqrt(vx*vy), true
}
