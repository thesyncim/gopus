// Package main benchmarks Opus decode throughput for gopus vs libopus.
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
	"strconv"
	"strings"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	"github.com/thesyncim/gopus/internal/benchutil"
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
	mode := flag.String("mode", "both", "Benchmark mode: gopus, libopus, or both")
	opusDemo := flag.String("opus-demo", "", "Path to tmp_check/opus-<version>/opus_demo (default: auto-detect pinned libopus)")
	batch := flag.Int("batch", 8, "Number of full-stream repeats per timed iteration to amortize startup overhead")
	flag.Parse()

	modeValue := strings.ToLower(strings.TrimSpace(*mode))
	switch modeValue {
	case "gopus", "libopus", "both":
	case "ffmpeg":
		modeValue = "libopus"
	default:
		log.Fatalf("Invalid -mode %q (use gopus, libopus, or both)", *mode)
	}
	if *batch < 1 {
		log.Fatal("-batch must be >= 1")
	}

	data, label, _, cleanup, err := loadInput(*input, *url, *sample)
	if err != nil {
		log.Fatalf("Load input failed: %v", err)
	}
	defer cleanup()

	fmt.Printf("Input: %s\n", label)
	fmt.Printf("Batch: %d full-stream repeats per timed iteration\n", *batch)

	packets, channels, baseSamples, err := parsePacketStream(data)
	if err != nil {
		log.Fatalf("Parse packet stream failed: %v", err)
	}
	durationSec := float64(baseSamples*(*batch)) / float64(sampleRate)

	var gopusSamples int

	if modeValue == "gopus" || modeValue == "both" {
		times, samples, err := benchGopus(packets, channels, *batch, *iters, *warmup)
		if err != nil {
			log.Fatalf("Gopus benchmark failed: %v", err)
		}
		gopusSamples = samples
		printResults("gopus", times, durationSec)
	}

	if modeValue == "libopus" || modeValue == "both" {
		fmt.Println("Preparing libopus(opus_demo) bitstream input...")
		opusDemoPath := strings.TrimSpace(*opusDemo)
		if opusDemoPath == "" {
			opusDemoPath, err = benchutil.OpusDemoPath()
			if err != nil {
				log.Fatalf("Resolve opus_demo failed: %v", err)
			}
		}
		bitstream, err := os.CreateTemp("", "gopus_bench_decode_*.bit")
		if err != nil {
			log.Fatalf("Create libopus bitstream failed: %v", err)
		}
		bitstreamPath := bitstream.Name()
		_ = bitstream.Close()
		defer os.Remove(bitstreamPath)
		if err := benchutil.WriteRepeatedOpusDemoBitstream(bitstreamPath, packets, *batch); err != nil {
			log.Fatalf("Prepare libopus bitstream failed: %v", err)
		}
		fmt.Println("Running libopus(opus_demo) benchmark...")
		times, err := benchLibopus(bitstreamPath, channels, opusDemoPath, *iters, *warmup)
		if err != nil {
			log.Fatalf("libopus benchmark failed: %v", err)
		}
		printResults("libopus(opus_demo)", times, durationSec)
	}

	if gopusSamples > 0 {
		fmt.Printf("Decoded samples (per channel, batched): %d\n", gopusSamples)
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

func parsePacketStream(data []byte) ([][]byte, int, int, error) {
	r := bytes.NewReader(data)
	oggReader, err := ogg.NewReader(r)
	if err != nil {
		return nil, 0, 0, err
	}

	channels := int(oggReader.Channels())
	if channels < 1 {
		return nil, 0, 0, errors.New("invalid channel count")
	}

	var packets [][]byte
	for {
		packet, _, err := oggReader.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, 0, 0, err
		}
		dup := make([]byte, len(packet))
		copy(dup, packet)
		packets = append(packets, dup)
	}
	if len(packets) == 0 {
		return nil, 0, 0, errors.New("input contains no packets")
	}

	samples, err := decodeGopusOnce(packets, channels, 1)
	if err != nil {
		return nil, 0, 0, err
	}
	return packets, channels, samples, nil
}

func benchGopus(packets [][]byte, channels, batch, iters, warmup int) ([]time.Duration, int, error) {
	if iters < 1 {
		return nil, 0, errors.New("iters must be >= 1")
	}
	var times []time.Duration
	var samples int
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		count, err := decodeGopusOnce(packets, channels, batch)
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

func decodeGopusOnce(packets [][]byte, channels, batch int) (int, error) {
	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return 0, err
	}
	pcmOut := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	totalSamples := 0

	for r := 0; r < batch; r++ {
		for _, packet := range packets {
			n, err := dec.Decode(packet, pcmOut)
			if err != nil {
				return 0, err
			}
			totalSamples += n
		}
	}

	return totalSamples, nil
}

func benchLibopus(bitstreamPath string, channels int, opusDemoPath string, iters, warmup int) ([]time.Duration, error) {
	if iters < 1 {
		return nil, errors.New("iters must be >= 1")
	}
	if strings.TrimSpace(opusDemoPath) == "" {
		return nil, errors.New("opus_demo path is empty")
	}

	var times []time.Duration
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		cmd := exec.Command(opusDemoPath,
			"-d",
			strconv.Itoa(sampleRate),
			strconv.Itoa(channels),
			"-f32",
			bitstreamPath,
			os.DevNull,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("opus_demo failed: %v (%s)", err, bytes.TrimSpace(out))
		}
		dur := time.Since(start)
		if i >= warmup {
			times = append(times, dur)
		}
	}
	return times, nil
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
	if path := os.Getenv("OPUS_DEMO_PATH"); path != "" {
		if filepath.IsAbs(path) {
			_ = path
		}
	}
}
