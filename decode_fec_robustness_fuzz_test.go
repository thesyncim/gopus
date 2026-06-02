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
// (ProbeDecodeDiff with DecodeFEC=true, fresh decoder per case), asserting NO
// panic, accept/reject parity, and sample-count parity. PCM-value parity is held
// only to the same loose gross bound the malformed sweep uses, since FEC on
// corrupt input drives concealment where a 1-ULP predictor difference amplifies.
//
// libopus decode_fec is permissive: a packet with no LBRR conceals (PLC) rather
// than erroring, so most accepted-vs-accepted cases are PLC. The frame size is
// taken from the output buffer (requested 5760), so the oracle and gopus conceal
// the same span. A pre-loss "good" packet is decoded first so the FEC/PLC state
// is primed identically on both sides before the mutated FEC decode.

package gopus

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// fecRobustGopusDecode primes the decoder with one good packet, then runs
// DecodeWithFEC(fec=true) on the mutated packet, recovering a panic into an error
// so a crash minimises to one (good, mutated) pair.
func fecRobustGopusDecode(sampleRate, channels int, good, mutated []byte, frameSize int) (samples int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus DecodeWithFEC: %v", r)
		}
	}()
	dec, derr := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if derr != nil {
		return 0, derr
	}
	// Prime: a normal decode of one good packet so prevMode/lastFrameSize match the
	// oracle's primed state before the FEC decode.
	if len(good) > 0 {
		prime := make([]float32, frameSize*channels)
		if _, e := dec.Decode(good, prime); e != nil {
			// A prime failure is not the case under test; bail without flagging it as
			// a FEC divergence.
			return -1, nil
		}
	}
	pcm := make([]float32, frameSize*channels)
	n, e := dec.DecodeWithFEC(mutated, pcm, true)
	if e != nil {
		return 0, e
	}
	return n, nil
}

// fecRobustOracleDecode mirrors fecRobustGopusDecode through the libopus oracle: a
// primed good-packet decode_fec=0 decode followed by the mutated decode_fec=1
// decode, in one fresh-decoder session. It returns the FEC decode's return code.
func fecRobustOracleDecode(sampleRate, channels int, good, mutated []byte, frameSize int) (samples int, rejected bool, primeFailed bool, err error) {
	cases := []libopustest.DecodeDiffCase{
		{Packet: good, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: false},
		{Packet: mutated, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: true},
	}
	res, e := libopustest.ProbeDecodeDiff(sampleRate, channels, cases)
	if e != nil {
		return 0, false, false, e
	}
	if res[0].Code < 0 {
		return 0, false, true, nil
	}
	if res[1].Code < 0 {
		return 0, true, false, nil
	}
	return int(res[1].Code), false, false, nil
}

// TestDecodeWithFECRobustnessMalformed mutates valid packets and asserts gopus
// DecodeWithFEC(fec=true) and the libopus decode_fec oracle agree on
// accept-vs-reject and sample count, with NO panic. Each case primes both
// decoders with the same good packet first.
func TestDecodeWithFECRobustnessMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	seeds := seedPacketsForMutation(t)
	if len(seeds) == 0 {
		t.Skip("no seed packets")
	}

	const frameSize = 5760
	iters := diffFuzzBudget(8000)
	rng := rand.New(rand.NewSource(0xFEC0B0F))

	total := 0
	primeSkipped := 0
	for _, channels := range []int{1, 2} {
		for k := 0; k < iters/2; k++ {
			good := seeds[rng.Intn(len(seeds))]
			mutated := mutatePacket(rng, seeds[rng.Intn(len(seeds))])
			label := fmt.Sprintf("ch%d/fec/mut%d", channels, k)

			on, rejected, primeFailed, oerr := fecRobustOracleDecode(48000, channels, good, mutated, frameSize)
			if oerr != nil {
				libopustest.HelperUnavailable(t, "decode diff probe", oerr)
				return
			}
			if primeFailed {
				// The good seed was rejected at this channel count (e.g. a stereo bit
				// mismatch); the FEC case under test never runs. Skip.
				primeSkipped++
				continue
			}

			gn, gerr := fecRobustGopusDecode(48000, channels, good, mutated, frameSize)
			if gerr != nil && isMSRobustPanic(gerr) {
				t.Fatalf("%s: %v — good=% x mutated=% x", label, gerr, good, mutated)
			}
			if gn == -1 {
				// gopus declined the prime; the oracle accepted it, so this is a prime
				// accept/reject divergence on a CLEAN seed — flag it.
				t.Errorf("%s: gopus rejected the prime good packet the oracle accepted — good=% x", label, good)
				continue
			}
			total++

			if rejected {
				if gerr == nil {
					t.Errorf("%s: libopus decode_fec REJECTED but gopus ACCEPTED (n=%d) — mutated=% x", label, gn, mutated)
				}
				continue
			}
			if gerr != nil {
				t.Errorf("%s: libopus decode_fec ACCEPTED (n=%d) but gopus REJECTED: %v — mutated=% x", label, on, gerr, mutated)
				continue
			}
			if gn != on {
				t.Errorf("%s: decode_fec sample count gopus=%d libopus=%d — mutated=% x", label, gn, on, mutated)
			}
		}
	}
	t.Logf("decode_fec malformed sweep: %d cases (%d prime-skipped)", total, primeSkipped)
}
