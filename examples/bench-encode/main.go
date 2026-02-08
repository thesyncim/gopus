// Package main benchmarks Opus encode throughput for gopus vs ffmpeg/libopus.
//
// Usage:
//
//	go run .
//	go run . -in input.opus
//	go run . -sample speech -iters 2
//	go run . -bitrate 128000 -complexity 10
package main

import (
	"bytes"
	"encoding/binary"
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
	input := flag.String("in", "", "Input Ogg Opus file (to be used as PCM source)")
	url := flag.String("url", "", "Download Ogg Opus file from URL (overrides -sample)")
	sample := flag.String("sample", "stereo", "Preset sample to download: stereo or speech")
	iters := flag.Int("iters", 1, "Number of timed iterations per encoder")
	warmup := flag.Int("warmup", 0, "Warmup iterations per encoder")
	mode := flag.String("mode", "both", "Benchmark mode: gopus, ffmpeg, or both")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "Path to ffmpeg binary")
	bitrate := flag.Int("bitrate", 128000, "Target bitrate in bps")
	complexity := flag.Int("complexity", 10, "Encoder complexity (0-10)")
	frameSize := flag.Int("frame-size", 960, "Frame size in samples (default 960 = 20ms)")
	flag.Parse()

	modeValue := strings.ToLower(strings.TrimSpace(*mode))
	switch modeValue {
	case "gopus", "ffmpeg", "both":
	default:
		log.Fatalf("Invalid -mode %q (use gopus, ffmpeg, or both)", *mode)
	}

	data, label, _, cleanup, err := loadInput(*input, *url, *sample)
	if err != nil {
		log.Fatalf("Load input failed: %v", err)
	}
	defer cleanup()

	fmt.Printf("PCM Source: %s\n", label)
	pcm, channels, err := decodeToPCM(data)
	if err != nil {
		log.Fatalf("Failed to decode source to PCM: %v", err)
	}

	durationSec := float64(len(pcm)) / float64(sampleRate*channels)
	fmt.Printf("Audio duration: %.2fs (%d channels)\n", durationSec, channels)
	fmt.Printf("Settings: %d bps, complexity %d, frame size %d\n", *bitrate, *complexity, *frameSize)

	if modeValue == "gopus" || modeValue == "both" {
		times, err := benchGopus(pcm, channels, *bitrate, *complexity, *frameSize, *iters, *warmup)
		if err != nil {
			log.Fatalf("Gopus benchmark failed: %v", err)
		}
		printResults("gopus", times, durationSec)
	}

	if modeValue == "ffmpeg" || modeValue == "both" {
		times, err := benchFFmpeg(pcm, channels, *ffmpegBin, *bitrate, *complexity, *iters, *warmup)
		if err != nil {
			log.Fatalf("ffmpeg benchmark failed: %v", err)
		}
		printResults("ffmpeg(libopus)", times, durationSec)
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

func decodeToPCM(data []byte) ([]float32, int, error) {
	r := bytes.NewReader(data)
	oggReader, err := ogg.NewReader(r)
	if err != nil {
		return nil, 0, err
	}

	channels := int(oggReader.Channels())
	if channels < 1 {
		return nil, 0, errors.New("invalid channel count")
	}

	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		return nil, 0, err
	}
	pcmFrame := make([]float32, cfg.MaxPacketSamples*cfg.Channels)

	remainingSkip := int(oggReader.PreSkip())
	var fullPCM []float32

	for {
		packet, _, err := oggReader.ReadPacket()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, 0, err
		}

		n, err := dec.Decode(packet, pcmFrame)
		if err != nil {
			return nil, 0, err
		}

		frameSamples := n
		offset := 0
		if remainingSkip > 0 {
			if frameSamples <= remainingSkip {
				remainingSkip -= frameSamples
				continue
			}
			offset = remainingSkip
			frameSamples -= remainingSkip
			remainingSkip = 0
		}

		if frameSamples == 0 {
			continue
		}

		fullPCM = append(fullPCM, pcmFrame[offset*channels:(offset+frameSamples)*channels]...)
	}

	return fullPCM, channels, nil
}

func benchGopus(pcm []float32, channels, bitrate, complexity, frameSize, iters, warmup int) ([]time.Duration, error) {
	if iters < 1 {
		return nil, errors.New("iters must be >= 1")
	}
	var times []time.Duration
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		err := encodeGopusOnce(pcm, channels, bitrate, complexity, frameSize)
		if err != nil {
			return nil, err
		}
		dur := time.Since(start)
		if i >= warmup {
			times = append(times, dur)
		}
	}
	return times, nil
}

func encodeGopusOnce(pcm []float32, channels, bitrate, complexity, frameSize int) error {
	enc, err := gopus.NewEncoder(sampleRate, channels, gopus.ApplicationAudio)
	if err != nil {
		return err
	}
	_ = enc.SetBitrate(bitrate)
	_ = enc.SetComplexity(complexity)
	_ = enc.SetFrameSize(frameSize)

	packetBuf := make([]byte, 4000)
	step := frameSize * channels
	for i := 0; i+step <= len(pcm); i += step {
		_, err := enc.Encode(pcm[i:i+step], packetBuf)
		if err != nil {
			return err
		}
	}

	return nil
}

func benchFFmpeg(pcm []float32, channels int, ffmpegBin string, bitrate, complexity, iters, warmup int) ([]time.Duration, error) {
	if iters < 1 {
		return nil, errors.New("iters must be >= 1")
	}
	
	// Convert PCM to bytes for ffmpeg
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, pcm)
	if err != nil {
		return nil, err
	}
	pcmBytes := buf.Bytes()

	var times []time.Duration
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		cmd := exec.Command(ffmpegBin,
			"-hide_banner",
			"-loglevel", "error",
			"-f", "f32le",
			"-ar", fmt.Sprintf("%d", sampleRate),
			"-ac", fmt.Sprintf("%d", channels),
			"-i", "-",
			"-c:a", "libopus",
			"-b:a", fmt.Sprintf("%d", bitrate),
			"-vbr", "on",
			"-compression_level", fmt.Sprintf("%d", complexity),
			"-f", "null",
			"-")
		cmd.Stdin = bytes.NewReader(pcmBytes)
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