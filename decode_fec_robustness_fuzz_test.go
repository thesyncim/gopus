// decode_fec_robustness_fuzz_test.go — DECODE ROBUSTNESS fuzz for the FEC decode
// entry point Decoder.DecodeWithFEC(data, pcm, fec=true), the libopus decode_fec
// path. It is the FEC sibling of decode_differential_malformed_fuzz_test.go.
//
// decode_fec has its own surface arbitrary input must not crash and must
// accept/reject in lockstep with libopus opus_decode(..., decode_fec=1):
//   - the in-band LBRR (SILK/Hybrid) presence probe + LBRR frame decode,
//   - the no-LBRR PLC fallback, and
//   - the "requested size exceeds the packet frame size" prefix-PLC split.
//
// Strategy (seeded): structured-malformed mutations of valid packets are decoded
// through gopus DecodeWithFEC(fec=true) AND the libopus decode_fec oracle
// (ProbeDecodeSequence with DecodeFEC=true after the clean prime), asserting NO
// panic, accept/reject parity, sample-count parity, exact PCM, and final range
// for accepted prime and FEC decodes.
//
// libopus decode_fec is permissive: a packet with no LBRR conceals (PLC) rather
// than erroring, so most accepted-vs-accepted cases are PLC. The frame size is
// taken from the output buffer (requested 5760), so the oracle and gopus conceal
// the same span. A pre-loss "good" packet is decoded first so the FEC/PLC state
// is primed identically on both sides before the mutated FEC decode.

package gopus

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// fecRobustDecodeResult holds one step from the FEC robustness sequence.
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
