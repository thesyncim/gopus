//go:build gopus_libopus_bench

// benchmark_libopus_scoreboard_interleaved_test.go is the low-noise form of the
// gopus-vs-libopus scoreboard. The one-shot scoreboard times gopus and libopus
// in separate windows, so clock, thermal and co-tenant drift between the two
// windows lands in the ratio. This harness removes that drift:
//
//   - The libopus helper runs in serve mode (tools/csrc/libopus_codec_bench.c
//     --serve): one long-lived process per config holds a primed codec and runs
//     passes on command, so gopus and libopus passes alternate in lockstep.
//   - Each side runs the identical workload per pass: reset (untimed), then the
//     full one-second frame batch through one stream (timed). The same PCM
//     feeds both encoders; libopus decodes the gopus-produced packets that
//     gopus decodes.
//   - Every turn starts with one untimed warm pass so each side is timed with
//     warm caches, and the turn order alternates between rounds.
//   - Each side runs several independent instances (gopus codecs, libopus
//     processes). Where a codec's buffers land in memory moves its speed by up
//     to ~15% on some configs, so one instance per side is not a stable
//     estimate.
//   - Per instance the estimate is the minimum per-pass time over all rounds
//     (min-of-N), which rejects interrupts and preemption; per side it is the
//     median over its instances. The median over rounds of the per-round ratio
//     is reported alongside it.
//   - The A/A spread (slowest/fastest instance mins of the same side) shows the
//     noise floor of the run.
//
// Pin the process to one CPU so both sides share a core and its caches:
//
//	GOPUS_SCOREBOARD_INTERLEAVED=1 GOMAXPROCS=1 taskset -c 2 \
//	  go test -tags gopus_libopus_bench -run TestScoreboardInterleaved -v -count=1 .
//
// Environment:
//
//	GOPUS_SCOREBOARD_INTERLEAVED=1   enable the test
//	GOPUS_SCOREBOARD_FILTER=<regexp> config-name filter (default: all)
//	GOPUS_SCOREBOARD_MODES=encode,decode
//	GOPUS_SCOREBOARD_ROUNDS=N        alternating rounds per config (default 15)
//	GOPUS_SCOREBOARD_PASSES=N        timed passes per turn (default 3)
//	GOPUS_SCOREBOARD_INSTANCES=N     instances per side (default 3)

package gopus_test

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
)

// scoreboardServer is one serve-mode libopus helper process.
type scoreboardServer struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func startScoreboardServer(helper string, args []string) (*scoreboardServer, error) {
	cmd := exec.Command(helper, append(args, "--serve")...)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &scoreboardServer{cmd: cmd, in: in, out: bufio.NewReader(out)}
	line, err := s.out.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		s.close()
		return nil, fmt.Errorf("libopus serve helper did not start: %q %v", line, err)
	}
	return s, nil
}

// passes runs one warm pass and n timed passes and returns ns per packet for
// each timed pass.
func (s *scoreboardServer) passes(n int) ([]float64, error) {
	if _, err := fmt.Fprintf(s.in, "pass %d\n", n); err != nil {
		return nil, err
	}
	line, err := s.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(line)
	if len(fields) != n+1 || fields[0] != "ns" {
		return nil, fmt.Errorf("libopus serve helper reply %q", line)
	}
	out := make([]float64, n)
	for i, f := range fields[1:] {
		if out[i], err = strconv.ParseFloat(f, 64); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *scoreboardServer) close() {
	_, _ = fmt.Fprintln(s.in, "quit")
	_ = s.in.Close()
	_ = s.cmd.Wait()
}

// scoreboardGoRunner drives one gopus codec over a config's frame batch.
type scoreboardGoRunner struct {
	enc     *gopus.Encoder
	dec     *gopus.Decoder
	pcm     []float32
	packets [][]byte
	out     []byte
	pcmOut  []float32
	spf     int
	frames  int
}

func newScoreboardGoRunner(c scoreboardConfig, pcm []float32, packets [][]byte, decode bool) (*scoreboardGoRunner, error) {
	r := &scoreboardGoRunner{pcm: pcm, packets: packets, spf: c.nativeFrame() * c.Channels, frames: c.frameCount()}
	var err error
	if decode {
		r.dec, err = gopus.NewDecoder(gopus.DefaultDecoderConfig(c.Rate, c.Channels))
		r.pcmOut = make([]float32, r.spf)
		r.frames = len(packets)
	} else {
		r.enc, err = newGopusEncoder(c)
		r.out = make([]byte, scoreboardMaxPacketBytes)
	}
	return r, err
}

func (r *scoreboardGoRunner) reset() {
	if r.dec != nil {
		r.dec.Reset()
		return
	}
	r.enc.Reset()
}

// pass runs the whole batch from the current codec state.
func (r *scoreboardGoRunner) pass() error {
	if r.dec != nil {
		for _, p := range r.packets {
			if _, err := r.dec.Decode(p, r.pcmOut); err != nil {
				return err
			}
		}
		return nil
	}
	for f := 0; f < r.frames; f++ {
		if _, err := r.enc.Encode(r.pcm[f*r.spf:(f+1)*r.spf], r.out); err != nil {
			return err
		}
	}
	return nil
}

// passes mirrors the libopus serve command: one warm pass, then n timed passes
// with the reset outside the timed region.
func (r *scoreboardGoRunner) passes(n int) ([]float64, error) {
	r.reset()
	if err := r.pass(); err != nil {
		return nil, err
	}
	out := make([]float64, n)
	for i := range out {
		r.reset()
		st := time.Now()
		err := r.pass()
		out[i] = float64(time.Since(st).Nanoseconds()) / float64(r.frames)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scoreboardSide is one timed participant of the interleaved rotation.
type scoreboardSide struct {
	name   string
	passes func(n int) ([]float64, error)
	mins   []float64 // per-round minimum ns/packet
}

func (s *scoreboardSide) best() float64 { return slicesMin(s.mins) }

func slicesMin(v []float64) float64 {
	m := math.Inf(1)
	for _, x := range v {
		m = min(m, x)
	}
	return m
}

func sortedMedian(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func scoreboardEnvInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

// scoreboardInterleavedResult is one config's interleaved measurement.
type scoreboardInterleavedResult struct {
	name       string
	goNs, cNs  float64
	ratio      float64 // gopus / libopus of the per-side estimates
	roundRatio float64 // median over rounds of the per-round min ratio
	aaGo, aaC  float64 // A/A spread: slowest/fastest instance min per side
}

func runScoreboardInterleaved(t *testing.T, helper string, c scoreboardConfig, decode bool, rounds, passes, instances int) scoreboardInterleavedResult {
	t.Helper()
	nFrames := c.frameCount()
	pcm, err := genGopusPCM(c, nFrames)
	if err != nil {
		t.Fatalf("gen gopus pcm (%s): %v", c.name(), err)
	}
	var packets [][]byte
	in := filepath.Join(t.TempDir(), "in")
	var args []string
	if decode {
		var bit []byte
		packets, bit = encodeGopusBatch(t, c, pcm, nFrames)
		if err := os.WriteFile(in, bit, 0o644); err != nil {
			t.Fatal(err)
		}
		args = []string{"--mode", "decode", "--rate", strconv.Itoa(c.Rate), "--channels", strconv.Itoa(c.Channels), "--application", "audio"}
	} else {
		b, err := genLibopusPCMBytes(c, nFrames)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(in, b, 0o644); err != nil {
			t.Fatal(err)
		}
		args = []string{"--mode", "encode", "--rate", strconv.Itoa(c.Rate), "--channels", strconv.Itoa(c.Channels),
			"--frame-size", strconv.Itoa(c.nativeFrame()), "--bitrate", strconv.Itoa(c.Bitrate),
			"--application", c.LibopusApp, "--bandwidth", c.LibopusBW, "--force-mode", c.ForceMode,
			"--signal", c.LibopusSig, "--complexity", "10", "--vbr", "0"}
	}
	args = append(args, "--min-ns", "1", "--count", "1", "--in", in)

	var goSides, cSides []*scoreboardSide
	for range instances {
		r, err := newScoreboardGoRunner(c, pcm, packets, decode)
		if err != nil {
			t.Fatalf("gopus codec (%s): %v", c.name(), err)
		}
		goSides = append(goSides, &scoreboardSide{name: "gopus", passes: r.passes})
		srv, err := startScoreboardServer(helper, args)
		if err != nil {
			t.Fatalf("libopus serve helper (%s): %v", c.name(), err)
		}
		t.Cleanup(srv.close)
		cSides = append(cSides, &scoreboardSide{name: "libopus", passes: srv.passes})
	}
	var order []*scoreboardSide
	for k := range instances {
		order = append(order, goSides[k], cSides[k])
	}

	runtime.GC()
	var roundRatios []float64
	for round := 0; round < rounds; round++ {
		for i := range order {
			// Rotate the starting side each round so no side always runs first.
			s := order[(i+round)%len(order)]
			v, err := s.passes(passes)
			if err != nil {
				t.Fatalf("%s pass (%s): %v", s.name, c.name(), err)
			}
			s.mins = append(s.mins, slicesMin(v))
		}
		roundRatios = append(roundRatios, sideRoundMedian(goSides, round)/sideRoundMedian(cSides, round))
	}
	goBest, cBest := sideBests(goSides), sideBests(cSides)
	res := scoreboardInterleavedResult{
		name:       c.name(),
		goNs:       sortedMedian(goBest),
		cNs:        sortedMedian(cBest),
		roundRatio: sortedMedian(roundRatios),
		aaGo:       slicesMax(goBest) / slicesMin(goBest),
		aaC:        slicesMax(cBest) / slicesMin(cBest),
	}
	res.ratio = res.goNs / res.cNs
	return res
}

// sideBests returns each instance's min-of-N.
func sideBests(sides []*scoreboardSide) []float64 {
	out := make([]float64, len(sides))
	for i, s := range sides {
		out[i] = s.best()
	}
	return out
}

// sideRoundMedian returns the median over instances of one round's minimum.
func sideRoundMedian(sides []*scoreboardSide, round int) float64 {
	v := make([]float64, len(sides))
	for i, s := range sides {
		v[i] = s.mins[round]
	}
	return sortedMedian(v)
}

// TestScoreboardInterleaved prints the low-noise gopus/libopus ratio for every
// scoreboard config (see the file comment for the method and the knobs).
func TestScoreboardInterleaved(t *testing.T) {
	if os.Getenv("GOPUS_SCOREBOARD_INTERLEAVED") == "" {
		t.Skip("set GOPUS_SCOREBOARD_INTERLEAVED=1 to run the interleaved scoreboard")
	}
	if !validateScoreboardConfiguration(t) {
		return
	}
	if !prepareScoreboardReference(t) {
		return
	}
	helper, err := scoreboardHelper.helper()
	if err != nil {
		t.Fatal(err)
	}
	tier, err := resolveScoreboardTier()
	if err != nil {
		t.Fatal(err)
	}
	filter, err := regexp.Compile(os.Getenv("GOPUS_SCOREBOARD_FILTER"))
	if err != nil {
		t.Fatalf("GOPUS_SCOREBOARD_FILTER: %v", err)
	}
	modes := strings.Split(os.Getenv("GOPUS_SCOREBOARD_MODES"), ",")
	if os.Getenv("GOPUS_SCOREBOARD_MODES") == "" {
		modes = []string{"encode", "decode"}
	}
	rounds := scoreboardEnvInt("GOPUS_SCOREBOARD_ROUNDS", 15)
	passes := scoreboardEnvInt("GOPUS_SCOREBOARD_PASSES", 3)
	instances := scoreboardEnvInt("GOPUS_SCOREBOARD_INSTANCES", 3)

	fmt.Printf("PERF TIER: %s (libopus: %s) GOMAXPROCS=%d rounds=%d passes=%d instances=%d\n",
		tier.name, tier.refDesc, runtime.GOMAXPROCS(0), rounds, passes, instances)
	for _, mode := range modes {
		var decode bool
		switch mode {
		case "encode":
		case "decode":
			decode = true
		default:
			t.Fatalf("GOPUS_SCOREBOARD_MODES entry %q (want encode or decode)", mode)
		}
		var results []scoreboardInterleavedResult
		for _, c := range scoreboardConfigs() {
			if !filter.MatchString(c.name()) {
				continue
			}
			r := runScoreboardInterleaved(t, helper, c, decode, rounds, passes, instances)
			results = append(results, r)
			fmt.Printf("SB %s %-24s go %10.0f c %10.0f g/l %.3f round-med %.3f aa-go %.3f aa-c %.3f\n",
				mode, r.name, r.goNs, r.cNs, r.ratio, r.roundRatio, r.aaGo, r.aaC)
		}
		printScoreboardInterleavedSummary(mode, results)
	}
}

func printScoreboardInterleavedSummary(mode string, results []scoreboardInterleavedResult) {
	if len(results) == 0 {
		return
	}
	var all, aaDev []float64
	fams := map[string][]float64{}
	for _, r := range results {
		all = append(all, r.ratio)
		fam := r.name[:strings.IndexByte(r.name, '/')]
		fams[fam] = append(fams[fam], r.ratio)
		aaDev = append(aaDev, r.aaGo-1, r.aaC-1)
	}
	logSum := 0.0
	for _, v := range all {
		logSum += math.Log(v)
	}
	fmt.Printf("SUMMARY %s n=%d median %.3f geomean %.3f min %.3f max %.3f\n",
		mode, len(all), sortedMedian(all), math.Exp(logSum/float64(len(all))), slicesMin(all), slicesMax(all))
	for _, fam := range []string{"CELT", "SILK", "Hybrid"} {
		if v := fams[fam]; len(v) > 0 {
			fmt.Printf("SUMMARY %s %-6s n=%d min %.3f median %.3f max %.3f\n", mode, fam, len(v), slicesMin(v), sortedMedian(v), slicesMax(v))
		}
	}
	fmt.Printf("SUMMARY %s A/A instance spread-1 median %.3f max %.3f\n", mode, sortedMedian(aaDev), slicesMax(aaDev))
	worst := append([]scoreboardInterleavedResult(nil), results...)
	sort.Slice(worst, func(i, j int) bool { return worst[i].ratio > worst[j].ratio })
	var parts []string
	for _, r := range worst[:min(10, len(worst))] {
		parts = append(parts, fmt.Sprintf("%s %.3f", r.name, r.ratio))
	}
	fmt.Printf("WORST10 %s %s\n", mode, strings.Join(parts, ", "))
}

func slicesMax(v []float64) float64 {
	m := math.Inf(-1)
	for _, x := range v {
		m = max(m, x)
	}
	return m
}
