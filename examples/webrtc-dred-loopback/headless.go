package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
)

type toneAudio struct {
	sample int
	trace  bool
	pcm    []float32
}

func newToneAudio(trace bool) *toneAudio {
	return &toneAudio{trace: trace}
}

func (t *toneAudio) readCaptureFrame(dst []float32) int {
	for i := 0; i < len(dst); i++ {
		n := t.sample + i
		sec := float64(n) / audioSampleRate
		env := 0.65 + 0.25*math.Sin(2*math.Pi*2.1*sec)
		v := 0.32*math.Sin(2*math.Pi*180*sec) +
			0.18*math.Sin(2*math.Pi*360*sec+0.2) +
			0.08*math.Sin(2*math.Pi*720*sec+0.4)
		dst[i] = float32(env * v)
	}
	t.sample += len(dst)
	if t.trace {
		t.pcm = append(t.pcm, dst...)
	}
	return len(dst)
}

func (t *toneAudio) queuePlayback(_ []float32) {}

func (t *toneAudio) setLivePlayback(_ bool) {}

func (t *toneAudio) close() {}

func (t *toneAudio) samplesCopy() []float32 {
	if t == nil {
		return nil
	}
	return append([]float32(nil), t.pcm...)
}

func runHeadless(cfg engineConfig, source string, duration time.Duration) (engineStats, error) {
	if duration <= 0 {
		duration = 5 * time.Second
	}
	cfg.LivePlayback = false
	cfg.RecordWAV = true
	cfg.TraceQuality = source == "tone" || source == ""

	var (
		e    *engine
		tone *toneAudio
		err  error
	)
	switch source {
	case "mic":
		e, err = startEngine(cfg)
	case "tone", "":
		tone = newToneAudio(cfg.TraceQuality)
		e, err = startEngineWithAudio(cfg, tone)
	default:
		return engineStats{}, fmt.Errorf("unknown headless source %q", source)
	}
	if err != nil {
		return engineStats{}, err
	}
	time.Sleep(duration)
	e.close()
	stats := e.Stats()
	if tone != nil {
		decoded, loss := e.decodedTraceCopy()
		applyReferenceMetrics(&stats, tone.samplesCopy(), decoded, loss, audioSampleRate)
	}
	return stats, nil
}

func printStatsJSON(stats engineStats) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(stats)
}

func applyReferenceMetrics(stats *engineStats, reference, decoded []float32, loss []bool, sampleRate int) {
	if stats == nil || len(reference) == 0 || len(decoded) == 0 {
		return
	}
	maxLag := sampleRate / 5
	if maxLag > len(reference)-1 {
		maxLag = len(reference) - 1
	}
	if maxLag < 0 {
		return
	}
	bestLag := 0
	bestCorr := -2.0
	step := 120
	for lag := 0; lag <= maxLag; lag += step {
		corr, n := referenceCorrelation(reference, decoded, nil, lag)
		if n > 0 && corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}
	start := bestLag - step
	if start < 0 {
		start = 0
	}
	end := bestLag + step
	if end > maxLag {
		end = maxLag
	}
	for lag := start; lag <= end; lag++ {
		corr, n := referenceCorrelation(reference, decoded, nil, lag)
		if n > 0 && corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}

	stats.ReferenceLagSamples = bestLag
	stats.ReferenceCorrelation = bestCorr
	stats.ReferenceRMSE, stats.ReferenceSNRDB, stats.ReferenceComparedSamples = referenceError(reference, decoded, nil, bestLag)
	stats.LossReferenceCorrelation, _ = referenceCorrelation(reference, decoded, loss, bestLag)
	stats.LossReferenceRMSE, stats.LossReferenceSNRDB, stats.LossComparedSamples = referenceError(reference, decoded, loss, bestLag)
}

func referenceCorrelation(reference, decoded []float32, mask []bool, lag int) (float64, uint64) {
	limit := len(decoded)
	if len(reference)-lag < limit {
		limit = len(reference) - lag
	}
	if limit <= 0 {
		return 0, 0
	}
	var dot, refEnergy, decEnergy float64
	var n uint64
	for i := 0; i < limit; i++ {
		if mask != nil && (i >= len(mask) || !mask[i]) {
			continue
		}
		r := float64(reference[i+lag])
		d := float64(decoded[i])
		dot += r * d
		refEnergy += r * r
		decEnergy += d * d
		n++
	}
	if n == 0 || refEnergy == 0 || decEnergy == 0 {
		return 0, n
	}
	return dot / math.Sqrt(refEnergy*decEnergy), n
}

func referenceError(reference, decoded []float32, mask []bool, lag int) (rmse float64, snrDB float64, compared uint64) {
	limit := len(decoded)
	if len(reference)-lag < limit {
		limit = len(reference) - lag
	}
	if limit <= 0 {
		return 0, 0, 0
	}
	var errEnergy, refEnergy float64
	for i := 0; i < limit; i++ {
		if mask != nil && (i >= len(mask) || !mask[i]) {
			continue
		}
		r := float64(reference[i+lag])
		d := float64(decoded[i])
		diff := r - d
		errEnergy += diff * diff
		refEnergy += r * r
		compared++
	}
	if compared == 0 {
		return 0, 0, 0
	}
	rmse = math.Sqrt(errEnergy / float64(compared))
	if errEnergy == 0 {
		return rmse, 120, compared
	}
	return rmse, 10 * math.Log10(refEnergy/errEnergy), compared
}
