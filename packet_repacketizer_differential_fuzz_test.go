package gopus

// packet_repacketizer_differential_fuzz_test.go — a SEEDED differential fuzz
// harness for the repacketizer and the opus_packet_* utility functions vs the
// libopus C oracle, on random VALID and MALFORMED packet sequences.
//
// The enumerated parity tests (packet_toc_edge_libopus_parity_test.go,
// packet_repacketizer_libopus_parity_test.go, packet_duration_libopus_test.go)
// cover a hand-picked grid. This harness instead fires thousands of randomized,
// reproducible cases at the same two C oracles to flush out byte/accept-reject
// divergences hiding in framing/edge corners.
//
// Two oracles drive the comparison (both already present in tools/csrc):
//   - GPPI/GPPO (libopus_packet_parse_info.c): opus_packet_get_bandwidth,
//     _nb_channels, _nb_frames, _get_samples_per_frame (48k), _get_nb_samples
//     (48k), opus_packet_parse (toc, payload offset, per-frame sizes).
//   - GRPI/GRPO (libopus_repacketizer_info.c): opus_repacketizer_init/cat/
//     out_range, opus_packet_pad, opus_packet_unpad.
//
// For each generated case the harness asserts, with libopus as the oracle:
//   (a) accept/reject parity — gopus errors iff libopus returns < 0,
//   (b) on accept, identical utility return values (nb_frames,
//       samples_per_frame, nb_samples, bandwidth, channels) and byte-exact
//       opus_packet_parse results (toc, frame count, frame sizes),
//   (c) byte-exact repacketizer cat→out / cat→out_range output,
//   (d) byte-exact opus_packet_pad / opus_packet_unpad,
//   (e) NO panic — a recovered panic is a hard failure.
//
// Generation strategy (deterministic, reseed via GOPUS_FUZZ_SEED):
//   - real encoder output across all three modes, mono+stereo, every frame
//     size, framed as code 0/1/2/3 via the repacketizer,
//   - structured-malformed packets: truncation at every boundary, byte/bit
//     flips, TOC config/stereo/code rewrites, code-2/3 frame-length and
//     padding-byte corruption, code-3 M boundary values (0/1/48/49/63),
//     oversized frames, self-delimited boundary mutations.

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// fuzzSeed returns the base RNG seed, overridable via GOPUS_FUZZ_SEED so a
// failing case can be reproduced or the corpus widened in CI.
func fuzzSeed(def int64) int64 {
	if v := os.Getenv("GOPUS_FUZZ_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

// fuzzIterations returns the per-batch case count, overridable via
// GOPUS_FUZZ_ITERS for longer soak runs.
func fuzzIterations(def int) int {
	if v := os.Getenv("GOPUS_FUZZ_ITERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// buildPacketFuzzSeedBank returns a bank of real, valid Opus packets spanning
// the three coding modes, mono+stereo, several frame sizes, plus multi-frame
// (code 1/2/3) framings assembled via the repacketizer. These are the bases the
// mutator perturbs and the inputs the repacketizer cat-loop concatenates.
func buildPacketFuzzSeedBank(t *testing.T) [][]byte {
	t.Helper()
	var seeds [][]byte
	add := func(p []byte) {
		if len(p) > 0 {
			seeds = append(seeds, append([]byte(nil), p...))
		}
	}

	for _, ch := range []int{1, 2} {
		for _, fs := range []int{120, 240, 480, 960, 1920} {
			add(encodeAPIRateCELTPacketFrameSize(t, ch, fs))
		}
		for _, fs := range []int{480, 960, 1920, 2880} {
			add(encodeAPIRateSILKPacketFrameSize(t, ch, fs))
		}
		for _, fs := range []int{480, 960} {
			add(encodeAPIRateHybridPacketFrameSize(t, ch, fs))
		}
	}

	// Multi-frame framings (code 1/2/3) via the repacketizer so the mutator and
	// the cat-loop also exercise multi-frame headers and CBR/VBR re-framing.
	base := seeds[0:len(seeds):len(seeds)]
	for _, s := range base {
		for _, n := range []int{2, 3, 6} {
			add(repackNCopies(s, n))
		}
	}

	// A few hand-built framings across codes to guarantee coverage of every
	// framing-code re-emit path even if the encoder never produces them.
	for _, cfg := range []uint8{1, 11, 14, 18, 31} {
		add(code0Packet(cfg, false, 40, 1))
		add(code1Packet(cfg, false, 25, 2))
		add(code2Packet(cfg, false, 17, 33, 3))
		add(code3CBRPacket(cfg, false, 3, 14, 4))
		add(code3VBRPacket(cfg, false, []int{11, 22, 9}, 5))
	}
	return seeds
}

// repackNCopies repacketizes n copies of base into a single packet, or nil if
// repacketization is not possible (e.g. would exceed 120ms).
func repackNCopies(base []byte, n int) []byte {
	rp := NewRepacketizer()
	for i := 0; i < n; i++ {
		if err := rp.Cat(base); err != nil {
			return nil
		}
	}
	buf := make([]byte, len(base)*n+64)
	written, err := rp.Out(buf)
	if err != nil || written <= 0 {
		return nil
	}
	return append([]byte(nil), buf[:written]...)
}

// mutateFuzzPacket applies one seeded structured mutation to a copy of src,
// producing a (usually) malformed packet. Returns nil to signal "skip".
func mutateFuzzPacket(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	switch rng.Intn(13) {
	case 0: // truncate to a random shorter length (incl. 0)
		return p[:rng.Intn(len(p)+1)]
	case 1: // single random byte overwrite
		p[rng.Intn(len(p))] = byte(rng.Intn(256))
		return p
	case 2: // single bit flip
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // rewrite TOC config (mode/bandwidth/frame-size), keep low 3 bits
		p[0] = (p[0] & 0x07) | byte(rng.Intn(32))<<3
		return p
	case 4: // rewrite TOC code bits (frame packing)
		p[0] = (p[0] & 0xFC) | byte(rng.Intn(4))
		return p
	case 5: // flip TOC stereo bit
		p[0] ^= 0x04
		return p
	case 6: // force code 3 with a boundary frame-count byte
		p[0] = (p[0] & 0xFC) | 0x03
		boundary := []byte{0x00, 0x01, 0x02, 0x30, 0x31, 0x3F, 0x80, 0x81, 0xC0, 0xC1, 0xFF}
		fc := boundary[rng.Intn(len(boundary))]
		if len(p) < 2 {
			p = append(p, fc)
		} else {
			p[1] = fc
		}
		return p
	case 7: // corrupt a code-2/3 frame-length / padding byte in the header region
		if len(p) >= 2 {
			n := 1 + rng.Intn(min(4, len(p)-1))
			p[n] = byte(rng.Intn(256))
		}
		return p
	case 8: // append junk bytes (oversize / trailing data)
		extra := make([]byte, 1+rng.Intn(8))
		for i := range extra {
			extra[i] = byte(rng.Intn(256))
		}
		return append(p, extra...)
	case 9: // force code 0 with an oversized payload (> maxOpusFrameBytes)
		head := byte(p[0]&0xFC) | 0x00
		over := make([]byte, 1+maxOpusFrameBytes+1+rng.Intn(4))
		over[0] = head
		return over
	case 10: // self-delimited round-trip on the (possibly mutated) base
		if sd, err := makeSelfDelimitedPacket(p); err == nil {
			// mutate one header byte of the self-delimited form too
			if rng.Intn(2) == 0 && len(sd) > 1 {
				sd[rng.Intn(len(sd))] = byte(rng.Intn(256))
			}
			return sd
		}
		return p
	case 11: // tiny packet (1-2 random bytes) — TOC-only / minimal boundary
		out := make([]byte, 1+rng.Intn(2))
		for i := range out {
			out[i] = byte(rng.Intn(256))
		}
		return out
	default: // pass the unmodified valid packet through (control)
		return p
	}
}

// fuzzPacketBank picks a packet for a fuzz case: with probability ~1/2 a
// mutated/malformed packet, otherwise an unmodified valid seed.
func fuzzPacketBank(rng *rand.Rand, seeds [][]byte) []byte {
	src := seeds[rng.Intn(len(seeds))]
	if rng.Intn(2) == 0 {
		if m := mutateFuzzPacket(rng, src); m != nil {
			return m
		}
	}
	return append([]byte(nil), src...)
}

// ── packet-utility differential fuzz (GPPI/GPPO oracle) ───────────────────────

// TestPacketUtilFuzzMatchesLibopus fires randomized single packets at the
// packet-parse oracle and asserts opus_packet_get_* + opus_packet_parse parity.
func TestPacketUtilFuzzMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := buildPacketFuzzSeedBank(t)
	rng := rand.New(rand.NewSource(fuzzSeed(0x1e3779b97f4a7c15)))
	iters := fuzzIterations(4000)

	cases := make([]libopusPacketParseCase, 0, iters)
	for i := 0; i < iters; i++ {
		pkt := fuzzPacketBank(rng, seeds)
		cases = append(cases, libopusPacketParseCase{
			name:   fmt.Sprintf("util_%04d_len%d", i, len(pkt)),
			packet: pkt,
		})
	}

	want, err := probeLibopusPacketParse(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet parse", err)
	}

	for i, tc := range cases {
		w := want[i]
		assertPacketUtilParity(t, tc.name, tc.packet, w)
	}
}

// assertPacketUtilParity checks every opus_packet_* utility plus opus_packet_parse
// for one packet against the oracle outputs, recovering panics as hard failures.
func assertPacketUtilParity(t *testing.T, name string, pkt []byte, w libopusPacketParseResult) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panic on packet %s: %v", name, hex.EncodeToString(pkt), r)
		}
	}()

	// nb_frames parity (opus_packet_get_nb_frames)
	gotFrames, framesErr := packetFrameCountLibopus(pkt)
	if w.nbFrames < 0 {
		if framesErr == nil {
			t.Fatalf("%s: nb_frames gopus=%d (nil err) libopus=%d pkt=%s",
				name, gotFrames, w.nbFrames, hex.EncodeToString(pkt))
		}
	} else {
		if framesErr != nil {
			t.Fatalf("%s: nb_frames gopus err=%v libopus=%d pkt=%s",
				name, framesErr, w.nbFrames, hex.EncodeToString(pkt))
		}
		if int32(gotFrames) != w.nbFrames {
			t.Fatalf("%s: nb_frames gopus=%d libopus=%d pkt=%s",
				name, gotFrames, w.nbFrames, hex.EncodeToString(pkt))
		}
	}

	// samples_per_frame parity at 48kHz (opus_packet_get_samples_per_frame)
	// Only meaningful for len>=1; the oracle reports OPUS_BAD_ARG for empty.
	if len(pkt) >= 1 {
		gotSPF, spfErr := packetSamplesPerFrameAtRate(pkt, 48000)
		if spfErr != nil {
			t.Fatalf("%s: samples_per_frame unexpected err=%v pkt=%s", name, spfErr, hex.EncodeToString(pkt))
		}
		if int32(gotSPF) != w.samplesPerFrame {
			t.Fatalf("%s: samples_per_frame gopus=%d libopus=%d pkt=%s",
				name, gotSPF, w.samplesPerFrame, hex.EncodeToString(pkt))
		}

		// bandwidth / channels parity (TOC-derived, always defined for len>=1)
		toc := ParseTOC(pkt[0])
		gotBW := libopusBandwidthCode(toc.Bandwidth)
		if gotBW != w.bandwidth {
			t.Fatalf("%s: bandwidth gopus=%d libopus=%d toc=0x%02x", name, gotBW, w.bandwidth, pkt[0])
		}
		gotCh := int32(1)
		if toc.Stereo {
			gotCh = 2
		}
		if gotCh != w.nbChannels {
			t.Fatalf("%s: channels gopus=%d libopus=%d toc=0x%02x", name, gotCh, w.nbChannels, pkt[0])
		}
	}

	// nb_samples parity at 48kHz (opus_packet_get_nb_samples / opus_decoder_get_nb_samples)
	gotSamples, samplesErr := packetSamplesAtRate(pkt, 48000)
	if w.nbSamples < 0 {
		if samplesErr == nil {
			t.Fatalf("%s: nb_samples gopus=%d (nil err) libopus=%d pkt=%s",
				name, gotSamples, w.nbSamples, hex.EncodeToString(pkt))
		}
	} else {
		if samplesErr != nil {
			t.Fatalf("%s: nb_samples gopus err=%v libopus=%d pkt=%s",
				name, samplesErr, w.nbSamples, hex.EncodeToString(pkt))
		}
		if int32(gotSamples) != w.nbSamples {
			t.Fatalf("%s: nb_samples gopus=%d libopus=%d pkt=%s",
				name, gotSamples, w.nbSamples, hex.EncodeToString(pkt))
		}
	}

	// opus_packet_parse parity: accept/reject, frame count, TOC byte, frame sizes.
	info, parseErr := ParsePacket(pkt)
	libAccepted := w.parseRet > 0
	gopAccepted := parseErr == nil
	if gopAccepted != libAccepted {
		t.Fatalf("%s: parse accept gopus=%v(%v) libopus=%v(ret=%d) pkt=%s",
			name, gopAccepted, parseErr, libAccepted, w.parseRet, hex.EncodeToString(pkt))
	}
	if !libAccepted {
		return
	}
	if int32(info.FrameCount) != w.parseRet {
		t.Fatalf("%s: parse frame count gopus=%d libopus=%d pkt=%s",
			name, info.FrameCount, w.parseRet, hex.EncodeToString(pkt))
	}
	if int32(pkt[0]) != w.parseTOC {
		t.Fatalf("%s: parse toc gopus=0x%02x libopus=0x%02x pkt=%s",
			name, pkt[0], byte(w.parseTOC), hex.EncodeToString(pkt))
	}
	if int32(len(info.FrameSizes)) != w.nFrameSizes {
		t.Fatalf("%s: parse nFrameSizes gopus=%d libopus=%d pkt=%s",
			name, len(info.FrameSizes), w.nFrameSizes, hex.EncodeToString(pkt))
	}
	for j, sz := range info.FrameSizes {
		if int32(sz) != int32(w.frameSizes[j]) {
			t.Fatalf("%s: parse frameSizes[%d] gopus=%d libopus=%d pkt=%s",
				name, j, sz, w.frameSizes[j], hex.EncodeToString(pkt))
		}
	}
}

// ── repacketizer differential fuzz (GRPI/GRPO oracle) ─────────────────────────

// TestRepacketizerFuzzMatchesLibopus fires randomized cat sequences + out_range
// + pad/unpad at the repacketizer oracle and asserts byte-exact parity.
func TestRepacketizerFuzzMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	seeds := buildPacketFuzzSeedBank(t)
	rng := rand.New(rand.NewSource(fuzzSeed(0x5eadbeefcafef00d)))
	iters := fuzzIterations(3000)

	cases := make([]repacketizerOracleCase, 0, iters)
	for i := 0; i < iters; i++ {
		cases = append(cases, randomRepacketizerCase(rng, seeds, i))
	}

	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}

	for i, tc := range cases {
		w := want[i]
		assertRepacketizerCaseParity(t, tc, w)
	}
}

// randomRepacketizerCase builds one randomized repacketizer fuzz case: 1..6
// input packets (mix of valid + malformed), a random begin/end sub-range, a
// random maxlen (sometimes too small), and a random pad target.
func randomRepacketizerCase(rng *rand.Rand, seeds [][]byte, idx int) repacketizerOracleCase {
	nPkts := 1 + rng.Intn(6)
	packets := make([][]byte, nPkts)
	maxFrame := 0
	for j := range packets {
		// Bias toward valid packets so multi-packet merges actually exercise the
		// happy path; still inject malformed inputs ~1/3 of the time.
		var p []byte
		if rng.Intn(3) == 0 {
			p = mutateFuzzPacket(rng, seeds[rng.Intn(len(seeds))])
		}
		if p == nil {
			p = append([]byte(nil), seeds[rng.Intn(len(seeds))]...)
		}
		packets[j] = p
		if len(p) > maxFrame {
			maxFrame = len(p)
		}
	}

	// begin/end span: usually full range (end=0 → resolved after cat), sometimes
	// a random sub-range that may be out of bounds (testing reject parity).
	begin, end := 0, 0
	if rng.Intn(2) == 0 {
		begin = rng.Intn(8)
		end = begin + rng.Intn(8)
	}

	// maxlen: usually generous, occasionally far too small to force buffer-too-small.
	maxlen := 2048
	switch rng.Intn(4) {
	case 0:
		maxlen = rng.Intn(6) // tiny → likely buffer-too-small
	case 1:
		maxlen = maxFrame + rng.Intn(16)
	}

	// pad target: 0 (skip) half the time, else a length >= packet[0] len.
	padNewLen := 0
	if rng.Intn(2) == 0 && len(packets[0]) > 0 {
		padNewLen = len(packets[0]) + rng.Intn(320)
	}

	return repacketizerOracleCase{
		name:      fmt.Sprintf("rp_%04d_n%d_b%d_e%d_max%d_pad%d", idx, nPkts, begin, end, maxlen, padNewLen),
		packets:   packets,
		begin:     begin,
		end:       end,
		maxlen:    maxlen,
		padNewLen: padNewLen,
	}
}

// assertRepacketizerCaseParity replays one repacketizer fuzz case through gopus
// and asserts full parity (cat accept/reject, nb_frames, out_range bytes, pad/
// unpad bytes) vs the oracle, recovering panics as hard failures.
func assertRepacketizerCaseParity(t *testing.T, tc repacketizerOracleCase, w repacketizerOracleResult) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panic: %v\n%s", tc.name, r, dumpRepacketizerCase(tc))
		}
	}()

	got := runRepacketizerGopus(tc)

	// cat accept/reject parity
	libCatOK := w.catRet == 0
	gopCatOK := got.catRet == 0
	if libCatOK != gopCatOK {
		t.Fatalf("%s: cat parity gopus(ok=%v ret=%d) libopus(ok=%v ret=%d)\n%s",
			tc.name, gopCatOK, got.catRet, libCatOK, w.catRet, dumpRepacketizerCase(tc))
	}
	if !libCatOK {
		return
	}
	if got.nbFrames != w.nbFrames {
		t.Fatalf("%s: nb_frames gopus=%d libopus=%d\n%s",
			tc.name, got.nbFrames, w.nbFrames, dumpRepacketizerCase(tc))
	}

	// out_range accept/reject + byte-exact
	libOutOK := w.outRet > 0
	gopOutOK := got.outRet > 0
	if libOutOK != gopOutOK {
		t.Fatalf("%s: out_range parity gopus=%d libopus=%d\n%s",
			tc.name, got.outRet, w.outRet, dumpRepacketizerCase(tc))
	}
	if libOutOK {
		if got.outRet != w.outRet {
			t.Fatalf("%s: out_range len gopus=%d libopus=%d\n%s",
				tc.name, got.outRet, w.outRet, dumpRepacketizerCase(tc))
		}
		if hex.EncodeToString(got.outBytes) != hex.EncodeToString(w.outBytes) {
			t.Fatalf("%s: out_range bytes\n got=%s\nwant=%s\n%s",
				tc.name, hex.EncodeToString(got.outBytes), hex.EncodeToString(w.outBytes), dumpRepacketizerCase(tc))
		}
	}

	// pad / unpad parity
	if w.padRet == repacketizerSkipped {
		return
	}
	libPadOK := w.padRet == 0
	gopPadOK := got.padRet == 0
	if libPadOK != gopPadOK {
		t.Fatalf("%s: pad parity gopus=%d libopus=%d\n%s",
			tc.name, got.padRet, w.padRet, dumpRepacketizerCase(tc))
	}
	if !libPadOK {
		return
	}
	if hex.EncodeToString(got.padBytes) != hex.EncodeToString(w.padBytes) {
		t.Fatalf("%s: pad bytes\n got=%s\nwant=%s\n%s",
			tc.name, hex.EncodeToString(got.padBytes), hex.EncodeToString(w.padBytes), dumpRepacketizerCase(tc))
	}
	libUnpadOK := w.unpadRet > 0
	gopUnpadOK := got.unpadRet > 0
	if libUnpadOK != gopUnpadOK {
		t.Fatalf("%s: unpad parity gopus=%d libopus=%d\n%s",
			tc.name, got.unpadRet, w.unpadRet, dumpRepacketizerCase(tc))
	}
	if libUnpadOK {
		if got.unpadRet != w.unpadRet {
			t.Fatalf("%s: unpad len gopus=%d libopus=%d\n%s",
				tc.name, got.unpadRet, w.unpadRet, dumpRepacketizerCase(tc))
		}
		if hex.EncodeToString(got.unpadBytes) != hex.EncodeToString(w.unpadBytes) {
			t.Fatalf("%s: unpad bytes\n got=%s\nwant=%s\n%s",
				tc.name, hex.EncodeToString(got.unpadBytes), hex.EncodeToString(w.unpadBytes), dumpRepacketizerCase(tc))
		}
	}
}

// dumpRepacketizerCase renders a failing case as reproducible hex for triage.
func dumpRepacketizerCase(tc repacketizerOracleCase) string {
	out := fmt.Sprintf("  begin=%d end=%d maxlen=%d padNewLen=%d packets=%d\n",
		tc.begin, tc.end, tc.maxlen, tc.padNewLen, len(tc.packets))
	for i, p := range tc.packets {
		out += fmt.Sprintf("  pkt[%d]=%s\n", i, hex.EncodeToString(p))
	}
	return out
}
