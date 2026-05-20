package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
)

type syntheticAudio struct {
	sample int
	trace  bool
	kind   string
	pcm    []float32
}

func newSyntheticAudio(kind string, trace bool) *syntheticAudio {
	if kind == "" {
		kind = "speech"
	}
	return &syntheticAudio{kind: kind, trace: trace}
}

func (a *syntheticAudio) readCaptureFrame(dst []float32) int {
	for i := 0; i < len(dst); i++ {
		n := a.sample + i
		sec := float64(n) / audioSampleRate
		if a.kind == "tone" {
			env := 0.65 + 0.25*math.Sin(2*math.Pi*2.1*sec)
			v := 0.32*math.Sin(2*math.Pi*180*sec) +
				0.18*math.Sin(2*math.Pi*360*sec+0.2) +
				0.08*math.Sin(2*math.Pi*720*sec+0.4)
			dst[i] = float32(env * v)
			continue
		}
		phase := 2 * math.Pi * (150.0*sec -
			28.0*math.Cos(2*math.Pi*0.61*sec)/(2*math.Pi*0.61) -
			9.0*math.Cos(2*math.Pi*2.3*sec+0.4)/(2*math.Pi*2.3))
		voiced := 0.0
		for h := 1; h <= 6; h++ {
			amp := math.Exp(-0.34 * float64(h-1))
			voiced += amp * math.Sin(float64(h)*phase+0.17*float64(h*h))
		}
		formants := 0.16*math.Sin(2*math.Pi*(620.0+90.0*math.Sin(2*math.Pi*0.41*sec))*sec+0.4) +
			0.10*math.Sin(2*math.Pi*(1320.0+180.0*math.Sin(2*math.Pi*0.27*sec))*sec+0.9)
		syllable := 0.55 + 0.45*math.Pow(0.5+0.5*math.Sin(2*math.Pi*2.7*sec+0.2), 2)
		onset := 1.0
		if n < audioSampleRate/5 {
			onset = float64(n) / float64(audioSampleRate/5)
		}
		dst[i] = float32(0.20 * onset * syllable * (0.68*voiced + formants))
	}
	a.sample += len(dst)
	if a.trace {
		a.pcm = append(a.pcm, dst...)
	}
	return len(dst)
}

func (a *syntheticAudio) queuePlayback(_ []float32) {}

func (a *syntheticAudio) setLivePlayback(_ bool) {}

func (a *syntheticAudio) close() {}

func (a *syntheticAudio) samplesCopy() []float32 {
	if a == nil {
		return nil
	}
	return append([]float32(nil), a.pcm...)
}

func runHeadless(cfg engineConfig, source string, duration time.Duration) (engineStats, error) {
	if duration <= 0 {
		duration = 5 * time.Second
	}
	cfg.LivePlayback = false
	cfg.RecordWAV = true
	cfg.TraceQuality = source == "speech" || source == "tone" || source == ""

	var (
		e         *engine
		synthetic *syntheticAudio
		err       error
	)
	switch source {
	case "mic":
		e, err = startEngine(cfg)
	case "speech", "tone", "":
		synthetic = newSyntheticAudio(source, cfg.TraceQuality)
		e, err = startEngineWithAudio(cfg, synthetic)
	default:
		return engineStats{}, fmt.Errorf("unknown headless source %q", source)
	}
	if err != nil {
		return engineStats{}, err
	}
	time.Sleep(duration)
	e.close()
	stats := e.Stats()
	if synthetic != nil {
		decoded, loss := e.decodedTraceCopy()
		applyReferenceMetrics(&stats, synthetic.samplesCopy(), decoded, loss, audioSampleRate)
	}
	return stats, nil
}

type comparisonResult struct {
	Name  string
	FEC   bool
	RED   bool
	DRED  bool
	Stats engineStats
}

func runHeadlessComparison(cfg engineConfig, source string, duration time.Duration) ([]comparisonResult, error) {
	cases := []struct {
		name string
		fec  bool
		red  bool
		dred bool
	}{
		{name: "plc"},
		{name: "fec", fec: true},
		{name: "red", red: true},
		{name: "red+fec", red: true, fec: true},
		{name: "dred", dred: true},
		{name: "fec+dred", fec: true, dred: true},
		{name: "red+dred", red: true, dred: true},
		{name: "red+fec+dred", red: true, fec: true, dred: true},
	}

	results := make([]comparisonResult, 0, len(cases))
	baseRecordDir := cfg.RecordDir
	for _, tc := range cases {
		runCfg := cfg
		runCfg.FEC = tc.fec
		runCfg.RED = tc.red
		runCfg.DRED = tc.dred
		if baseRecordDir != "" {
			runCfg.RecordDir = baseRecordDir + "/" + tc.name
		}
		stats, err := runHeadless(runCfg, source, duration)
		if err != nil {
			return nil, fmt.Errorf("%s comparison: %w", tc.name, err)
		}
		results = append(results, comparisonResult{
			Name:  tc.name,
			FEC:   tc.fec,
			RED:   tc.red,
			DRED:  tc.dred,
			Stats: stats,
		})
	}
	return results, nil
}

func printStatsJSON(stats engineStats) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(stats)
}

func printComparisonJSON(results []comparisonResult) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

func applyReferenceMetrics(stats *engineStats, reference, decoded []float32, loss []bool, sampleRate int) {
	if stats == nil || len(reference) == 0 || len(decoded) == 0 {
		return
	}
	maxLag := 2 * frameSamples
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
	stats.ReferenceIntelligibility = intelligibilityScore(reference, decoded, nil, bestLag, sampleRate)
	stats.LossIntelligibility = intelligibilityScore(reference, decoded, loss, bestLag, sampleRate)
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
