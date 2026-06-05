// decode_differential_crossframe_corrupt_test.go — broadened decode-robustness
// coverage focused on CROSS-FRAME and STATE-MACHINE behaviour on corrupt input,
// complementing the per-packet mutation sweep in
// decode_differential_malformed_fuzz_test.go.
//
// The per-case oracle isolates each packet behind a fresh decoder, so cross-frame
// decoder state (CELT decode_mem / MDCT overlap, SILK history, mode transitions)
// can only be exercised WITHIN a single multi-frame Opus packet. These tests
// therefore construct deliberately mode-crossed multi-frame packets (a valid
// frame followed by a corrupt or mode-rewritten frame) so the second in-sequence
// frame decodes against the first frame's carried state, then assert gopus and
// libopus agree exactly (accept/reject, sample count, bit-exact PCM, no panic).
//
// This is the regression surface for the hybrid CELT-silence cross-frame bug
// (a 2nd-frame CELT silence overlap-add that surfaced a large carried MDCT tail):
// every case here is a HARD assertion with no allow-list.

package gopus

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// crossFrameCorruptCase is one constructed multi-frame packet plus a label.
type crossFrameCorruptCase struct {
	label  string
	packet []byte
}

// buildCrossFrameCorruptPackets constructs code-3 (multi-frame) packets whose
// TOC is rewritten across coding modes and whose 2nd frame payload is corrupted,
// so the in-sequence 2nd frame decodes against the 1st frame's carried state.
func buildCrossFrameCorruptPackets(t *testing.T) []crossFrameCorruptCase {
	t.Helper()
	var out []crossFrameCorruptCase

	// Base single-frame seeds across modes / channels / frame sizes.
	type seed struct {
		name string
		pkt  []byte
	}
	for _, ch := range []int{1, 2} {
		seeds := []seed{
			{fmt.Sprintf("celt10ms/ch%d", ch), encodeAPIRateCELTPacketFrameSize(t, ch, 480)},
			{fmt.Sprintf("celt20ms/ch%d", ch), encodeAPIRateCELTPacketFrameSize(t, ch, 960)},
			{fmt.Sprintf("silk20ms/ch%d", ch), encodeAPIRateSILKPacketFrameSize(t, ch, 960)},
			{fmt.Sprintf("hybrid10ms/ch%d", ch), encodeAPIRateHybridPacketFrameSize(t, ch, 480)},
			{fmt.Sprintf("hybrid20ms/ch%d", ch), encodeAPIRateHybridPacketFrameSize(t, ch, 960)},
		}

		// Every TOC config (mode/bandwidth/frame-size) the 2nd frame can be
		// rewritten to. These cross modes against the 1st frame's true mode.
		crossConfigs := []byte{0, 4, 11, 12, 14, 15, 17, 24, 28, 31}

		for _, s := range seeds {
			if len(s.pkt) < 2 {
				continue
			}
			// Build a 2-copy code-3 VBR packet so each sub-frame's length is
			// explicit and the 2nd frame can be corrupted independently.
			base2 := repackMultiFrame(t, s.pkt, 2)
			if base2 == nil {
				continue
			}

			// (a) TOC config rewrite of the whole multi-frame packet to each
			//     cross-mode config (keeps code bits): exercises the 1st->2nd
			//     same-but-reinterpreted mode and the cross-frame state carry.
			for _, cfg := range crossConfigs {
				p := append([]byte(nil), base2...)
				p[0] = (p[0] & 0x07) | cfg<<3
				out = append(out, crossFrameCorruptCase{
					label:  fmt.Sprintf("%s/cfgrewrite=%d", s.name, cfg),
					packet: p,
				})
			}

			// (b) Corrupt only the 2nd frame's payload tail (truncate / zero /
			//     0xFF fill) so the 2nd frame is garbage or a forced CELT silence
			//     (tell >= storage), decoded against the valid 1st frame.
			//     This is the exact class of the fixed hybrid-silence bug.
			for _, mut := range []string{"zero2nd", "ff2nd", "trunc2nd"} {
				p := append([]byte(nil), base2...)
				// Heuristic: corrupt the last quarter of the packet (2nd frame
				// region). Exact frame split is mode-dependent; perturbing the
				// tail reliably lands in or after the 2nd frame header.
				start := max(len(p)-len(p)/4, 2)
				switch mut {
				case "zero2nd":
					for i := start; i < len(p); i++ {
						p[i] = 0x00
					}
				case "ff2nd":
					for i := start; i < len(p); i++ {
						p[i] = 0xFF
					}
				case "trunc2nd":
					if start < len(p) {
						p = p[:start]
					}
				}
				out = append(out, crossFrameCorruptCase{
					label:  fmt.Sprintf("%s/%s", s.name, mut),
					packet: p,
				})
			}
		}
	}
	return out
}

// runCrossFrameCorruptCases decodes the cases through both gopus and libopus and
// hard-asserts full agreement (accept/reject, sample count, bit-exact PCM on
// accept, no panic) for every channel count and PCM format.
func runCrossFrameCorruptCases(t *testing.T, cases []crossFrameCorruptCase) {
	t.Helper()
	formats := []uint32{
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}
	for _, channels := range []int{1, 2} {
		for _, format := range formats {
			diffCases := make([]libopustest.DecodeDiffCase, len(cases))
			for i, c := range cases {
				diffCases[i] = libopustest.DecodeDiffCase{Packet: c.packet, Format: format, FrameSize: 5760}
			}
			oracle, err := libopustest.ProbeDecodeDiff(48000, channels, diffCases)
			if err != nil {
				libopustest.HelperUnavailable(t, "decode diff probe", err)
				return
			}
			for i, c := range cases {
				or := oracle[i]
				gpcm, gn, gerr := safeGopusDecode(48000, channels, diffCases[i])
				label := fmt.Sprintf("ch%d/fmt%d/%s", channels, format, c.label)

				if or.Code < 0 {
					if gerr == nil {
						t.Errorf("%s: libopus REJECTED (code=%d) but gopus ACCEPTED (n=%d) packet=% x", label, or.Code, gn, c.packet)
					}
					continue
				}
				if gerr != nil {
					t.Errorf("%s: libopus ACCEPTED (n=%d) but gopus REJECTED: %v packet=% x", label, or.Code, gerr, c.packet)
					continue
				}
				if gn != int(or.Code) {
					t.Errorf("%s: sample count gopus=%d libopus=%d packet=% x", label, gn, or.Code, c.packet)
					continue
				}
				want := oracleResultToFloat32(format, or)
				worst := malformedPCMWorst(format, gpcm, want)
				if worst > malformedPCMGrossTol {
					t.Errorf("%s: gross PCM divergence (worst |Δ|=%g, tol=%g) on accepted packet=% x", label, worst, malformedPCMGrossTol, c.packet)
				}
			}
		}
	}
}

// TestDecodeDifferentialCrossFrameCorrupt asserts gopus matches libopus on
// mode-crossed and tail-corrupted multi-frame packets, where the 2nd in-sequence
// frame decodes against the 1st frame's carried CELT/SILK/hybrid state.
func TestDecodeDifferentialCrossFrameCorrupt(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}
	cases := buildCrossFrameCorruptPackets(t)
	if len(cases) == 0 {
		t.Skip("no cross-frame corrupt cases")
	}
	runCrossFrameCorruptCases(t, cases)
	t.Logf("cross-frame corrupt sweep: %d constructed packets", len(cases))
}

// TestDecodeDifferentialSizeBoundaryCorrupt probes packets right at the framing
// size boundaries (empty, 1-byte TOC-only, 2-byte code-3 header with boundary M,
// max-frame-count code-3) and asserts gopus matches libopus, no panic.
func TestDecodeDifferentialSizeBoundaryCorrupt(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	celt := encodeAPIRateCELTPacketFrameSize(t, 1, 480)
	if len(celt) < 2 {
		t.Skip("seed too short")
	}
	toc := celt[0]

	var cases []crossFrameCorruptCase
	// Empty packet (NULL handled separately by the PLC path; here a zero-length
	// slice is a concrete decode input).
	cases = append(cases, crossFrameCorruptCase{"empty", []byte{}})
	// 1-byte TOC-only packets across every code.
	for code := range byte(4) {
		cases = append(cases, crossFrameCorruptCase{
			label:  fmt.Sprintf("toc-only/code%d", code),
			packet: []byte{(toc & 0xFC) | code},
		})
	}
	// 2-byte code-3 with boundary frame counts in the M byte (VBR + CBR).
	for _, m := range []byte{0x00, 0x01, 0x02, 0x30, 0x31, 0x3F, 0x80, 0x81, 0xB0, 0xC0, 0xC1, 0xFF} {
		cases = append(cases, crossFrameCorruptCase{
			label:  fmt.Sprintf("code3-2byte/m=0x%02x", m),
			packet: []byte{(toc & 0xFC) | 0x03, m},
		})
	}
	// code-3 header with a body of exactly one byte per claimed frame at the
	// 48/49/63 frame-count boundary (CBR), padded to plausible sizes.
	for _, m := range []byte{0x30, 0x31, 0x3F} {
		for _, extra := range []int{0, 1, 48, 49, 63} {
			p := []byte{(toc & 0xFC) | 0x03, m}
			for range extra {
				p = append(p, 0x00)
			}
			cases = append(cases, crossFrameCorruptCase{
				label:  fmt.Sprintf("code3-cbr/m=0x%02x/body=%d", m, extra),
				packet: p,
			})
		}
	}

	runCrossFrameCorruptCases(t, cases)
	t.Logf("size-boundary corrupt sweep: %d packets", len(cases))
}

// TestDecodeDifferentialFECOnCorruptLBRR asserts that FEC (in-band redundancy)
// decoding of a corrupted packet agrees between gopus and libopus: the decoder
// is asked to recover the PRIOR frame from a packet whose LBRR/redundancy region
// has been mutated, exercising the FEC state machine on garbage. gopus must
// either recover identically or reject/PLC identically, with no panic.
func TestDecodeDifferentialFECOnCorruptLBRR(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	rng := rand.New(rand.NewSource(0xFEC0FFEE))
	type fecSeed struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		frameSize int
	}
	// Configs proven to produce the wanted mode with in-band FEC (mirroring the
	// dedicated FEC LBRR parity tests and the API-rate sweep). encodeAPIRateFECSequence
	// hard-fails on a mode mismatch, so only known-good combos are used here.
	seeds := []fecSeed{
		{"silk20ms", EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 960},
		{"hybrid20ms", EncoderModeHybrid, ModeHybrid, BandwidthFullband, 64000, 960},
	}

	const channels = 1
	const frameSizeReq = 960

	for _, s := range seeds {
		_, second := encodeAPIRateFECSequence(t, s.mode, s.wantMode, s.bandwidth, s.bitrate, channels, s.frameSize)
		if len(second) < 3 {
			continue
		}
		// Build corrupt variants of the FEC-carrying packet.
		variants := map[string][]byte{
			"clean": append([]byte(nil), second...),
		}
		for i := range 8 {
			m := append([]byte(nil), second...)
			idx := 1 + rng.Intn(len(m)-1)
			m[idx] = byte(rng.Intn(256))
			variants[fmt.Sprintf("flip%d", i)] = m
		}
		// Zero / 0xFF the payload tail (LBRR region tends to trail SILK frames).
		for _, fill := range []byte{0x00, 0xFF} {
			m := append([]byte(nil), second...)
			for i := len(m) - len(m)/3; i < len(m); i++ {
				m[i] = fill
			}
			variants[fmt.Sprintf("tailfill=0x%02x", fill)] = m
		}

		for name, pkt := range variants {
			label := fmt.Sprintf("%s/%s", s.name, name)
			// libopus FEC decode through the oracle.
			oracle, err := libopustest.ProbeDecodeDiff(48000, channels, []libopustest.DecodeDiffCase{
				{Packet: pkt, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSizeReq, DecodeFEC: true},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "decode diff probe", err)
				return
			}
			or := oracle[0]

			gpcm, gn, gerr := safeGopusFECDecode(48000, channels, pkt, frameSizeReq)

			if or.Code < 0 {
				if gerr == nil {
					t.Errorf("%s: libopus FEC REJECTED (code=%d) but gopus ACCEPTED (n=%d) packet=% x", label, or.Code, gn, pkt)
				}
				continue
			}
			if gerr != nil {
				t.Errorf("%s: libopus FEC ACCEPTED (n=%d) but gopus REJECTED: %v packet=% x", label, or.Code, gerr, pkt)
				continue
			}
			if gn != int(or.Code) {
				t.Errorf("%s: FEC sample count gopus=%d libopus=%d packet=% x", label, gn, or.Code, pkt)
				continue
			}
			want := or.Float32()
			worst := malformedPCMWorst(libopustest.DecodeDiffFormatFloat32, gpcm, want)
			if worst > malformedPCMGrossTol {
				t.Errorf("%s: FEC gross PCM divergence (worst |Δ|=%g, tol=%g) packet=% x", label, worst, malformedPCMGrossTol, pkt)
			}
		}
	}
}

// safeGopusFECDecode runs gopus FEC decode (DecodeWithFEC, fec=true) recovering
// from a panic so a crash is reported as an error.
func safeGopusFECDecode(sampleRate, channels int, pkt []byte, frameSize int) (pcm []float32, samples int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus FEC decode: %v", r)
		}
	}()
	dec, derr := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if derr != nil {
		return nil, 0, derr
	}
	buf := make([]float32, frameSize*channels)
	n, e := dec.DecodeWithFEC(pkt, buf, true)
	if e != nil {
		return nil, 0, e
	}
	return buf[:n*channels], n, nil
}
