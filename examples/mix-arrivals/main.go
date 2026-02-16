// Package main demonstrates WebRTC-style track mixing with timestamped PCM.
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
	"sort"
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

	mixed, streamStats, err := MixTimedTracksWebRTCStyle(tracks, frameSize)
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
	fmt.Printf("  Stream ingest: accepted=%d, droppedLate=%d, droppedAhead=%d\n",
		streamStats.AcceptedFrames, streamStats.DroppedLate, streamStats.DroppedAhead)
	fmt.Printf("  Peak before normalize: %.3f, applied gain: %.3f\n", peakBefore, appliedGain)
	fmt.Printf("  Output: %s\n", *outPath)
	fmt.Printf("  Duration: %.2fs, frames: %d, encoded bytes: %d, avg bitrate: %.1f kbps\n",
		stats.durationSeconds, stats.frames, stats.encodedBytes, stats.avgBitrateKbps)
}

func MixTimedTracksWebRTCStyle(tracks []TimedTrack, mixFrameSamples int) ([]float32, StreamMixerStats, error) {
	stats := StreamMixerStats{}
	if mixFrameSamples < 1 {
		return nil, stats, fmt.Errorf("mix frame samples must be >= 1")
	}
	if len(tracks) == 0 {
		return make([]float32, 0), stats, nil
	}

	endSample := maxEndSample(tracks, channels)
	totalMixFrames := int((endSample + int64(mixFrameSamples) - 1) / int64(mixFrameSamples))
	if totalMixFrames < 1 {
		totalMixFrames = 1
	}

	mixer, err := NewStreamMixer(StreamMixerConfig{
		Channels:          channels,
		FrameSamples:      mixFrameSamples,
		MaxLookaheadFrame: 160, // ~3.2s at 20 ms frames.
		StartSample:       0,
	})
	if err != nil {
		return nil, stats, err
	}

	for i := range tracks {
		mixer.SetTrackGain(tracks[i].Name, tracks[i].Gain)
	}

	arrivals, err := buildArrivalSchedule(tracks, mixFrameSamples, totalMixFrames)
	if err != nil {
		return nil, stats, err
	}

	framePCM := make([]float32, mixFrameSamples*channels)
	mixed := make([]float32, totalMixFrames*mixFrameSamples*channels)
	nextArrival := 0
	const playoutDelayFrames = 2 // absorb small network jitter before playout
	for tick := 0; tick < totalMixFrames; tick++ {
		ingestLimit := tick + playoutDelayFrames
		for nextArrival < len(arrivals) && arrivals[nextArrival].arrivalTick <= ingestLimit {
			event := arrivals[nextArrival]
			err := mixer.PushFrame(event.trackID, event.startSample, event.pcm)
			if err != nil && err != ErrFrameTooLate {
				return nil, stats, fmt.Errorf("push frame %s @%d: %w", event.trackID, event.startSample, err)
			}
			nextArrival++
		}

		if _, err := mixer.MixNext(framePCM); err != nil {
			return nil, stats, fmt.Errorf("mix frame %d: %w", tick, err)
		}
		copy(mixed[tick*len(framePCM):], framePCM)
	}

	stats = mixer.Stats()
	return mixed[:int(endSample)*channels], stats, nil
}

type encodeStats struct {
	durationSeconds float64
	frames          int
	encodedBytes    int
	avgBitrateKbps  float64
}

type arrivalEvent struct {
	arrivalTick int
	trackID     string
	startSample int64
	pcm         []float32
}

func buildArrivalSchedule(tracks []TimedTrack, mixFrameSamples int, totalMixFrames int) ([]arrivalEvent, error) {
	events := make([]arrivalEvent, 0, len(tracks)*8)
	if totalMixFrames < 1 {
		totalMixFrames = 1
	}

	for i := range tracks {
		track := tracks[i]
		if track.StartSample < 0 {
			return nil, fmt.Errorf("track %q has negative start sample", track.Name)
		}
		if len(track.PCM)%channels != 0 {
			return nil, fmt.Errorf("track %q PCM length (%d) is not aligned to %d channels", track.Name, len(track.PCM), channels)
		}

		totalTrackSamples := len(track.PCM) / channels
		for frameIdx, srcSample := 0, 0; srcSample < totalTrackSamples; frameIdx, srcSample = frameIdx+1, srcSample+mixFrameSamples {
			frameSamples := mixFrameSamples
			if remain := totalTrackSamples - srcSample; remain < frameSamples {
				frameSamples = remain
			}

			framePCM := make([]float32, frameSamples*channels)
			copy(framePCM, track.PCM[srcSample*channels:(srcSample+frameSamples)*channels])

			startSample := int64(track.StartSample + srcSample)
			baseTick := int(startSample / int64(mixFrameSamples))
			jitter := deterministicJitterTick(track.Name, frameIdx)
			arrivalTick := baseTick + jitter
			if arrivalTick < 0 {
				arrivalTick = 0
			}
			if arrivalTick >= totalMixFrames {
				arrivalTick = totalMixFrames - 1
			}

			events = append(events, arrivalEvent{
				arrivalTick: arrivalTick,
				trackID:     track.Name,
				startSample: startSample,
				pcm:         framePCM,
			})
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].arrivalTick != events[j].arrivalTick {
			return events[i].arrivalTick < events[j].arrivalTick
		}
		if events[i].startSample != events[j].startSample {
			return events[i].startSample < events[j].startSample
		}
		return events[i].trackID < events[j].trackID
	})

	return events, nil
}

func deterministicJitterTick(track string, frameIdx int) int {
	seed := uint32(2166136261)
	for i := 0; i < len(track); i++ {
		seed ^= uint32(track[i])
		seed *= 16777619
	}
	seed ^= uint32(frameIdx + 1)
	seed = seed*1664525 + 1013904223
	return int(seed%3) - 1 // -1,0,+1 frame jitter
}

func maxEndSample(tracks []TimedTrack, channels int) int64 {
	var maxEnd int64
	for i := range tracks {
		track := tracks[i]
		samples := int64(len(track.PCM) / channels)
		end := int64(track.StartSample) + samples
		if end > maxEnd {
			maxEnd = end
		}
	}
	return maxEnd
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
