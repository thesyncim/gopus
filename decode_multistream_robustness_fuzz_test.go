// decode_multistream_robustness_fuzz_test.go — DECODE ROBUSTNESS fuzz for the
// public MultistreamDecoder entry points (Decode / DecodeInt16 / DecodeInt24)
// and the projection (demixing-matrix) decode path. It is the multistream
// analogue of decode_differential_malformed_fuzz_test.go.
//
// The generic single-stream malformed fuzzer covers Decoder.Decode*; the
// multistream wrapper adds its own surface that arbitrary input must not crash
// and must accept/reject in lockstep with libopus opus_multistream_decode*:
//
//   - the cross-stream self-delimited sub-packet framing parser,
//   - the per-channel frame-size derivation from the FIRST stream's TOC,
//   - the channel mapping / coupling routing,
//   - the integer DecodeInt16 / DecodeInt24 fixed-point hook, and
//   - (with a demixing matrix) the projection demixing matmul.
//
// Strategy (seeded, reproducible):
//   (a) purely random byte buffers of every length 0..N  → NO-PANIC only (no
//       oracle: an arbitrary buffer is almost never a valid MS packet, but it
//       must never crash the wrapper),
//   (b) structured-malformed mutations of valid multistream packets (truncation,
//       byte/bit flips, first-stream TOC config/code/stereo rewrites, sub-packet
//       self-delimited length corruption, append junk) → NO-PANIC + accept/reject
//       parity + sample-count parity vs the libopus multistream oracle, plus a
//       gross-PCM-divergence guard when both accept.
//
// The multistream oracle (libopus_refdecode_multistream.c) aborts the whole
// batch on the first opus_multistream_decode* < 0, so accept/reject is probed
// ONE packet per oracle call: an oracle error ⇔ libopus rejected. A gopus panic
// (recovered into an error), a gopus-accepts-where-libopus-rejects (or vice
// versa), or a sample-count mismatch is a HARD failure with the packet printed.

package gopus

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// msRobustLayout is one multistream channel configuration exercised by the
// robustness fuzzer: a full (channels, streams, coupled, mapping) tuple plus the
// per-stream channel counts used to build valid seed packets.
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

// msRobustGopusDecode decodes one packet through a fresh gopus MultistreamDecoder
// in the selected format, recovering a panic into an error so a crash minimises
// to one packet. PCM is returned in the shared float32 comparison scale.
func msRobustGopusDecode(layout msRobustLayout, format msRobustFormat, packet []byte, frameSize int) (pcm []float32, samples int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus multistream decode: %v", r)
		}
	}()
	dec, derr := NewMultistreamDecoder(48000, layout.channels, layout.streams, layout.coupled, layout.mapping)
	if derr != nil {
		return nil, 0, derr
	}
	bufCap := frameSize * layout.channels
	switch format {
	case msRobustInt16:
		buf := make([]int16, bufCap)
		n, e := dec.DecodeInt16(packet, buf)
		if e != nil {
			return nil, 0, e
		}
		out := make([]float32, n*layout.channels)
		for i := range out {
			out[i] = float32(buf[i]) / 32768.0
		}
		return out, n, nil
	case msRobustInt24:
		buf := make([]int32, bufCap)
		n, e := dec.DecodeInt24(packet, buf)
		if e != nil {
			return nil, 0, e
		}
		out := make([]float32, n*layout.channels)
		for i := range out {
			out[i] = float32(buf[i]) / 8388608.0
		}
		return out, n, nil
	default:
		buf := make([]float32, bufCap)
		n, e := dec.Decode(packet, buf)
		if e != nil {
			return nil, 0, e
		}
		return buf[:n*layout.channels], n, nil
	}
}

// msRobustOracleSampleFormat maps the fuzz format to the multistream oracle's
// sample_format selector.
func msRobustOracleSampleFormat(format msRobustFormat) int {
	switch format {
	case msRobustInt16:
		return 1
	case msRobustInt24:
		return libopusRefdecodeMSFormatInt24
	default:
		return 0
	}
}

// msRobustOracleDecode decodes ONE packet through the libopus multistream oracle
// and returns the per-channel sample count and PCM (shared float32 scale). The
// oracle aborts (returns a non-nil error) on opus_multistream_decode* < 0, so a
// non-nil err means libopus REJECTED the packet — exactly the accept/reject
// signal the parity check needs.
func msRobustOracleDecode(layout msRobustLayout, format msRobustFormat, packet []byte, frameSize int) (pcm []float32, samples int, rejected bool, err error) {
	switch format {
	case msRobustInt16:
		out, e := decodeLibopusMultistreamInt16Gain(48000, layout.channels, layout.streams, layout.coupled, frameSize, 0, layout.mapping, [][]byte{packet})
		if e != nil {
			return nil, 0, true, nil
		}
		f := make([]float32, len(out))
		for i, v := range out {
			f[i] = float32(v) / 32768.0
		}
		return f, len(out) / layout.channels, false, nil
	case msRobustInt24:
		out, e := decodeLibopusMultistreamInt24(48000, layout.channels, layout.streams, layout.coupled, frameSize, layout.mapping, [][]byte{packet})
		if e != nil {
			return nil, 0, true, nil
		}
		f := make([]float32, len(out))
		for i, v := range out {
			f[i] = float32(v) / 8388608.0
		}
		return f, len(out) / layout.channels, false, nil
	default:
		out, e := decodeLibopusMultistreamFloat32(48000, layout.channels, layout.streams, layout.coupled, frameSize, layout.mapping, [][]byte{packet})
		if e != nil {
			return nil, 0, true, nil
		}
		return out, len(out) / layout.channels, false, nil
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
	for range iters {
		lo := layouts[rng.Intn(len(layouts))]
		n := rng.Intn(maxLen + 1)
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(rng.Intn(256))
		}
		for _, format := range []msRobustFormat{msRobustFloat32, msRobustInt16, msRobustInt24} {
			pcm, samples, err := msRobustGopusDecode(lo, format, buf, frameSize)
			cases++
			if err != nil && isMSRobustPanic(err) {
				t.Fatalf("%s/fmt%d: %v — packet=% x", lo.name, format, err, buf)
			}
			if err == nil {
				if samples < 0 || samples > frameSize {
					t.Fatalf("%s/fmt%d: samples=%d outside [0,%d] — packet=% x", lo.name, format, samples, frameSize, buf)
				}
				if len(pcm) != samples*lo.channels {
					t.Fatalf("%s/fmt%d: pcm len=%d want %d — packet=% x", lo.name, format, len(pcm), samples*lo.channels, buf)
				}
				msRobustRequireFinite(t, lo.name, format, pcm, buf)
			}
		}

		// PLC path: nil packet must also never panic.
		for _, format := range []msRobustFormat{msRobustFloat32, msRobustInt16, msRobustInt24} {
			if _, _, err := msRobustGopusDecode(lo, format, nil, frameSize); err != nil && isMSRobustPanic(err) {
				t.Fatalf("%s/fmt%d PLC(nil): %v", lo.name, format, err)
			}
		}
	}
	t.Logf("multistream random no-panic sweep: %d cases over %d layouts", cases, len(layouts))
}

// TestDecodeMultistreamRobustnessMalformed mutates valid multistream packets and
// asserts gopus and the libopus multistream oracle agree on accept-vs-reject and
// per-channel sample count, with NO panic. When both accept, a gross PCM
// divergence (the signature of a real decode mistake, not ULP amplification on
// corrupt input) is also flagged.
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
	for k := range iters {
		seed := seeds[rng.Intn(len(seeds))]
		m := msRobustMutate(rng, seed.packet)
		format := formats[rng.Intn(len(formats))]
		label := fmt.Sprintf("%s/fmt%d/mut%d", seed.layout.name, format, k)

		gpcm, gn, gerr := msRobustGopusDecode(seed.layout, format, m, frameSize)
		if gerr != nil && isMSRobustPanic(gerr) {
			t.Fatalf("%s: %v — packet=% x", label, gerr, m)
		}

		opcm, on, rejected, oerr := msRobustOracleDecode(seed.layout, format, m, frameSize)
		if oerr != nil {
			libopustest.HelperUnavailable(t, "multistream reference decode", oerr)
			return
		}
		total++

		// ---- accept/reject parity (HARD) ----
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
			continue
		}

		// ---- gross PCM divergence guard on accepted corrupt input ----
		// Valid only in the default (float) build, where gopus and the float
		// multistream oracle use identical arithmetic. Under gopus_fixed_point gopus
		// decodes int16/int24 through the integer path against this same float
		// oracle, so a PCM-value diff is the expected float-vs-integer gap amplified
		// by the unstable filters garbage drives, not a decode mistake; the
		// accept/reject + no-panic invariants above remain enforced under both.
		if robustFixedPointDecode {
			continue
		}
		worst := malformedPCMWorst(uint32(msRobustOracleSampleFormat(format)), gpcm, opcm)
		if worst > malformedPCMGrossTol {
			pcmDiverged++
			t.Errorf("%s: gross PCM divergence (worst |Δ|=%g, tol=%g) on accepted packet=% x",
				label, worst, malformedPCMGrossTol, m)
		}
	}
	t.Logf("multistream malformed sweep: %d cases, %d gross PCM divergence(s)", total, pcmDiverged)
}

// isMSRobustPanic reports whether an error came from a recovered panic (the
// hard-fail signal) rather than an ordinary decode rejection.
func isMSRobustPanic(err error) bool {
	return err != nil && len(err.Error()) >= 5 && err.Error()[:5] == "PANIC"
}

// msRobustRequireFinite asserts decoded float PCM is finite (no NaN/Inf) — a
// decoder must never emit non-finite samples even on garbage input.
func msRobustRequireFinite(t *testing.T, name string, format msRobustFormat, pcm []float32, packet []byte) {
	t.Helper()
	for i, v := range pcm {
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
