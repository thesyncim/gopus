// Package main benchmarks Opus encode throughput for gopus vs libopus.
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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
	examplecleanup "github.com/thesyncim/gopus/examples/internal/cleanup"
	"github.com/thesyncim/gopus/internal/benchutil"
)

const sampleRate = 48000

var sampleURLs = map[string]string{
	"stereo": "https://opus-codec.org/static/examples/ehren-paper_lights-96.opus",
	"speech": "https://upload.wikimedia.org/wikipedia/commons/6/6a/Hussain_Ahmad_Madani%27s_Voice.ogg",
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	input := flag.String("in", "", "Input Ogg Opus file (to be used as PCM source)")
	url := flag.String("url", "", "Download Ogg Opus file from URL (overrides -sample)")
	sample := flag.String("sample", "stereo", "Preset sample to download: stereo or speech")
	iters := flag.Int("iters", 3, "Number of timed iterations per encoder")
	warmup := flag.Int("warmup", 0, "Warmup iterations per encoder")
	mode := flag.String("mode", "both", "Benchmark mode: gopus, libopus, or both")
	opusDemo := flag.String("opus-demo", "", "Path to tmp_check/opus-<version>/opus_demo (default: auto-detect pinned libopus)")
	batch := flag.Int("batch", 8, "Number of full-stream repeats per timed iteration to amortize startup overhead")
	bitrate := flag.Int("bitrate", 128000, "Target bitrate in bps")
	complexity := flag.Int("complexity", 10, "Encoder complexity (0-10)")
	frameSize := flag.Int("frame-size", 960, "Frame size in samples (default 960 = 20ms)")
	flag.Parse()

	modeValue := strings.ToLower(strings.TrimSpace(*mode))
	switch modeValue {
	case "gopus", "libopus", "both":
	case "ffmpeg":
		modeValue = "libopus"
	default:
		return fmt.Errorf("invalid -mode %q (use gopus, libopus, or both)", *mode)
	}
	if *iters < 1 {
		return errors.New("-iters must be >= 1")
	}
	if *warmup < 0 {
		return errors.New("-warmup must be >= 0")
	}
	if *batch < 1 {
		return errors.New("-batch must be >= 1")
	}

	data, label, _, cleanup, err := loadInput(*input, *url, *sample)
	if err != nil {
		return fmt.Errorf("load input failed: %w", err)
	}
	defer cleanup()

	fmt.Printf("PCM Source: %s\n", label)
	pcm, channels, err := decodeToPCM(data)
	if err != nil {
		return fmt.Errorf("decode source to PCM: %w", err)
	}
	if err := validateEncodeSettings(*bitrate, *complexity, *frameSize, channels); err != nil {
		return fmt.Errorf("invalid encoder settings: %w", err)
	}

	durationSec := float64(len(pcm)) / float64(sampleRate*channels)
	fmt.Printf("Prepared PCM duration (after pre-skip, before Ogg EOS trim): %.2fs (%d channels)\n", durationSec, channels)
	fmt.Printf("Settings: %d bps, complexity %d, frame size %d, batch %d\n", *bitrate, *complexity, *frameSize, *batch)
	fmt.Println("Timing note: rough CLI timings; libopus includes opus_demo process and file I/O overhead.")

	if modeValue == "gopus" || modeValue == "both" {
		times, err := benchGopus(pcm, channels, *bitrate, *complexity, *frameSize, *batch, *iters, *warmup)
		if err != nil {
			return fmt.Errorf("gopus benchmark failed: %w", err)
		}
		printResults("gopus", times, durationSec*float64(*batch))
	}

	if modeValue == "libopus" || modeValue == "both" {
		fmt.Println("Preparing libopus(opus_demo) PCM input...")
		opusDemoPath := strings.TrimSpace(*opusDemo)
		if opusDemoPath == "" {
			opusDemoPath, err = benchutil.OpusDemoPath()
			if err != nil {
				return fmt.Errorf("resolve opus_demo failed: %w", err)
			}
		}
		repeatedPCM, err := os.CreateTemp("", "gopus_bench_encode_*.f32")
		if err != nil {
			return fmt.Errorf("create libopus input failed: %w", err)
		}
		repeatedPCMPath := repeatedPCM.Name()
		_ = repeatedPCM.Close()
		defer examplecleanup.OnReturn("remove benchmark PCM", func() error { return os.Remove(repeatedPCMPath) })
		if err := benchutil.WriteRepeatedRawFloat32(repeatedPCMPath, pcm, *batch); err != nil {
			return fmt.Errorf("prepare libopus PCM input failed: %w", err)
		}

		fmt.Println("Running libopus(opus_demo) benchmark...")
		times, err := benchLibopus(repeatedPCMPath, channels, opusDemoPath, *bitrate, *complexity, *frameSize, *iters, *warmup)
		if err != nil {
			return fmt.Errorf("libopus benchmark failed: %w", err)
		}
		printResults("libopus(opus_demo)", times, durationSec*float64(*batch))
	}
	return nil
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
	defer examplecleanup.OnReturn("close sample response", resp.Body.Close)
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

func benchGopus(pcm []float32, channels, bitrate, complexity, frameSize, batch, iters, warmup int) ([]time.Duration, error) {
	if iters < 1 {
		return nil, errors.New("iters must be >= 1")
	}
	if warmup < 0 {
		return nil, errors.New("warmup must be >= 0")
	}
	if batch < 1 {
		return nil, errors.New("batch must be >= 1")
	}
	var times []time.Duration
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		err := encodeGopusOnce(pcm, channels, bitrate, complexity, frameSize, batch)
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

func encodeGopusOnce(pcm []float32, channels, bitrate, complexity, frameSize, batch int) error {
	if batch < 1 {
		return errors.New("batch must be >= 1")
	}
	if channels < 1 || channels > 2 {
		return fmt.Errorf("channels must be 1 or 2, got %d", channels)
	}
	if len(pcm)%channels != 0 {
		return fmt.Errorf("PCM sample count %d is not aligned to %d channels", len(pcm), channels)
	}
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return err
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return fmt.Errorf("set bitrate: %w", err)
	}
	if err := enc.SetComplexity(complexity); err != nil {
		return fmt.Errorf("set complexity: %w", err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeVBR); err != nil {
		return fmt.Errorf("set bitrate mode: %w", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		return fmt.Errorf("set frame size: %w", err)
	}

	frameCount, totalSamples, err := repeatedInputFrameCount(len(pcm)/channels, frameSize, batch)
	if err != nil {
		return err
	}
	frame := make([]int32, frameSize*channels)
	packetBuf := make([]byte, 15000)
	for frameIndex := 0; frameIndex < frameCount; frameIndex++ {
		if err := fillRepeatedInt24Frame(pcm, channels, frameSize, totalSamples, frameIndex, frame); err != nil {
			return err
		}
		if _, err := enc.EncodeInt24(frame, packetBuf); err != nil {
			return fmt.Errorf("encode frame %d: %w", frameIndex, err)
		}
	}
	return nil
}

func validateEncodeSettings(bitrate, complexity, frameSize, channels int) error {
	if _, err := benchutil.FrameSizeArg(frameSize); err != nil {
		return err
	}
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return fmt.Errorf("create validation encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return fmt.Errorf("bitrate: %w", err)
	}
	if err := enc.SetComplexity(complexity); err != nil {
		return fmt.Errorf("complexity: %w", err)
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeVBR); err != nil {
		return fmt.Errorf("bitrate mode: %w", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		return fmt.Errorf("frame size: %w", err)
	}
	return nil
}

// repeatedInputFrameCount mirrors opus_demo's encode-only EOF behavior: a
// partial last frame is zero padded, and an exact frame multiple gets one
// additional all-zero frame when the next read reaches EOF.
func repeatedInputFrameCount(inputSamples, frameSize, batch int) (int, int, error) {
	if inputSamples < 0 || frameSize < 1 {
		return 0, 0, fmt.Errorf("invalid input samples/frame size %d/%d", inputSamples, frameSize)
	}
	if batch < 1 {
		return 0, 0, errors.New("batch must be >= 1")
	}
	maxInt := int(^uint(0) >> 1)
	if inputSamples > maxInt/batch {
		return 0, 0, errors.New("repeated PCM sample count overflows int")
	}
	totalSamples := inputSamples * batch
	frameCount := totalSamples / frameSize
	if frameCount == maxInt {
		return 0, 0, errors.New("encoded frame count overflows int")
	}
	return frameCount + 1, totalSamples, nil
}

// fillRepeatedInt24Frame frames the repeated input as one continuous stream
// and converts each float sample the way opus_demo's FORMAT_F32_LE path does.
func fillRepeatedInt24Frame(pcm []float32, channels, frameSize, totalSamples, frameIndex int, dst []int32) error {
	if channels < 1 || channels > 2 || len(pcm)%channels != 0 {
		return fmt.Errorf("invalid PCM channel layout: %d channels, %d samples", channels, len(pcm))
	}
	if frameSize < 1 || frameSize > int(^uint(0)>>1)/channels || len(dst) != frameSize*channels {
		return fmt.Errorf("invalid frame buffer for frame size %d and %d channels", frameSize, channels)
	}
	inputSamples := len(pcm) / channels
	if totalSamples < 0 || (totalSamples > 0 && inputSamples == 0) || frameIndex < 0 || frameIndex > totalSamples/frameSize {
		return errors.New("invalid repeated PCM frame position")
	}
	clear(dst)
	startSample := frameIndex * frameSize
	available := totalSamples - startSample
	for i := 0; i < frameSize && i < available; i++ {
		sourceFrame := ((startSample + i) % inputSamples) * channels
		for channel := 0; channel < channels; channel++ {
			sample := pcm[sourceFrame+channel] * 8388608
			dst[i*channels+channel] = int32(math.Floor(0.5 + float64(sample)))
		}
	}
	return nil
}

func benchLibopus(pcmPath string, channels int, opusDemoPath string, bitrate, complexity, frameSize, iters, warmup int) ([]time.Duration, error) {
	if iters < 1 {
		return nil, errors.New("iters must be >= 1")
	}
	if strings.TrimSpace(opusDemoPath) == "" {
		return nil, errors.New("opus_demo path is empty")
	}
	frameArg, err := benchutil.FrameSizeArg(frameSize)
	if err != nil {
		return nil, err
	}

	var times []time.Duration
	for i := 0; i < iters+warmup; i++ {
		start := time.Now()
		cmd := exec.Command(opusDemoPath,
			"-e",
			"audio",
			strconv.Itoa(sampleRate),
			strconv.Itoa(channels),
			strconv.Itoa(bitrate),
			"-f32",
			"-complexity", strconv.Itoa(complexity),
			"-framesize", frameArg,
			pcmPath,
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
