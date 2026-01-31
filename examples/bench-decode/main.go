// Package main benchmarks Opus decode throughput for gopus vs ffmpeg/libopus.
//
// Usage:
//
//	go run .
//	go run . -in input.opus
//	go run . -sample speech -iters 2
//	go run . -url https://example.com/file.opus -mode gopus
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
)

const sampleRate = 48000

var sampleURLs = map[string]string{
	"stereo": "https://opus-codec.org/static/examples/ehren-paper_lights-96.opus",
	"speech": "https://upload.wikimedia.org/wikipedia/commons/6/6a/Hussain_Ahmad_Madani%27s_Voice.ogg",
}

func main() {
	input := flag.String("in", "", "Input Ogg Opus file")
	url := flag.String("url", "", "Download Ogg Opus file from URL (overrides -sample)")
	sample := flag.String("sample", "stereo", "Preset sample to download: stereo or speech")
	iters := flag.Int("iters", 1, "Number of timed iterations per decoder")
	warmup := flag.Int("warmup", 0, "Warmup iterations per decoder")
	mode := flag.String("mode", "both", "Benchmark mode: gopus, ffmpeg, or both")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "Path to ffmpeg binary")
	flag.Parse()

	modeValue := strings.ToLower(strings.TrimSpace(*mode))
	switch modeValue {
	case "gopus", "ffmpeg", "both":
	default:
		log.Fatalf("Invalid -mode %q (use gopus, ffmpeg, or both)", *mode)
	}

	data, label, inputPath, cleanup, err := loadInput(*input, *url, *sample)
	if err != nil {
		log.Fatalf("Load input failed: %v", err)
	}
	defer cleanup()

	fmt.Printf("Input: %s\n", label)

	var durationSec float64
	var gopusSamples int

	if modeValue == "gopus" || modeValue == "both" {
		times, samples, err := benchGopus(data, *iters, *warmup)
		if err != nil {
			log.Fatalf("Gopus benchmark failed: %v", err)
		}
		gopusSamples = samples
		durationSec = float64(samples) / float64(sampleRate)
		printResults("gopus", times, durationSec)
	}

	if modeValue == "ffmpeg" || modeValue == "both" {
		if inputPath == "" {
			log.Fatalf("ffmpeg benchmark requires a file path")
		}
		times, err := benchFFmpeg(inputPath, *ffmpegBin, *iters, *warmup)
		if err != nil {
			log.Fatalf("ffmpeg benchmark failed: %v", err)
		}
		if durationSec == 0 {
			durationSec = estimateDurationSeconds(data)
		}
		printResults("ffmpeg(libopus)", times, durationSec)
	}

	if gopusSamples > 0 {
		fmt.Printf("Decoded samples (per channel): %d\n", gopusSamples)
	}
}

func loadInput(inputPath, urlValue, sample string) ([]byte, string, string, func(), error) {
	inputPath = strings.TrimSpace(inputPath)
	urlValue = strings.TrimSpace(urlValue)

	if inputPath != "" {
		data, err := os.ReadFile(inputPath)
		if err != nil {
			return nil, "", "", nil, err
		}
		return data, inputPath, inputPath, func() {}, nil
	}

	if urlValue == "" {
		resolved, err := resolveSampleURL(sample)
		if err != nil {
			return nil, "", "", nil, err
		}
		urlValue = resolved
	}

	data, err := downloadBytes(urlValue)
	if err != nil {
		return nil, "", "", nil, err
	}

	tmp, err := os.CreateTemp("", "gopus_bench_*.opus")
	if err != nil {
		return nil, "", "", nil, err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, "", "", nil, err
	}
	_ = tmp.Close()
	label := urlValue

	cleanup := func() { _ = os.Remove(tmp.Name()) }
	return data, label, tmp.Name(), cleanup, nil
}

func resolveSampleURL(name string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = "stereo"
	}
	url, ok := sampleURLs[key]
	if !ok {
		return "", fmt.Errorf("unknown sample %q (valid: stereo, speech)", name)
	}
	return url, nil
}

func downloadBytes(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gopus-bench/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("download: unexpected status %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func benchGopus(data []byte, iters, warmup int) ([]time.Duration, int, error) {
	if iters < 1 {
		return nil, 0, errors.New("iters must be >= 1")
	}
	var times []time.Duration
	var samples int
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		count, err := decodeGopusOnce(data)
		if err != nil {
			return nil, 0, err
		}
		dur := time.Since(start)
		if i >= warmup {
			times = append(times, dur)
			samples = count
		}
	}
	return times, samples, nil
}

func decodeGopusOnce(data []byte) (int, error) {
	r := bytes.NewReader(data)
	oggReader, err := ogg.NewReader(r)
	if err != nil {
		return 0, err
	}

	channels := int(oggReader.Channels())
	if channels < 1 {
		return 0, errors.New("invalid channel count")
	}

	dec, err := gopus.NewDecoder(sampleRate, channels)
	if err != nil {
		return 0, err
	}

	remainingSkip := int(oggReader.PreSkip())
	totalSamples := 0

	for {
		packet, _, err := oggReader.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}

		pcm, err := dec.DecodeFloat32(packet)
		if err != nil {
			return 0, err
		}

		frameSamples := len(pcm) / channels
		if remainingSkip > 0 {
			if frameSamples <= remainingSkip {
				remainingSkip -= frameSamples
				continue
			}
			pcm = pcm[remainingSkip*channels:]
			remainingSkip = 0
		}

		if len(pcm) == 0 {
			continue
		}

		totalSamples += len(pcm) / channels
	}

	return totalSamples, nil
}

func benchFFmpeg(inputPath, ffmpegBin string, iters, warmup int) ([]time.Duration, error) {
	if iters < 1 {
		return nil, errors.New("iters must be >= 1")
	}
	if strings.TrimSpace(ffmpegBin) == "" {
		return nil, errors.New("ffmpeg path is empty")
	}
	if _, err := os.Stat(inputPath); err != nil {
		return nil, err
	}

	var times []time.Duration
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		cmd := exec.Command(ffmpegBin,
			"-hide_banner",
			"-loglevel", "error",
			"-i", inputPath,
			"-f", "s16le",
			"-acodec", "pcm_s16le",
			"-")
		cmd.Stdout = io.Discard
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			errText := strings.TrimSpace(stderr.String())
			if errText == "" {
				errText = err.Error()
			}
			return nil, fmt.Errorf("ffmpeg failed: %s", errText)
		}
		dur := time.Since(start)
		if i >= warmup {
			times = append(times, dur)
		}
	}
	return times, nil
}

func estimateDurationSeconds(data []byte) float64 {
	r := bytes.NewReader(data)
	oggReader, err := ogg.NewReader(r)
	if err != nil {
		return 0
	}
	var lastGranule uint64
	for {
		_, granule, err := oggReader.ReadPacket()
		if err != nil {
			break
		}
		lastGranule = granule
	}
	if lastGranule == 0 {
		return 0
	}
	return float64(lastGranule) / float64(sampleRate)
}

func printResults(label string, times []time.Duration, durationSec float64) {
	if len(times) == 0 {
		fmt.Printf("%s: no timings\n", label)
		return
	}
	best := times[0]
	var sum time.Duration
	for _, t := range times {
		sum += t
		if t < best {
			best = t
		}
	}
	avg := time.Duration(int64(sum) / int64(len(times)))

	if durationSec <= 0 {
		fmt.Printf("%s: best %s, avg %s\n", label, best, avg)
		return
	}

	bestRTF := durationSec / best.Seconds()
	avgRTF := durationSec / avg.Seconds()
	fmt.Printf("%s: best %s (%.2fx realtime), avg %s (%.2fx)\n", label, best, bestRTF, avg, avgRTF)
}

func init() {
	if path := os.Getenv("FFMPEG_PATH"); path != "" {
		if filepath.IsAbs(path) {
			_ = path
		}
	}
}
