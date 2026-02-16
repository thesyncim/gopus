// Package main demonstrates WebRTC-style speech mixing with real open-source clips.
//
// Usage:
//
//	go run ./examples/mix-arrivals -out mixed_arrivals.opus
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	play := flag.Bool("play", false, "Play the mixed output after encoding")
	cacheDir := flag.String("cache-dir", filepath.Join(".cache", "mix-arrivals"), "Cache directory for downloaded speech clips")
	loss := flag.Float64("loss", 0.08, "Base random frame-loss probability (0..1)")
	burstStart := flag.Float64("burst-start", 0.10, "Probability of entering burst-loss state (0..1)")
	burstKeep := flag.Float64("burst-keep", 0.55, "Probability to continue dropping while in burst-loss state (0..1)")
	plc := flag.Bool("plc", true, "Enable simple PLC-style concealment on dropped frames")
	jitterNeg := flag.Int("jitter-neg", 1, "Max early-arrival jitter in frames")
	jitterPos := flag.Int("jitter-pos", 2, "Max late-arrival jitter in frames")
	seed := flag.Uint("seed", 1337, "Deterministic seed for loss/jitter simulation")
	flag.Parse()

	tracks, sourceInfo, err := LoadOpenSourceSpeechTracks(*cacheDir)
	if err != nil {
		log.Fatalf("load open-source speech tracks: %v", err)
	}

	netCfg := DefaultNetworkSimConfig()
	netCfg.BaseLossProbability = *loss
	netCfg.BurstStartProbability = *burstStart
	netCfg.BurstContinueProbability = *burstKeep
	netCfg.EnablePLC = *plc
	netCfg.MaxNegativeJitterFrames = *jitterNeg
	netCfg.MaxPositiveJitterFrames = *jitterPos
	netCfg.Seed = uint32(*seed)

	mixed, streamStats, netStats, err := MixTimedTracksWebRTCWithNetwork(tracks, frameSize, netCfg)
	if err != nil {
		log.Fatalf("mix tracks: %v", err)
	}

	peakBefore, appliedGain := NormalizePeakInPlace(mixed, 0.98)
	stats, err := encodeMixToOgg(*outPath, mixed, *bitrate)
	if err != nil {
		log.Fatalf("encode output: %v", err)
	}

	fmt.Println("Mixing open-source speech tracks into one output")
	fmt.Printf("  Source dataset: %s\n", sourceInfo.Dataset)
	fmt.Printf("  Source license: %s\n", sourceInfo.License)
	fmt.Printf("  Source URL: %s\n", sourceInfo.RepositoryURL)
	fmt.Printf("  Downloaded clips: %d (cache: %s)\n", sourceInfo.ClipCount, *cacheDir)
	for i := range tracks {
		track := tracks[i]
		trackSeconds := float64(len(track.PCM)/channels) / sampleRate
		startMs := 1000 * float64(track.StartSample) / sampleRate
		fmt.Printf("  - %s: start=%.0fms, duration=%.2fs, gain=%.2f\n", track.Name, startMs, trackSeconds, track.Gain)
	}
	fmt.Printf("  Network simulation: generated=%d, dropped=%d, concealed=%d\n",
		netStats.GeneratedFrames, netStats.DroppedByNetwork, netStats.ConcealedFrames)
	fmt.Printf("  Stream ingest: accepted=%d, droppedLate=%d, droppedAhead=%d\n",
		streamStats.AcceptedFrames, streamStats.DroppedLate, streamStats.DroppedAhead)
	fmt.Printf("  Peak before normalize: %.3f, applied gain: %.3f\n", peakBefore, appliedGain)
	fmt.Printf("  Output: %s\n", *outPath)
	fmt.Printf("  Duration: %.2fs, frames: %d, encoded bytes: %d, avg bitrate: %.1f kbps\n",
		stats.durationSeconds, stats.frames, stats.encodedBytes, stats.avgBitrateKbps)

	if *play {
		if err := playEncodedOutput(*outPath); err != nil {
			log.Printf("playback unavailable: %v", err)
			fmt.Printf("Play manually: %s\n", *outPath)
		}
	}
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
