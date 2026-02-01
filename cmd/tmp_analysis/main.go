package main

import (
	"fmt"
	"math"

	"github.com/thesyncim/gopus/celt"
)

func main() {
	sampleRate := 48000
	frameSize := 960
	frames := 1

	freqs := []float64{440, 1000, 2000, 5000, 10000}
	amp := 0.3 / float64(len(freqs))
	signal := make([]float64, frames*frameSize)
	for i := 0; i < len(signal); i++ {
		t := float64(i) / float64(sampleRate)
		var sample float64
		for _, f := range freqs {
			sample += amp * math.Sin(2*math.Pi*f*t)
		}
		signal[i] = sample
	}

	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)
	dec := celt.NewDecoder(1)

	packet, _ := enc.EncodeFrame(signal, frameSize)
	decoded, _ := dec.DecodeFrame(packet, frameSize)

	bestDelay := 0
	bestCorr := -2.0
	for d := -1000; d <= 1000; d++ {
		c := corrWithDelay(signal, decoded, d)
		if c > bestCorr {
			bestCorr = c
			bestDelay = d
		}
	}
	fmt.Printf("best delay=%d corr=%.4f\n", bestDelay, bestCorr)
}

func corrWithDelay(x, y []float64, delay int) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	var sumXY, sumX2, sumY2 float64
	count := 0
	for i := 0; i < n; i++ {
		j := i + delay
		if j >= 0 && j < len(y) {
			xi := x[i]
			yj := y[j]
			sumXY += xi * yj
			sumX2 += xi * xi
			sumY2 += yj * yj
			count++
		}
	}
	if count == 0 || sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / math.Sqrt(sumX2*sumY2)
}

