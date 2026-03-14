package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
)

type soakConfig struct {
	duration           time.Duration
	reportInterval     time.Duration
	maxRSSGrowthMiB    int64
	maxGoroutineGrowth int
	maxHotPathAllocs   float64
	seed               int64
}

type soakStats struct {
	iterations    uint64
	encodes       uint64
	decodes       uint64
	encodeErrors  uint64
	decodeErrors  uint64
	panics        uint64
	truncated     uint64
	corrupted     uint64
	duplicated    uint64
	reordered     uint64
	randomInputs  uint64
	plc           uint64
	peakRSSBytes  uint64
	peakGoroutine int
}

func main() {
	cfg := soakConfig{}
	flag.DurationVar(&cfg.duration, "duration", 30*time.Second, "how long to run the randomized soak")
	flag.DurationVar(&cfg.reportInterval, "report-interval", 10*time.Second, "how often to print status")
	flag.Int64Var(&cfg.maxRSSGrowthMiB, "max-rss-growth-mib", 256, "maximum allowed RSS growth in MiB")
	flag.IntVar(&cfg.maxGoroutineGrowth, "max-goroutine-growth", 16, "maximum allowed goroutine growth")
	flag.Float64Var(&cfg.maxHotPathAllocs, "max-hotpath-allocs", 0.0, "maximum allowed allocs/op for root encode/decode hot paths")
	flag.Int64Var(&cfg.seed, "seed", 1, "random seed")
	flag.Parse()

	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "safety soak failed: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg soakConfig) error {
	enc, err := gopus.NewEncoder(gopus.DefaultEncoderConfig(48000, 1, gopus.ApplicationAudio))
	if err != nil {
		return fmt.Errorf("create encoder: %w", err)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		return fmt.Errorf("create decoder: %w", err)
	}

	encodeAllocs, decodeAllocs, err := measureHotPathAllocs()
	if err != nil {
		return err
	}

	startRSS, rssOK := sampleRSSBytes()
	baseGoroutines := runtime.NumGoroutine()

	stats := soakStats{
		peakRSSBytes:  startRSS,
		peakGoroutine: baseGoroutines,
	}

	fmt.Printf("safety soak start: duration=%s report_interval=%s seed=%d hotpath_allocs=(encode=%.2f decode=%.2f)\n",
		cfg.duration, cfg.reportInterval, cfg.seed, encodeAllocs, decodeAllocs)

	rng := rand.New(rand.NewSource(cfg.seed))
	packetBuf := make([]byte, 4000)
	pcmOut := make([]float32, 5760)
	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	backlog := make([][]byte, 0, 8)
	var lastGood []byte

	deadline := time.Now().Add(cfg.duration)
	nextReport := time.Now().Add(cfg.reportInterval)

	for time.Now().Before(deadline) {
		stats.iterations++

		frameSize := frameSizes[rng.Intn(len(frameSizes))]
		if err := enc.SetFrameSize(frameSize); err != nil {
			return fmt.Errorf("set frame size %d: %w", frameSize, err)
		}

		pcm := generateSignal(rng, frameSize)
		n, err := enc.Encode(pcm, packetBuf)
		if err != nil {
			stats.encodeErrors++
			continue
		}
		stats.encodes++

		packet := append([]byte(nil), packetBuf[:n]...)
		if len(packet) > 0 {
			lastGood = append([]byte(nil), packet...)
			backlog = append(backlog, append([]byte(nil), packet...))
			if len(backlog) > 8 {
				backlog = backlog[1:]
			}
		}

		input, inputKind := chooseDecodeInput(rng, packet, lastGood, backlog, &stats)
		if err := decodeOnce(dec, input, inputKind, pcmOut, &stats); err != nil {
			return err
		}

		if time.Now().After(nextReport) {
			updateProcessPeaks(&stats)
			fmt.Printf("status: iterations=%d decodes=%d decode_errors=%d panics=%d rss_peak_mib=%d goroutines_peak=%d\n",
				stats.iterations, stats.decodes, stats.decodeErrors, stats.panics, stats.peakRSSBytes>>20, stats.peakGoroutine)
			nextReport = time.Now().Add(cfg.reportInterval)
		}
	}

	runtime.GC()
	updateProcessPeaks(&stats)
	endRSS, endRSSOK := sampleRSSBytes()
	if endRSSOK && endRSS > stats.peakRSSBytes {
		stats.peakRSSBytes = endRSS
	}
	endGoroutines := runtime.NumGoroutine()
	if endGoroutines > stats.peakGoroutine {
		stats.peakGoroutine = endGoroutines
	}

	fmt.Printf("safety soak done: iterations=%d encodes=%d decodes=%d encode_errors=%d decode_errors=%d panics=%d rss_peak_mib=%d goroutines_peak=%d truncated=%d corrupted=%d duplicated=%d reordered=%d plc=%d random=%d\n",
		stats.iterations, stats.encodes, stats.decodes, stats.encodeErrors, stats.decodeErrors, stats.panics,
		stats.peakRSSBytes>>20, stats.peakGoroutine, stats.truncated, stats.corrupted, stats.duplicated,
		stats.reordered, stats.plc, stats.randomInputs)

	if stats.panics > 0 {
		return errors.New("panic observed during soak")
	}
	if encodeAllocs > cfg.maxHotPathAllocs {
		return fmt.Errorf("encode allocs/op %.2f exceed threshold %.2f", encodeAllocs, cfg.maxHotPathAllocs)
	}
	if decodeAllocs > cfg.maxHotPathAllocs {
		return fmt.Errorf("decode allocs/op %.2f exceed threshold %.2f", decodeAllocs, cfg.maxHotPathAllocs)
	}
	if stats.peakGoroutine-baseGoroutines > cfg.maxGoroutineGrowth {
		return fmt.Errorf("goroutine growth %d exceeds threshold %d", stats.peakGoroutine-baseGoroutines, cfg.maxGoroutineGrowth)
	}
	if rssOK && endRSSOK {
		rssGrowthMiB := int64(endRSS-startRSS) >> 20
		if rssGrowthMiB > cfg.maxRSSGrowthMiB {
			return fmt.Errorf("rss growth %d MiB exceeds threshold %d MiB", rssGrowthMiB, cfg.maxRSSGrowthMiB)
		}
	}

	return nil
}

func measureHotPathAllocs() (float64, float64, error) {
	enc, err := gopus.NewEncoder(gopus.DefaultEncoderConfig(48000, 1, gopus.ApplicationAudio))
	if err != nil {
		return 0, 0, fmt.Errorf("create encoder for alloc check: %w", err)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		return 0, 0, fmt.Errorf("create decoder for alloc check: %w", err)
	}

	pcm := generateDeterministicSignal(960)
	packet := make([]byte, 4000)
	pcmOut := make([]float32, 5760)

	encodeAllocs := testing.AllocsPerRun(100, func() {
		if _, err := enc.Encode(pcm, packet); err != nil {
			panic(err)
		}
	})
	n, err := enc.Encode(pcm, packet)
	if err != nil {
		return 0, 0, fmt.Errorf("prime encode for alloc check: %w", err)
	}
	validPacket := append([]byte(nil), packet[:n]...)

	decodeAllocs := testing.AllocsPerRun(100, func() {
		if _, err := dec.Decode(validPacket, pcmOut); err != nil {
			panic(err)
		}
	})

	return encodeAllocs, decodeAllocs, nil
}

func generateDeterministicSignal(frameSize int) []float32 {
	pcm := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = float32(0.35 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	return pcm
}

func generateSignal(rng *rand.Rand, frameSize int) []float32 {
	pcm := make([]float32, frameSize)
	freq := 80.0 + rng.Float64()*1600.0
	phase := rng.Float64() * 2 * math.Pi
	for i := range pcm {
		t := float64(i) / 48000.0
		tone := 0.35 * math.Sin(phase+2*math.Pi*freq*t)
		noise := (rng.Float64()*2 - 1) * 0.03
		pcm[i] = float32(tone + noise)
	}
	return pcm
}

func chooseDecodeInput(rng *rand.Rand, packet, lastGood []byte, backlog [][]byte, stats *soakStats) ([]byte, string) {
	switch roll := rng.Intn(100); {
	case roll < 55:
		return packet, "current"
	case roll < 65:
		if len(packet) > 1 {
			stats.truncated++
			return append([]byte(nil), packet[:rng.Intn(len(packet)-1)+1]...), "truncated"
		}
		stats.plc++
		return nil, "plc"
	case roll < 75:
		if len(packet) == 0 {
			stats.plc++
			return nil, "plc"
		}
		stats.corrupted++
		corrupted := append([]byte(nil), packet...)
		flips := 1 + rng.Intn(minInt(3, len(corrupted)))
		for i := 0; i < flips; i++ {
			idx := rng.Intn(len(corrupted))
			corrupted[idx] ^= byte(1 << uint(rng.Intn(8)))
		}
		return corrupted, "corrupted"
	case roll < 82:
		if len(lastGood) > 0 {
			stats.duplicated++
			return append([]byte(nil), lastGood...), "duplicated"
		}
	case roll < 89:
		if len(backlog) > 1 {
			stats.reordered++
			return append([]byte(nil), backlog[rng.Intn(len(backlog)-1)]...), "reordered"
		}
	case roll < 95:
		stats.plc++
		return nil, "plc"
	default:
		stats.randomInputs++
		random := make([]byte, rng.Intn(96))
		for i := range random {
			random[i] = byte(rng.Intn(256))
		}
		return random, "random"
	}
	return packet, "current"
}

func decodeOnce(dec *gopus.Decoder, input []byte, inputKind string, pcmOut []float32, stats *soakStats) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stats.panics++
			err = fmt.Errorf(
				"decode panic (%s, len=%d, last_duration=%d, bandwidth=%v, prev_mode=%v, prev_stereo=%t): %v\n%s",
				inputKind,
				len(input),
				dec.LastPacketDuration(),
				dec.Bandwidth(),
				dec.DebugPrevMode(),
				dec.DebugPrevPacketStereo(),
				r,
				debug.Stack(),
			)
		}
	}()

	n, err := dec.Decode(input, pcmOut)
	if err != nil {
		stats.decodeErrors++
		return nil
	}
	if n < 0 || n > 5760 {
		return fmt.Errorf("decoded samples=%d outside [0,5760]", n)
	}
	for i, sample := range pcmOut[:n] {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			return fmt.Errorf("decoded sample[%d] is not finite: %v", i, sample)
		}
	}
	stats.decodes++
	return nil
}

func updateProcessPeaks(stats *soakStats) {
	if rss, ok := sampleRSSBytes(); ok && rss > stats.peakRSSBytes {
		stats.peakRSSBytes = rss
	}
	if goroutines := runtime.NumGoroutine(); goroutines > stats.peakGoroutine {
		stats.peakGoroutine = goroutines
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
