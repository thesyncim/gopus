// projection_decode_robustness_fuzz_test.go — DECODE ROBUSTNESS fuzz for the
// projection (mapping family 3) decode entry points NewProjectionDecoder +
// DecodeToFloat32 / DecodeToInt16 / DecodeToInt24. It is the projection analogue
// of the surround robustness sweep: arbitrary / structured-malformed input must
// never panic and must accept/reject in lockstep with libopus
// opus_projection_decode*.
//
// The clean-packet projection differential (TestProjectionDecodeDifferentialFuzz)
// proves PCM parity on valid bitstreams; this proves the projection decoder is
// crash-safe and accept/reject-faithful on garbage, exercising:
//   - the cross-stream self-delimited sub-packet framing parser,
//   - the per-stream Opus decode under corrupt payloads,
//   - the demixing-matrix matmul (which a malformed packet must not drive out of
//     bounds), and
//   - the empty/short-packet PLC path.
//
// Strategy (seeded):
//   (a) purely random byte buffers of every length 0..N → NO-PANIC only,
//   (b) structured-malformed mutations of valid family-3 packets (truncation,
//       byte/bit flips, first-stream TOC rewrites, sub-packet length corruption,
//       junk append) → NO-PANIC + accept/reject parity vs the libopus projection
//       oracle (one packet per call) + exact full PCM when both accept. Only a
//       typed oracle exit with a negative C decode status counts as rejection.

package multistream

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// projRobustSeed pairs a valid family-3 projection packet with the layout
// (channels/streams/coupled/demixing) it was encoded for.
type projRobustSeed struct {
	channels  int
	streams   int
	coupled   int
	demixing  []byte
	frameSize int
	packet    []byte
}

// projRobustSeedPackets encodes valid family-3 projection packets across the
// supported ambisonics orders (FOA/SOA/TOA) and a couple of frame sizes via the
// libopus projection encoder, returning one packet + its layout per spec. These
// are the bases the mutator perturbs.
func projRobustSeedPackets(t *testing.T) []projRobustSeed {
	t.Helper()
	const (
		application    = 2049 // OPUS_APPLICATION_AUDIO
		bandwidthAuto  = -1000
		maxPacketBytes = 4000
		sampleRate     = 48000
	)
	var seeds []projRobustSeed
	for _, channels := range []int{4, 9, 16} {
		for _, fs := range []int{480, 960} {
			pcm := generateAmbisonicsSweep(channels, fs, 2)
			ref, err := encodeLibopusProjection(sampleRate, channels, application,
				128000, false, false, 10, bandwidthAuto, fs, 2, maxPacketBytes, 0, pcm, nil)
			if err != nil {
				libopustest.HelperUnavailable(t, "projection reference encode", err)
				return seeds
			}
			if len(ref.packets) != 2 || len(ref.packets[0]) == 0 {
				t.Fatalf("projection seed ch%d frame%d: missing C packets", channels, fs)
			}
			seeds = append(seeds, projRobustSeed{
				channels:  channels,
				streams:   ref.streams,
				coupled:   ref.coupledStreams,
				demixing:  append([]byte(nil), ref.demixing...),
				frameSize: fs,
				packet:    append([]byte(nil), ref.packets[0]...),
			})
		}
	}
	return seeds
}

// projRobustMutate applies one seeded structured mutation to a copy of a valid
// projection packet.
func projRobustMutate(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	switch rng.Intn(8) {
	case 0: // truncate (incl. 0 → empty/PLC)
		return p[:rng.Intn(len(p)+1)]
	case 1: // single byte overwrite
		p[rng.Intn(len(p))] = byte(rng.Intn(256))
		return p
	case 2: // single bit flip
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // rewrite first stream TOC config
		p[0] = (p[0] & 0x07) | byte(rng.Intn(32))<<3
		return p
	case 4: // flip first stream TOC code bits
		p[0] = (p[0] & 0xFC) | byte(rng.Intn(4))
		return p
	case 5: // corrupt a self-delimited sub-packet length byte
		if len(p) >= 2 {
			i := 1 + rng.Intn(min(8, len(p)-1))
			p[i] = byte(rng.Intn(256))
		}
		return p
	case 6: // append junk
		n := 1 + rng.Intn(8)
		for range n {
			p = append(p, byte(rng.Intn(256)))
		}
		return p
	default: // scribble a short run
		n := 1 + rng.Intn(4)
		for range n {
			p[rng.Intn(len(p))] = byte(rng.Intn(256))
		}
		return p
	}
}

// projRobustFormat selects the matching Go and C public sample format.
type projRobustFormat int

const (
	projRobustFloat32 projRobustFormat = iota
	projRobustInt16
	projRobustInt24
)

// projRobustGopusDecode decodes one packet through a fresh gopus projection
// decoder, recovering a panic into an error so a crash minimises to one packet.
func projRobustGopusDecode(seed projRobustSeed, format projRobustFormat, packet []byte) (pcm []uint32, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus projection decode: %v", r)
		}
	}()
	dec, err := NewProjectionDecoder(48000, seed.channels, seed.streams, seed.coupled, seed.demixing)
	if err != nil {
		return nil, err
	}
	switch format {
	case projRobustInt16:
		out, e := dec.DecodeToInt16(packet, seed.frameSize)
		for _, v := range out {
			pcm = append(pcm, uint32(int32(v)))
		}
		err = e
	case projRobustInt24:
		out, e := dec.DecodeToInt24(packet, seed.frameSize)
		for _, v := range out {
			pcm = append(pcm, uint32(v))
		}
		err = e
	default:
		out, e := dec.DecodeToFloat32(packet, seed.frameSize)
		for _, v := range out {
			pcm = append(pcm, math.Float32bits(v))
		}
		err = e
	}
	return pcm, err
}

// projRobustOracleDecode preserves every output bit. Infrastructure failures
// remain errors; only the C helper's negative decode status means rejection.
func projRobustOracleDecode(seed projRobustSeed, format projRobustFormat, packet []byte) (pcm []uint32, rejected bool, err error) {
	mapping := trivialMapping(seed.channels)
	switch format {
	case projRobustInt16:
		out, e := decodeWithLibopusReferencePacketsInt16Gain(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, 0, mapping, seed.demixing, [][]byte{packet})
		for _, v := range out {
			pcm = append(pcm, uint32(int32(v)))
		}
		err = e
	case projRobustInt24:
		out, e := decodeWithLibopusReferencePacketsInt24Gain(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, 0, mapping, seed.demixing, [][]byte{packet})
		for _, v := range out {
			pcm = append(pcm, uint32(v))
		}
		err = e
	default:
		out, e := decodeWithLibopusReferencePackets(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, mapping, seed.demixing, [][]byte{packet})
		for _, v := range out {
			pcm = append(pcm, math.Float32bits(v))
		}
		err = e
	}
	if projRobustOracleRejected(err) {
		return nil, true, nil
	}
	return pcm, false, err
}

func projRobustOracleRejected(err error) bool {
	var child *exec.ExitError
	if !errors.As(err, &child) || child.ExitCode() != 1 {
		return false
	}
	const marker = "(opus_projection_decode_float failed: "
	message := err.Error()
	start := strings.LastIndex(message, marker)
	if start < 0 || !strings.HasSuffix(message, ")") {
		return false
	}
	code, e := strconv.ParseInt(strings.TrimSuffix(message[start+len(marker):], ")"), 10, 32)
	return e == nil && code < 0
}

func TestProjectionRobustOracleErrorClassification(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := projRobustSeedPackets(t)
	if len(seeds) != 6 {
		t.Fatalf("seed count=%d, want 6", len(seeds))
	}
	for _, format := range []projRobustFormat{projRobustFloat32, projRobustInt16, projRobustInt24} {
		_, rejected, err := projRobustOracleDecode(seeds[0], format, []byte{0xff})
		if err != nil || !rejected {
			t.Fatalf("format %d: rejected=%v error=%v, want C packet rejection", format, rejected, err)
		}
	}
	for _, message := range []string{
		"compile projection reference: compiler failed",
		"run helper: exit status 1 (failed to read packet payload)",
		"projection helper: truncated output header",
		"run helper: exit status 1 (opus_projection_decode_float failed: -4",
		"run helper: exit status 1 (opus_projection_decode_float failed: -4)",
	} {
		if projRobustOracleRejected(errors.New(message)) {
			t.Fatalf("infrastructure error classified as packet rejection: %s", message)
		}
	}
}

func projIsRobustPanic(err error) bool {
	return err != nil && len(err.Error()) >= 5 && err.Error()[:5] == "PANIC"
}

// TestProjectionDecodeRobustnessRandom feeds random byte buffers to the
// projection decoder across the supported orders and asserts NO panic and a
// sample count within the requested frame size.
func TestProjectionDecodeRobustnessRandom(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := projRobustSeedPackets(t)
	if len(seeds) == 0 {
		t.Fatal("no projection seed packets")
	}

	const maxLen = 160
	rng := rand.New(rand.NewSource(0x9203A))
	iters := fuzzBudget(4000)
	cases := 0
	for range iters {
		seed := seeds[rng.Intn(len(seeds))]
		n := rng.Intn(maxLen + 1)
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		for _, format := range []projRobustFormat{projRobustFloat32, projRobustInt16, projRobustInt24} {
			pcm, err := projRobustGopusDecode(seed, format, buf)
			samples := len(pcm) / seed.channels
			cases++
			if projIsRobustPanic(err) {
				t.Fatalf("ch%d/fmt%d: %v — packet=% x", seed.channels, format, err, buf)
			}
			if err == nil && (samples < 0 || samples > seed.frameSize) {
				t.Fatalf("ch%d/fmt%d: samples=%d outside [0,%d] — packet=% x", seed.channels, format, samples, seed.frameSize, buf)
			}
		}
	}
	t.Logf("projection random no-panic sweep: %d cases over %d seeds", cases, len(seeds))
}

// TestProjectionDecodeRobustnessMalformed mutates valid family-3 packets and
// asserts gopus and the libopus projection oracle agree on accept-vs-reject and
// exact PCM bits in the selected format, with NO panic.
func TestProjectionDecodeRobustnessMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := projRobustSeedPackets(t)
	if len(seeds) == 0 {
		t.Fatal("no projection seed packets")
	}

	rng := rand.New(rand.NewSource(0x9203AB0F))
	iters := fuzzBudget(4000)
	formats := []projRobustFormat{projRobustFloat32, projRobustInt16, projRobustInt24}
	total := 0
	var accepted, rejectedCounts [3]int
	for k := range iters {
		seed := seeds[rng.Intn(len(seeds))]
		m := projRobustMutate(rng, seed.packet)
		format := formats[rng.Intn(len(formats))]
		label := fmt.Sprintf("ch%d/fmt%d/mut%d", seed.channels, format, k)

		got, gerr := projRobustGopusDecode(seed, format, m)
		gn := len(got) / seed.channels
		if projIsRobustPanic(gerr) {
			t.Fatalf("%s: %v — packet=% x", label, gerr, m)
		}
		want, rejected, oerr := projRobustOracleDecode(seed, format, m)
		on := len(want) / seed.channels
		if oerr != nil {
			libopustest.HelperUnavailable(t, "projection reference decode", oerr)
			return
		}
		total++

		if rejected {
			rejectedCounts[format]++
			if gerr == nil {
				t.Errorf("%s: libopus REJECTED but gopus ACCEPTED (n=%d) — packet=% x", label, gn, m)
			}
			continue
		}
		if gerr != nil {
			t.Errorf("%s: libopus ACCEPTED (n=%d) but gopus REJECTED: %v — packet=% x", label, on, gerr, m)
			continue
		}
		accepted[format]++
		if !slices.Equal(got, want) {
			first := 0
			for first < min(len(got), len(want)) && got[first] == want[first] {
				first++
			}
			t.Errorf("%s: PCM differs at sample %d; lengths Go=%d C=%d — packet=% x", label, first, len(got), len(want), m)
		}
	}
	for _, format := range formats {
		if accepted[format] == 0 || rejectedCounts[format] == 0 {
			t.Fatalf("format %d lacks accepted/rejected coverage: %d/%d", format, accepted[format], rejectedCounts[format])
		}
	}
	t.Logf("projection malformed sweep: %d cases; accepted float/int16/int24=%v, rejected=%v", total, accepted, rejectedCounts)
}
