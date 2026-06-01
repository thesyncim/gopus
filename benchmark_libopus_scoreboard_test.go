//go:build gopus_libopus_bench

// benchmark_libopus_scoreboard_test.go is the gopus-vs-libopus PERFORMANCE
// SCOREBOARD: matched-config Go benchmarks that time gopus Encode/Decode against
// the pinned libopus 1.6.1 reference across the codec config space
// (CELT/SILK/Hybrid x mono/stereo x 8/16/24/48 kHz x 2.5/10/20/60 ms x a
// representative bitrate). Each sub-benchmark reports gopus ns/op AND the libopus
// ns/packet for the same workload, plus their ratio, so "beating libopus" is
// directly measurable.
//
// Build tag: this file (and its only cgo-adjacent dependency, the libopus C
// bench helper built by internal/libopustest) lives behind gopus_libopus_bench
// so the DEFAULT build never compiles it and never pulls in the reference
// toolchain. Run with:
//
//	go test -tags gopus_libopus_bench -run XXX -bench . -benchtime 50x .
//
// Methodology
//   - gopus side: a single Encoder/Decoder is driven over a fixed frame batch;
//     testing.B times the native Go calls (one packet per op).
//   - libopus side: tools/csrc/libopus_codec_bench.c is built once against the
//     pinned static libopus and SELF-TIMES the same batch inside one process, so
//     its ns/packet excludes process spawn, file I/O, and codec construction. The
//     Go benchmark runs that helper once per config and surfaces its ns/packet via
//     ReportMetric ("libopus_ns/pkt") alongside the ratio ("g/l", gopus/libopus;
//     <1.00 means gopus is faster). The helper result is computed once and reused
//     across b.N iterations (it does not depend on b.N).
//   - The per-config Benchmarks time gopus at the single-packet steady state
//     (Reset only at batch wrap), so their "g/l" is an APPROXIMATE per-op ratio
//     (gopus skips most stream resets that the libopus self-timed pass includes).
//     For the authoritative apples-to-apples ratio use TestScoreboardSummary,
//     which times a full reset+batch pass on BOTH sides; PERF_BASELINE.md is built
//     from that test.
//   - Matched work: both encode the same number of frames of the same duration.
//     gopus frame sizes are 48 kHz-relative (F in {120,480,960,2880} = 2.5/10/20/
//     60 ms) and gopus consumes F samples of 48 kHz-rate PCM per frame; libopus
//     consumes the rate-R equivalent (F*R/48000 samples) per frame. The audio is
//     the same signal class at the same duration (not bit-identical).

package gopus_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	scoreboardMaxPacketBytes = 4000
	scoreboardBatchSeconds   = 1.0 // audio seconds per measured batch (frame count = batchSeconds / frameDuration)
	scoreboardLibopusMinNS   = 100_000_000
	scoreboardLibopusCount   = 5
)

// scoreboardConfig is one matched gopus/libopus workload.
type scoreboardConfig struct {
	Mode       gopus.EncoderMode
	ModeName   string // "CELT" | "SILK" | "Hybrid"
	ForceMode  string // libopus --force-mode token
	Rate       int    // API sample rate
	Channels   int
	Frame48    int     // gopus frame size in 48 kHz-relative samples (120/480/960/2880)
	DurMS      float64 // frame duration in ms
	Bitrate    int
	Bandwidth  gopus.Bandwidth
	LibopusBW  string // libopus --bandwidth token
	Signal     gopus.Signal
	LibopusSig string // libopus --signal token
	LibopusApp string // libopus --application token
	SignalName string // testsignal corpus class
}

// libFrame returns the libopus per-frame sample count at the API rate for this
// config's duration (gopus Frame48 is 48 kHz-relative).
func (c scoreboardConfig) libFrame() int { return c.Frame48 * c.Rate / 48000 }

// frameCount returns the number of frames in one measured batch.
func (c scoreboardConfig) frameCount() int {
	n := int(scoreboardBatchSeconds*1000.0/c.DurMS + 0.5)
	if n < 1 {
		n = 1
	}
	return n
}

// name is the b.Run sub-benchmark label.
func (c scoreboardConfig) name() string {
	ch := "mono"
	if c.Channels == 2 {
		ch = "stereo"
	}
	return fmt.Sprintf("%s/%s/%dk/%s", c.ModeName, ch, c.Rate/1000, durLabel(c.DurMS))
}

func durLabel(ms float64) string {
	if ms == math.Trunc(ms) {
		return fmt.Sprintf("%dms", int(ms))
	}
	return strings.ReplaceAll(fmt.Sprintf("%.1fms", ms), ".", "_")
}

// scoreboardConfigs builds the matched config matrix. Combinations are gated to
// what both encoders accept (verified against gopus + libopus 1.6.1):
//
//	CELT  : 8/16/24/48 kHz x 2.5/10/20 ms
//	SILK  : 8/16/24/48 kHz x 10/20/60 ms
//	Hybrid: 24/48 kHz     x 10/20 ms
//
// each x mono/stereo at one representative bitrate per mode.
func scoreboardConfigs() []scoreboardConfig {
	type dur struct {
		f48 int
		ms  float64
	}
	celtDurs := []dur{{120, 2.5}, {480, 10}, {960, 20}}
	silkDurs := []dur{{480, 10}, {960, 20}, {2880, 60}}
	hybridDurs := []dur{{480, 10}, {960, 20}}
	allRates := []int{8000, 16000, 24000, 48000}
	hybridRates := []int{24000, 48000}
	channels := []int{1, 2}

	// libopus bandwidth token per API rate (the widest the rate supports).
	bwToken := func(rate int) (gopus.Bandwidth, string) {
		switch rate {
		case 8000:
			return gopus.BandwidthNarrowband, "nb"
		case 16000:
			return gopus.BandwidthWideband, "wb"
		case 24000:
			return gopus.BandwidthSuperwideband, "swb"
		default:
			return gopus.BandwidthFullband, "fb"
		}
	}

	var cfgs []scoreboardConfig
	add := func(modeName string, mode gopus.EncoderMode, force, app string, sig gopus.Signal, sigTok, signalClass string, rates []int, durs []dur, bitrate func(rate, ch int) int) {
		for _, ch := range channels {
			for _, rate := range rates {
				bw, bwTok := bwToken(rate)
				// Hybrid is always at least SWB; never NB/WB.
				for _, d := range durs {
					cfgs = append(cfgs, scoreboardConfig{
						Mode: mode, ModeName: modeName, ForceMode: force,
						Rate: rate, Channels: ch, Frame48: d.f48, DurMS: d.ms,
						Bitrate: bitrate(rate, ch), Bandwidth: bw, LibopusBW: bwTok,
						Signal: sig, LibopusSig: sigTok, LibopusApp: app, SignalName: signalClass,
					})
				}
			}
		}
	}

	celtBR := func(rate, ch int) int { return 64000 * ch }
	silkBR := func(rate, ch int) int {
		base := 24000
		if rate >= 24000 {
			base = 32000
		}
		return base * ch
	}
	hybridBR := func(rate, ch int) int { return 48000 * ch }

	add("CELT", gopus.EncoderModeCELT, "celt", "audio", gopus.SignalMusic, "music", testsignal.CorpusMusicV1, allRates, celtDurs, celtBR)
	add("SILK", gopus.EncoderModeSILK, "silk", "voip", gopus.SignalVoice, "voice", testsignal.CorpusCleanSpeechV1, allRates, silkDurs, silkBR)
	add("Hybrid", gopus.EncoderModeHybrid, "hybrid", "audio", gopus.SignalAuto, "auto", testsignal.CorpusMixedV1, hybridRates, hybridDurs, hybridBR)

	return cfgs
}

// scoreboardLibopusBench wraps the self-timing libopus encode/decode helper.
type scoreboardLibopusBench struct {
	once sync.Once
	path string
	err  error
}

func (s *scoreboardLibopusBench) helper() (string, error) {
	s.once.Do(func() {
		// By default the bench links the pinned libopus.a, which is the
		// SIMD-DISABLED parity reference — comparing gopus (with arm64 NEON / amd64
		// asm) against it OVERSTATES gopus (see PERF_BASELINE.md fairness caveat).
		// For a FAIR comparison set GOPUS_BENCH_LIBOPUS_A to a separately-built
		// SIMD (NEON/SSE) libopus.a; the parity lib must stay scalar for bit-exact
		// determinism, so it cannot double as the perf reference.
		libPath := libopustest.RefPath(".libs", "libopus.a")
		outBase := "gopus_libopus_codec_bench"
		if p := os.Getenv("GOPUS_BENCH_LIBOPUS_A"); p != "" {
			libPath = p
			outBase = "gopus_libopus_codec_bench_simd"
		}
		s.path, s.err = libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label:       "libopus codec scoreboard bench",
			OutputBase:  outBase,
			SourceFile:  "libopus_codec_bench.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt", "silk", "src"},
			Libs:        []string{libPath, "-lm"},
			DeadStrip:   true,
		})
	})
	return s.path, s.err
}

var scoreboardHelper scoreboardLibopusBench

// libopusRow is the parsed single TSV data row emitted by libopus_codec_bench.c.
type libopusRow struct {
	NsPerPacket float64
	NsPerSample float64
	XRealtime   float64
}

func parseLibopusBenchRow(out []byte) (libopusRow, error) {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return libopusRow{}, fmt.Errorf("libopus bench helper produced no data row: %q", string(out))
	}
	fields := strings.Split(lines[len(lines)-1], "\t")
	// implementation mode rate channels count iterations elapsed_ns packets_per_op samples_per_op ns_per_packet ns_per_sample x_realtime
	if len(fields) != 12 {
		return libopusRow{}, fmt.Errorf("libopus bench row has %d fields: %q", len(fields), lines[len(lines)-1])
	}
	nsPkt, err := strconv.ParseFloat(fields[9], 64)
	if err != nil {
		return libopusRow{}, fmt.Errorf("parse ns_per_packet: %w", err)
	}
	nsSmp, err := strconv.ParseFloat(fields[10], 64)
	if err != nil {
		return libopusRow{}, fmt.Errorf("parse ns_per_sample: %w", err)
	}
	xrt, err := strconv.ParseFloat(fields[11], 64)
	if err != nil {
		return libopusRow{}, fmt.Errorf("parse x_realtime: %w", err)
	}
	return libopusRow{NsPerPacket: nsPkt, NsPerSample: nsSmp, XRealtime: xrt}, nil
}

// genGopusPCM builds nFrames*Frame48*channels samples of 48 kHz-rate PCM for the
// gopus encoder (gopus consumes 48 kHz-relative frames).
func genGopusPCM(c scoreboardConfig, nFrames int) ([]float32, error) {
	samples := nFrames * c.Frame48 * c.Channels
	return testsignal.GenerateCorpusSignal(c.SignalName, 48000, samples, c.Channels)
}

// genLibopusPCMBytes builds the rate-R interleaved float32 LE PCM the libopus
// encode helper reads (matched duration / frame count).
func genLibopusPCMBytes(c scoreboardConfig, nFrames int) ([]byte, error) {
	lf := c.libFrame()
	samples := nFrames * lf * c.Channels
	pcm, err := testsignal.GenerateCorpusSignal(c.SignalName, c.Rate, samples, c.Channels)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Grow(len(pcm) * 4)
	var w [4]byte
	for _, s := range pcm {
		binary.LittleEndian.PutUint32(w[:], math.Float32bits(s))
		buf.Write(w[:])
	}
	return buf.Bytes(), nil
}

// newGopusEncoder builds a configured gopus encoder for the config.
func newGopusEncoder(c scoreboardConfig) (*gopus.Encoder, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: c.Rate, Channels: c.Channels, Application: gopus.ApplicationAudio})
	if err != nil {
		return nil, err
	}
	if err := enc.SetMode(c.Mode); err != nil {
		return nil, err
	}
	if err := enc.SetFrameSize(c.Frame48); err != nil {
		return nil, err
	}
	if err := enc.SetBitrate(c.Bitrate); err != nil {
		return nil, err
	}
	if err := enc.SetComplexity(10); err != nil {
		return nil, err
	}
	if err := enc.SetBitrateMode(gopus.BitrateModeCBR); err != nil {
		return nil, err
	}
	enc.SetVBR(false)
	if err := enc.SetBandwidth(c.Bandwidth); err != nil {
		return nil, err
	}
	if err := enc.SetSignal(c.Signal); err != nil {
		return nil, err
	}
	return enc, nil
}

// runLibopusEncode times the libopus encode helper for the config and returns its
// self-timed row.
func runLibopusEncode(tb testing.TB, c scoreboardConfig, nFrames int) (libopusRow, bool) {
	tb.Helper()
	helper, err := scoreboardHelper.helper()
	if err != nil {
		libopustest.HelperUnavailable(tb, "codec bench", err)
		return libopusRow{}, false
	}
	pcm, err := genLibopusPCMBytes(c, nFrames)
	if err != nil {
		tb.Fatalf("gen libopus pcm: %v", err)
	}
	in := filepath.Join(tb.TempDir(), "in.f32")
	if err := os.WriteFile(in, pcm, 0o644); err != nil {
		tb.Fatalf("write libopus pcm: %v", err)
	}
	out, err := libopustest.RunHelperArgs(helper, nil,
		"--mode", "encode",
		"--rate", strconv.Itoa(c.Rate),
		"--channels", strconv.Itoa(c.Channels),
		"--frame-size", strconv.Itoa(c.libFrame()),
		"--bitrate", strconv.Itoa(c.Bitrate),
		"--application", c.LibopusApp,
		"--bandwidth", c.LibopusBW,
		"--force-mode", c.ForceMode,
		"--signal", c.LibopusSig,
		"--complexity", "10",
		"--vbr", "0",
		"--min-ns", strconv.Itoa(scoreboardLibopusMinNS),
		"--count", strconv.Itoa(scoreboardLibopusCount),
		"--in", in,
	)
	if err != nil {
		tb.Fatalf("run libopus encode helper (%s): %v", c.name(), err)
	}
	row, err := parseLibopusBenchRow(out)
	if err != nil {
		tb.Fatalf("parse libopus encode row (%s): %v", c.name(), err)
	}
	return row, true
}

// runLibopusDecode times the libopus decode helper over the supplied opus_demo
// .bit packet stream.
func runLibopusDecode(tb testing.TB, c scoreboardConfig, bitStream []byte) (libopusRow, bool) {
	tb.Helper()
	helper, err := scoreboardHelper.helper()
	if err != nil {
		libopustest.HelperUnavailable(tb, "codec bench", err)
		return libopusRow{}, false
	}
	in := filepath.Join(tb.TempDir(), "in.bit")
	if err := os.WriteFile(in, bitStream, 0o644); err != nil {
		tb.Fatalf("write bit stream: %v", err)
	}
	out, err := libopustest.RunHelperArgs(helper, nil,
		"--mode", "decode",
		"--rate", strconv.Itoa(c.Rate),
		"--channels", strconv.Itoa(c.Channels),
		"--application", "audio", // ignored by decode but required by arg validation
		"--min-ns", strconv.Itoa(scoreboardLibopusMinNS),
		"--count", strconv.Itoa(scoreboardLibopusCount),
		"--in", in,
	)
	if err != nil {
		tb.Fatalf("run libopus decode helper (%s): %v", c.name(), err)
	}
	row, err := parseLibopusBenchRow(out)
	if err != nil {
		tb.Fatalf("parse libopus decode row (%s): %v", c.name(), err)
	}
	return row, true
}

// encodeGopusBatch encodes nFrames into a slice of packet copies and an
// opus_demo .bit stream (BE u32 len + BE u32 final range + payload).
func encodeGopusBatch(tb testing.TB, c scoreboardConfig, pcm []float32, nFrames int) ([][]byte, []byte) {
	tb.Helper()
	enc, err := newGopusEncoder(c)
	if err != nil {
		tb.Fatalf("new gopus encoder (%s): %v", c.name(), err)
	}
	samplesPerFrame := c.Frame48 * c.Channels
	packets := make([][]byte, 0, nFrames)
	var bit bytes.Buffer
	scratch := make([]byte, scoreboardMaxPacketBytes)
	var hdr [8]byte
	for f := 0; f < nFrames; f++ {
		n, err := enc.Encode(pcm[f*samplesPerFrame:(f+1)*samplesPerFrame], scratch)
		if err != nil {
			tb.Fatalf("gopus encode frame %d (%s): %v", f, c.name(), err)
		}
		pkt := append([]byte(nil), scratch[:n]...)
		packets = append(packets, pkt)
		binary.BigEndian.PutUint32(hdr[0:4], uint32(n))
		binary.BigEndian.PutUint32(hdr[4:8], enc.FinalRange())
		bit.Write(hdr[:])
		bit.Write(pkt)
	}
	return packets, bit.Bytes()
}

// BenchmarkScoreboardEncode times gopus Encode against libopus opus_encode_float
// for every matched config and records the gopus/libopus ratio.
func BenchmarkScoreboardEncode(b *testing.B) {
	libopustest.RequireOracle(b)
	for _, c := range scoreboardConfigs() {
		c := c
		b.Run(c.name(), func(b *testing.B) {
			nFrames := c.frameCount()
			pcm, err := genGopusPCM(c, nFrames)
			if err != nil {
				b.Fatalf("gen gopus pcm: %v", err)
			}
			enc, err := newGopusEncoder(c)
			if err != nil {
				b.Fatalf("new gopus encoder: %v", err)
			}
			samplesPerFrame := c.Frame48 * c.Channels
			out := make([]byte, scoreboardMaxPacketBytes)

			// libopus reference (self-timed, independent of b.N).
			lib, ok := runLibopusEncode(b, c, nFrames)

			// Warm one full batch so first-frame allocation / mode hysteresis is
			// not charged to the measurement.
			enc.Reset()
			for f := 0; f < nFrames; f++ {
				if _, err := enc.Encode(pcm[f*samplesPerFrame:(f+1)*samplesPerFrame], out); err != nil {
					b.Fatalf("gopus warm encode: %v", err)
				}
			}

			b.ResetTimer()
			frame := 0
			for i := 0; i < b.N; i++ {
				if frame == 0 {
					b.StopTimer()
					enc.Reset()
					b.StartTimer()
				}
				if _, err := enc.Encode(pcm[frame*samplesPerFrame:(frame+1)*samplesPerFrame], out); err != nil {
					b.Fatalf("gopus encode: %v", err)
				}
				frame++
				if frame == nFrames {
					frame = 0
				}
			}
			b.StopTimer()

			if ok {
				gopusNsPkt := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
				b.ReportMetric(lib.NsPerPacket, "libopus_ns/pkt")
				b.ReportMetric(gopusNsPkt/lib.NsPerPacket, "g/l")
			}
		})
	}
}

// BenchmarkScoreboardDecode times gopus Decode against libopus opus_decode_float
// for every matched config (decoding gopus-produced packets) and records the
// gopus/libopus ratio.
func BenchmarkScoreboardDecode(b *testing.B) {
	libopustest.RequireOracle(b)
	for _, c := range scoreboardConfigs() {
		c := c
		b.Run(c.name(), func(b *testing.B) {
			nFrames := c.frameCount()
			pcm, err := genGopusPCM(c, nFrames)
			if err != nil {
				b.Fatalf("gen gopus pcm: %v", err)
			}
			packets, bit := encodeGopusBatch(b, c, pcm, nFrames)
			if len(packets) == 0 {
				b.Fatalf("no packets produced for %s", c.name())
			}

			dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(c.Rate, c.Channels))
			if err != nil {
				b.Fatalf("new gopus decoder: %v", err)
			}
			// Output buffer large enough for the longest frame at this rate.
			maxOut := c.Frame48 * c.Rate / 48000 * c.Channels
			pcmOut := make([]float32, maxOut)

			lib, ok := runLibopusDecode(b, c, bit)

			// Warm one batch.
			dec.Reset()
			for _, pkt := range packets {
				if _, err := dec.Decode(pkt, pcmOut); err != nil {
					b.Fatalf("gopus warm decode: %v", err)
				}
			}

			b.ResetTimer()
			idx := 0
			for i := 0; i < b.N; i++ {
				if idx == 0 {
					b.StopTimer()
					dec.Reset()
					b.StartTimer()
				}
				if _, err := dec.Decode(packets[idx], pcmOut); err != nil {
					b.Fatalf("gopus decode: %v", err)
				}
				idx++
				if idx == len(packets) {
					idx = 0
				}
			}
			b.StopTimer()

			if ok {
				gopusNsPkt := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
				b.ReportMetric(lib.NsPerPacket, "libopus_ns/pkt")
				b.ReportMetric(gopusNsPkt/lib.NsPerPacket, "g/l")
			}
		})
	}
}

// scoreboardAgg accumulates ratios for an aggregate summary line.
type scoreboardAgg struct {
	mu     sync.Mutex
	ratios []float64
}

func (a *scoreboardAgg) add(r float64) {
	a.mu.Lock()
	a.ratios = append(a.ratios, r)
	a.mu.Unlock()
}

func (a *scoreboardAgg) summary() (geomean, median float64, n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n = len(a.ratios)
	if n == 0 {
		return 0, 0, 0
	}
	sorted := append([]float64(nil), a.ratios...)
	sort.Float64s(sorted)
	median = sorted[n/2]
	logSum := 0.0
	for _, r := range a.ratios {
		logSum += math.Log(r)
	}
	geomean = math.Exp(logSum / float64(n))
	return geomean, median, n
}

// TestScoreboardSummary runs the full matched config matrix once (short
// self-timed passes on both sides) and prints a per-config + aggregate
// gopus/libopus ratio table. It is a Test (not a Benchmark) so a single
// invocation emits the whole scoreboard; it is skipped when the libopus oracle
// is unavailable. Run with:
//
//	go test -tags gopus_libopus_bench -run TestScoreboardSummary -v .
func TestScoreboardSummary(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := scoreboardHelper.helper(); err != nil {
		libopustest.HelperUnavailable(t, "codec bench", err)
		return
	}

	type row struct {
		name     string
		encGopus float64
		encLib   float64
		encRatio float64
		decGopus float64
		decLib   float64
		decRatio float64
	}
	var rows []row
	var encAgg, decAgg scoreboardAgg

	for _, c := range scoreboardConfigs() {
		nFrames := c.frameCount()
		pcm, err := genGopusPCM(c, nFrames)
		if err != nil {
			t.Fatalf("gen gopus pcm (%s): %v", c.name(), err)
		}

		// --- encode ---
		encLib, _ := runLibopusEncode(t, c, nFrames)
		enc, err := newGopusEncoder(c)
		if err != nil {
			t.Fatalf("new gopus encoder (%s): %v", c.name(), err)
		}
		samplesPerFrame := c.Frame48 * c.Channels
		out := make([]byte, scoreboardMaxPacketBytes)
		encGopusNs := timeGopus(func() {
			enc.Reset()
			for f := 0; f < nFrames; f++ {
				if _, err := enc.Encode(pcm[f*samplesPerFrame:(f+1)*samplesPerFrame], out); err != nil {
					t.Fatalf("gopus encode (%s): %v", c.name(), err)
				}
			}
		}, nFrames)

		// --- decode ---
		packets, bit := encodeGopusBatch(t, c, pcm, nFrames)
		decLib, _ := runLibopusDecode(t, c, bit)
		dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(c.Rate, c.Channels))
		if err != nil {
			t.Fatalf("new gopus decoder (%s): %v", c.name(), err)
		}
		maxOut := c.Frame48 * c.Rate / 48000 * c.Channels
		pcmOut := make([]float32, maxOut)
		decGopusNs := timeGopus(func() {
			dec.Reset()
			for _, pkt := range packets {
				if _, err := dec.Decode(pkt, pcmOut); err != nil {
					t.Fatalf("gopus decode (%s): %v", c.name(), err)
				}
			}
		}, len(packets))

		encRatio := encGopusNs / encLib.NsPerPacket
		decRatio := decGopusNs / decLib.NsPerPacket
		encAgg.add(encRatio)
		decAgg.add(decRatio)
		rows = append(rows, row{
			name:     c.name(),
			encGopus: encGopusNs, encLib: encLib.NsPerPacket, encRatio: encRatio,
			decGopus: decGopusNs, decLib: decLib.NsPerPacket, decRatio: decRatio,
		})
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%-26s %12s %12s %7s   %12s %12s %7s\n",
		"config", "enc_gopus", "enc_libopus", "g/l", "dec_gopus", "dec_libopus", "g/l")
	fmt.Fprintf(&sb, "%s\n", strings.Repeat("-", 100))
	for _, r := range rows {
		fmt.Fprintf(&sb, "%-26s %10.0fns %10.0fns %6.2fx   %10.0fns %10.0fns %6.2fx\n",
			r.name, r.encGopus, r.encLib, r.encRatio, r.decGopus, r.decLib, r.decRatio)
	}
	eg, em, en := encAgg.summary()
	dg, dm, dn := decAgg.summary()
	fmt.Fprintf(&sb, "%s\n", strings.Repeat("-", 100))
	fmt.Fprintf(&sb, "ENCODE aggregate (n=%d): geomean g/l=%.3fx  median=%.3fx\n", en, eg, em)
	fmt.Fprintf(&sb, "DECODE aggregate (n=%d): geomean g/l=%.3fx  median=%.3fx\n", dn, dg, dm)
	fmt.Fprintf(&sb, "g/l < 1.00x => gopus FASTER than libopus; > 1.00x => slower.\n")
	t.Log(sb.String())
}

// timeGopus runs fn repeatedly for a short window and returns ns per unit (frame
// or packet). It mirrors the self-timed loop the libopus helper uses so both
// sides exclude warmup and report a comparable per-packet cost.
func timeGopus(fn func(), unitsPerPass int) float64 {
	// Warm pass.
	fn()
	const minDur = 150 * time.Millisecond
	start := time.Now()
	passes := 0
	for {
		fn()
		passes++
		if time.Since(start) >= minDur {
			break
		}
	}
	elapsed := time.Since(start)
	return float64(elapsed.Nanoseconds()) / float64(int64(passes)*int64(unitsPerPass))
}
