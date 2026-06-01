//go:build gopus_dred

// dred_decode_differential_malformed_fuzz_test.go — the DRED analogue of the
// generic decode_differential_malformed_fuzz: it takes valid DRED-carrying Opus
// packets (emitted by the libopus DRED encoder) and synthetic DRED-extension
// packets, applies seeded structured mutations targeting the DRED extension blob
// (truncation, latent/quantizer-byte corruption, header-region corruption,
// extension-ID flips, DRED-duration sweeps), then parses each mutant through
// BOTH the gopus standalone DRED parser (DREDDecoder.Parse) and the libopus
// opus_dred_parse oracle, asserting:
//
//   - accept-vs-reject parity (libopus ret<0 <=> gopus Parse error), HARD;
//   - availableSamples + dredEnd parity when both accept, HARD;
//   - on accept, byte-identical nbLatents / dred_offset / state / latents vs the
//     libopus opus_dred_parse(defer=0)+decode oracle, HARD;
//   - NO PANIC in the gopus parser on any mutant, HARD.
//
// libopus opus_dred_parse (src/opus_decoder.c:1548) returns negative only when
// opus_packet_parse_impl rejects the packet structure; once a DRED payload is
// found dred_ec_decode never fails — it decodes whatever latents the range coder
// yields (garbage included) and returns max(0, nb_latents*…). The gopus parser
// must mirror that: it may not reject a structurally-valid packet whose DRED
// payload is corrupt, and it must produce the same latent metadata. Every case
// is a single self-contained packet parsed through a fresh DREDDecoder, so a
// failure is already minimal (one packet, printed as hex).

package gopus

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// dredFuzzSeed pairs a DRED-carrying packet with the DRED-request parameters the
// differential parse is exercised under.
type dredFuzzSeed struct {
	packet         []byte
	maxDREDSamples int
	sampleRate     int
}

// seedPacketsForDREDMutation builds the bank of valid DRED-carrying packets the
// mutator perturbs: real libopus-emitted DRED packets across modes / frame sizes
// / channels, plus synthetic DRED-extension packets (single + double extension,
// short payload) so the mutator also reaches hand-built extension framings.
func seedPacketsForDREDMutation(t *testing.T) []dredFuzzSeed {
	t.Helper()
	var seeds []dredFuzzSeed

	configs := []libopusDREDPacketConfig{
		{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		{FrameSize: 480, ForceMode: ModeCELT, Bandwidth: BandwidthFullband},
		{FrameSize: 960, ForceMode: ModeCELT, Bandwidth: BandwidthFullband, ForceChannels: 2, Channels: 2},
		{FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband},
		{FrameSize: 960, ForceMode: ModeHybrid, Bandwidth: BandwidthSuperwideband},
	}
	for _, cfg := range configs {
		info, err := emitLibopusDREDPacketWithConfig(cfg)
		if err != nil {
			// The emit helper requires the DRED-capable oracle tree; skip the
			// whole gate if it is unavailable rather than failing on infra.
			libopustest.HelperUnavailable(t, "dred emit packet", err)
		}
		// Sweep a few DRED-duration requests per packet so the mutator also
		// covers the request-bounded latent count (min_feature_frames) path.
		for _, maxDRED := range []int{120, 480, 960, info.maxDREDSamples} {
			if maxDRED <= 0 {
				continue
			}
			seeds = append(seeds, dredFuzzSeed{
				packet:         append([]byte(nil), info.packet...),
				maxDREDSamples: maxDRED,
				sampleRate:     48000,
			})
		}
	}

	// Synthetic DRED-extension packets (hand-built framings the encoder never
	// emits): single valid extension, invalid-first/valid-second, short body.
	base := makeValidCELTPacketForDREDTest(t)
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	synth := [][]packetExtensionData{
		{{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)}},
		{
			{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'X', internaldred.ExperimentalVersion, 0x10}},
			{ID: internaldred.ExtensionID, Frame: 0, Data: append([]byte{'D', internaldred.ExperimentalVersion}, body...)},
		},
		{{ID: internaldred.ExtensionID, Frame: 0, Data: []byte{'D', internaldred.ExperimentalVersion, 0xaa, 0xbb}}},
	}
	for _, exts := range synth {
		pkt := buildSingleFramePacketWithExtensionsForDREDTest(t, base, exts)
		for _, maxDRED := range []int{480, 960} {
			seeds = append(seeds, dredFuzzSeed{
				packet:         append([]byte(nil), pkt...),
				maxDREDSamples: maxDRED,
				sampleRate:     48000,
			})
		}
	}
	return seeds
}

// mutateDREDPacket applies one seeded structured mutation to a copy of src,
// biased toward the DRED extension region (the back of the packet, where padding
// + extensions live) so corruption lands on the DRED blob rather than the audio
// frame headers (which the generic malformed fuzzer already covers).
func mutateDREDPacket(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	// The DRED extension lives in the packet padding region near the tail; bias
	// mutations into the back half so they perturb the DRED blob.
	tailStart := len(p) / 2
	tailLen := len(p) - tailStart
	switch rng.Intn(9) {
	case 0: // truncate the DRED extension / payload at a tail byte boundary
		if tailLen <= 0 {
			return p
		}
		return p[:tailStart+rng.Intn(tailLen)]
	case 1: // overwrite a single tail byte (latent / quantizer corruption)
		if tailLen <= 0 {
			return p
		}
		p[tailStart+rng.Intn(tailLen)] = byte(rng.Intn(256))
		return p
	case 2: // flip one bit anywhere (whole-packet bit flip)
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // scribble a short run of tail bytes (multi-byte latent corruption)
		if tailLen <= 0 {
			return p
		}
		n := 1 + rng.Intn(4)
		for k := 0; k < n; k++ {
			p[tailStart+rng.Intn(tailLen)] = byte(rng.Intn(256))
		}
		return p
	case 4: // corrupt the DRED experimental prefix ('D'/version) — flip the ID
		// Hunt for a 'D'+version pair in the tail and rewrite it.
		for i := tailStart; i+1 < len(p); i++ {
			if p[i] == 'D' && p[i+1] == byte(internaldred.ExperimentalVersion) {
				if rng.Intn(2) == 0 {
					p[i] = byte(rng.Intn(256)) // flip the 'D' marker
				} else {
					p[i+1] = byte(rng.Intn(256)) // flip the version
				}
				return p
			}
		}
		// No prefix found: fall back to a tail-byte overwrite.
		if tailLen > 0 {
			p[tailStart+rng.Intn(tailLen)] = byte(rng.Intn(256))
		}
		return p
	case 5: // corrupt an early payload byte (DRED header q0/dq/offset region)
		if tailLen <= 0 {
			return p
		}
		// First few bytes after the prefix carry the entropy-coded header.
		i := tailStart + rng.Intn(min(4, tailLen))
		p[i] = byte(rng.Intn(256))
		return p
	case 6: // append junk (padding-length / extension-overrun ambiguity)
		n := 1 + rng.Intn(8)
		for k := 0; k < n; k++ {
			p = append(p, byte(rng.Intn(256)))
		}
		return p
	case 7: // truncate the whole packet at a random shorter length
		return p[:rng.Intn(len(p)+1)]
	default: // overwrite a single early byte (TOC / padding-count region)
		p[rng.Intn(min(3, len(p)))] = byte(rng.Intn(256))
		return p
	}
}

// newFuzzDREDDecoder builds a gopus standalone DRED decoder with a valid (test)
// DNN blob loaded. DRED parse defers all model-heavy work, so the synthetic test
// blob is sufficient for the parse-level metadata the differential gate compares
// (the existing TestStandaloneDREDParseMatchesLibopus relies on the same blob).
func newFuzzDREDDecoder(t *testing.T) *DREDDecoder {
	t.Helper()
	dec := NewDREDDecoder()
	if err := dec.SetDNNBlob(makeValidDREDDecoderTestDNNBlob()); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	return dec
}

// safeDREDParse parses one packet through a fresh-state gopus standalone DRED
// parser, converting a panic into an error so a crash on a malformed DRED blob is
// reported as a divergence (and the case printed) rather than aborting the sweep.
func safeDREDParse(dec *DREDDecoder, dst *DRED, packet []byte, maxDREDSamples, sampleRate int) (available, dredEnd int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus DRED parse: %v", r)
		}
	}()
	return dec.Parse(dst, packet, maxDREDSamples, sampleRate, true)
}

// TestDecodeDREDDifferentialMalformed mutates valid DRED-carrying packets and
// asserts the gopus standalone DRED parser agrees with the libopus opus_dred_parse
// oracle on accept-vs-reject, on availableSamples/dredEnd when both accept, and on
// the decoded latent metadata (nbLatents/dred_offset/state/latents) — with NO
// PANIC on any mutant.
func TestDecodeDREDDifferentialMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := getLibopusDREDParseHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "dred parse probe", err)
	}

	seeds := seedPacketsForDREDMutation(t)
	if len(seeds) == 0 {
		t.Skip("no DRED seed packets")
	}

	dec := newFuzzDREDDecoder(t)
	dst := NewDRED()

	iters := diffFuzzBudget(2000)
	rng := rand.New(rand.NewSource(0xD9ED))

	var (
		accepted     int
		rejected     int
		latentParity int
	)
	for k := 0; k < iters; k++ {
		seed := seeds[rng.Intn(len(seeds))]
		m := mutateDREDPacket(rng, seed.packet)
		label := fmt.Sprintf("mut%d/maxDRED%d", k, seed.maxDREDSamples)

		gAvail, gEnd, gErr := safeDREDParse(dec, dst, m, seed.maxDREDSamples, seed.sampleRate)
		// A panic surfaces as a non-nil error whose message starts with PANIC.
		if gErr != nil && len(gErr.Error()) >= 5 && gErr.Error()[:5] == "PANIC" {
			t.Fatalf("%s: %v — packet=% x", label, gErr, m)
		}

		oracle, err := probeLibopusDREDParse(m, seed.maxDREDSamples, seed.sampleRate)
		if err != nil {
			libopustest.HelperUnavailable(t, "dred parse probe", err)
			return
		}

		// ---- accept/reject parity (HARD) ----
		if oracle.availableSamples < 0 {
			rejected++
			if gErr == nil {
				t.Errorf("%s: libopus REJECTED (ret=%d) but gopus ACCEPTED (avail=%d end=%d) — packet=% x",
					label, oracle.availableSamples, gAvail, gEnd, m)
			}
			continue
		}
		if gErr != nil {
			t.Errorf("%s: libopus ACCEPTED (ret=%d end=%d) but gopus REJECTED: %v — packet=% x",
				label, oracle.availableSamples, oracle.dredEndSamples, gErr, m)
			continue
		}
		accepted++

		// ---- availableSamples + dredEnd parity (HARD) ----
		if gAvail != oracle.availableSamples {
			t.Errorf("%s: availableSamples gopus=%d libopus=%d — packet=% x", label, gAvail, oracle.availableSamples, m)
			continue
		}
		if gEnd != oracle.dredEndSamples {
			t.Errorf("%s: dredEnd gopus=%d libopus=%d — packet=% x", label, gEnd, oracle.dredEndSamples, m)
			continue
		}

		// ---- decoded latent metadata parity (HARD) ----
		// opus_dred_parse with a DRED payload always returns >=0; the deeper
		// nbLatents/dred_offset/state/latents come from the decode oracle, which
		// runs the same parse+ec_decode. Compare them on every accepted DRED
		// payload (avail>0 implies a payload was found and decoded).
		if oracle.availableSamples == 0 && gAvail == 0 {
			// No DRED payload (or zero-length availability): nothing more to
			// compare; the accept/reject + dredEnd parity above already held.
			continue
		}
		decodeWant, err := probeLibopusDREDDecode(m, seed.maxDREDSamples, seed.sampleRate)
		if err != nil {
			libopustest.HelperUnavailable(t, "dred decode probe", err)
			return
		}
		if decodeWant.availableSamples < 0 {
			t.Errorf("%s: parse accepted but decode oracle rejected (ret=%d) — packet=% x", label, decodeWant.availableSamples, m)
			continue
		}
		if got := dst.LatentCount(); got != decodeWant.nbLatents {
			t.Errorf("%s: LatentCount gopus=%d libopus=%d — packet=% x", label, got, decodeWant.nbLatents, m)
			continue
		}
		if got := dst.Parsed().Header.DredOffset; got != int32(decodeWant.dredOffset) {
			t.Errorf("%s: DredOffset gopus=%d libopus=%d — packet=% x", label, got, decodeWant.dredOffset, m)
			continue
		}
		state := make([]float32, internaldred.StateDim)
		if n := dst.FillState(state); n != internaldred.StateDim {
			t.Errorf("%s: FillState count=%d want %d — packet=% x", label, n, internaldred.StateDim, m)
			continue
		}
		if !float32SlicesBitsEqual(state, decodeWant.state[:]) {
			t.Errorf("%s: DRED state diverged — packet=% x", label, m)
			continue
		}
		wantLatents := decodeWant.nbLatents * internaldred.LatentStride
		latents := make([]float32, internaldred.MaxLatents*internaldred.LatentStride)
		if n := dst.FillLatents(latents); n != wantLatents {
			t.Errorf("%s: FillLatents count=%d want %d — packet=% x", label, n, wantLatents, m)
			continue
		}
		if !float32SlicesBitsEqual(latents[:wantLatents], decodeWant.latents) {
			t.Errorf("%s: DRED latents diverged — packet=% x", label, m)
			continue
		}
		latentParity++
	}
	t.Logf("DRED malformed sweep: %d mutants (%d accepted, %d rejected), %d full latent-parity checks",
		iters, accepted, rejected, latentParity)
}

// float32SlicesBitsEqual reports whether two float32 slices are bit-identical
// (including NaN bit patterns), the parity bar for DRED decoded state/latents.
func float32SlicesBitsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}
