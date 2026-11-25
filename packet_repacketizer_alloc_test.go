package gopus

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestPacketPadUnpadCallerOwnedCapacity(t *testing.T) {
	packet := []byte{0x48, 0x11, 0x22, 0x33}
	storage := bytes.Repeat([]byte{0xa5}, 32)
	copy(storage, packet)
	input := storage[:len(packet)]
	if err := PacketPad(input, len(packet), 24); err != nil {
		t.Fatalf("PacketPad with spare capacity: %v", err)
	}
	if len(input) != len(packet) {
		t.Fatalf("PacketPad changed the caller slice length to %d, want %d", len(input), len(packet))
	}
	if info, err := ParsePacket(storage[:24]); err != nil || info.TotalSize != 24 {
		t.Fatalf("ParsePacket(padded)=(%+v,%v), want a valid 24-byte packet", info, err)
	}
	if !bytes.Equal(storage[24:], bytes.Repeat([]byte{0xa5}, len(storage)-24)) {
		t.Fatalf("PacketPad wrote past newLen: tail=%x", storage[24:])
	}

	n, err := PacketUnpad(storage[:24], 24)
	if err != nil {
		t.Fatalf("PacketUnpad with spare capacity: %v", err)
	}
	if got := storage[:n]; !bytes.Equal(got, packet) {
		t.Fatalf("PacketUnpad=%x, want %x", got, packet)
	}
}

func TestPacketUnpadCanonicalizesAndDropsOpaquePadding(t *testing.T) {
	t.Run("noncanonical framing", func(t *testing.T) {
		packet := code3VBRPacket(18, false, []int{1, 1}, 7)
		want := append([]byte{GenerateTOC(18, false, 1)}, packet[len(packet)-2:]...)
		n, err := PacketUnpad(packet, len(packet))
		if err != nil {
			t.Fatalf("PacketUnpad: %v", err)
		}
		if got := packet[:n]; !bytes.Equal(got, want) {
			t.Fatalf("PacketUnpad=%x, want canonical code-1 packet %x", got, want)
		}
	})

	t.Run("malformed opaque extension", func(t *testing.T) {
		packet := mustDecodeHex(t, "4b4102112233ffff")
		want := []byte{0x48, 0x11, 0x22, 0x33}
		n, err := PacketUnpad(packet, len(packet))
		if err != nil {
			t.Fatalf("PacketUnpad: %v", err)
		}
		if got := packet[:n]; !bytes.Equal(got, want) {
			t.Fatalf("PacketUnpad=%x, want %x", got, want)
		}
	})

	t.Run("truncated padding length", func(t *testing.T) {
		packet := []byte{0x4b, 0x41, 0xff}
		before := append([]byte(nil), packet...)
		if _, err := PacketUnpad(packet, len(packet)); err != ErrPacketTooShort {
			t.Fatalf("PacketUnpad error=%v, want %v", err, ErrPacketTooShort)
		}
		if !bytes.Equal(packet, before) {
			t.Fatalf("PacketUnpad changed malformed input: got %x want %x", packet, before)
		}
	})
}

func TestPacketPadUnpad48FrameVBR(t *testing.T) {
	packet := code3VBRPacket(16, false, packet48VBRFrameSizes(), 0x20)
	newLen := len(packet) + 43
	storage := make([]byte, len(packet), newLen)
	copy(storage, packet)
	if err := PacketPad(storage, len(packet), newLen); err != nil {
		t.Fatalf("PacketPad(48-frame VBR): %v", err)
	}
	n, err := PacketUnpad(storage[:newLen], newLen)
	if err != nil {
		t.Fatalf("PacketUnpad(48-frame VBR): %v", err)
	}
	if got := storage[:n]; !bytes.Equal(got, packet) {
		t.Fatalf("pad/unpad changed 48-frame VBR packet: got %x want %x", got, packet)
	}
}

func TestPacketPadExtensionFallbackWithShortSliceAndSpareCapacity(t *testing.T) {
	packet := mustDecodeHex(t, "4b41061122330baa50deadbe")
	wantPadded := mustDecodeHex(t, "4b410a112233010101010baa50deadbe")
	storage := make([]byte, len(packet), len(wantPadded))
	copy(storage, packet)
	if err := PacketPad(storage[:len(packet)], len(packet), len(wantPadded)); err != nil {
		t.Fatalf("PacketPad(extension packet): %v", err)
	}
	if got := storage[:len(wantPadded)]; !bytes.Equal(got, wantPadded) {
		t.Fatalf("PacketPad(extension packet)=%x, want %x", got, wantPadded)
	}
	n, err := PacketUnpad(storage[:len(wantPadded)], len(wantPadded))
	if err != nil {
		t.Fatalf("PacketUnpad(extension packet): %v", err)
	}
	wantUnpadded := []byte{0x48, 0x11, 0x22, 0x33}
	if got := storage[:n]; !bytes.Equal(got, wantUnpadded) {
		t.Fatalf("PacketUnpad(extension packet)=%x, want %x", got, wantUnpadded)
	}
}

func TestPacketPadLargeExtensionPaddingRoundTrip(t *testing.T) {
	packet := packetWithLargeLongExtension(4096)
	newLen := len(packet) + 43
	buf := make([]byte, len(packet), newLen)
	copy(buf, packet)
	if err := PacketPad(buf, len(packet), newLen); err != nil {
		t.Fatalf("PacketPad(large extension): %v", err)
	}
	n, err := PacketUnpad(buf[:newLen], newLen)
	if err != nil {
		t.Fatalf("PacketUnpad(large extension): %v", err)
	}
	want := []byte{GenerateTOC(18, false, 0), 0x11, 0x22, 0x33}
	if got := buf[:n]; !bytes.Equal(got, want) {
		t.Fatalf("PacketUnpad(large extension)=%x, want %x", got, want)
	}
}

func TestPacketPadUnpadSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "code 0", data: []byte{0x48, 0x11, 0x22, 0x33}},
		{name: "48-frame VBR", data: makePacket48FrameVBR()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newLen := len(tt.data) + 43
			buf := make([]byte, len(tt.data), newLen)
			pad := func() {
				copy(buf, tt.data)
				if err := PacketPad(buf, len(tt.data), newLen); err != nil {
					panic(err)
				}
			}
			pad()
			if got := testing.AllocsPerRun(100, pad); got != 0 {
				t.Errorf("PacketPad allocs/op=%.2f, want 0", got)
			}

			padded := append([]byte(nil), buf[:newLen]...)
			unpadBuf := make([]byte, newLen)
			unpad := func() {
				copy(unpadBuf, padded)
				if _, err := PacketUnpad(unpadBuf, newLen); err != nil {
					panic(err)
				}
			}
			unpad()
			if got := testing.AllocsPerRun(100, unpad); got != 0 {
				t.Errorf("PacketUnpad allocs/op=%.2f, want 0", got)
			}
		})
	}
}

func makePacket48FrameVBR() []byte {
	return code3VBRPacket(16, false, packet48VBRFrameSizes(), 0x20)
}

func BenchmarkPacketPadCallerCapacity(b *testing.B) {
	benchmarkPacketPad(b, []byte{0x48, 0x11, 0x22, 0x33})
}

func BenchmarkPacketPad48FrameVBR(b *testing.B) {
	benchmarkPacketPad(b, makePacket48FrameVBR())
}

func BenchmarkPacketPadWithExtensions(b *testing.B) {
	benchmarkPacketPad(b, []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe})
}

func BenchmarkPacketPadLargeExtensionPadding(b *testing.B) {
	benchmarkPacketPad(b, packetWithLargeLongExtension(4096))
}

func benchmarkPacketPad(b *testing.B, packet []byte) {
	newLen := len(packet) + 43
	buf := make([]byte, len(packet), newLen)
	b.ReportAllocs()
	b.SetBytes(int64(len(packet)))
	b.ResetTimer()
	for range b.N {
		copy(buf, packet)
		if err := PacketPad(buf, len(packet), newLen); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPacketUnpadCallerCapacity(b *testing.B) {
	benchmarkPacketUnpad(b, []byte{0x48, 0x11, 0x22, 0x33})
}

func BenchmarkPacketUnpad48FrameVBR(b *testing.B) {
	benchmarkPacketUnpad(b, makePacket48FrameVBR())
}

func benchmarkPacketUnpad(b *testing.B, packet []byte) {
	newLen := len(packet) + 43
	padded := make([]byte, newLen)
	copy(padded, packet)
	if err := PacketPad(padded, len(packet), newLen); err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, newLen)
	b.ReportAllocs()
	b.SetBytes(int64(newLen))
	b.ResetTimer()
	for range b.N {
		copy(buf, padded)
		if _, err := PacketUnpad(buf, newLen); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParsePacket48FrameVBR(b *testing.B) {
	packet := makePacket48FrameVBR()
	b.ReportAllocs()
	b.SetBytes(int64(len(packet)))
	b.ResetTimer()
	for range b.N {
		if _, err := ParsePacket(packet); err != nil {
			b.Fatal(err)
		}
	}
}

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
	for range n {
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
		// Clearing the low two TOC bits selects frame-count code 0.
		head := p[0] & 0xFC
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
	for i := range iters {
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
	for i := range iters {
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
	var out strings.Builder
	fmt.Fprintf(&out, "  begin=%d end=%d maxlen=%d padNewLen=%d packets=%d\n",
		tc.begin, tc.end, tc.maxlen, tc.padNewLen, len(tc.packets))
	for i, p := range tc.packets {
		fmt.Fprintf(&out, "  pkt[%d]=%s\n", i, hex.EncodeToString(p))
	}
	return out.String()
}

type repacketizerOracleCase struct {
	name      string
	packets   [][]byte
	begin     int
	end       int // 0 == all frames
	maxlen    int
	padNewLen int // 0 == skip pad/unpad
}

type repacketizerOracleResult struct {
	catRet     int32
	nbFrames   int32
	outRet     int32
	outBytes   []byte
	padRet     int32
	padBytes   []byte
	unpadRet   int32
	unpadBytes []byte
}

const repacketizerSkipped = 0x7fffffff

var repacketizerParityHelper libopustest.HelperCache

func getRepacketizerHelperPath() (string, error) {
	return repacketizerParityHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "repacketizer",
		OutputBase:  "gopus_libopus_repacketizer",
		SourceFile:  "libopus_repacketizer_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"src", "celt", "silk"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusRepacketizer(cases []repacketizerOracleCase) ([]repacketizerOracleResult, error) {
	binPath, err := getRepacketizerHelperPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayload("GRPI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.packets)))
		for _, p := range tc.packets {
			payload.U32(uint32(len(p)))
			payload.Raw(p)
		}
		payload.U32(uint32(tc.begin))
		payload.U32(uint32(tc.end))
		payload.U32(uint32(tc.maxlen))
		payload.U32(uint32(tc.padNewLen))
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "repacketizer", "GRPO")
	if err != nil {
		return nil, err
	}
	reader.Count(len(cases))
	if reader.Err() != nil {
		return nil, reader.Err()
	}

	out := make([]repacketizerOracleResult, len(cases))
	for i := range out {
		r := &out[i]
		r.catRet = reader.I32()
		r.nbFrames = reader.I32()
		r.outRet = reader.I32()
		outLen := reader.I32()
		if outLen > 0 {
			r.outBytes = append([]byte(nil), reader.Bytes(int(outLen))...)
		}
		r.padRet = reader.I32()
		padLen := reader.I32()
		if padLen > 0 {
			r.padBytes = append([]byte(nil), reader.Bytes(int(padLen))...)
		}
		r.unpadRet = reader.I32()
		unpadLen := reader.I32()
		if unpadLen > 0 {
			r.unpadBytes = append([]byte(nil), reader.Bytes(int(unpadLen))...)
		}
		if reader.Err() != nil {
			return nil, reader.Err()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// runRepacketizerGopus replays a case through the gopus repacketizer + pad/unpad
// and returns results in the same shape as the oracle output.
func runRepacketizerGopus(tc repacketizerOracleCase) repacketizerOracleResult {
	var res repacketizerOracleResult
	rp := NewRepacketizer()
	catOK := true
	for _, p := range tc.packets {
		if err := rp.Cat(p); err != nil {
			res.catRet = -4 // any negative; oracle uses OPUS_INVALID_PACKET (-4)
			catOK = false
			break
		}
	}
	if !catOK {
		res.outRet = res.catRet
		return res
	}
	res.catRet = 0
	res.nbFrames = int32(rp.NumFrames())

	end := tc.end
	if end == 0 {
		end = rp.NumFrames()
	}
	buf := make([]byte, tc.maxlen)
	n, err := rp.OutRange(tc.begin, end, buf)
	if err != nil {
		res.outRet = -1 // negative sentinel
	} else {
		res.outRet = int32(n)
		res.outBytes = append([]byte(nil), buf[:n]...)
	}

	if tc.padNewLen > 0 && len(tc.packets) > 0 && len(tc.packets[0]) > 0 {
		src := tc.packets[0]
		padBuf := make([]byte, tc.padNewLen)
		copy(padBuf, src)
		if err := PacketPad(padBuf, len(src), tc.padNewLen); err != nil {
			res.padRet = -1
		} else {
			res.padRet = 0
			res.padBytes = append([]byte(nil), padBuf...)
			unpadBuf := make([]byte, tc.padNewLen)
			copy(unpadBuf, padBuf)
			ul, uerr := PacketUnpad(unpadBuf, tc.padNewLen)
			if uerr != nil {
				res.unpadRet = -1
			} else {
				res.unpadRet = int32(ul)
				res.unpadBytes = append([]byte(nil), unpadBuf[:ul]...)
			}
		}
	} else {
		res.padRet = repacketizerSkipped
		res.unpadRet = repacketizerSkipped
	}
	return res
}

func TestRepacketizerByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := repacketizerOracleCases()
	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}

	for i, tc := range cases {
		w := want[i]
		t.Run(tc.name, func(t *testing.T) {
			got := runRepacketizerGopus(tc)

			// cat accept/reject parity
			libCatOK := w.catRet == 0
			gopCatOK := got.catRet == 0
			if libCatOK != gopCatOK {
				t.Fatalf("cat parity: gopus ok=%v (ret=%d) libopus ok=%v (ret=%d)",
					gopCatOK, got.catRet, libCatOK, w.catRet)
			}
			if !libCatOK {
				return
			}

			if got.nbFrames != w.nbFrames {
				t.Errorf("nb_frames: got %d want %d", got.nbFrames, w.nbFrames)
			}

			// out_range accept/reject parity (positive vs negative)
			libOutOK := w.outRet > 0
			gopOutOK := got.outRet > 0
			if libOutOK != gopOutOK {
				t.Fatalf("out_range parity: gopus ret=%d libopus ret=%d (bufmismatch?)", got.outRet, w.outRet)
			}
			if libOutOK {
				if got.outRet != w.outRet {
					t.Errorf("out_range length: got %d want %d", got.outRet, w.outRet)
				}
				if hex.EncodeToString(got.outBytes) != hex.EncodeToString(w.outBytes) {
					t.Errorf("out_range bytes:\n got=%s\nwant=%s",
						hex.EncodeToString(got.outBytes), hex.EncodeToString(w.outBytes))
				}
			}

			// pad / unpad parity
			if w.padRet != repacketizerSkipped {
				libPadOK := w.padRet == 0
				gopPadOK := got.padRet == 0
				if libPadOK != gopPadOK {
					t.Fatalf("pad parity: gopus ret=%d libopus ret=%d", got.padRet, w.padRet)
				}
				if libPadOK {
					if hex.EncodeToString(got.padBytes) != hex.EncodeToString(w.padBytes) {
						t.Errorf("pad bytes:\n got=%s\nwant=%s",
							hex.EncodeToString(got.padBytes), hex.EncodeToString(w.padBytes))
					}
					libUnpadOK := w.unpadRet > 0
					gopUnpadOK := got.unpadRet > 0
					if libUnpadOK != gopUnpadOK {
						t.Fatalf("unpad parity: gopus ret=%d libopus ret=%d", got.unpadRet, w.unpadRet)
					}
					if libUnpadOK {
						if got.unpadRet != w.unpadRet {
							t.Errorf("unpad length: got %d want %d", got.unpadRet, w.unpadRet)
						}
						if hex.EncodeToString(got.unpadBytes) != hex.EncodeToString(w.unpadBytes) {
							t.Errorf("unpad bytes:\n got=%s\nwant=%s",
								hex.EncodeToString(got.unpadBytes), hex.EncodeToString(w.unpadBytes))
						}
					}
				}
			}
		})
	}
}

func TestPacketUnpadMalformedExtensionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := mustDecodeHex(t, "4b4102112233ffff")
	tc := repacketizerOracleCase{
		name:      "unpad_opaque_malformed_extension",
		packets:   [][]byte{packet},
		begin:     0,
		end:       1,
		maxlen:    64,
		padNewLen: len(packet), // PacketPad is a no-op, so the oracle unpads the input directly.
	}
	want, err := probeLibopusRepacketizer([]repacketizerOracleCase{tc})
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}
	got := runRepacketizerGopus(tc)
	if want[0].unpadRet != int32(len([]byte{0x48, 0x11, 0x22, 0x33})) {
		t.Fatalf("libopus PacketUnpad ret=%d, want 4", want[0].unpadRet)
	}
	if got.unpadRet != want[0].unpadRet || !bytes.Equal(got.unpadBytes, want[0].unpadBytes) {
		t.Fatalf("PacketUnpad=%x (ret=%d), libopus=%x (ret=%d)", got.unpadBytes, got.unpadRet, want[0].unpadBytes, want[0].unpadRet)
	}
}

func TestMalformedPacketExtensionErrorsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	malformed := mustDecodeHex(t, "4b4102112233ffff")
	valid := []byte{0x48, 0x44}
	cases := []repacketizerOracleCase{
		{
			name:      "extension_out_range_adequate_buffer",
			packets:   [][]byte{malformed},
			begin:     0,
			end:       1,
			maxlen:    64,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_short_buffer",
			packets:   [][]byte{malformed},
			begin:     0,
			end:       1,
			maxlen:    1,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_invalid_range",
			packets:   [][]byte{malformed},
			begin:     1,
			end:       1,
			maxlen:    64,
			padNewLen: 16,
		},
		{
			name:      "extension_out_range_skips_malformed_frame",
			packets:   [][]byte{malformed, valid},
			begin:     1,
			end:       2,
			maxlen:    64,
			padNewLen: 16,
		},
	}
	want, err := probeLibopusRepacketizer(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "repacketizer", err)
	}
	for i, tc := range cases {
		if want[i].catRet != 0 {
			t.Fatalf("libopus %s cat ret=%d, want 0", tc.name, want[i].catRet)
		}
		if want[i].padRet != -3 {
			t.Errorf("libopus %s PacketPad ret=%d, want OPUS_INTERNAL_ERROR (-3)", tc.name, want[i].padRet)
		}
	}
	if want[0].outRet != -3 || want[1].outRet != -3 {
		t.Fatalf("libopus OutRange malformed extension: adequate=%d short=%d, want -3 for both", want[0].outRet, want[1].outRet)
	}
	if want[2].outRet != -1 {
		t.Fatalf("libopus OutRange invalid range ret=%d, want OPUS_BAD_ARG (-1)", want[2].outRet)
	}
	if want[3].outRet != int32(len(valid)) || !bytes.Equal(want[3].outBytes, valid) {
		t.Fatalf("libopus OutRange after malformed frame: ret=%d bytes=%x, want %x", want[3].outRet, want[3].outBytes, valid)
	}

	if _, err := parsePacketExtensionList([]byte{0xff, 0xff}, 1); err != ErrInvalidPacket {
		t.Fatalf("parsePacketExtensionList malformed data err=%v, want ErrInvalidPacket", err)
	}

	rp := NewRepacketizer()
	if err := rp.Cat(malformed); err != nil {
		t.Fatalf("Cat malformed-extension packet: %v", err)
	}
	if got := rp.NumFrames(); got != 1 {
		t.Fatalf("NumFrames after Cat=%d, want 1", got)
	}

	for _, tc := range []struct {
		name      string
		begin     int
		end       int
		outLen    int
		wantError error
	}{
		{name: "adequate output", begin: 0, end: 1, outLen: 64, wantError: ErrInternalError},
		{name: "short output", begin: 0, end: 1, outLen: 1, wantError: ErrInternalError},
		{name: "invalid range", begin: 1, end: 1, outLen: 64, wantError: ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := bytes.Repeat([]byte{0xa5}, tc.outLen)
			before := append([]byte(nil), out...)
			n, err := rp.OutRange(tc.begin, tc.end, out)
			if n != 0 || err != tc.wantError {
				t.Fatalf("OutRange(%d,%d)=(%d,%v), want (0,%v)", tc.begin, tc.end, n, err, tc.wantError)
			}
			if !bytes.Equal(out, before) {
				t.Fatalf("OutRange(%d,%d) changed output on error: got %x want %x", tc.begin, tc.end, out, before)
			}
			if got := rp.NumFrames(); got != 1 {
				t.Fatalf("OutRange(%d,%d) changed NumFrames to %d, want 1", tc.begin, tc.end, got)
			}
		})
	}

	pad := bytes.Repeat([]byte{0xa5}, 16)
	copy(pad, malformed)
	padBefore := append([]byte(nil), pad...)
	if err := PacketPad(pad, len(malformed), len(pad)); err != ErrInternalError {
		t.Fatalf("PacketPad malformed extension err=%v, want ErrInternalError", err)
	}
	if !bytes.Equal(pad, padBefore) {
		t.Fatalf("PacketPad changed output on error: got %x want %x", pad, padBefore)
	}

	if err := rp.Cat(valid); err != nil {
		t.Fatalf("Cat valid packet after extension errors: %v", err)
	}
	out := make([]byte, 64)
	n, err := rp.OutRange(1, 2, out)
	if err != nil {
		t.Fatalf("OutRange valid frame after malformed frame: %v", err)
	}
	if !bytes.Equal(out[:n], want[3].outBytes) {
		t.Fatalf("OutRange valid continuation=%x, want libopus %x", out[:n], want[3].outBytes)
	}
}

// ── case builders ────────────────────────────────────────────────────────────

// mkPayload returns n deterministic non-zero bytes.
func mkPayload(n, seed int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*7 + seed*31 + 1) & 0xFF)
	}
	return b
}

// code0Packet builds a single-frame (code 0) packet of the given config/stereo.
func code0Packet(config uint8, stereo bool, payloadLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 0)}
	return append(p, mkPayload(payloadLen, seed)...)
}

// code1Packet builds two equal frames (code 1).
func code1Packet(config uint8, stereo bool, frameLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 1)}
	p = append(p, mkPayload(frameLen, seed)...)
	p = append(p, mkPayload(frameLen, seed+1)...)
	return p
}

// code2Packet builds two unequal frames (code 2).
func code2Packet(config uint8, stereo bool, len0, len1, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 2)}
	szBuf := make([]byte, 2)
	n := encodeFrameLength(szBuf, len0)
	p = append(p, szBuf[:n]...)
	p = append(p, mkPayload(len0, seed)...)
	p = append(p, mkPayload(len1, seed+1)...)
	return p
}

// code3CBRPacket builds a code-3 CBR packet with m equal frames of frameLen.
func code3CBRPacket(config uint8, stereo bool, m, frameLen, seed int) []byte {
	p := []byte{GenerateTOC(config, stereo, 3), byte(m & 0x3F)}
	for i := range m {
		p = append(p, mkPayload(frameLen, seed+i)...)
	}
	return p
}

// code3VBRPacket builds a code-3 VBR packet with frame sizes given.
func code3VBRPacket(config uint8, stereo bool, sizes []int, seed int) []byte {
	m := len(sizes)
	p := []byte{GenerateTOC(config, stereo, 3), byte(0x80 | (m & 0x3F))}
	for i := 0; i < m-1; i++ {
		szBuf := make([]byte, 2)
		n := encodeFrameLength(szBuf, sizes[i])
		p = append(p, szBuf[:n]...)
	}
	for i := range m {
		p = append(p, mkPayload(sizes[i], seed+i)...)
	}
	return p
}

func repacketizerOraclecasesAppendSingle(cases []repacketizerOracleCase) []repacketizerOracleCase {
	// Single-packet round-trips across all framing codes and several configs.
	configs := []uint8{0, 1, 8, 11, 12, 14, 16, 18, 20, 28, 30, 31}
	for _, cfg := range configs {
		for _, stereo := range []bool{false, true} {
			s := "m"
			if stereo {
				s = "s"
			}
			cases = append(cases,
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code0_cfg%d_%s", cfg, s),
					packets:   [][]byte{code0Packet(cfg, stereo, 40, 1)},
					maxlen:    512,
					padNewLen: 60,
				},
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code1_cfg%d_%s", cfg, s),
					packets:   [][]byte{code1Packet(cfg, stereo, 20, 2)},
					maxlen:    512,
					padNewLen: 80,
				},
				repacketizerOracleCase{
					name:      fmt.Sprintf("single_code2_cfg%d_%s", cfg, s),
					packets:   [][]byte{code2Packet(cfg, stereo, 15, 33, 3)},
					maxlen:    512,
					padNewLen: 100,
				},
			)
		}
	}
	return cases
}

func repacketizerOracleCases() []repacketizerOracleCase {
	var cases []repacketizerOracleCase
	cases = repacketizerOraclecasesAppendSingle(cases)
	largeExtensionPacket := packetWithLargeLongExtension(4096)

	// CELT 10ms config (18) supports up to 12 frames in 120ms (480 samples each).
	// SILK 20ms config (1) supports up to 6 frames. Use small frame sizes.

	// Merge two code-0 packets -> code 1 (equal) or code 2 (unequal).
	cases = append(cases,
		repacketizerOracleCase{
			name:    "merge_two_code0_equal",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 30, 2)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "merge_two_code0_unequal",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 41, 2)},
			maxlen:  512, padNewLen: 0,
		},
		// large frame (>=252) forces 2-byte size encoding in code 2 / VBR
		repacketizerOracleCase{
			name:    "merge_two_code0_largefirst",
			packets: [][]byte{code0Packet(18, false, 300, 1), code0Packet(18, false, 41, 2)},
			maxlen:  1024, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "merge_two_code0_stereo",
			packets: [][]byte{code0Packet(31, true, 50, 1), code0Packet(31, true, 60, 2)},
			maxlen:  512, padNewLen: 0,
		},
	)

	// Merge three code-0 -> code 3.
	cases = append(cases,
		repacketizerOracleCase{
			name: "merge_three_code0_equal",
			packets: [][]byte{
				code0Packet(18, false, 25, 1),
				code0Packet(18, false, 25, 2),
				code0Packet(18, false, 25, 3),
			},
			maxlen: 512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name: "merge_three_code0_vbr",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			maxlen: 512, padNewLen: 0,
		},
	)

	// OutRange sub-range over a 3-frame accumulation.
	cases = append(cases,
		repacketizerOracleCase{
			name: "outrange_1_3_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 1, end: 3, maxlen: 512,
		},
		repacketizerOracleCase{
			name: "outrange_0_1_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 0, end: 1, maxlen: 512,
		},
		repacketizerOracleCase{
			name: "outrange_1_2_of_3",
			packets: [][]byte{
				code0Packet(18, false, 20, 1),
				code0Packet(18, false, 35, 2),
				code0Packet(18, false, 27, 3),
			},
			begin: 1, end: 2, maxlen: 512,
		},
	)

	// Feed multi-frame packets directly (code1/2/3) then re-emit.
	cases = append(cases,
		repacketizerOracleCase{
			name:    "in_code1_passthrough",
			packets: [][]byte{code1Packet(18, false, 22, 5)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "in_code3cbr_passthrough",
			packets: [][]byte{code3CBRPacket(18, false, 4, 15, 6)},
			maxlen:  512, padNewLen: 0,
		},
		repacketizerOracleCase{
			name:    "in_code3vbr_passthrough",
			packets: [][]byte{code3VBRPacket(18, false, []int{10, 20, 30}, 7)},
			maxlen:  512, padNewLen: 0,
		},
		// merge a code1 + code0 (3 total frames -> code 3)
		repacketizerOracleCase{
			name:    "merge_code1_plus_code0",
			packets: [][]byte{code1Packet(18, false, 22, 5), code0Packet(18, false, 22, 8)},
			maxlen:  512, padNewLen: 0,
		},
	)

	// pad / unpad focused cases on various single packets.
	cases = append(cases,
		repacketizerOracleCase{
			name:      "pad_code0_small",
			packets:   [][]byte{code0Packet(18, false, 10, 1)},
			maxlen:    512,
			padNewLen: 11, // +1 byte -> minimal code 3 padding
		},
		repacketizerOracleCase{
			name:      "pad_code0_large",
			packets:   [][]byte{code0Packet(18, false, 10, 1)},
			maxlen:    512,
			padNewLen: 300, // forces chained 255 padding bytes
		},
		repacketizerOracleCase{
			name:      "pad_code1",
			packets:   [][]byte{code1Packet(18, false, 12, 2)},
			maxlen:    512,
			padNewLen: 200,
		},
		repacketizerOracleCase{
			name:      "pad_code3cbr",
			packets:   [][]byte{code3CBRPacket(18, false, 3, 12, 4)},
			maxlen:    512,
			padNewLen: 150,
		},
		repacketizerOracleCase{
			name:      "pad_noncanonical_two_frame_vbr",
			packets:   [][]byte{code3VBRPacket(18, false, []int{12, 12}, 5)},
			maxlen:    512,
			padNewLen: len(code3VBRPacket(18, false, []int{12, 12}, 5)) + 29,
		},
		repacketizerOracleCase{
			name:      "pad_48_frame_vbr",
			packets:   [][]byte{code3VBRPacket(16, false, packet48VBRFrameSizes(), 6)},
			maxlen:    512,
			padNewLen: len(code3VBRPacket(16, false, packet48VBRFrameSizes(), 6)) + 43,
		},
		repacketizerOracleCase{
			name:      "pad_with_packet_extension",
			packets:   [][]byte{{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}},
			maxlen:    512,
			padNewLen: 16,
		},
		repacketizerOracleCase{
			name:      "pad_with_large_packet_extension",
			packets:   [][]byte{largeExtensionPacket},
			maxlen:    len(largeExtensionPacket) + 128,
			padNewLen: len(largeExtensionPacket) + 43,
		},
	)

	// buffer-too-small: maxlen smaller than required output.
	cases = append(cases,
		repacketizerOracleCase{
			name:    "buftoosmall_merge",
			packets: [][]byte{code0Packet(18, false, 30, 1), code0Packet(18, false, 30, 2)},
			maxlen:  3, // way too small
		},
	)

	// over-duration / TOC-mismatch rejection on cat.
	cases = append(cases,
		repacketizerOracleCase{
			name: "toc_mismatch",
			packets: [][]byte{
				code0Packet(18, false, 10, 1),
				code0Packet(20, false, 10, 2), // different config -> toc top bits differ
			},
			maxlen: 512,
		},
		repacketizerOracleCase{
			name: "over_120ms",
			packets: [][]byte{
				// config 31 = 960 samples/frame; 2 frames = 40ms; cat 4 of them = 80ms ok,
				// but cat enough to exceed 120ms. 6 frames*960 = 5760 (=120ms) ok,
				// the 7th makes it exceed.
				code3CBRPacket(31, false, 6, 8, 1),
				code0Packet(31, false, 8, 2),
			},
			maxlen: 2048,
		},
	)

	return cases
}

func packet48VBRFrameSizes() []int {
	sizes := make([]int, maxRepacketizerFrames)
	for i := range sizes {
		sizes[i] = 1
	}
	sizes[len(sizes)-1] = 2
	return sizes
}

func packetWithLargeLongExtension(payloadLen int) []byte {
	extensionLen := payloadLen + 1 // one byte for the long-extension ID
	packet := []byte{GenerateTOC(18, false, 3), 0x41}
	remainingPadding := extensionLen
	for remainingPadding > 254 {
		packet = append(packet, 255)
		remainingPadding -= 254
	}
	packet = append(packet, byte(remainingPadding))
	extension := make([]byte, extensionLen)
	extension[0] = 0x50
	for i := 0; i < payloadLen; i++ {
		extension[i+1] = byte(i*13 + 7)
	}
	packet = append(packet, 0x11, 0x22, 0x33)
	return append(packet, extension...)
}

func TestRepacketizerResetClearsOwnedPacketReferences(t *testing.T) {
	rp := NewRepacketizer()
	packetWithPadding := []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}
	if err := rp.Cat(packetWithPadding); err != nil {
		t.Fatalf("Cat(packet with padding): %v", err)
	}
	if rp.NumFrames() != 1 || rp.frames[0].len == 0 {
		t.Fatalf("Cat retained frames=%d, want one owned frame", rp.NumFrames())
	}
	if rp.paddings[0].len == 0 || !rp.hasPad {
		t.Fatalf("Cat retained paddings=%d, want one non-empty owned padding block", rp.paddings[0].len)
	}

	rp.Reset()
	if rp.NumFrames() != 0 {
		t.Fatalf("NumFrames after Reset=%d, want 0", rp.NumFrames())
	}
	if len(rp.frameBuf) != 0 {
		t.Fatalf("Reset retained frameBuf len=%d, want 0", len(rp.frameBuf))
	}
	if len(rp.padBuf) != 0 {
		t.Fatalf("Reset retained padBuf len=%d, want 0", len(rp.padBuf))
	}
	if rp.hasPad {
		t.Fatalf("Reset retained hasPad=true, want false")
	}

	packetAfterReset := []byte{0x48, 0x44, 0x55, 0x66}
	if err := rp.Cat(packetAfterReset); err != nil {
		t.Fatalf("Cat after Reset: %v", err)
	}
	out := make([]byte, len(packetAfterReset))
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("Out after Reset and Cat: %v", err)
	}
	if got := out[:n]; !bytes.Equal(got, packetAfterReset) {
		t.Fatalf("Out after Reset=%x, want %x", got, packetAfterReset)
	}
}

func TestRepacketizerCatSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name       string
		packet     []byte
		wantAllocs float64
	}{
		{name: "empty frame", packet: []byte{0x48}, wantAllocs: 0},
		{name: "owned frame", packet: []byte{0x48, 0x44, 0x55, 0x66}, wantAllocs: 0},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}, wantAllocs: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := NewRepacketizer()
			out := make([]byte, len(tt.packet)+64)
			run := func() {
				if err := rp.Cat(tt.packet); err != nil {
					panic(err)
				}
				if _, err := rp.Out(out); err != nil {
					panic(err)
				}
				rp.Reset()
			}
			run()
			if got := testing.AllocsPerRun(100, run); got != tt.wantAllocs {
				t.Fatalf("Cat/Out/Reset allocs/op=%.2f, want %.0f", got, tt.wantAllocs)
			}
		})
	}
}

func TestPacketPadSteadyStateAllocations(t *testing.T) {
	tests := []struct {
		name       string
		packet     []byte
		newLen     int
		wantAllocs float64
	}{
		{name: "plain", packet: []byte{0x48, 0x11, 0x22, 0x33}, newLen: 16, wantAllocs: 0},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}, newLen: 20, wantAllocs: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, tt.newLen)
			run := func() {
				copy(buf, tt.packet)
				if err := PacketPad(buf, len(tt.packet), tt.newLen); err != nil {
					panic(err)
				}
				if _, err := PacketUnpad(buf, tt.newLen); err != nil {
					panic(err)
				}
			}
			run()
			if got := testing.AllocsPerRun(100, run); got != tt.wantAllocs {
				t.Fatalf("PacketPad/PacketUnpad allocs/op=%.2f, want %.0f", got, tt.wantAllocs)
			}
		})
	}
}

func BenchmarkRepacketizerResetCatOut(b *testing.B) {
	for _, tt := range []struct {
		name   string
		packet []byte
	}{
		{name: "code0", packet: []byte{0x48, 0x44, 0x55, 0x66}},
		{name: "extension", packet: []byte{0x4b, 0x41, 0x06, 0x11, 0x22, 0x33, 0x0b, 0xaa, 0x50, 0xde, 0xad, 0xbe}},
	} {
		b.Run(tt.name, func(b *testing.B) {
			rp := NewRepacketizer()
			out := make([]byte, len(tt.packet)+64)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := rp.Cat(tt.packet); err != nil {
					b.Fatal(err)
				}
				if _, err := rp.Out(out); err != nil {
					b.Fatal(err)
				}
				rp.Reset()
			}
		})
	}
}

type repacketizerLibopusFixture struct {
	PacketA   string `json:"packet_a"`
	PacketB   string `json:"packet_b"`
	PacketC   string `json:"packet_c"`
	ABOut     string `json:"ab_out"`
	ACOut     string `json:"ac_out"`
	ABCOut    string `json:"abc_out"`
	ABC13     string `json:"abc_1_3"`
	PadNewLen int    `json:"pad_new_len"`
	Pad12     string `json:"pad_12"`
	UnpadLen  int    `json:"unpad_len"`
	Unpad     string `json:"unpad"`
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode %q error: %v", s, err)
	}
	return b
}

func loadRepacketizerFixture(t *testing.T) repacketizerLibopusFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/repacketizer_libopus_fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var f repacketizerLibopusFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return f
}

func TestRepacketizerParityWithLibopusFixture(t *testing.T) {
	f := loadRepacketizerFixture(t)

	a := mustDecodeHex(t, f.PacketA)
	b := mustDecodeHex(t, f.PacketB)
	c := mustDecodeHex(t, f.PacketC)

	rp := NewRepacketizer()
	out := make([]byte, 256)

	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(b); err != nil {
		t.Fatalf("cat(b): %v", err)
	}
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("out(ab): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABOut; got != want {
		t.Fatalf("out(ab)=%s want=%s", got, want)
	}

	rp.Reset()
	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(c); err != nil {
		t.Fatalf("cat(c): %v", err)
	}
	n, err = rp.Out(out)
	if err != nil {
		t.Fatalf("out(ac): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ACOut; got != want {
		t.Fatalf("out(ac)=%s want=%s", got, want)
	}

	rp.Reset()
	if err := rp.Cat(a); err != nil {
		t.Fatalf("cat(a): %v", err)
	}
	if err := rp.Cat(b); err != nil {
		t.Fatalf("cat(b): %v", err)
	}
	if err := rp.Cat(c); err != nil {
		t.Fatalf("cat(c): %v", err)
	}
	n, err = rp.Out(out)
	if err != nil {
		t.Fatalf("out(abc): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABCOut; got != want {
		t.Fatalf("out(abc)=%s want=%s", got, want)
	}
	n, err = rp.OutRange(1, 3, out)
	if err != nil {
		t.Fatalf("out_range(1,3): %v", err)
	}
	if got, want := hex.EncodeToString(out[:n]), f.ABC13; got != want {
		t.Fatalf("out_range(1,3)=%s want=%s", got, want)
	}

	buf := make([]byte, f.PadNewLen)
	copy(buf, a)
	if err := PacketPad(buf, len(a), f.PadNewLen); err != nil {
		t.Fatalf("packet_pad: %v", err)
	}
	if got, want := hex.EncodeToString(buf[:f.PadNewLen]), f.Pad12; got != want {
		t.Fatalf("packet_pad=%s want=%s", got, want)
	}
	unpaddedLen, err := PacketUnpad(buf, f.PadNewLen)
	if err != nil {
		t.Fatalf("packet_unpad: %v", err)
	}
	if got, want := unpaddedLen, f.UnpadLen; got != want {
		t.Fatalf("packet_unpad len=%d want=%d", got, want)
	}
	if got, want := hex.EncodeToString(buf[:unpaddedLen]), f.Unpad; got != want {
		t.Fatalf("packet_unpad=%s want=%s", got, want)
	}
}

func TestRepacketizerRejectsTOCMismatch(t *testing.T) {
	rp := NewRepacketizer()
	p1 := []byte{0x48, 0x11, 0x22, 0x33}
	p2 := []byte{0x78, 0x44, 0x55, 0x66}

	if err := rp.Cat(p1); err != nil {
		t.Fatalf("cat(p1): %v", err)
	}
	if err := rp.Cat(p2); err != ErrInvalidPacket {
		t.Fatalf("cat(p2)=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestRepacketizerRejectsDurationOver120ms(t *testing.T) {
	rp := NewRepacketizer()

	packet120ms := append([]byte{GenerateTOC(16, false, 3), 0x30}, make([]byte, 48)...)
	if err := rp.Cat(packet120ms); err != nil {
		t.Fatalf("cat(120ms packet): %v", err)
	}

	oneMore := []byte{GenerateTOC(16, false, 0), 0x7f}
	if err := rp.Cat(oneMore); err != ErrInvalidPacket {
		t.Fatalf("cat(extra frame)=%v want=%v", err, ErrInvalidPacket)
	}
}

func TestRepacketizerRejectsLibopusParserEnvelopeViolations(t *testing.T) {
	tests := []struct {
		name   string
		packet []byte
	}{
		{
			name:   "implicit_frame_too_large",
			packet: append([]byte{0x00}, make([]byte, maxOpusFrameBytes+1)...),
		},
		{
			name:   "code3_duration_over_120ms",
			packet: []byte{GenerateTOC(31, false, 3), 0x07},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rp := NewRepacketizer()
			if err := rp.Cat(tt.packet); err != ErrInvalidPacket {
				t.Fatalf("Cat error=%v want %v", err, ErrInvalidPacket)
			}
		})
	}
}

func TestRepacketizerPreservesPacketExtensions(t *testing.T) {
	packetAExt := mustDecodeHex(t, "4b41061122330baa50deadbe")
	packetB := mustDecodeHex(t, "48445566")
	wantAB := "4b42061122334455660baa50deadbe"

	rp := NewRepacketizer()
	if err := rp.Cat(packetAExt); err != nil {
		t.Fatalf("cat(packetAExt): %v", err)
	}
	clear(packetAExt)
	if err := rp.Cat(packetB); err != nil {
		t.Fatalf("cat(packetB): %v", err)
	}

	out := make([]byte, 64)
	n, err := rp.Out(out)
	if err != nil {
		t.Fatalf("out(ab with extensions): %v", err)
	}
	if got := hex.EncodeToString(out[:n]); got != wantAB {
		t.Fatalf("out(ab with extensions)=%s want=%s", got, wantAB)
	}
}

func TestRepacketizerExtensionOutRangeShortBuffers(t *testing.T) {
	const packetHex = "4b41061122330baa50deadbe"
	packet := mustDecodeHex(t, packetHex)
	rp := NewRepacketizer()
	if err := rp.Cat(packet); err != nil {
		t.Fatalf("cat(packet with extensions): %v", err)
	}

	// This one-frame code-3 output needs a two-byte header and three frame
	// bytes before it can carry the retained extension padding.
	for size := 0; size < len(packet); size++ {
		out := make([]byte, size)
		n, err := rp.OutRange(0, 1, out)
		if n != 0 || err != ErrBufferTooSmall {
			t.Fatalf("OutRange buffer length %d: n=%d err=%v, want ErrBufferTooSmall", size, n, err)
		}
		if got := rp.NumFrames(); got != 1 {
			t.Fatalf("OutRange buffer length %d changed repacketizer frame count to %d", size, got)
		}
	}

	out := make([]byte, len(packet))
	n, err := rp.OutRange(0, 1, out)
	if err != nil {
		t.Fatalf("OutRange after short buffers: %v", err)
	}
	if n != len(packet) || hex.EncodeToString(out[:n]) != packetHex {
		t.Fatalf("OutRange after short buffers=%s (n=%d), want %s", hex.EncodeToString(out[:n]), n, packetHex)
	}
}

func TestPacketPadPreservesPacketExtensions(t *testing.T) {
	packetAExt := mustDecodeHex(t, "4b41061122330baa50deadbe")
	wantPadded := "4b410a112233010101010baa50deadbe"
	wantUnpadded := "48112233"

	buf := make([]byte, 16)
	copy(buf, packetAExt)
	if err := PacketPad(buf, len(packetAExt), len(buf)); err != nil {
		t.Fatalf("PacketPad(packet with extensions): %v", err)
	}
	if got := hex.EncodeToString(buf); got != wantPadded {
		t.Fatalf("PacketPad(packet with extensions)=%s want=%s", got, wantPadded)
	}

	unpaddedLen, err := PacketUnpad(buf, len(buf))
	if err != nil {
		t.Fatalf("PacketUnpad(packet with extensions): %v", err)
	}
	if got, want := unpaddedLen, 4; got != want {
		t.Fatalf("PacketUnpad len=%d want=%d", got, want)
	}
	if got := hex.EncodeToString(buf[:unpaddedLen]); got != wantUnpadded {
		t.Fatalf("PacketUnpad(packet with extensions)=%s want=%s", got, wantUnpadded)
	}
}

func TestSelfDelimitedPacketPreservesPacketExtensions(t *testing.T) {
	packetAExt := mustDecodeHex(t, "4b41061122330baa50deadbe")
	wantSelfDelimited := "4b4106031122330baa50deadbe"

	selfDelimited, err := makeSelfDelimitedPacket(packetAExt)
	if err != nil {
		t.Fatalf("makeSelfDelimitedPacket(packet with extensions): %v", err)
	}
	if got := hex.EncodeToString(selfDelimited); got != wantSelfDelimited {
		t.Fatalf("makeSelfDelimitedPacket(packet with extensions)=%s want=%s", got, wantSelfDelimited)
	}

	decoded, consumed, err := decodeSelfDelimitedPacket(selfDelimited)
	if err != nil {
		t.Fatalf("decodeSelfDelimitedPacket(packet with extensions): %v", err)
	}
	if consumed != len(selfDelimited) {
		t.Fatalf("decodeSelfDelimitedPacket consumed=%d want=%d", consumed, len(selfDelimited))
	}
	if got := hex.EncodeToString(decoded); got != hex.EncodeToString(packetAExt) {
		t.Fatalf("decodeSelfDelimitedPacket(packet with extensions)=%s want=%s", got, hex.EncodeToString(packetAExt))
	}
}

func TestDecodeSelfDelimitedPacketPreservesOpaqueMalformedPadding(t *testing.T) {
	packet := mustDecodeHex(t, "4b4102112233ffff")
	selfDelimited := mustDecodeHex(t, "4b410203112233ffff")

	decoded, consumed, err := decodeSelfDelimitedPacket(selfDelimited)
	if err != nil {
		t.Fatalf("decodeSelfDelimitedPacket(malformed padding): %v", err)
	}
	if consumed != len(selfDelimited) {
		t.Fatalf("decodeSelfDelimitedPacket consumed=%d want=%d", consumed, len(selfDelimited))
	}
	if got := hex.EncodeToString(decoded); got != hex.EncodeToString(packet) {
		t.Fatalf("decodeSelfDelimitedPacket(malformed padding)=%s want=%s", got, hex.EncodeToString(packet))
	}
}

func TestPacketPadRejectsLengthBeyondInputOnNoOp(t *testing.T) {
	for _, data := range [][]byte{nil, {0x08}} {
		length := len(data) + 1
		if err := PacketPad(data, length, length); err != ErrInvalidArgument {
			t.Fatalf("PacketPad(len=%d, length=%d, newLen=%d) = %v, want %v", len(data), length, length, err, ErrInvalidArgument)
		}
	}
}
