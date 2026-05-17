package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/container/ogg"
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
	rootMono      uint64
	rootStereo    uint64
	streaming     uint64
	multistream   uint64
	container     uint64
	streamedBytes uint64
	oggPackets    uint64
	peakRSSBytes  uint64
	peakGoroutine int
}

type rootSoakSurface struct {
	name      string
	channels  int
	enc       *gopus.Encoder
	dec       *gopus.Decoder
	packetBuf []byte
	pcmOut    []float32
	backlog   [][]byte
	lastGood  []byte
}

type multistreamSoakSurface struct {
	enc       *gopus.MultistreamEncoder
	dec       *gopus.MultistreamDecoder
	channels  int
	packetBuf []byte
	pcmOut    []float32
	backlog   [][]byte
	lastGood  []byte
}

type packetCollector struct {
	packets [][]byte
	closed  bool
}

type packetSliceReader struct {
	packets [][]byte
	index   int
	granule uint64
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
	rootMono, err := newRootSoakSurface("root-mono", 1)
	if err != nil {
		return err
	}
	rootStereo, err := newRootSoakSurface("root-stereo", 2)
	if err != nil {
		return err
	}
	multistreamSurface, err := newMultistreamSoakSurface()
	if err != nil {
		return err
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

	deadline := time.Now().Add(cfg.duration)
	nextReport := time.Now().Add(cfg.reportInterval)

	for time.Now().Before(deadline) {
		stats.iterations++

		switch roll := rng.Intn(100); {
		case roll < 30:
			err = rootMono.step(rng, &stats)
		case roll < 55:
			err = rootStereo.step(rng, &stats)
		case roll < 75:
			err = multistreamSurface.step(rng, &stats)
		case roll < 90:
			err = runStreamingSurface(rng, &stats)
		default:
			err = runContainerSurface(rng, &stats)
		}
		if err != nil {
			return err
		}

		if time.Now().After(nextReport) {
			updateProcessPeaks(&stats)
			fmt.Printf("status: iterations=%d decodes=%d decode_errors=%d panics=%d surfaces=(mono=%d stereo=%d stream=%d multistream=%d ogg=%d) rss_peak_mib=%d goroutines_peak=%d\n",
				stats.iterations, stats.decodes, stats.decodeErrors, stats.panics,
				stats.rootMono, stats.rootStereo, stats.streaming, stats.multistream, stats.container,
				stats.peakRSSBytes>>20, stats.peakGoroutine)
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

	fmt.Printf("safety soak done: iterations=%d encodes=%d decodes=%d encode_errors=%d decode_errors=%d panics=%d rss_peak_mib=%d goroutines_peak=%d truncated=%d corrupted=%d duplicated=%d reordered=%d plc=%d random=%d surfaces=(mono=%d stereo=%d stream=%d multistream=%d ogg=%d) streamed_bytes=%d ogg_packets=%d\n",
		stats.iterations, stats.encodes, stats.decodes, stats.encodeErrors, stats.decodeErrors, stats.panics,
		stats.peakRSSBytes>>20, stats.peakGoroutine, stats.truncated, stats.corrupted, stats.duplicated,
		stats.reordered, stats.plc, stats.randomInputs, stats.rootMono, stats.rootStereo, stats.streaming,
		stats.multistream, stats.container, stats.streamedBytes, stats.oggPackets)

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

func newRootSoakSurface(name string, channels int) (*rootSoakSurface, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return nil, fmt.Errorf("create %s encoder: %w", name, err)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		return nil, fmt.Errorf("create %s decoder: %w", name, err)
	}
	return &rootSoakSurface{
		name:      name,
		channels:  channels,
		enc:       enc,
		dec:       dec,
		packetBuf: make([]byte, 4000),
		pcmOut:    make([]float32, 5760*channels),
		backlog:   make([][]byte, 0, 8),
	}, nil
}

func (s *rootSoakSurface) step(rng *rand.Rand, stats *soakStats) error {
	if s.channels == 1 {
		stats.rootMono++
	} else {
		stats.rootStereo++
	}

	frameSizes := []int{120, 240, 480, 960, 1920, 2880}
	frameSize := frameSizes[rng.Intn(len(frameSizes))]
	if err := s.enc.SetFrameSize(frameSize); err != nil {
		return fmt.Errorf("%s set frame size %d: %w", s.name, frameSize, err)
	}

	pcm := generateSignal(rng, frameSize, s.channels)
	n, err := s.enc.Encode(pcm, s.packetBuf)
	if err != nil {
		stats.encodeErrors++
		return nil
	}
	stats.encodes++

	packet := append([]byte(nil), s.packetBuf[:n]...)
	if len(packet) > 0 {
		s.lastGood = append(s.lastGood[:0], packet...)
		s.backlog = append(s.backlog, append([]byte(nil), packet...))
		if len(s.backlog) > 8 {
			s.backlog = s.backlog[1:]
		}
	}

	input, inputKind := chooseDecodeInput(rng, packet, s.lastGood, s.backlog, stats)
	return decodeOnce(s.dec, input, s.name+"/"+inputKind, s.pcmOut, stats)
}

func newMultistreamSoakSurface() (*multistreamSoakSurface, error) {
	const channels = 6
	enc, err := gopus.NewMultistreamEncoderDefault(48000, channels, gopus.ApplicationAudio)
	if err != nil {
		return nil, fmt.Errorf("create multistream encoder: %w", err)
	}
	dec, err := gopus.NewMultistreamDecoderDefault(48000, channels)
	if err != nil {
		return nil, fmt.Errorf("create multistream decoder: %w", err)
	}
	if err := enc.SetBitrate(256000); err != nil {
		return nil, fmt.Errorf("set multistream bitrate: %w", err)
	}
	return &multistreamSoakSurface{
		enc:       enc,
		dec:       dec,
		channels:  channels,
		packetBuf: make([]byte, 4000*4),
		pcmOut:    make([]float32, 5760*channels),
		backlog:   make([][]byte, 0, 8),
	}, nil
}

func (s *multistreamSoakSurface) step(rng *rand.Rand, stats *soakStats) error {
	stats.multistream++

	frameSizes := []int{480, 960, 1920, 2880}
	frameSize := frameSizes[rng.Intn(len(frameSizes))]
	if err := s.enc.SetFrameSize(frameSize); err != nil {
		return fmt.Errorf("multistream set frame size %d: %w", frameSize, err)
	}

	pcm := generateSignal(rng, frameSize, s.channels)
	n, err := s.enc.Encode(pcm, s.packetBuf)
	if err != nil {
		stats.encodeErrors++
		return nil
	}
	stats.encodes++

	packet := append([]byte(nil), s.packetBuf[:n]...)
	if len(packet) > 0 {
		s.lastGood = append(s.lastGood[:0], packet...)
		s.backlog = append(s.backlog, append([]byte(nil), packet...))
		if len(s.backlog) > 8 {
			s.backlog = s.backlog[1:]
		}
	}

	input, inputKind := chooseDecodeInput(rng, packet, s.lastGood, s.backlog, stats)
	return decodeOnceMultistream(s.dec, input, "multistream/"+inputKind, s.pcmOut, stats)
}

func measureHotPathAllocs() (float64, float64, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: 1, Application: gopus.ApplicationAudio})
	if err != nil {
		return 0, 0, fmt.Errorf("create encoder for alloc check: %w", err)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	if err != nil {
		return 0, 0, fmt.Errorf("create decoder for alloc check: %w", err)
	}

	pcm := generateDeterministicSignal(960, 1)
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

func runStreamingSurface(rng *rand.Rand, stats *soakStats) error {
	channels := 1 + rng.Intn(2)
	format := gopus.FormatFloat32LE
	if rng.Intn(2) == 0 {
		format = gopus.FormatInt16LE
	}

	sink := &packetCollector{}
	writer, err := gopus.NewWriter(48000, channels, sink, format, gopus.ApplicationAudio)
	if err != nil {
		return fmt.Errorf("create streaming writer: %w", err)
	}

	frames := 1 + rng.Intn(3)
	pcmBytes := encodePCMBytes(generateSignal(rng, frames*960, channels), format)
	for offset := 0; offset < len(pcmBytes); {
		chunk := 1 + rng.Intn(minInt(713, len(pcmBytes)-offset))
		if _, err := writer.Write(pcmBytes[offset : offset+chunk]); err != nil {
			return fmt.Errorf("streaming write: %w", err)
		}
		offset += chunk
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("streaming close: %w", err)
	}

	stats.streaming++
	stats.encodes += uint64(len(sink.packets))

	reader, err := gopus.NewReader(gopus.DefaultDecoderConfig(48000, channels), &packetSliceReader{packets: sink.packets}, format)
	if err != nil {
		return fmt.Errorf("create streaming reader: %w", err)
	}

	readBuf := make([]byte, 257+rng.Intn(2048))
	var streamed []byte
	for {
		n, err := reader.Read(readBuf)
		if n > 0 {
			stats.streamedBytes += uint64(n)
			if format == gopus.FormatFloat32LE {
				streamed = append(streamed, readBuf[:n]...)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			stats.decodeErrors++
			return fmt.Errorf("streaming read: %w", err)
		}
	}
	if format == gopus.FormatFloat32LE {
		if err := assertFloat32BytesFinite(streamed); err != nil {
			return err
		}
	}
	stats.decodes += uint64(len(sink.packets))
	return nil
}

func runContainerSurface(rng *rand.Rand, stats *soakStats) error {
	channels := 1 + rng.Intn(2)
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: 48000, Channels: channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return fmt.Errorf("create container encoder: %w", err)
	}
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, channels))
	if err != nil {
		return fmt.Errorf("create container decoder: %w", err)
	}

	var buf bytes.Buffer
	writer, err := ogg.NewWriter(&buf, 48000, uint8(channels))
	if err != nil {
		return fmt.Errorf("create ogg writer: %w", err)
	}

	packetBuf := make([]byte, 4000)
	frameSizes := []int{480, 960, 1920}
	packetCount := 1 + rng.Intn(4)
	for i := 0; i < packetCount; i++ {
		frameSize := frameSizes[rng.Intn(len(frameSizes))]
		if err := enc.SetFrameSize(frameSize); err != nil {
			return fmt.Errorf("container set frame size %d: %w", frameSize, err)
		}
		pcm := generateSignal(rng, frameSize, channels)
		n, err := enc.Encode(pcm, packetBuf)
		if err != nil {
			stats.encodeErrors++
			continue
		}
		stats.encodes++
		if n == 0 {
			continue
		}
		if err := writer.WritePacket(packetBuf[:n], frameSize); err != nil {
			return fmt.Errorf("write ogg packet: %w", err)
		}
		stats.oggPackets++
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close ogg writer: %w", err)
	}

	reader, err := ogg.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("create ogg reader: %w", err)
	}
	if reader.Header.Channels != uint8(channels) {
		return fmt.Errorf("ogg header channels=%d want %d", reader.Header.Channels, channels)
	}

	pcmOut := make([]float32, 5760*channels)
	for {
		packet, _, err := reader.ReadPacket()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read ogg packet: %w", err)
		}
		if err := decodeOnce(dec, packet, "ogg/current", pcmOut, stats); err != nil {
			return err
		}
	}
	stats.container++
	return nil
}

func generateDeterministicSignal(frameSize, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			freq := 440.0 + float64(ch)*110.0
			pcm[i*channels+ch] = float32(0.35 * math.Sin(2*math.Pi*freq*float64(i)/48000))
		}
	}
	return pcm
}

func generateSignal(rng *rand.Rand, frameSize, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	freq := 80.0 + rng.Float64()*1600.0
	phase := rng.Float64() * 2 * math.Pi
	for i := 0; i < frameSize; i++ {
		t := float64(i) / 48000.0
		for ch := 0; ch < channels; ch++ {
			channelPhase := phase + float64(ch)*0.41
			channelFreq := freq + float64(ch)*97.0
			tone := 0.35 * math.Sin(channelPhase+2*math.Pi*channelFreq*t)
			noise := (rng.Float64()*2 - 1) * 0.03
			pcm[i*channels+ch] = float32(tone + noise)
		}
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

func encodePCMBytes(pcm []float32, format gopus.SampleFormat) []byte {
	switch format {
	case gopus.FormatFloat32LE:
		out := make([]byte, len(pcm)*4)
		for i, sample := range pcm {
			binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(sample))
		}
		return out
	case gopus.FormatInt16LE:
		out := make([]byte, len(pcm)*2)
		for i, sample := range pcm {
			if sample > 1 {
				sample = 1
			} else if sample < -1 {
				sample = -1
			}
			binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(sample*32767)))
		}
		return out
	default:
		return nil
	}
}

func assertFloat32BytesFinite(data []byte) error {
	fullSamples := len(data) / 4
	for i := 0; i < fullSamples; i++ {
		sample := math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			return fmt.Errorf("streamed sample[%d] is not finite: %v", i, sample)
		}
	}
	return nil
}

func (s *packetCollector) WritePacket(packet []byte) (int, error) {
	s.packets = append(s.packets, append([]byte(nil), packet...))
	return len(packet), nil
}

func (s *packetCollector) Close() error {
	s.closed = true
	return nil
}

func (r *packetSliceReader) ReadPacketInto(dst []byte) (int, uint64, error) {
	if r.index >= len(r.packets) {
		return 0, 0, io.EOF
	}
	packet := r.packets[r.index]
	r.index++
	if len(packet) > len(dst) {
		return 0, 0, gopus.ErrBufferTooSmall
	}
	r.granule += 960
	return copy(dst, packet), r.granule, nil
}

func decodeOnce(dec *gopus.Decoder, input []byte, inputKind string, pcmOut []float32, stats *soakStats) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stats.panics++
			err = fmt.Errorf(
				"decode panic (%s, len=%d, last_duration=%d, bandwidth=%v): %v\n%s",
				inputKind,
				len(input),
				dec.LastPacketDuration(),
				dec.Bandwidth(),
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
	total := n * dec.Channels()
	if total > len(pcmOut) {
		return fmt.Errorf("decoded sample count=%d exceeds output buffer %d", total, len(pcmOut))
	}
	for i, sample := range pcmOut[:total] {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			return fmt.Errorf("decoded sample[%d] is not finite: %v", i, sample)
		}
	}
	stats.decodes++
	return nil
}

func decodeOnceMultistream(dec *gopus.MultistreamDecoder, input []byte, inputKind string, pcmOut []float32, stats *soakStats) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stats.panics++
			err = fmt.Errorf(
				"multistream decode panic (%s, len=%d): %v\n%s",
				inputKind,
				len(input),
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
		return fmt.Errorf("multistream decoded samples=%d outside [0,5760]", n)
	}
	total := n * dec.Channels()
	if total > len(pcmOut) {
		return fmt.Errorf("multistream decoded sample count=%d exceeds output buffer %d", total, len(pcmOut))
	}
	for i, sample := range pcmOut[:total] {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			return fmt.Errorf("multistream decoded sample[%d] is not finite: %v", i, sample)
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
