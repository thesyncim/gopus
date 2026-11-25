package gopus

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeFECNoPacketLossChannelRoutingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modeCases := []struct {
		name   string
		mode   Mode
		packet func(*testing.T, int, int) []byte
	}{
		{
			name: "hybrid",
			mode: ModeHybrid,
			packet: func(t *testing.T, channels, frameSize int) []byte {
				return encodeAPIRateHybridPacketFrameSize(t, channels, frameSize)
			},
		},
		{
			name: "silk",
			mode: ModeSILK,
			packet: func(t *testing.T, channels, frameSize int) []byte {
				return encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)
			},
		},
	}

	for _, modeCase := range modeCases {
		for _, codedChannels := range []int{1, 2} {
			packet := modeCase.packet(t, codedChannels, 960)
			toc := ParseTOC(packet[0])
			if toc.Mode != modeCase.mode || toc.Stereo != (codedChannels == 2) {
				t.Fatalf("%s coded channels=%d TOC=(%v, stereo=%v), want (%v, stereo=%v)",
					modeCase.name, codedChannels, toc.Mode, toc.Stereo, modeCase.mode, codedChannels == 2)
			}
			for _, apiChannels := range []int{1, 2} {
				for _, plcFrameSize := range []int{240, 480, 960, 1920} {
					for _, fec := range []bool{false, true} {
						name := modeCase.name + "/coded_" + itoaSmall(codedChannels) +
							"_api_" + itoaSmall(apiChannels) +
							"_plc_" + itoaSmall(plcFrameSize) +
							"_fec_" + itoaSmall(boolInt(fec))
						t.Run(name, func(t *testing.T) {
							decodeNoPacketLossSequenceMatchesLibopus(t, packet, apiChannels, plcFrameSize, fec)
						})
					}
				}
			}
		}
	}
}

func decodeNoPacketLossSequenceMatchesLibopus(t *testing.T, packet []byte, channels, plcFrameSize int, fec bool) {
	t.Helper()
	const primeFrameSize = 960
	steps := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: primeFrameSize},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(plcFrameSize), DecodeFEC: fec},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: primeFrameSize},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "stateful selected-libopus PLC sequence", err)
	}
	if len(want) != len(steps) {
		t.Fatalf("libopus returned %d sequence results, want %d", len(want), len(steps))
	}
	for i := range want {
		if want[i].Code <= 0 {
			t.Fatalf("libopus step %d returned %d", i, want[i].Code)
		}
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	frame := make([]float32, primeFrameSize*channels)
	decodeAndCompare := func(step int, decode func([]float32) (int, error), out []float32) {
		t.Helper()
		gotSamples, err := decode(out)
		if err != nil {
			t.Fatalf("Go decode step %d: %v", step, err)
		}
		if gotSamples != int(want[step].Code) {
			t.Fatalf("Go step %d returned %d samples, want %d", step, gotSamples, want[step].Code)
		}
		gotRange, wantRange := dec.FinalRange(), want[step].FinalRange
		if gotRange != wantRange {
			t.Fatalf("Go step %d final range=%08x, C=%08x", step, gotRange, wantRange)
		}
		wantPCM := want[step].Float32()
		if len(out) < len(wantPCM) {
			t.Fatalf("Go step %d output length=%d, C PCM length=%d", step, len(out), len(wantPCM))
		}
		for i := range wantPCM {
			if gotBits, wantBits := math.Float32bits(out[i]), math.Float32bits(wantPCM[i]); gotBits != wantBits {
				t.Fatalf("step %d PCM[%d]=%08x, C=%08x", step, i, gotBits, wantBits)
			}
		}
	}
	decodeAndCompare(0, func(out []float32) (int, error) { return dec.Decode(packet, out) }, frame)

	plc := make([]float32, plcFrameSize*channels)
	decodeLoss := func(out []float32) (int, error) {
		if fec {
			return dec.DecodeWithFEC(nil, out, true)
		}
		return dec.Decode(nil, out)
	}
	decodeAndCompare(1, decodeLoss, plc)
	decodeAndCompare(2, func(out []float32) (int, error) { return dec.Decode(packet, out) }, frame)

	// The same public loss path remains allocation-free after its lazy scratch
	// buffers and channel-specific resamplers have been warmed.
	if _, err := decodeLoss(plc); err != nil {
		t.Fatalf("warm PLC decode: %v", err)
	}
	if _, err := decodeLoss(plc); err != nil {
		t.Fatalf("second warm PLC decode: %v", err)
	}
	allocs := testing.AllocsPerRun(10, func() {
		if _, err := decodeLoss(plc); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("warmed PLC allocations=%g, want 0", allocs)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

type fecRobustDecodeResult struct {
	samples    int
	finalRange uint32
	pcm        []float32
}

type fecRobustSequenceResult struct {
	prime fecRobustDecodeResult
	fec   fecRobustDecodeResult
}

type fecRobustMutationPair struct {
	channels int
	k        int
	good     []byte
	mutated  []byte
}

type fecRobustMismatchWitness struct {
	pairIndex int
	sample    int
	gotBits   uint32
	wantBits  uint32
}

type fecRobustRangeResult struct {
	ran            bool
	fatal          bool
	pairs          int
	primeSkipped   int
	fecAccepted    int
	fecRejected    int
	total          int
	oracleErr      error
	oracleErrIndex int
	witness        fecRobustMismatchWitness
	hasWitness     bool
}

// fecRobustGopusDecode primes the decoder with one good packet, then runs
// DecodeWithFEC(fec=true) on the mutated packet, recovering a panic into an error
// so a crash minimises to one (good, mutated) pair.
func fecRobustGopusDecode(sampleRate, channels int, good, mutated []byte, frameSize int) (result fecRobustSequenceResult, err error) {
	result.prime.samples = -1
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus DecodeWithFEC: %v", r)
		}
	}()
	dec, derr := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if derr != nil {
		return result, derr
	}
	// Prime: a normal decode of one good packet so prevMode/lastFrameSize match the
	// oracle's primed state before the FEC decode.
	if len(good) > 0 {
		prime := make([]float32, frameSize*channels)
		n, e := dec.Decode(good, prime)
		if e != nil {
			// A prime failure is not the case under test; bail without flagging it as
			// a FEC divergence.
			result.prime.samples = -1
			return result, nil
		}
		result.prime = fecRobustDecodeResult{
			samples:    n,
			finalRange: dec.FinalRange(),
			pcm:        append([]float32(nil), prime[:n*channels]...),
		}
	}
	pcm := make([]float32, frameSize*channels)
	n, e := dec.DecodeWithFEC(mutated, pcm, true)
	if e != nil {
		return result, e
	}
	result.fec = fecRobustDecodeResult{
		samples:    n,
		finalRange: dec.FinalRange(),
		pcm:        append([]float32(nil), pcm[:n*channels]...),
	}
	return result, nil
}

// fecRobustOracleDecode mirrors fecRobustGopusDecode through one persistent
// libopus decoder: a primed good-packet decode_fec=0 followed by the mutated
// decode_fec=1 call. It returns PCM and final-range results for both steps.
func fecRobustOracleDecode(sampleRate, channels int, good, mutated []byte, frameSize int) ([]libopustest.DecodeDiffResult, error) {
	cases := []libopustest.DecodeDiffCase{
		{Packet: good, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: false},
		{Packet: mutated, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: true},
	}
	res, e := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if e != nil {
		return nil, e
	}
	return res, nil
}

// TestDecodeWithFECRobustnessMalformed mutates valid packets and asserts gopus
// DecodeWithFEC(fec=true) and the libopus decode_fec oracle agree on
// accept-vs-reject, sample count, PCM, and final range, with NO panic. Each case
// primes both decoders with the same good packet first.
func TestDecodeWithFECRobustnessMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	seeds := seedPacketsForMutation(t)
	if len(seeds) == 0 {
		t.Fatal("seed corpus is empty")
	}

	const frameSize = 5760
	iters := diffFuzzBudget(8000)
	rng := rand.New(rand.NewSource(0xFEC0B0F))

	// Generate every pair before the parallel ranges start. This keeps the
	// original single-RNG sequence and leaves each pair's packet bytes immutable
	// while its independent decoder and oracle histories run.
	pairs := make([]fecRobustMutationPair, 0, iters)
	for _, channels := range []int{1, 2} {
		for k := 0; k < iters/2; k++ {
			good := seeds[rng.Intn(len(seeds))]
			mutated := mutatePacket(rng, seeds[rng.Intn(len(seeds))])
			pairs = append(pairs, fecRobustMutationPair{
				channels: channels,
				k:        k,
				good:     good,
				mutated:  mutated,
			})
		}
	}

	const maxParallelRanges = 4
	rangeCount := min(maxParallelRanges, len(pairs))
	rangeResults := make([]fecRobustRangeResult, rangeCount)
	for rangeIndex := range rangeCount {
		start := rangeIndex * len(pairs) / rangeCount
		end := (rangeIndex + 1) * len(pairs) / rangeCount
		rangeIndex, start, end := rangeIndex, start, end
		t.Run(fmt.Sprintf("range%d", rangeIndex), func(t *testing.T) {
			t.Parallel()
			result := fecRobustRangeResult{ran: true, oracleErrIndex: -1}
			defer func() { rangeResults[rangeIndex] = result }()

			for pairIndex := start; pairIndex < end; pairIndex++ {
				pair := pairs[pairIndex]
				label := fmt.Sprintf("ch%d/fec/mut%d", pair.channels, pair.k)
				result.pairs++

				oracle, oerr := fecRobustOracleDecode(48000, pair.channels, pair.good, pair.mutated, frameSize)
				if oerr != nil {
					result.oracleErr = fmt.Errorf("%s: oracle failed at pair %d; %d later cases in this range were not run: %w",
						label, pairIndex, end-pairIndex-1, oerr)
					result.oracleErrIndex = pairIndex
					return
				}
				if len(oracle) != 2 {
					result.fatal = true
					t.Fatalf("%s: oracle returned %d sequence records, want 2; %d later cases in this range were not run",
						label, len(oracle), end-pairIndex-1)
				}

				got, gerr := fecRobustGopusDecode(48000, pair.channels, pair.good, pair.mutated, frameSize)
				if gerr != nil && isMSRobustPanic(gerr) {
					result.fatal = true
					t.Fatalf("%s: %v — good=% x mutated=% x; %d later cases in this range were not run",
						label, gerr, pair.good, pair.mutated, end-pairIndex-1)
				}
				if oracle[0].Code < 0 {
					if got.prime.samples >= 0 {
						t.Errorf("%s: libopus rejected prime (%d) but gopus accepted (%d) — good=% x", label, oracle[0].Code, got.prime.samples, pair.good)
						continue
					}
					// The good seed was rejected at this channel count (e.g. a stereo bit
					// mismatch); the FEC case under test never runs on either decoder.
					result.primeSkipped++
					continue
				}
				if got.prime.samples == -1 {
					// gopus declined the prime; the oracle accepted it, so this is a prime
					// accept/reject divergence on a CLEAN seed — flag it.
					t.Errorf("%s: gopus rejected the prime good packet the oracle accepted — good=% x", label, pair.good)
					continue
				}
				result.total++
				if int(got.prime.samples) != int(oracle[0].Code) {
					t.Errorf("%s: prime sample count gopus=%d libopus=%d — good=% x", label, got.prime.samples, oracle[0].Code, pair.good)
					continue
				}
				if got.prime.finalRange != oracle[0].FinalRange {
					t.Errorf("%s: prime final range gopus=%08x libopus=%08x — good=% x", label, got.prime.finalRange, oracle[0].FinalRange, pair.good)
					continue
				}
				if !fecRobustPCMEqual(got.prime.pcm, oracle[0].Float32()) {
					t.Errorf("%s: prime PCM differs — good=% x", label, pair.good)
					continue
				}

				if oracle[1].Code < 0 {
					if gerr == nil {
						t.Errorf("%s: libopus decode_fec REJECTED (%d) but gopus ACCEPTED (n=%d) — mutated=% x", label, oracle[1].Code, got.fec.samples, pair.mutated)
					} else {
						result.fecRejected++
					}
					continue
				}
				if gerr != nil {
					t.Errorf("%s: libopus decode_fec ACCEPTED (n=%d) but gopus REJECTED: %v — mutated=% x", label, oracle[1].Code, gerr, pair.mutated)
					continue
				}
				if got.fec.samples != int(oracle[1].Code) {
					t.Errorf("%s: decode_fec sample count gopus=%d libopus=%d — mutated=% x", label, got.fec.samples, oracle[1].Code, pair.mutated)
					continue
				}
				result.fecAccepted++
				if got.fec.finalRange != oracle[1].FinalRange {
					t.Errorf("%s: decode_fec final range gopus=%08x libopus=%08x — mutated=% x", label, got.fec.finalRange, oracle[1].FinalRange, pair.mutated)
					continue
				}
				wantFEC := oracle[1].Float32()
				if i := fecRobustPCMFirstMismatch(got.fec.pcm, wantFEC); i >= 0 {
					var toc byte
					if len(pair.mutated) > 0 {
						toc = pair.mutated[0]
					}
					if len(got.fec.pcm) != len(wantFEC) {
						t.Errorf("%s: decode_fec PCM length gopus=%d libopus=%d", label, len(got.fec.pcm), len(wantFEC))
						continue
					}
					gotBits, wantBits := math.Float32bits(got.fec.pcm[i]), math.Float32bits(wantFEC[i])
					t.Errorf("%s: decode_fec PCM[%d]=%08x want %08x (packet bytes=%d TOC=%02x)", label, i, gotBits, wantBits, len(pair.mutated), toc)
					if !result.hasWitness {
						result.witness = fecRobustMismatchWitness{
							pairIndex: pairIndex,
							sample:    i,
							gotBits:   gotBits,
							wantBits:  wantBits,
						}
						result.hasWitness = true
					}
				}
			}
		})
	}

	t.Cleanup(func() {
		var total, primeSkipped, fecAccepted, fecRejected, processedPairs, expectedPairs int
		var fatalChild bool
		var helperErr error
		helperErrIndex := len(pairs)
		firstWitnessIndex := len(pairs)
		var firstWitness fecRobustMismatchWitness
		for rangeIndex, result := range rangeResults {
			if !result.ran {
				continue
			}
			start := rangeIndex * len(pairs) / rangeCount
			end := (rangeIndex + 1) * len(pairs) / rangeCount
			expectedPairs += end - start
			processedPairs += result.pairs
			total += result.total
			primeSkipped += result.primeSkipped
			fecAccepted += result.fecAccepted
			fecRejected += result.fecRejected
			fatalChild = fatalChild || result.fatal
			if result.oracleErr != nil && result.oracleErrIndex < helperErrIndex {
				helperErr = result.oracleErr
				helperErrIndex = result.oracleErrIndex
			}
			if result.hasWitness && result.witness.pairIndex < firstWitnessIndex {
				firstWitness = result.witness
				firstWitnessIndex = result.witness.pairIndex
			}
		}

		if expectedPairs == 0 {
			return
		}

		// Handle a helper failure on the parent so optional-oracle runs still skip
		// the whole sweep, matching the serial test's behavior.
		if helperErr != nil {
			helperErr = fmt.Errorf("%w; started %d of %d selected pairs (%d not started)",
				helperErr, processedPairs, expectedPairs, expectedPairs-processedPairs)
			libopustest.HelperUnavailable(t, "decode diff probe", helperErr)
			return
		}

		if firstWitnessIndex < len(pairs) {
			pair := pairs[firstWitness.pairIndex]
			var goodTOC, mutatedTOC byte
			if len(pair.good) > 0 {
				goodTOC = pair.good[0]
			}
			if len(pair.mutated) > 0 {
				mutatedTOC = pair.mutated[0]
			}
			t.Logf("first mismatch witness: format=float32 rate=48000 channels=%d request=%d good_bytes=%d good_TOC=%02x mutated_bytes=%d mutated_TOC=%02x sample=%d go=%08x c=%08x good=% x mutated=% x",
				pair.channels, frameSize, len(pair.good), goodTOC, len(pair.mutated), mutatedTOC,
				firstWitness.sample, firstWitness.gotBits, firstWitness.wantBits, pair.good, pair.mutated)
		}

		t.Logf("decode_fec malformed sweep: %d primed sequences, %d accepted FEC, %d rejected FEC (%d prime-skipped); pairs=%d/%d",
			total, fecAccepted, fecRejected, primeSkipped, processedPairs, expectedPairs)
		if fatalChild {
			return
		}
		if processedPairs != expectedPairs {
			t.Errorf("decode_fec malformed sweep processed %d of %d selected pairs", processedPairs, expectedPairs)
		}
		if total == 0 || fecAccepted == 0 {
			t.Fatalf("decode_fec malformed sweep had no accepted sequences: matched=%d accepted_fec=%d rejected_fec=%d prime-skipped=%d", total, fecAccepted, fecRejected, primeSkipped)
		}
		if primeSkipped != 0 {
			t.Fatalf("decode_fec malformed sweep contains %d prime seeds rejected by both decoders", primeSkipped)
		}
	})
}

func fecRobustPCMEqual(got, want []float32) bool {
	return fecRobustPCMFirstMismatch(got, want) < 0
}

func fecRobustPCMFirstMismatch(got, want []float32) int {
	if len(got) != len(want) {
		return min(len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			return i
		}
	}
	return -1
}

type msRobustLayout struct {
	name        string
	channels    int
	streams     int
	coupled     int
	mapping     []byte
	streamChans []int // 2 for a coupled stream, 1 for an uncoupled mono stream
}

// msRobustLayouts enumerates the layouts swept: a single mono/stereo stream
// (the simplest wrapper path), mono-pair, coupled stereo, quad, and a 5.1-style
// surround mapping (the most routing-heavy public layout).
func msRobustLayouts() []msRobustLayout {
	return []msRobustLayout{
		{"mono_1stream", 1, 1, 0, []byte{0}, []int{1}},
		{"stereo_coupled", 2, 1, 1, []byte{0, 1}, []int{2}},
		{"mono_2streams", 2, 2, 0, []byte{0, 1}, []int{1, 1}},
		{"quad_2coupled", 4, 2, 2, []byte{0, 1, 2, 3}, []int{2, 2}},
		{"surround51", 6, 4, 2, []byte{0, 4, 1, 2, 3, 5}, []int{2, 2, 1, 1}},
	}
}

// msRobustPackSelfDelimitedLength appends the self-delimited frame-length prefix
// (one byte < 252, else two bytes) that opus_multistream uses between sub-packets.
func msRobustPackSelfDelimitedLength(dst []byte, n int) []byte {
	if n < 252 {
		return append(dst, byte(n))
	}
	return append(dst, byte(252+(n-252)&0x3), byte((n-252)>>2))
}

// msRobustBuildPacket concatenates per-stream code-0 packets into one
// multistream packet using opus_multistream framing (N-1 self-delimited streams
// followed by one standard-framed stream). It is the non-fatal sibling of the
// parity test's buildMultistreamPacket: it returns nil if any stream packet is
// not a code-0 single frame, so seed generation can simply skip such inputs.
func msRobustBuildPacket(streamPackets [][]byte) []byte {
	var out []byte
	for i, pkt := range streamPackets {
		if len(pkt) < 1 || (pkt[0]&0x03) != 0 {
			return nil
		}
		toc := pkt[0]
		frame := pkt[1:]
		if i < len(streamPackets)-1 {
			out = append(out, toc)
			out = msRobustPackSelfDelimitedLength(out, len(frame))
			out = append(out, frame...)
		} else {
			out = append(out, toc)
			out = append(out, frame...)
		}
	}
	return out
}

// msRobustSeed pairs a valid multistream packet with the layout it was built for
// (the layout is needed to construct the matching gopus decoder and to drive the
// oracle).
type msRobustSeed struct {
	layout msRobustLayout
	packet []byte
}

// msRobustSeedPackets builds a bank of valid multistream packets across the
// layouts and the three coding modes (CELT / SILK / Hybrid), mono+stereo per
// stream, several frame sizes. These are the bases the mutator perturbs.
func msRobustSeedPackets(t *testing.T) []msRobustSeed {
	t.Helper()
	var seeds []msRobustSeed

	// Per-(channels,frameSize,mode) code-0 stream packets to assemble from.
	streamPacket := func(ch, fs int, mode Mode) []byte {
		switch mode {
		case ModeSILK:
			return encodeAPIRateSILKPacketFrameSize(t, ch, fs)
		case ModeHybrid:
			return encodeAPIRateHybridPacketFrameSize(t, ch, fs)
		default:
			return encodeAPIRateCELTPacketFrameSize(t, ch, fs)
		}
	}

	for _, lo := range msRobustLayouts() {
		for _, mode := range []Mode{ModeCELT, ModeSILK, ModeHybrid} {
			// Frame size valid for this mode at the API rate (48k).
			fsList := []int{480, 960}
			if mode == ModeCELT {
				fsList = []int{240, 480, 960}
			}
			for _, fs := range fsList {
				streamPackets := make([][]byte, lo.streams)
				ok := true
				for s := 0; s < lo.streams; s++ {
					pkt := streamPacket(lo.streamChans[s], fs, mode)
					// The mode actually produced must match (the encoder may pick a
					// different mode for some channel/bitrate combinations); skip if not.
					if len(pkt) == 0 || ParseTOC(pkt[0]).Mode != mode || (pkt[0]&0x03) != 0 {
						ok = false
						break
					}
					streamPackets[s] = pkt
				}
				if !ok {
					continue
				}
				if msPkt := msRobustBuildPacket(streamPackets); msPkt != nil {
					seeds = append(seeds, msRobustSeed{layout: lo, packet: msPkt})
				}
			}
		}
	}
	return seeds
}

// msRobustMutate applies one seeded structured mutation to a copy of a valid
// multistream packet, biased to hit BOTH the cross-stream framing header region
// (front) and the trailing sub-packet bytes.
func msRobustMutate(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	switch rng.Intn(9) {
	case 0: // truncate to a random shorter length (incl. 0) — sub-packet boundary cut
		return p[:rng.Intn(len(p)+1)]
	case 1: // single random byte overwrite
		p[rng.Intn(len(p))] = byte(rng.Intn(256))
		return p
	case 2: // single bit flip
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // rewrite the FIRST stream's TOC config (mode/bw/frame-size)
		p[0] = (p[0] & 0x07) | byte(rng.Intn(32))<<3
		return p
	case 4: // flip the FIRST stream's TOC code bits (per-stream framing)
		p[0] = (p[0] & 0xFC) | byte(rng.Intn(4))
		return p
	case 5: // flip the FIRST stream's stereo bit (coupling/channel-count mismatch)
		p[0] ^= 0x04
		return p
	case 6: // corrupt a self-delimited sub-packet length byte (header region)
		if len(p) >= 2 {
			i := 1 + rng.Intn(min(6, len(p)-1))
			p[i] = byte(rng.Intn(256))
		}
		return p
	case 7: // append junk bytes (last-stream overrun / trailing data)
		extra := 1 + rng.Intn(8)
		for range extra {
			p = append(p, byte(rng.Intn(256)))
		}
		return p
	default: // scribble a short run anywhere (multi-byte corruption)
		n := 1 + rng.Intn(4)
		for range n {
			p[rng.Intn(len(p))] = byte(rng.Intn(256))
		}
		return p
	}
}

// msRobustFormat selects which gopus + oracle decode path a case exercises.
type msRobustFormat int

const (
	msRobustFloat32 msRobustFormat = iota
	msRobustInt16
	msRobustInt24
)

func msRobustSampleBytes(format msRobustFormat) int {
	if format == msRobustInt16 {
		return 2
	}
	return 4
}

// msRobustGopusDecode decodes one packet through a fresh gopus MultistreamDecoder
// in the selected format, recovering a panic into an error so a crash minimises
// to one packet. PCM is serialized in the public format's original width when requested.
func msRobustGopusDecode(layout msRobustLayout, format msRobustFormat, packet []byte, frameSize int, capturePCM bool) (pcm []byte, samples int, finalRange uint32, err error) {
	defer func() {
		if r := recover(); r != nil {
			finalRange = 0
			err = fmt.Errorf("PANIC in gopus multistream decode: %v", r)
		}
	}()
	dec, derr := NewMultistreamDecoder(48000, layout.channels, layout.streams, layout.coupled, layout.mapping)
	if derr != nil {
		return nil, 0, 0, derr
	}
	bufCap := frameSize * layout.channels
	switch format {
	case msRobustInt16:
		buf := make([]int16, bufCap)
		n, e := dec.DecodeInt16(packet, buf)
		finalRange = dec.FinalRange()
		if e != nil {
			return nil, 0, finalRange, e
		}
		decoded := buf[:n*layout.channels]
		if capturePCM {
			pcm = make([]byte, 2*len(decoded))
			for i, v := range decoded {
				binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
			}
		}
		return pcm, n, finalRange, nil
	case msRobustInt24:
		buf := make([]int32, bufCap)
		n, e := dec.DecodeInt24(packet, buf)
		finalRange = dec.FinalRange()
		if e != nil {
			return nil, 0, finalRange, e
		}
		decoded := buf[:n*layout.channels]
		if capturePCM {
			pcm = make([]byte, 4*len(decoded))
			for i, v := range decoded {
				binary.LittleEndian.PutUint32(pcm[4*i:], uint32(v))
			}
		}
		return pcm, n, finalRange, nil
	default:
		buf := make([]float32, bufCap)
		n, e := dec.Decode(packet, buf)
		finalRange = dec.FinalRange()
		if e != nil {
			return nil, 0, finalRange, e
		}
		decoded := buf[:n*layout.channels]
		if capturePCM {
			pcm = make([]byte, 4*len(decoded))
			for i, v := range decoded {
				binary.LittleEndian.PutUint32(pcm[4*i:], math.Float32bits(v))
			}
		}
		return pcm, n, finalRange, nil
	}
}

// msRobustOracleDecode decodes ONE packet through the libopus multistream oracle
// and returns the per-channel sample count and raw-format PCM. The helper prints
// its negative decoder return code before exiting; unrelated errors remain errors.
func msRobustOracleDecode(layout msRobustLayout, format msRobustFormat, packet []byte, frameSize int) (pcm []byte, samples int, rejected bool, err error) {
	switch format {
	case msRobustInt16:
		out, e := decodeLibopusMultistreamInt16Gain(48000, layout.channels, layout.streams, layout.coupled, frameSize, 0, layout.mapping, [][]byte{packet})
		if e != nil {
			return nil, 0, msRobustOracleRejected(e), msRobustOracleError(e)
		}
		f := make([]byte, 2*len(out))
		for i, v := range out {
			binary.LittleEndian.PutUint16(f[2*i:], uint16(v))
		}
		return f, len(out) / layout.channels, false, nil
	case msRobustInt24:
		out, e := decodeLibopusMultistreamInt24(48000, layout.channels, layout.streams, layout.coupled, frameSize, layout.mapping, [][]byte{packet})
		if e != nil {
			return nil, 0, msRobustOracleRejected(e), msRobustOracleError(e)
		}
		f := make([]byte, 4*len(out))
		for i, v := range out {
			binary.LittleEndian.PutUint32(f[4*i:], uint32(v))
		}
		return f, len(out) / layout.channels, false, nil
	default:
		out, e := decodeLibopusMultistreamFloat32(48000, layout.channels, layout.streams, layout.coupled, frameSize, layout.mapping, [][]byte{packet})
		if e != nil {
			return nil, 0, msRobustOracleRejected(e), msRobustOracleError(e)
		}
		f := make([]byte, 4*len(out))
		for i, v := range out {
			binary.LittleEndian.PutUint32(f[4*i:], math.Float32bits(v))
		}
		return f, len(out) / layout.channels, false, nil
	}
}

func msRobustOracleRejected(err error) bool {
	// libopus_refdecode_multistream.c reports only negative decode return codes
	// with this diagnostic and exit code 1. Build, launch, and protocol failures
	// do not carry both the typed child exit and the complete decoder marker.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	const marker = "(opus_multistream_decode_float failed: "
	message := err.Error()
	start := strings.LastIndex(message, marker)
	if start < 0 || !strings.HasSuffix(message, ")") {
		return false
	}
	code, parseErr := strconv.ParseInt(strings.TrimSuffix(message[start+len(marker):], ")"), 10, 32)
	return parseErr == nil && code < 0
}

func msRobustOracleError(err error) error {
	if msRobustOracleRejected(err) {
		return nil
	}
	return err
}

func TestMSRobustOracleErrorClassification(t *testing.T) {
	libopustest.RequireOracle(t)
	layout := msRobustLayouts()[0]
	_, _, rejected, err := msRobustOracleDecode(layout, msRobustInt16, []byte{0xff}, 960)
	if !rejected || err != nil {
		t.Fatalf("invalid packet: rejected=%v error=%v, want decoder rejection", rejected, err)
	}
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"build", fmt.Errorf("compile multistream reference: compiler failed")},
		{"process", fmt.Errorf("run helper: exit status 1 (failed to read packet payload)")},
		{"truncated wire", fmt.Errorf("multistream helper: truncated output header")},
		{"truncated decode marker", fmt.Errorf("run helper: exit status 1 (opus_multistream_decode_float failed: -4")},
		{"forged decode marker", fmt.Errorf("run helper: exit status 1 (opus_multistream_decode_float failed: -4)")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if msRobustOracleRejected(tc.err) || msRobustOracleError(tc.err) == nil {
				t.Fatalf("infrastructure error classified as decoder rejection: %v", tc.err)
			}
		})
	}
}

// The appended bytes form a SILK-to-CELT redundancy payload. libopus captures
// the integer SILK body before fading the CELT tail into its opus_res output.
func TestDecodeMultistreamMalformedSILKRedundancyParity(t *testing.T) {
	libopustest.RequireOracle(t)
	layout := msRobustLayouts()[0]
	packet := []byte{
		0x40, 0x82, 0x2e, 0x68, 0x51, 0x73, 0xfb, 0x43,
		0x3c, 0xec, 0xdf, 0xa7, 0xe6, 0xca, 0xd8, 0xbc,
		0xa7, 0xa4, 0x7a, 0x58, 0x4b, 0x92, 0x89, 0x7d,
		0x80, 0x1c, 0x65, 0xfc, 0x40, 0xaf, 0xa6, 0x1d,
		0x51, 0x63, 0x52, 0x57, 0x7a,
	}
	for _, format := range []msRobustFormat{msRobustFloat32, msRobustInt16, msRobustInt24} {
		got, gotN, _, gotErr := msRobustGopusDecode(layout, format, packet, 960, true)
		want, wantN, rejected, oracleErr := msRobustOracleDecode(layout, format, packet, 960)
		if oracleErr != nil {
			t.Fatalf("format %d selected C oracle: %v", format, oracleErr)
		}
		if rejected || gotErr != nil || gotN != wantN || !bytes.Equal(got, want) {
			t.Fatalf("format %d: rejected=%v Go=(n=%d, err=%v, bytes=%x) C=(n=%d, bytes=%x)",
				format, rejected, gotN, gotErr, got, wantN, want)
		}
	}
}

// TestDecodeMultistreamRobustnessRandom feeds purely random byte buffers of every
// length 0..N to MultistreamDecoder.Decode / DecodeInt16 / DecodeInt24 across the
// layouts and asserts the wrapper NEVER panics and never reports a sample count
// outside the buffer it was given. No oracle is consulted: an arbitrary buffer is
// virtually never a valid multistream packet, so this stage is a pure crash/abort
// guard over the structural parser and routing.
func TestDecodeMultistreamRobustnessRandom(t *testing.T) {
	layouts := msRobustLayouts()
	const maxLen = 200
	const frameSize = 5760
	rng := rand.New(rand.NewSource(0x115EA0))

	iters := diffFuzzBudget(6000)
	cases := 0
	buf := make([]byte, maxLen)
	for range iters {
		lo := layouts[rng.Intn(len(layouts))]
		n := rng.Intn(maxLen + 1)
		buf = buf[:n]
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		for _, format := range []msRobustFormat{msRobustFloat32, msRobustInt16, msRobustInt24} {
			pcm, samples, _, err := msRobustGopusDecode(lo, format, buf, frameSize, true)
			cases++
			if err != nil && isMSRobustPanic(err) {
				t.Fatalf("%s/fmt%d: %v — packet=% x", lo.name, format, err, buf)
			}
			if err == nil {
				if samples < 0 || samples > frameSize {
					t.Fatalf("%s/fmt%d: samples=%d outside [0,%d] — packet=% x", lo.name, format, samples, frameSize, buf)
				}
				if len(pcm) != samples*lo.channels*msRobustSampleBytes(format) {
					t.Fatalf("%s/fmt%d: pcm bytes=%d want %d — packet=% x", lo.name, format, len(pcm), samples*lo.channels*msRobustSampleBytes(format), buf)
				}
				msRobustRequireFinite(t, lo.name, format, pcm, buf)
			}
		}

		// PLC path: nil packet must also never panic.
		for _, format := range []msRobustFormat{msRobustFloat32, msRobustInt16, msRobustInt24} {
			_, _, _, decodeErr := msRobustGopusDecode(lo, format, nil, frameSize, false)
			if decodeErr != nil && isMSRobustPanic(decodeErr) {
				t.Fatalf("%s/fmt%d PLC(nil): %v", lo.name, format, decodeErr)
			}
		}
	}
	t.Logf("multistream random no-panic sweep: %d cases over %d layouts", cases, len(layouts))
}

// TestDecodeMultistreamRobustnessMalformed mutates valid multistream packets and
// asserts gopus and the libopus multistream oracle agree on accept-vs-reject and
// per-channel sample count, with NO panic. Accepted PCM must match in the
// selected public output format, including every int24 integer bit.
func TestDecodeMultistreamRobustnessMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := decodeLibopusMultistreamFloat32(48000, 1, 1, 0, 960, []byte{0}, [][]byte{minimalCELTProbePacket(t)}); err != nil {
		libopustest.HelperUnavailable(t, "multistream reference decode", err)
	}

	seeds := msRobustSeedPackets(t)
	if len(seeds) == 0 {
		t.Skip("no multistream seed packets")
	}

	const frameSize = 5760
	iters := diffFuzzBudget(9000)
	rng := rand.New(rand.NewSource(0x115EAB0F))

	formats := []msRobustFormat{msRobustFloat32, msRobustInt16, msRobustInt24}
	total := 0
	pcmDiverged := 0
	const batchSize = 128
	for base := 0; base < iters; base += batchSize {
		count := min(batchSize, iters-base)
		type malformedCase struct {
			layout msRobustLayout
			format msRobustFormat
			packet []byte
			label  string
			gpcm   []byte
			gn     int
			grange uint32
			gerr   error
		}
		cases := make([]malformedCase, count)
		oracleCases := make([]libopustest.MultistreamDecodeCase, count)
		for i := range cases {
			k := base + i
			seed := seeds[rng.Intn(len(seeds))]
			m := msRobustMutate(rng, seed.packet)
			format := formats[rng.Intn(len(formats))]
			label := fmt.Sprintf("%s/fmt%d/mut%d", seed.layout.name, format, k)

			gpcm, gn, grange, gerr := msRobustGopusDecode(seed.layout, format, m, frameSize, true)
			if gerr != nil && isMSRobustPanic(gerr) {
				t.Fatalf("%s: %v — packet=% x", label, gerr, m)
			}
			cases[i] = malformedCase{seed.layout, format, m, label, gpcm, gn, grange, gerr}
			oracleCases[i] = libopustest.MultistreamDecodeCase{
				SampleRate: 48000,
				Format:     msRobustOracleFormat(format),
				Family:     0,
				Channels:   uint32(seed.layout.channels),
				Streams:    uint32(seed.layout.streams),
				Coupled:    uint32(seed.layout.coupled),
				FrameSize:  uint32(frameSize),
				Mapping:    seed.layout.mapping,
				Packet:     m,
			}
		}

		oracleResults, oerr := libopustest.ProbeMultistreamDecodeFresh(oracleCases)
		if oerr != nil {
			libopustest.HelperUnavailable(t, "multistream reference decode", oerr)
			return
		}
		if len(oracleResults) != len(cases) {
			t.Fatalf("multistream reference decode returned %d results for %d cases", len(oracleResults), len(cases))
		}
		for i, tc := range cases {
			oracle := oracleResults[i]
			gpcm, gn, grange, gerr, opcm, on := tc.gpcm, tc.gn, tc.grange, tc.gerr, oracle.PCM, int(oracle.Code)
			rejected := oracle.Code < 0
			total++

			// ---- accept/reject parity (HARD) ----
			if rejected {
				if gerr == nil {
					t.Errorf("%s: libopus REJECTED but gopus ACCEPTED (n=%d) — packet=% x", tc.label, gn, tc.packet)
				}
				continue
			}
			if gerr != nil {
				t.Errorf("%s: libopus ACCEPTED (n=%d) but gopus REJECTED: %v — packet=% x", tc.label, on, gerr, tc.packet)
				continue
			}
			if gn != on {
				t.Errorf("%s: sample count gopus=%d libopus=%d — packet=% x", tc.label, gn, on, tc.packet)
				continue
			}
			if grange != oracle.FinalRange {
				t.Errorf("%s: final range gopus=%08x libopus=%08x (samples Go=%d C code=%d frameSize=%d) — packet=% x",
					tc.label, grange, oracle.FinalRange, gn, oracle.Code, frameSize, tc.packet)
			}

			// ---- exact same-format PCM on accepted corrupt input ----
			if !bytes.Equal(gpcm, opcm) {
				pcmDiverged++
				first := 0
				for first < min(len(gpcm), len(opcm)) && gpcm[first] == opcm[first] {
					first++
				}
				end := min(first+msRobustSampleBytes(tc.format), min(len(gpcm), len(opcm)))
				t.Errorf("%s: PCM bytes differ at %d (sample=%d, Go=%x C=%x, Go len=%d C len=%d), accepted packet=% x",
					tc.label, first, first/msRobustSampleBytes(tc.format), gpcm[first:end], opcm[first:end], len(gpcm), len(opcm), tc.packet)
			}
		}
	}
	t.Logf("multistream malformed sweep: %d cases, %d exact PCM divergence(s)", total, pcmDiverged)
}

func msRobustOracleFormat(format msRobustFormat) uint32 {
	switch format {
	case msRobustInt16:
		return libopustest.MultistreamDecodeFormatInt16
	case msRobustInt24:
		return libopustest.MultistreamDecodeFormatInt24
	default:
		return libopustest.MultistreamDecodeFormatFloat32
	}
}

// isMSRobustPanic reports whether an error came from a recovered panic (the
// hard-fail signal) rather than an ordinary decode rejection.
func isMSRobustPanic(err error) bool {
	return err != nil && len(err.Error()) >= 5 && err.Error()[:5] == "PANIC"
}

// msRobustRequireFinite asserts decoded float PCM is finite (no NaN/Inf) — a
// decoder must never emit non-finite samples even on garbage input.
func msRobustRequireFinite(t *testing.T, name string, format msRobustFormat, pcm []byte, packet []byte) {
	t.Helper()
	if format != msRobustFloat32 {
		return
	}
	for i := 0; i < len(pcm)/4; i++ {
		v := math.Float32frombits(binary.LittleEndian.Uint32(pcm[4*i:]))
		if v != v || v > 3.4e38 || v < -3.4e38 { // NaN or |x|>~FLT_MAX
			t.Fatalf("%s/fmt%d: sample[%d]=%v not finite — packet=% x", name, format, i, v, packet)
		}
	}
}

// minimalCELTProbePacket returns a tiny valid mono CELT packet used only to
// confirm the multistream oracle binary is available before the malformed sweep.
func minimalCELTProbePacket(t *testing.T) []byte {
	t.Helper()
	return encodeAPIRateCELTPacketFrameSize(t, 1, 960)
}
