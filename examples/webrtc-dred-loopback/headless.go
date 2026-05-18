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
}

func newToneAudio() *toneAudio {
	return &toneAudio{}
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
	return len(dst)
}

func (t *toneAudio) queuePlayback(_ []float32) {}

func (t *toneAudio) setLivePlayback(_ bool) {}

func (t *toneAudio) close() {}

func runHeadless(cfg engineConfig, source string, duration time.Duration) (engineStats, error) {
	if duration <= 0 {
		duration = 5 * time.Second
	}
	cfg.LivePlayback = false
	cfg.RecordWAV = true

	var (
		e   *engine
		err error
	)
	switch source {
	case "mic":
		e, err = startEngine(cfg)
	case "tone", "":
		e, err = startEngineWithAudio(cfg, newToneAudio())
	default:
		return engineStats{}, fmt.Errorf("unknown headless source %q", source)
	}
	if err != nil {
		return engineStats{}, err
	}
	time.Sleep(duration)
	e.close()
	return e.Stats(), nil
}

func printStatsJSON(stats engineStats) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(stats)
}
