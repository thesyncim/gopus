package ogg

// Differential Ogg coverage against libopus-authored streams.
//
// The fuzz/differential targets in demux_fuzz_test.go and demux_fuzz_ext_test.go
// drive gopus's demux with streams produced by gopus's own Writer/encoder. That
// proves the gopus write→read round-trip but never exercises libopus's own Ogg
// byte layout: opusenc (opus-tools, libopus 1.6.1 — the pinned reference) packs
// pages, lacing, the OpusHead/OpusTags packets, comment padding and granule
// positions differently from the gopus Writer.
//
// This file closes that gap. It uses opusenc to produce genuine libopus Ogg Opus
// streams and feeds those exact bytes through gopus's demux. Two axes:
//
//   - PARITY: TestOggLibopusStreamParity encodes a matrix of streams with opusenc
//     and asserts gopus demuxes+decodes each to PCM whose per-channel energy
//     matches the opusdec oracle. The same libopus-authored streams also seed the
//     PCM differential fuzzer FuzzOggExt_DifferentialOpusfilePCM.
//   - ROBUSTNESS: FuzzOggLibopusMutationRobustness seeds with libopus streams and
//     their byte mutations; gopus must never panic, hang, over-allocate or emit a
//     packet larger than the input. gopus may reject (it can be stricter than
//     opusfile) but must do so cleanly.
//
// When opusenc/opusdec are unavailable (no opus-tools, or a sandbox that blocks
// exec) the libopus seeds are simply absent and TestOggLibopusStreamParity skips;
// the robustness fuzzer keeps its structural fallback seeds and the gopus-only
// no-panic invariant, so the package still builds and runs anywhere.

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"os/exec"
	"testing"
)

// ---- opusenc oracle helpers ----

// getOpusencPath returns the path to the opusenc binary, mirroring the lookup
// strategy of getOpusdecPath (PATH first, then the common Homebrew/system dirs).
func getOpusencPath() string {
	if path, err := exec.LookPath("opusenc"); err == nil {
		return path
	}
	for _, p := range []string{
		"/opt/homebrew/bin/opusenc",
		"/usr/local/bin/opusenc",
		"/usr/bin/opusenc",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "opusenc"
}

// checkOpusenc reports whether an opusenc binary can be located.
func checkOpusenc() bool {
	if _, err := exec.LookPath("opusenc"); err == nil {
		return true
	}
	for _, p := range []string{
		"/opt/homebrew/bin/opusenc",
		"/usr/local/bin/opusenc",
		"/usr/bin/opusenc",
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// libopusEncodeOpts describes one opusenc invocation. Fields left zero use
// opusenc defaults.
type libopusEncodeOpts struct {
	channels   int
	bitrateK   int    // --bitrate in kbit/s; 0 => opusenc default (VBR)
	frameMS    string // --framesize value, e.g. "20"; "" => default
	hardCBR    bool   // --hard-cbr
	signalKind string // "sine", "noise", "chirp", "silence"
	freqHz     float64
	durFrames  int // number of 960-sample (20 ms @ 48 kHz) frames of input
	// bigCommentLen, when > 0, adds a comment of that many bytes so opusenc
	// emits a multi-page OpusTags header — a real libopus layout the gopus Writer
	// never produces.
	bigCommentLen int
}

// extEncodeWithOpusenc renders int16 PCM to a temp WAV, runs opusenc and returns
// the resulting Ogg Opus bytes. ok is false (no error) when opusenc is present
// but cannot run in this environment (sandbox/quarantine), so callers can skip
// the oracle gracefully. tb is testing.TB so both Test (*testing.T) and Fuzz-seed
// (*testing.F) callers can drive it.
func extEncodeWithOpusenc(tb testing.TB, pcm []int16, opts libopusEncodeOpts) (ogg []byte, ok bool) {
	tb.Helper()

	tmpWav, err := os.CreateTemp("", "gopus_libopus_in_*.wav")
	if err != nil {
		return nil, false
	}
	defer os.Remove(tmpWav.Name())
	if _, err := tmpWav.Write(buildWAV16(pcm, opts.channels, 48000)); err != nil {
		tmpWav.Close()
		return nil, false
	}
	tmpWav.Close()

	tmpOpus, err := os.CreateTemp("", "gopus_libopus_out_*.opus")
	if err != nil {
		return nil, false
	}
	defer os.Remove(tmpOpus.Name())
	tmpOpus.Close()

	args := []string{"--quiet"}
	if opts.bitrateK > 0 {
		args = append(args, "--bitrate", itoa(opts.bitrateK))
	}
	if opts.frameMS != "" {
		args = append(args, "--framesize", opts.frameMS)
	}
	if opts.hardCBR {
		args = append(args, "--hard-cbr")
	}
	if opts.bigCommentLen > 0 {
		args = append(args, "--comment", "DESC="+repeatByte('X', opts.bigCommentLen))
	}
	args = append(args, tmpWav.Name(), tmpOpus.Name())

	cmd := exec.Command(getOpusencPath(), args...)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if isEnvExecFailure(out) {
			return nil, false
		}
		tb.Fatalf("opusenc failed unexpectedly (args=%v): %v: %s", args, runErr, bytes.TrimSpace(out))
	}

	data, err := os.ReadFile(tmpOpus.Name())
	if err != nil || len(data) == 0 {
		return nil, false
	}
	// Strip macOS quarantine so a later opusdec on derived files is not blocked.
	exec.Command("xattr", "-c", tmpOpus.Name()).Run()
	return data, true
}

// isEnvExecFailure reports whether opusenc/opusdec output indicates an
// environmental failure (sandbox, code-signing/quarantine) rather than a real
// stream rejection.
func isEnvExecFailure(out []byte) bool {
	for _, marker := range [][]byte{
		[]byte("provenance"),
		[]byte("quarantine"),
		[]byte("killed"),
		[]byte("Operation not permitted"),
		[]byte("Failed to open"),
		[]byte("cannot open"),
		[]byte("Permission denied"),
	} {
		if bytes.Contains(out, marker) {
			return true
		}
	}
	return false
}

// buildWAV16 wraps interleaved int16 PCM in a canonical 44-byte PCM WAV header.
func buildWAV16(pcm []int16, channels, sampleRate int) []byte {
	if channels <= 0 {
		channels = 1
	}
	dataBytes := len(pcm) * 2
	var b bytes.Buffer
	b.WriteString("RIFF")
	writeU32(&b, uint32(36+dataBytes))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	writeU32(&b, 16)                            // PCM fmt chunk size
	writeU16(&b, 1)                             // PCM
	writeU16(&b, uint16(channels))              // channels
	writeU32(&b, uint32(sampleRate))            // sample rate
	writeU32(&b, uint32(sampleRate*channels*2)) // byte rate
	writeU16(&b, uint16(channels*2))            // block align
	writeU16(&b, 16)                            // bits per sample
	b.WriteString("data")
	writeU32(&b, uint32(dataBytes))
	for _, s := range pcm {
		writeU16(&b, uint16(s))
	}
	return b.Bytes()
}

func writeU16(b *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	b.Write(tmp[:])
}

func writeU32(b *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	b.Write(tmp[:])
}

// repeatByte returns a string of n copies of c, used to build oversized opusenc
// comments that force a multi-page header.
func repeatByte(c byte, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

// itoa is a tiny dependency-free int formatter for opusenc arguments.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ---- signal generators (int16, interleaved) ----

// genPCM16 produces interleaved int16 PCM for one libopusEncodeOpts signal spec.
func genPCM16(opts libopusEncodeOpts) []int16 {
	channels := opts.channels
	if channels <= 0 {
		channels = 1
	}
	frames := opts.durFrames
	if frames <= 0 {
		frames = 25
	}
	n := frames * 960
	pcm := make([]int16, n*channels)
	freq := opts.freqHz
	if freq <= 0 {
		freq = 440
	}
	// Deterministic LCG for the noise case (reproducible corpus).
	var rng uint32 = 0x9e3779b9
	nextNoise := func() float64 {
		rng = rng*1664525 + 1013904223
		return (float64(rng>>8)/float64(1<<24))*2 - 1
	}
	for i := 0; i < n; i++ {
		tsec := float64(i) / 48000.0
		var v float64
		switch opts.signalKind {
		case "noise":
			v = 0.3 * nextNoise()
		case "chirp":
			f := freq + (float64(i)/float64(n))*(freq*3)
			v = 0.4 * math.Sin(2*math.Pi*f*tsec)
		case "silence":
			v = 0
		default: // "sine"
			v = 0.4 * math.Sin(2*math.Pi*freq*tsec)
		}
		s := int16(v * 32767)
		for c := 0; c < channels; c++ {
			// Slight per-channel detune for stereo so coupling has work to do.
			if c == 1 && opts.signalKind != "silence" {
				v2 := 0.4 * math.Sin(2*math.Pi*(freq*1.5)*tsec)
				pcm[i*channels+c] = int16(v2 * 32767)
				continue
			}
			pcm[i*channels+c] = s
		}
	}
	return pcm
}

// libopusOggCorpus is the matrix of libopus-authored streams used both as fuzz
// seeds and as the explicit-parity sanity matrix. It is intentionally small and
// fast (a few short streams) so the gate stays cheap on a shared host.
func libopusOggCorpus() []libopusEncodeOpts {
	return []libopusEncodeOpts{
		{channels: 1, bitrateK: 64, signalKind: "sine", durFrames: 25},
		{channels: 2, bitrateK: 96, signalKind: "sine", durFrames: 25},
		{channels: 1, bitrateK: 24, signalKind: "sine", durFrames: 20},   // SILK range
		{channels: 2, bitrateK: 128, signalKind: "chirp", durFrames: 20}, // CELT/Hybrid range
		{channels: 1, bitrateK: 32, frameMS: "60", signalKind: "sine", durFrames: 20},
		{channels: 2, bitrateK: 96, frameMS: "10", signalKind: "noise", durFrames: 20},
		{channels: 1, bitrateK: 48, hardCBR: true, signalKind: "sine", durFrames: 20},
		{channels: 2, bitrateK: 64, hardCBR: true, frameMS: "20", signalKind: "chirp", durFrames: 20},
		// Multi-page OpusTags header (oversized comment) + many-segment audio pages.
		{channels: 2, bitrateK: 96, signalKind: "sine", durFrames: 30, bigCommentLen: 2000},
	}
}

// buildLibopusOggSeeds renders every corpus entry with opusenc. Returns nil when
// opusenc is unavailable. Used to seed the fuzz corpus with real libopus bytes.
// tb is testing.TB so the fuzz seeders (*testing.F) can call it directly.
func buildLibopusOggSeeds(tb testing.TB) [][]byte {
	if !checkOpusenc() {
		return nil
	}
	var seeds [][]byte
	for _, opts := range libopusOggCorpus() {
		pcm := genPCM16(opts)
		ogg, ok := extEncodeWithOpusenc(tb, pcm, opts)
		if !ok || len(ogg) == 0 {
			continue
		}
		seeds = append(seeds, ogg)
	}
	return seeds
}

// ---- parity check ----

// requireLibopusContainerParity demuxes+decodes a libopus-authored stream with
// gopus and cross-checks the recovered PCM against the opusdec oracle.
//
// Both run libopus-equivalent decode logic at 48 kHz with the same pre-skip, so
// for clean encoder output their per-channel energy must agree closely. opusdec
// emits int16 PCM and may add a one-frame encoder-delay tail, so the comparison
// uses the common prefix with a one-frame duration slack.
func requireLibopusContainerParity(t *testing.T, oggData []byte) {
	t.Helper()

	r, err := NewReader(bytes.NewReader(oggData))
	if err != nil {
		t.Fatalf("gopus rejected a libopus-authored stream at NewReader: %v", err)
	}
	channels := int(r.Channels())
	if channels <= 0 {
		t.Fatalf("gopus parsed non-positive channel count %d from libopus stream", channels)
	}

	gopusPCM, err := decodeWithInternalDecoder(oggData)
	if err != nil {
		t.Fatalf("gopus failed to demux+decode a libopus-authored stream: %v", err)
	}
	requireFiniteFuzzSamples(t, gopusPCM)
	if len(gopusPCM)%channels != 0 {
		t.Fatalf("gopus PCM length %d not divisible by channels %d", len(gopusPCM), channels)
	}

	refPCM, ran, oracleErr := extOpusdecDecodePCM(oggData)
	if oracleErr != nil {
		t.Fatalf("opusdec rejected a libopus-authored stream gopus accepted: %v", oracleErr)
	}
	if !ran {
		t.Skip("opusdec present but could not run in this environment")
	}
	requireFiniteFuzzSamples(t, refPCM)
	requireContainerPCMParity(t, gopusPCM, refPCM, channels)
}

// ---- explicit parity matrix (runs under plain `go test`) ----

// TestOggLibopusStreamParity is the always-on sanity gate: every corpus stream
// is encoded by opusenc and must demux+decode through gopus to PCM matching
// opusdec. It guarantees the libopus-container parity property is exercised by a
// normal `go test ./container/ogg/...` run, not only under `-fuzz`.
func TestOggLibopusStreamParity(t *testing.T) {
	if !checkOpusenc() || !checkOpusdec() {
		t.Skip("opusenc/opusdec (opus-tools) not available")
	}
	for i, opts := range libopusOggCorpus() {
		t.Run(libopusCaseName(i, opts), func(t *testing.T) {
			pcm := genPCM16(opts)
			ogg, ok := extEncodeWithOpusenc(t, pcm, opts)
			if !ok {
				t.Skip("opusenc could not run in this environment")
			}
			requireLibopusContainerParity(t, ogg)
		})
	}
}

func libopusCaseName(i int, o libopusEncodeOpts) string {
	mode := "vbr"
	if o.hardCBR {
		mode = "cbr"
	}
	fr := o.frameMS
	if fr == "" {
		fr = "def"
	}
	name := itoa(i) + "_ch" + itoa(o.channels) + "_" + itoa(o.bitrateK) + "k_" + mode + "_fr" + fr + "_" + o.signalKind
	if o.bigCommentLen > 0 {
		name += "_bigtags"
	}
	return name
}

// ---- fuzz: robustness on mutated libopus streams ----

// FuzzOggLibopusMutationRobustness seeds with libopus-authored streams and their
// structural mutations (CRC flips, magic clobbers, header/payload truncations,
// segment-table corruption) and asserts gopus's demux is hardened: it never
// panics, never hangs, never over-allocates and never returns a packet larger
// than the whole input. gopus is permitted to be stricter than opusfile, so a
// gopus rejection is always acceptable; the invariant is purely "no crash, no
// oversized output".
func FuzzOggLibopusMutationRobustness(f *testing.F) {
	seeds := buildLibopusOggSeeds(f)
	for _, s := range seeds {
		f.Add(s)
		for _, m := range mutateOggStream(s) {
			f.Add(m)
		}
	}
	// Structural fallbacks so the corpus is non-empty without opusenc.
	if s := buildValidOpusStream(1, 6); len(s) > 0 {
		f.Add(s)
		for _, m := range mutateOggStream(s) {
			f.Add(m)
		}
	}
	f.Add([]byte{})
	f.Add([]byte("OggS\x00\x02"))
	f.Add(make([]byte, 27))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}

		// NewReader + drain: must terminate, never panic, never emit an
		// oversized packet.
		r, err := NewReader(bytes.NewReader(data))
		if err == nil {
			for i := 0; i < 4096; i++ {
				pkt, _, perr := r.ReadPacket()
				if perr == io.EOF || perr != nil {
					break
				}
				if len(pkt) > len(data) {
					t.Fatalf("packet len=%d exceeds input len=%d", len(pkt), len(data))
				}
			}
		}

		// ParsePage on a sliding window must also be panic-free and never claim
		// to consume more than is present.
		for off := 0; off < len(data); {
			page, consumed, perr := ParsePage(data[off:])
			if perr != nil {
				break
			}
			if consumed <= 0 || off+consumed > len(data) {
				t.Fatalf("ParsePage consumed=%d at off=%d out of bounds [0,%d]", consumed, off, len(data))
			}
			if len(page.Payload) > len(data) {
				t.Fatalf("ParsePage payload len=%d exceeds input len=%d", len(page.Payload), len(data))
			}
			off += consumed
		}
	})
}

// mutateOggStream returns a handful of deterministic structural corruptions of a
// valid Ogg stream, targeting the bytes a demux parser is most sensitive to:
// the capture pattern, the CRC, the segment count and arbitrary interior bytes.
func mutateOggStream(s []byte) [][]byte {
	if len(s) < 32 {
		return nil
	}
	out := make([][]byte, 0, 8)

	dup := func() []byte { return append([]byte(nil), s...) }

	// Clobber the capture pattern of the first page.
	m := dup()
	m[0] = 'X'
	out = append(out, m)

	// Flip a bit in the first page CRC (bytes 22..25).
	m = dup()
	m[22] ^= 0x01
	out = append(out, m)

	// Corrupt the first page segment count to 255 (claims a 255-entry table).
	m = dup()
	m[26] = 0xFF
	out = append(out, m)

	// Truncate to just the first page header.
	if len(s) > 27 {
		out = append(out, append([]byte(nil), s[:27]...))
	}

	// Truncate to the first half.
	out = append(out, append([]byte(nil), s[:len(s)/2]...))

	// Truncate one byte before the end (drops the final lacing/payload byte).
	out = append(out, append([]byte(nil), s[:len(s)-1]...))

	// Flip a byte deep in the stream (likely inside an audio page payload).
	m = dup()
	m[len(m)*3/4] ^= 0xFF
	out = append(out, m)

	// Splice: corrupt a mid-stream capture pattern if a second "OggS" exists.
	if idx := bytes.Index(s[4:], []byte("OggS")); idx >= 0 {
		m = dup()
		m[4+idx] = 'Z'
		out = append(out, m)
	}

	return out
}
