// Package main demonstrates mixing tracks that start at different times.
//
// Usage:
//
//	go run ./examples/mix-arrivals -out mixed_arrivals.opus
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

const (
	sampleRate = 48000
	channels   = 2
	frameSize  = 960 // 20 ms at 48 kHz
)

func main() {
	outPath := flag.String("out", "mixed_arrivals.opus", "Output Ogg Opus file path")
	bitrate := flag.Int("bitrate", 128000, "Target bitrate in bps")
	flag.Parse()

	tracks := []TimedTrack{
		{
			Name:        "pad",
			StartSample: durationToSamples(0, sampleRate),
			Gain:        0.70,
			PCM:         synthPad(4 * time.Second),
		},
		{
			Name:        "bass",
			StartSample: durationToSamples(350*time.Millisecond, sampleRate),
			Gain:        0.85,
			PCM:         synthBass(3200 * time.Millisecond),
		},
		{
			Name:        "lead",
			StartSample: durationToSamples(900*time.Millisecond, sampleRate),
			Gain:        0.75,
			PCM:         synthLead(2600 * time.Millisecond),
		},
	}

	mixed, err := MixTimedTracks(tracks, channels)
	if err != nil {
		log.Fatalf("mix tracks: %v", err)
	}

	peakBefore, appliedGain := NormalizePeakInPlace(mixed, 0.98)
	stats, err := encodeMixToOgg(*outPath, mixed, *bitrate)
	if err != nil {
		log.Fatalf("encode output: %v", err)
	}

	fmt.Println("Mixing timed tracks into one output")
	for i := range tracks {
		track := tracks[i]
		trackSeconds := float64(len(track.PCM)/channels) / sampleRate
		startMs := 1000 * float64(track.StartSample) / sampleRate
		fmt.Printf("  - %s: start=%.0fms, duration=%.2fs, gain=%.2f\n", track.Name, startMs, trackSeconds, track.Gain)
	}
	fmt.Printf("  Peak before normalize: %.3f, applied gain: %.3f\n", peakBefore, appliedGain)
	fmt.Printf("  Output: %s\n", *outPath)
	fmt.Printf("  Duration: %.2fs, frames: %d, encoded bytes: %d, avg bitrate: %.1f kbps\n",
		stats.durationSeconds, stats.frames, stats.encodedBytes, stats.avgBitrateKbps)
}

type encodeStats struct {
	durationSeconds float64
	frames          int
	encodedBytes    int
	avgBitrateKbps  float64
}

func encodeMixToOgg(path string, pcm []float32, bitrate int) (encodeStats, error) {
	stats := encodeStats{}
	if len(pcm)%channels != 0 {
		return stats, fmt.Errorf("mixed PCM is not aligned to %d channels", channels)
	}
	if len(pcm) == 0 {
		return stats, fmt.Errorf("mixed PCM is empty")
	}

	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		return stats, fmt.Errorf("create encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return stats, fmt.Errorf("set bitrate: %w", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		return stats, fmt.Errorf("set frame size: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return stats, fmt.Errorf("create output: %w", err)
	}
	defer f.Close()

	ow, err := ogg.NewWriter(f, sampleRate, channels)
	if err != nil {
		return stats, fmt.Errorf("create ogg writer: %w", err)
	}

	totalSamples := len(pcm) / channels
	stats.durationSeconds = float64(totalSamples) / sampleRate
	stats.frames = (totalSamples + frameSize - 1) / frameSize
	framePCM := make([]float32, frameSize*channels)
	packet := make([]byte, 4000)

	for frame := 0; frame < stats.frames; frame++ {
		for i := range framePCM {
			framePCM[i] = 0
		}

		start := frame * frameSize * channels
		if start < len(pcm) {
			end := start + len(framePCM)
			if end > len(pcm) {
				end = len(pcm)
			}
			copy(framePCM, pcm[start:end])
		}

		n, err := enc.Encode(framePCM, packet)
		if err != nil {
			return stats, fmt.Errorf("encode frame %d: %w", frame, err)
		}
		if n == 0 {
			continue
		}

		if err := ow.WritePacket(packet[:n], frameSize); err != nil {
			return stats, fmt.Errorf("write packet %d: %w", frame, err)
		}
		stats.encodedBytes += n
	}

	if err := ow.Close(); err != nil {
		return stats, fmt.Errorf("close ogg writer: %w", err)
	}
	if stats.durationSeconds > 0 {
		stats.avgBitrateKbps = float64(stats.encodedBytes*8) / stats.durationSeconds / 1000
	}

	return stats, nil
}

func synthPad(duration time.Duration) []float32 {
	pcm := synthTone(duration, 220, 0.30, -0.25, 0.20)
	upper := synthTone(duration, 330, 0.18, -0.25, 0.11)
	for i := range pcm {
		pcm[i] += upper[i]
	}
	return pcm
}

func synthBass(duration time.Duration) []float32 {
	samples := durationToSamples(duration, sampleRate)
	pcm := make([]float32, samples*channels)
	left, right := equalPowerPan(0)

	beatSamples := sampleRate / 2 // 120 BPM quarter-note pulses.
	if beatSamples < 1 {
		beatSamples = 1
	}

	for i := 0; i < samples; i++ {
		t := float64(i) / sampleRate
		beatPos := float64(i%beatSamples) / float64(beatSamples)
		envelope := math.Exp(-9 * beatPos)
		s := float32(math.Sin(2*math.Pi*110*t)*envelope) * 0.45
		pcm[i*2] = s * left
		pcm[i*2+1] = s * right
	}

	return pcm
}

func synthLead(duration time.Duration) []float32 {
	samples := durationToSamples(duration, sampleRate)
	pcm := make([]float32, samples*channels)
	left, right := equalPowerPan(0.35)

	denom := float64(samples - 1)
	if denom < 1 {
		denom = 1
	}

	for i := 0; i < samples; i++ {
		t := float64(i) / sampleRate
		progress := float64(i) / denom
		freq := 440 + 440*progress
		vibrato := 0.004 * math.Sin(2*math.Pi*5*t)
		phase := 2 * math.Pi * (freq*t + vibrato)
		env := 0.5 - 0.5*math.Cos(2*math.Pi*progress)
		s := float32(math.Sin(phase)*env) * 0.30
		pcm[i*2] = s * left
		pcm[i*2+1] = s * right
	}

	return pcm
}

func synthTone(duration time.Duration, freq float64, amp float32, pan float64, lfoHz float64) []float32 {
	samples := durationToSamples(duration, sampleRate)
	pcm := make([]float32, samples*channels)
	left, right := equalPowerPan(pan)

	denom := float64(samples - 1)
	if denom < 1 {
		denom = 1
	}

	for i := 0; i < samples; i++ {
		t := float64(i) / sampleRate
		progress := float64(i) / denom
		freqLFO := 1 + 0.01*math.Sin(2*math.Pi*lfoHz*t)
		phase := 2 * math.Pi * freq * t * freqLFO
		env := 0.5 - 0.5*math.Cos(2*math.Pi*progress)
		s := float32(math.Sin(phase)*env) * amp
		pcm[i*2] = s * left
		pcm[i*2+1] = s * right
	}

	return pcm
}

func equalPowerPan(pan float64) (float32, float32) {
	if pan < -1 {
		pan = -1
	} else if pan > 1 {
		pan = 1
	}
	angle := (pan + 1) * math.Pi / 4
	return float32(math.Cos(angle)), float32(math.Sin(angle))
}

func durationToSamples(d time.Duration, rate int) int {
	if d <= 0 || rate <= 0 {
		return 0
	}
	return int((d.Nanoseconds()*int64(rate) + int64(time.Second)/2) / int64(time.Second))
}
