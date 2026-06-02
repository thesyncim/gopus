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
//       oracle (one packet per call; an oracle error ⇔ libopus rejected) + sample
//       count parity when both accept.

package multistream

import (
	"fmt"
	"math/rand"
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
			if len(ref.packets) == 0 || len(ref.packets[0]) == 0 {
				continue
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
		for k := 0; k < n; k++ {
			p = append(p, byte(rng.Intn(256)))
		}
		return p
	default: // scribble a short run
		n := 1 + rng.Intn(4)
		for k := 0; k < n; k++ {
			p[rng.Intn(len(p))] = byte(rng.Intn(256))
		}
		return p
	}
}

// projRobustFormat selects the decode path. The lower-level projection Decoder
// exposes float32 and int16 output (int24 is a gopus-package-only entry point,
// covered by the surround robustness sweep).
type projRobustFormat int

const (
	projRobustFloat32 projRobustFormat = iota
	projRobustInt16
)

// projRobustGopusDecode decodes one packet through a fresh gopus projection
// decoder, recovering a panic into an error so a crash minimises to one packet.
func projRobustGopusDecode(seed projRobustSeed, format projRobustFormat, packet []byte) (samples int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus projection decode: %v", r)
		}
	}()
	dec, derr := NewProjectionDecoder(48000, seed.channels, seed.streams, seed.coupled, seed.demixing)
	if derr != nil {
		return 0, derr
	}
	switch format {
	case projRobustInt16:
		out, e := dec.DecodeToInt16(packet, seed.frameSize)
		if e != nil {
			return 0, e
		}
		return len(out) / seed.channels, nil
	default:
		out, e := dec.DecodeToFloat32(packet, seed.frameSize)
		if e != nil {
			return 0, e
		}
		return len(out) / seed.channels, nil
	}
}

// projRobustOracleDecode decodes one packet through the libopus projection oracle
// and reports rejection (oracle error) or the per-channel sample count.
func projRobustOracleDecode(seed projRobustSeed, format projRobustFormat, packet []byte) (samples int, rejected bool, err error) {
	mapping := trivialMapping(seed.channels)
	switch format {
	case projRobustInt16:
		out, e := decodeWithLibopusReferencePacketsInt16Gain(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, 0, mapping, seed.demixing, [][]byte{packet})
		if e != nil {
			return 0, true, nil
		}
		return len(out) / seed.channels, false, nil
	default:
		out, e := decodeWithLibopusReferencePackets(3, 48000, seed.channels, seed.streams, seed.coupled, seed.frameSize, mapping, seed.demixing, [][]byte{packet})
		if e != nil {
			return 0, true, nil
		}
		return len(out) / seed.channels, false, nil
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
		t.Skip("no projection seed packets")
	}

	const maxLen = 160
	rng := rand.New(rand.NewSource(0x9203A))
	iters := fuzzBudget(4000)
	cases := 0
	for k := 0; k < iters; k++ {
		seed := seeds[rng.Intn(len(seeds))]
		n := rng.Intn(maxLen + 1)
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		for _, format := range []projRobustFormat{projRobustFloat32, projRobustInt16} {
			samples, err := projRobustGopusDecode(seed, format, buf)
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
// per-channel sample count, with NO panic.
func TestProjectionDecodeRobustnessMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := projRobustSeedPackets(t)
	if len(seeds) == 0 {
		t.Skip("no projection seed packets")
	}

	rng := rand.New(rand.NewSource(0x9203AB0F))
	iters := fuzzBudget(4000)
	formats := []projRobustFormat{projRobustFloat32, projRobustInt16}
	total := 0
	for k := 0; k < iters; k++ {
		seed := seeds[rng.Intn(len(seeds))]
		m := projRobustMutate(rng, seed.packet)
		format := formats[rng.Intn(len(formats))]
		label := fmt.Sprintf("ch%d/fmt%d/mut%d", seed.channels, format, k)

		gn, gerr := projRobustGopusDecode(seed, format, m)
		if projIsRobustPanic(gerr) {
			t.Fatalf("%s: %v — packet=% x", label, gerr, m)
		}
		on, rejected, oerr := projRobustOracleDecode(seed, format, m)
		if oerr != nil {
			libopustest.HelperUnavailable(t, "projection reference decode", oerr)
			return
		}
		total++

		if rejected {
			if gerr == nil {
				t.Errorf("%s: libopus REJECTED but gopus ACCEPTED (n=%d) — packet=% x", label, gn, m)
			}
			continue
		}
		if gerr != nil {
			t.Errorf("%s: libopus ACCEPTED (n=%d) but gopus REJECTED: %v — packet=% x", label, on, gerr, m)
			continue
		}
		if gn != on {
			t.Errorf("%s: sample count gopus=%d libopus=%d — packet=% x", label, gn, on, m)
		}
	}
	t.Logf("projection malformed sweep: %d cases", total)
}
