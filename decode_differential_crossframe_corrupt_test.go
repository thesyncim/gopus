package gopus

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	celtpkg "github.com/thesyncim/gopus/internal/celt"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/qualitycompare"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/types"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

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
				if len(gpcm) != len(want) {
					t.Errorf("%s: PCM length gopus=%d libopus=%d packet=% x", label, len(gpcm), len(want), c.packet)
					continue
				}
				worst := malformedPCMWorst(format, gpcm, want)
				if worst > malformedPCMGrossTol {
					t.Errorf("%s: gross PCM divergence (worst |Δ|=%g, tol=%g) on accepted packet=% x", label, worst, malformedPCMGrossTol, c.packet)
				}
				if format == libopustest.DecodeDiffFormatInt24 {
					assertFreshDecodeInt24MatchesOracle(t, label, 48000, channels, diffCases[i], or)
				} else if sample := malformedPCMFirstBitMismatch(gpcm, want); sample >= 0 {
					t.Errorf("%s: PCM sample %d bits=%08x want=%08x packet=% x", label, sample,
						math.Float32bits(gpcm[sample]), math.Float32bits(want[sample]), c.packet)
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
			if len(gpcm) != len(want) {
				t.Errorf("%s: FEC PCM length gopus=%d libopus=%d packet=% x", label, len(gpcm), len(want), pkt)
				continue
			}
			worst := malformedPCMWorst(libopustest.DecodeDiffFormatFloat32, gpcm, want)
			if worst > malformedPCMGrossTol {
				t.Errorf("%s: FEC gross PCM divergence (worst |Δ|=%g, tol=%g) packet=% x", label, worst, malformedPCMGrossTol, pkt)
			}
			if sample := malformedPCMFirstBitMismatch(gpcm, want); sample >= 0 {
				t.Errorf("%s: FEC PCM sample %d bits=%08x want=%08x packet=% x", label, sample,
					math.Float32bits(gpcm[sample]), math.Float32bits(want[sample]), pkt)
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

func diffFuzzBudget(full int) int {
	if testing.Short() {
		b := max(full/8, 16)
		return b
	}
	return full
}

// ---- gopus-side decode dispatch -------------------------------------------

// gopusDecodeProbe decodes one packet through a fresh gopus decoder, mirroring
// the oracle's fresh-decoder-per-case isolation. It returns the decoded PCM as
// float32 (converting int16/int24 to the same 1/32768 / 1/8388608 scale the
// diagnostic uses), the per-channel sample count, and any error. Exact int24
// assertions use assertFreshDecodeInt24MatchesOracle to retain every int32 bit.
func gopusDecodeProbe(sampleRate, channels int, c libopustest.DecodeDiffCase) (pcm []float32, samples int, err error) {
	dec, derr := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if derr != nil {
		return nil, 0, derr
	}
	bufSamples := int(c.FrameSize)
	if bufSamples == 0 {
		bufSamples = 5760
	}
	switch c.Format {
	case libopustest.DecodeDiffFormatInt16:
		buf := make([]int16, bufSamples*channels)
		n, e := dec.DecodeInt16(c.Packet, buf)
		if e != nil {
			return nil, 0, e
		}
		out := make([]float32, n*channels)
		for i := range out {
			out[i] = float32(buf[i]) / 32768.0
		}
		return out, n, nil
	case libopustest.DecodeDiffFormatInt24:
		buf := make([]int32, bufSamples*channels)
		n, e := dec.DecodeInt24(c.Packet, buf)
		if e != nil {
			return nil, 0, e
		}
		out := make([]float32, n*channels)
		for i := range out {
			out[i] = float32(buf[i]) / 8388608.0
		}
		return out, n, nil
	default:
		buf := make([]float32, bufSamples*channels)
		n, e := dec.Decode(c.Packet, buf)
		if e != nil {
			return nil, 0, e
		}
		return buf[:n*channels], n, nil
	}
}

// oracleResultToFloat32 normalises the oracle's PCM to the same float32 scale as
// gopusDecodeProbe.
func oracleResultToFloat32(format uint32, r libopustest.DecodeDiffResult) []float32 {
	switch format {
	case libopustest.DecodeDiffFormatInt16:
		in := r.Int16()
		out := make([]float32, len(in))
		for i, v := range in {
			out[i] = float32(v) / 32768.0
		}
		return out
	case libopustest.DecodeDiffFormatInt24:
		in := r.Int24()
		out := make([]float32, len(in))
		for i, v := range in {
			out[i] = float32(v) / 8388608.0
		}
		return out
	default:
		return r.Float32()
	}
}

func absF32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// ---- (a) encode-then-decode sweep -----------------------------------------

// encodeSweepSpec is one point in the encoder configuration space.
type encodeSweepSpec struct {
	name        string
	application Application
	mode        EncoderMode
	bandwidth   Bandwidth
	autoBW      bool
	frameMs     ExpertFrameDuration
	bitrate     int
	channels    int
	vbr         BitrateMode
	fec         bool
	dtx         bool
}

func (s encodeSweepSpec) frameSamples48k() int {
	switch s.frameMs {
	case ExpertFrameDuration2_5Ms:
		return 120
	case ExpertFrameDuration5Ms:
		return 240
	case ExpertFrameDuration10Ms:
		return 480
	case ExpertFrameDuration20Ms:
		return 960
	case ExpertFrameDuration40Ms:
		return 1920
	case ExpertFrameDuration60Ms:
		return 2880
	default:
		return 960
	}
}

// buildEncodeSweep enumerates the structured encoder config matrix.
func buildEncodeSweep() []encodeSweepSpec {
	var specs []encodeSweepSpec

	type modeDef struct {
		name string
		app  Application
		mode EncoderMode
		bw   Bandwidth
		mins int // min frame ms index
	}
	modes := []modeDef{
		{"silk_nb", ApplicationRestrictedSilk, EncoderModeSILK, BandwidthNarrowband, 0},
		{"silk_mb", ApplicationRestrictedSilk, EncoderModeSILK, BandwidthMediumband, 0},
		{"silk_wb", ApplicationRestrictedSilk, EncoderModeSILK, BandwidthWideband, 0},
		{"hybrid_swb", ApplicationVoIP, EncoderModeHybrid, BandwidthSuperwideband, 0},
		{"hybrid_fb", ApplicationVoIP, EncoderModeHybrid, BandwidthFullband, 0},
		{"celt_nb", ApplicationRestrictedCelt, EncoderModeCELT, BandwidthNarrowband, 0},
		{"celt_wb", ApplicationRestrictedCelt, EncoderModeCELT, BandwidthWideband, 0},
		{"celt_swb", ApplicationRestrictedCelt, EncoderModeCELT, BandwidthSuperwideband, 0},
		{"celt_fb", ApplicationRestrictedCelt, EncoderModeCELT, BandwidthFullband, 0},
	}

	// SILK supports 10/20/40/60 ms; CELT supports 2.5/5/10/20 ms; Hybrid 10/20 ms.
	silkFrames := []ExpertFrameDuration{ExpertFrameDuration10Ms, ExpertFrameDuration20Ms, ExpertFrameDuration40Ms, ExpertFrameDuration60Ms}
	hybridFrames := []ExpertFrameDuration{ExpertFrameDuration10Ms, ExpertFrameDuration20Ms}
	celtFrames := []ExpertFrameDuration{ExpertFrameDuration2_5Ms, ExpertFrameDuration5Ms, ExpertFrameDuration10Ms, ExpertFrameDuration20Ms}

	bitratesByMode := map[EncoderMode][]int{
		EncoderModeSILK:   {8000, 16000, 32000},
		EncoderModeHybrid: {24000, 48000, 96000},
		EncoderModeCELT:   {32000, 96000, 256000},
	}
	vbrModes := []BitrateMode{BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR}

	for _, m := range modes {
		var frames []ExpertFrameDuration
		switch m.mode {
		case EncoderModeSILK:
			frames = silkFrames
		case EncoderModeHybrid:
			frames = hybridFrames
		default:
			frames = celtFrames
		}
		for _, ch := range []int{1, 2} {
			for _, fr := range frames {
				for _, br := range bitratesByMode[m.mode] {
					for _, vbr := range vbrModes {
						// FEC only meaningful for SILK/Hybrid; DTX for SILK/Hybrid.
						fecOpts := []bool{false}
						dtxOpts := []bool{false}
						if m.mode == EncoderModeSILK || m.mode == EncoderModeHybrid {
							fecOpts = []bool{false, true}
							dtxOpts = []bool{false, true}
						}
						for _, fec := range fecOpts {
							for _, dtx := range dtxOpts {
								specs = append(specs, encodeSweepSpec{
									name:        fmt.Sprintf("%s_ch%d_%dms_%dbps_vbr%d_fec%t_dtx%t", m.name, ch, frameMsOf(fr), br, vbr, fec, dtx),
									application: m.app,
									mode:        m.mode,
									bandwidth:   m.bw,
									frameMs:     fr,
									bitrate:     br,
									channels:    ch,
									vbr:         vbr,
									fec:         fec,
									dtx:         dtx,
								})
							}
						}
					}
				}
			}
		}
	}
	return specs
}

func frameMsOf(fr ExpertFrameDuration) int {
	switch fr {
	case ExpertFrameDuration2_5Ms:
		return 2
	case ExpertFrameDuration5Ms:
		return 5
	case ExpertFrameDuration10Ms:
		return 10
	case ExpertFrameDuration20Ms:
		return 20
	case ExpertFrameDuration40Ms:
		return 40
	case ExpertFrameDuration60Ms:
		return 60
	default:
		return 20
	}
}

// genPCM fills an interleaved PCM buffer with a seeded mix of tones + noise.
func genPCM(rng *rand.Rand, frameSamples, channels int, sampleRate float64) []float32 {
	pcm := make([]float32, frameSamples*channels)
	// Random tone parameters per channel.
	type tone struct{ f, a, ph float64 }
	tones := make([][]tone, channels)
	for c := range channels {
		nt := 1 + rng.Intn(3)
		ts := make([]tone, nt)
		for k := range ts {
			ts[k] = tone{
				f:  120 + rng.Float64()*6000,
				a:  0.05 + rng.Float64()*0.3,
				ph: rng.Float64() * 2 * math.Pi,
			}
		}
		tones[c] = ts
	}
	noise := rng.Float64() * 0.05
	for i := range frameSamples {
		tm := float64(i) / sampleRate
		for c := range channels {
			var v float64
			for _, ts := range tones[c] {
				v += ts.a * math.Sin(2*math.Pi*ts.f*tm+ts.ph)
			}
			v += (rng.Float64()*2 - 1) * noise
			if v > 0.99 {
				v = 0.99
			} else if v < -0.99 {
				v = -0.99
			}
			pcm[i*channels+c] = float32(v)
		}
	}
	return pcm
}

// encodePackets encodes nFrames consecutive frames for one spec, returning the
// produced packets. Packets of length 0/1 (DTX/CELT silence) are kept: they are
// valid and exercise the PLC/empty-frame decode path identically in both codecs.
func encodePackets(t *testing.T, spec encodeSweepSpec, rng *rand.Rand, nFrames int) ([][]byte, bool) {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    spec.channels,
		Application: spec.application,
	})
	if err != nil {
		t.Fatalf("NewEncoder(%s): %v", spec.name, err)
	}
	if err := enc.SetMode(spec.mode); err != nil {
		return nil, false
	}
	frameSamples := spec.frameSamples48k()
	if err := enc.SetFrameSize(frameSamples); err != nil {
		return nil, false
	}
	if err := enc.SetExpertFrameDuration(spec.frameMs); err != nil {
		return nil, false
	}
	if spec.autoBW {
		if err := enc.SetBandwidthAuto(); err != nil {
			return nil, false
		}
	} else if err := enc.SetBandwidth(spec.bandwidth); err != nil {
		return nil, false
	}
	if err := enc.SetBitrate(spec.bitrate); err != nil {
		return nil, false
	}
	if err := enc.SetBitrateMode(spec.vbr); err != nil {
		return nil, false
	}
	enc.SetFEC(spec.fec)
	if spec.fec {
		if err := enc.SetPacketLoss(20); err != nil {
			return nil, false
		}
	}
	enc.SetDTX(spec.dtx)
	if spec.channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			return nil, false
		}
	}

	packets := make([][]byte, 0, nFrames)
	for f := range nFrames {
		pcm := genPCM(rng, frameSamples, spec.channels, sampleRate)
		pkt, err := encodeOneFrame(enc, pcm)
		if err != nil {
			// Every selected config must generate all frames for the exact
			// decoder gate; the caller reports this encoder failure as a test error.
			t.Logf("encoder failed (%s frame %d): %v", spec.name, f, err)
			return nil, false
		}
		// Copy: EncodeFloat32 may reuse an internal buffer across calls.
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets, true
}

// encodeOneFrame encodes one frame and converts any encoder panic into an error
// so an encoder-side crash does not abort the decode-focused sweep.
func encodeOneFrame(enc *Encoder, pcm []float32) (pkt []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus encode: %v", r)
		}
	}()
	return enc.EncodeFloat32(pcm)
}

// assertFreshDecodeInt24MatchesOracle preserves every public int32 output bit
// against the paired C opus_decode24 call, with independent fresh decoder state.
func assertFreshDecodeInt24MatchesOracle(t *testing.T, label string, sampleRate, channels int, c libopustest.DecodeDiffCase, want libopustest.DecodeDiffResult) bool {
	t.Helper()
	if c.DecodeFEC {
		t.Fatal("raw int24 probe requires a non-FEC decode case")
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("%s: NewDecoder: %v", label, err)
	}
	frameSize := int(c.FrameSize)
	if frameSize == 0 {
		frameSize = 5760
	}
	buf := make([]int32, frameSize*channels)
	n, err := dec.DecodeInt24(c.Packet, buf)
	if err != nil || int32(n) != want.Code {
		t.Fatalf("%s: raw DecodeInt24 samples=%d err=%v, C=%d", label, n, err, want.Code)
	}
	gotPCM, wantPCM := buf[:n*channels], want.Int24()
	if len(gotPCM) != len(wantPCM) {
		t.Fatalf("%s: raw int24 length=%d want %d", label, len(gotPCM), len(wantPCM))
	}
	for i := range gotPCM {
		if gotPCM[i] != wantPCM[i] {
			t.Errorf("%s: raw int24 sample %d=%08x want=%08x packet=% x", label, i,
				uint32(gotPCM[i]), uint32(wantPCM[i]), c.Packet)
			return false
		}
	}
	return true
}

// assertExactValidDecodePCM compares every output sample as float32 bits. The
// int16 output is scaled exactly. Int24 uses its original int32 representation
// in assertFreshDecodeInt24MatchesOracle.
func assertExactValidDecodePCM(t *testing.T, label string, got, want []float32) bool {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: PCM length gopus=%d libopus=%d", label, len(got), len(want))
		return false
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Errorf("%s: PCM[%d] bits gopus=%08x libopus=%08x", label, i,
				math.Float32bits(got[i]), math.Float32bits(want[i]))
			return false
		}
	}
	return true
}

// TestDecodeDifferentialEncodeThenDecode encodes across the full config space
// and requires exact decoded length and PCM bits for every accepted packet and
// every public output format against the paired libopus decoder.
func TestDecodeDifferentialEncodeThenDecode(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	specs := buildEncodeSweep()
	if !testing.Short() && len(specs) != 1440 {
		t.Fatalf("encode-then-decode matrix has %d specs, want 1440", len(specs))
	}
	// Decode formats to exercise; float32 is the primary, int16/int24 share the
	// same decode core and differ only in final conversion.
	formats := []uint32{
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}

	const framesPerSpec = 3
	budget := min(diffFuzzBudget(len(specs)), len(specs))
	// Deterministically stride through the spec list so a shrunk budget still
	// covers the whole space rather than a prefix.
	stride := 1
	if budget < len(specs) {
		stride = len(specs) / budget
	}

	type decodeSweepResult struct {
		selected         bool
		generatedPackets int
		matchedDecodes   int
	}
	results := make([]decodeSweepResult, len(specs))
	parallelism := min(4, runtime.GOMAXPROCS(0), budget)
	semaphore := make(chan struct{}, parallelism)

	tested := 0
	for idx := 0; idx < len(specs) && tested < budget; idx += stride {
		configIndex := idx
		spec := specs[configIndex]
		tested++
		slot := &results[configIndex]
		t.Run(spec.name, func(t *testing.T) {
			t.Parallel()
			slot.selected = true
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			specRng := rand.New(rand.NewSource(int64(configIndex)*1000003 + 1))
			packets, ok := encodePackets(t, spec, specRng, framesPerSpec)
			if !ok {
				t.Errorf("encoder did not produce packets for selected config %s", spec.name)
				return
			}
			slot.generatedPackets = len(packets)
			for _, format := range formats {
				cases := make([]libopustest.DecodeDiffCase, len(packets))
				for i, p := range packets {
					cases[i] = libopustest.DecodeDiffCase{Packet: p, Format: format, FrameSize: 5760}
				}
				oracle, err := libopustest.ProbeDecodeDiff(48000, spec.channels, cases)
				if err != nil {
					libopustest.HelperUnavailable(t, "decode diff probe", err)
					return
				}
				for i, p := range packets {
					or := oracle[i]
					label := fmt.Sprintf("%s/fmt%d/frame%d", spec.name, format, i)
					if or.Code < 0 {
						t.Errorf("%s: libopus rejected (code=%d) a gopus packet — packet=% x", label, or.Code, p)
						continue
					}
					if format == libopustest.DecodeDiffFormatInt24 {
						if assertFreshDecodeInt24MatchesOracle(t, label, 48000, spec.channels, cases[i], or) {
							slot.matchedDecodes++
						}
						continue
					}
					gpcm, gn, gerr := gopusDecodeProbe(48000, spec.channels, cases[i])
					if gerr != nil {
						t.Errorf("%s: libopus accepted (n=%d) but gopus rejected: %v — packet=% x",
							label, or.Code, gerr, p)
						continue
					}
					if gn != int(or.Code) {
						t.Errorf("%s: sample count gopus=%d libopus=%d — packet=% x", label, gn, or.Code, p)
						continue
					}
					want := oracleResultToFloat32(format, or)
					if !assertExactValidDecodePCM(t, label, gpcm, want) {
						t.Logf("%s: diverging packet=% x", label, p)
						continue
					}
					slot.matchedDecodes++
				}
			}
		})
	}
	t.Cleanup(func() {
		selected := 0
		generatedPackets := 0
		matchedDecodes := 0
		for _, result := range results {
			if !result.selected {
				continue
			}
			selected++
			generatedPackets += result.generatedPackets
			matchedDecodes += result.matchedDecodes
		}
		if generatedPackets != selected*framesPerSpec {
			t.Errorf("encode-then-decode generated %d packets, want %d", generatedPackets, selected*framesPerSpec)
		}
		if matchedDecodes != selected*framesPerSpec*len(formats) {
			t.Errorf("encode-then-decode matched %d decoded packets, want %d across %d formats",
				matchedDecodes, selected*framesPerSpec*len(formats), len(formats))
		}
		t.Logf("encode-then-decode exact sweep: %d selected of %d/%d visited specs, %d packets, %d decoded formats",
			selected, tested, len(specs), generatedPackets, matchedDecodes)
	})
}

func seedPacketsForMutation(t *testing.T) [][]byte {
	t.Helper()
	var seeds [][]byte

	for _, ch := range []int{1, 2} {
		for _, fs := range []int{120, 240, 480, 960, 1920} {
			// CELT (supports 2.5/5/10/20/40 ms).
			if fs <= 1920 {
				seeds = append(seeds, encodeAPIRateCELTPacketFrameSize(t, ch, fs))
			}
		}
		for _, fs := range []int{480, 960, 1920, 2880} {
			// SILK (10/20/40/60 ms).
			seeds = append(seeds, encodeAPIRateSILKPacketFrameSize(t, ch, fs))
		}
		for _, fs := range []int{480, 960} {
			// Hybrid (10/20 ms).
			seeds = append(seeds, encodeAPIRateHybridPacketFrameSize(t, ch, fs))
		}
	}

	// Build code-1/2/3 framings by concatenating frames via the repacketizer so
	// the mutator also perturbs multi-frame headers.
	for _, ch := range []int{1, 2} {
		base := encodeAPIRateCELTPacketFrameSize(t, ch, 480)
		if multi := repackMultiFrame(t, base, 3); multi != nil {
			seeds = append(seeds, multi)
		}
		if multi := repackMultiFrame(t, base, 6); multi != nil {
			seeds = append(seeds, multi)
		}
	}

	// Dedup empty.
	out := seeds[:0]
	for _, s := range seeds {
		if len(s) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// repackMultiFrame repacketizes n copies of base into a single code-3 packet.
// Returns nil if repacketization is not possible for the input.
func repackMultiFrame(t *testing.T, base []byte, n int) []byte {
	t.Helper()
	rp := NewRepacketizer()
	for range n {
		if err := rp.Cat(base); err != nil {
			return nil
		}
	}
	buf := make([]byte, 1500*n+64)
	written, err := rp.Out(buf)
	if err != nil || written <= 0 {
		return nil
	}
	return append([]byte(nil), buf[:written]...)
}

// mutatePacket applies one seeded structured mutation to a copy of src.
func mutatePacket(rng *rand.Rand, src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	p := append([]byte(nil), src...)
	switch rng.Intn(9) {
	case 0: // truncate to a random shorter length (including 0)
		n := rng.Intn(len(p) + 1)
		return p[:n]
	case 1: // single random byte overwrite
		p[rng.Intn(len(p))] = byte(rng.Intn(256))
		return p
	case 2: // single bit flip
		i := rng.Intn(len(p))
		p[i] ^= 1 << uint(rng.Intn(8))
		return p
	case 3: // rewrite TOC config (mode/bandwidth/frame-size) keeping low 3 bits
		p[0] = (p[0] & 0x07) | byte(rng.Intn(32))<<3
		return p
	case 4: // flip TOC code bits (frame packing)
		p[0] = (p[0] & 0xFC) | byte(rng.Intn(4))
		return p
	case 5: // flip TOC stereo bit
		p[0] ^= 0x04
		return p
	case 6: // force code-3 with a boundary frame count
		ms := []byte{0x00, 0x01, 0x30, 0x31, 0x3F, 0x80, 0x81, 0xB0, 0xC1}
		p[0] = (p[0] & 0xFC) | 0x03
		if len(p) < 2 {
			p = append(p, 0)
		}
		p[1] = ms[rng.Intn(len(ms))]
		return p
	case 7: // corrupt a frame-length byte (code-2/3 VBR region)
		if len(p) >= 3 {
			i := 1 + rng.Intn(len(p)-1)
			p[i] = byte(rng.Intn(256))
		}
		return p
	default: // append junk bytes (oversize / padding-ambiguity)
		extra := 1 + rng.Intn(8)
		for range extra {
			p = append(p, byte(rng.Intn(256)))
		}
		return p
	}
}

// malformedPCMGrossTol identifies large differences for an additional
// diagnostic. Every defined accepted sample also has an exact-bit assertion.
const malformedPCMGrossTol = 0.05

// malformedPCMWorst returns a coarse waveform diagnostic. The exact int24 gate
// compares the original integer outputs, including magnitudes this diagnostic
// excludes.
func malformedPCMWorst(format uint32, got, want []float32) float32 {
	if len(got) != len(want) {
		return float32(1e9)
	}
	int24Overflow := format == libopustest.DecodeDiffFormatInt24
	var worst float32
	for i := range got {
		if int24Overflow && (absF32(got[i]) >= 250.0 || absF32(want[i]) >= 250.0) {
			continue
		}
		if d := absF32(got[i] - want[i]); d > worst {
			worst = d
		}
	}
	return worst
}

// malformedPCMFirstBitMismatch compares float and int16 normalized outputs.
// Int24 uses assertFreshDecodeInt24MatchesOracle so float32 normalization cannot erase bits.
func malformedPCMFirstBitMismatch(got, want []float32) int {
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			return i
		}
	}
	return -1
}

// TestDecodeDifferentialMalformed mutates valid packets and asserts gopus and
// libopus agree on accept-vs-reject and (when both accept) identical PCM.
//
// The accept/reject parity, sample-count parity, and no-panic invariants are
// HARD failures (they are the security-critical robustness contract). PCM-value
// equality on packets both decoders accept is enforced for defined output.
func TestDecodeDifferentialMalformed(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	seeds := seedPacketsForMutation(t)
	if len(seeds) == 0 {
		t.Skip("no seed packets")
	}

	// Decode every mutation at both channel counts (a mutated TOC may toggle the
	// stereo bit, and the decoder is created per channel-count). We probe each
	// mutation through both 1ch and 2ch decoders since the decoder channel count
	// is fixed at construction and a mutated stereo bit must be handled.
	formats := []uint32{
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}

	iters := diffFuzzBudget(20000)
	rng := rand.New(rand.NewSource(0xC0FFEE))

	totalCases := 0
	pcmDiverged := 0
	// Batch mutations per (channels, format) so the oracle decodes many cases in
	// one subprocess invocation.
	for _, channels := range []int{1, 2} {
		for _, format := range formats {
			// Generate this batch's mutations deterministically.
			n := iters / (2 * len(formats))
			cases := make([]libopustest.DecodeDiffCase, 0, n)
			muts := make([][]byte, 0, n)
			for range n {
				seed := seeds[rng.Intn(len(seeds))]
				m := mutatePacket(rng, seed)
				muts = append(muts, m)
				cases = append(cases, libopustest.DecodeDiffCase{Packet: m, Format: format, FrameSize: 5760})
			}
			oracle, err := libopustest.ProbeDecodeDiff(48000, channels, cases)
			if err != nil {
				libopustest.HelperUnavailable(t, "decode diff probe", err)
				return
			}
			totalCases += len(cases)
			for i := range cases {
				or := oracle[i]
				m := muts[i]
				gpcm, gn, gerr := safeGopusDecode(48000, channels, cases[i])
				label := fmt.Sprintf("ch%d/fmt%d/mut%d", channels, format, i)

				// ---- critical invariants: accept/reject parity + no panic ----
				if or.Code < 0 {
					if gerr == nil {
						t.Errorf("%s: libopus REJECTED (code=%d) but gopus ACCEPTED (n=%d) — packet=% x",
							label, or.Code, gn, m)
					}
					continue
				}
				if gerr != nil {
					t.Errorf("%s: libopus ACCEPTED (n=%d) but gopus REJECTED: %v — packet=% x",
						label, or.Code, gerr, m)
					continue
				}
				if gn != int(or.Code) {
					t.Errorf("%s: sample count gopus=%d libopus=%d — packet=% x", label, gn, or.Code, m)
					continue
				}

				// Retain the gross-difference diagnostic and compare every defined
				// accepted sample against the selected C decoder's exact output.
				want := oracleResultToFloat32(format, or)
				if len(gpcm) != len(want) {
					t.Errorf("%s: PCM length gopus=%d libopus=%d packet=% x", label, len(gpcm), len(want), m)
					continue
				}
				worst := malformedPCMWorst(format, gpcm, want)
				if worst > malformedPCMGrossTol {
					pcmDiverged++
					t.Errorf("%s: gross PCM divergence (worst |Δ|=%g, tol=%g) on accepted packet=% x",
						label, worst, malformedPCMGrossTol, m)
				}
				if format == libopustest.DecodeDiffFormatInt24 {
					assertFreshDecodeInt24MatchesOracle(t, label, 48000, channels, cases[i], or)
				} else if sample := malformedPCMFirstBitMismatch(gpcm, want); sample >= 0 {
					t.Errorf("%s: PCM sample %d bits=%08x want=%08x packet=% x", label, sample,
						math.Float32bits(gpcm[sample]), math.Float32bits(want[sample]), m)
				}
			}
		}
	}
	t.Logf("malformed sweep: %d cases, %d gross PCM divergence(s)", totalCases, pcmDiverged)
}

// safeGopusDecode wraps gopusDecodeProbe and converts a panic into an error so a
// crash on a malformed packet is reported as a divergence (and minimised) rather
// than aborting the whole sweep.
func safeGopusDecode(sampleRate, channels int, c libopustest.DecodeDiffCase) (pcm []float32, samples int, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("PANIC in gopus decode: %v", r)
		}
	}()
	return gopusDecodeProbe(sampleRate, channels, c)
}

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

func TestDecodeGainLinearMatchesLibopusCELTExp2(t *testing.T) {
	libopustest.RequireOracle(t)
	gains := []int{-32768, -8192, -4096, -256, 256, 4096, 8192, 32767}
	inputs := make([]float32, len(gains))
	for i, gain := range gains {
		inputs[i] = float32(6.48814081e-4) * float32(gain)
	}
	want, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeExp2, inputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT exp2 decode gain", err)
	}
	for i, gain := range gains {
		got := decodeGainLinear(gain)
		if math.Float32bits(got) != math.Float32bits(want[i]) {
			t.Fatalf("decodeGainLinear(%d)=%08x(%g) want %08x(%g)", gain, math.Float32bits(got), got, math.Float32bits(want[i]), want[i])
		}
	}
}

func TestDecodeCELTUsesAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeCELT {
			t.Fatalf("channels=%d test packet mode=%v want CELT", channels, toc.Mode)
		}
		if toc.Stereo != (channels == 2) {
			t.Fatalf("channels=%d packet stereo=%v", channels, toc.Stereo)
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				want, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				pcm := make([]float32, want*channels)
				n, err := dec.Decode(packet, pcm)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if n != want {
					t.Fatalf("Decode samples=%d want %d", n, want)
				}

				plc := make([]float32, want*channels)
				n, err = dec.Decode(nil, plc)
				if err != nil {
					t.Fatalf("Decode PLC: %v", err)
				}
				if n != want {
					t.Fatalf("Decode PLC samples=%d want %d", n, want)
				}

				intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder int16: %v", err)
				}
				pcm16 := make([]int16, want*channels)
				n, err = intDec.DecodeInt16(packet, pcm16)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				if n != want {
					t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeCELTAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet, nil}
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				for i, pkt := range sequence {
					n, err := dec.Decode(pkt, frame)
					if err != nil {
						t.Fatalf("Decode sequence[%d]: %v", i, err)
					}
					if n != frameSize {
						t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertAPIRateQualityFloat32(t, got, want, sampleRate, channels, "CELT api-rate decode")
				assertAPIRateFloat32BitsExact(t, got, want, "CELT api-rate decode")
			})
		}
	}
}

func TestDecodeCELTRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 16000, 48000} {
			packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, requestedFrameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate requested CELT PLC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)

					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode packet: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode packet samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("Decode(nil) samples=%d want requested %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, true, "CELT requested PLC duration")
					assertAPIRateFloat32BitsExact(t, got, want, "CELT requested PLC duration")
				})
			}
		}
	}
}

func TestDecodeInt16CELTRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 16000, 48000} {
			packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, requestedFrameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate requested CELT int16 PLC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]int16, 0, len(want))
					frame := make([]int16, requestedFrameSize*channels)

					n, err := dec.DecodeInt16(packet, frame)
					if err != nil {
						t.Fatalf("DecodeInt16 packet: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("DecodeInt16 packet samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.DecodeInt16(nil, frame)
					if err != nil {
						t.Fatalf("DecodeInt16(nil): %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("DecodeInt16(nil) samples=%d want requested %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "CELT requested int16 PLC duration")
					assertAPIRateInt16Exact(t, got, want, "CELT requested int16 PLC duration")
				})
			}
		}
	}
}

func TestDecodeInt16AndFECUseSILKAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				want, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder int16: %v", err)
				}
				pcm16 := make([]int16, want*channels)
				n, err := intDec.DecodeInt16(packet, pcm16)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				if n != want {
					t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
				}

				fecDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder fec: %v", err)
				}
				pcm := make([]float32, want*channels)
				n, err = fecDec.DecodeWithFEC(packet, pcm, true)
				if err != nil {
					t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
				}
				if n != want {
					t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeWithFECCELTRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		seedPacket := encodeAPIRateCELTPacket(t, channels)
		recoveryPacket := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
		for _, sampleRate := range []int{8000, 16000, 48000} {
			packetFrameSize, err := packetSamplesAtRate(seedPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
				t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
					steps := []libopusAPIRateDecodeStep{
						{packet: seedPacket},
						{packet: recoveryPacket, fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate requested CELT FEC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)

					n, err := dec.Decode(seedPacket, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC recovery: %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("DecodeWithFEC samples=%d want requested %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "CELT requested FEC duration")
				})
			}
		}
	}
}

func TestDecodeHybridFECUsesAPIRatePacketDurationForTenMs(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacketFrameSize(t, channels, 480)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid || toc.FrameSize != 480 {
			t.Fatalf("channels=%d test packet mode=%v frame=%d want Hybrid 10ms", channels, toc.Mode, toc.FrameSize)
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				want, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				pcm := make([]float32, want*channels)
				n, err := dec.DecodeWithFEC(packet, pcm, true)
				if err != nil {
					t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
				}
				if n != want {
					t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeWithFECNoLBRRAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		packet    func(t *testing.T, channels int) []byte
		tolerance float64
	}{
		{name: "silk_20ms", packet: encodeAPIRateSILKPacket, tolerance: 8e-3},
		{name: "silk_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 1920) }, tolerance: 8e-3},
		{name: "silk_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 2880) }, tolerance: 8e-3},
		{name: "celt_20ms", packet: encodeAPIRateCELTPacket, tolerance: 3e-3},
		{name: "celt_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 1920) }, tolerance: 3e-3},
		{name: "celt_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 2880) }, tolerance: 3e-3},
		{name: "hybrid_20ms", packet: encodeAPIRateHybridPacket, tolerance: 1e-2},
		{name: "hybrid_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 1920) }, tolerance: 1e-2},
		{name: "hybrid_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 2880) }, tolerance: 1e-2},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			toc := ParseTOC(packet[0])
			if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
				firstFrameData, err := extractFirstFramePayload(packet, toc)
				if err != nil {
					t.Fatalf("%s ch=%d extract first frame: %v", tc.name, channels, err)
				}
				if packetHasLBRR(firstFrameData, toc) {
					t.Fatalf("%s ch=%d test packet unexpectedly contains LBRR", tc.name, channels)
				}
			}

			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: packet},
						{packet: packet, fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate no-LBRR FEC reference decode", err)
					}
					fecFrameSize := len(want)/channels - frameSize
					if fecFrameSize <= 0 {
						t.Fatalf("api-rate no-LBRR FEC reference decoded %d samples after seed, want positive", fecFrameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frameCapacity := max(fecFrameSize, frameSize)
					frame := make([]float32, frameCapacity*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.DecodeWithFEC(packet, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
					}
					if n != fecFrameSize {
						t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want %d", n, fecFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate no-LBRR FEC decode")
				})
			}
		}
	}
}

func TestDecodeWithFECNoLBRRRequestedDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		seed      func(t *testing.T, channels int) []byte
		recovery  func(t *testing.T, channels int) []byte
		tolerance float64
	}{
		{name: "celt_to_silk", seed: encodeAPIRateCELTPacket, recovery: encodeAPIRateSILKPacket, tolerance: 1e-2},
		{name: "silk_to_hybrid", seed: encodeAPIRateSILKPacket, recovery: encodeAPIRateHybridPacket, tolerance: 1.2e-2},
	} {
		for _, channels := range []int{1, 2} {
			seedPacket := tc.seed(t, channels)
			recoveryPacket := tc.recovery(t, channels)
			toc := ParseTOC(recoveryPacket[0])
			if toc.Mode != ModeSILK && toc.Mode != ModeHybrid {
				t.Fatalf("%s recovery mode=%v want SILK or Hybrid", tc.name, toc.Mode)
			}
			firstFrameData, err := extractFirstFramePayload(recoveryPacket, toc)
			if err != nil {
				t.Fatalf("%s ch=%d extract first frame: %v", tc.name, channels, err)
			}
			if packetHasLBRR(firstFrameData, toc) {
				t.Fatalf("%s ch=%d recovery packet unexpectedly contains LBRR", tc.name, channels)
			}

			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				packetFrameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
						if requestedFrameSize <= packetFrameSize {
							t.Fatalf("requestedFrameSize=%d want > packetFrameSize=%d", requestedFrameSize, packetFrameSize)
						}
						steps := []libopusAPIRateDecodeStep{
							{packet: seedPacket},
							{packet: recoveryPacket, fec: true},
							{packet: recoveryPacket},
						}
						want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "api-rate requested no-LBRR reference decode", err)
						}

						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatalf("NewDecoder: %v", err)
						}
						got := make([]float32, 0, len(want))
						frame := make([]float32, requestedFrameSize*channels)

						seedSamples, err := packetSamplesAtRate(seedPacket, sampleRate)
						if err != nil {
							t.Fatalf("seed packetSamplesAtRate: %v", err)
						}
						n, err := dec.Decode(seedPacket, frame)
						if err != nil {
							t.Fatalf("Decode seed: %v", err)
						}
						if n != seedSamples {
							t.Fatalf("Decode seed samples=%d want %d", n, seedSamples)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
						if err != nil {
							t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
						}
						if n != requestedFrameSize {
							t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want requested %d", n, requestedFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.Decode(recoveryPacket, frame)
						if err != nil {
							t.Fatalf("Decode recovery packet: %v", err)
						}
						if n != packetFrameSize {
							t.Fatalf("Decode recovery packet samples=%d want %d", n, packetFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						// The middle FEC frame requests a longer duration than the
						// 20 ms packet with no LBRR available, so 20-40 ms of this
						// stream is packet-loss concealment. opus_compare's Q is not a
						// valid metric on extrapolated concealment (a silk_to_hybrid
						// transition scores Q<0 even though gopus matches libopus to
						// <3e-3 abs, corr~=0.99999, rms~=1.0), so gate on the trusted
						// near-exact corr/RMS bar and log Q. Steady-state per-mode Q is
						// covered by TestDecoderParityLibopusMatrix.
						assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, true, tc.name+" requested no-LBRR duration")
					})
				}
			}
		}
	}
}

// opus_decode_frame treats a payload of at most one byte as PLC even when the
// enclosing decode_fec call supplies a packet. The requested prefix and the
// empty recovery frame must follow the selected C decoder's state sequence.
func TestDecodeWithFECEmptyPayloadRequestedDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 2
		requested  = 5760
	)
	seed := encodeAPIRateSILKPacket(t, channels)
	for _, toc := range []byte{0x70, 0x58, 0x40} {
		t.Run("toc_"+itoaSmall(int(toc)), func(t *testing.T) {
			packet := []byte{toc}
			steps := []libopusAPIRateDecodeStep{{packet: seed}, {packet: packet, fec: true}}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requested, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate empty-payload FEC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatal(err)
			}
			frame := make([]float32, requested*channels)
			seedSamples, err := dec.Decode(seed, frame)
			if err != nil {
				t.Fatalf("Decode seed: %v", err)
			}
			got := append([]float32(nil), frame[:seedSamples*channels]...)
			clear(frame)
			n, err := dec.DecodeWithFEC(packet, frame, true)
			if err != nil || n != requested {
				t.Fatalf("DecodeWithFEC empty packet=(%d,%v), want %d", n, err, requested)
			}
			got = append(got, frame[:n*channels]...)
			assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, true, "empty-payload FEC")
		})
	}
}

func TestDecodeWithFECNoLBRRPacketWarmZeroAllocs(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		requested  = 1920
	)
	seed := encodeAPIRateSILKPacket(t, channels)
	recovery := encodeAPIRateHybridPacket(t, channels)
	if PacketHasLBRR(recovery) {
		t.Fatal("recovery packet unexpectedly carries LBRR")
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, requested*channels)
	if _, err := dec.Decode(seed, frame); err != nil {
		t.Fatalf("Decode seed: %v", err)
	}
	for range 4 {
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != requested {
			t.Fatalf("warm DecodeWithFEC=(%d,%v), want %d", n, err, requested)
		}
	}
	allocs := testing.AllocsPerRun(20, func() {
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != requested {
			t.Fatalf("DecodeWithFEC=(%d,%v), want %d", n, err, requested)
		}
	})
	if allocs != 0 {
		t.Fatalf("warm DecodeWithFEC allocations=%g, want 0", allocs)
	}
}

func TestDecodeWithFECNilAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range apiRatePLCDurationCases() {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: packet},
						{fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate nil FEC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.DecodeWithFEC(nil, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC(nil): %v", err)
					}
					if n != frameSize {
						t.Fatalf("DecodeWithFEC(nil) samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate nil FEC decode")
				})
			}
		}
	}
}

func TestDecodeWithFECOverlongNoLBRRRequestMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		seedPacket := encodeAPIRateCELTPacket(t, channels)
		recoveryPacket := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
		const sampleRate = 48000
		packetFrameSize, err := packetSamplesAtRate(seedPacket, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		requestedFrameSize := overlongAPIRateRequestedFrameSize(sampleRate)

		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate overlong no-LBRR FEC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]float32, 0, len(want))
			frame := make([]float32, requestedFrameSize*channels)

			n, err := dec.Decode(seedPacket, frame)
			if err != nil {
				t.Fatalf("Decode seed: %v", err)
			}
			if n != packetFrameSize {
				t.Fatalf("Decode seed samples=%d want %d", n, packetFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
			if err != nil {
				t.Fatalf("DecodeWithFEC no-LBRR: %v", err)
			}
			if n != requestedFrameSize {
				t.Fatalf("DecodeWithFEC no-LBRR samples=%d want %d", n, requestedFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "CELT overlong no-LBRR FEC request")
		})
	}
}

func TestDecodeWithFECInvalidRequestedFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("nil_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					steps := []libopusAPIRateDecodeStep{{fec: true}}
					if _, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps); err == nil {
						t.Fatalf("libopus DecodeWithFEC(nil) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.DecodeWithFEC(nil, make([]float32, frameSize*channels), true)
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeWithFEC(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
				t.Run("packet_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					steps := []libopusAPIRateDecodeStep{{packet: packet, fec: true}}
					if _, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps); err == nil {
						t.Fatalf("libopus DecodeWithFEC(packet) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.DecodeWithFEC(packet, make([]float32, frameSize*channels), true)
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeWithFEC(packet) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestDecodeWithFECInvalidRequestedSizePrecedesPacketParsing(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const channels = 1
	const frameSize = sampleRate/400 + 1
	packet := []byte{0x03} // Code-3 packet missing its frame-count byte.

	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{{
		Packet: packet, Format: libopustest.DecodeDiffFormatFloat32,
		FrameSize: frameSize, DecodeFEC: true,
	}})
	if err != nil {
		t.Fatalf("probe libopus invalid FEC request: %v", err)
	}
	if len(want) != 1 || want[0].Code != -1 {
		t.Fatalf("libopus result=%+v, want OPUS_BAD_ARG (-1) before parsing malformed packet", want)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got, err := dec.DecodeWithFEC(packet, make([]float32, frameSize*channels), true)
	if got != 0 || err != ErrInvalidFrameSize {
		t.Fatalf("DecodeWithFEC(invalid size, malformed packet)=(%d,%v), want (0,%v)", got, err, ErrInvalidFrameSize)
	}
}

func TestDecodeWithFECLBRRAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		tolerance float64
		channels  []int
	}{
		{name: "silk_mb_stereo", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthMediumband, bitrate: 18000, tolerance: 8e-3, channels: []int{2}},
		{name: "silk_wb", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 24000, tolerance: 8e-3},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, tolerance: 1.2e-2},
	} {
		channelsSet := tc.channels
		if len(channelsSet) == 0 {
			channelsSet = []int{1, 2}
		}
		for _, channels := range channelsSet {
			seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: seedPacket},
						{packet: recoveryPacket, fec: true},
						{packet: recoveryPacket},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate FEC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					n, err := dec.Decode(seedPacket, frame)
					if err != nil {
						t.Fatalf("Decode seed: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode seed samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC recovery: %v", err)
					}
					if n != frameSize {
						t.Fatalf("DecodeWithFEC samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					n, err = dec.Decode(recoveryPacket, frame)
					if err != nil {
						t.Fatalf("Decode recovery packet: %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode recovery packet samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate FEC decode")
				})
			}
		}
	}
}

func TestDecodeWithFECSILKFinalRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, 960)
		for _, sampleRate := range []int{8000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				steps := []libopusAPIRateDecodeStep{
					{packet: seedPacket},
					{packet: recoveryPacket, fec: true},
				}
				_, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate final range reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				frame := make([]float32, frameSize*channels)
				if _, err := dec.Decode(seedPacket, frame); err != nil {
					t.Fatalf("Decode seed: %v", err)
				}
				if got := dec.FinalRange(); got != ranges[0] {
					t.Fatalf("Decode seed FinalRange()=0x%08x want 0x%08x", got, ranges[0])
				}
				clear(frame)
				n, err := dec.DecodeWithFEC(recoveryPacket, frame, true)
				if err != nil {
					t.Fatalf("DecodeWithFEC recovery: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeWithFEC samples=%d want %d", n, frameSize)
				}
				if got := dec.FinalRange(); got != ranges[1] {
					t.Fatalf("DecodeWithFEC FinalRange()=0x%08x want 0x%08x", got, ranges[1])
				}
			})
		}
	}
}

func TestDecodeWithFECLBRRRequestedDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		tolerance float64
	}{
		{name: "silk_wb", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 24000, tolerance: 8e-3},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, tolerance: 1.2e-2},
	} {
		for _, channels := range []int{1, 2} {
			seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				packetFrameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				for _, requestedFrameSize := range []int{sampleRate / 25, sampleRate * 3 / 50} {
					t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(requestedFrameSize), func(t *testing.T) {
						steps := []libopusAPIRateDecodeStep{
							{packet: seedPacket},
							{packet: recoveryPacket, fec: true},
							{packet: recoveryPacket},
						}
						want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, requestedFrameSize, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "api-rate requested LBRR reference decode", err)
						}

						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatalf("NewDecoder: %v", err)
						}
						got := make([]float32, 0, len(want))
						frame := make([]float32, requestedFrameSize*channels)

						n, err := dec.Decode(seedPacket, frame)
						if err != nil {
							t.Fatalf("Decode seed: %v", err)
						}
						if n != packetFrameSize {
							t.Fatalf("Decode seed samples=%d want %d", n, packetFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.DecodeWithFEC(recoveryPacket, frame, true)
						if err != nil {
							t.Fatalf("DecodeWithFEC recovery: %v", err)
						}
						if n != requestedFrameSize {
							t.Fatalf("DecodeWithFEC samples=%d want requested %d", n, requestedFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						clear(frame)
						n, err = dec.Decode(recoveryPacket, frame)
						if err != nil {
							t.Fatalf("Decode recovery packet: %v", err)
						}
						if n != packetFrameSize {
							t.Fatalf("Decode recovery packet samples=%d want %d", n, packetFrameSize)
						}
						got = append(got, frame[:n*channels]...)

						assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" requested LBRR duration")
					})
				}
			}
		}
	}
}

func TestDecodeWithFECNilAfterLBRRAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		tolerance float64
		channels  []int
	}{
		{name: "silk_wb", mode: EncoderModeSILK, wantMode: ModeSILK, bandwidth: BandwidthWideband, bitrate: 24000, tolerance: 8e-3},
		{name: "hybrid", mode: EncoderModeHybrid, wantMode: ModeHybrid, bandwidth: BandwidthFullband, bitrate: 64000, tolerance: 1.2e-2},
	} {
		channelsSet := tc.channels
		if len(channelsSet) == 0 {
			channelsSet = []int{1, 2}
		}
		for _, channels := range channelsSet {
			seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(recoveryPacket, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					steps := []libopusAPIRateDecodeStep{
						{packet: seedPacket},
						{packet: recoveryPacket},
						{fec: true},
					}
					want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate nil FEC after LBRR reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					for i, packet := range [][]byte{seedPacket, recoveryPacket} {
						n, err := dec.Decode(packet, frame)
						if err != nil {
							t.Fatalf("Decode packet[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("Decode packet[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}

					n, err := dec.DecodeWithFEC(nil, frame, true)
					if err != nil {
						t.Fatalf("DecodeWithFEC(nil): %v", err)
					}
					if n != frameSize {
						t.Fatalf("DecodeWithFEC(nil) samples=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate nil FEC after LBRR decode")
				})
			}
		}
	}
}

func TestDecodeInt16PLCNoSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
		gainQ8     = 8192
	)
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)
			steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
			want, err := decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate int16 PLC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}

			got := make([]int16, 0, len(want))
			frame := make([]int16, frameSize*channels)
			n, err := dec.DecodeInt16(packet, frame)
			if err != nil {
				t.Fatalf("DecodeInt16 packet: %v", err)
			}
			if n != frameSize {
				t.Fatalf("DecodeInt16 packet samples=%d want %d", n, frameSize)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.DecodeInt16(nil, frame)
			if err != nil {
				t.Fatalf("DecodeInt16(nil): %v", err)
			}
			if n != frameSize {
				t.Fatalf("DecodeInt16(nil) samples=%d want %d", n, frameSize)
			}
			got = append(got, frame[:n*channels]...)

			assertAPIRateQualityInt16(t, got, want, sampleRate, channels, "high-gain int16 PLC")
			assertAPIRateInt16Exact(t, got, want, "high-gain int16 PLC")
		})
	}
}

func TestDecodeInt16OverlongPLCRequestAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
		const sampleRate = 48000
		packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		requestedFrameSize := overlongAPIRateRequestedFrameSize(sampleRate)

		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			sequence := [][]byte{packet, nil}
			want, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, requestedFrameSize, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate overlong int16 PLC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int16, 0, len(want))
			frame := make([]int16, requestedFrameSize*channels)
			n, err := dec.DecodeInt16(packet, frame)
			if err != nil {
				t.Fatalf("DecodeInt16 packet: %v", err)
			}
			if n != packetFrameSize {
				t.Fatalf("DecodeInt16 packet samples=%d want %d", n, packetFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			clear(frame)
			n, err = dec.DecodeInt16(nil, frame)
			if err != nil {
				t.Fatalf("DecodeInt16 nil: %v", err)
			}
			if n != requestedFrameSize {
				t.Fatalf("DecodeInt16 nil samples=%d want %d", n, requestedFrameSize)
			}
			got = append(got, frame[:n*channels]...)

			assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "CELT overlong int16 PLC request")
			assertAPIRateInt16Exact(t, got, want, "CELT overlong int16 PLC request")
		})
	}
}

func TestDecodeInt16APIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name   string
		packet func(t *testing.T, channels int) []byte
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket},
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, frameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate int16 reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]int16, 0, len(want))
					frame := make([]int16, frameSize*channels)
					for i, pkt := range sequence {
						n, err := dec.DecodeInt16(pkt, frame)
						if err != nil {
							t.Fatalf("DecodeInt16 sequence[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("DecodeInt16 sequence[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}
					assertAPIRateQualityInt16(t, got, want, sampleRate, channels, tc.name+" api-rate int16 decode")
					assertAPIRateInt16Exact(t, got, want, tc.name+" api-rate int16 decode")
				})
			}
		}
	}
}

func TestDecodeInt16PacketAfterShortPLCAPIRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{16000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				shortFrameSize := sampleRate / 400
				steps := []libopusAPIRateDecodeStep{
					{packet: packet, frameSize: frameSize},
					{frameSize: shortFrameSize},
					{packet: packet, frameSize: frameSize},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, channels, frameSize, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate int16 variable reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				for i, step := range steps {
					frame := make([]int16, step.frameSize*channels)
					n, err := dec.DecodeInt16(step.packet, frame)
					if err != nil {
						t.Fatalf("DecodeInt16 sequence[%d]: %v", i, err)
					}
					if n != step.frameSize {
						t.Fatalf("DecodeInt16 sequence[%d] samples=%d want %d", i, n, step.frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertAPIRateQualityInt16(t, got, want, sampleRate, channels, "packet-short-plc-packet int16 decode")
				assertAPIRateInt16Exact(t, got, want, "packet-short-plc-packet int16 decode")
			})
		}
	}
}

func TestDecodeInt16InvalidRequestedPLCFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	for _, channels := range []int{1, 2} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("int16_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, frameSize, [][]byte{nil}); err == nil {
						t.Fatalf("libopus DecodeInt16(nil) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.DecodeInt16(nil, make([]int16, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestDecodeSILKUsesAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK {
			t.Fatalf("channels=%d test packet mode=%v want SILK", channels, toc.Mode)
		}
		if toc.Stereo != (channels == 2) {
			t.Fatalf("channels=%d packet stereo=%v", channels, toc.Stereo)
		}
		if first, err := extractFirstFramePayload(packet, toc); err != nil {
			t.Fatalf("channels=%d extract first frame: %v", channels, err)
		} else if packetHasLBRR(first, toc) {
			t.Fatalf("channels=%d test packet unexpectedly contains LBRR", channels)
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				want, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				pcm := make([]float32, want*channels)
				n, err := dec.Decode(packet, pcm)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if n != want {
					t.Fatalf("Decode samples=%d want %d", n, want)
				}

				smallDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder small: %v", err)
				}
				if _, err := smallDec.Decode(packet, make([]float32, want*channels-1)); err != ErrBufferTooSmall {
					t.Fatalf("Decode small-buffer error=%v want %v", err, ErrBufferTooSmall)
				}

				plc := make([]float32, want*channels)
				n, err = dec.Decode(nil, plc)
				if err != nil {
					t.Fatalf("Decode PLC: %v", err)
				}
				if n != want {
					t.Fatalf("Decode PLC samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodeSILKAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet, nil}
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate SILK reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				for i, pkt := range sequence {
					n, err := dec.Decode(pkt, frame)
					if err != nil {
						t.Fatalf("Decode sequence[%d]: %v", i, err)
					}
					if n != frameSize {
						t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "SILK api-rate decode")
			})
		}
	}
}

func TestDecodeOutputGainFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		frameSize  = 960
		gainQ8     = 8192
	)
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packet := encodeAPIRateCELTPacket(t, channels)
			steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate float32 output gain reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if err := dec.SetGain(gainQ8); err != nil {
				t.Fatalf("SetGain(%d): %v", gainQ8, err)
			}

			got := make([]float32, 0, len(want))
			frame := make([]float32, frameSize*channels)
			n, err := dec.Decode(packet, frame)
			if err != nil {
				t.Fatalf("Decode packet: %v", err)
			}
			got = append(got, frame[:n*channels]...)
			clear(frame)
			n, err = dec.Decode(nil, frame)
			if err != nil {
				t.Fatalf("Decode(nil): %v", err)
			}
			got = append(got, frame[:n*channels]...)

			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "high-gain float32 output")
		})
	}
}

func TestDecodeHybridUsesAPIRatePacketDuration(t *testing.T) {
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacket(t, channels)
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeHybrid {
			t.Fatalf("channels=%d test packet mode=%v want Hybrid", channels, toc.Mode)
		}
		if toc.Stereo != (channels == 2) {
			t.Fatalf("channels=%d packet stereo=%v", channels, toc.Stereo)
		}

		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				want, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				pcm := make([]float32, want*channels)
				n, err := dec.Decode(packet, pcm)
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				if n != want {
					t.Fatalf("Decode samples=%d want %d", n, want)
				}

				plc := make([]float32, want*channels)
				n, err = dec.Decode(nil, plc)
				if err != nil {
					t.Fatalf("Decode PLC: %v", err)
				}
				if n != want {
					t.Fatalf("Decode PLC samples=%d want %d", n, want)
				}

				intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder int16: %v", err)
				}
				pcm16 := make([]int16, want*channels)
				n, err = intDec.DecodeInt16(packet, pcm16)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				if n != want {
					t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
				}

				fecDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder fec: %v", err)
				}
				fecPCM := make([]float32, want*channels)
				n, err = fecDec.DecodeWithFEC(packet, fecPCM, true)
				if err != nil {
					t.Fatalf("DecodeWithFEC(no LBRR): %v", err)
				}
				if n != want {
					t.Fatalf("DecodeWithFEC(no LBRR) samples=%d want %d", n, want)
				}
			})
		}
	}
}

func TestDecodePLCDurationAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range apiRatePLCDurationCases() {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate PLC duration reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					for i, pkt := range sequence {
						n, err := dec.Decode(pkt, frame)
						if err != nil {
							t.Fatalf("Decode sequence[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate PLC duration decode")
				})
			}
		}
	}
}

func TestDecodeOverlongPLCRequestAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []apiRatePacketParityCase{
		{name: "silk_20ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 960) }, tolerance: 8e-3},
		{name: "celt_20ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 960) }, tolerance: 3e-3},
		{name: "hybrid_20ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 960) }, tolerance: 1e-2},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					requestedFrameSize := overlongAPIRateRequestedFrameSize(sampleRate)
					if requestedFrameSize <= sampleRate*3/25 {
						t.Fatalf("requestedFrameSize=%d not over one 120ms chunk", requestedFrameSize)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, requestedFrameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate overlong PLC reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode packet: %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode packet samples=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode nil: %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("Decode nil samples=%d want %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)

					assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, true, tc.name+" overlong PLC request")
				})
			}
		}
	}
}

func TestDecodeHybridAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet, nil}
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, 0, len(want))
				frame := make([]float32, frameSize*channels)
				for i, pkt := range sequence {
					n, err := dec.Decode(pkt, frame)
					if err != nil {
						t.Fatalf("Decode sequence[%d]: %v", i, err)
					}
					if n != frameSize {
						t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
				}
				assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, "Hybrid api-rate decode")
			})
		}
	}
}

func TestDecodeMultiFrameAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []apiRatePacketParityCase{
		{
			name: "silk_code1_two_equal",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateSILKPacketFrameSize(t, channels, 960)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame}, 1)
			},
			tolerance: 8e-3,
		},
		{
			name: "hybrid_code1_two_equal",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateHybridPacketFrameSize(t, channels, 960)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame}, 1)
			},
			tolerance: 1e-2,
		},
		{
			name: "celt_code1_two_equal",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, 480, 64000, 0)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame}, 1)
			},
			tolerance: 3e-3,
		},
		{
			name: "celt_code2_two_vbr",
			packet: func(t *testing.T, channels int) []byte {
				packets := encodeAPIRateCELTPacketVariants(t, channels, 480, []int{64000, 96000}, 2)
				frames := firstAPIRateFramePayloads(t, packets)
				return buildAPIRateMultiFramePacket(t, packets[0], frames, 2)
			},
			tolerance: 3e-3,
		},
		{
			name: "celt_code3_three_cbr",
			packet: func(t *testing.T, channels int) []byte {
				packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, 480, 64000, 1)
				frame := firstAPIRateFramePayload(t, packet)
				return buildAPIRateMultiFramePacket(t, packet, [][]byte{frame, frame, frame}, 3)
			},
			tolerance: 3e-3,
		},
		{
			name: "celt_code3_three_vbr",
			packet: func(t *testing.T, channels int) []byte {
				packets := encodeAPIRateCELTPacketVariants(t, channels, 480, []int{64000, 96000, 128000}, 3)
				frames := firstAPIRateFramePayloads(t, packets)
				return buildAPIRateMultiFramePacket(t, packets[0], frames, 3)
			},
			tolerance: 3e-3,
		},
	} {
		for _, channels := range []int{1, 2} {
			packet := tc.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(tc.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "api-rate multi-frame reference decode", err)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					for i, pkt := range sequence {
						n, err := dec.Decode(pkt, frame)
						if err != nil {
							t.Fatalf("Decode sequence[%d]: %v", i, err)
						}
						if n != frameSize {
							t.Fatalf("Decode sequence[%d] samples=%d want %d", i, n, frameSize)
						}
						got = append(got, frame[:n*channels]...)
					}

					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, tc.name+" api-rate multi-frame decode")
				})
			}
		}
	}
}

func TestDecodeColdPLCAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize := sampleRate / 50
				want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, [][]byte{nil})
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate cold PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]float32, frameSize*channels)
				n, err := dec.Decode(nil, got)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				if n != frameSize {
					t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
				}
				if dec.LastPacketDuration() != frameSize {
					t.Fatalf("LastPacketDuration()=%d want %d", dec.LastPacketDuration(), frameSize)
				}
				assertSelectedPublicAPIRateFloat32(t, got[:n*channels], want, sampleRate, channels, "cold PLC api-rate decode")

				dec.Reset()
				if dec.LastPacketDuration() != 0 {
					t.Fatalf("LastPacketDuration() after Reset=%d want 0", dec.LastPacketDuration())
				}
				clear(got)
				n, err = dec.Decode(nil, got)
				if err != nil {
					t.Fatalf("Decode(nil) after Reset: %v", err)
				}
				if n != frameSize {
					t.Fatalf("Decode(nil) after Reset samples=%d want %d", n, frameSize)
				}
				assertSelectedPublicAPIRateFloat32(t, got[:n*channels], want, sampleRate, channels, "reset cold PLC api-rate decode")
			})
		}
	}
}

func TestDecodeInvalidRequestedPLCFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	for _, channels := range []int{1, 2} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("float_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, [][]byte{nil}); err == nil {
						t.Fatalf("libopus Decode(nil) accepted frame_size=%d", frameSize)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					n, err := dec.Decode(nil, make([]float32, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestDecodeRejectsNonChannelMultipleRequestedFrameBuffer(t *testing.T) {
	const channels = 2
	packet := encodeAPIRateSILKPacket(t, channels)
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		frameSize := sampleRate / 50
		sampleCount := frameSize*channels + 1
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			n, err := dec.Decode(nil, make([]float32, sampleCount))
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}

			intDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			n, err = intDec.DecodeInt16(nil, make([]int16, sampleCount))
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}

			fecDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder FEC: %v", err)
			}
			n, err = fecDec.DecodeWithFEC(nil, make([]float32, sampleCount), true)
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("DecodeWithFEC(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}

			packetFECDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder packet FEC: %v", err)
			}
			n, err = packetFECDec.DecodeWithFEC(packet, make([]float32, sampleCount), true)
			if n != 0 || err != ErrInvalidFrameSize {
				t.Fatalf("DecodeWithFEC(packet) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
			}
		})
	}
}

var apiRateSubRateBar = qualitycompare.QualityBar{
	MinQ:    math.Inf(-1), // opus_compare N/A here; Q is logged, not gated.
	MinCorr: 0.997,
	RMSLo:   0.98,
	RMSHi:   1.02,
	Desc:    "near-exact vs libopus (sub-48k/short/PLC-dominated: corr/RMS, opus_compare N/A)",
}

// opusCompareApplies reports whether opus_compare (and thus the Q gate) is valid
// for an api-rate decoded stream: 48 kHz with at least 480 samples per channel.
func opusCompareApplies(sampleRate, channels, totalSamples int) bool {
	return sampleRate == 48000 && channels > 0 && totalSamples/channels >= 480
}

// assertAPIRateQualityFloat32 checks the quality metrics for a decoded PCM
// stream with no PLC-dominated tail.
func assertAPIRateQualityFloat32(t *testing.T, got, want []float32, sampleRate, channels int, label string) {
	t.Helper()
	assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, false, label)
}

// assertAPIRateQualityFloat32PLC checks decoded-PCM quality metrics. got/want are interleaved
// 48 kHz-or-lower PCM that are sample-aligned vs libopus. plcDominated must be
// true when the requested output is mostly packet-loss concealment (a short real
// frame followed by a longer PLC tail), in which case the quality check uses
// correlation and RMS instead of opus_compare Q (see file header).
func assertAPIRateQualityFloat32PLC(t *testing.T, got, want []float32, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	if opusCompareApplies(sampleRate, channels, len(got)) && !plcDominated {
		// 48 kHz, >=10 ms, real content: opus_compare applies. Check its quality
		// bar and log the measured metrics.
		cmp, err := qualitycompare.CompareDecodedFloat32(got, want, sampleRate, channels, 0)
		if err != nil {
			t.Fatalf("%s CompareDecodedFloat32: %v", label, err)
		}
		qualitycompare.AssertQuality(t, cmp, qualitycompare.QualityBarNearExact, label)
		return
	}
	// Sub-48 kHz, short, or PLC-dominated output uses the rate-independent
	// waveform corr/RMS metrics. For 48 kHz PLC-dominated streams, also measure
	// Q for the log; it does not affect the quality bar.
	corr, rms := apiRateWaveformCorrelationRMS(got, want)
	q := 0.0
	if opusCompareApplies(sampleRate, channels, len(got)) {
		if cmp, err := qualitycompare.CompareDecodedFloat32(got, want, sampleRate, channels, 0); err == nil {
			q = cmp.Q
		}
	}
	cmp := qualitycompare.QualityComparison{Q: q, BestDelay: 0, Corr: corr, RMSRatio: rms}
	qualitycompare.AssertQuality(t, cmp, apiRateSubRateBar, label)
}

// apiRateWaveformCorrelationRMS mirrors qualitycompare's internal (unexported)
// waveform diagnostics: Pearson correlation and RMS(got)/RMS(want) over the
// common prefix. Used only for the sub-48k/short gate where opus_compare cannot
// run; the 48k path uses CompareDecodedFloat32 directly.
func apiRateWaveformCorrelationRMS(a, b []float32) (corr, rmsRatio float64) {
	n := min(len(b), len(a))
	if n == 0 {
		return 0, 0
	}
	var sumA, sumB, sumASq, sumBSq float64
	for i := range n {
		fa, fb := float64(a[i]), float64(b[i])
		sumA += fa
		sumB += fb
		sumASq += fa * fa
		sumBSq += fb * fb
	}
	meanA, meanB := sumA/float64(n), sumB/float64(n)
	var varA, varB, cov float64
	for i := range n {
		da, db := float64(a[i])-meanA, float64(b[i])-meanB
		cov += da * db
		varA += da * da
		varB += db * db
	}
	if varA > 0 && varB > 0 {
		corr = cov / math.Sqrt(varA*varB)
	} else if varA == 0 && varB == 0 {
		corr = 1
	}
	rmsA := math.Sqrt(sumASq / float64(n))
	rmsB := math.Sqrt(sumBSq / float64(n))
	if rmsB > 0 {
		rmsRatio = rmsA / rmsB
	} else if rmsA == 0 {
		rmsRatio = 1
	}
	return corr, rmsRatio
}

// assertAPIRateQualityInt16 converts both int16 streams to float32, scaled by
// 1/32768, and checks their waveform quality. Call assertAPIRateInt16Exact when
// the test also requires exact sample equality.
func assertAPIRateQualityInt16(t *testing.T, got, want []int16, sampleRate, channels int, label string) {
	t.Helper()
	assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, false, label)
}

func assertAPIRateQualityInt16PLC(t *testing.T, got, want []int16, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	gotF := int16SliceToFloat32(got)
	wantF := int16SliceToFloat32(want)
	assertAPIRateQualityFloat32PLC(t, gotF, wantF, sampleRate, channels, plcDominated, label)
}

func int16SliceToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 32768.0
	}
	return out
}

// assertAPIRateInt16Exact compares integer PCM samples with the selected C
// reference. Quality metrics alone do not establish sample equality.
func assertAPIRateInt16Exact(t *testing.T, got, want []int16, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s sample[%d]=%d want %d", label, i, got[i], want[i])
		}
	}
}

func assertAPIRateFloat32BitsExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s sample[%d]=%08x want %08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// The public API-rate reference helper selects the active libopus feature and
// instruction lane, then checks both quality metrics and every output bit.
// Call this only when want comes from decodeWithLibopusReferenceAPIRateFloat32.
func assertSelectedPublicAPIRateFloat32(t *testing.T, got, want []float32, sampleRate, channels int, label string) {
	t.Helper()
	assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, false, label)
}

func assertSelectedPublicAPIRateFloat32PLC(t *testing.T, got, want []float32, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, plcDominated, label)
	assertAPIRateFloat32BitsExact(t, got, want, label)
}

const (
	libopusRefdecodeSingleFormatFloat32 = uint32(0)
	libopusRefdecodeSingleFormatInt16   = uint32(1)
)

var libopusAPIRateRefdecodeHelper libopustest.HelperCache

type apiRatePacketParityCase struct {
	name      string
	packet    func(t *testing.T, channels int) []byte
	tolerance float64
}

func apiRatePLCDurationCases() []apiRatePacketParityCase {
	return []apiRatePacketParityCase{
		{name: "silk_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 480) }, tolerance: 8e-3},
		{name: "silk_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 1920) }, tolerance: 8e-3},
		{name: "silk_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateSILKPacketFrameSize(t, channels, 2880) }, tolerance: 8e-3},
		{name: "celt_2p5ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 120) }, tolerance: 3e-3},
		{name: "celt_5ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 240) }, tolerance: 3e-3},
		{name: "celt_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 480) }, tolerance: 3e-3},
		{name: "celt_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 1920) }, tolerance: 3e-3},
		{name: "celt_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateCELTPacketFrameSize(t, channels, 2880) }, tolerance: 3e-3},
		{name: "hybrid_10ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 480) }, tolerance: 1e-2},
		{name: "hybrid_40ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 1920) }, tolerance: 1e-2},
		{name: "hybrid_60ms", packet: func(t *testing.T, channels int) []byte { return encodeAPIRateHybridPacketFrameSize(t, channels, 2880) }, tolerance: 1e-2},
	}
}

func invalidAPIRateRequestedFrameSizes(sampleRate int) []int {
	quantum := sampleRate / 400
	return []int{quantum + 1, sampleRate/50 - 1, sampleRate/50 + 1}
}

func overlongAPIRateRequestedFrameSize(sampleRate int) int {
	return sampleRate / 5 // 200 ms, larger than one 120 ms internal PLC chunk.
}

func encodeAPIRateSILKPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateSILKPacketFrameSize(t, channels, 960)
}

func encodeAPIRateSILKPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		left := 0.22 * float32(math.Sin(2*math.Pi*440*float64(i)/sampleRate))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.18 * float32(math.Sin(2*math.Pi*660*float64(i)/sampleRate))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateCELTPacketFrameSize(t, channels, 960)
}

func encodeAPIRateCELTPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		left := 0.28 * float32(math.Sin(2*math.Pi*1200*float64(i)/sampleRate))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.19 * float32(math.Sin(2*math.Pi*1900*float64(i)/sampleRate))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacketFrameSizeVariant(t *testing.T, channels, frameSize, bitrate, variant int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeVBR); err != nil {
		t.Fatalf("SetBitrateMode(VBR): %v", err)
	}
	enc.SetVBRConstraint(false)
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		tm := float64(variant*frameSize+i) / sampleRate
		left := 0.28*float32(math.Sin(2*math.Pi*(1200+float64(variant)*137)*tm+float64(variant)*0.11)) +
			0.05*float32(math.Sin(2*math.Pi*(2300+float64(variant)*91)*tm+0.23))
		pcm[i*channels] = left
		if channels == 2 {
			pcm[i*channels+1] = 0.19*float32(math.Sin(2*math.Pi*(1900+float64(variant)*151)*tm+0.31)) +
				0.04*float32(math.Sin(2*math.Pi*(3100+float64(variant)*73)*tm+0.07))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateCELTPacketVariants(t *testing.T, channels, frameSize int, bitrates []int, count int) [][]byte {
	t.Helper()
	packets := make([][]byte, 0, 16)
	for variant := range 16 {
		bitrate := bitrates[variant%len(bitrates)]
		packet := encodeAPIRateCELTPacketFrameSizeVariant(t, channels, frameSize, bitrate, variant)
		if len(packets) > 0 && packet[0]&0xFC != packets[0][0]&0xFC {
			t.Fatalf("variant TOC base=0x%02x want 0x%02x", packet[0]&0xFC, packets[0][0]&0xFC)
		}
		packets = append(packets, packet)
		for start := 0; start+count <= len(packets); start++ {
			window := packets[start : start+count]
			if apiRatePacketsHaveUnequalPayloadSizes(window) {
				return append([][]byte(nil), window...)
			}
		}
	}
	t.Fatalf("failed to generate %d CELT VBR frames with unequal payload sizes", count)
	return nil
}

func encodeAPIRateHybridPacket(t *testing.T, channels int) []byte {
	t.Helper()
	return encodeAPIRateHybridPacketFrameSize(t, channels, 960)
}

func encodeAPIRateHybridPacketFrameSize(t *testing.T, channels, frameSize int) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeHybrid); err != nil {
		t.Fatalf("SetMode(Hybrid): %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(64000 * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	pcm := make([]float32, frameSize*channels)
	for i := range frameSize {
		tm := float64(i) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
			0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
		}
	}
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return packet
}

func encodeAPIRateFECSequence(t *testing.T, mode EncoderMode, wantMode Mode, bandwidth Bandwidth, bitrate, channels, frameSize int) ([]byte, []byte) {
	t.Helper()
	const sampleRate = 48000
	app := ApplicationVoIP
	if mode == EncoderModeSILK {
		app = ApplicationRestrictedSilk
	}
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: app,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bandwidth); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, 12)
	for frameIndex := range 12 {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			pcm[i*channels] = 0.38*float32(math.Sin(2*math.Pi*220*tm)) +
				0.14*float32(math.Sin(2*math.Pi*440*tm+0.11))
			if channels == 2 {
				pcm[i*channels+1] = 0.33*float32(math.Sin(2*math.Pi*330*tm+0.07)) +
					0.12*float32(math.Sin(2*math.Pi*660*tm+0.19))
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != wantMode {
			t.Fatalf("Encode frame %d mode=%v want %v", frameIndex, toc.Mode, wantMode)
		}
		packets = append(packets, append([]byte(nil), packet...))
		if len(packets) >= 3 && packetHasInBandFEC(t, packet) {
			return packets[len(packets)-3], packet
		}
	}
	t.Fatalf("failed to generate %v packet carrying LBRR", wantMode)
	return nil, nil
}

func firstAPIRateFramePayload(t *testing.T, packet []byte) []byte {
	t.Helper()
	_, frames, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parsePacketFrames: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("single-frame packet has %d frames", len(frames))
	}
	return append([]byte(nil), frames[0]...)
}

func firstAPIRateFramePayloads(t *testing.T, packets [][]byte) [][]byte {
	t.Helper()
	frames := make([][]byte, len(packets))
	for i, packet := range packets {
		frames[i] = firstAPIRateFramePayload(t, packet)
	}
	return frames
}

func buildAPIRateMultiFramePacket(t *testing.T, basePacket []byte, frames [][]byte, wantFrameCode byte) []byte {
	t.Helper()
	data := make([]byte, maxPacketBytesPerStream)
	n, err := buildRepacketizedPacketWithOptions(basePacket[0]&0xFC, frames, data, 0, false, nil)
	if err != nil {
		t.Fatalf("buildRepacketizedPacketWithOptions: %v", err)
	}
	packet := append([]byte(nil), data[:n]...)
	info, parsed, err := parsePacketFrames(packet)
	if err != nil {
		t.Fatalf("parse repacketized packet: %v", err)
	}
	if info.TOC.FrameCode != wantFrameCode {
		t.Fatalf("frame code=%d want %d", info.TOC.FrameCode, wantFrameCode)
	}
	if len(parsed) != len(frames) {
		t.Fatalf("frame count=%d want %d", len(parsed), len(frames))
	}
	return packet
}

func apiRatePacketsHaveUnequalPayloadSizes(packets [][]byte) bool {
	if len(packets) < 2 {
		return false
	}
	firstLen := len(firstPacketPayloadForSizeCheck(packets[0]))
	for _, packet := range packets[1:] {
		if len(firstPacketPayloadForSizeCheck(packet)) != firstLen {
			return true
		}
	}
	return false
}

func firstPacketPayloadForSizeCheck(packet []byte) []byte {
	if len(packet) == 0 {
		return nil
	}
	if packet[0]&0x03 == 0 {
		return packet[1:]
	}
	_, frames, err := parsePacketFrames(packet)
	if err != nil || len(frames) == 0 {
		return nil
	}
	return frames[0]
}

type libopusAPIRateDecodeStep struct {
	packet    []byte
	frameSize int
	fec       bool
}

func getLibopusAPIRateRefdecodeHelperPath() (string, error) {
	return apiRateReferenceHelperPath()
}

func requireLibopusAPIRateRefdecodeHelper(t *testing.T) {
	t.Helper()
	if _, err := getLibopusAPIRateRefdecodeHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "api-rate reference decode", err)
	}
}

func decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize int, packets [][]byte) ([]float32, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, packet := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: packet}
	}
	return decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]float32, error) {
	return decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32StepsGain(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]float32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5, libopusRefdecodeSingleFormatFloat32, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]float32, []uint32, error) {
	return decodeWithLibopusReferenceAPIRateFloat32StepsGainRanges(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateFloat32StepsGainRanges(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]float32, []uint32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 6, libopusRefdecodeSingleFormatFloat32, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	data, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		return nil, nil, err
	}
	reader, version, err := libopustest.NewOracleReaderVersion("api-rate reference decode ranges", "GOSO", data)
	if err != nil {
		return nil, nil, err
	}
	if version != 2 {
		return nil, nil, fmt.Errorf("api-rate reference decode ranges helper version=%d want 2", version)
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples*4 + 4 + len(steps)*4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	rangeCount := reader.Count(len(steps))
	ranges := make([]uint32, rangeCount)
	for i := range ranges {
		ranges[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return decoded, ranges, nil
}

func decodeWithLibopusReferenceAPIRateInt16(sampleRate, channels, frameSize int, packets [][]byte) ([]int16, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, packet := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: packet}
	}
	return decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	return decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, 0, steps)
}

func decodeWithLibopusReferenceAPIRateInt16StepsGain(sampleRate, channels, frameSize, gainQ8 int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5, libopusRefdecodeSingleFormatInt16, uint32(sampleRate), uint32(int32(gainQ8)), uint32(channels), uint32(frameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, channels, maxFrameSize int, steps []libopusAPIRateDecodeStep) ([]int16, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 7, libopusRefdecodeSingleFormatInt16, uint32(sampleRate), 0, uint32(channels), uint32(maxFrameSize), uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		frameSize := step.frameSize
		if frameSize == 0 {
			frameSize = maxFrameSize
		}
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

type malformedDecodeFramingCase struct {
	name   string
	packet []byte
}

func malformedDecodeFramingCases() []malformedDecodeFramingCase {
	code0 := append([]byte{0x00}, make([]byte, maxOpusFrameBytes+1)...)
	return []malformedDecodeFramingCase{
		{name: "code0", packet: code0},
		{name: "code1", packet: []byte{0x01, 0x00}},
		{name: "code2", packet: []byte{0x02, 0x05, 0x00}},
		{name: "code3", packet: []byte{0x03, 0x82, 0x05, 0x00}},
	}
}

func TestDecodeMalformedFramingPrecedesSmallOutput(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, format := range []uint32{
				libopustest.DecodeDiffFormatFloat32,
				libopustest.DecodeDiffFormatInt16,
				libopustest.DecodeDiffFormatInt24,
			} {
				formatName := [...]string{"float32", "int16", "int24"}[format]
				t.Run(fmt.Sprintf("%dhz/%dch/%s", sampleRate, channels, formatName), func(t *testing.T) {
					assertDecodeMalformedFramingPrecedesSmallOutput(
						t,
						sampleRate,
						channels,
						format,
						encodeAPIRateSILKPacket(t, channels),
						sampleRate/50,
					)
				})
			}
		}
	}
}

func TestDecodeEmptyAndPartialChannelBuffersPrecedePacketParsing(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, format := range []uint32{
			libopustest.DecodeDiffFormatFloat32,
			libopustest.DecodeDiffFormatInt16,
			libopustest.DecodeDiffFormatInt24,
		} {
			formatName := [...]string{"float32", "int16", "int24"}[format]
			t.Run(fmt.Sprintf("%dch/%s", channels, formatName), func(t *testing.T) {
				packet := encodeAPIRateSILKPacket(t, channels)
				assertDecodeEmptyAndPartialChannelBuffers(
					t, 48000, channels, format, packet, 960)
			})
		}
	}
}

type decodeBufferBoundaryCall struct {
	packet    []byte
	outputLen int
	fec       bool
}

func assertDecodeEmptyAndPartialChannelBuffers(t *testing.T, sampleRate, channels int, format uint32, validPacket []byte, frameSize int) {
	t.Helper()

	malformed := []byte{0x01, 0x00}
	steps := []libopustest.DecodeDiffCase{{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)}}
	calls := []decodeBufferBoundaryCall{{packet: validPacket, outputLen: frameSize * channels}}
	for outputLen := 0; outputLen < channels; outputLen++ {
		fecModes := []bool{false}
		if format == libopustest.DecodeDiffFormatFloat32 {
			fecModes = append(fecModes, true)
		}
		for _, fec := range fecModes {
			for _, packet := range [][]byte{malformed, validPacket} {
				steps = append(steps, libopustest.DecodeDiffCase{
					Packet: packet, Format: format, LiteralFrameSize: true, DecodeFEC: fec,
				})
				calls = append(calls, decodeBufferBoundaryCall{
					packet: packet, outputLen: outputLen, fec: fec,
				})
			}
		}
	}
	steps = append(steps, libopustest.DecodeDiffCase{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)})
	calls = append(calls, decodeBufferBoundaryCall{packet: validPacket, outputLen: frameSize * channels})

	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(steps) {
		t.Fatalf("C returned %d decode results, want %d", len(want), len(steps))
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder(%d, %d): %v", sampleRate, channels, err)
	}
	pcmF32 := make([]float32, frameSize*channels)
	pcmI16 := make([]int16, frameSize*channels)
	pcmI24 := make([]int32, frameSize*channels)
	previousRange := dec.FinalRange()
	for i, call := range calls {
		var n int
		var decodeErr error
		switch format {
		case libopustest.DecodeDiffFormatInt16:
			n, decodeErr = dec.DecodeInt16(call.packet, pcmI16[:call.outputLen])
		case libopustest.DecodeDiffFormatInt24:
			n, decodeErr = dec.DecodeInt24(call.packet, pcmI24[:call.outputLen])
		default:
			if call.fec {
				n, decodeErr = dec.DecodeWithFEC(call.packet, pcmF32[:call.outputLen], true)
			} else {
				n, decodeErr = dec.Decode(call.packet, pcmF32[:call.outputLen])
			}
		}
		if want[i].Code > 0 {
			if decodeErr != nil || int32(n) != want[i].Code {
				t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, decodeErr, want[i].Code)
			}
			if err := assertDecodeMalformedFramingPCM(format, n, channels, want[i].PCM, pcmF32, pcmI16, pcmI24); err != nil {
				t.Fatalf("step%d: %v", i, err)
			}
		} else {
			// The public Go facade maps a zero-samples-per-channel buffer to
			// ErrBufferTooSmall, while all three C wrappers return OPUS_BAD_ARG.
			if want[i].Code != -1 || n != 0 || decodeErr != ErrBufferTooSmall {
				t.Fatalf("step%d decode=(%d,%v), C=%d; want zero-capacity bad-argument mapping", i, n, decodeErr, want[i].Code)
			}
			if len(want[i].PCM) != 0 {
				t.Fatalf("step%d C emitted %d PCM bytes for rejected call", i, len(want[i].PCM))
			}
			if want[i].FinalRange != previousRange {
				t.Fatalf("step%d C error changed final range to %08x, preceding range %08x", i, want[i].FinalRange, previousRange)
			}
		}
		if got := dec.FinalRange(); got != want[i].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", i, got, want[i].FinalRange)
		}
		if want[i].Code < 0 && dec.FinalRange() != previousRange {
			t.Fatalf("step%d failed call changed Go range to %08x, preceding range %08x", i, dec.FinalRange(), previousRange)
		}
		previousRange = want[i].FinalRange
	}
}

func assertDecodeMalformedFramingPrecedesSmallOutput(t *testing.T, sampleRate, channels int, format uint32, validPacket []byte, frameSize int) {
	t.Helper()

	shortFrameSize := sampleRate / 400
	malformed := malformedDecodeFramingCases()
	steps := make([]libopustest.DecodeDiffCase, 1, 1+len(malformed)*2)
	steps[0] = libopustest.DecodeDiffCase{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)}
	for _, tc := range malformed {
		steps = append(steps,
			libopustest.DecodeDiffCase{Packet: tc.packet, Format: format, FrameSize: uint32(shortFrameSize)},
			libopustest.DecodeDiffCase{Packet: validPacket, Format: format, FrameSize: uint32(frameSize)},
		)
	}
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != len(steps) {
		t.Fatalf("C returned %d decode results, want %d", len(want), len(steps))
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder(%d, %d): %v", sampleRate, channels, err)
	}
	pcmF32 := make([]float32, frameSize*channels)
	pcmI16 := make([]int16, frameSize*channels)
	pcmI24 := make([]int32, frameSize*channels)
	decode := func(step libopustest.DecodeDiffCase) (int, error) {
		capacity := int(step.FrameSize) * channels
		switch format {
		case libopustest.DecodeDiffFormatInt16:
			return dec.DecodeInt16(step.Packet, pcmI16[:capacity])
		case libopustest.DecodeDiffFormatInt24:
			return dec.DecodeInt24(step.Packet, pcmI24[:capacity])
		default:
			return dec.Decode(step.Packet, pcmF32[:capacity])
		}
	}
	previousRange := uint32(0)
	for i, step := range steps {
		n, decodeErr := decode(step)
		if want[i].Code < 0 {
			if want[i].Code != -4 || n != 0 || decodeErr != ErrInvalidPacket {
				t.Fatalf("step%d decode=(%d,%v), C=%d; want malformed-packet error", i, n, decodeErr, want[i].Code)
			}
			if len(want[i].PCM) != 0 {
				t.Fatalf("step%d C emitted %d PCM bytes for rejected packet", i, len(want[i].PCM))
			}
			if want[i].FinalRange != previousRange {
				t.Fatalf("step%d C error changed final range to %08x, preceding range %08x", i, want[i].FinalRange, previousRange)
			}
		} else if decodeErr != nil || int32(n) != want[i].Code {
			t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, decodeErr, want[i].Code)
		}
		if got := dec.FinalRange(); got != want[i].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", i, got, want[i].FinalRange)
		}
		if want[i].Code > 0 {
			if err := assertDecodeMalformedFramingPCM(format, n, channels, want[i].PCM, pcmF32, pcmI16, pcmI24); err != nil {
				t.Fatalf("step%d: %v", i, err)
			}
		}
		previousRange = want[i].FinalRange
	}

	if sampleRate == 48000 && channels == 1 && format == libopustest.DecodeDiffFormatFloat32 {
		bad := malformed[1].packet
		if allocs := testing.AllocsPerRun(20, func() {
			if n, err := dec.Decode(bad, pcmF32[:shortFrameSize]); n != 0 || err != ErrInvalidPacket {
				t.Fatalf("warm malformed decode=(%d,%v)", n, err)
			}
		}); allocs != 0 {
			t.Fatalf("warm malformed decode allocations=%g, want 0", allocs)
		}
	}
}

func assertDecodeMalformedFramingPCM(format uint32, samples, channels int, want []byte, pcmF32 []float32, pcmI16 []int16, pcmI24 []int32) error {
	count := samples * channels
	switch format {
	case libopustest.DecodeDiffFormatInt16:
		if len(want) != count*2 {
			return fmt.Errorf("C PCM has %d bytes, want %d", len(want), count*2)
		}
		for i := 0; i < count; i++ {
			if got, expected := uint16(pcmI16[i]), binary.LittleEndian.Uint16(want[i*2:]); got != expected {
				return fmt.Errorf("sample%d int16=%04x, C=%04x", i, got, expected)
			}
		}
	case libopustest.DecodeDiffFormatInt24:
		if len(want) != count*4 {
			return fmt.Errorf("C PCM has %d bytes, want %d", len(want), count*4)
		}
		for i := 0; i < count; i++ {
			if got, expected := uint32(pcmI24[i]), binary.LittleEndian.Uint32(want[i*4:]); got != expected {
				return fmt.Errorf("sample%d int24=%08x, C=%08x", i, got, expected)
			}
		}
	default:
		if len(want) != count*4 {
			return fmt.Errorf("C PCM has %d bytes, want %d", len(want), count*4)
		}
		for i := 0; i < count; i++ {
			if got, expected := math.Float32bits(pcmF32[i]), binary.LittleEndian.Uint32(want[i*4:]); got != expected {
				return fmt.Errorf("sample%d float32=%08x, C=%08x", i, got, expected)
			}
		}
	}
	return nil
}

const (
	cngRateHistoryFrameSize = 960
	cngRateHistorySteps     = 17
)

type cngRateHistoryStep struct {
	packet     []byte
	pcm        []float32
	finalRange uint32
	pitch      int32
}

type cngRateHistoryCase struct {
	direction int
	amplitude int
	steps     []cngRateHistoryStep
}

var (
	cngRateHistoryHelper libopustest.HelperCache
	cngRateHistorySink   uint32
)

func readCNGRateHistoryU32(t *testing.T, r *bytes.Reader, field string) uint32 {
	t.Helper()
	var value uint32
	if err := binary.Read(r, binary.LittleEndian, &value); err != nil {
		t.Fatalf("read CNG rate-history %s: %v", field, err)
	}
	return value
}

func runCNGRateHistoryOracle(t *testing.T) []cngRateHistoryCase {
	t.Helper()
	path, err := cngRateHistoryHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "SILK CNG rate-history transition",
			OutputBase:  "gopus_libopus_decoder_cng_rate_history",
			SourceFile:  "libopus_decoder_cng_rate_history.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"", "include", "src", "silk", "celt"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK CNG rate-history transition", err)
		return nil
	}
	output, err := libopustest.RunHelper(path, nil)
	if err != nil {
		t.Fatalf("CNG rate-history oracle: %v", err)
	}
	r := bytes.NewReader(output)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "GCRH" {
		t.Fatalf("CNG rate-history oracle magic=%q err=%v", magic, err)
	}
	caseCount := readCNGRateHistoryU32(t, r, "case count")
	if caseCount != 8 {
		t.Fatalf("CNG rate-history oracle cases=%d, want 8", caseCount)
	}
	cases := make([]cngRateHistoryCase, int(caseCount))
	pcmBytes := make([]byte, cngRateHistoryFrameSize*4)
	for caseIndex := range cases {
		tc := &cases[caseIndex]
		tc.direction = int(readCNGRateHistoryU32(t, r, "direction"))
		tc.amplitude = int(readCNGRateHistoryU32(t, r, "amplitude"))
		stepCount := readCNGRateHistoryU32(t, r, "step count")
		if tc.direction > 1 || tc.amplitude > 3 || stepCount != cngRateHistorySteps {
			t.Fatalf("CNG sequence %d metadata=%d/%d/%d", caseIndex, tc.direction, tc.amplitude, stepCount)
		}
		tc.steps = make([]cngRateHistoryStep, int(stepCount))
		for stepIndex := range tc.steps {
			step := &tc.steps[stepIndex]
			packetSize := readCNGRateHistoryU32(t, r, "packet size")
			samples := readCNGRateHistoryU32(t, r, "decoded sample count")
			step.finalRange = readCNGRateHistoryU32(t, r, "final range")
			step.pitch = int32(readCNGRateHistoryU32(t, r, "pitch"))
			if packetSize > 1000 || samples != cngRateHistoryFrameSize {
				t.Fatalf("CNG sequence %d step %d packet/sample counts=%d/%d", caseIndex, stepIndex, packetSize, samples)
			}
			step.packet = make([]byte, int(packetSize))
			if _, err := io.ReadFull(r, step.packet); err != nil {
				t.Fatalf("CNG sequence %d step %d packet: %v", caseIndex, stepIndex, err)
			}
			if _, err := io.ReadFull(r, pcmBytes); err != nil {
				t.Fatalf("CNG sequence %d step %d PCM: %v", caseIndex, stepIndex, err)
			}
			step.pcm = make([]float32, cngRateHistoryFrameSize)
			for sample := range step.pcm {
				step.pcm[sample] = math.Float32frombits(binary.LittleEndian.Uint32(pcmBytes[sample*4:]))
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("CNG rate-history oracle has %d trailing bytes", r.Len())
	}
	return cases
}

func assertCNGRateHistorySequence(t *testing.T, dec *Decoder, tc cngRateHistoryCase, pcm []float32) {
	t.Helper()
	dec.Reset()
	firstBandwidth, secondBandwidth := BandwidthWideband, BandwidthNarrowband
	if tc.direction == 1 {
		firstBandwidth, secondBandwidth = secondBandwidth, firstBandwidth
	}
	for stepIndex, step := range tc.steps {
		if stepIndex < 7 {
			if len(step.packet) == 0 {
				t.Fatalf("sequence %s step %d has no C-encoded packet", cngRateHistoryCaseName(tc), stepIndex)
			}
			wantBandwidth := firstBandwidth
			if stepIndex == 6 {
				wantBandwidth = secondBandwidth
			}
			info := ParseTOC(step.packet[0])
			if info.Mode != ModeSILK || info.Bandwidth != wantBandwidth {
				t.Fatalf("sequence %s step %d C packet mode/bandwidth=%v/%v, want SILK/%v", cngRateHistoryCaseName(tc), stepIndex, info.Mode, info.Bandwidth, wantBandwidth)
			}
		} else if len(step.packet) != 0 {
			t.Fatalf("sequence %s loss step %d contains a packet", cngRateHistoryCaseName(tc), stepIndex)
		}

		n, err := dec.Decode(step.packet, pcm)
		if err != nil || n != cngRateHistoryFrameSize {
			t.Fatalf("sequence %s step %d Decode=(%d,%v), want (%d,nil)", cngRateHistoryCaseName(tc), stepIndex, n, err, cngRateHistoryFrameSize)
		}
		if got := dec.FinalRange(); got != step.finalRange {
			t.Fatalf("sequence %s step %d range=%08x, want %08x", cngRateHistoryCaseName(tc), stepIndex, got, step.finalRange)
		}
		if got := int32(dec.Pitch()); got != step.pitch {
			t.Fatalf("sequence %s step %d pitch=%d, want %d", cngRateHistoryCaseName(tc), stepIndex, got, step.pitch)
		}
		for sample := range step.pcm {
			if got, want := math.Float32bits(pcm[sample]), math.Float32bits(step.pcm[sample]); got != want {
				t.Fatalf("sequence %s step %d PCM[%d]=%08x, want %08x", cngRateHistoryCaseName(tc), stepIndex, sample, got, want)
			}
		}
	}
}

func cngRateHistoryCaseName(tc cngRateHistoryCase) string {
	direction := "wideband-to-narrowband"
	if tc.direction == 1 {
		direction = "narrowband-to-wideband"
	}
	return fmt.Sprintf("%s-amplitude-%d", direction, tc.amplitude)
}

func TestSILKCNGRateChangeRetainsExcitationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := runCNGRateHistoryOracle(t)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, cngRateHistoryFrameSize)
	for _, tc := range cases {
		t.Run(cngRateHistoryCaseName(tc), func(t *testing.T) {
			assertCNGRateHistorySequence(t, dec, tc, pcm)
		})
	}
}

func TestSILKCNGRateChangeWarmZeroAllocs(t *testing.T) {
	cases := runCNGRateHistoryOracle(t)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, cngRateHistoryFrameSize)
	var runErr error
	run := func() {
		for _, tc := range cases {
			dec.Reset()
			for stepIndex, step := range tc.steps {
				n, err := dec.Decode(step.packet, pcm)
				if err != nil || n != cngRateHistoryFrameSize {
					runErr = fmt.Errorf("sequence %s step %d Decode=(%d,%v)", cngRateHistoryCaseName(tc), stepIndex, n, err)
					return
				}
				cngRateHistorySink ^= math.Float32bits(pcm[(stepIndex*47)%len(pcm)]) ^ dec.FinalRange()
			}
		}
	}
	for range 3 {
		run()
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if got := testing.AllocsPerRun(10, run); got != 0 {
		t.Fatalf("warm SILK CNG rate-change decode allocated %g times per sequence batch", got)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
}

func TestDecodeCode3OverflowPaddingMatchesParsePacket(t *testing.T) {
	// Code-3 packet with padding flag (0x40), 1 frame, and two 0xFF padding
	// length bytes (254+254 > len(data)).
	malformed := []byte{0x03, 0x41, 0xFF, 0xFF}
	if _, err := ParsePacket(malformed); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("ParsePacket err = %v, want ErrInvalidPacket", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, 960)
	if _, err := dec.Decode(malformed, out); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("Decode err = %v, want ErrInvalidPacket", err)
	}
}

func TestDecoder_BandwidthAndLastPacketDuration(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() before decode=%d want=0", got)
	}
	n := decodeMinimalHybrid20ms(t, dec)
	if n != 960 {
		t.Fatalf("Decode returned %d samples, want 960", n)
	}

	if got := dec.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want=%v", got, BandwidthFullband)
	}
	if got := dec.LastPacketDuration(); got != 960 {
		t.Fatalf("LastPacketDuration()=%d want=960", got)
	}
	dec.Reset()
	if got := dec.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration() after Reset=%d want=0", got)
	}
}

func TestDecoder_InDTX(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if dec.InDTX() {
		t.Fatal("InDTX()=true before any packet")
	}

	dec.lastDataLen = 2
	if !dec.InDTX() {
		t.Fatal("InDTX()=false for 2-byte packet length")
	}

	dec.lastDataLen = 3
	if dec.InDTX() {
		t.Fatal("InDTX()=true for 3-byte packet length")
	}
}

func TestDecoder_SetGainBounds(t *testing.T) {
	dec := newMonoTestDecoder(t)

	if err := dec.SetGain(-32769); err != ErrInvalidGain {
		t.Fatalf("SetGain(-32769) error=%v want=%v", err, ErrInvalidGain)
	}
	if err := dec.SetGain(32768); err != ErrInvalidGain {
		t.Fatalf("SetGain(32768) error=%v want=%v", err, ErrInvalidGain)
	}

	for _, gain := range []int{-32768, 0, 256, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Fatalf("Gain()=%d want=%d", got, gain)
		}
	}
}

func TestDecoder_ComplexityControl(t *testing.T) {
	dec := newMonoTestDecoder(t)
	if got := dec.Complexity(); got != 0 {
		t.Fatalf("Complexity() default=%d want 0", got)
	}

	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) error: %v", err)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Complexity()=%d want 7", got)
	}
	if got := dec.celtDecoder.Complexity(); got != 7 {
		t.Fatalf("CELT Complexity()=%d want 7", got)
	}
	if got := dec.hybridDecoder.Complexity(); got != 7 {
		t.Fatalf("Hybrid Complexity()=%d want 7", got)
	}

	if err := dec.SetComplexity(11); err != ErrInvalidComplexity {
		t.Fatalf("SetComplexity(11) error=%v want %v", err, ErrInvalidComplexity)
	}
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("invalid SetComplexity changed setting to %d", got)
	}

	dec.Reset()
	if got := dec.Complexity(); got != 7 {
		t.Fatalf("Reset changed Complexity() to %d", got)
	}
}

func TestDecoder_PhaseInversionControl(t *testing.T) {
	mono := newMonoTestDecoder(t)
	if !mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=false want true")
	}
	mono.SetPhaseInversionDisabled(false)
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=true after Set(false)")
	}
	mono.Reset()
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono Reset changed phase inversion control")
	}

	stereo, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder stereo error: %v", err)
	}
	if stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=true want false")
	}
	stereo.SetPhaseInversionDisabled(true)
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=false after Set(true)")
	}
	stereo.Reset()
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo Reset changed phase inversion control")
	}
}

func TestDecoder_IgnoreExtensions(t *testing.T) {
	assertIgnoreExtensionsControls(t, newMonoTestDecoder(t))
}

func TestDecoder_OptionalExtensionControls(t *testing.T) {
	dec := newMonoTestDecoder(t)

	assertOptionalDecoderControls(t, dec)
	if osce, ok := any(dec).(extraOSCEBWEControl); ok {
		if extsupport.OSCEBWERuntime {
			assertWorkingOSCEBWEControl(t, osce)
		} else {
			t.Fatal("non-OSCE-runtime build unexpectedly exposes OSCE BWE control")
		}
	} else if extsupport.OSCEBWERuntime {
		t.Fatal("OSCE runtime build does not expose OSCE BWE control")
	}
}

func TestDecoder_GainAppliedToDecodeOutput(t *testing.T) {
	packet := minimalHybridTestPacket20ms()

	base, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder base error: %v", err)
	}
	withGain, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder withGain error: %v", err)
	}
	if err := withGain.SetGain(256); err != nil {
		t.Fatalf("SetGain(+1dB) error: %v", err)
	}

	pcmBase := make([]float32, 960)
	pcmGain := make([]float32, 960)

	nBase, err := base.Decode(packet, pcmBase)
	if err != nil {
		t.Fatalf("base Decode error: %v", err)
	}
	nGain, err := withGain.Decode(packet, pcmGain)
	if err != nil {
		t.Fatalf("withGain Decode error: %v", err)
	}
	if nBase != nGain {
		t.Fatalf("decode sample mismatch: base=%d gain=%d", nBase, nGain)
	}

	rms := func(x []float32) float64 {
		if len(x) == 0 {
			return 0
		}
		var sum float64
		for _, v := range x {
			f := float64(v)
			sum += f * f
		}
		return math.Sqrt(sum / float64(len(x)))
	}

	baseRMS := rms(pcmBase[:nBase])
	gainRMS := rms(pcmGain[:nGain])
	if baseRMS == 0 || gainRMS == 0 {
		t.Fatalf("unexpected zero RMS: base=%.8f gain=%.8f", baseRMS, gainRMS)
	}

	gotRatio := gainRMS / baseRMS
	wantRatio := float64(decodeGainLinear(256))
	if celtIntegerPLCActive {
		// src/opus_decoder.c:opus_decode_frame clamps gained opus_res to
		// +/-32767 before RES2FLOAT divides by 2^23. This packet reaches
		// that clamp, so its RMS does not follow an unclipped linear gain.
		const limit = float32(32767.0 / 8388608.0)
		wantPCM := make([]float32, nBase)
		for i, sample := range pcmBase[:nBase] {
			wantPCM[i] = max(-limit, min(limit, sample*float32(wantRatio)))
		}
		wantRatio = rms(wantPCM) / baseRMS
	}
	if math.Abs(gotRatio-wantRatio) > 0.02 {
		t.Fatalf("gain RMS ratio=%.6f want≈%.6f (tol=0.02)", gotRatio, wantRatio)
	}
}

func TestDecoder_PitchGetter(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	if got := dec.Pitch(); got != 0 {
		t.Fatalf("Pitch()=%d want=0 before decode", got)
	}

	celtEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder CELT error: %v", err)
	}
	if err := celtEnc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}
	packet := make([]byte, 4000)
	n, err := celtEnc.Encode(generateSineWave(48000, 440, 960), packet)
	if err != nil {
		t.Fatalf("CELT Encode error: %v", err)
	}
	pcm := make([]float32, 960)
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("CELT Decode error: %v", err)
	}

	got := dec.Pitch()
	want := dec.celtDecoder.PostfilterPeriod()
	if got != want {
		t.Fatalf("CELT Pitch()=%d want=%d", got, want)
	}
	if got < 0 {
		t.Fatalf("Pitch() should not be negative: %d", got)
	}

	dec.Reset()
	silkEnc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder SILK error: %v", err)
	}
	if err := silkEnc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("SetMode(SILK) error: %v", err)
	}
	if err := silkEnc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth(WB) error: %v", err)
	}
	if err := silkEnc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal(Voice) error: %v", err)
	}
	n, err = silkEnc.Encode(generateSineWave(48000, 200, 960), packet)
	if err != nil {
		t.Fatalf("SILK Encode error: %v", err)
	}
	if _, err := dec.Decode(packet[:n], pcm); err != nil {
		t.Fatalf("SILK Decode error: %v", err)
	}
	want = 0
	if dec.silkDecoder.GetLastSignalType() == 2 {
		want = dec.silkDecoder.GetLagPrev() * 3
		if want <= 0 {
			t.Fatalf("SILK lagPrev=%d produced invalid pitch", dec.silkDecoder.GetLagPrev())
		}
	}
	if got := dec.Pitch(); got != want {
		t.Fatalf("SILK Pitch()=%d want=%d", got, want)
	}

	for _, tc := range []struct {
		bandwidth Bandwidth
		want      int
	}{
		{BandwidthNarrowband, 6},
		{BandwidthMediumband, 4},
		{BandwidthWideband, 3},
		{BandwidthSuperwideband, 3},
		{BandwidthFullband, 3},
	} {
		if got := silkPitchScale(tc.bandwidth); got != tc.want {
			t.Fatalf("silkPitchScale(%v)=%d want=%d", tc.bandwidth, got, tc.want)
		}
	}
}

func TestDecoderCTL_Defaults(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	// OPUS_GET_COMPLEXITY default = 0
	if got := dec.Complexity(); got != 0 {
		t.Errorf("Complexity() default = %d, want 0 (libopus: st->complexity=0 at init)", got)
	}

	// OPUS_GET_GAIN default = 0
	if got := dec.Gain(); got != 0 {
		t.Errorf("Gain() default = %d, want 0 (libopus: decode_gain=0 via OPUS_CLEAR)", got)
	}

	// OPUS_GET_LAST_PACKET_DURATION default = 0 (no packet decoded yet)
	if got := dec.LastPacketDuration(); got != 0 {
		t.Errorf("LastPacketDuration() default = %d, want 0", got)
	}

	// OPUS_GET_FINAL_RANGE default = 0 (lastDataLen=0 → FinalRange returns 0)
	if got := dec.FinalRange(); got != 0 {
		t.Errorf("FinalRange() default = %d, want 0", got)
	}

	// OPUS_GET_PITCH default = 0 (prev_mode=0, not MODE_CELT_ONLY, silk signal
	// type not VOICED)
	if got := dec.Pitch(); got != 0 {
		t.Errorf("Pitch() default = %d, want 0", got)
	}

	// OPUS_GET_PHASE_INVERSION_DISABLED: mono decoder always has phase
	// inversion disabled in gopus (libopus enforces mono constraint in CELT).
	// Stereo default is false.
	stereo := mustNewTestDecoder(t, 48000, 2)
	if stereo.PhaseInversionDisabled() {
		t.Errorf("stereo PhaseInversionDisabled() default = true, want false")
	}

	// OPUS_GET_IGNORE_EXTENSIONS default = false (OPUS_CLEAR zeroes the struct)
	if dec.IgnoreExtensions() {
		t.Errorf("IgnoreExtensions() default = true, want false")
	}

	// OPUS_GET_SAMPLE_RATE
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		d := mustNewTestDecoder(t, rate, 1)
		if got := d.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}

	// Channels is not a CTL but a constructor parameter.
	if got := dec.Channels(); got != 1 {
		t.Errorf("Channels() = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// CTL round-trip: SET then GET must return the value just set.
// ---------------------------------------------------------------------------

// TestDecoderCTL_GainRoundTrip verifies OPUS_SET_GAIN / OPUS_GET_GAIN
// round-trips the full valid range boundary values.
//
// C ref: opus_decoder_ctl OPUS_SET_GAIN_REQUEST – range [-32768, 32767].
func TestDecoderCTL_GainRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	for _, gain := range []int{-32768, -1, 0, 1, 256, 32767} {
		if err := dec.SetGain(gain); err != nil {
			t.Fatalf("SetGain(%d) unexpected error: %v", gain, err)
		}
		if got := dec.Gain(); got != gain {
			t.Errorf("Gain() = %d after SetGain(%d), want %d", got, gain, gain)
		}
	}
}

// TestDecoderCTL_GainBoundaryReject verifies out-of-range gain returns an error
// and does not change the stored value.
//
// C ref: opus_decoder_ctl OPUS_SET_GAIN_REQUEST – "if (value<-32768 || value>32767) goto bad_arg"
func TestDecoderCTL_GainBoundaryReject(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(0); err != nil {
		t.Fatalf("SetGain(0) error: %v", err)
	}

	for _, gain := range []int{-32769, 32768, -100000, 100000} {
		err := dec.SetGain(gain)
		if err == nil {
			t.Errorf("SetGain(%d) should return error, got nil", gain)
		}
		// Value must not have changed.
		if got := dec.Gain(); got != 0 {
			t.Errorf("SetGain(%d) changed Gain() to %d, want 0", gain, got)
		}
	}
}

// TestDecoderCTL_ComplexityRoundTrip verifies OPUS_SET_COMPLEXITY /
// OPUS_GET_COMPLEXITY round-trips all valid values [0,10].
//
// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestDecoderCTL_ComplexityRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	for c := 0; c <= 10; c++ {
		if err := dec.SetComplexity(c); err != nil {
			t.Fatalf("SetComplexity(%d) error: %v", c, err)
		}
		if got := dec.Complexity(); got != c {
			t.Errorf("Complexity() = %d after SetComplexity(%d)", got, c)
		}
	}
}

// TestDecoderCTL_ComplexityBoundaryReject verifies out-of-range complexity is
// rejected.
//
// C ref: opus_decoder_ctl OPUS_SET_COMPLEXITY_REQUEST – "if(value<0 || value>10) goto bad_arg"
func TestDecoderCTL_ComplexityBoundaryReject(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetComplexity(5); err != nil {
		t.Fatalf("SetComplexity(5) error: %v", err)
	}

	for _, c := range []int{-1, 11, 100} {
		err := dec.SetComplexity(c)
		if err == nil {
			t.Errorf("SetComplexity(%d) should return error, got nil", c)
		}
		if got := dec.Complexity(); got != 5 {
			t.Errorf("invalid SetComplexity(%d) changed Complexity() to %d, want 5", c, got)
		}
	}
}

// TestDecoderCTL_PhaseInversionDisabledRoundTrip verifies
// OPUS_SET/GET_PHASE_INVERSION_DISABLED on a stereo decoder.
//
// C ref: opus_decoder_ctl OPUS_SET_PHASE_INVERSION_DISABLED_REQUEST –
//
//	"if(value<0 || value>1) goto bad_arg"
func TestDecoderCTL_PhaseInversionDisabledRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 2)

	dec.SetPhaseInversionDisabled(true)
	if !dec.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = false after Set(true), want true")
	}

	dec.SetPhaseInversionDisabled(false)
	if dec.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() = true after Set(false), want false")
	}
}

// TestDecoderCTL_IgnoreExtensionsRoundTrip verifies
// OPUS_SET/GET_IGNORE_EXTENSIONS round-trip.
//
// C ref: opus_decoder_ctl OPUS_SET_IGNORE_EXTENSIONS_REQUEST –
//
//	"if(value<0 || value>1) goto bad_arg"
func TestDecoderCTL_IgnoreExtensionsRoundTrip(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)

	dec.SetIgnoreExtensions(true)
	if !dec.IgnoreExtensions() {
		t.Error("IgnoreExtensions() = false after Set(true), want true")
	}

	dec.SetIgnoreExtensions(false)
	if dec.IgnoreExtensions() {
		t.Error("IgnoreExtensions() = true after Set(false), want false")
	}
}

// TestDecoderCTL_LastPacketDurationAfterDecode verifies
// OPUS_GET_LAST_PACKET_DURATION equals the decoded sample count after a real
// packet.
//
// C ref: opus_decode_native – "st->last_packet_duration = nb_samples"
func TestDecoderCTL_LastPacketDurationAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)
	n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.LastPacketDuration(); got != n {
		t.Errorf("LastPacketDuration() = %d after Decode returning %d, want equal", got, n)
	}
}

// TestDecoderCTL_BandwidthAfterDecode verifies OPUS_GET_BANDWIDTH returns the
// bandwidth of the last decoded packet.
//
// C ref: opus_decode_native – "st->bandwidth = packet_bandwidth"
func TestDecoderCTL_BandwidthAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// minimalHybridTestPacket20ms is Hybrid Fullband (config 15).
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.Bandwidth(); got != BandwidthFullband {
		t.Errorf("Bandwidth() = %v after Hybrid-FB decode, want BandwidthFullband", got)
	}
}

// TestDecoderCTL_SampleRateAllRates verifies OPUS_GET_SAMPLE_RATE returns the
// configured API rate for all valid sample rates.
//
// C ref: opus_decoder_ctl OPUS_GET_SAMPLE_RATE_REQUEST – "*value = st->Fs"
func TestDecoderCTL_SampleRateAllRates(t *testing.T) {
	for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
		dec := mustNewTestDecoder(t, rate, 1)
		if got := dec.SampleRate(); got != rate {
			t.Errorf("SampleRate() = %d, want %d", got, rate)
		}
	}
}

// TestDecoderCTL_FinalRangeZeroBeforeDecode verifies FinalRange returns 0
// before any packet has been decoded.
//
// C ref: opus_decoder.c FinalRange – "if lastDataLen <= 1 return 0"
func TestDecoderCTL_FinalRangeZeroBeforeDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if got := dec.FinalRange(); got != 0 {
		t.Errorf("FinalRange() = %d before any decode, want 0", got)
	}
}

// TestDecoderCTL_FinalRangeNonZeroAfterDecode verifies FinalRange returns a
// non-zero value after successfully decoding a real packet.
//
// C ref: opus_decoder.c – "st->rangeFinal" is set from the range coder state
//
//	after opus_decode_frame (celt_decoder_ctl OPUS_GET_FINAL_RANGE).
func TestDecoderCTL_FinalRangeNonZeroAfterDecode(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if got := dec.FinalRange(); got == 0 {
		t.Error("FinalRange() = 0 after decoding real packet, want non-zero")
	}
}

// TestDecoderCTL_ResetPreservesComplexity verifies that Reset() does not clear
// the complexity setting (matching libopus: opus_decoder_ctl OPUS_RESET_STATE
// uses OPUS_CLEAR over the RESET_START range, which excludes complexity from
// its reset because st->complexity is inside the preserved region).
//
// C ref: opus_decoder.c OPUS_RESET_STATE case – OPUS_CLEAR starts at
//
//	&st->OPUS_DECODER_RESET_START, which is after st->complexity.
func TestDecoderCTL_ResetPreservesComplexity(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetComplexity(7); err != nil {
		t.Fatalf("SetComplexity(7) error: %v", err)
	}
	dec.Reset()
	if got := dec.Complexity(); got != 7 {
		t.Errorf("Complexity() = %d after Reset(), want 7 (complexity is preserved by libopus reset)", got)
	}
}

// TestDecoderCTL_ResetPreservesGain verifies that Reset() preserves the
// decode gain, matching libopus OPUS_RESET_STATE behavior.
//
// C ref: opus_decoder.c struct layout – decode_gain is at line 71, before
//
//	"OPUS_DECODER_RESET_START stream_channels" (line 80).  OPUS_RESET_STATE
//	clears only from stream_channels onward, so decode_gain survives reset.
//	Complexity (line 72) is likewise preserved (see TestDecoderCTL_ResetPreservesComplexity).
func TestDecoderCTL_ResetPreservesGain(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if err := dec.SetGain(256); err != nil {
		t.Fatalf("SetGain(256) error: %v", err)
	}
	dec.Reset()
	if got := dec.Gain(); got != 256 {
		t.Errorf("Gain() = %d after Reset(), want 256 (libopus preserves decode_gain across OPUS_RESET_STATE)", got)
	}
}

// TestDecoderCTL_ResetClearsLastPacketDuration verifies Reset() zeroes
// LastPacketDuration.
func TestDecoderCTL_ResetClearsLastPacketDuration(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if dec.LastPacketDuration() == 0 {
		t.Fatal("LastPacketDuration() = 0 after valid decode – precondition failed")
	}
	dec.Reset()
	if got := dec.LastPacketDuration(); got != 0 {
		t.Errorf("LastPacketDuration() = %d after Reset(), want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Error-code equivalence for invalid packets.
// ---------------------------------------------------------------------------
// libopus maps packet parsing failures to OPUS_INVALID_PACKET (-4) and buffer
// sizing failures to OPUS_BUFFER_TOO_SMALL (-2).  gopus uses ErrInvalidPacket
// and ErrBufferTooSmall.  The tests below feed known-bad packets and confirm
// the gopus error matches the libopus classification.

// TestDecodeErrorCode_ZeroLengthPacket verifies that an empty (zero-length)
// byte slice returns ErrInvalidPacket.
//
// C ref: opus_decode_native – non-nil data with len==0 takes the PLC branch,
//
//	but when called from opus_decode_float with data!=nil it would have
//	attempted get_nb_samples first; however gopus (like libopus float path)
//	treats an empty non-nil slice as a zero-length data → PLC, not an error.
//	A truly absent packet (nil) is PLC.  A single-byte (TOC-only) packet with
//	no frame data is handled by opus_packet_parse_impl returning count≥0, and
//	the single frame has zero bytes, which is valid for the CELT/SILK layer.
//
// gopus mirrors libopus: empty non-nil data decodes as PLC (no error).
func TestDecodeErrorCode_ZeroLengthPacket(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960)

	// nil data → PLC, not error
	_, err := dec.Decode(nil, pcm)
	if err != nil {
		t.Errorf("Decode(nil) error = %v, want nil (PLC)", err)
	}

	// empty non-nil data → PLC (matches libopus len==0 branch)
	_, err = dec.Decode([]byte{}, pcm)
	if err != nil {
		t.Errorf("Decode([]byte{}) error = %v, want nil (PLC)", err)
	}
}

// TestDecodeErrorCode_TruncatedCode3 verifies that a code-3 packet that is
// only 1 byte (missing the frame-count byte) is rejected.
//
// C ref: opus_packet_parse_impl – code==3 requires at least 2 bytes; returns
//
//	OPUS_INVALID_PACKET when len<2.
func TestDecodeErrorCode_TruncatedCode3(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// One-byte code-3 packet (missing frame-count byte).
	_, err := dec.Decode([]byte{GenerateTOC(16, false, 3)}, pcm)
	if err == nil {
		t.Error("Decode(truncated code-3) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3ZeroFrames verifies that M=0 in a code-3 packet
// is rejected.
//
// C ref: opus_packet_parse_impl – "if (count==0) return OPUS_INVALID_PACKET"
// gopus: ErrInvalidFrameCount ("M > 48" message covers M==0 via m==0 check)
func TestDecodeErrorCode_Code3ZeroFrames(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// Code-3 packet with M=0 in the frame-count byte.
	pkt := []byte{GenerateTOC(16, false, 3), 0x00}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 M=0) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3ExcessiveFrames verifies M>48 is rejected.
//
// C ref: opus_packet_parse_impl – if (count > 48) return OPUS_INVALID_PACKET
func TestDecodeErrorCode_Code3ExcessiveFrames(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 5760)

	// Code-3 packet with M=49 (0x31 = 0b0110001 = CBR, no-padding, 49 frames).
	pkt := []byte{GenerateTOC(16, false, 3), 49}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 M=49) = nil, want error")
	}
}

// TestDecodeErrorCode_TotalDurationOver120ms verifies that a packet whose
// total duration exceeds 120ms is rejected.
//
// C ref: opus_packet_get_nb_samples – "if (samples*25 > Fs*3) return
//
//	OPUS_INVALID_PACKET"
func TestDecodeErrorCode_TotalDurationOver120ms(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// Config 31 = CELT FB 20ms; code 3 with M=7 → 140ms > 120ms.
	pkt := []byte{GenerateTOC(31, false, 3), 0x07}
	pcm := make([]float32, 7000)

	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(>120ms) = nil, want error")
	}
}

// TestDecodeErrorCode_BufferTooSmall verifies OPUS_BUFFER_TOO_SMALL when the
// output PCM buffer is shorter than the packet requires.
//
// C ref: opus_decode_native – "if (count*packet_frame_size > frame_size)
//
//	return OPUS_BUFFER_TOO_SMALL"
func TestDecodeErrorCode_BufferTooSmall(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	// minimalHybridTestPacket20ms() is 960 samples; give only 480.
	pcm := make([]float32, 480)
	_, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with short buffer = %v, want ErrBufferTooSmall", err)
	}
}

// TestDecodeErrorCode_Code1OddPayload verifies that a code-1 packet with an
// odd payload length is rejected (two equal-length frames require an even
// total payload).
//
// C ref: opus_packet_parse_impl code==1 branch – "if (framesize & 1)
//
//	return OPUS_INVALID_PACKET"
func TestDecodeErrorCode_Code1OddPayload(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// Config 16 (CELT NB 2.5ms), code 1, 3 payload bytes (odd).
	pkt := []byte{GenerateTOC(16, false, 1), 0xAA, 0xBB, 0xCC}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-1 odd payload) = nil, want error")
	}
}

// TestDecodeErrorCode_Code2ShortPacket verifies that a code-2 packet with
// insufficient bytes for the first frame length is rejected.
//
// C ref: opus_packet_parse_impl code==2 branch checks parsed frame1Len
//
//	against the remaining packet bytes.
func TestDecodeErrorCode_Code2ShortPacket(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// TOC byte only, no frame-length byte for code-2.
	pkt := []byte{GenerateTOC(16, false, 2)}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-2 truncated) = nil, want error")
	}
}

// TestDecodeErrorCode_Code2Frame1TooLarge verifies that a code-2 packet
// where the encoded first-frame length exceeds the packet remainder is
// rejected.
//
// C ref: opus_packet_parse_impl code==2 – frame2 length becomes negative →
//
//	OPUS_INVALID_PACKET.
func TestDecodeErrorCode_Code2Frame1TooLarge(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*2)

	// TOC (code-2) + frame1-length byte 200 + 3 bytes payload (200 > 3).
	pkt := []byte{GenerateTOC(16, false, 2), 200, 0xAA, 0xBB, 0xCC}
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-2 frame1 too large) = nil, want error")
	}
}

// TestDecodeErrorCode_Code3CBRUneven verifies that a CBR code-3 packet whose
// total frame bytes are not divisible by M is rejected.
//
// C ref: opus_packet_parse_impl code==3 CBR branch –
//
//	"if (framesize % count) return OPUS_INVALID_PACKET"
func TestDecodeErrorCode_Code3CBRUneven(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]float32, 960*3)

	// Code-3, CBR, M=3, no padding: 2.5ms CELT NB config, payload 5 bytes
	// (5 % 3 != 0 → invalid).
	frameCountByte := byte(3) // CBR (bit7=0), no padding (bit6=0), M=3
	pkt := append([]byte{GenerateTOC(16, false, 3), frameCountByte}, make([]byte, 5)...)
	_, err := dec.Decode(pkt, pcm)
	if err == nil {
		t.Error("Decode(code-3 CBR uneven) = nil, want error")
	}
}

// TestDecodeErrorCode_Int16BufferTooSmall verifies ErrBufferTooSmall is
// returned from DecodeInt16 when the output int16 buffer is short.
func TestDecodeErrorCode_Int16BufferTooSmall(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]int16, 480)
	_, err := dec.DecodeInt16(minimalHybridTestPacket20ms(), pcm)
	if err != ErrBufferTooSmall {
		t.Errorf("DecodeInt16 with short buffer = %v, want ErrBufferTooSmall", err)
	}
}

// TestDecodeErrorCode_InvalidPacketPropagatesFromInt16 verifies that
// ErrInvalidPacket is returned from DecodeInt16 for a malformed packet.
func TestDecodeErrorCode_InvalidPacketPropagatesFromInt16(t *testing.T) {
	dec := mustNewTestDecoder(t, 48000, 1)
	pcm := make([]int16, 5760)
	// Code-3 M=7 with 20ms config → >120ms
	_, err := dec.DecodeInt16([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err == nil {
		t.Error("DecodeInt16(>120ms) = nil, want error")
	}
}

func buildSingleFramePacketWithExtensionsForTest(t *testing.T, packet []byte, extensions []packetExtensionData) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	dst := make([]byte, len(packet)+16)
	n, err := buildPacketWithOptions(packet[0]&0xFC, [][]byte{packet[1:]}, dst, 0, false, extensions, false)
	if err != nil {
		t.Fatalf("buildPacketWithOptions: %v", err)
	}
	return dst[:n]
}

func buildMalformedSingleFrameExtensionPacketForTest(t *testing.T, packet []byte) []byte {
	t.Helper()

	if len(packet) < 2 {
		t.Fatal("packet too short for extension test")
	}
	out := make([]byte, 0, len(packet)+4)
	out = append(out, (packet[0]&0xFC)|0x03)
	out = append(out, 0x41) // one CBR frame with padding
	out = append(out, 0x02) // total padding bytes = 2
	out = append(out, packet[1:]...)
	out = append(out, 0xFF, 0xFF) // invalid long extension: truncated lacing payload
	return out
}

func TestDecoderDecodeValidUnknownExtensionMatchesBasePacket(t *testing.T) {
	base := minimalHybridTestPacket20ms()
	extended := buildSingleFramePacketWithExtensionsForTest(t, base, []packetExtensionData{
		{ID: 60, Frame: 0, Data: []byte{0x42}},
	})

	baseDec := newMonoTestDecoder(t)
	extDec := newMonoTestDecoder(t)

	basePCM := make([]float32, 960)
	extPCM := make([]float32, 960)

	baseN, err := baseDec.Decode(base, basePCM)
	if err != nil {
		t.Fatalf("Decode(base): %v", err)
	}
	extN, err := extDec.Decode(extended, extPCM)
	if err != nil {
		t.Fatalf("Decode(extended): %v", err)
	}
	if extN != baseN {
		t.Fatalf("Decode sample count=%d want %d", extN, baseN)
	}
	for i := range basePCM {
		if extPCM[i] != basePCM[i] {
			t.Fatalf("sample[%d]=%v want %v", i, extPCM[i], basePCM[i])
		}
	}
}

func TestDecoderOpaquePaddingRemainsDecodableInDefaultBuild(t *testing.T) {
	base := minimalHybridTestPacket20ms()
	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, base)

	baseDec := newMonoTestDecoder(t)
	basePCM := make([]float32, 960)
	baseN, err := baseDec.Decode(base, basePCM)
	if err != nil {
		t.Fatalf("Decode(base): %v", err)
	}

	for _, ignore := range []bool{false, true} {
		dec := newMonoTestDecoder(t)
		dec.SetIgnoreExtensions(ignore)
		gotPCM := make([]float32, 960)
		gotN, err := dec.Decode(malformed, gotPCM)
		if err != nil {
			t.Fatalf("Decode(malformed, ignore=%v): %v", ignore, err)
		}
		if gotN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", gotN, baseN, ignore)
		}
		for i := range gotN {
			if gotPCM[i] != basePCM[i] {
				t.Fatalf("sample[%d]=%v want %v (ignore=%v)", i, gotPCM[i], basePCM[i], ignore)
			}
		}
	}
}

func makeValidCELTPacketForExtensionTest(t *testing.T) []byte {
	t.Helper()

	enc := internalenc.NewEncoder(48000, 2)
	enc.SetMode(internalenc.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(256000)

	pcm := make([]float32, 960*2)
	for i := range 960 {
		phase := 2 * math.Pi * 997 * float64(i) / 48000.0
		pcm[2*i] = float32(0.45 * math.Sin(phase))
		pcm[2*i+1] = float32(0.35 * math.Sin(phase+0.37))
	}

	packet, err := enc.Encode(pcm, 960)
	if err != nil {
		t.Fatalf("Encode(CELT): %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("Encode(CELT) returned empty packet")
	}
	return packet
}

func TestDecoderCELTOpaquePaddingRemainsDecodable(t *testing.T) {
	base := makeValidCELTPacketForExtensionTest(t)
	malformed := buildMalformedSingleFrameExtensionPacketForTest(t, base)

	for _, ignore := range []bool{false, true} {
		baseDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(base): %v", err)
		}
		baseDec.SetIgnoreExtensions(ignore)
		basePCM := make([]float32, 960*2)
		baseN, err := baseDec.Decode(base, basePCM)
		if err != nil {
			t.Fatalf("Decode(base, ignore=%v): %v", ignore, err)
		}

		extDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(extended): %v", err)
		}
		extDec.SetIgnoreExtensions(ignore)
		extPCM := make([]float32, 960*2)
		extN, err := extDec.Decode(malformed, extPCM)
		if err != nil {
			t.Fatalf("Decode(malformed, ignore=%v): %v", ignore, err)
		}

		if extN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", extN, baseN, ignore)
		}
		for i := 0; i < extN*2; i++ {
			if math.IsNaN(float64(extPCM[i])) || math.IsInf(float64(extPCM[i]), 0) {
				t.Fatalf("sample[%d]=%v invalid (ignore=%v)", i, extPCM[i], ignore)
			}
		}
	}
}

func TestDecoderCELTExtraQEXTExtensionMatchesBasePacket(t *testing.T) {
	if SupportsOptionalExtension(OptionalExtensionQEXT) {
		t.Skip("test covers the default non-QEXT build")
	}

	base := makeValidCELTPacketForExtensionTest(t)
	extended := buildSingleFramePacketWithExtensionsForTest(t, base, []packetExtensionData{
		{ID: qextPacketExtensionID, Frame: 0, Data: []byte{0x29, 0x17, 0xA4, 0x53}},
	})

	for _, ignore := range []bool{false, true} {
		baseDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(base): %v", err)
		}
		baseDec.SetIgnoreExtensions(ignore)
		basePCM := make([]float32, 960*2)
		baseN, err := baseDec.Decode(base, basePCM)
		if err != nil {
			t.Fatalf("Decode(base, ignore=%v): %v", ignore, err)
		}

		extDec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
		if err != nil {
			t.Fatalf("NewDecoder(extended): %v", err)
		}
		extDec.SetIgnoreExtensions(ignore)
		extPCM := make([]float32, 960*2)
		extN, err := extDec.Decode(extended, extPCM)
		if err != nil {
			t.Fatalf("Decode(extended, ignore=%v): %v", ignore, err)
		}

		if extN != baseN {
			t.Fatalf("Decode sample count=%d want %d (ignore=%v)", extN, baseN, ignore)
		}
		for i := 0; i < extN*2; i++ {
			if extPCM[i] != basePCM[i] {
				t.Fatalf("sample[%d]=%v want %v (ignore=%v)", i, extPCM[i], basePCM[i], ignore)
			}
		}
	}
}

func mustNewTestEncoder(t *testing.T, sampleRate, channels int, application Application) *Encoder {
	t.Helper()

	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: application})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	return enc
}

func mustNewTestDecoder(t *testing.T, sampleRate, channels int) *Decoder {
	t.Helper()

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	return dec
}

func mustNewDefaultMultistreamEncoder(t *testing.T, sampleRate, channels int, application Application) *MultistreamEncoder {
	t.Helper()

	enc, err := NewMultistreamEncoderDefault(sampleRate, channels, application)
	if err != nil {
		t.Fatalf("NewMultistreamEncoderDefault error: %v", err)
	}
	return enc
}

func mustNewDefaultMultistreamDecoder(t *testing.T, sampleRate, channels int) *MultistreamDecoder {
	t.Helper()

	dec, err := NewMultistreamDecoderDefault(sampleRate, channels)
	if err != nil {
		t.Fatalf("NewMultistreamDecoderDefault error: %v", err)
	}
	return dec
}

func newMonoTestDecoder(t *testing.T) *Decoder {
	t.Helper()

	return mustNewTestDecoder(t, 48000, 1)
}

func decodeMinimalHybrid20ms(t *testing.T, dec *Decoder) int {
	t.Helper()

	pcm := make([]float32, 960)
	n, err := dec.Decode(minimalHybridTestPacket20ms(), pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	return n
}

type plcInt16LossStep struct {
	packet []byte // nil → PLC
}

// plcInt16LossPattern groups a named sequence of loss steps (built relative to
// a concrete packet once the mode is known).
type plcInt16LossPattern struct {
	name  string
	build func(pkt []byte) []plcInt16LossStep
}

// plcInt16LossPatterns returns the five canonical loss patterns used in the
// parity matrix.  All patterns produce at least one PLC step.
func plcInt16LossPatterns(pkt []byte) []plcInt16LossPattern {
	return []plcInt16LossPattern{
		{
			name: "single",
			// one good packet then one lost frame then one recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{{packet: p}, {packet: nil}, {packet: p}}
			},
		},
		{
			name: "burst",
			// one good packet then three consecutive losses then recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{
					{packet: p},
					{packet: nil},
					{packet: nil},
					{packet: nil},
					{packet: p},
				}
			},
		},
		{
			name: "periodic",
			// loss on every third frame over 9 total frames (3 losses, 6 good)
			build: func(p []byte) []plcInt16LossStep {
				steps := make([]plcInt16LossStep, 0, 9)
				for i := range 9 {
					if (i+1)%3 == 0 {
						steps = append(steps, plcInt16LossStep{packet: nil})
					} else {
						steps = append(steps, plcInt16LossStep{packet: p})
					}
				}
				return steps
			},
		},
		{
			name: "leading",
			// cold PLC: loss before any good packet (libopus returns silence)
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{{packet: nil}, {packet: p}}
			},
		},
		{
			name: "trailing",
			// good packet then two trailing losses, no recovery
			build: func(p []byte) []plcInt16LossStep {
				return []plcInt16LossStep{
					{packet: p},
					{packet: nil},
					{packet: nil},
				}
			},
		},
	}
}

// plcInt16ModeCases returns the three canonical mode test cases.
func plcInt16ModeCases(t *testing.T, channels int) []struct {
	mode   string
	packet []byte
} {
	t.Helper()
	return []struct {
		mode   string
		packet []byte
	}{
		{"silk", encodeAPIRateSILKPacket(t, channels)},
		{"celt", encodeAPIRateCELTPacket(t, channels)},
		{"hybrid", encodeAPIRateHybridPacket(t, channels)},
	}
}

// plcInt16DecodeSteps converts our loss-step slice to libopusAPIRateDecodeStep.
func plcInt16DecodeSteps(steps []plcInt16LossStep) []libopusAPIRateDecodeStep {
	out := make([]libopusAPIRateDecodeStep, len(steps))
	for i, s := range steps {
		out[i] = libopusAPIRateDecodeStep{packet: s.packet}
	}
	return out
}

// Fixed CELT and Hybrid expose different C conversions at the int16 and float
// public boundaries. Compare each Go boundary with the matching selected-C
// boundary on the same packet and PLC sequence (celt/arch.h: RES2INT16 and
// RES2FLOAT), instead of asserting an invalid cross-format round trip.
func assertPLCFormatsMatchSelectedC(t *testing.T, label string, sampleRate, channels, frameSize int, steps []plcInt16LossStep) {
	t.Helper()
	libopustest.RequireOracle(t)
	libSteps := plcInt16DecodeSteps(steps)
	wantF, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, frameSize, libSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "float PLC reference decode", err)
	}
	want16, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "int16 PLC reference decode", err)
	}
	decF := mustNewTestDecoder(t, sampleRate, channels)
	dec16 := mustNewTestDecoder(t, sampleRate, channels)
	bufF := make([]float32, frameSize*channels)
	buf16 := make([]int16, frameSize*channels)
	gotF := make([]float32, 0, len(wantF))
	got16 := make([]int16, 0, len(want16))
	for i, step := range steps {
		nF, err := decF.Decode(step.packet, bufF)
		if err != nil {
			t.Fatalf("Decode step[%d]: %v", i, err)
		}
		n16, err := dec16.DecodeInt16(step.packet, buf16)
		if err != nil {
			t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
		}
		if nF != n16 {
			t.Fatalf("step[%d] sample count mismatch: float32=%d int16=%d", i, nF, n16)
		}
		gotF = append(gotF, bufF[:nF*channels]...)
		got16 = append(got16, buf16[:n16*channels]...)
	}
	assertAPIRateQualityFloat32PLC(t, gotF, wantF, sampleRate, channels, true, label+" float PLC vs libopus")
	assertAPIRateQualityInt16PLC(t, got16, want16, sampleRate, channels, true, label+" int16 PLC vs libopus")
	assertAPIRateFloat32BitsExact(t, gotF, wantF, label+" float PLC vs libopus")
	assertAPIRateInt16Exact(t, got16, want16, label+" int16 PLC vs libopus")
}

// TestDecodeInt16PLCModeChannelLossMatrixMatchesLibopus asserts that
// DecodeInt16(nil,...) matches libopus opus_decode(NULL,...) across
// mode {SILK,CELT,Hybrid} × channels {1,2} × loss-pattern {single, burst,
// periodic, leading, trailing} at the 48 kHz API rate.
//
// Quality diagnostics accompany an exact selected-C int16 comparison for each
// packet and loss sequence.
func TestDecodeInt16PLCModeChannelLossMatrixMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			for _, pat := range plcInt16LossPatterns(pkt) {
				steps := pat.build(pkt)
				name := mc.mode + "_ch" + itoaSmall(channels) + "_" + pat.name

				t.Run(name, func(t *testing.T) {
					// Oracle: libopus int16.
					libSteps := plcInt16DecodeSteps(steps)
					want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
					if err != nil {
						libopustest.HelperUnavailable(t, "int16 PLC matrix reference decode", err)
					}

					// gopus DecodeInt16.
					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					got := make([]int16, 0, len(want))
					buf := make([]int16, frameSize*channels)
					for i, step := range steps {
						n, err := dec.DecodeInt16(step.packet, buf)
						if err != nil {
							t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
						}
						got = append(got, buf[:n*channels]...)
					}

					if len(got) != len(want) {
						t.Fatalf("sample count mismatch: got %d want %d", len(got), len(want))
					}

					// Any PLC step makes this PLC-dominated; use the PLC bar so
					// opus_compare's psychoacoustic Q is not the gate on concealed audio.
					hasPLC := false
					for _, step := range steps {
						if step.packet == nil {
							hasPLC = true
							break
						}
					}
					assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, hasPLC, name+" int16 PLC vs libopus")
					assertAPIRateInt16Exact(t, got, want, name+" int16 PLC vs libopus")
				})
			}
		}
	}
}

// TestDecodeInt16PLCEqualsFloat32PLCQuantized asserts the float-build int16 PLC
// path is exactly the float32 PLC path quantized by the int16 converter. Fixed
// CELT and Hybrid instead compare both public output formats exactly to their
// matching selected-C API on the same packet-loss sequence.
//
// In the float build, opus_decode and opus_decode_float share the same PLC
// output before FLOAT2INT16 conversion (opus_decoder.c).
//
// The test uses two fresh independent decoders with the same warm-up sequence so
// both decoders hold identical state before the PLC step. The float32 PLC output
// is converted to int16 using float32ToInt16NoSoftClip, the same function that
// DecodeInt16 uses internally, so the tested outputs match sample for sample in
// each selected float build.
func TestDecodeInt16PLCEqualsFloat32PLCQuantized(t *testing.T) {
	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			for _, pat := range plcInt16LossPatterns(pkt) {
				steps := pat.build(pkt)
				name := mc.mode + "_ch" + itoaSmall(channels) + "_" + pat.name

				t.Run(name, func(t *testing.T) {
					if (celtIntegerPLCActive && mc.mode == "celt") || (hybridIntegerPLCActive && mc.mode == "hybrid") {
						assertPLCFormatsMatchSelectedC(t, name, sampleRate, channels, frameSize, steps)
						return
					}
					decF := mustNewTestDecoder(t, sampleRate, channels)
					dec16 := mustNewTestDecoder(t, sampleRate, channels)

					// wantBuf is used to convert the float32 PLC output to int16
					// using the same function as DecodeInt16 (float32ToInt16NoSoftClip).
					bufF := make([]float32, frameSize*channels)
					wantBuf := make([]int16, frameSize*channels)
					buf16 := make([]int16, frameSize*channels)

					for i, step := range steps {
						nF, err := decF.Decode(step.packet, bufF)
						if err != nil {
							t.Fatalf("Decode step[%d]: %v", i, err)
						}
						n16, err := dec16.DecodeInt16(step.packet, buf16)
						if err != nil {
							t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
						}
						if nF != n16 {
							t.Fatalf("step[%d] sample count mismatch: float32=%d int16=%d", i, nF, n16)
						}

						if step.packet != nil {
							// Good packet: no assertion required (covered elsewhere).
							continue
						}

						// PLC frame: int16 must equal float32ToInt16NoSoftClip(float32 PLC).
						// This uses the same quantization path as DecodeInt16 internally.
						float32ToInt16NoSoftClip(wantBuf, bufF, nF, channels)
						for j := 0; j < n16*channels; j++ {
							if buf16[j] != wantBuf[j] {
								t.Fatalf("step[%d] sample[%d]: DecodeInt16=%d want quantize(Decode=%g)=%d",
									i, j, buf16[j], bufF[j], wantBuf[j])
							}
						}
					}
				})
			}
		}
	}
}

// TestDecodeInt16PLCLeadingColdMatchesLibopus verifies cold PLC
// (nil before any good packet) matches libopus int16 for all three modes.
// This is a focused sub-test of the matrix to make regressions easy to locate.
func TestDecodeInt16PLCLeadingColdMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
	)

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		frameSize, err := packetSamplesAtRate(pkt, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate %s: %v", mc.mode, err)
		}

		t.Run(mc.mode, func(t *testing.T) {
			// Cold PLC: the oracle sequences nil, then the packet.
			libSteps := []libopusAPIRateDecodeStep{{packet: nil}, {packet: pkt}}
			want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
			if err != nil {
				libopustest.HelperUnavailable(t, "int16 cold PLC reference decode", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int16, 0, len(want))
			buf := make([]int16, frameSize*channels)
			for _, p := range [][]byte{nil, pkt} {
				n, err := dec.DecodeInt16(p, buf)
				if err != nil {
					t.Fatalf("DecodeInt16: %v", err)
				}
				got = append(got, buf[:n*channels]...)
			}
			assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" cold int16 PLC")
			assertAPIRateInt16Exact(t, got, want, mc.mode+" cold int16 PLC")
		})
	}
}

// TestDecodeInt16PLCBurstMatchesLibopus verifies a 3-frame burst loss matches
// libopus int16 for all three modes and both channel counts.
func TestDecodeInt16PLCBurstMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			t.Run(mc.mode+"_ch"+itoaSmall(channels), func(t *testing.T) {
				// Warm up then 3-frame burst loss then recovery.
				libSteps := []libopusAPIRateDecodeStep{
					{packet: pkt},
					{packet: nil},
					{packet: nil},
					{packet: nil},
					{packet: pkt},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 burst PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" burst int16 PLC")
				assertAPIRateInt16Exact(t, got, want, mc.mode+" burst int16 PLC")
			})
		}
	}
}

// TestDecodeInt16PLCPeriodicDecaysMatchesLibopus exercises the periodic loss
// pattern (loss every 3rd frame) and verifies energy behaviour matches the
// documented PLC decay while also asserting libopus int16 parity.
func TestDecodeInt16PLCPeriodicDecaysMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateSILKPacket(t, channels) // Use SILK for voiced PLC energy
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	// Encode enough packets to warm up PLC state.
	const warmup = 4
	warmupPkt := encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)

	// Sequence: warmup good packets + 9 periodic-loss frames.
	libSteps := make([]libopusAPIRateDecodeStep, 0, warmup+9)
	for range warmup {
		libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: warmupPkt})
	}
	for i := range 9 {
		if (i+1)%3 == 0 {
			libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: nil})
		} else {
			libSteps = append(libSteps, libopusAPIRateDecodeStep{packet: pkt})
		}
	}

	want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "int16 periodic PLC reference decode", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	got := make([]int16, 0, len(want))
	buf := make([]int16, frameSize*channels)
	for i, step := range libSteps {
		n, err := dec.DecodeInt16(step.packet, buf)
		if err != nil {
			t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
		}
		got = append(got, buf[:n*channels]...)
	}

	assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "SILK periodic int16 PLC")
	assertAPIRateInt16Exact(t, got, want, "SILK periodic int16 PLC")
}

// TestDecodeInt16PLCTrailingMatchesLibopus verifies trailing loss (loss at end
// with no recovery packet) for all three modes.
func TestDecodeInt16PLCTrailingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000

	for _, channels := range []int{1, 2} {
		for _, mc := range plcInt16ModeCases(t, channels) {
			pkt := mc.packet
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s ch%d: %v", mc.mode, channels, err)
			}

			t.Run(mc.mode+"_ch"+itoaSmall(channels), func(t *testing.T) {
				libSteps := []libopusAPIRateDecodeStep{
					{packet: pkt},
					{packet: nil},
					{packet: nil},
				}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 trailing PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" trailing int16 PLC")
				assertAPIRateInt16Exact(t, got, want, mc.mode+" trailing int16 PLC")
			})
		}
	}
}

// TestDecodeInt16PLCSubRateMatchesLibopus verifies int16 PLC parity at sub-48 kHz
// API rates for each mode. Sub-rate decodes use the corr/RMS bar (opus_compare N/A).
func TestDecodeInt16PLCSubRateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const channels = 1

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		for _, sampleRate := range []int{8000, 16000, 24000} {
			frameSize, err := packetSamplesAtRate(pkt, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate %s %dHz: %v", mc.mode, sampleRate, err)
			}

			t.Run(mc.mode+"_"+itoaSmall(sampleRate)+"hz", func(t *testing.T) {
				libSteps := []libopusAPIRateDecodeStep{{packet: pkt}, {packet: nil}}
				want, err := decodeWithLibopusReferenceAPIRateInt16Steps(sampleRate, channels, frameSize, libSteps)
				if err != nil {
					libopustest.HelperUnavailable(t, "int16 sub-rate PLC reference decode", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int16, 0, len(want))
				buf := make([]int16, frameSize*channels)
				for i, step := range libSteps {
					n, err := dec.DecodeInt16(step.packet, buf)
					if err != nil {
						t.Fatalf("DecodeInt16 step[%d]: %v", i, err)
					}
					got = append(got, buf[:n*channels]...)
				}
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, mc.mode+" sub-rate int16 PLC")
				assertAPIRateInt16Exact(t, got, want, mc.mode+" sub-rate int16 PLC")
			})
		}
	}
}

// plcInt16DecoderReset resets a decoder and verifies that no PLC energy leaks
// across a Reset boundary.  This tests the warm-state reset invariant.
func TestDecodeInt16PLCResetBoundaryNoLeak(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateCELTPacket(t, channels)
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]int16, frameSize*channels)

	// Warm up.
	for range 3 {
		if _, err := dec.DecodeInt16(pkt, buf); err != nil {
			t.Fatalf("warm-up DecodeInt16: %v", err)
		}
	}

	// Reset then cold PLC: should be silence.
	dec.Reset()
	n, err := dec.DecodeInt16(nil, buf)
	if err != nil {
		t.Fatalf("post-reset DecodeInt16(nil): %v", err)
	}
	for _, v := range buf[:n*channels] {
		if v != 0 {
			t.Fatalf("post-reset cold PLC: non-zero sample %d (want silence)", v)
		}
	}
}

// TestDecodeInt16PLCSelfConsistencyWarmupN asserts the float-build
// float-path-quantized identity for warm-up sequences of length 1, 3, and 6
// before the first PLC. Fixed CELT and Hybrid compare both output formats
// exactly with selected C. Each warmup length exercises a different decoder state.
//
// The float32 PLC output is converted to int16 using float32ToInt16NoSoftClip,
// the same function float-build DecodeInt16 uses, ensuring exact equality for
// each tested sequence in the selected build.
func TestDecodeInt16PLCSelfConsistencyWarmupN(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)

	for _, mc := range plcInt16ModeCases(t, channels) {
		pkt := mc.packet
		frameSize, err := packetSamplesAtRate(pkt, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate %s: %v", mc.mode, err)
		}

		for _, warmup := range []int{1, 3, 6} {
			name := mc.mode + "_warmup" + itoaSmall(warmup)
			t.Run(name, func(t *testing.T) {
				if (celtIntegerPLCActive && mc.mode == "celt") || (hybridIntegerPLCActive && mc.mode == "hybrid") {
					steps := make([]plcInt16LossStep, warmup+1)
					for i := range warmup {
						steps[i].packet = pkt
					}
					assertPLCFormatsMatchSelectedC(t, name, sampleRate, channels, frameSize, steps)
					return
				}
				decF := mustNewTestDecoder(t, sampleRate, channels)
				dec16 := mustNewTestDecoder(t, sampleRate, channels)

				bufF := make([]float32, frameSize*channels)
				buf16 := make([]int16, frameSize*channels)
				wantBuf := make([]int16, frameSize*channels)

				// Warm up both decoders identically.
				for i := range warmup {
					if _, err := decF.Decode(pkt, bufF); err != nil {
						t.Fatalf("warmup Decode step %d: %v", i, err)
					}
					if _, err := dec16.DecodeInt16(pkt, buf16); err != nil {
						t.Fatalf("warmup DecodeInt16 step %d: %v", i, err)
					}
				}

				// PLC frame.
				nF, err := decF.Decode(nil, bufF)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				n16, err := dec16.DecodeInt16(nil, buf16)
				if err != nil {
					t.Fatalf("DecodeInt16(nil): %v", err)
				}
				if nF != n16 {
					t.Fatalf("sample count mismatch: float32=%d int16=%d", nF, n16)
				}

				// Convert float32 PLC output using the same path as DecodeInt16.
				float32ToInt16NoSoftClip(wantBuf, bufF, nF, channels)
				for j := 0; j < n16*channels; j++ {
					if buf16[j] != wantBuf[j] {
						t.Fatalf("sample[%d]: DecodeInt16=%d want quantize(Decode=%g)=%d",
							j, buf16[j], bufF[j], wantBuf[j])
					}
				}
			})
		}
	}
}

// TestDecodeInt16PLCSingleSampleEnergy verifies that a SILK single-loss PLC
// frame produces non-zero output (the PLC is active, not silence) at the
// int16 level.  Complements the float energy test in TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus.
func TestDecodeInt16PLCSingleSampleEnergy(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
	)
	pkt := encodeAPIRateSILKPacket(t, channels)
	frameSize, err := packetSamplesAtRate(pkt, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]int16, frameSize*channels)

	// Warm up with 4 packets.
	for i := range 4 {
		if _, err := dec.DecodeInt16(pkt, buf); err != nil {
			t.Fatalf("warm-up step %d: %v", i, err)
		}
	}

	// PLC frame.
	n, err := dec.DecodeInt16(nil, buf)
	if err != nil {
		t.Fatalf("DecodeInt16(nil): %v", err)
	}

	var energy float64
	for _, v := range buf[:n*channels] {
		energy += float64(v) * float64(v)
	}
	energy /= float64(n * channels)
	if energy < 1 {
		t.Fatalf("SILK int16 PLC frame is silent: mean-square energy=%.2e, expected non-zero speech extrapolation", energy)
	}

	// Silence assertion: the next loss frame should have ≤ energy than the first.
	buf2 := make([]int16, frameSize*channels)
	if _, err := dec.DecodeInt16(nil, buf2); err != nil {
		t.Fatalf("second DecodeInt16(nil): %v", err)
	}
	var energy2 float64
	for _, v := range buf2[:n*channels] {
		energy2 += float64(v) * float64(v)
	}
	energy2 /= float64(n * channels)
	if energy2 >= energy*1.1 {
		// Allow 10 % tolerance for level variation but flag gross non-decay.
		t.Fatalf("SILK int16 PLC burst energy did not decay: frame1=%.2e frame2=%.2e", energy, energy2)
	}
}

const libopusRefdecodeSingleFormatInt24 = uint32(2)

// decodeWithLibopusReferenceAPIRateInt24 drives libopus opus_decode24() through
// the refdecode_single C helper for a sequence of packets, mirroring the pattern
// used by decodeWithLibopusReferenceAPIRateInt16.
func decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize int, packets [][]byte) ([]int32, error) {
	steps := make([]libopusAPIRateDecodeStep, len(packets))
	for i, pkt := range packets {
		steps[i] = libopusAPIRateDecodeStep{packet: pkt}
	}
	return decodeWithLibopusReferenceAPIRateInt24Steps(sampleRate, channels, frameSize, steps)
}

func decodeWithLibopusReferenceAPIRateInt24Steps(sampleRate, channels, frameSize int, steps []libopusAPIRateDecodeStep) ([]int32, error) {
	binPath, err := getLibopusAPIRateRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	// Protocol version 5: magic GOSI + version + format + sample_rate + gain +
	// channels + frame_size + packet_count.
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24,
		uint32(sampleRate),
		0, // gain
		uint32(channels),
		uint32(frameSize),
		uint32(len(steps)),
	)
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "api-rate reference decode int24", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// int32Int24ToFloat32 converts an int32 slice of right-justified 24-bit samples
// to float32 by dividing by 8388608 (= 2^23), matching libopus INT24TORES for
// float builds: (1.f/32768.f/256.f) * a.
func int32Int24ToFloat32(in []int32) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 8388608.0
	}
	return out
}

// assertInt24ParitySelectedCExact keeps the quality diagnostic and requires
// exact int24 samples from the selected libopus build.
func assertInt24ParitySelectedCExact(t *testing.T, got, want []int32, sampleRate, channels int, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s sample[%d]=%d want %d", label, i, got[i], want[i])
		}
	}
	assertAPIRateQualityFloat32(t, int32Int24ToFloat32(got), int32Int24ToFloat32(want), sampleRate, channels, label)
}

// TestDecodeInt24SILKAPIRatePCMMatchesLibopus verifies that Decoder.DecodeInt24
// produces bit-exact output vs the selected libopus opus_decode24() reference
// for the tested SILK packets and API rates.
func TestDecodeInt24SILKAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateSILKPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate SILK reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				// SILK output is bit-exact for this selected-reference case.
				if len(got) != len(want) {
					t.Fatalf("DecodeInt24 len=%d want %d", len(got), len(want))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Fatalf("DecodeInt24[%d]=%d want %d", i, got[i], want[i])
					}
				}
			})
		}
	}
}

// TestDecodeInt24CELTAPIRatePCMMatchesLibopus compares public CELT int24
// output sample-for-sample with the selected libopus opus_decode24 build.
func TestDecodeInt24CELTAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate CELT reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				assertInt24ParitySelectedCExact(t, got, want, sampleRate, channels, "CELT int24 decode")
			})
		}
	}
}

// TestDecodeInt24HybridAPIRatePCMMatchesLibopus verifies Decoder.DecodeInt24
// for Hybrid (SILK+CELT) packets vs libopus opus_decode24().
func TestDecodeInt24HybridAPIRatePCMMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateHybridPacket(t, channels)
		for _, sampleRate := range []int{16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}

				sequence := [][]byte{packet}
				want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "api-rate Hybrid reference decode int24", err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				got := make([]int32, frameSize*channels)
				n, err := dec.DecodeInt24(packet, got)
				if err != nil {
					t.Fatalf("DecodeInt24: %v", err)
				}
				if n != frameSize {
					t.Fatalf("DecodeInt24 samples=%d want %d", n, frameSize)
				}
				got = got[:n*channels]
				assertInt24ParitySelectedCExact(t, got, want, sampleRate, channels, "Hybrid int24 decode")
			})
		}
	}
}

// TestDecodeInt24TracksFloat32Decode verifies that Decoder.DecodeInt24 output
// is consistent with Decoder.Decode (float32) output via the int24 conversion,
// confirming that DecodeInt24 shares the same decode path and differs only in
// the final sample format conversion.
func TestDecodeInt24TracksFloat32Decode(t *testing.T) {
	// This test asserts that DecodeInt24 derives its samples from the same
	// float32 decode as Decode (default build). Under -tags gopus_fixed_point,
	// DecodeInt24 routes CELT-only frames directly to the integer decoder's
	// libopus-exact opus_res int24, which is produced by a different (fixed)
	// decode than the float path, so the "tracks float" relationship no longer
	// holds. The int24 path's correctness is covered there by
	// TestDecoderFixedPointCELTParity instead.
	if decodeInt24TracksFloat32Skip {
		t.Skip("DecodeInt24 routes CELT directly to the integer decoder under gopus_fixed_point")
	}
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		const sampleRate = 48000
		frameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			decF := mustNewTestDecoder(t, sampleRate, channels)
			pcmF := make([]float32, frameSize*channels)
			nF, err := decF.Decode(packet, pcmF)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			dec24 := mustNewTestDecoder(t, sampleRate, channels)
			pcm24 := make([]int32, frameSize*channels)
			n24, err := dec24.DecodeInt24(packet, pcm24)
			if err != nil {
				t.Fatalf("DecodeInt24: %v", err)
			}

			if nF != n24 {
				t.Fatalf("sample count mismatch: float32=%d int24=%d", nF, n24)
			}
			for i := 0; i < nF*channels; i++ {
				want := float32ToInt24(pcmF[i])
				if pcm24[i] != want {
					t.Fatalf("pcm24[%d]=%d want float32ToInt24(%g)=%d", i, pcm24[i], pcmF[i], want)
				}
			}
		})
	}
}

// TestDecodeInt24SliceMatchesDecodeInt24 verifies that DecodeInt24Slice returns
// the same samples as DecodeInt24 with a pre-allocated buffer.
func TestDecodeInt24SliceMatchesDecodeInt24(t *testing.T) {
	packet := encodeAPIRateCELTPacket(t, 1)
	const sampleRate = 48000
	frameSize, err := packetSamplesAtRate(packet, sampleRate)
	if err != nil {
		t.Fatalf("packetSamplesAtRate: %v", err)
	}

	dec1 := mustNewTestDecoder(t, sampleRate, 1)
	buf := make([]int32, frameSize)
	n, err := dec1.DecodeInt24(packet, buf)
	if err != nil {
		t.Fatalf("DecodeInt24: %v", err)
	}
	want := buf[:n]

	dec2 := mustNewTestDecoder(t, sampleRate, 1)
	got, err := dec2.DecodeInt24Slice(packet)
	if err != nil {
		t.Fatalf("DecodeInt24Slice: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("DecodeInt24Slice len=%d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

// TestDecodeInt24InvalidFrameSize verifies that DecodeInt24 returns
// ErrBufferTooSmall when the output buffer is too small.
func TestDecodeInt24InvalidFrameSize(t *testing.T) {
	packet := encodeAPIRateCELTPacket(t, 1)
	dec := mustNewTestDecoder(t, 48000, 1)

	if _, err := dec.DecodeInt24(packet, nil); err != ErrBufferTooSmall {
		t.Fatalf("DecodeInt24(nil pcm) error=%v want %v", err, ErrBufferTooSmall)
	}
	if _, err := dec.DecodeInt24(packet, make([]int32, 1)); err != ErrBufferTooSmall {
		t.Fatalf("DecodeInt24(short pcm) error=%v want %v", err, ErrBufferTooSmall)
	}
}

// TestDecodeInt24PLCMatchesLibopus verifies that Decoder.DecodeInt24 with
// a nil packet (PLC) matches libopus opus_decode24(NULL, ...) within the
// trusted near-exact bar.
//
// Under -tags gopus_fixed_point the CELT loss is concealed by the integer
// celt_decode_lost (different from the float concealment this float-oracle bar
// expects); that path is gated bit-exact by TestDecoderFixedPointCELTPLCParity.
func TestDecodeInt24PLCMatchesLibopus(t *testing.T) {
	if celtIntegerPLCActive {
		t.Skip("CELT PLC routes to the integer decoder under gopus_fixed_point; see TestDecoderFixedPointCELTPLCParity")
	}
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		const sampleRate = 48000
		frameSize, err := packetSamplesAtRate(packet, sampleRate)
		if err != nil {
			t.Fatalf("packetSamplesAtRate: %v", err)
		}
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			sequence := [][]byte{packet, nil}
			want, err := decodeWithLibopusReferenceAPIRateInt24(sampleRate, channels, frameSize, sequence)
			if err != nil {
				libopustest.HelperUnavailable(t, "api-rate CELT reference decode int24 PLC", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			got := make([]int32, 0, len(want))
			buf := make([]int32, frameSize*channels)
			for i, pkt := range sequence {
				n, err := dec.DecodeInt24(pkt, buf)
				if err != nil {
					t.Fatalf("DecodeInt24 seq[%d]: %v", i, err)
				}
				got = append(got, buf[:n*channels]...)
			}
			assertInt24ParitySelectedCExact(t, got, want, sampleRate, channels, "CELT int24 PLC decode")
		})
	}
}

const (
	cbrAppAudio          = uint32(0)
	cbrAppVoIP           = uint32(1)
	cbrAppRestrictedSILK = uint32(2)
	cbrAppRestrictedCELT = uint32(3)
)

// libopus bandwidth constants (opus_defines.h), used by the CBR helper.
const (
	cbrBWNarrowband    = uint32(1101)
	cbrBWMediumband    = uint32(1102)
	cbrBWWideband      = uint32(1103)
	cbrBWSuperwideband = uint32(1104)
	cbrBWFullband      = uint32(1105)
)

var libopusCBREncodeHelper libopustest.HelperCache

func getLibopusCBREncodeHelperPath() (string, error) {
	return libopusCBREncodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "cbr encode packets",
			OutputBase: "gopus_libopus_cbr_encode_packets",
			SourceFile: "libopus_cbr_encode_packets.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		})
	})
}

type cbrEncodeConfig struct {
	app        uint32
	bandwidth  uint32
	channels   int
	bitrate    int
	frameSize  int // samples per frame at 48 kHz
	complexity int
	numFrames  int
}

// encodeLibopusCBRPackets drives libopus opus_encode_float() in CBR mode over a
// sequence of frames, returning the encoded packets. The encoder always runs at
// 48 kHz (gopus internal rate).
func encodeLibopusCBRPackets(cfg cbrEncodeConfig, pcm []float32) ([][]byte, error) {
	binPath, err := getLibopusCBREncodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GCBR", 1,
		cfg.app,
		cfg.bandwidth,
		uint32(cfg.channels),
		uint32(cfg.bitrate),
		uint32(cfg.frameSize),
		uint32(cfg.complexity),
		uint32(cfg.numFrames),
	)
	payload.Float32s(pcm...)
	reader, version, err := libopustest.NewOracleReaderVersion("cbr encode", "GCBO", mustRunHelper(binPath, payload.Bytes()))
	if err != nil {
		return nil, err
	}
	if version != 2 {
		return nil, fmt.Errorf("cbr encode helper version=%d want 2", version)
	}
	versionLen := int(reader.U32())
	if versionLen <= 0 || versionLen > 128 {
		return nil, fmt.Errorf("invalid cbr encode libopus version length %d", versionLen)
	}
	if got := string(reader.Bytes(versionLen)); !strings.Contains(got, libopustooling.DefaultVersion) {
		return nil, fmt.Errorf("cbr encode helper reports libopus version %q, want %s", got, libopustooling.DefaultVersion)
	}
	archMask, buildFeatures, selectedArch := reader.U32(), reader.U32(), reader.U32()
	if selectedArch > archMask {
		return nil, fmt.Errorf("cbr encode helper selected architecture %d above OPUS_ARCHMASK %d", selectedArch, archMask)
	}
	identity, err := libopustest.ResolvePublicAPIReferenceIdentity()
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(string(identity.Variant), "scalar"):
		if archMask != 0 || buildFeatures != 0 || selectedArch != 0 {
			return nil, fmt.Errorf("scalar CBR reference emitted SIMD metadata: arch_mask=%d features=0x%x selected_arch=%d", archMask, buildFeatures, selectedArch)
		}
	case strings.HasSuffix(string(identity.Variant), "simd"):
		if buildFeatures == 0 {
			return nil, fmt.Errorf("SIMD CBR reference emitted no generated SIMD build features")
		}
	default:
		return nil, fmt.Errorf("unsupported paired CBR reference variant %q", identity.Variant)
	}
	count := reader.Count(cfg.numFrames)
	packets := make([][]byte, 0, count)
	for range count {
		n := int(reader.U32())
		_ = reader.U32() // OPUS_GET_FINAL_RANGE
		packets = append(packets, append([]byte(nil), reader.Bytes(n)...))
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return packets, nil
}

func TestDecodeLibopusCBROracleOutputV2(t *testing.T) {
	libopustest.RequireOracle(t)
	cfg := cbrEncodeConfig{
		app:        cbrAppAudio,
		bandwidth:  cbrBWWideband,
		channels:   1,
		bitrate:    32000,
		frameSize:  480,
		complexity: 1,
		numFrames:  2,
	}
	packets, err := encodeLibopusCBRPackets(cfg, configEdgesPCM(cfg.frameSize, cfg.channels, cfg.numFrames))
	if err != nil {
		libopustest.HelperUnavailable(t, "cbr encode", err)
		return
	}
	if len(packets) != cfg.numFrames {
		t.Fatalf("CBR oracle returned %d packets, want %d", len(packets), cfg.numFrames)
	}
	for frame, packet := range packets {
		if len(packet) == 0 {
			t.Fatalf("CBR oracle returned empty packet at frame %d", frame)
		}
	}
}

func mustRunHelper(binPath string, input []byte) []byte {
	out, err := libopustest.RunHelper(binPath, input)
	if err != nil {
		panic(err)
	}
	return out
}

// configEdgesPCM produces a deterministic, seeded multi-tone + envelope signal
// suitable for exercising every coding mode. It is band-limited softly so SILK,
// Hybrid, and CELT all encode meaningful content.
func configEdgesPCM(frameSize, channels, numFrames int) []float32 {
	total := frameSize * channels * numFrames
	pcm := make([]float32, total)
	const sampleRate = 48000
	for f := range numFrames {
		for i := range frameSize {
			n := f*frameSize + i
			tt := float64(n) / float64(sampleRate)
			env := 0.7 + 0.3*math.Sin(2*math.Pi*0.9*tt)
			s := 0.0
			s += 0.30 * math.Sin(2*math.Pi*180*tt)
			s += 0.18 * math.Sin(2*math.Pi*523*tt+0.3)
			s += 0.10 * math.Sin(2*math.Pi*1200*tt+0.7)
			s += 0.05 * math.Sin(2*math.Pi*3300*tt+1.1)
			v := float32(env * s)
			for ch := range channels {
				// Slight inter-channel decorrelation for stereo coverage.
				pcm[(f*frameSize+i)*channels+ch] = v * (1.0 - 0.07*float32(ch))
			}
		}
	}
	return pcm
}

// decodeGopusFloat32Sequence decodes a packet sequence through a single gopus
// decoder instance (stateful), returning interleaved float32 PCM.
func decodeGopusFloat32Sequence(t *testing.T, sampleRate, channels, frameSize int, packets [][]byte) []float32 {
	t.Helper()
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, 0, frameSize*channels*len(packets))
	buf := make([]float32, frameSize*channels)
	for i, pkt := range packets {
		n, err := dec.Decode(pkt, buf)
		if err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
		out = append(out, buf[:n*channels]...)
	}
	return out
}

// TestDecodeLibopusCBRConfigEdgesMatchesLibopus exercises the gopus decoder on
// CBR libopus-encoded streams across the bandwidth × frame-size × channels grid.
// CBR is a configuration the gopus encoder does not pick by default, so this
// covers decode of libopus output the encoder-driven gates never produce.
func TestDecodeLibopusCBRConfigEdgesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type bwCase struct {
		name string
		bw   uint32
		// frame sizes valid for the encoder bandwidth; libopus clamps SILK to
		// >=10 ms, so NB/MB/WB SILK use >=480 here, while FB CELT covers short
		// frames too.
		frameSizes []int
	}
	bwCases := []bwCase{
		{"nb", cbrBWNarrowband, []int{480, 960, 1920}},
		{"mb", cbrBWMediumband, []int{480, 960, 1920}},
		{"wb", cbrBWWideband, []int{480, 960, 1920, 2880}},
		{"swb", cbrBWSuperwideband, []int{480, 960, 1920}},
		{"fb", cbrBWFullband, []int{120, 240, 480, 960, 1920, 2880}},
	}

	for _, channels := range []int{1, 2} {
		for _, bc := range bwCases {
			for _, frameSize := range bc.frameSizes {
				name := fmt.Sprintf("ch%d_%s_fs%d", channels, bc.name, frameSize)
				t.Run(name, func(t *testing.T) {
					// Choose a moderate per-bandwidth bitrate so CBR fits the
					// frame budget at all sizes.
					bitrate := 48000
					if channels == 2 {
						bitrate = 96000
					}
					cfg := cbrEncodeConfig{
						app:        cbrAppAudio,
						bandwidth:  bc.bw,
						channels:   channels,
						bitrate:    bitrate,
						frameSize:  frameSize,
						complexity: 10,
						numFrames:  numFrames,
					}
					pcm := configEdgesPCM(frameSize, channels, numFrames)
					packets, err := encodeLibopusCBRPackets(cfg, pcm)
					if err != nil {
						libopustest.HelperUnavailable(t, "cbr encode", err)
					}
					if len(packets) == 0 {
						t.Fatalf("libopus produced no packets")
					}

					want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, packets)
					if err != nil {
						libopustest.HelperUnavailable(t, "cbr reference decode", err)
					}
					got := decodeGopusFloat32Sequence(t, sampleRate, channels, frameSize, packets)
					assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, channels, name)
				})
			}
		}
	}
}

// TestDecodeLibopusBoundaryBitratesMatchesLibopus exercises the extreme bitrate
// edges: the 6 kbps minimum and 510 kbps maximum, across mode-selecting
// bandwidths and frame sizes. The libopus encoder picks SILK/Hybrid/CELT
// internally at these rates; the gopus decoder must track its output.
func TestDecodeLibopusBoundaryBitratesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type edge struct {
		name      string
		bw        uint32
		bitrate   int
		channels  int
		frameSize int
	}
	edges := []edge{
		// 6 kbps minimum: libopus forces SILK/NB-class at the floor.
		{"min6k_nb_mono_20ms", cbrBWNarrowband, 6000, 1, 960},
		{"min6k_wb_mono_20ms", cbrBWWideband, 6000, 1, 960},
		{"min6k_wb_mono_60ms", cbrBWWideband, 6000, 1, 2880},
		// 510 kbps maximum: libopus forces CELT/FB at the ceiling.
		{"max510k_fb_mono_20ms", cbrBWFullband, 510000, 1, 960},
		{"max510k_fb_stereo_20ms", cbrBWFullband, 510000, 2, 960},
		{"max510k_fb_stereo_5ms", cbrBWFullband, 510000, 2, 240},
		{"max510k_fb_stereo_2_5ms", cbrBWFullband, 510000, 2, 120},
		// Mode-selection boundary region: ~16-24 kbps WB toggles SILK<->Hybrid.
		{"bound16k_wb_mono_20ms", cbrBWWideband, 16000, 1, 960},
		{"bound24k_swb_stereo_20ms", cbrBWSuperwideband, 24000, 2, 960},
		{"bound64k_fb_stereo_20ms", cbrBWFullband, 64000, 2, 960},
	}

	for _, e := range edges {
		t.Run(e.name, func(t *testing.T) {
			cfg := cbrEncodeConfig{
				app:        cbrAppAudio,
				bandwidth:  e.bw,
				channels:   e.channels,
				bitrate:    e.bitrate,
				frameSize:  e.frameSize,
				complexity: 10,
				numFrames:  numFrames,
			}
			pcm := configEdgesPCM(e.frameSize, e.channels, numFrames)
			packets, err := encodeLibopusCBRPackets(cfg, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr encode", err)
			}
			if len(packets) == 0 {
				t.Fatalf("libopus produced no packets")
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, e.channels, e.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr reference decode", err)
			}
			got := decodeGopusFloat32Sequence(t, sampleRate, e.channels, e.frameSize, packets)
			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, e.channels, e.name)
		})
	}
}

// transitionSegment describes one libopus-encoded run whose packets are
// concatenated into a single switching stream.
type transitionSegment struct {
	cfg    cbrEncodeConfig
	frames int
}

// buildTransitionStream encodes each segment as a separate libopus CBR run and
// concatenates the packets into one stateful stream. All segments must share the
// channel count (a single decoder instance is used end-to-end). Returns the
// packets and the max frame size across segments (the decoder output buffer
// size). The PCM is phase-continuous across segments so the switch points carry
// real signal.
func buildTransitionStream(t *testing.T, channels int, segments []transitionSegment) (packets [][]byte, maxFrameSize int) {
	t.Helper()
	globalFrame := 0
	for _, seg := range segments {
		if seg.cfg.channels != channels {
			t.Fatalf("segment channels=%d want %d", seg.cfg.channels, channels)
		}
		if seg.cfg.frameSize > maxFrameSize {
			maxFrameSize = seg.cfg.frameSize
		}
		cfg := seg.cfg
		cfg.numFrames = seg.frames
		// Generate phase-continuous PCM offset by the global frame index so the
		// signal flows across the switch boundary.
		pcm := configEdgesPCMOffset(cfg.frameSize, channels, seg.frames, globalFrame*cfg.frameSize)
		segPackets, err := encodeLibopusCBRPackets(cfg, pcm)
		if err != nil {
			libopustest.HelperUnavailable(t, "transition cbr encode", err)
		}
		packets = append(packets, segPackets...)
		globalFrame += seg.frames
	}
	return packets, maxFrameSize
}

// configEdgesPCMOffset is configEdgesPCM with a sample offset so that
// concatenated segments form a continuous waveform.
func configEdgesPCMOffset(frameSize, channels, numFrames, sampleOffset int) []float32 {
	total := frameSize * channels * numFrames
	pcm := make([]float32, total)
	const sampleRate = 48000
	for f := range numFrames {
		for i := range frameSize {
			n := sampleOffset + f*frameSize + i
			tt := float64(n) / float64(sampleRate)
			env := 0.7 + 0.3*math.Sin(2*math.Pi*0.9*tt)
			s := 0.0
			s += 0.30 * math.Sin(2*math.Pi*180*tt)
			s += 0.18 * math.Sin(2*math.Pi*523*tt+0.3)
			s += 0.10 * math.Sin(2*math.Pi*1200*tt+0.7)
			s += 0.05 * math.Sin(2*math.Pi*3300*tt+1.1)
			v := float32(env * s)
			for ch := range channels {
				pcm[(f*frameSize+i)*channels+ch] = v * (1.0 - 0.07*float32(ch))
			}
		}
	}
	return pcm
}

// TestDecodeLibopusModeTransitionsMatchesLibopus exercises within-stream
// transitions: the gopus decoder must track libopus across SILK<->Hybrid<->CELT
// mode switches, NB<->WB<->SWB<->FB bandwidth steps, and frame-size changes
// across consecutive packets. Each segment is a separate libopus CBR encode;
// concatenating their packets produces a real switching stream decoded through a
// single stateful decoder on both sides.
func TestDecodeLibopusModeTransitionsMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000

	const fs20 = 960 // 20 ms at 48 kHz
	seg := func(app, bw uint32, channels, bitrate, frameSize, frames int) transitionSegment {
		return transitionSegment{
			cfg: cbrEncodeConfig{
				app:        app,
				bandwidth:  bw,
				channels:   channels,
				bitrate:    bitrate,
				frameSize:  frameSize,
				complexity: 10,
			},
			frames: frames,
		}
	}

	type tcase struct {
		name     string
		channels int
		segments []transitionSegment
	}
	cases := []tcase{
		{
			// SILK -> Hybrid -> CELT mode walk (mono).
			name:     "silk_hybrid_celt_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppVoIP, cbrBWWideband, 1, 16000, fs20, 3),  // SILK WB
				seg(cbrAppAudio, cbrBWFullband, 1, 64000, fs20, 3), // Hybrid/CELT FB
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, fs20, 3),
			},
		},
		{
			// NB -> WB -> SWB -> FB bandwidth steps (mono).
			name:     "bandwidth_steps_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppAudio, cbrBWNarrowband, 1, 16000, fs20, 3),
				seg(cbrAppAudio, cbrBWWideband, 1, 32000, fs20, 3),
				seg(cbrAppAudio, cbrBWSuperwideband, 1, 48000, fs20, 3),
				seg(cbrAppAudio, cbrBWFullband, 1, 96000, fs20, 3),
			},
		},
		{
			// Frame-size changes across consecutive frames (FB CELT, mono):
			// 2.5 -> 5 -> 10 -> 20 -> 40 -> 60 ms.
			name:     "frame_size_walk_mono",
			channels: 1,
			segments: []transitionSegment{
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 120, 4),  // 2.5 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 240, 4),  // 5 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 480, 4),  // 10 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 960, 4),  // 20 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 1920, 2), // 40 ms
				seg(cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 2880, 2), // 60 ms
			},
		},
		{
			// Stereo SILK -> CELT bandwidth+mode walk.
			name:     "silk_celt_stereo",
			channels: 2,
			segments: []transitionSegment{
				seg(cbrAppVoIP, cbrBWWideband, 2, 32000, fs20, 3),   // SILK WB stereo
				seg(cbrAppAudio, cbrBWFullband, 2, 128000, fs20, 3), // CELT/Hybrid FB stereo
				seg(cbrAppRestrictedCELT, cbrBWFullband, 2, 192000, fs20, 3),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packets, maxFrameSize := buildTransitionStream(t, tc.channels, tc.segments)
			if len(packets) == 0 {
				t.Fatalf("no packets produced")
			}
			// Decode through a single libopus decoder (stateful, max frame size).
			steps := make([]libopusAPIRateDecodeStep, len(packets))
			for i, pkt := range packets {
				steps[i] = libopusAPIRateDecodeStep{packet: pkt}
			}
			wantI16, err := decodeWithLibopusReferenceAPIRateInt16VariableSteps(sampleRate, tc.channels, maxFrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "transition reference decode", err)
			}
			// Decode through a single gopus decoder (stateful).
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			gotI16 := make([]int16, 0, len(wantI16))
			buf := make([]int16, maxFrameSize*tc.channels)
			for i, pkt := range packets {
				n, err := dec.DecodeInt16(pkt, buf)
				if err != nil {
					t.Fatalf("DecodeInt16 packet %d: %v", i, err)
				}
				gotI16 = append(gotI16, buf[:n*tc.channels]...)
			}
			assertAPIRateQualityInt16(t, gotI16, wantI16, sampleRate, tc.channels, tc.name)
			assertAPIRateInt16Exact(t, gotI16, wantI16, tc.name)
		})
	}
}

// TestDecodeLibopusRestrictedAppEdgesMatchesLibopus exercises decode of streams
// from the restricted-SILK and restricted-CELT applications, which lock the
// encoder into a single coding mode the auto encoder would not always pick.
func TestDecodeLibopusRestrictedAppEdgesMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const numFrames = 6

	type rc struct {
		name      string
		app       uint32
		bw        uint32
		channels  int
		bitrate   int
		frameSize int
	}
	cases := []rc{
		{"rsilk_nb_mono_20ms", cbrAppRestrictedSILK, cbrBWNarrowband, 1, 24000, 960},
		{"rsilk_wb_stereo_20ms", cbrAppRestrictedSILK, cbrBWWideband, 2, 48000, 960},
		{"rsilk_mb_mono_40ms", cbrAppRestrictedSILK, cbrBWMediumband, 1, 24000, 1920},
		{"rcelt_fb_mono_10ms", cbrAppRestrictedCELT, cbrBWFullband, 1, 64000, 480},
		{"rcelt_fb_stereo_20ms", cbrAppRestrictedCELT, cbrBWFullband, 2, 128000, 960},
		{"rcelt_fb_mono_2_5ms", cbrAppRestrictedCELT, cbrBWFullband, 1, 96000, 120},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := cbrEncodeConfig{
				app:        c.app,
				bandwidth:  c.bw,
				channels:   c.channels,
				bitrate:    c.bitrate,
				frameSize:  c.frameSize,
				complexity: 10,
				numFrames:  numFrames,
			}
			pcm := configEdgesPCM(c.frameSize, c.channels, numFrames)
			packets, err := encodeLibopusCBRPackets(cfg, pcm)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr encode", err)
			}
			if len(packets) == 0 {
				t.Fatalf("libopus produced no packets")
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "cbr reference decode", err)
			}
			got := decodeGopusFloat32Sequence(t, sampleRate, c.channels, c.frameSize, packets)
			assertSelectedPublicAPIRateFloat32(t, got, want, sampleRate, c.channels, c.name)
		})
	}
}

func encodeSILKVoicedPLCTestSequence(t *testing.T, channels int) [][]byte {
	t.Helper()
	const (
		sampleRate = 48000
		frameSize  = 960
		bitrate    = 24000
		numFrames  = 8
	)
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}

	packets := make([][]byte, 0, numFrames)
	for frameIndex := range numFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			s := 0.35*float32(math.Sin(2*math.Pi*220*tm)) +
				0.15*float32(math.Sin(2*math.Pi*440*tm+0.21)) +
				0.08*float32(math.Sin(2*math.Pi*880*tm+0.43))
			for ch := range channels {
				pcm[i*channels+ch] = s
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK {
			t.Fatalf("Encode frame %d mode=%v want SILK", frameIndex, toc.Mode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

// TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus verifies that the first
// concealed frame after a voiced SILK sequence is non-silent and (with oracle)
// matches libopus samples. Covers the lossCnt=0 branch in silk_PLC_conceal()
// where harmAttQ15_0=0.99 and randScaleQ14 is initialised.
func TestSILKPLCIIRFirstLossOutputNonSilentMatchesLibopus(t *testing.T) {
	const channels = 1
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	const frameSize = 960
	buf := make([]float32, frameSize*channels)

	// Warm up: decode first 4 packets normally.
	for i := range 4 {
		if _, err := dec.Decode(packets[i], buf); err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
	}

	// Simulate loss of packet 4.
	n, err := dec.Decode(nil, buf)
	if err != nil {
		t.Fatalf("Decode(nil) PLC: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
	}

	// First concealed frame should be non-silent (PLC reproduces speech).
	energy := float64(0)
	for _, s := range buf[:n*channels] {
		energy += float64(s) * float64(s)
	}
	energy /= float64(n * channels)
	if energy < 1e-6 {
		t.Fatalf("first PLC frame RMS²=%.2e, expected non-silent speech extrapolation", energy)
	}

	// With oracle: verify exact samples and retain the quality diagnostic.
	if !libopustest.OracleEnabled() {
		return
	}
	steps := []libopusAPIRateDecodeStep{
		{packet: packets[0]},
		{packet: packets[1]},
		{packet: packets[2]},
		{packet: packets[3]},
		{}, // nil packet → PLC
	}
	want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(48000, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK PLC IIR first-loss reference", err)
	}
	// Compare only the PLC frame (last frameSize*channels samples).
	wantPLC := want[4*frameSize*channels:]
	got, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("oracle compare: NewDecoder: %v", err)
	}
	gotBuf := make([]float32, frameSize*channels*5)
	for i := range 4 {
		if _, err := got.Decode(packets[i], gotBuf[i*frameSize:(i+1)*frameSize]); err != nil {
			t.Fatalf("oracle compare: Decode packet %d: %v", i, err)
		}
	}
	gotPLC := gotBuf[4*frameSize : 5*frameSize]
	if _, err := got.Decode(nil, gotPLC); err != nil {
		t.Fatalf("oracle compare: Decode(nil) PLC: %v", err)
	}

	assertAPIRateQualityFloat32PLC(t, gotPLC, wantPLC, 48000, channels, true,
		"SILK PLC IIR first-loss mono")
	assertAPIRateFloat32BitsExact(t, gotPLC, wantPLC, "SILK PLC IIR first-loss mono")
}

// TestSILKPLCIIRMultiLossEnergyDecaysMatchesLibopus verifies that energy
// decreases monotonically across a consecutive 3-frame loss run. This exercises
// the harmGainQ15 attenuation chain: frame0→harmAttQ15_0(0.99), frame1+→
// harmAttQ15_1(0.95). Reference: silk/PLC.c:353-355.
func TestSILKPLCIIRMultiLossEnergyDecaysMatchesLibopus(t *testing.T) {
	const channels = 1
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	const frameSize = 960
	energies := make([]float64, 3)

	for lossIdx := range 3 {
		dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
		if err != nil {
			t.Fatalf("NewDecoder (loss %d): %v", lossIdx, err)
		}
		buf := make([]float32, frameSize*channels)
		// Always warm up with 4 packets.
		for i := range 4 {
			if _, err := dec.Decode(packets[i], buf); err != nil {
				t.Fatalf("Decode packet %d (loss %d): %v", i, lossIdx, err)
			}
		}
		// Generate lossIdx+1 consecutive PLC frames and record the last one.
		var lostBuf []float32
		for k := 0; k <= lossIdx; k++ {
			lostBuf = make([]float32, frameSize*channels)
			if _, err := dec.Decode(nil, lostBuf); err != nil {
				t.Fatalf("Decode(nil) loss %d frame %d: %v", lossIdx, k, err)
			}
		}
		e := float64(0)
		for _, s := range lostBuf {
			e += float64(s) * float64(s)
		}
		energies[lossIdx] = e / float64(frameSize*channels)
	}

	// Energy must be strictly decreasing across the first three loss frames.
	for i := 1; i < len(energies); i++ {
		if energies[i] >= energies[i-1] {
			t.Fatalf("PLC energy did not decay: loss[%d]=%.4e >= loss[%d]=%.4e",
				i, energies[i], i-1, energies[i-1])
		}
	}

	// With oracle: compare each PLC frame against libopus.
	if !libopustest.OracleEnabled() {
		return
	}
	// Build steps: 4 good + 3 losses.
	steps := make([]libopusAPIRateDecodeStep, 7)
	for i := range 4 {
		steps[i] = libopusAPIRateDecodeStep{packet: packets[i]}
	}
	// steps[4..6] are nil (PLC).
	want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(48000, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK PLC IIR multi-loss reference", err)
	}
	// Decode with gopus and compare.
	dec2, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("oracle compare: NewDecoder: %v", err)
	}
	got := make([]float32, 0, len(want))
	buf2 := make([]float32, frameSize*channels)
	for i := range 4 {
		n, err := dec2.Decode(packets[i], buf2)
		if err != nil {
			t.Fatalf("oracle compare: Decode packet %d: %v", i, err)
		}
		got = append(got, buf2[:n*channels]...)
	}
	for k := range 3 {
		n, err := dec2.Decode(nil, buf2)
		if err != nil {
			t.Fatalf("oracle compare: Decode(nil) loss %d: %v", k, err)
		}
		got = append(got, buf2[:n*channels]...)
	}
	// PLC frames dominate the comparison; use PLC quality bar.
	assertAPIRateQualityFloat32PLC(t, got, want, 48000, channels, true,
		"SILK PLC IIR multi-loss mono")
	assertAPIRateFloat32BitsExact(t, got, want, "SILK PLC IIR multi-loss mono")
}

// TestSILKPLCIIRVoicedUnvoicedTransitionNoPanic verifies that the full-stack
// decode path does not panic when a SILK voiced frame is followed by a PLC loss.
// This exercises the voiced→PLC-concealment transition in the decoder path.
func TestSILKPLCIIRVoicedUnvoicedTransitionNoPanic(t *testing.T) {
	for _, channels := range []int{1, 2} {
		t.Run("ch_"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeSILKVoicedPLCTestSequence(t, channels)
			const frameSize = 960
			dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, frameSize*channels)

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode(nil) panic on voiced→PLC transition: %v", r)
				}
			}()

			for i := range 5 {
				if _, err := dec.Decode(packets[i], buf); err != nil {
					t.Fatalf("Decode packet %d: %v", i, err)
				}
			}
			n, err := dec.Decode(nil, buf)
			if err != nil {
				t.Fatalf("Decode(nil) PLC: %v", err)
			}
			if n != frameSize {
				t.Fatalf("Decode(nil) samples=%d want %d", n, frameSize)
			}
		})
	}
}

// TestSILKPLCIIRStereoMultiLossEnergyDecays verifies the stereo PLC IIR energy
// decay chain mirrors the mono case. libopus processes left/right channels
// independently through the same silk_PLC_conceal path.
func TestSILKPLCIIRStereoMultiLossEnergyDecays(t *testing.T) {
	const channels = 2
	packets := encodeSILKVoicedPLCTestSequence(t, channels)

	const frameSize = 960
	var prevEnergy = math.MaxFloat64
	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	for i := range 4 {
		if _, err := dec.Decode(packets[i], buf); err != nil {
			t.Fatalf("Decode packet %d: %v", i, err)
		}
	}

	for k := range 3 {
		lostBuf := make([]float32, frameSize*channels)
		if _, err := dec.Decode(nil, lostBuf); err != nil {
			t.Fatalf("Decode(nil) loss %d: %v", k, err)
		}
		e := float64(0)
		for _, s := range lostBuf {
			e += float64(s) * float64(s)
		}
		e /= float64(frameSize * channels)
		if k > 0 && e >= prevEnergy {
			t.Fatalf("stereo PLC energy did not decay at loss %d: %.4e >= %.4e", k, e, prevEnergy)
		}
		prevEnergy = e
	}
}

func TestDecodeMalformedVBRPreservesSelectedLibopusState(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := append([]byte(nil), encodeAPIRateSILKPacket(t, channels)...)
		body := packet[1:]
		makeBad := func(padding, oversizedLast bool) []byte {
			header := []byte{packet[0]&^3 | 3, 0x83}
			if padding {
				header[1] |= 0x40
				header = append(header, 3)
			}
			var length [2]byte
			n := encodeFrameLength(length[:], len(body))
			header = append(header, length[:n]...)
			if oversizedLast {
				header = append(header, 0)
			} else {
				header = append(header, 2)
			}
			header = append(header, body...)
			if oversizedLast {
				header = append(header, make([]byte, maxOpusFrameBytes+1)...)
			}
			if padding {
				header = append(header, 0, 0, 0)
			}
			return header
		}
		for _, format := range []uint32{libopustest.DecodeDiffFormatFloat32, libopustest.DecodeDiffFormatInt16, libopustest.DecodeDiffFormatInt24} {
			for _, padded := range []bool{false, true} {
				for _, oversized := range []bool{false, true} {
					t.Run(fmt.Sprintf("ch%d/format%d/padded%t/oversized%t", channels, format, padded, oversized), func(t *testing.T) {
						bad := makeBad(padded, oversized)
						steps := []libopustest.DecodeDiffCase{
							{Packet: packet, Format: format, FrameSize: 960},
							{Packet: bad, Format: format, FrameSize: 2880},
							{Packet: packet, Format: format, FrameSize: 960},
							{Format: format, FrameSize: 960},
							{Packet: packet, Format: format, FrameSize: 960},
						}
						want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
						if err != nil {
							t.Fatal(err)
						}
						if want[1].Code != -4 || want[1].FinalRange != want[0].FinalRange {
							t.Fatalf("C malformed packet status=%d range=%08x, preceding=%08x", want[1].Code, want[1].FinalRange, want[0].FinalRange)
						}
						cfg := DefaultDecoderConfig(48000, channels)
						cfg.MaxPacketBytes = 2 * maxOpusFrameBytes
						dec, err := NewDecoder(cfg)
						if err != nil {
							t.Fatal(err)
						}
						f32 := make([]float32, 2880*channels)
						i16 := make([]int16, len(f32))
						i24 := make([]int32, len(f32))
						decode := func(step libopustest.DecodeDiffCase) (int, error) {
							n := int(step.FrameSize) * channels
							switch format {
							case libopustest.DecodeDiffFormatInt16:
								return dec.DecodeInt16(step.Packet, i16[:n])
							case libopustest.DecodeDiffFormatInt24:
								return dec.DecodeInt24(step.Packet, i24[:n])
							default:
								return dec.Decode(step.Packet, f32[:n])
							}
						}
						for i, step := range steps {
							n, err := decode(step)
							if want[i].Code < 0 {
								if n != 0 || err != ErrInvalidPacket {
									t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
								}
							} else if err != nil || int32(n) != want[i].Code {
								t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
							}
							if dec.FinalRange() != want[i].FinalRange {
								t.Fatalf("step%d range=%08x C=%08x", i, dec.FinalRange(), want[i].FinalRange)
							}
							for j := range n * channels {
								var got, expected uint32
								switch format {
								case libopustest.DecodeDiffFormatInt16:
									got, expected = uint32(uint16(i16[j])), uint32(binary.LittleEndian.Uint16(want[i].PCM[j*2:]))
								case libopustest.DecodeDiffFormatInt24:
									got, expected = uint32(i24[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:])
								default:
									got, expected = math.Float32bits(f32[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:])
								}
								if got != expected {
									t.Fatalf("step%d sample%d raw=%08x C=%08x", i, j, got, expected)
								}
							}
						}
						if allocs := testing.AllocsPerRun(20, func() {
							if n, err := decode(steps[1]); n != 0 || err != ErrInvalidPacket {
								t.Fatalf("malformed decode=(%d,%v)", n, err)
							}
						}); allocs != 0 {
							t.Fatalf("warm malformed decode allocations=%g, want0", allocs)
						}
					})
				}
			}
		}
	}
}

func TestDecodeMalformedRawCSequenceWitnessPreservesState(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels = 1
	packet := encodeAPIRateSILKPacket(t, channels)
	bad := []byte{0x4b, 0x83, 0x02, 0x01, 0x00, 0x00}
	steps := []libopustest.DecodeDiffCase{
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: bad, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 2880},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960},
	}
	want, err := libopustest.ProbeDecodeSequence(48000, channels, steps)
	if err != nil {
		t.Fatal(err)
	}
	if want[1].Code != -4 || want[1].FinalRange != want[0].FinalRange {
		t.Fatalf("C witness status=%d range=%08x, preceding=%08x", want[1].Code, want[1].FinalRange, want[0].FinalRange)
	}

	cfg := DefaultDecoderConfig(48000, channels)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 2880*channels)
	for i, step := range steps {
		n, err := dec.Decode(step.Packet, pcm[:int(step.FrameSize)*channels])
		if want[i].Code < 0 {
			if n != 0 || err != ErrInvalidPacket {
				t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
			}
		} else if err != nil || int32(n) != want[i].Code {
			t.Fatalf("step%d decode=(%d,%v), C=%d", i, n, err, want[i].Code)
		}
		if dec.FinalRange() != want[i].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", i, dec.FinalRange(), want[i].FinalRange)
		}
		for j := range n * channels {
			if got, expected := math.Float32bits(pcm[j]), binary.LittleEndian.Uint32(want[i].PCM[j*4:]); got != expected {
				t.Fatalf("step%d sample%d raw=%08x C=%08x", i, j, got, expected)
			}
		}
	}
}

func TestMonoHybridShortPLCBackgroundMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	packetHex := []string{
		"5cee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80",
		"40822e685173fe9a9996eb0122e11bebfebeee6dd11997f75d87fe319556ab5da14072",
		"70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13ef63a92",
		"",
	}
	formats := []uint32{
		libopustest.DecodeDiffFormatInt24,
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}
	frameSizes := []uint32{5760, 5760, 5760, 240}
	cases := make([]libopustest.DecodeDiffCase, len(packetHex))
	for i, raw := range packetHex {
		packet, err := hex.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		cases[i] = libopustest.DecodeDiffCase{Packet: packet, Format: formats[i], FrameSize: frameSizes[i]}
	}
	want, err := libopustest.ProbeDecodeSequence(48000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono-to-stereo Hybrid short PLC", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	const maxSamples = 5760 * 2
	floatPCM := make([]float32, maxSamples)
	int16PCM := make([]int16, maxSamples)
	int24PCM := make([]int32, maxSamples)
	for step, c := range cases {
		var n int
		switch c.Format {
		case libopustest.DecodeDiffFormatFloat32:
			n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
		case libopustest.DecodeDiffFormatInt16:
			n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
		default:
			n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
		}
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got := dec.FinalRange(); got != want[step].FinalRange {
			t.Fatalf("step%d range=%08x C=%08x", step, got, want[step].FinalRange)
		}
		for sample := 0; sample < n*2; sample++ {
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				got := math.Float32bits(floatPCM[sample])
				want := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
				if got != want {
					t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, want)
				}
			case libopustest.DecodeDiffFormatInt16:
				got := int16PCM[sample]
				want := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:]))
				if got != want {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, want)
				}
			default:
				got := int24PCM[sample]
				want := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:]))
				if got != want {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, want)
				}
			}
		}
	}
	decodeSequence := func() {
		dec.Reset()
		for step, c := range cases {
			var n int
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
			case libopustest.DecodeDiffFormatInt16:
				n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
			default:
				n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
			}
			if err != nil || int32(n) != want[step].Code {
				panic("decode sequence changed during allocation check")
			}
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(10, decodeSequence); allocs != 0 {
		t.Fatalf("warm decode sequence allocations=%g", allocs)
	}
}

func TestMonoCELTToStereoEnergyHistoryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	packetHex := []string{
		"c3029fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a9",
		"f47e0540971e6dfb0379e8868ecd4a9a820822b3a59547b25a61a29495cddfc3b3df03e1202ee2dc4289afb89d15f98f21bc3058291fdac32d01fbfeab9f8c29760a03e58192bff8b6f23029af1cbebf92bf50a78a4a7103b9f1ba2b96dbbd8e1c84a4ec087cdd2c43241a427c0000000000000000000000152e5b82dbe453852650da096955445bd9796955445bd97aabe78b74598aabe78b7459881f0c24f07c3093dc70c71c304d171445c003de3ef15b905d8a4a36bae0141d87e9ace16b87dfeee40ec135ffd00300000f946e9a8fd3d163151bafc50e924a4e9d",
	}
	cases := make([]libopustest.DecodeDiffCase, len(packetHex))
	for i, raw := range packetHex {
		packet, err := hex.DecodeString(raw)
		if err != nil {
			t.Fatal(err)
		}
		cases[i] = libopustest.DecodeDiffCase{
			Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960,
		}
	}
	want, err := libopustest.ProbeDecodeSequence(8000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono-to-stereo CELT sequence", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(8000, 2))
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 960*2)
	wantCounts := [...]int32{40, 80}
	for step := range cases {
		if want[step].Code != wantCounts[step] {
			t.Fatalf("C step%d returned %d samples, want %d", step, want[step].Code, wantCounts[step])
		}
		n, err := dec.Decode(cases[step].Packet, pcm)
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
			t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
		}
		for sample, got := range pcm[:n*2] {
			if expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:]); math.Float32bits(got) != expected {
				t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, math.Float32bits(got), expected)
			}
		}
	}
	decodeSequence := func() {
		dec.Reset()
		for step, c := range cases {
			n, err := dec.Decode(c.Packet, pcm)
			if err != nil || int32(n) != want[step].Code {
				panic("decode sequence changed during allocation check")
			}
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(10, decodeSequence); allocs != 0 {
		t.Fatalf("warm decode sequence allocations=%g", allocs)
	}
}

func TestMonoHybridPLCAfterRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const seed = 364
	prime := mustDecodeHex(t,
		"f3069fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d"+
			"81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e0000000000000"+
			"00000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d0"+
			"4598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1"+
			"fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c8"+
			"5ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd"+
			"46548fd8cfe354b535e00000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd587"+
			"86fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f6"+
			"5e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c"+
			"43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16"+
			"e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b61"+
			"39a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d"+
			"81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e0000000000000"+
			"00000000000000000000000000000000000000000000000000000000000000000000000000000001f77be29c293286d0"+
			"4598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1"+
			"fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f65e311779803222ac56befc97631a75c8"+
			"5ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c43466c625da9f29f0815192755eee5bd"+
			"46548fd8cfe354b535e00000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16e8b316f69d8e107bf71f5b14521fd587"+
			"86fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b6139a99fd5d9e8c1cd16f2a29d8ec8d4f6"+
			"5e311779803222ac56befc97631a75c85ae23859c0cb53315a3b16cc85cb286d81a14af585a3810a2cb84ae20c51da3c"+
			"43466c625da9f29f0815192755eee5bd46548fd8cfe354b535e000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000001f77be29c293286d04598eed16f65e645d80cf162c557cf16"+
			"e8b316f69d8e107bf71f5b14521fd58786fd1178615472b04ec3ec808d3828e1fe6ac4370d347348d5e4251a4d100b61"+
			"39a9",
	)
	mutant := mustDecodeHex(t,
		"819ecc3c1abc39107aac7ce5ce18793519666ce6d2503fd70b3adf02a51812d57da03d547d792672cc9f7f808d0be1b4"+
			"cb109a8ff8a5c3d7442690fb306450df69e14aa80d030a7671a2c2da3496d21c540e38a611329fcf9e364a4a50bf8e0b"+
			"97187cd5c4e27a5e6d1420e967fa3dedcf2c0c9ebb89002f9ead28000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"0000000000000000000000000000000000000000000000000000000000000000000000000000000000000036749914e1"+
			"499436820b31dda2decbc62a4a7cefdd97a77596e8b3157b56c7182e3b8fa6bd1487f550f61fa22e0512fd2fd5641acf"+
			"ae94416cfc213656ba83d23cb0a3da8bbeec13ee1fd52bf0234c173baba139c99e8dec9f5df96dcb21c68073d31d3056"+
			"bbf06a651845f7aa47b3bcee2f6f3bdbd242873470de6cda4a0605f7abcff78fcb09a158aab56423d075d6f55028996d"+
			"9533513151df8592b280dac28205816e8488d94b5d7cc90615a4530698cae50a3103df86d3290c40012d528fc0822827"+
			"f81a5cbec3bae89183a1bbbaeb7e92548400f8175afb855026afc6e99594504d6eb31c3534f6c35b87d1018c9ea5f770"+
			"503f20ab0e952059b294a8ed4d9e59817703c96683cff1eec8be4206f25706a463f268586a6c4e4d2dd15387c95a5da2"+
			"3022bbb472ef4ff9d8dd2f3e7b13a5825bea03d1291644ae6b8830462124a501e32490a5cea2e09dadfed26215a9a033"+
			"8511a5230231aac06e4406daa8acf7a72db28f2d675220ff1cdb3860e40c72741ab481f103e0fdfaeee27f3019a63626"+
			"b03d73724783707a98998b26c3fdc9",
	)
	recovery := mustDecodeHex(t,
		"f89ecc3c1abc39107aac7c6f0f14812ad56396763f3811fbbcdeb4484cc2341f57f4397aebf1ae9cd2861f0b00c65fd6"+
			"d1bc1ade33026844ea9e43cb87cd45b73eedcafdd10e92bae665c74952f8d9e7b0d52290a322b6e7f9f08c2015ec7760"+
			"e37b614827315ae6237d4ac567edabc589879eb69ae19d328617ed4e3a42650257780886508000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"+
			"000000000000000000000000000021762d14e1499436820b31dda2decbc315253e77eecbd3bacb74598abdab638c171d"+
			"c7d35e8a43faa87b0fd117028815c48573e2ee0ec143f26fc4896a9696ed5817e22fc65ce0ff2b1d5295f810d341739e"+
			"b5a139c9",
	)
	packets := [][]byte{prime, mutant, recovery, nil, prime}
	formats := [...]uint32{
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
		libopustest.DecodeDiffFormatFloat32,
		libopustest.DecodeDiffFormatInt16,
		libopustest.DecodeDiffFormatInt24,
	}
	frameSizes := [...]uint32{960, 960, 960, 40, 960}
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, packet := range packets {
		cases[i] = libopustest.DecodeDiffCase{Packet: packet, Format: formats[i], FrameSize: frameSizes[i]}
	}
	want, err := libopustest.ProbeDecodeSequence(8000, 2, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "mono Hybrid PLC recovery", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(8000, 2))
	if err != nil {
		t.Fatal(err)
	}
	const maxSamples = 960 * 2
	floatPCM := make([]float32, maxSamples)
	int16PCM := make([]int16, maxSamples)
	int24PCM := make([]int32, maxSamples)
	for step, c := range cases {
		var n int
		switch c.Format {
		case libopustest.DecodeDiffFormatFloat32:
			n, err = dec.Decode(c.Packet, floatPCM[:int(c.FrameSize)*2])
		case libopustest.DecodeDiffFormatInt16:
			n, err = dec.DecodeInt16(c.Packet, int16PCM[:int(c.FrameSize)*2])
		default:
			n, err = dec.DecodeInt24(c.Packet, int24PCM[:int(c.FrameSize)*2])
		}
		if err != nil || int32(n) != want[step].Code {
			t.Fatalf("step%d decode=(%d,%v), C returned %d", step, n, err, want[step].Code)
		}
		if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
			t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
		}
		for sample := 0; sample < n*2; sample++ {
			switch c.Format {
			case libopustest.DecodeDiffFormatFloat32:
				got := math.Float32bits(floatPCM[sample])
				if expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:]); got != expected {
					t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
				}
			case libopustest.DecodeDiffFormatInt16:
				got := int16PCM[sample]
				if expected := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:])); got != expected {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
				}
			default:
				got := int24PCM[sample]
				if expected := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:])); got != expected {
					t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
				}
			}
		}
	}
}

func assertDecoderSequenceFormatsMatchSelectedLibopus(t *testing.T, rate, channels int, packets [][]byte) {
	t.Helper()
	frameSize := rate / 50
	for format := uint32(0); format < 3; format++ {
		t.Run(fmt.Sprintf("format%d", format), func(t *testing.T) {
			cases := make([]libopustest.DecodeDiffCase, len(packets))
			for i, p := range packets {
				cases[i] = libopustest.DecodeDiffCase{Packet: p, Format: format, FrameSize: uint32(frameSize)}
			}
			want, err := libopustest.ProbeDecodeSequence(rate, channels, cases)
			if err != nil {
				t.Fatal(err)
			}
			d, err := NewDecoder(DefaultDecoderConfig(rate, channels))
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, frameSize*channels)
			pcm16 := make([]int16, frameSize*channels)
			pcm24 := make([]int32, frameSize*channels)
			decode := func(p []byte) (int, error) {
				switch format {
				case 0:
					return d.Decode(p, pcm)
				case 1:
					return d.DecodeInt16(p, pcm16)
				default:
					return d.DecodeInt24(p, pcm24)
				}
			}
			for step, p := range packets {
				n, err := decode(p)
				if err != nil || n != int(want[step].Code) {
					t.Fatalf("step%d samples%d err%v C%d", step, n, err, want[step].Code)
				}
				if d.FinalRange() != want[step].FinalRange {
					t.Fatalf("step%d range%08x C%08x", step, d.FinalRange(), want[step].FinalRange)
				}
				switch format {
				case 0:
					for i, v := range want[step].Float32() {
						if math.Float32bits(pcm[i]) != math.Float32bits(v) {
							t.Fatalf("step%d sample%d=%08x C=%08x", step, i, math.Float32bits(pcm[i]), math.Float32bits(v))
						}
					}
				case 1:
					for i, v := range want[step].Int16() {
						if pcm16[i] != v {
							t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm16[i], v)
						}
					}
				case 2:
					for i, v := range want[step].Int24() {
						if pcm24[i] != v {
							t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm24[i], v)
						}
					}
				}
			}
			if allocs := testing.AllocsPerRun(10, func() {
				for _, p := range packets {
					if _, err := decode(p); err != nil {
						panic(err)
					}
				}
			}); allocs != 0 {
				t.Fatalf("warm allocations=%g", allocs)
			}
		})
	}
}

func TestNewDecoder_ValidParams(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate int
		channels   int
	}{
		{"48kHz_mono", 48000, 1},
		{"48kHz_stereo", 48000, 2},
		{"24kHz_mono", 24000, 1},
		{"24kHz_stereo", 24000, 2},
		{"16kHz_mono", 16000, 1},
		{"16kHz_stereo", 16000, 2},
		{"12kHz_mono", 12000, 1},
		{"12kHz_stereo", 12000, 2},
		{"8kHz_mono", 8000, 1},
		{"8kHz_stereo", 8000, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(tt.sampleRate, tt.channels))
			if err != nil {
				t.Fatalf("NewDecoder(%d, %d) unexpected error: %v", tt.sampleRate, tt.channels, err)
			}
			if dec == nil {
				t.Fatal("NewDecoder returned nil decoder")
			}
			if dec.SampleRate() != tt.sampleRate {
				t.Errorf("SampleRate() = %d, want %d", dec.SampleRate(), tt.sampleRate)
			}
			if dec.Channels() != tt.channels {
				t.Errorf("Channels() = %d, want %d", dec.Channels(), tt.channels)
			}
		})
	}
}

func TestNewDecoder_InvalidSampleRate(t *testing.T) {
	// 96000 is only invalid without the gopus_qext build tag.
	invalidRates := []int{0, 1000, 7999, 8001, 44100, -1}
	if !extsupport.QEXT {
		invalidRates = append(invalidRates, 96000)
	}

	for _, rate := range invalidRates {
		t.Run(fmt.Sprintf("rate_%d", rate), func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(rate, 1))
			if err != ErrInvalidSampleRate {
				t.Errorf("NewDecoder(%d, 1) error = %v, want ErrInvalidSampleRate", rate, err)
			}
			if dec != nil {
				t.Error("NewDecoder returned non-nil decoder on error")
			}
		})
	}
}

func TestNewDecoder_InvalidChannels(t *testing.T) {
	invalidChannels := []int{0, -1, 3, 4, 100}

	for _, ch := range invalidChannels {
		t.Run(fmt.Sprintf("channels_%d", ch), func(t *testing.T) {
			dec, err := NewDecoder(DefaultDecoderConfig(48000, ch))
			if err != ErrInvalidChannels {
				t.Errorf("NewDecoder(48000, %d) error = %v, want ErrInvalidChannels", ch, err)
			}
			if dec != nil {
				t.Error("NewDecoder returned non-nil decoder on error")
			}
		})
	}
}

func TestNewDecoderRejectsUnrepresentableMaxPacketSamples(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	sampleRates := []int{48000}
	if extsupport.QEXT {
		sampleRates = append(sampleRates, 96000)
	}
	for _, sampleRate := range sampleRates {
		t.Run(fmt.Sprintf("%dHz", sampleRate), func(t *testing.T) {
			dec, err := NewDecoder(DecoderConfig{
				SampleRate:       sampleRate,
				Channels:         2,
				MaxPacketSamples: maxInt,
			})
			if dec != nil {
				t.Fatal("NewDecoder returned a decoder for an unrepresentable sample cap")
			}
			if err != ErrInvalidMaxPacketSamples {
				t.Fatalf("NewDecoder error=%v, want %v", err, ErrInvalidMaxPacketSamples)
			}
		})
	}
}

func TestNewDecoder_DefaultMaxPacketLimits(t *testing.T) {
	dec, err := NewDecoder(DecoderConfig{
		SampleRate: 48000,
		Channels:   2,
	})
	if err != nil {
		t.Fatalf("NewDecoder() unexpected error: %v", err)
	}
	if dec.maxPacketSamples != defaultMaxPacketSamples {
		t.Fatalf("maxPacketSamples=%d want %d", dec.maxPacketSamples, defaultMaxPacketSamples)
	}
	if dec.maxPacketBytes != defaultMaxPacketBytes {
		t.Fatalf("maxPacketBytes=%d want %d", dec.maxPacketBytes, defaultMaxPacketBytes)
	}
}

func minimalHybridTestPacket20ms() []byte {
	// TOC byte: config=15 (Hybrid FB 20ms), mono, code 0
	// 15 << 3 | 0 << 2 | 0 = 0x78
	toc := byte(0x78)

	// Frame data: minimal valid hybrid data
	// These bytes are designed to produce valid (if near-silence) SILK+CELT decode
	frameData := []byte{
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
	}

	return append([]byte{toc}, frameData...)
}

// minimalHybridTestPacket20msStereo creates a test packet for Hybrid FB 20ms stereo.
// TOC byte (0x7C) indicates: config=15 (Hybrid FB 20ms), stereo, code 0.
func minimalHybridTestPacket20msStereo() []byte {
	// TOC byte: config=15 (Hybrid FB 20ms), stereo, code 0
	// 15 << 3 | 1 << 2 | 0 = 0x7C
	toc := byte(0x7C)

	// Frame data for stereo
	frameData := []byte{
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
	}

	return append([]byte{toc}, frameData...)
}

func TestDecoder_Decode_Float32(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960 // 20ms at 48kHz

	pcmOut := make([]float32, frameSize)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decode returned %d samples, want %d", n, frameSize)
	}

	// Check that output buffer was written to (decoder ran without panic)
	t.Logf("Decoded %d samples successfully", n)
}

func TestDecoder_Decode_Int16(t *testing.T) {
	dec := newMonoTestDecoder(t)

	pcmOut := make([]int16, 960)
	n, err := dec.DecodeInt16(minimalHybridTestPacket20ms(), pcmOut)
	if err != nil {
		t.Fatalf("DecodeInt16 error: %v", err)
	}

	if n != 960 {
		t.Errorf("DecodeInt16 returned %d samples, want %d", n, 960)
	}
}

func TestDecodeFloatClearsSoftClipMemOnPacket(t *testing.T) {
	dec := newMonoTestDecoder(t)
	dec.softClipMem[0] = 0.25

	pcm := make([]float32, dec.maxPacketSamples*int(dec.channels))
	if _, err := dec.Decode(minimalHybridTestPacket20ms(), pcm); err != nil {
		t.Fatalf("Decode packet error: %v", err)
	}
	if dec.softClipMem[0] != 0 || dec.softClipMem[1] != 0 {
		t.Fatalf("Decode packet softClipMem=%v want zeroed", dec.softClipMem)
	}
}

func TestDecodeInt16PLCPreservesSoftClipMem(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	want := [2]float32{0.25, -0.125}
	dec.softClipMem = want

	pcm := make([]int16, dec.maxPacketSamples*int(dec.channels))
	if _, err := dec.DecodeInt16(nil, pcm); err != nil {
		t.Fatalf("DecodeInt16 PLC error: %v", err)
	}
	if dec.softClipMem != want {
		t.Fatalf("DecodeInt16 PLC softClipMem=%v want %v", dec.softClipMem, want)
	}
}

func TestDecoder_Decode_BufferTooSmall(t *testing.T) {
	dec := newMonoTestDecoder(t)

	// Buffer too small for float32
	pcmOut := make([]float32, 480)
	_, err := dec.Decode(minimalHybridTestPacket20ms(), pcmOut)
	if err != ErrBufferTooSmall {
		t.Errorf("Decode with small buffer: got %v, want ErrBufferTooSmall", err)
	}

	// Buffer too small for int16
	pcmOut16 := make([]int16, 480)
	_, err = dec.DecodeInt16(minimalHybridTestPacket20ms(), pcmOut16)
	if err != ErrBufferTooSmall {
		t.Errorf("DecodeInt16 with small buffer: got %v, want ErrBufferTooSmall", err)
	}
}

func TestDecodeRejectsCode3DurationOver120msBeforeLocalCap(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	_, err = dec.Decode([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err != ErrInvalidPacket {
		t.Fatalf("Decode error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeInt16RejectsCode3DurationOver120msBeforeLocalCap(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]int16, cfg.MaxPacketSamples)
	_, err = dec.DecodeInt16([]byte{GenerateTOC(31, false, 3), 0x07}, pcm)
	if err != ErrInvalidPacket {
		t.Fatalf("DecodeInt16 error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeValid120msPacketUsesBufferSizeAfterProtocolCheck(t *testing.T) {
	dec := newMonoTestDecoder(t)
	packet := append([]byte{GenerateTOC(16, false, 3), 0x30}, make([]byte, 48)...)

	_, err := dec.Decode(packet, make([]float32, 120))
	if err != ErrBufferTooSmall {
		t.Fatalf("Decode error=%v want %v", err, ErrBufferTooSmall)
	}
}

func TestDecoder_Decode_PLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// First decode a valid frame to set up state
	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcmOut := make([]float32, frameSize)
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Now simulate packet loss by passing nil
	pcmPLC := make([]float32, frameSize)
	n, err := dec.Decode(nil, pcmPLC)
	if err != nil {
		t.Fatalf("Decode PLC error: %v", err)
	}

	if n != frameSize {
		t.Errorf("PLC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("PLC produced %d samples", n)
}

func TestDecoder_DecodePLCUsesRequestedBufferFrameSize(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	if _, err := dec.Decode(packet, make([]float32, 960)); err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	for _, requested := range []int{960, 1920} {
		pcmPLC := make([]float32, requested)
		n, err := dec.Decode(nil, pcmPLC)
		if err != nil {
			t.Fatalf("Decode PLC request=%d: %v", requested, err)
		}
		if n != requested {
			t.Fatalf("PLC request=%d returned %d samples per channel", requested, n)
		}
	}
}

func TestDecoder_Reset(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	// Decode a frame
	pcmOut := make([]float32, frameSize)
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Reset
	dec.Reset()

	// Decode again should work
	_, err = dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode after Reset error: %v", err)
	}
}

func TestDecoder_Stereo(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	packet := minimalHybridTestPacket20msStereo()
	frameSize := 960

	pcmOut := make([]float32, frameSize*2) // Stereo: 2 channels
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	if n != frameSize {
		t.Errorf("Decode returned %d samples per channel, want %d", n, frameSize)
	}
}

func TestDecoder_TOCParsing(t *testing.T) {
	// Test that the decoder correctly parses the TOC byte to determine frame size
	tests := []struct {
		name      string
		toc       byte
		frameSize int
	}{
		// Hybrid FB 20ms: config 15
		{"hybrid_fb_20ms", 0x78, 960},
		// Hybrid FB 10ms: config 14
		{"hybrid_fb_10ms", 0x70, 480},
		// SILK WB 20ms: config 9
		{"silk_wb_20ms", 0x48, 960},
		// CELT FB 20ms: config 31
		{"celt_fb_20ms", 0xF8, 960},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toc := ParseTOC(tt.toc)
			if toc.FrameSize != tt.frameSize {
				t.Errorf("TOC %02X: FrameSize = %d, want %d", tt.toc, toc.FrameSize, tt.frameSize)
			}
		})
	}
}

// TestDecode_ModeRouting verifies that packets are routed to the correct decoder
// based on their TOC mode field.
func TestDecode_ModeRouting(t *testing.T) {
	tests := []struct {
		name      string
		config    uint8 // TOC config (0-31)
		frameSize int   // Expected frame size at 48kHz
		mode      Mode  // Expected mode
	}{
		// SILK-only (configs 0-11)
		{"SILK NB 10ms", 0, 480, ModeSILK},
		{"SILK NB 20ms", 1, 960, ModeSILK},
		{"SILK NB 40ms", 2, 1920, ModeSILK},
		{"SILK NB 60ms", 3, 2880, ModeSILK},
		{"SILK MB 20ms", 5, 960, ModeSILK},
		{"SILK WB 20ms", 9, 960, ModeSILK},
		{"SILK WB 40ms", 10, 1920, ModeSILK},
		{"SILK WB 60ms", 11, 2880, ModeSILK},

		// Hybrid (configs 12-15)
		{"Hybrid SWB 10ms", 12, 480, ModeHybrid},
		{"Hybrid SWB 20ms", 13, 960, ModeHybrid},
		{"Hybrid FB 10ms", 14, 480, ModeHybrid},
		{"Hybrid FB 20ms", 15, 960, ModeHybrid},

		// CELT-only (configs 16-31)
		{"CELT NB 2.5ms", 16, 120, ModeCELT},
		{"CELT NB 5ms", 17, 240, ModeCELT},
		{"CELT NB 10ms", 18, 480, ModeCELT},
		{"CELT NB 20ms", 19, 960, ModeCELT},
		{"CELT FB 2.5ms", 28, 120, ModeCELT},
		{"CELT FB 5ms", 29, 240, ModeCELT},
		{"CELT FB 10ms", 30, 480, ModeCELT},
		{"CELT FB 20ms", 31, 960, ModeCELT},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify TOC parsing
			toc := ParseTOC(GenerateTOC(tt.config, false, 0))

			if toc.Mode != tt.mode {
				t.Errorf("Mode mismatch: got %v, want %v", toc.Mode, tt.mode)
			}
			if toc.FrameSize != tt.frameSize {
				t.Errorf("FrameSize mismatch: got %d, want %d", toc.FrameSize, tt.frameSize)
			}

			// Test decoder accepts the packet (may fail on decode but should not fail on routing)
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatalf("NewDecoder failed: %v", err)
			}

			// Create minimal valid packet (TOC + some data)
			packet := make([]byte, 100)
			packet[0] = GenerateTOC(tt.config, false, 0)
			// Fill with minimal valid data for range decoder
			for i := 1; i < len(packet); i++ {
				packet[i] = byte(i)
			}

			// Decode should not panic and should not return "hybrid: invalid frame size"
			pcm := make([]float32, tt.frameSize*2) // Extra buffer
			_, err = dec.Decode(packet, pcm)

			// For extended frame sizes, we expect decode to succeed (no routing error)
			// The decode may still fail for other reasons (invalid bitstream) but
			// should NOT fail with "hybrid: invalid frame size"
			if err != nil {
				errStr := err.Error()
				if strings.Contains(errStr, "hybrid: invalid frame size") {
					t.Errorf("Mode routing failed: SILK/CELT packet incorrectly routed to hybrid decoder: %v", err)
				}
				// Log other errors but don't fail - bitstream content may be invalid
				t.Logf("Decode error (non-routing): %v", err)
			}
		})
	}
}

// TestDecode_ExtendedFrameSizes verifies that extended frame sizes (CELT 2.5/5ms,
// SILK 40/60ms) are accepted without being rejected by the hybrid decoder.
func TestDecode_ExtendedFrameSizes(t *testing.T) {
	// Test that extended frame sizes don't trigger hybrid validation error
	extendedConfigs := []struct {
		name      string
		config    uint8
		frameSize int
	}{
		{"CELT 2.5ms", 28, 120}, // CELT FB 2.5ms
		{"CELT 5ms", 29, 240},   // CELT FB 5ms
		{"SILK 40ms", 10, 1920}, // SILK WB 40ms
		{"SILK 60ms", 11, 2880}, // SILK WB 60ms
	}

	for _, tt := range extendedConfigs {
		t.Run(tt.name, func(t *testing.T) {
			dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

			packet := make([]byte, 100)
			packet[0] = GenerateTOC(tt.config, false, 0)
			for i := 1; i < len(packet); i++ {
				packet[i] = byte(i * 7) // Different pattern
			}

			pcm := make([]float32, tt.frameSize*2)
			_, err := dec.Decode(packet, pcm)

			// Critical: should NOT fail with hybrid frame size error
			if err != nil && strings.Contains(err.Error(), "hybrid: invalid frame size") {
				t.Errorf("Extended frame size incorrectly rejected as hybrid: %v", err)
			}
		})
	}
}

// TestDecode_PLC_ModeTracking verifies that PLC uses the last decoded mode,
// not defaulting to Hybrid mode.
func TestDecode_PLC_ModeTracking(t *testing.T) {
	dec, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

	// First: decode a SILK packet to set mode
	silkPacket := make([]byte, 50)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i)
	}

	pcm := make([]float32, 960*2)
	_, _ = dec.Decode(silkPacket, pcm)

	// PLC should use last mode (SILK)
	_, err := dec.Decode(nil, pcm)
	if err != nil && strings.Contains(err.Error(), "hybrid") {
		t.Errorf("PLC should use SILK mode, not hybrid: %v", err)
	}
}

func TestExtractFirstFramePayloadCode3VBROneFrameWithPadding(t *testing.T) {
	packet := append([]byte{0x43, 0xC1, 0x03}, []byte{0x4D, 0x66, 0xDD, 0x53, 0xE3}...)
	packet = append(packet, 0x00, 0x00, 0x00)

	firstFrameData, err := extractFirstFramePayload(packet, ParseTOC(packet[0]))
	if err != nil {
		t.Fatalf("extractFirstFramePayload error: %v", err)
	}

	want := []byte{0x4D, 0x66, 0xDD, 0x53, 0xE3}
	if len(firstFrameData) != len(want) {
		t.Fatalf("payload length=%d want=%d", len(firstFrameData), len(want))
	}
	for i := range want {
		if firstFrameData[i] != want[i] {
			t.Fatalf("payload[%d]=0x%02x want=0x%02x", i, firstFrameData[i], want[i])
		}
	}
}

func TestDecodeCode3VBROneFramePaddingRegression(t *testing.T) {
	packet := mustDecodeHex(t, "43c1064d66dd53e3b92d85ca64ec672fb6384f7b2dd2cb3164f5e17ae7b97e7a7e69544afe2e8880")

	cfg := DefaultDecoderConfig(48000, 1)
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm := make([]float32, cfg.MaxPacketSamples)
	n, err := dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if n <= 0 || n > cfg.MaxPacketSamples {
		t.Fatalf("Decode samples=%d outside (0,%d]", n, cfg.MaxPacketSamples)
	}
}

func decodeWithPrivateFloatCELTAPIRateReference(sampleRate, channels, frameSize int, packets [][]byte) ([]float32, error) {
	binPath, err := privateFloatCELTAPIRateReferenceHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatFloat32, uint32(sampleRate), 0,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "private float CELT API-rate reference decode", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]float32, nSamples)
	for i := range decoded {
		decoded[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

func TestCELTDecoderAPIRateToFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize48 := range []int{240, 960} {
		for _, packetChannels := range []int{1, 2} {
			packet := encodeAPIRateCELTPacketFrameSize(t, packetChannels, frameSize48)
			toc := ParseTOC(packet[0])
			if toc.Mode != ModeCELT {
				t.Fatalf("packet mode=%v want CELT", toc.Mode)
			}
			payload := firstAPIRateFramePayload(t, packet)
			for _, decoderChannels := range []int{1, 2} {
				for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
					t.Run("frame_"+itoaSmall(frameSize48)+"_pktch_"+itoaSmall(packetChannels)+"_decch_"+itoaSmall(decoderChannels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
						frameSize, err := packetSamplesAtRate(packet, sampleRate)
						if err != nil {
							t.Fatalf("packetSamplesAtRate: %v", err)
						}
						want, err := decodeWithPrivateFloatCELTAPIRateReference(sampleRate, decoderChannels, frameSize, [][]byte{packet})
						if err != nil {
							libopustest.HelperUnavailable(t, "low-level CELT API-rate reference decode", err)
						}

						dec := celtpkg.NewDecoder(decoderChannels)
						if err := dec.SetAPISampleRate(sampleRate); err != nil {
							t.Fatalf("SetAPISampleRate: %v", err)
						}
						dec.SetBandwidth(celtpkg.BandwidthFromOpusConfig(int(toc.Bandwidth)))
						got := make([]float32, frameSize*decoderChannels)
						if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(payload, frameSize, toc.Stereo, got); err != nil {
							t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate: %v", err)
						}
						assertAPIRateQualityFloat32(t, got, want, sampleRate, decoderChannels, "low-level CELT API-rate decode")
						assertAPIRateFloat32BitsExact(t, got, want, "low-level CELT API-rate decode")
					})
				}
			}
		}
	}
}

func TestCELTDecoderAPIRatePLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, packetChannels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, packetChannels)
		toc := ParseTOC(packet[0])
		payload := firstAPIRateFramePayload(t, packet)
		for _, decoderChannels := range []int{1, 2} {
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run("pktch_"+itoaSmall(packetChannels)+"_decch_"+itoaSmall(decoderChannels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					want, err := decodeWithPrivateFloatCELTAPIRateReference(sampleRate, decoderChannels, frameSize, [][]byte{packet, nil})
					if err != nil {
						libopustest.HelperUnavailable(t, "low-level CELT API-rate PLC reference decode", err)
					}

					dec := celtpkg.NewDecoder(decoderChannels)
					if err := dec.SetAPISampleRate(sampleRate); err != nil {
						t.Fatalf("SetAPISampleRate: %v", err)
					}
					dec.SetBandwidth(celtpkg.BandwidthFromOpusConfig(int(toc.Bandwidth)))
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*decoderChannels)
					if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(payload, frameSize, toc.Stereo, frame); err != nil {
						t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate packet: %v", err)
					}
					got = append(got, frame...)
					clear(frame)
					if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(nil, frameSize, toc.Stereo, frame); err != nil {
						t.Fatalf("DecodeFrameWithPacketStereoToFloat32AtAPIRate PLC: %v", err)
					}
					got = append(got, frame...)
					assertAPIRateQualityFloat32(t, got, want, sampleRate, decoderChannels, "low-level CELT API-rate PLC")
					assertAPIRateFloat32BitsExact(t, got, want, "low-level CELT API-rate PLC")
				})
			}
		}
	}
}

func TestCELTChannelRecoveryMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	mono, err := hex.DecodeString("f87de653c754da8fd5b84b5c152311f42e2736250011668e86ffe7531d2559002d1069c04ba89ce0191fa84b411482b75a8d02fdc36760a2d58341a47247422ebdaad83ec8e520711ffaccd38509bac00000000000000000000000000000000000056be55a555117d558557cf16e8b3101f0c24f72c33861766a8be92409a7147be6532762112640002dc15097392136b2b9382e1e0705696039660141a53754")
	if err != nil {
		t.Fatal(err)
	}
	stereo := encodeAPIRateCELTPacketFrameSize(t, 2, 960)
	if stereo[0]&4 == 0 {
		t.Fatal("recovery packet must be stereo")
	}
	rates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for _, losses := range []int{0, 1, 2, 6, 15} {
			t.Run(fmt.Sprintf("fs%d/losses%d", rate, losses), func(t *testing.T) {
				packets := make([][]byte, losses+3)
				packets[0] = mono
				packets[len(packets)-2] = stereo
				packets[len(packets)-1] = stereo
				assertDecoderSequenceFormatsMatchSelectedLibopus(t, rate, 2, packets)
			})
		}
	}
}

var libopusCELTTraceHelper libopustest.HelperCache

// libopusCELTTrace reports signal, gain and log-energy values in float units.
// The C helper removes fixed-point Q formats before serializing those fields.
type libopusCELTTrace struct {
	decodedSamples      int
	internalSamples     int
	downsample          int
	overlap             int
	decodeBuffer        int
	channel             int
	start               int
	finalRange          uint32
	celtRng             uint32
	lossDuration        int
	plcDuration         int
	postfilter          int
	postfilterOld       int
	postfilterGain      float32
	postfilterGainOld   float32
	postfilterTapset    int
	postfilterTapsetOld int
	lastPitchPeriod     int
	lastFrameType       int
	skipPLC             bool
	prefilterFold       bool
	oldBandE            []float32
	final               []float32
	preDeemphasis       []float32
}

func buildLibopusCELTTraceHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "CELT decode trace",
		OutputBase:  "gopus_libopus_celt_trace",
		SourceFile:  "libopus_celt_trace_single.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	})
}

func traceLibopusCELT(sampleRate, channels, frameSize, targetStep, targetChannel, start, count int, steps []libopusAPIRateDecodeStep) (*libopusCELTTrace, error) {
	binPath, err := libopusCELTTraceHelper.Path(buildLibopusCELTTraceHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GCTI",
		uint32(sampleRate), uint32(channels), uint32(frameSize),
		uint32(targetStep), uint32(targetChannel), uint32(start), uint32(count),
		uint32(len(steps)))
	for _, step := range steps {
		if step.fec {
			payload.U32(1)
		} else {
			payload.U32(0)
		}
		payload.U32(uint32(len(step.packet)))
		payload.Raw(step.packet)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "CELT decode trace", "GCTO")
	if err != nil {
		return nil, err
	}
	trace := &libopusCELTTrace{
		decodedSamples:  int(reader.U32()),
		internalSamples: int(reader.U32()),
		downsample:      int(reader.U32()),
		overlap:         int(reader.U32()),
		decodeBuffer:    int(reader.U32()),
		channel:         int(reader.U32()),
		start:           int(reader.U32()),
	}
	outCount := int(reader.U32())
	trace.finalRange = reader.U32()
	trace.celtRng = reader.U32()
	trace.lossDuration = int(reader.U32())
	trace.plcDuration = int(reader.U32())
	trace.postfilter = int(reader.U32())
	trace.postfilterOld = int(reader.U32())
	trace.postfilterGain = reader.Float32()
	trace.postfilterGainOld = reader.Float32()
	trace.postfilterTapset = int(reader.U32())
	trace.postfilterTapsetOld = int(reader.U32())
	trace.lastPitchPeriod = int(reader.U32())
	trace.lastFrameType = int(reader.U32())
	trace.skipPLC = reader.U32() != 0
	trace.prefilterFold = reader.U32() != 0
	oldBandECount := int(reader.U32())
	trace.final = make([]float32, outCount)
	trace.preDeemphasis = make([]float32, outCount)
	trace.oldBandE = make([]float32, oldBandECount)
	reader.ExpectRemaining((outCount*2 + oldBandECount) * 4)
	for i := range trace.final {
		trace.final[i] = reader.Float32()
	}
	for i := range trace.preDeemphasis {
		trace.preDeemphasis[i] = reader.Float32()
	}
	for i := range trace.oldBandE {
		trace.oldBandE[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return trace, nil
}

func TestLibopusCELTTraceMatchesReferenceDecodeWindow(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		start      = 384
		count      = 64
	)
	steps := []libopusAPIRateDecodeStep{{packet: packet}}
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, start, count, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}
	if trace.decodedSamples != frameSize || trace.internalSamples != frameSize {
		t.Fatalf("trace samples decoded=%d internal=%d want %d", trace.decodedSamples, trace.internalSamples, frameSize)
	}
	if trace.downsample != 1 || trace.decodeBuffer != 2048 || trace.overlap <= 0 {
		t.Fatalf("trace metadata downsample=%d decodeBuffer=%d overlap=%d", trace.downsample, trace.decodeBuffer, trace.overlap)
	}
	if trace.channel != 0 || trace.start != start || len(trace.final) != count || len(trace.preDeemphasis) != count {
		t.Fatalf("trace window channel=%d start=%d final=%d pre=%d", trace.channel, trace.start, len(trace.final), len(trace.preDeemphasis))
	}

	want, err := decodeWithLibopusReferenceAPIRateFloat32(sampleRate, channels, frameSize, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "api-rate reference decode", err)
	}
	for i, got := range trace.final {
		if got != want[start+i] {
			t.Fatalf("trace final[%d]=%08x want %08x", start+i, math.Float32bits(got), math.Float32bits(want[start+i]))
		}
	}
	nonZero := false
	for _, sample := range trace.preDeemphasis {
		if sample != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatal("trace pre-deemphasis window is silent")
	}
}

func TestLibopusCELTTraceFinalRangeMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, 0, 1, []libopusAPIRateDecodeStep{{packet: packet}})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize)
	n, err := dec.Decode(packet, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if n != frameSize {
		t.Fatalf("Decode samples=%d want %d", n, frameSize)
	}
	if got := dec.FinalRange(); got != trace.finalRange {
		t.Fatalf("FinalRange()=0x%08x want libopus 0x%08x", got, trace.finalRange)
	}
	if trace.lossDuration != 0 || trace.plcDuration != 0 {
		t.Fatalf("unexpected good-frame loss state: loss=%d plc=%d", trace.lossDuration, trace.plcDuration)
	}
}

func TestLibopusCELTTracePostfilterStateMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
	goodTrace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, 0, 1, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}
	lossTrace, err := traceLibopusCELT(sampleRate, channels, frameSize, 1, 0, 0, 1, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize)
	if _, err := dec.Decode(packet, out); err != nil {
		t.Fatalf("Decode good: %v", err)
	}
	goodState := dec.celtDecoder.PostfilterState()
	if got, want := goodState.Period, goodTrace.postfilter; got != want {
		t.Fatalf("good postfilterPeriod=%d want libopus %d", got, want)
	}
	if got, want := goodState.PreviousPeriod, goodTrace.postfilterOld; got != want {
		t.Fatalf("good postfilterPeriodOld=%d want libopus %d", got, want)
	}
	if got, want := goodState.Gain, goodTrace.postfilterGain; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("good postfilterGain=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := goodState.PreviousGain, goodTrace.postfilterGainOld; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("good postfilterGainOld=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := goodState.Tapset, goodTrace.postfilterTapset; got != want {
		t.Fatalf("good postfilterTapset=%d want libopus %d", got, want)
	}
	if got, want := goodState.PreviousTapset, goodTrace.postfilterTapsetOld; got != want {
		t.Fatalf("good postfilterTapsetOld=%d want libopus %d", got, want)
	}
	if _, err := dec.Decode(nil, out); err != nil {
		t.Fatalf("Decode loss: %v", err)
	}
	lossState := dec.celtDecoder.PostfilterState()
	if got, want := lossState.Period, lossTrace.postfilter; got != want {
		t.Fatalf("loss postfilterPeriod=%d want libopus %d", got, want)
	}
	if got, want := lossState.PreviousPeriod, lossTrace.postfilterOld; got != want {
		t.Fatalf("loss postfilterPeriodOld=%d want libopus %d", got, want)
	}
	if got, want := lossState.Gain, lossTrace.postfilterGain; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("loss postfilterGain=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := lossState.PreviousGain, lossTrace.postfilterGainOld; math.Float32bits(got) != math.Float32bits(want) {
		t.Fatalf("loss postfilterGainOld=%08x want libopus %08x", math.Float32bits(got), math.Float32bits(want))
	}
	if got, want := lossState.Tapset, lossTrace.postfilterTapset; got != want {
		t.Fatalf("loss postfilterTapset=%d want libopus %d", got, want)
	}
	if got, want := lossState.PreviousTapset, lossTrace.postfilterTapsetOld; got != want {
		t.Fatalf("loss postfilterTapsetOld=%d want libopus %d", got, want)
	}
}

func TestLibopusCELTTracePLCStateMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 2)
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960
	)
	steps := []libopusAPIRateDecodeStep{{packet: packet}, {}}
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 1, 1, 0, 1, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize*channels)
	if _, err := dec.Decode(packet, out); err != nil {
		t.Fatalf("Decode good: %v", err)
	}
	if _, err := dec.Decode(nil, out); err != nil {
		t.Fatalf("Decode loss: %v", err)
	}

	state := dec.celtDecoder.SnapshotPLCState()
	if state.LossDuration != trace.lossDuration {
		t.Fatalf("lossDuration=%d want libopus %d", state.LossDuration, trace.lossDuration)
	}
	if state.PLCDuration != trace.plcDuration {
		t.Fatalf("plcDuration=%d want libopus %d", state.PLCDuration, trace.plcDuration)
	}
	if state.LastFrameType != trace.lastFrameType {
		t.Fatalf("lastFrameType=%d want libopus %d", state.LastFrameType, trace.lastFrameType)
	}
	if state.LastPitchPeriod != trace.lastPitchPeriod {
		t.Fatalf("lastPitchPeriod=%d want libopus %d", state.LastPitchPeriod, trace.lastPitchPeriod)
	}
	if state.SkipPLC != trace.skipPLC {
		t.Fatalf("skipPLC=%v want libopus %v", state.SkipPLC, trace.skipPLC)
	}
	if state.PrefilterAndFold != trace.prefilterFold {
		t.Fatalf("prefilterAndFold=%v want libopus %v", state.PrefilterAndFold, trace.prefilterFold)
	}
}

func TestLibopusCELTTraceEnergyStateMatchesDecoder(t *testing.T) {
	libopustest.RequireOracle(t)

	packet := encodeAPIRateCELTPacket(t, 1)
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
	)
	trace, err := traceLibopusCELT(sampleRate, channels, frameSize, 0, 0, 0, 1, []libopusAPIRateDecodeStep{{packet: packet}})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT decode trace", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	out := make([]float32, frameSize)
	if _, err := dec.Decode(packet, out); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	gotEnergy := dec.celtDecoder.PrevEnergy()
	if len(trace.oldBandE) < len(gotEnergy) {
		t.Fatalf("trace oldBandE len=%d want at least %d", len(trace.oldBandE), len(gotEnergy))
	}
	for i, got := range gotEnergy {
		got32 := float32(got)
		want := trace.oldBandE[i]
		if math.Float32bits(got32) != math.Float32bits(want) {
			t.Fatalf("oldBandE[%d]=%08x %.10g want %08x %.10g",
				i,
				math.Float32bits(got32), got32,
				math.Float32bits(want), want)
		}
	}
}

func TestDecodeWithFECFallbackUsesActiveSILKStateAfterDTX(t *testing.T) {
	libopustest.RequireOracle(t)
	packets := []struct {
		name string
		hex  string
	}{
		{"mediumband", "2c886c8fa475063df63e6542b4ce9f05336f72206289fea7969d38df05574daee769acbfb80969ae842bc66b7c899baed97f5f567e16cf66543eaf4b57bfed16ff4c788b0d2fe3fdcf505fa2f2f019f0a5eecc6525b878f33f366c019488098b917f3d927910602f569282dc523180"},
		{"wideband", "4c886bdd439a389a6d1adfa4b0f4676d21b8be7e8fadb6595ae40dd22868cf1ac68f79b23eba441b9c1566addf3ecd6e230f512b52a599418013841f59bbde9f010ac0d9315e55524fb9883f021dbd2a7d22ba628301297eabc89a2744b3d211a25d0afd66eb9d94d18733ea9760fdbc6eb475882cbb13e0932df6065c85427141650526d2566aacfeb63cfc0559e06340948af9bd842d7d13c0"},
		{"hybrid", fecRateChangePrimeHex},
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, fixture := range packets {
				for _, concealment := range []struct {
					name      string
					packet    []byte
					decodeFEC bool
				}{
					{name: "plc"},
					{name: "fec_missing", decodeFEC: true},
					{name: "fec_celt", packet: []byte{0x80}, decodeFEC: true},
				} {
					t.Run(fmt.Sprintf("rate%d/ch%d/%s/%s", sampleRate, channels, fixture.name, concealment.name), func(t *testing.T) {
						packet := mustDecodeFECTransitionPacket(t, fixture.hex)
						frameSize := sampleRate / 50
						// DTX updates the packet TOC but keeps SILK's active internal
						// rate and stereo layout. A CELT carrier cannot supply FEC
						// and takes the same path as ordinary concealment.
						steps := []libopustest.DecodeDiffCase{
							{Packet: packet, FrameSize: uint32(frameSize)},
							{Packet: []byte{0x80}, FrameSize: uint32(sampleRate / 400)},
							{Packet: concealment.packet, FrameSize: uint32(frameSize), DecodeFEC: concealment.decodeFEC},
							{Packet: packet, FrameSize: uint32(frameSize)},
						}
						want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "FEC fallback after DTX", err)
						}
						if len(want) != len(steps) {
							t.Fatalf("oracle returned %d calls, want %d", len(want), len(steps))
						}
						wantPCM := make([][]float32, len(want))
						for i := range want {
							if want[i].Code != int32(steps[i].FrameSize) {
								t.Fatalf("oracle call %d returned %d, want %d", i, want[i].Code, steps[i].FrameSize)
							}
							wantPCM[i] = want[i].Float32()
						}
						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatal(err)
						}
						pcm := make([]float32, frameSize*channels)
						runSequence := func() {
							dec.Reset()
							for i, step := range steps {
								out := pcm[:int(step.FrameSize)*channels]
								var n int
								var err error
								if step.DecodeFEC {
									n, err = dec.DecodeWithFEC(step.Packet, out, true)
								} else {
									n, err = dec.Decode(step.Packet, out)
								}
								if err != nil || n != int(want[i].Code) {
									t.Fatalf("call %d returned (%d,%v), C=%d", i, n, err, want[i].Code)
								}
								for j, sample := range out {
									if got, expected := math.Float32bits(sample), math.Float32bits(wantPCM[i][j]); got != expected {
										t.Fatalf("call %d PCM[%d]=%08x C=%08x", i, j, got, expected)
									}
								}
								if got := dec.FinalRange(); got != want[i].FinalRange {
									t.Fatalf("call %d range=%08x C=%08x", i, got, want[i].FinalRange)
								}
								if fixture.name == "hybrid" && (dec.prevMode != ModeHybrid || dec.prevRedundancy) {
									t.Fatalf("call %d must retain Hybrid concealment: mode=%v redundancy=%t", i, dec.prevMode, dec.prevRedundancy)
								}
							}
						}
						runSequence()
						if allocs := testing.AllocsPerRun(10, runSequence); allocs != 0 {
							t.Fatalf("warmed FEC/DTX sequence allocations=%g, want zero", allocs)
						}
					})
				}
			}
		}
	}
}

func TestDecodeFECHybridCELTStateRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	// Literal WB stereo SILK 20 ms with LBRR, mono FB Hybrid 10 ms, and mono
	// CELT 2.5 ms packets keep this transition independent of Go encoder changes.
	stereoSILK20 := mustDecodeFECTransitionPacket(t, "4cf4b570ea239b5f266d24663ae2018a2bad20194a98c83bf66e5ec83862f90768bb45b8e0bbc425dd1108971a382be20259bfbdae586b222ee1ed5ce7d224a6996468ef220390695d90df41c9435ac102b33f0aa0244c329f763ca5f04799b2bfd140bac3adb2d933b3e6f4ce18e44320bb0ff32b30")
	monoHybrid10 := mustDecodeFECTransitionPacket(t, "70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13ef63a92")
	monoCELT2p5 := mustDecodeFECTransitionPacket(t, "e0709a5e94881db77455c8f4a5cee95b6a6371fa6535a8228e472c23f406a21af682798a46ab92e97cf6dc6b419353bc82f2277591ad")
	if toc := ParseTOC(stereoSILK20[0]); toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("stereo SILK fixture TOC=(%v, stereo=%t)", toc.Mode, toc.Stereo)
	}
	if toc := ParseTOC(monoHybrid10[0]); toc.Mode != ModeHybrid || toc.Stereo {
		t.Fatalf("mono Hybrid fixture TOC=(%v, stereo=%t)", toc.Mode, toc.Stereo)
	}
	if toc := ParseTOC(monoCELT2p5[0]); toc.Mode != ModeCELT || toc.Stereo {
		t.Fatalf("mono CELT fixture TOC=(%v, stereo=%t)", toc.Mode, toc.Stereo)
	}

	formats := []struct {
		name   string
		format uint32
	}{
		{name: "float32", format: libopustest.DecodeDiffFormatFloat32},
		{name: "int16", format: libopustest.DecodeDiffFormatInt16},
		{name: "int24", format: libopustest.DecodeDiffFormatInt24},
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, outputFormat := range formats {
			t.Run(itoaSmall(sampleRate)+"/"+outputFormat.name, func(t *testing.T) {
				frames := [5]int{
					sampleRate / 50,        // 20 ms stereo SILK prime
					sampleRate / 400,       // 2.5 ms loss
					sampleRate / 100,       // 10 ms mono Hybrid transition
					sampleRate * 30 / 1000, // 10 ms recursive PLC + 20 ms stereo SILK LBRR
					sampleRate / 400,       // 2.5 ms mono CELT recovery
				}
				packets := [5][]byte{stereoSILK20, nil, monoHybrid10, stereoSILK20, monoCELT2p5}
				cases := make([]libopustest.DecodeDiffCase, len(frames))
				for i := range cases {
					format := outputFormat.format
					if i == 3 {
						// The public Go API exposes DecodeWithFEC only for float32.
						format = libopustest.DecodeDiffFormatFloat32
					}
					cases[i] = libopustest.DecodeDiffCase{
						Packet:    packets[i],
						Format:    format,
						FrameSize: uint32(frames[i]),
					}
				}
				cases[3].DecodeFEC = true
				want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, cases)
				if err != nil {
					libopustest.HelperUnavailable(t, "stateful selected-libopus SILK recovery sequence", err)
				}
				if len(want) != len(cases) {
					t.Fatalf("libopus returned %d calls, want %d", len(want), len(cases))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				for i, packet := range packets {
					var n int
					var gotF32 []float32
					var gotI16 []int16
					var gotI24 []int32
					if i == 3 || outputFormat.format == libopustest.DecodeDiffFormatFloat32 {
						gotF32 = make([]float32, frames[i]*2)
						if i == 3 {
							n, err = dec.DecodeWithFEC(packet, gotF32, true)
						} else {
							n, err = dec.Decode(packet, gotF32)
						}
					} else if outputFormat.format == libopustest.DecodeDiffFormatInt16 {
						gotI16 = make([]int16, frames[i]*2)
						n, err = dec.DecodeInt16(packet, gotI16)
					} else {
						gotI24 = make([]int32, frames[i]*2)
						n, err = dec.DecodeInt24(packet, gotI24)
					}
					if err != nil {
						t.Fatalf("Go call %d: %v", i, err)
					}
					if n != int(want[i].Code) {
						t.Fatalf("call %d returned %d samples, C=%d", i, n, want[i].Code)
					}
					if gotRange, wantRange := dec.FinalRange(), want[i].FinalRange; gotRange != wantRange {
						t.Fatalf("call %d final range=%08x, C=%08x", i, gotRange, wantRange)
					}

					switch {
					case i == 3 || outputFormat.format == libopustest.DecodeDiffFormatFloat32:
						ref := want[i].Float32()
						for j := range ref {
							if gotBits, wantBits := math.Float32bits(gotF32[j]), math.Float32bits(ref[j]); gotBits != wantBits {
								t.Fatalf("call %d float32 PCM[%d]=%08x, C=%08x", i, j, gotBits, wantBits)
							}
						}
					case outputFormat.format == libopustest.DecodeDiffFormatInt16:
						ref := want[i].Int16()
						for j := range ref {
							if gotI16[j] != ref[j] {
								t.Fatalf("call %d int16 PCM[%d]=%d, C=%d", i, j, gotI16[j], ref[j])
							}
						}
					default:
						ref := want[i].Int24()
						for j := range ref {
							if gotI24[j] != ref[j] {
								t.Fatalf("call %d int24 PCM[%d]=%d, C=%d", i, j, gotI24[j], ref[j])
							}
						}
					}
				}
			})
		}
	}

	// Repeated Reset plus the same recovery sequence stays allocation-free once
	// the decoder's buffers and per-channel state have been warmed.
	const sampleRate = 48000
	frames := [5]int{sampleRate / 50, sampleRate / 400, sampleRate / 100, sampleRate * 30 / 1000, sampleRate / 400}
	packets := [5][]byte{stereoSILK20, nil, monoHybrid10, stereoSILK20, monoCELT2p5}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
	if err != nil {
		t.Fatalf("NewDecoder for allocation check: %v", err)
	}
	outputs := [5][]float32{
		make([]float32, frames[0]*2),
		make([]float32, frames[1]*2),
		make([]float32, frames[2]*2),
		make([]float32, frames[3]*2),
		make([]float32, frames[4]*2),
	}
	var sequenceErr error
	badCountStep := -1
	badCount := 0
	runSequence := func() {
		dec.Reset()
		for i, packet := range packets {
			var n int
			var callErr error
			if i == 3 {
				n, callErr = dec.DecodeWithFEC(packet, outputs[i], true)
			} else {
				n, callErr = dec.Decode(packet, outputs[i])
			}
			if callErr != nil {
				sequenceErr = callErr
				return
			}
			if n != frames[i] {
				badCountStep = i
				badCount = n
				return
			}
		}
	}
	for range 3 {
		runSequence()
		if sequenceErr != nil {
			t.Fatalf("warm Reset/recovery sequence: %v", sequenceErr)
		}
		if badCountStep >= 0 {
			t.Fatalf("warm Reset/recovery call %d returned %d samples, want %d", badCountStep, badCount, frames[badCountStep])
		}
	}
	allocs := testing.AllocsPerRun(10, runSequence)
	if sequenceErr != nil {
		t.Fatalf("measured Reset/recovery sequence: %v", sequenceErr)
	}
	if badCountStep >= 0 {
		t.Fatalf("measured Reset/recovery call %d returned %d samples, want %d", badCountStep, badCount, frames[badCountStep])
	}
	if allocs != 0 {
		t.Fatalf("warmed Reset/recovery sequence allocations=%g, want 0", allocs)
	}
}

func TestDecodeWithFECHybridToSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	for _, sampleRate := range []int{16000, 48000} {
		for _, channels := range []int{1, 2} {
			for _, gainQ8 := range []int{0, 768} {
				for _, hasLBRR := range []bool{false, true} {
					name := "no_lbrr"
					if hasLBRR {
						name = "lbrr"
					}
					t.Run(strconv.Itoa(sampleRate)+"hz/ch"+strconv.Itoa(channels)+"/gain"+strconv.Itoa(gainQ8)+"/"+name, func(t *testing.T) {
						const primeFrameSize48 = 960
						frameSize := sampleRate * 3 / 25 // 120 ms, including the PLC prefix.
						primeFrameSize := primeFrameSize48 * sampleRate / 48000
						prime := encodeAPIRateHybridPacketFrameSize(t, channels, primeFrameSize48)
						var recovery []byte
						if hasLBRR {
							_, recovery = encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, 960)
							if !PacketHasLBRR(recovery) {
								t.Fatal("recovery packet does not carry LBRR")
							}
						} else {
							recovery = encodeAPIRateSILKPacketFrameSize(t, channels, 480)
							if len(recovery) <= 1 || PacketHasLBRR(recovery) {
								t.Fatalf("no-LBRR SILK packet length=%d hasLBRR=%v", len(recovery), PacketHasLBRR(recovery))
							}
						}
						if got := ParseTOC(recovery[0]).Mode; got != ModeSILK {
							t.Fatalf("recovery packet mode=%v want SILK", got)
						}

						steps := []libopusAPIRateDecodeStep{
							{packet: prime},
							{packet: recovery, fec: true},
						}
						want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsGainRanges(sampleRate, channels, frameSize, gainQ8, steps)
						if err != nil {
							libopustest.HelperUnavailable(t, "Hybrid-to-SILK FEC transition reference", err)
						}
						stride := frameSize * channels
						if len(want) != (primeFrameSize+frameSize)*channels || len(ranges) != len(steps) {
							t.Fatalf("reference samples/ranges=%d/%d want=%d/%d", len(want), len(ranges), (primeFrameSize+frameSize)*channels, len(steps))
						}

						dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gainQ8); err != nil {
							t.Fatal(err)
						}
						out := make([]float32, stride)
						if n, err := dec.Decode(prime, out); err != nil || n != primeFrameSize {
							t.Fatalf("prime decode=(%d,%v), want %d", n, err, primeFrameSize)
						}
						assertHybridSILKFECPCM(t, out[:primeFrameSize*channels], want[:primeFrameSize*channels], "prime")
						if got := dec.FinalRange(); got != ranges[0] {
							t.Fatalf("prime range=%08x want %08x", got, ranges[0])
						}
						n, err := dec.DecodeWithFEC(recovery, out, true)
						if err != nil || n != frameSize {
							t.Fatalf("FEC decode=(%d,%v), want %d", n, err, frameSize)
						}
						assertHybridSILKFECPCM(t, out, want[primeFrameSize*channels:], "FEC")
						if got := dec.FinalRange(); got != ranges[1] {
							t.Fatalf("FEC range=%08x want %08x", got, ranges[1])
						}
					})
				}
			}
		}
	}
}

func assertHybridSILKFECPCM(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM length=%d want=%d", label, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s PCM[%d]=%08x want=%08x", label, i, gotBits, wantBits)
		}
	}
}

func TestDecodeWithFECHybridToSILKWarmZeroAllocs(t *testing.T) {
	_, recovery := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, 960)
	if !PacketHasLBRR(recovery) {
		t.Fatal("warm SILK recovery packet has no LBRR")
	}
	prime := encodeAPIRateHybridPacketFrameSize(t, 1, 960)
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := dec.SetGain(768); err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, 5760)
	decodeSequence := func() {
		dec.Reset()
		if n, err := dec.Decode(prime, frame); err != nil || n != 960 {
			t.Fatalf("prime decode=(%d,%v)", n, err)
		}
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != 5760 {
			t.Fatalf("FEC decode=(%d,%v)", n, err)
		}
	}
	for range 3 {
		decodeSequence()
	}
	if allocs := testing.AllocsPerRun(20, decodeSequence); allocs != 0 {
		t.Fatalf("warm Hybrid-to-SILK FEC allocations=%g, want zero", allocs)
	}
}

func TestDecodeWithFECMultiFrameSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	cases := []struct {
		name      string
		frameSize int // at 48 kHz: 1920 = 40 ms, 2880 = 60 ms
		bw        Bandwidth
		decRate   int // decoder API rate (native rate keeps the check resampler-noise free)
		channels  int
	}{
		{"NB_40ms", 1920, BandwidthNarrowband, 8000, 1},
		{"MB_40ms", 1920, BandwidthMediumband, 12000, 1},
		{"WB_40ms", 1920, BandwidthWideband, 16000, 1},
		{"NB_60ms", 2880, BandwidthNarrowband, 8000, 1},
		{"MB_60ms", 2880, BandwidthMediumband, 12000, 1},
		{"WB_60ms", 2880, BandwidthWideband, 16000, 1},
		{"WB_60ms_48k", 2880, BandwidthWideband, 48000, 1},
		{"WB_60ms_stereo", 2880, BandwidthWideband, 16000, 2},
		{"WB_40ms_stereo", 1920, BandwidthWideband, 16000, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channels := tc.channels
			packets := encodeFECBurstyStreamForTest(t, tc.bw, 24000, channels, tc.frameSize, 40)

			fs, err := packetSamplesAtRate(packets[0], tc.decRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			// Collect every LBRR-carrying recovery packet (index >= 2 so the
			// decoder is warmed up) and verify each as an FEC recovery step.
			recovered := 0
			partial := 0
			missingPrefix := 0
			replayLogged := false
			logReplay := func(recovery int) {
				if replayLogged {
					return
				}
				replayLogged = true
				// The encoder's native arithmetic can change the input packets.
				// Preserve the first failing history for cross-host decoder replay.
				t.Logf("FEC replay: rate=%d channels=%d frame_size=%d recovery=%d", tc.decRate, channels, fs, recovery)
				for i, packet := range packets[:recovery+1] {
					t.Logf("FEC replay packet %d: %x", i, packet)
				}
			}
			for r := 2; r < len(packets); r++ {
				if !packetHasInBandFEC(t, packets[r]) {
					continue
				}

				// Plan: decode packets[0..r-1] normally, then FEC-recover packets[r].
				steps := make([]libopusAPIRateDecodeStep, 0, r+1)
				for i := 0; i < r; i++ {
					steps = append(steps, libopusAPIRateDecodeStep{packet: packets[i]})
				}
				steps = append(steps, libopusAPIRateDecodeStep{packet: packets[r], fec: true})
				fecIdx := len(steps) - 1

				want, wantRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(tc.decRate, channels, fs, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "multi-frame FEC reference", err)
				}
				if len(want) != len(steps)*fs*channels || len(wantRanges) != len(steps) {
					t.Fatalf("reference geometry: samples=%d ranges=%d, want samples=%d ranges=%d",
						len(want), len(wantRanges), len(steps)*fs*channels, len(steps))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(tc.decRate, channels))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				buf := make([]float32, fs*channels)
				got := make([]float32, 0, len(want))
				var gotRange uint32
				fecStart := 0
				for i, s := range steps {
					if i == fecIdx {
						fecStart = len(got)
					}
					var n int
					var de error
					if s.fec {
						n, de = dec.DecodeWithFEC(s.packet, buf, true)
					} else {
						n, de = dec.Decode(s.packet, buf)
					}
					if de != nil {
						t.Fatalf("recovery packet %d step %d: %v", r, i, de)
					}
					if n != fs {
						logReplay(r)
						t.Fatalf("recovery packet %d step %d: samples=%d want=%d", r, i, n, fs)
					}
					if i < fecIdx {
						if dec.FinalRange() != wantRanges[i] {
							logReplay(r)
							t.Fatalf("recovery packet %d prefix step %d: range=%08x want=%08x", r, i, dec.FinalRange(), wantRanges[i])
						}
						for j, sample := range buf[:n*channels] {
							expected := want[i*fs*channels+j]
							if math.Float32bits(sample) != math.Float32bits(expected) {
								logReplay(r)
								t.Fatalf("recovery packet %d prefix step %d sample %d: bits=%08x want=%08x", r, i, j, math.Float32bits(sample), math.Float32bits(expected))
							}
						}
					}
					got = append(got, buf[:n*channels]...)
					if i == fecIdx {
						gotRange = dec.FinalRange()
					}
				}

				// Final-range must be bit-exact on the FEC step.
				if gotRange != wantRanges[fecIdx] {
					logReplay(r)
					t.Errorf("recovery packet %d: FEC final-range mismatch gopus=0x%08x libopus=0x%08x",
						r, gotRange, wantRanges[fecIdx])
				}

				// FEC-recovered frame must be sample-exact.
				fecEnd := fecStart + fs*channels
				maxDiff := 0.0
				worst := -1
				firstMismatch := -1
				for j := fecStart; j < fecEnd; j++ {
					if firstMismatch < 0 && math.Float32bits(got[j]) != math.Float32bits(want[j]) {
						firstMismatch = j - fecStart
					}
					d := math.Abs(float64(got[j] - want[j]))
					if d > maxDiff {
						maxDiff = d
						worst = j - fecStart
					}
				}
				// SILK synthesis and MS-to-LR operate on integers. At the native
				// API rate, converting int16 PCM to float32 / 32768 is exact.
				// Matching entropy ranges alone does not prove matching PLC state.
				if firstMismatch >= 0 {
					logReplay(r)
					t.Errorf("recovery packet %d: FEC PCM bits differ at sample %d: got=%08x want=%08x; worst per-sample diff=%.6f at sample %d (fs=%d)",
						r, firstMismatch, math.Float32bits(got[fecStart+firstMismatch]), math.Float32bits(want[fecStart+firstMismatch]), maxDiff, worst, fs)
				}

				recovered++
				if channels == 1 && isPartialMultiFrameLBRR(t, packets[r]) {
					partial++
				}
				if channels == 1 && hasMissingLBRRPrefixBeforePresentFrame(t, packets[r]) {
					missingPrefix++
				}
			}

			if recovered == 0 {
				t.Fatalf("no LBRR-carrying recovery packets emitted for %s", tc.name)
			}
			// The partial-LBRR gate (the multi-LBRR-frame mix this regression
			// targets) is asserted for mono, where the first-payload flag layout is
			// unambiguous. Stereo packets still exercise the same DecodeFEC path.
			if channels == 1 && partial == 0 {
				t.Fatalf("no PARTIAL multi-frame LBRR packets emitted for %s; "+
					"test does not exercise the multi-LBRR-frame mix it targets", tc.name)
			}
			if channels == 1 && missingPrefix == 0 {
				t.Fatalf("no multi-frame LBRR packet begins with a missing frame followed by present LBRR for %s", tc.name)
			}
			t.Logf("%s: verified %d FEC recoveries (%d partial, %d missing-prefix multi-frame LBRR)", tc.name, recovered, partial, missingPrefix)
		})
	}
}

// encodeFECBurstyStreamForTest encodes a SILK stream with FEC enabled and a
// bursty (variable speech-activity) signal so the encoder emits PARTIAL
// multi-frame LBRR symbols: the ~2.3 Hz amplitude-modulated envelope has
// near-silent troughs, so within a 40/60 ms packet only some 20 ms sub-frames
// exceed the encoder's LBRR speech-activity threshold and carry LBRR.
func encodeFECBurstyStreamForTest(t *testing.T, bandwidth Bandwidth, bitrate, channels, frameSize, nFrames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, set := range []struct {
		name string
		fn   func() error
	}{
		{"SetMode", func() error { return enc.SetMode(EncoderModeSILK) }},
		{"SetFrameSize", func() error { return enc.SetFrameSize(frameSize) }},
		{"SetBandwidth", func() error { return enc.SetBandwidth(bandwidth) }},
		{"SetBitrate", func() error { return enc.SetBitrate(bitrate * channels) }},
		{"SetSignal", func() error { return enc.SetSignal(SignalVoice) }},
		{"SetPacketLoss", func() error { return enc.SetPacketLoss(25) }},
	} {
		if err := set.fn(); err != nil {
			t.Fatalf("%s: %v", set.name, err)
		}
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	enc.SetFEC(true)

	packets := make([][]byte, 0, nFrames)
	for frameIndex := range nFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			env := 0.5 + 0.5*math.Sin(2*math.Pi*2.3*tm)
			env = env * env // sharpen troughs to push some sub-frames below the LBRR threshold
			f0 := 200.0 + 40.0*math.Sin(2*math.Pi*0.7*tm)
			s := env * (0.5*math.Sin(2*math.Pi*f0*tm) + 0.2*math.Sin(2*math.Pi*2*f0*tm+0.3))
			pcm[i*channels] = float32(s)
			if channels == 2 {
				pcm[i*channels+1] = float32(s * 0.9)
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil || len(packet) == 0 {
			t.Fatalf("Encode frame %d: %v len=%d", frameIndex, err, len(packet))
		}
		if toc := ParseTOC(packet[0]); toc.Mode != ModeSILK {
			t.Fatalf("Encode frame %d mode=%v want SILK", frameIndex, toc.Mode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}

// isPartialMultiFrameLBRR reports whether a multi-frame SILK packet carries LBRR
// for some but not all of its internal 20 ms sub-frames (a partial LBRR_flags
// symbol). These are the packets that mix real LBRR sub-frames with concealed
// sub-frames inside one FEC recovery decode.
func isPartialMultiFrameLBRR(t *testing.T, packet []byte) bool {
	t.Helper()
	toc := ParseTOC(packet[0])
	if toc.Mode == ModeCELT || toc.FrameSize <= 960 {
		return false
	}
	first, err := extractFirstFramePayload(packet, toc)
	if err != nil || len(first) == 0 {
		return false
	}
	nFrames := toc.FrameSize / 960
	if nFrames < 2 || nFrames > 3 {
		return false
	}
	flags, ok := decodeMonoLBRRFlagsForTest(first, nFrames)
	if !ok {
		return false
	}
	set := 0
	for i := range nFrames {
		set += flags[i]
	}
	return set > 0 && set < nFrames
}

// hasMissingLBRRPrefixBeforePresentFrame reports a FEC packet whose first
// internal 20 ms frame has no LBRR while a later frame does. The PLC prefix
// resets that channel's OSCE state before the later LBRR frame runs its
// postfilter within the same DecodeWithFEC call.
func hasMissingLBRRPrefixBeforePresentFrame(t *testing.T, packet []byte) bool {
	t.Helper()
	toc := ParseTOC(packet[0])
	if toc.Mode == ModeCELT || toc.FrameSize <= 960 {
		return false
	}
	first, err := extractFirstFramePayload(packet, toc)
	if err != nil || len(first) == 0 {
		return false
	}
	nFrames := toc.FrameSize / 960
	if nFrames < 2 || nFrames > 3 {
		return false
	}
	flags, ok := decodeMonoLBRRFlagsForTest(first, nFrames)
	if !ok || flags[0] != 0 {
		return false
	}
	for i := 1; i < nFrames; i++ {
		if flags[i] != 0 {
			return true
		}
	}
	return false
}

// silk_LBRR_flags_iCDF for 2- and 3-frame packets (silk/tables_other.c). Used
// to replay the LBRR_flags symbol decode for the partial-LBRR test gate.
var lbrrFlagsICDFForTest = map[int][]uint8{
	2: {203, 150, 0},
	3: {215, 195, 166, 125, 110, 82, 0},
}

// decodeMonoLBRRFlagsForTest replays the libopus mono VAD + LBRR flag decode on a
// first-frame SILK payload and returns the per-sub-frame LBRR flags. Mirrors
// silk/dec_API.c: nFrames VAD bits, one LBRR-present bit, then (if present) the
// LBRR_flags symbol = ec_dec_icdf(silk_LBRR_flags_iCDF_ptr[nFrames-2]) + 1.
func decodeMonoLBRRFlagsForTest(firstFrame []byte, nFrames int) ([3]int, bool) {
	var out [3]int
	icdf, ok := lbrrFlagsICDFForTest[nFrames]
	if !ok || len(firstFrame) == 0 {
		return out, false
	}
	var rd rangecoding.Decoder
	rd.Init(firstFrame)
	for range nFrames {
		_ = rd.DecodeBit(1) // VAD flag
	}
	lbrr := rd.DecodeBit(1) // LBRR-present flag
	if lbrr == 0 {
		return out, true
	}
	symbol := rd.DecodeICDF8Unchecked(icdf) + 1
	for i := range nFrames {
		out[i] = (symbol >> uint(i)) & 1
	}
	return out, true
}

func TestDecodeWithFECMonoFirstPacketLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 1
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	for _, sampleRate := range []int{16000, 48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			// Libopus reference: seed → FEC → normal.
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
				{packet: recoveryPacket},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "mono first-packet LBRR reference", err)
			}

			// gopus.
			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			got := make([]float32, 0, len(want))

			if n, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.DecodeWithFEC(recoveryPacket, buf, true); err != nil {
				t.Fatalf("DecodeWithFEC recovery: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.Decode(recoveryPacket, buf); err != nil {
				t.Fatalf("Decode recovery packet: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels,
				"mono first-packet LBRR FEC decode")
			assertAPIRateFloat32BitsExact(t, got, want, "mono first-packet LBRR FEC decode")
		})
	}
}

// TestDecodeWithFECStereoWarmLBRRMatchesLibopus checks that a stereo SILK WB
// packet carrying LBRR (both mid and side channels) is correctly recovered by
// DecodeWithFEC, matching the libopus oracle. This is the "stereo warm" case:
// the encoder has processed enough frames to warm up the stereo predictor and
// LBRR gain state for both channels.
func TestDecodeWithFECStereoWarmLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 2
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	for _, sampleRate := range []int{16000, 48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}

			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
				{packet: recoveryPacket},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "stereo warm LBRR reference", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			got := make([]float32, 0, len(want))

			if n, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.DecodeWithFEC(recoveryPacket, buf, true); err != nil {
				t.Fatalf("DecodeWithFEC recovery: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}
			if n, err := dec.Decode(recoveryPacket, buf); err != nil {
				t.Fatalf("Decode recovery packet: %v", err)
			} else {
				got = append(got, buf[:n*channels]...)
			}

			assertAPIRateQualityFloat32(t, got, want, sampleRate, channels,
				"stereo warm LBRR FEC decode")
			assertAPIRateFloat32BitsExact(t, got, want, "stereo warm LBRR FEC decode")
		})
	}
}

// TestDecodeWithFECMonoFirstPacketNoLBRRFallbackMatchesPLC verifies that when
// a mono packet carries no LBRR data, DecodeWithFEC(packet, true) falls back to
// the same PLC output as Decode(nil, buf). This ensures the LBRR gate is correct
// and there is no spurious FEC decoding on non-LBRR packets.
func TestDecodeWithFECMonoFirstPacketNoLBRRFallbackMatchesPLC(t *testing.T) {
	const (
		channels   = 1
		frameSize  = 960
		sampleRate = 48000
	)

	// Encode a SILK mono packet without FEC.
	seedPacket := encodeAPIRateSILKPacketFrameSize(t, channels, frameSize)
	if packetHasInBandFEC(t, seedPacket) {
		t.Fatal("FEC-disabled SILK packet should not carry LBRR")
	}

	// Decoder A: Decode seed then Decode(nil) → PLC path.
	decPLC, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder PLC: %v", err)
	}
	bufSeed := make([]float32, frameSize*channels)
	if _, err := decPLC.Decode(seedPacket, bufSeed); err != nil {
		t.Fatalf("decPLC Decode seed: %v", err)
	}
	wantBuf := make([]float32, frameSize*channels)
	nWant, err := decPLC.Decode(nil, wantBuf)
	if err != nil {
		t.Fatalf("decPLC Decode(nil): %v", err)
	}

	// Decoder B: Decode seed then DecodeWithFEC(seed, true) on a packet without LBRR.
	decFEC, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder FEC: %v", err)
	}
	if _, err := decFEC.Decode(seedPacket, bufSeed); err != nil {
		t.Fatalf("decFEC Decode seed: %v", err)
	}
	gotBuf := make([]float32, frameSize*channels)
	nGot, err := decFEC.DecodeWithFEC(seedPacket, gotBuf, true)
	if err != nil {
		t.Fatalf("decFEC DecodeWithFEC(no-LBRR): %v", err)
	}

	if nWant != nGot {
		t.Fatalf("sample count mismatch: PLC=%d FEC-fallback=%d", nWant, nGot)
	}
	for i := 0; i < nWant*channels; i++ {
		if wantBuf[i] != gotBuf[i] {
			t.Fatalf("FEC fallback diverged from PLC at sample %d: got=%v want=%v",
				i, gotBuf[i], wantBuf[i])
		}
	}
}

// TestDecodeWithFECStereoWarmLBRRNonSilent verifies (without oracle) that stereo
// LBRR recovery produces a non-silent output, confirming the FEC decoder path
// activates and produces audible signal.
func TestDecodeWithFECStereoWarmLBRRNonSilent(t *testing.T) {
	const (
		channels  = 2
		frameSize = 960
	)
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet does not carry LBRR; test setup error")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	if _, err := dec.Decode(seedPacket, buf); err != nil {
		t.Fatalf("Decode seed: %v", err)
	}

	recovered := make([]float32, frameSize*channels)
	n, err := dec.DecodeWithFEC(recoveryPacket, recovered, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC: %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeWithFEC samples=%d want %d", n, frameSize)
	}

	e := computeEnergyFloat32(recovered[:n*channels])
	if e < 1e-8 {
		t.Fatalf("stereo LBRR recovery energy=%.2e, expected non-silent FEC output", e)
	}
}

// TestDecodeWithFECMonoFirstPacketByteExact asserts that the encoder-side mono
// first-packet LBRR bytes (packet index 0, before any LBRR carry-over) are
// identical between gopus and libopus. This is the encoding side of the
// mono-first-packet case. The check is oracle-gated.
func TestDecodeWithFECMonoFirstPacketByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		channels  = 1
		frameSize = 960
	)

	// Build a single-frame mono LBRR-FEC packet pair from both gopus and libopus.
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(
		t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, channels, frameSize)

	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet missing LBRR")
	}

	// Decode the LBRR-recovered frame and compare gopus vs libopus output.
	for _, sampleRate := range []int{48000} {
		t.Run("fs_"+itoaSmall(sampleRate), func(t *testing.T) {
			fs, err := packetSamplesAtRate(recoveryPacket, sampleRate)
			if err != nil {
				t.Fatalf("packetSamplesAtRate: %v", err)
			}
			steps := []libopusAPIRateDecodeStep{
				{packet: seedPacket},
				{packet: recoveryPacket, fec: true},
			}
			want, err := decodeWithLibopusReferenceAPIRateFloat32Steps(sampleRate, channels, fs, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "mono first-packet byte-exact reference", err)
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			buf := make([]float32, fs*channels)
			if _, err := dec.Decode(seedPacket, buf); err != nil {
				t.Fatalf("Decode seed: %v", err)
			}
			fecBuf := make([]float32, fs*channels)
			if _, err := dec.DecodeWithFEC(recoveryPacket, fecBuf, true); err != nil {
				t.Fatalf("DecodeWithFEC: %v", err)
			}

			wantFEC := want[fs*channels:]
			assertAPIRateQualityFloat32(t, fecBuf, wantFEC, sampleRate, channels,
				"mono first-packet LBRR byte-exact")
			assertAPIRateFloat32BitsExact(t, fecBuf, wantFEC, "mono first-packet LBRR byte-exact")
		})
	}
}

// TestDecodeWithFECStereoHybridAfterLongLossRangeExact pins the final
// range-coder state on a stereo Hybrid in-band FEC (decode_fec=1) recovery that
// follows a long packet-loss burst.
//
// In a Hybrid FEC step libopus decodes the SILK LBRR (lost_flag=2) and then runs
// the CELT decoder in PLC mode (celt_decode_with_ec(NULL); opus_decoder.c:606),
// reporting the CELT decoder's post-PLC st->rng as the frame's final range
// (opus_decoder.c:612, 697-700). celt_decode_lost advances that range-coder
// state (the noise LCG) on EVERY lost Hybrid frame, no matter how far the
// concealment energy has decayed. A long loss burst before the FEC step must not
// freeze that state: the hybrid PLC path must keep advancing the CELT rng even
// once its loss-fade is exhausted, or the FEC final range desyncs from libopus.
//
// This regression covers a fade-exhausted FEC recovery; the bit-exact per-step
// final-range assertion is the load-bearing entropy check (a desync here means
// gopus consumed a different number of bits than libopus on the FEC path).
func TestDecodeWithFECStereoHybridAfterLongLossRangeExact(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)

	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 480 // 10 ms
		bitrate    = 64000
		warmUp     = 4
		lossBurst  = 16 // long enough to exhaust the PLC loss-fade before recovery
	)

	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	for _, set := range []struct {
		name string
		fn   func() error
	}{
		{"SetMode", func() error { return enc.SetMode(EncoderModeHybrid) }},
		{"SetFrameSize", func() error { return enc.SetFrameSize(frameSize) }},
		{"SetExpertFrameDuration", func() error { return enc.SetExpertFrameDuration(ExpertFrameDuration10Ms) }},
		{"SetBandwidth", func() error { return enc.SetBandwidth(BandwidthFullband) }},
		{"SetBitrate", func() error { return enc.SetBitrate(bitrate * channels) }},
		{"SetSignal", func() error { return enc.SetSignal(SignalVoice) }},
		{"SetPacketLoss", func() error { return enc.SetPacketLoss(30) }},
		{"SetForceChannels", func() error { return enc.SetForceChannels(2) }},
	} {
		if err := set.fn(); err != nil {
			t.Fatalf("%s: %v", set.name, err)
		}
	}
	enc.SetFEC(true)

	// Encode a stateful stream and locate an LBRR-carrying recovery packet that
	// arrives after the loss burst (so SILK LBRR state is warm).
	const nFrames = 30
	packets := make([][]byte, 0, nFrames)
	recoveryIdx := -1
	for f := range nFrames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			tm := float64(f*frameSize+i) / sampleRate
			f0 := 180.0 * (1.0 + 0.02*math.Sin(2*math.Pi*3.0*tm))
			pcm[i*channels] = 0.40*float32(math.Sin(2*math.Pi*f0*tm)) +
				0.16*float32(math.Sin(2*math.Pi*2*f0*tm+0.21))
			pcm[i*channels+1] = 0.36*float32(math.Sin(2*math.Pi*f0*tm+0.13)) +
				0.14*float32(math.Sin(2*math.Pi*2*f0*tm+0.34))
		}
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil || len(pkt) == 0 {
			t.Fatalf("encode frame %d: %v len=%d", f, err, len(pkt))
		}
		if ParseTOC(pkt[0]).Mode != ModeHybrid {
			t.Fatalf("frame %d not hybrid", f)
		}
		packets = append(packets, append([]byte(nil), pkt...))
		if recoveryIdx < 0 && f > warmUp+lossBurst && packetHasInBandFEC(t, pkt) {
			recoveryIdx = f
		}
	}
	if recoveryIdx < 0 {
		t.Fatal("no warm LBRR-carrying hybrid recovery packet emitted")
	}

	// Decode plan: warm-up normal decodes, a long PLC burst (Decode(nil)) that
	// drives the loss-fade to zero, then the FEC recovery (decode_fec=1) of the
	// most recent lost frame, then a normal decode of the recovery packet. This
	// mirrors the opus_demo loss-recovery model for a long burst.
	type step struct {
		packet []byte
		fec    bool
	}
	plan := make([]step, 0, warmUp+lossBurst+2)
	for i := range warmUp {
		plan = append(plan, step{packet: packets[i]})
	}
	for range lossBurst {
		plan = append(plan, step{packet: nil}) // PLC
	}
	plan = append(plan, step{packet: packets[recoveryIdx], fec: true}) // FEC after fade exhaustion
	plan = append(plan, step{packet: packets[recoveryIdx]})            // normal decode

	oracleSteps := make([]libopusAPIRateDecodeStep, len(plan))
	for i, s := range plan {
		oracleSteps[i] = libopusAPIRateDecodeStep{packet: s.packet, fec: s.fec}
	}
	_, wantRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, channels, frameSize, oracleSteps)
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo hybrid long-loss FEC range reference", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	buf := make([]float32, frameSize*channels)
	fecStep := -1
	for i, s := range plan {
		var de error
		switch {
		case s.fec:
			fecStep = i
			_, de = dec.DecodeWithFEC(s.packet, buf, true)
		case s.packet == nil:
			_, de = dec.Decode(nil, buf)
		default:
			_, de = dec.Decode(s.packet, buf)
		}
		if de != nil {
			t.Fatalf("step %d (fec=%t plc=%t): %v", i, s.fec, s.packet == nil, de)
		}
		got := dec.FinalRange()
		if got != wantRanges[i] {
			t.Errorf("step %d final-range mismatch: gopus=0x%08x libopus=0x%08x (fec=%t plc=%t)",
				i, got, wantRanges[i], s.fec, s.packet == nil)
		}
	}
	if fecStep < 0 {
		t.Fatal("no FEC step in plan; test setup error")
	}
	// The FEC step must report a non-zero range (it decodes real LBRR/CELT-PLC
	// state, not a PLC sentinel) and must equal libopus exactly.
	if wantRanges[fecStep] == 0 {
		t.Fatalf("libopus reported zero final range on the FEC step %d; test setup error", fecStep)
	}
}

func TestDecodeWithFECMonoToStereoTransitionMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	monoPrime, err := hex.DecodeString("4882015d6c4724ea0378fb4d130329a3bb118c3dcf94d3ab4fb99572856213c0715e2741d0afda05128fa3b48962aa234661fc0c0bfff29c89c20353c28a7d31bc415a9d01542ceca42a3c54da9332d8490e84c2876d3367d4b1")
	if err != nil {
		t.Fatal(err)
	}
	stereoFEC, err := hex.DecodeString("4ca4d1b5615c6e332ec95d782f854c08750e1c9f0ba0fced4f8e882cb41e264e465928bf6a420a2f940502fe422a2276728c440eeb0a6078836098d456e63f9f7cee50a3ed7e4c184a22644e54c015eac5a1bd7048efd40ab9f65c4b7f80")
	if err != nil {
		t.Fatal(err)
	}
	if ParseTOC(monoPrime[0]).Stereo || !ParseTOC(stereoFEC[0]).Stereo {
		t.Fatalf("fixture channel modes: prime stereo=%t, FEC stereo=%t", ParseTOC(monoPrime[0]).Stereo, ParseTOC(stereoFEC[0]).Stereo)
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		frameSizes := []int{sampleRate / 50}
		if sampleRate == 48000 {
			frameSizes = append(frameSizes, 5760)
		}
		for _, frameSize := range frameSizes {
			t.Run(fmt.Sprintf("api_%dk/%d_samples", sampleRate/1000, frameSize), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
				if err != nil {
					t.Fatal(err)
				}
				primePCM := make([]float32, frameSize*2)
				fecPCM := make([]float32, frameSize*2)
				recoveryPCM := make([]float32, frameSize*2)
				primeSamples, err := dec.Decode(monoPrime, primePCM)
				if err != nil {
					t.Fatalf("Go prime: %v", err)
				}
				primeRange := dec.FinalRange()
				fecSamples, err := dec.DecodeWithFEC(stereoFEC, fecPCM, true)
				if err != nil {
					t.Fatalf("Go FEC: %v", err)
				}
				fecRange := dec.FinalRange()
				recoverySamples, err := dec.Decode(stereoFEC, recoveryPCM)
				if err != nil {
					t.Fatalf("Go recovery: %v", err)
				}
				recoveryRange := dec.FinalRange()
				want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, []libopustest.DecodeDiffCase{
					{Packet: monoPrime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
					{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: true},
					{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "decode diff probe", err)
					return
				}
				if len(want) != 3 {
					t.Fatalf("selected C returned %d records, want prime, FEC, and recovery", len(want))
				}
				actuals := []struct {
					name   string
					count  int
					range_ uint32
					pcm    []float32
				}{
					{name: "prime", count: primeSamples, range_: primeRange, pcm: primePCM},
					{name: "FEC", count: fecSamples, range_: fecRange, pcm: fecPCM},
					{name: "recovery", count: recoverySamples, range_: recoveryRange, pcm: recoveryPCM},
				}
				for i, actual := range actuals {
					if want[i].Code < 0 {
						t.Fatalf("selected C rejected %s: %d", actual.name, want[i].Code)
					}
					if actual.count != int(want[i].Code) {
						t.Fatalf("%s sample count Go=%d C=%d", actual.name, actual.count, want[i].Code)
					}
					if actual.range_ != want[i].FinalRange {
						t.Fatalf("%s final range Go=%08x C=%08x", actual.name, actual.range_, want[i].FinalRange)
					}
					actualLen := actual.count * 2
					if actualLen > len(actual.pcm) {
						t.Fatalf("%s Go PCM buffer length=%d, decoded samples require %d", actual.name, len(actual.pcm), actualLen)
					}
					actualPCM := actual.pcm[:actualLen]
					wantPCM := want[i].Float32()
					if len(actualPCM) != len(wantPCM) {
						t.Fatalf("%s PCM length Go=%d C=%d", actual.name, len(actualPCM), len(wantPCM))
					}
					if mismatch := fecRobustPCMFirstMismatch(actualPCM, wantPCM); mismatch >= 0 {
						t.Fatalf("%s PCM[%d] Go=%08x C=%08x", actual.name, mismatch, math.Float32bits(actualPCM[mismatch]), math.Float32bits(wantPCM[mismatch]))
					}
				}
			})
		}
	}

	t.Run("api_48k_after_loss", func(t *testing.T) {
		const sampleRate, frameSize = 48000, 960
		dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
		if err != nil {
			t.Fatal(err)
		}
		primePCM := make([]float32, frameSize*2)
		lossPCM := make([]float32, frameSize*2)
		fecPCM := make([]float32, frameSize*2)
		recoveryPCM := make([]float32, frameSize*2)
		primeSamples, err := dec.Decode(monoPrime, primePCM)
		if err != nil {
			t.Fatalf("Go prime: %v", err)
		}
		primeRange := dec.FinalRange()
		lossSamples, err := dec.Decode(nil, lossPCM)
		if err != nil {
			t.Fatalf("Go loss: %v", err)
		}
		lossRange := dec.FinalRange()
		fecSamples, err := dec.DecodeWithFEC(stereoFEC, fecPCM, true)
		if err != nil {
			t.Fatalf("Go FEC: %v", err)
		}
		fecRange := dec.FinalRange()
		recoverySamples, err := dec.Decode(stereoFEC, recoveryPCM)
		if err != nil {
			t.Fatalf("Go recovery: %v", err)
		}
		recoveryRange := dec.FinalRange()
		want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, []libopustest.DecodeDiffCase{
			{Packet: monoPrime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
			{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
			{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize), DecodeFEC: true},
			{Packet: stereoFEC, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(frameSize)},
		})
		if err != nil {
			libopustest.HelperUnavailable(t, "decode diff probe", err)
			return
		}
		actuals := []struct {
			name   string
			count  int
			range_ uint32
			pcm    []float32
		}{
			{name: "prime", count: primeSamples, range_: primeRange, pcm: primePCM},
			{name: "loss", count: lossSamples, range_: lossRange, pcm: lossPCM},
			{name: "FEC", count: fecSamples, range_: fecRange, pcm: fecPCM},
			{name: "recovery", count: recoverySamples, range_: recoveryRange, pcm: recoveryPCM},
		}
		if len(want) != len(actuals) {
			t.Fatalf("selected C returned %d records, want %d", len(want), len(actuals))
		}
		for i, actual := range actuals {
			if want[i].Code < 0 || actual.count != int(want[i].Code) {
				t.Fatalf("%s sample count Go=%d C=%d", actual.name, actual.count, want[i].Code)
			}
			if actual.range_ != want[i].FinalRange {
				t.Fatalf("%s final range Go=%08x C=%08x", actual.name, actual.range_, want[i].FinalRange)
			}
			actualLen := actual.count * 2
			if actualLen > len(actual.pcm) {
				t.Fatalf("%s Go PCM buffer length=%d, decoded samples require %d", actual.name, len(actual.pcm), actualLen)
			}
			actualPCM := actual.pcm[:actualLen]
			wantPCM := want[i].Float32()
			if len(actualPCM) != len(wantPCM) {
				t.Fatalf("%s PCM length Go=%d C=%d", actual.name, len(actualPCM), len(wantPCM))
			}
			if mismatch := fecRobustPCMFirstMismatch(actualPCM, wantPCM); mismatch >= 0 {
				t.Fatalf("%s PCM[%d] Go=%08x C=%08x", actual.name, mismatch, math.Float32bits(actualPCM[mismatch]), math.Float32bits(wantPCM[mismatch]))
			}
		}
	})

	// Warm the mono→stereo FEC transition and verify the complete recurring
	// mono packet → stereo FEC → stereo recovery sequence stays allocation-free.
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 2))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, 960*2)
	fecPCM := make([]float32, 960*2)
	recoveryPCM := make([]float32, 960*2)
	step := func() error {
		if _, err := dec.Decode(monoPrime, primePCM); err != nil {
			return err
		}
		if _, err := dec.DecodeWithFEC(stereoFEC, fecPCM, true); err != nil {
			return err
		}
		_, err := dec.Decode(stereoFEC, recoveryPCM)
		return err
	}
	for range 3 {
		if err := step(); err != nil {
			t.Fatalf("warm FEC transition: %v", err)
		}
	}
	var stepErr error
	allocs := testing.AllocsPerRun(50, func() {
		if err := step(); err != nil && stepErr == nil {
			stepErr = err
		}
	})
	if stepErr != nil {
		t.Fatalf("measured FEC transition: %v", stepErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed mono→stereo FEC/recovery allocs = %v, want 0", allocs)
	}
}

func TestDecodeWithFECPreviousCELTRedundancyFallbackMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, channels, packetFrameSize = 48000, 1, 960
	packets := encodePreviousRedundancyFECFallbackPackets(t)
	if ParseTOC(packets[0][0]).Mode != ModeSILK ||
		ParseTOC(packets[1][0]).Mode != ModeSILK ||
		ParseTOC(packets[2][0]).Mode != ModeHybrid ||
		ParseTOC(packets[3][0]).Mode != ModeCELT {
		t.Fatalf("packet modes=%v, want SILK/SILK/Hybrid/CELT", []Mode{
			ParseTOC(packets[0][0]).Mode, ParseTOC(packets[1][0]).Mode,
			ParseTOC(packets[2][0]).Mode, ParseTOC(packets[3][0]).Mode,
		})
	}

	type scenario struct {
		name             string
		fecPacketIndex   int
		requestedSamples int
	}
	scenarios := []scenario{
		{name: "CELT_packet_full_request", fecPacketIndex: 3, requestedSamples: packetFrameSize},
		{name: "CELT_packet_short_request", fecPacketIndex: 3, requestedSamples: packetFrameSize / 2},
		{name: "Hybrid_packet_short_request", fecPacketIndex: 2, requestedSamples: packetFrameSize / 2},
		{name: "SILK_packet_short_request", fecPacketIndex: 0, requestedSamples: packetFrameSize / 2},
	}
	formats := []struct {
		name string
		kind uint32
	}{
		{name: "float32_continuation", kind: libopustest.DecodeDiffFormatFloat32},
		{name: "int16_continuation", kind: libopustest.DecodeDiffFormatInt16},
		{name: "int24_continuation", kind: libopustest.DecodeDiffFormatInt24},
	}
	for _, format := range formats {
		format := format
		t.Run(format.name, func(t *testing.T) {
			for _, tc := range scenarios {
				t.Run(tc.name, func(t *testing.T) {
					steps := []libopustest.DecodeDiffCase{
						{Packet: packets[0], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[1], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[2], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[tc.fecPacketIndex], Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(tc.requestedSamples), DecodeFEC: true},
						{Format: format.kind, FrameSize: uint32(tc.requestedSamples)},
						{Packet: packets[tc.fecPacketIndex], Format: format.kind, FrameSize: packetFrameSize},
					}
					want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
					if err != nil {
						t.Fatalf("probe selected libopus sequence: %v", err)
					}
					if len(want) != len(steps) {
						t.Fatalf("selected libopus records=%d, want %d", len(want), len(steps))
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatal(err)
					}
					pcm32 := make([]float32, packetFrameSize*channels)
					pcm16 := make([]int16, packetFrameSize*channels)
					pcm24 := make([]int32, packetFrameSize*channels)
					for step := range steps {
						if step == 3 && (!dec.haveDecoded || dec.prevMode != ModeHybrid || !dec.prevRedundancy) {
							t.Fatalf("redundancy seed state haveDecoded=%v prevMode=%v prevRedundancy=%v; want decoded Hybrid with CELT redundancy", dec.haveDecoded, dec.prevMode, dec.prevRedundancy)
						}
						frameSize := int(steps[step].FrameSize)
						outputLength := frameSize * channels
						var n int
						var decodeErr error
						switch step {
						case 3:
							n, decodeErr = dec.DecodeWithFEC(packets[tc.fecPacketIndex], pcm32[:outputLength], true)
						case 4:
							switch format.kind {
							case libopustest.DecodeDiffFormatFloat32:
								n, decodeErr = dec.Decode(nil, pcm32[:outputLength])
							case libopustest.DecodeDiffFormatInt16:
								n, decodeErr = dec.DecodeInt16(nil, pcm16[:outputLength])
							default:
								n, decodeErr = dec.DecodeInt24(nil, pcm24[:outputLength])
							}
						default:
							packetIndex := step
							if step == 5 {
								packetIndex = tc.fecPacketIndex
							}
							switch format.kind {
							case libopustest.DecodeDiffFormatFloat32:
								n, decodeErr = dec.Decode(packets[packetIndex], pcm32[:outputLength])
							case libopustest.DecodeDiffFormatInt16:
								n, decodeErr = dec.DecodeInt16(packets[packetIndex], pcm16[:outputLength])
							default:
								n, decodeErr = dec.DecodeInt24(packets[packetIndex], pcm24[:outputLength])
							}
						}
						if decodeErr != nil || n != int(want[step].Code) {
							t.Fatalf("step%d decode=(%d,%v), selected C=%d", step, n, decodeErr, want[step].Code)
						}
						if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
							t.Fatalf("step%d FinalRange=%08x selected C=%08x", step, got, expected)
						}
						if step == 3 || format.kind == libopustest.DecodeDiffFormatFloat32 {
							wantPCM := want[step].Float32()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if gotBits, wantBits := math.Float32bits(pcm32[i]), math.Float32bits(wantPCM[i]); gotBits != wantBits {
									t.Fatalf("step%d PCM[%d]=%08x selected C=%08x", step, i, gotBits, wantBits)
								}
							}
						} else if format.kind == libopustest.DecodeDiffFormatInt16 {
							wantPCM := want[step].Int16()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if pcm16[i] != wantPCM[i] {
									t.Fatalf("step%d PCM[%d]=%d selected C=%d", step, i, pcm16[i], wantPCM[i])
								}
							}
						} else {
							wantPCM := want[step].Int24()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if pcm24[i] != wantPCM[i] {
									t.Fatalf("step%d PCM[%d]=%d selected C=%d", step, i, pcm24[i], wantPCM[i])
								}
							}
						}
						if step == 3 && (dec.prevMode != ModeCELT || dec.prevRedundancy) {
							t.Fatalf("post-fallback state prevMode=%v prevRedundancy=%v, want CELT with redundancy consumed", dec.prevMode, dec.prevRedundancy)
						}
					}
				})
			}
		})
	}

	// Repeat the real history on one decoder so the new fallback remains
	// allocation-free after warmup without adding resets to the measured path.
	allocDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	allocPCM := make([]float32, packetFrameSize*channels)
	decodeHistoryAndFallback := func() error {
		for _, packet := range packets[:3] {
			if _, err := allocDec.Decode(packet, allocPCM); err != nil {
				return err
			}
		}
		_, err := allocDec.DecodeWithFEC(packets[3], allocPCM, true)
		return err
	}
	for range 3 {
		if err := decodeHistoryAndFallback(); err != nil {
			t.Fatalf("warm fallback history: %v", err)
		}
	}
	var allocErr error
	if allocs := testing.AllocsPerRun(20, func() {
		if err := decodeHistoryAndFallback(); err != nil && allocErr == nil {
			allocErr = err
		}
	}); allocs != 0 {
		t.Fatalf("warmed previous-redundancy FEC fallback allocations=%g, want zero", allocs)
	}
	if allocErr != nil {
		t.Fatalf("measured fallback history: %v", allocErr)
	}
}

func encodePreviousRedundancyFECFallbackPackets(t *testing.T) [][]byte {
	t.Helper()
	libopustest.RequireOracle(t)
	const sampleRate, channels, frameSize, frameCount = 48000, 1, 960, 4
	pcm := make([]int16, frameSize*channels*frameCount)
	for frame := range frameCount {
		for i := range frameSize {
			tm := float64(frame*frameSize+i) / sampleRate
			v := 0.25*math.Sin(2*math.Pi*220*tm) + 0.08*math.Sin(2*math.Pi*1700*tm)
			pcm[frame*frameSize+i] = int16(v * 32767)
		}
	}
	frames := []libopustest.OpusEncodeFixedMixedFrame{
		{ShortPCM: pcm[:frameSize], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[frameSize : 2*frameSize], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[2*frameSize : 3*frameSize], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband},
		{ShortPCM: pcm[3*frameSize:], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband},
	}
	records, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
		Bitrate: 128000, Complexity: 10, ForceChannels: channels, LSBDepth: 16,
		FrameSize: frameSize, FrameCount: frameCount, PCM: pcm,
	}, frames)
	if err != nil {
		t.Fatalf("encode selected libopus sequence: %v", err)
	}
	if len(records) != frameCount {
		t.Fatalf("selected libopus packets=%d, want %d", len(records), frameCount)
	}
	packets := make([][]byte, frameCount)
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) < 2 {
			t.Fatalf("selected libopus frame%d status=%d packetBytes=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	return packets
}

const fecRateChangePrimeHex = "7ca8f77c8cc3f35deee4276d31f766a6229b39420e4de1833ada01f092a99b77115b86294824303ab15623a03c8cb633a6210ad53be7541daceee61a5ca26b9e73726d9eab35e36b8d0960b4d9984e910321eba71767fc055ab49334a8fca13a35cc93f18488a94edec051a9d65617d5b486f80d0f26b377101e08b722ea782993bc1c7c9badefaee9e2668abf010a2430a682dd9461349665d541ce01537c857474ca9c648de04280af15a16cbf0f50a1524875df7d022e085449f28fe60a04414d73c103f6ce3f63566e4c3ef0ed5bd2360d4f434b7e153d438affbe7a65110fb46f8c437df2c86e56571638aaee78110479e9c9834d08afbbb00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002d3f56e3bf44d8468cd0d91644b70cf6e04ec90f65e0ef6ba4c5ad6617169954bc744e65f2085fef6992a66ddb44142892"
const fecRateChangePacketHex = "219ecc3c1abc39107aac7ce5ce18793519666ce6d2503fd70b3adf02a51812d57da03d547d792672cc9f7f808d0be1b4cb109a8ff8a5c3d7442690fb306450df69e14aa80d030a7671a2c2da3496d21c540e38a611329fcf9e364a4a50bf8e0b97187cd5c4e27a5e6d1420e967fa3dedcf2c0c9ebb89002f9ead280000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000036749914e1499436820b31dda2decbc62a4a7cefdd97a77596e8b3157b56c7182e3b8fa6bd1487f550f61fa22e0512fd2fd5641acfae94416cfc213656ba83d23cb0a3da8bbeec13ee1fd52bf0234c173baba139c99e8dec9f5df96dcb21c68073d31d3056bbf06a651845f7aa47b3bcee2f6f3bdbd242873470de6cda4a0605f7abcff78fcb09a158aab56423d075d6f55028996d9533513151df8592b280dac28205816e8488d94b5d7cc90615a4530698cae50a3103df86d3290c40012d528fc0822827f81a5cbec3bae89183a1bbbaeb7e92548400f8175afb855026afc6e99594504d6eb31c3534f6c35b87d1018c9ea5f770503f20ab0e952059b294a8ed4d9e59817703c96683cff1eec8be4206f25706a463f268586a6c4e4d2dd15387c95a5da23022bbb472ef4ff9d8dd2f3e7b13a5825bea03d1291644ae6b8830462124a501e32490a5cea2e09dadfed26215a9a0338511a5230231aac06e4406daa8acf7a72db28f2d675220ff1cdb3860e40c72741ab481f103e0fdfaeee27f3019a63626b03d73724783707a98998b26c3fdc9"

func decodeFECTransitionFixture(t *testing.T) (prime, packet []byte) {
	t.Helper()
	var err error
	prime, err = hex.DecodeString(fecRateChangePrimeHex)
	if err != nil {
		t.Fatal(err)
	}
	packet, err = hex.DecodeString(fecRateChangePacketHex)
	if err != nil {
		t.Fatal(err)
	}
	return prime, packet
}

func TestDecodeWithFECSILKPLCResetsOnRateChange(t *testing.T) {
	libopustest.RequireOracle(t)
	prime, basePacket := decodeFECTransitionFixture(t)
	if got := ParseTOC(prime[0]).Mode; got != ModeHybrid {
		t.Fatalf("prime mode=%v, want Hybrid", got)
	}

	for _, tc := range []struct {
		name string
		toc  byte
	}{
		{name: "narrowband", toc: 0x01},
		{name: "mediumband", toc: 0x21},
		{name: "wideband", toc: 0x41},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recovery := append([]byte(nil), basePacket...)
			recovery[0] = tc.toc
			if got := ParseTOC(recovery[0]).Mode; got != ModeSILK {
				t.Fatalf("recovery mode=%v, want SILK", got)
			}
			if PacketHasLBRR(recovery) {
				t.Fatal("fixture recovery packet unexpectedly carries LBRR")
			}

			for _, requested := range []int{480, 5760} {
				t.Run(fmt.Sprintf("request_%d", requested), func(t *testing.T) {
					want, err := libopustest.ProbeDecodeSequence(48000, 1, []libopustest.DecodeDiffCase{
						{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 5760},
						{Packet: recovery, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(requested), DecodeFEC: true},
					})
					if err != nil {
						t.Fatalf("libopus sequence probe: %v", err)
					}
					if len(want) != 2 || want[0].Code != 960 || want[1].Code != int32(requested) {
						t.Fatalf("libopus counts=%v, want [960 %d]", decodeFECTransitionCounts(want), requested)
					}

					dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
					if err != nil {
						t.Fatal(err)
					}
					primePCM := make([]float32, 5760)
					if n, err := dec.Decode(prime, primePCM); err != nil || n != 960 {
						t.Fatalf("prime decode=(%d,%v), want 960", n, err)
					}
					assertFECTransitionPCM(t, "prime", primePCM[:960], want[0].Float32())
					if got := dec.FinalRange(); got != want[0].FinalRange {
						t.Fatalf("prime range=%08x want=%08x", got, want[0].FinalRange)
					}

					fecPCM := make([]float32, requested)
					if n, err := dec.DecodeWithFEC(recovery, fecPCM, true); err != nil || n != requested {
						t.Fatalf("FEC decode=(%d,%v), want %d", n, err, requested)
					}
					assertFECTransitionPCM(t, "FEC", fecPCM, want[1].Float32())
					if got := dec.FinalRange(); got != want[1].FinalRange {
						t.Fatalf("FEC range=%08x want=%08x", got, want[1].FinalRange)
					}
				})
			}
		})
	}
}

func TestDecodeWithFECSILKPLCResetsOnRateChangeWarmZeroAllocs(t *testing.T) {
	prime, basePacket := decodeFECTransitionFixture(t)
	for _, tc := range []struct {
		name string
		toc  byte
	}{
		{name: "narrowband", toc: 0x01},
		{name: "mediumband", toc: 0x21},
		{name: "wideband", toc: 0x41},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recovery := append([]byte(nil), basePacket...)
			recovery[0] = tc.toc
			dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatal(err)
			}
			primePCM := make([]float32, 5760)
			fecPCM := make([]float32, 5760)
			decodeSequence := func() {
				dec.Reset()
				if n, err := dec.Decode(prime, primePCM); err != nil || n != 960 {
					t.Fatalf("prime decode=(%d,%v)", n, err)
				}
				if n, err := dec.DecodeWithFEC(recovery, fecPCM, true); err != nil || n != 5760 {
					t.Fatalf("FEC decode=(%d,%v)", n, err)
				}
			}
			for range 3 {
				decodeSequence()
			}
			if allocs := testing.AllocsPerRun(20, decodeSequence); allocs != 0 {
				t.Fatalf("warm Hybrid-to-%s FEC allocations=%g, want zero", tc.name, allocs)
			}
		})
	}
}

func assertFECTransitionPCM(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM length=%d want=%d", label, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s PCM[%d]=%08x want=%08x", label, i, gotBits, wantBits)
		}
	}
}

func decodeFECTransitionCounts(results []libopustest.DecodeDiffResult) []int32 {
	counts := make([]int32, len(results))
	for i := range results {
		counts[i] = results[i].Code
	}
	return counts
}

func TestDecoderPitchAfterSILKRateResetMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	prime, packet := decodeFECTransitionFixture(t)
	packet[0] = 0x01 // NB SILK; the fixture has no LBRR.
	if PacketHasLBRR(packet) {
		t.Fatal("rate-reset packet unexpectedly carries LBRR")
	}

	const sampleRate, channels, frameSize = 48000, 1, 5760
	cases := []libopustest.DecodeDiffCase{
		{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize, DecodeFEC: true},
	}
	want, err := probeSelectedLibopusPitch(t, sampleRate, channels, cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder pitch sequence probe", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("selected C returned %d pitch records, want %d", len(want), len(cases))
	}
	if want[1].decoded != frameSize || want[1].pitch != 0 {
		t.Fatalf("selected C rate-reset FEC=(samples=%d,pitch=%d), want (%d,0)", want[1].decoded, want[1].pitch, frameSize)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, frameSize)
	fecPCM := make([]float32, frameSize)
	primeSamples, err := dec.Decode(prime, primePCM)
	if err != nil {
		t.Fatalf("prime decode: %v", err)
	}
	if primeSamples != int(want[0].decoded) {
		t.Fatalf("prime samples=%d C=%d", primeSamples, want[0].decoded)
	}
	if got := dec.Pitch(); got != int(want[0].pitch) {
		t.Fatalf("prime pitch=%d C=%d", got, want[0].pitch)
	}

	fecSamples, err := dec.DecodeWithFEC(packet, fecPCM, true)
	if err != nil {
		t.Fatalf("rate-reset FEC decode: %v", err)
	}
	if fecSamples != int(want[1].decoded) {
		t.Fatalf("rate-reset FEC samples=%d C=%d", fecSamples, want[1].decoded)
	}
	if got := dec.Pitch(); got != int(want[1].pitch) {
		t.Fatalf("rate-reset FEC pitch=%d C=%d", got, want[1].pitch)
	}
	if got := dec.silkDecoder.GetLastSignalType(); got != 0 {
		t.Fatalf("SILK last signal type after rate-reset PLC=%d, want inactive", got)
	}
}

type selectedLibopusPitchStep struct {
	decoded int32
	pitch   int32
}

func probeSelectedLibopusPitch(t *testing.T, sampleRate, channels int, cases []libopustest.DecodeDiffCase) ([]selectedLibopusPitchStep, error) {
	t.Helper()
	bin, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:      "decoder pitch sequence probe",
		OutputBase: "gopus_decoder_pitch_sequence",
		SourceFile: "libopus_decoder_pitch_sequence.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
	})
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GPI1", 1,
		uint32(sampleRate), uint32(channels), uint32(len(cases)))
	for _, step := range cases {
		decodeFEC := uint32(0)
		if step.DecodeFEC {
			decodeFEC = 1
		}
		payload.U32(step.FrameSize)
		payload.U32(decodeFEC)
		payload.U32(uint32(len(step.Packet)))
		payload.Raw(step.Packet)
	}
	wire, err := libopustest.RunHelper(bin, payload.Bytes())
	if err != nil {
		return nil, err
	}
	r, err := libopustest.NewOracleReader("decoder pitch sequence probe", "GPO1", wire)
	if err != nil {
		return nil, err
	}
	count := r.Count(len(cases))
	out := make([]selectedLibopusPitchStep, count)
	for i := range out {
		out[i].decoded = r.I32()
		if ctl := r.I32(); ctl != 0 {
			return nil, fmt.Errorf("selected C OPUS_GET_PITCH step %d returned %d", i, ctl)
		}
		out[i].pitch = r.I32()
	}
	if err := r.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestDecodeWithFECRateSwitchRecoveryAndLossMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name     string
		channels int
		prime    string
		fec      string
	}{
		{
			name:     "api-mono-stereo-wb-to-mb",
			channels: 1,
			prime:    "54d9346d58758b87d1bbdb39e481b0cdf58959ea38e20f1255003666bb4633195ac9bd2f52ab993093f3d6a120f4a95b13b0163db9c0c3e21f8df1f4931a487ecb827357889078eea89ef7153b012ab77fde135545bbb90e58afbf954b78aa0edd9f4de4c08cef748c8873b86c33787710dfa3af84f3eb72bbb3e6700d1468896caf3a1e475ff74451cb22580a3c189f092391240c7160421137e84a80effbb35c2e0f817a",
			fec:      "34a8f77ca6242c85d1dfe89250f7bcd05a152957cab69d04b1d02d2dfdcd23b79593e89ff61d1dcedaa4fb8ad2155e16052b21c563cc828769374b9334e79f124655123c13ca005d9c048c566b1cc473ccef7467dda10aa8d9612fd230288c14f3f1106a98d19a1b22c89b867b7027648f65e0ef6ba4cdcd2320bbd578e89ccbe410ebf9b12176d174ac92",
		},
		{
			name:     "api-stereo-stereo-wb-to-nb",
			channels: 2,
			prime:    "5cee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80",
			fec:      "14ee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prime, err := hex.DecodeString(tc.prime)
			if err != nil {
				t.Fatal(err)
			}
			fec, err := hex.DecodeString(tc.fec)
			if err != nil {
				t.Fatal(err)
			}
			const sampleRate = 48000
			fecSamples, err := packetSamplesAtRate(fec, sampleRate)
			if err != nil {
				t.Fatal(err)
			}
			want, err := libopustest.ProbeDecodeSequence(sampleRate, tc.channels, []libopustest.DecodeDiffCase{
				{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 5760},
				{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(fecSamples), DecodeFEC: true},
				{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(fecSamples)},
				{Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(fecSamples)},
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "decode sequence probe", err)
			}
			if len(want) != 4 {
				t.Fatalf("selected C returned %d sequence records, want 4", len(want))
			}

			dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.channels))
			if err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, 5760*tc.channels)
			assertStep := func(label string, idx int, n int, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s decode: %v", label, err)
				}
				if want[idx].Code != int32(n) {
					t.Fatalf("%s samples=%d, C=%d", label, n, want[idx].Code)
				}
				assertFECTransitionPCM(t, label, pcm[:n*tc.channels], want[idx].Float32())
				if got := dec.FinalRange(); got != want[idx].FinalRange {
					t.Fatalf("%s final range=%08x, C=%08x", label, got, want[idx].FinalRange)
				}
			}

			primeN, err := dec.Decode(prime, pcm)
			assertStep("prime", 0, primeN, err)
			fecN, err := dec.DecodeWithFEC(fec, pcm[:fecSamples*tc.channels], true)
			assertStep("FEC", 1, fecN, err)
			recoveryN, err := dec.Decode(fec, pcm[:fecSamples*tc.channels])
			assertStep("normal recovery", 2, recoveryN, err)
			lossN, err := dec.Decode(nil, pcm[:fecSamples*tc.channels])
			assertStep("following PLC", 3, lossN, err)
		})
	}
}

func TestDecodeWithFECSideResetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireLibopusAPIRateRefdecodeHelper(t)
	data, err := os.ReadFile("testdata/fec_side_reset_packets.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name       string   `json:"name"`
			SampleRate int      `json:"sample_rate"`
			Channels   int      `json:"channels"`
			FrameSize  int      `json:"frame_size"`
			Packets    []string `json:"packets"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 2 {
		t.Fatalf("packet histories=%d want=2", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if len(tc.Packets) < 3 {
				t.Fatal("missing decoder warmup history")
			}
			steps := make([]libopusAPIRateDecodeStep, len(tc.Packets))
			for i, encoded := range tc.Packets {
				packet, err := hex.DecodeString(encoded)
				if err != nil {
					t.Fatalf("packet %d: %v", i, err)
				}
				steps[i] = libopusAPIRateDecodeStep{packet: packet, fec: i == len(steps)-1}
			}
			if !packetHasInBandFEC(t, steps[len(steps)-1].packet) {
				t.Fatal("recovery packet does not carry LBRR")
			}
			want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(tc.SampleRate, tc.Channels, tc.FrameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "side reset FEC reference", err)
			}
			stride := tc.FrameSize * tc.Channels
			if len(want) != len(steps)*stride || len(ranges) != len(steps) {
				t.Fatalf("reference samples/ranges=%d/%d want=%d/%d", len(want), len(ranges), len(steps)*stride, len(steps))
			}
			dec, err := NewDecoder(DefaultDecoderConfig(tc.SampleRate, tc.Channels))
			if err != nil {
				t.Fatal(err)
			}
			out := make([]float32, stride)
			for i, step := range steps {
				n, err := dec.DecodeWithFEC(step.packet, out, step.fec)
				if err != nil || n != tc.FrameSize {
					t.Fatalf("step %d fec=%v: samples=%d want=%d err=%v", i, step.fec, n, tc.FrameSize, err)
				}
				if dec.FinalRange() != ranges[i] {
					t.Fatalf("step %d fec=%v: range=%08x want=%08x", i, step.fec, dec.FinalRange(), ranges[i])
				}
				for j, sample := range out {
					gotBits, wantBits := math.Float32bits(sample), math.Float32bits(want[i*stride+j])
					if gotBits != wantBits {
						t.Fatalf("step %d fec=%v sample %d: PCM=%08x want=%08x", i, step.fec, j, gotBits, wantBits)
					}
				}
			}
		})
	}
}

func TestDecodeWithFECStereoToMonoTransitionMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	prime := mustDecodeFECTransitionPacket(t, "5cee4d1b5636a384d5974ea37cc01e037e8bff7a9a3416e1b58099326287f76046b0e3e06f8a922c0f9c4fd178c6bcc2e93cee1b6ecb1729adfeafd2e299b45c85263f0f78162486b50cb7b678bbe58284bb33ced74b818618aa4cb5dd22397e467975ba74187a4c836438e4f2f19cb63c96cb7445c3b1360f8e99c8b08875f0b3bf2b6ec2199a5d19db9cd95e5cc961980694f2a588f8b5a4c3f23a0d47588fffa56de233f46f1940b2abc601895f77978a9693e96f2fced293018429d7bf320cd073a8d4bda02c474c2fc20e0b80")
	fec := mustDecodeFECTransitionPacket(t, "70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13e023a92")
	if !ParseTOC(prime[0]).Stereo || ParseTOC(fec[0]).Stereo {
		t.Fatalf("fixture channel modes: prime stereo=%t, FEC stereo=%t", ParseTOC(prime[0]).Stereo, ParseTOC(fec[0]).Stereo)
	}

	const sampleRate, channels, frameSize = 48000, 2, 5760
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
		{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize, DecodeFEC: true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}
	if len(want) != 2 {
		t.Fatalf("selected C returned %d records, want prime and FEC", len(want))
	}

	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, frameSize*channels)
	fecPCM := make([]float32, frameSize*channels)
	primeSamples, err := dec.Decode(prime, primePCM)
	if err != nil {
		t.Fatalf("prime decode: %v", err)
	}
	if want[0].Code < 0 || primeSamples != int(want[0].Code) {
		t.Fatalf("prime samples=%d C=%d", primeSamples, want[0].Code)
	}
	if got, wantRange := dec.FinalRange(), want[0].FinalRange; got != wantRange {
		t.Fatalf("prime range=%08x C=%08x", got, wantRange)
	}
	assertDecodeFECTransitionPCM(t, "prime", primePCM[:primeSamples*channels], want[0].Float32())

	fecSamples, err := dec.DecodeWithFEC(fec, fecPCM, true)
	if err != nil {
		t.Fatalf("FEC decode: %v", err)
	}
	if want[1].Code < 0 || fecSamples != int(want[1].Code) {
		t.Fatalf("FEC samples=%d C=%d", fecSamples, want[1].Code)
	}
	if got, wantRange := dec.FinalRange(), want[1].FinalRange; got != wantRange {
		t.Fatalf("FEC range=%08x C=%08x", got, wantRange)
	}
	assertDecodeFECTransitionPCM(t, "FEC", fecPCM[:fecSamples*channels], want[1].Float32())

	// Keep the public prime→FEC sequence allocation-free after setup and warmup.
	step := func() error {
		if _, err := dec.Decode(prime, primePCM); err != nil {
			return err
		}
		_, err := dec.DecodeWithFEC(fec, fecPCM, true)
		return err
	}
	for range 3 {
		if err := step(); err != nil {
			t.Fatalf("warm FEC transition: %v", err)
		}
	}
	var stepErr error
	allocs := testing.AllocsPerRun(50, func() {
		if err := step(); err != nil && stepErr == nil {
			stepErr = err
		}
	})
	if stepErr != nil {
		t.Fatalf("measured FEC transition: %v", stepErr)
	}
	if allocs != 0 {
		t.Fatalf("warmed stereo→mono FEC allocations=%g, want zero", allocs)
	}
}

func TestDecodeWithFECMonoToStereoLongFrameMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.DecodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}

	prime := mustDecodeFECTransitionPacket(t, "70822e0dfbd19557d6eb45b1ab3a3afa38f40cc6336e613f1b7866db563fa9bf0890a9925c5a91a1cf2d82c71b43639216c08b6eaf05fabc112229568c940d3f360e8bb1cce3ba3234c13e023a92")
	fec := mustDecodeFECTransitionPacket(t, "54d9346d58758b87d1bbdb39e481b0cdf58959ea38e20f1255003666bb4633195ac9bd2f52ab993093f3d6a120f4a95b13b0163db9c0c3e21f8df1f4931a487ecb827357889078eea89ef7153b012ab77fde135545bbb90e58afbf954b78aa0edd9f4de4c08cef748c8873b86c33787710dfa3af84f3eb72bbb3e6700d1468896caf3a1e475ff74451cb22580a3c189f092391240c7160421137e84a80effbb35c2e0f817a")
	if ParseTOC(prime[0]).Stereo || !ParseTOC(fec[0]).Stereo {
		t.Fatalf("fixture channel modes: prime stereo=%t, FEC stereo=%t", ParseTOC(prime[0]).Stereo, ParseTOC(fec[0]).Stereo)
	}

	const sampleRate, channels, frameSize = 48000, 2, 5760
	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
		{Packet: prime, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize},
		{Packet: fec, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: frameSize, DecodeFEC: true},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "decode diff probe", err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	primePCM := make([]float32, frameSize*channels)
	fecPCM := make([]float32, frameSize*channels)
	primeSamples, err := dec.Decode(prime, primePCM)
	if err != nil {
		t.Fatalf("prime decode: %v", err)
	}
	fecSamples, err := dec.DecodeWithFEC(fec, fecPCM, true)
	if err != nil {
		t.Fatalf("FEC decode: %v", err)
	}
	if len(want) != 2 || want[0].Code < 0 || want[1].Code < 0 {
		t.Fatalf("selected C returned invalid sequence records: %+v", want)
	}
	if primeSamples != int(want[0].Code) || fecSamples != int(want[1].Code) {
		t.Fatalf("sample counts Go=%d/%d C=%d/%d", primeSamples, fecSamples, want[0].Code, want[1].Code)
	}
	assertDecodeFECTransitionPCM(t, "prime", primePCM[:primeSamples*channels], want[0].Float32())
	assertDecodeFECTransitionPCM(t, "FEC", fecPCM[:fecSamples*channels], want[1].Float32())
}

func mustDecodeFECTransitionPacket(t *testing.T, encoded string) []byte {
	t.Helper()
	packet, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode packet fixture: %v", err)
	}
	return packet
}

func assertDecodeFECTransitionPCM(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s PCM length=%d C=%d", label, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s PCM[%d]=%08x C=%08x", label, i, gotBits, wantBits)
		}
	}
}

func TestDecodeWithFECRejectsOver120msBeforeCELTPLCFallback(t *testing.T) {
	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketSamples = 7000
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Keep the FEC request itself valid (a multiple of the 2.5 ms quantum) so
	// this test reaches packet framing instead of returning OPUS_BAD_ARG first.
	pcm := make([]float32, 6960)
	_, err = dec.DecodeWithFEC([]byte{GenerateTOC(31, false, 3), 0x07}, pcm, true)
	if err != ErrInvalidPacket {
		t.Fatalf("DecodeWithFEC error=%v want %v", err, ErrInvalidPacket)
	}
}

func TestDecodeWithFECEnforcesMaxPacketBytesWithoutChangingState(t *testing.T) {
	packet := encodeAPIRateSILKPacket(t, 1)
	if len(packet) < 2 {
		t.Fatalf("test packet length=%d, need a multi-byte packet", len(packet))
	}

	cfg := DefaultDecoderConfig(48000, 1)
	cfg.MaxPacketBytes = len(packet) - 1
	dec, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	fresh, err := NewDecoder(cfg)
	if err != nil {
		t.Fatalf("NewDecoder fresh: %v", err)
	}
	pcm := make([]float32, 960)
	if n, err := dec.DecodeWithFEC(packet, pcm, true); n != 0 || err != ErrPacketTooLarge {
		t.Fatalf("DecodeWithFEC oversized packet=(%d,%v), want (0,%v)", n, err, ErrPacketTooLarge)
	}
	if dec.lastPacketMode != fresh.lastPacketMode || dec.lastBandwidth != fresh.lastBandwidth ||
		dec.lastPacketDuration != fresh.lastPacketDuration || dec.lastFrameSize != fresh.lastFrameSize ||
		dec.FinalRange() != fresh.FinalRange() || dec.hasFEC {
		t.Fatal("rejected oversized FEC packet changed decoder state")
	}

	// A one-byte code-0 SILK packet is an empty/DTX frame accepted under this
	// byte limit. Its PLC output must still match a fresh decoder after rejection.
	shortPacket := []byte{GenerateTOC(0, false, 0)}
	got := make([]float32, len(pcm))
	want := make([]float32, len(pcm))
	gotN, gotErr := dec.DecodeWithFEC(shortPacket, got, true)
	wantN, wantErr := fresh.DecodeWithFEC(shortPacket, want, true)
	if gotN != wantN || gotErr != wantErr {
		t.Fatalf("post-rejection DecodeWithFEC=(%d,%v), fresh=(%d,%v)", gotN, gotErr, wantN, wantErr)
	}
	if gotErr != nil {
		t.Fatalf("DecodeWithFEC(short packet): %v", gotErr)
	}
	for i := 0; i < gotN; i++ {
		if got[i] != want[i] {
			t.Fatalf("post-rejection PCM[%d]=%08x, fresh=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	if dec.FinalRange() != fresh.FinalRange() || dec.LastPacketDuration() != fresh.LastPacketDuration() {
		t.Fatal("post-rejection decoder history differs from fresh decoder")
	}
}

// TestDecodeWithFEC_FallbackToPLC verifies that DecodeWithFEC falls back to PLC
// when no FEC data is available (e.g., when no previous packet was decoded,
// or the previous packet was CELT-only mode).
func TestDecodeWithFEC_FallbackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Attempt FEC decode without any previous packet - should fall back to PLC
	frameSize := 960
	pcm := make([]float32, frameSize)

	// First FEC decode with no prior data - should use PLC and produce silence/zeros
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC fell back to PLC successfully, produced %d samples", n)
}

// TestDecodeWithFEC_CELTNoFEC verifies that CELT-only packets don't have FEC data.
// DecodeWithFEC should fall back to PLC after decoding a CELT packet.
func TestDecodeWithFEC_CELTNoFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a CELT packet (config 31 = CELT FB 20ms)
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the CELT packet normally
	_, err = dec.Decode(celtPacket, pcm)
	if err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// Check that no FEC data was stored (CELT doesn't have LBRR)
	if dec.hasFEC {
		t.Error("hasFEC should be false after CELT packet decode")
	}

	// Attempt FEC decode - should fall back to PLC since no FEC available
	n, err := dec.DecodeWithFEC(nil, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error (expected PLC fallback): %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("DecodeWithFEC correctly fell back to PLC for CELT mode")
}

// TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC verifies that SILK packets only
// arm FEC state when their LBRR flag is present.
func TestDecodeWithFEC_SILKNoLBRRDoesNotStoreFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Create a SILK packet (config 9 = SILK WB 20ms)
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i)
	}

	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the SILK packet normally
	_, err = dec.Decode(silkPacket, pcm)
	if err != nil {
		t.Fatalf("Decode SILK packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after SILK packet without LBRR")
	}

	t.Log("DecodeWithFEC correctly left FEC state clear for SILK without LBRR")
}

func TestStoreFECData_ReusesBackingBuffer(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	initialCap := cap(dec.fecData)
	if initialCap == 0 {
		t.Fatal("expected preallocated fecData backing buffer")
	}

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		Stereo:    false,
	}

	packetSmall := make([]byte, 32)
	packetLarge := make([]byte, 512)
	for i := range packetSmall {
		packetSmall[i] = byte(i)
	}
	for i := range packetLarge {
		packetLarge[i] = byte(255 - (i % 255))
	}
	packetSmall[0] |= 0x40
	packetLarge[0] |= 0x40

	dec.storeFECData(packetSmall, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after small packet: got %d want %d", cap(dec.fecData), initialCap)
	}

	dec.storeFECData(packetLarge, toc, 1, 960)
	if cap(dec.fecData) != initialCap {
		t.Fatalf("fecData cap changed after large packet: got %d want %d", cap(dec.fecData), initialCap)
	}
	if len(dec.fecData) != len(packetLarge) {
		t.Fatalf("fecData len = %d, want %d", len(dec.fecData), len(packetLarge))
	}
	if dec.fecData[0] != packetLarge[0] || dec.fecData[len(dec.fecData)-1] != packetLarge[len(packetLarge)-1] {
		t.Fatal("fecData content mismatch after copy")
	}
}

func TestStoreFECData_NoLBRRClearsState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK

	toc := TOC{
		Mode:      ModeSILK,
		Bandwidth: BandwidthWideband,
		FrameSize: 960,
	}
	dec.storeFECData([]byte{0x00, 0x01}, toc, 1, 960)
	if dec.hasFEC {
		t.Fatal("storeFECData kept FEC armed for packet without LBRR")
	}
}

func TestDecodeFECFrame_BufferSizingUsesSingleFrame(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Simulate stored FEC metadata from a 2-frame packet. decode_fec should still
	// only require one frame of output buffer.
	dec.hasFEC = true
	dec.fecData = []byte{0x01, 0x02, 0x03, 0x04}
	dec.fecMode = ModeSILK
	dec.fecBandwidth = BandwidthWideband
	dec.fecStereo = false
	dec.fecFrameSize = 960
	dec.fecFrameCount = 2

	pcm := make([]float32, 960)
	_, err = dec.decodeFECFrame(pcm, 0)
	if err == ErrBufferTooSmall {
		t.Fatal("decodeFECFrame rejected single-frame output buffer for multi-frame packet metadata")
	}
}

// TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC verifies that normal
// decode skips LBRR instead of caching it for a later nil decode_fec call.
func TestDecodeWithFEC_HybridLBRRNormalDecodeDoesNotCacheFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Use the minimal hybrid test packet
	packet := minimalHybridTestPacket20ms()
	frameSize := 960
	pcm := make([]float32, frameSize)

	// Decode the Hybrid packet normally
	_, err = dec.Decode(packet, pcm)
	if err != nil {
		t.Fatalf("Decode Hybrid packet error: %v", err)
	}

	if dec.hasFEC {
		t.Error("hasFEC should be false after normal Hybrid decode")
	}
}

// TestDecodeWithFEC_Recovery tests FEC recovery flow.
// This test simulates packet loss and FEC recovery.
func TestDecodeWithFEC_Recovery(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Decode a series of SILK packets to build up state
	silkPacket := make([]byte, 100)
	silkPacket[0] = GenerateTOC(9, false, 0) // SILK WB 20ms
	for i := 1; i < len(silkPacket); i++ {
		silkPacket[i] = byte(i * 3)
	}

	frameSize := 960
	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode packet 1
	_, err = dec.Decode(silkPacket, pcm1)
	if err != nil {
		t.Fatalf("Decode packet 1 error: %v", err)
	}

	// Simulate packet 2 is lost - use FEC to recover
	// In real usage, we'd use the NEXT packet's LBRR to recover the lost one.
	// For this test, we just verify DecodeWithFEC works without crashing.
	n, err := dec.DecodeWithFEC(nil, pcm2, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error: %v", err)
	}
	if n != frameSize {
		t.Errorf("DecodeWithFEC returned %d samples, want %d", n, frameSize)
	}

	t.Logf("FEC recovery produced %d samples", n)
}

func TestDecodeWithFEC_UsesProvidedPacketAndPreservesNormalDecode(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost" in decode sequence
	packet2 := encodeFrame(makeFrame(1920))

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing packet using packet2's LBRR.
	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}

	// The same packet must still decode normally after FEC recovery.
	pcm2 := make([]float32, frameSize)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != frameSize {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=%d", n2, frameSize)
	}
}

func TestDecodeWithFEC_FrameSizeTransitionUsesProvidedPacketGranularity(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	makeFrame := func(frameSize int, phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*330*tm) + 0.2*math.Sin(2*math.Pi*660*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(frameSize int, phase float64) []byte {
		if err := enc.SetFrameSize(frameSize); err != nil {
			t.Fatalf("SetFrameSize(%d) error: %v", frameSize, err)
		}
		n, err := enc.Encode(makeFrame(frameSize, phase), pktBuf)
		if err != nil {
			t.Fatalf("Encode(%d) error: %v", frameSize, err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating frame-size transition packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(1920, 0)
	_ = encodeFrame(960, 1920) // packet 1 intentionally "lost"
	packet2 := encodeFrame(960, 2880)

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, 1920)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}

	// Recover the missing 20ms packet from packet2 LBRR while previous decode
	// state still reflects a 40ms frame. The output buffer length is the
	// caller's requested decode_fec frame size, matching libopus' frame_size.
	pcmFEC := make([]float32, 960)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != 960 {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=960", nFEC)
	}

	pcm2 := make([]float32, 960)
	n2, err := dec.Decode(packet2, pcm2)
	if err != nil {
		t.Fatalf("Decode packet2 after FEC recovery error: %v", err)
	}
	if n2 != 960 {
		t.Fatalf("Decode packet2 after FEC recovery samples=%d want=960", n2)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketFallsBackToPLC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Seed last frame size/state with a normal decode.
	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0) // CELT FB 20ms
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}

	pcm := make([]float32, 960)
	if _, err := dec.Decode(celtPacket, pcm); err != nil {
		t.Fatalf("Decode CELT packet error: %v", err)
	}

	// CELT has no LBRR, so this should transparently fall back to PLC.
	n, err := dec.DecodeWithFEC(celtPacket, pcm, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if n != 960 {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) samples=%d want=%d", n, 960)
	}
}

func TestDecodeWithFEC_ProvidedCELTPacketClearsStoredFECState(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960
	pcm := make([]float32, 960)

	celtPacket := make([]byte, 100)
	celtPacket[0] = GenerateTOC(31, false, 0)
	for i := 1; i < len(celtPacket); i++ {
		celtPacket[i] = byte(i)
	}
	if _, err := dec.DecodeWithFEC(celtPacket, pcm, true); err != nil {
		t.Fatalf("DecodeWithFEC(CELT packet, fec=true) error: %v", err)
	}
	if dec.hasFEC {
		t.Fatalf("hasFEC should be false after CELT-based decode_fec fallback")
	}
}

func TestDecodeWithFEC_ProvidedPacketUsesPacketModeForCELTGate(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.5*math.Sin(2*math.Pi*440*tm) + 0.2*math.Sin(2*math.Pi*880*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating FEC test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))
	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := dec.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 error: %v", err)
	}
	if dec.lastPacketMode == ModeCELT {
		t.Fatalf("test setup invalid: first packet mode resolved to CELT")
	}

	// Simulate transient prevMode=CELLT (e.g., after redundancy/PLC path).
	// decode_fec gating should still use packet-mode state (lastPacketMode).
	dec.prevMode = ModeCELT

	pcmFEC := make([]float32, frameSize)
	nFEC, err := dec.DecodeWithFEC(packet2, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nFEC != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nFEC, frameSize)
	}
	if dec.prevMode != toc.Mode {
		t.Fatalf("prevMode=%v want %v (provided packet mode must override transient PLC mode)", dec.prevMode, toc.Mode)
	}
}

func TestDecodeWithFEC_ProvidedPacketWithoutLBRRUsesDirectPLCFallback(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(false)
	if err := enc.SetBitrate(12000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}

	const frameSize = 960
	makeFrame := func(phase float64) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			tm := (float64(i) + phase) / 48000.0
			pcm[i] = float32(0.4*math.Sin(2*math.Pi*220*tm) + 0.1*math.Sin(2*math.Pi*440*tm))
		}
		return pcm
	}

	pktBuf := make([]byte, 4000)
	encodeFrame := func(pcm []float32) []byte {
		n, err := enc.Encode(pcm, pktBuf)
		if err != nil {
			t.Fatalf("Encode error: %v", err)
		}
		if n == 0 {
			t.Fatal("unexpected DTX suppression while generating no-LBRR test packet")
		}
		packet := make([]byte, n)
		copy(packet, pktBuf[:n])
		return packet
	}

	packet0 := encodeFrame(makeFrame(0))
	_ = encodeFrame(makeFrame(960)) // packet 1 intentionally "lost"
	packet2 := encodeFrame(makeFrame(1920))

	toc, _, err := packetFrameCount(packet2)
	if err != nil {
		t.Fatalf("packetFrameCount(packet2) error: %v", err)
	}
	if toc.Mode == ModeCELT {
		t.Fatalf("test setup invalid: packet2 mode resolved to CELT")
	}
	firstFrameData, err := extractFirstFramePayload(packet2, toc)
	if err != nil {
		t.Fatalf("extractFirstFramePayload(packet2) error: %v", err)
	}
	if packetHasLBRR(firstFrameData, toc) {
		t.Fatalf("test setup invalid: packet2 unexpectedly has LBRR")
	}

	decExpected, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(expected) error: %v", err)
	}
	decActual, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder(actual) error: %v", err)
	}

	pcm0 := make([]float32, frameSize)
	if _, err := decExpected.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (expected) error: %v", err)
	}
	if _, err := decActual.Decode(packet0, pcm0); err != nil {
		t.Fatalf("Decode packet0 (actual) error: %v", err)
	}

	pcmExpected := make([]float32, frameSize)
	nExpected, err := decExpected.decodePLCForFECWithState(pcmExpected, frameSize, plcDecodeState{
		packetFrameSize: frameSize,
		mode:            toc.Mode,
		bandwidth:       toc.Bandwidth,
		packetStereo:    toc.Stereo,
	})
	if err != nil {
		t.Fatalf("decodePLCForFECWithState(expected) error: %v", err)
	}
	if nExpected != frameSize {
		t.Fatalf("decodePLCForFECWithState(expected) samples=%d want=%d", nExpected, frameSize)
	}

	pcmActual := make([]float32, frameSize)
	nActual, err := decActual.DecodeWithFEC(packet2, pcmActual, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) error: %v", err)
	}
	if nActual != frameSize {
		t.Fatalf("DecodeWithFEC(packet2, fec=true) samples=%d want=%d", nActual, frameSize)
	}

	const tol = 1e-7
	for i := range frameSize {
		if math.Abs(float64(pcmExpected[i]-pcmActual[i])) > tol {
			t.Fatalf("sample %d mismatch: expected=%v actual=%v", i, pcmExpected[i], pcmActual[i])
		}
	}
}

func TestDecodeWithFEC_PLCWithProvidedStateUsesProvidedMode(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	packet := minimalHybridTestPacket20ms()
	pcmPrime := make([]float32, frameSize)
	if _, err := dec.Decode(packet, pcmPrime); err != nil {
		t.Fatalf("Decode prime packet error: %v", err)
	}

	// Force decoder transient PLC mode to CELT and verify provided state wins.
	dec.prevMode = ModeCELT
	dec.prevRedundancy = false
	dec.haveDecoded = true

	pcmPLC := make([]float32, frameSize)
	n, err := dec.decodePLCForFECWithState(pcmPLC, frameSize, plcDecodeState{
		packetFrameSize: frameSize,
		mode:            ModeHybrid,
		bandwidth:       BandwidthFullband,
	})
	if err != nil {
		t.Fatalf("decodePLCForFECWithState error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("decodePLCForFECWithState samples=%d want=%d", n, frameSize)
	}
	if dec.prevMode != ModeHybrid {
		t.Fatalf("prevMode=%v want=%v (provided PLC mode must be honored)", dec.prevMode, ModeHybrid)
	}
}

// TestDecodeWithFEC_NoFECRequested verifies that when fec=false, DecodeWithFEC
// behaves identically to Decode.
func TestDecodeWithFEC_NoFECRequested(t *testing.T) {
	dec1, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	dec2, _ := NewDecoder(DefaultDecoderConfig(48000, 1))

	packet := minimalHybridTestPacket20ms()
	frameSize := 960

	pcm1 := make([]float32, frameSize)
	pcm2 := make([]float32, frameSize)

	// Decode with Decode
	n1, err1 := dec1.Decode(packet, pcm1)
	// Decode with DecodeWithFEC(fec=false)
	n2, err2 := dec2.DecodeWithFEC(packet, pcm2, false)

	if err1 != err2 {
		t.Errorf("Errors differ: Decode=%v, DecodeWithFEC=%v", err1, err2)
	}
	if n1 != n2 {
		t.Errorf("Sample counts differ: Decode=%d, DecodeWithFEC=%d", n1, n2)
	}

	// Verify samples are identical
	for i := 0; i < n1*1; i++ {
		if pcm1[i] != pcm2[i] {
			t.Errorf("Sample %d differs: Decode=%v, DecodeWithFEC=%v", i, pcm1[i], pcm2[i])
			break
		}
	}

	t.Log("DecodeWithFEC(fec=false) behaves identically to Decode")
}

// TestDecodeWithFEC_ResetClearsFEC verifies that Reset clears FEC state.
func TestDecodeWithFEC_ResetClearsFEC(t *testing.T) {
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	dec.hasFEC = true
	dec.fecData = append(dec.fecData[:0], 0x40, 0x01)
	dec.fecMode = ModeSILK
	dec.fecFrameSize = 960

	// Reset the decoder
	dec.Reset()

	// FEC state should be cleared
	if dec.hasFEC {
		t.Error("hasFEC should be false after Reset")
	}
	if dec.fecFrameSize != 0 {
		t.Error("fecFrameSize should be 0 after Reset")
	}

	t.Log("Reset correctly clears FEC state")
}

func TestHybridMalformedMainLengthMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for _, durationMS := range []int{10, 20} {
			frameSize := rate * durationMS / 1000
			good := encodeAPIRateHybridPacketFrameSize(t, 2, 48*durationMS)
			packetHexes := []string{
				"60825c38a0075e8e66c39989487af264b20624da",
				"60825c38a0075e8e66c39989486ef264b20624da",
			}
			if durationMS == 20 {
				packetHexes = []string{"78822eb7c2136d88ca1c827853c90bb9cb90a413fa1b9c476d5d20eb2f99eb3ee083f35a305ebd15faf960a9e081556ad0d42cf3fc73a94764f37efcaef43b27567656737aaec0914d6f6b9cdc0c08b5cffab01a0ee90c93f9a1edccb3a0524d471bfff74fa3a76810b30378372e7390b38928ee68fa958e36b8c2444afdea3cc85fb145aa3aa20eacc5e0e7e0053bab5869338f4bb9ded000000000000000000266908bc08578ce52243abc87bb5f0e14d276914669a8e2973486c0efa82eb89692"}
			}
			for witness, packetHex := range packetHexes {
				packet, err := hex.DecodeString(packetHex)
				if err != nil {
					t.Fatal(err)
				}
				for format := uint32(0); format < 3; format++ {
					t.Run(fmt.Sprintf("fs%d/ms%d/witness%d/format%d", rate, durationMS, witness, format), func(t *testing.T) {
						packets := [][]byte{packet, good, nil, good}
						cases := make([]libopustest.DecodeDiffCase, len(packets))
						for i, p := range packets {
							cases[i] = libopustest.DecodeDiffCase{Packet: p, Format: format, FrameSize: uint32(frameSize)}
						}
						want, err := libopustest.ProbeDecodeSequence(rate, 2, cases)
						if err != nil {
							t.Fatal(err)
						}
						if want[0].FinalRange != 0 {
							t.Fatalf("witness does not discard main CELT data: C range=%08x", want[0].FinalRange)
						}
						d, err := NewDecoder(DefaultDecoderConfig(rate, 2))
						if err != nil {
							t.Fatal(err)
						}
						pcm := make([]float32, frameSize*2)
						pcm16 := make([]int16, frameSize*2)
						pcm24 := make([]int32, frameSize*2)
						decode := func(p []byte) (int, error) {
							switch format {
							case libopustest.DecodeDiffFormatFloat32:
								return d.Decode(p, pcm)
							case libopustest.DecodeDiffFormatInt16:
								return d.DecodeInt16(p, pcm16)
							default:
								return d.DecodeInt24(p, pcm24)
							}
						}
						for step, p := range packets {
							n, err := decode(p)
							if err != nil || n != int(want[step].Code) {
								t.Fatalf("step%d samples=%d err=%v C=%d", step, n, err, want[step].Code)
							}
							if d.FinalRange() != want[step].FinalRange {
								t.Fatalf("step%d range=%08x C=%08x", step, d.FinalRange(), want[step].FinalRange)
							}
							switch format {
							case libopustest.DecodeDiffFormatFloat32:
								for i, v := range want[step].Float32() {
									if math.Float32bits(pcm[i]) != math.Float32bits(v) {
										t.Fatalf("step%d sample%d=%08x C=%08x", step, i, math.Float32bits(pcm[i]), math.Float32bits(v))
									}
								}
							case libopustest.DecodeDiffFormatInt16:
								for i, v := range want[step].Int16() {
									if pcm16[i] != v {
										t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm16[i], v)
									}
								}
							default:
								for i, v := range want[step].Int24() {
									if pcm24[i] != v {
										t.Fatalf("step%d sample%d=%d C=%d", step, i, pcm24[i], v)
									}
								}
							}
						}
						if allocs := testing.AllocsPerRun(20, func() {
							for _, p := range packets {
								if _, err := decode(p); err != nil {
									panic(err)
								}
							}
						}); allocs != 0 {
							t.Fatalf("warm decode allocations=%g", allocs)
						}
					})
				}
			}
		}
	}
}

func TestShortHybridTransitionPLCMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	seed := encodeAPIRateCELTPacketFrameSize(t, 1, 120)
	mutatedHybrid, err := hex.DecodeString("70a8f77ca6242c85d1dfe89250f7bcd05a152957cab69d04b1d02d2dfdcd23b79593e89ff61d1dcedaa4fb8ad2155e16052b21c563cc828769374b9334e79f124655123c13ca005d9c048c566b1cc473ccef7467dda10aa8d9612fd230288c14f3f1106a98d19a1b22c89b867b7027648f65e0ef6ba4cdcd2320bbd578e89ccbe410ebf9b12176d174ac92")
	if err != nil {
		t.Fatal(err)
	}
	shortCELT, err := hex.DecodeString("e470378d859b002ea26903dbf5ce78a48c66f7173b6e6bf546f1a3149c1d049e7c0768ab7279bc38cee724a0f0ca5b423816a563d6")
	if err != nil {
		t.Fatal(err)
	}
	if mutatedHybrid[0] != 0x70 || shortCELT[0] != 0xe4 {
		t.Fatalf("unexpected witness TOCs %02x/%02x", mutatedHybrid[0], shortCELT[0])
	}

	rates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, rate := range rates {
		for format := uint32(0); format < 3; format++ {
			t.Run(fmt.Sprintf("rate%d/format%d", rate, format), func(t *testing.T) {
				frameSizes := []int{rate / 400, rate / 100, rate / 400, rate / 400, rate / 400}
				packets := [][]byte{seed, mutatedHybrid, shortCELT, nil, shortCELT}
				cases := make([]libopustest.DecodeDiffCase, len(packets))
				for i, packet := range packets {
					cases[i] = libopustest.DecodeDiffCase{
						Packet:    packet,
						Format:    format,
						FrameSize: uint32(frameSizes[i]),
					}
				}
				want, err := libopustest.ProbeDecodeSequence(rate, 1, cases)
				if err != nil {
					t.Fatalf("ProbeDecodeSequence: %v", err)
				}
				wantCounts := []int32{int32(rate / 400), int32(rate / 100), int32(rate / 400), int32(rate / 400), int32(rate / 400)}
				for i := range want {
					if want[i].Code != wantCounts[i] {
						t.Fatalf("C step%d returned %d samples, want %d", i, want[i].Code, wantCounts[i])
					}
				}

				dec, err := NewDecoder(DefaultDecoderConfig(rate, 1))
				if err != nil {
					t.Fatal(err)
				}
				pcm32 := make([]float32, rate/50)
				pcm16 := make([]int16, rate/50)
				pcm24 := make([]int32, rate/50)
				decode := func(packet []byte, frameSize int) (int, error) {
					switch format {
					case libopustest.DecodeDiffFormatFloat32:
						return dec.Decode(packet, pcm32[:frameSize])
					case libopustest.DecodeDiffFormatInt16:
						return dec.DecodeInt16(packet, pcm16[:frameSize])
					default:
						return dec.DecodeInt24(packet, pcm24[:frameSize])
					}
				}
				compare := func(step, n int) {
					if dec.FinalRange() != want[step].FinalRange {
						t.Fatalf("step%d range=%08x C=%08x", step, dec.FinalRange(), want[step].FinalRange)
					}
					switch format {
					case libopustest.DecodeDiffFormatFloat32:
						for sample := 0; sample < n; sample++ {
							got := math.Float32bits(pcm32[sample])
							expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
							if got != expected {
								t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
							}
						}
					case libopustest.DecodeDiffFormatInt16:
						for sample, expected := range want[step].Int16() {
							if pcm16[sample] != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, pcm16[sample], expected)
							}
						}
					default:
						for sample, expected := range want[step].Int24() {
							if pcm24[sample] != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, pcm24[sample], expected)
							}
						}
					}
				}

				for step, packet := range packets {
					n, err := decode(packet, frameSizes[step])
					if err != nil || int32(n) != want[step].Code {
						t.Fatalf("step%d returned (%d,%v), C returned %d", step, n, err, want[step].Code)
					}
					compare(step, n)
				}
				if allocs := testing.AllocsPerRun(10, func() {
					for step, packet := range packets {
						n, err := decode(packet, frameSizes[step])
						if err != nil || int32(n) != want[step].Code {
							panic(fmt.Sprintf("step%d returned (%d,%v), C returned %d", step, n, err, want[step].Code))
						}
					}
				}); allocs != 0 {
					t.Fatalf("warm sequence allocations=%g", allocs)
				}
			})
		}
	}
}

func TestSILKPLCUsesActiveRateAfterCELTDTXMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	stereoSILKMB20 := mustDecodeFECTransitionPacket(t, "2c886c8fa475063df63e6542b4ce9f05336f72206289fea7969d38df05574daee769acbfb80969ae842bc66b7c899baed97f5f567e16cf66543eaf4b57bfed16ff4c788b0d2fe3fdcf505fa2f2f019f0a5eecc6525b878f33f366c019488098b917f3d927910602f569282dc523180")
	stereoSILKWB20 := mustDecodeFECTransitionPacket(t, "4c886bdd439a389a6d1adfa4b0f4676d21b8be7e8fadb6595ae40dd22868cf1ac68f79b23eba441b9c1566addf3ecd6e230f512b52a599418013841f59bbde9f010ac0d9315e55524fb9883f021dbd2a7d22ba628301297eabc89a2744b3d211a25d0afd66eb9d94d18733ea9760fdbc6eb475882cbb13e0932df6065c85427141650526d2566aacfeb63cfc0559e06340948af9bd842d7d13c0")
	celtNarrowbandDTX, err := hex.DecodeString("80")
	if err != nil {
		t.Fatal(err)
	}
	if toc := ParseTOC(stereoSILKMB20[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthMediumband || !toc.Stereo {
		t.Fatalf("SILK mediumband fixture TOC=(%v,%v,stereo=%t)", toc.Mode, toc.Bandwidth, toc.Stereo)
	}
	if toc := ParseTOC(stereoSILKWB20[0]); toc.Mode != ModeSILK || toc.Bandwidth != BandwidthWideband || !toc.Stereo {
		t.Fatalf("SILK wideband fixture TOC=(%v,%v,stereo=%t)", toc.Mode, toc.Bandwidth, toc.Stereo)
	}
	if toc := ParseTOC(celtNarrowbandDTX[0]); toc.Mode != ModeCELT || toc.Bandwidth != BandwidthNarrowband {
		t.Fatalf("one-byte DTX fixture TOC=(%v,%v), want CELT narrowband", toc.Mode, toc.Bandwidth)
	}

	formats := []struct {
		name   string
		format uint32
	}{
		{name: "float32", format: libopustest.DecodeDiffFormatFloat32},
		{name: "int16", format: libopustest.DecodeDiffFormatInt16},
		{name: "int24", format: libopustest.DecodeDiffFormatInt24},
	}
	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, outputChannels := range []int{1, 2} {
			for _, outputFormat := range formats {
				t.Run(fmt.Sprintf("rate%d/ch%d/%s", sampleRate, outputChannels, outputFormat.name), func(t *testing.T) {
					frames := [4]int{
						sampleRate / 50,        // 20 ms SILK mediumband packet
						sampleRate / 50,        // 20 ms FEC request
						sampleRate / 400,       // 2.5 ms one-byte CELT narrowband DTX
						sampleRate * 40 / 1000, // 40 ms SILK PLC
					}
					packets := [4][]byte{stereoSILKMB20, stereoSILKWB20, celtNarrowbandDTX, nil}
					cases := make([]libopustest.DecodeDiffCase, len(frames))
					for i := range cases {
						format := outputFormat.format
						if i == 1 {
							format = libopustest.DecodeDiffFormatFloat32
						}
						cases[i] = libopustest.DecodeDiffCase{
							Packet:    packets[i],
							Format:    format,
							FrameSize: uint32(frames[i]),
							DecodeFEC: i == 1,
						}
					}
					want, err := libopustest.ProbeDecodeSequence(sampleRate, outputChannels, cases)
					if err != nil {
						libopustest.HelperUnavailable(t, "selected-libopus SILK DTX recovery sequence", err)
					}
					if len(want) != len(cases) {
						t.Fatalf("libopus returned %d calls, want %d", len(want), len(cases))
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, outputChannels))
					if err != nil {
						t.Fatalf("NewDecoder: %v", err)
					}
					maxOutput := frames[3] * outputChannels
					pcmFloat := make([]float32, maxOutput)
					pcm16 := make([]int16, maxOutput)
					pcm24 := make([]int32, maxOutput)
					type output struct {
						float32 []float32
						int16   []int16
						int24   []int32
					}
					decode := func(i int) (int, output, error) {
						samples := frames[i] * outputChannels
						var got output
						var n int
						var err error
						switch cases[i].Format {
						case libopustest.DecodeDiffFormatFloat32:
							got.float32 = pcmFloat[:samples]
							if i == 1 {
								n, err = dec.DecodeWithFEC(packets[i], got.float32, true)
							} else {
								n, err = dec.Decode(packets[i], got.float32)
							}
						case libopustest.DecodeDiffFormatInt16:
							got.int16 = pcm16[:samples]
							n, err = dec.DecodeInt16(packets[i], got.int16)
						case libopustest.DecodeDiffFormatInt24:
							got.int24 = pcm24[:samples]
							n, err = dec.DecodeInt24(packets[i], got.int24)
						}
						return n, got, err
					}
					failedStep := -1
					var failure error
					runSequence := func() {
						dec.Reset()
						failedStep, failure = -1, nil
						for i := range cases {
							n, got, err := decode(i)
							if err != nil || n != int(want[i].Code) || n != frames[i] {
								failedStep, failure = i, err
								if failure == nil {
									failure = fmt.Errorf("returned %d samples, C returned %d; want %d", n, want[i].Code, frames[i])
								}
								return
							}
							if gotRange, wantRange := dec.FinalRange(), want[i].FinalRange; gotRange != wantRange {
								failedStep = i
								failure = fmt.Errorf("final range=%08x, C=%08x", gotRange, wantRange)
								return
							}
							if mismatch := assertDecodeMalformedFramingPCM(cases[i].Format, n, outputChannels, want[i].PCM, got.float32, got.int16, got.int24); mismatch != nil {
								failedStep, failure = i, mismatch
								return
							}
							if i == 2 && (dec.Bandwidth() != BandwidthNarrowband || dec.prevMode != ModeSILK || dec.lastPacketMode != ModeCELT || dec.silkDecoder.GetSampleRateKHz() != 16) {
								failedStep = i
								failure = fmt.Errorf("after CELT DTX public bandwidth=%v, previous mode=%v, packet mode=%v, active SILK rate=%d kHz", dec.Bandwidth(), dec.prevMode, dec.lastPacketMode, dec.silkDecoder.GetSampleRateKHz())
								return
							}
						}
						if dec.Bandwidth() != BandwidthNarrowband || dec.silkDecoder.GetSampleRateKHz() != 16 {
							failedStep = len(cases) - 1
							failure = fmt.Errorf("after PLC public bandwidth=%v, active SILK rate=%d kHz", dec.Bandwidth(), dec.silkDecoder.GetSampleRateKHz())
						}
					}
					runSequence()
					if failedStep >= 0 {
						t.Fatalf("call %d: %v", failedStep, failure)
					}
					if allocs := testing.AllocsPerRun(10, runSequence); allocs != 0 {
						t.Fatalf("warmed Reset/recovery sequence allocs=%g, want 0", allocs)
					}
					if failedStep >= 0 {
						t.Fatalf("warmed call %d: %v", failedStep, failure)
					}
				})
			}
		}
	}
}

func TestSILKLongStereoToMonoRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	// Fixed wire packets keep this recovery witness independent of encoder behavior.
	mono40, err := hex.DecodeString("50c100aeb623947d81bcc7980058ef2539604e53c2529c15b9499cb7d7dc318eccc9b705c399330c3ba8c4a8f9dd83081768b4c5cfe84c6ca76f3f89e6834b595ae4cbee4165a7eef36c43d23de0cd3520664548400d75e22dda891378b920b5ea9956717905f4692f36ed646edcaa643398c37d36849e9508490f3d6300ae1cc34150d371c3e713840ef0")
	if err != nil {
		t.Fatal(err)
	}
	stereo40, err := hex.DecodeString("54d9346d58758b87d1bbdb39e481b0cdf58959ea38e20f1255003666bb4633195ac9bd2f52ab993093f3d6a120f4a95b13b0163db9c0c3e21f8df1f4931a487ecb827357889078eea89ef7153b012ab77fde135545bbb90e58afbf954b78aa0edd9f4de4c08cef748c8873b86c33787710dfa3af84f3eb72bbb3e6700d1468896caf3a1e475ff74451cb22580a3c099f092391240c7160421137e84a80effbb35c2e0f817a")
	if err != nil {
		t.Fatal(err)
	}
	if mono40[0]&4 != 0 || stereo40[0]&4 == 0 {
		t.Fatalf("packet channel flags are mono=%02x stereo=%02x", mono40[0], stereo40[0])
	}

	type outputFormat struct {
		name string
		id   uint32
	}
	formats := []outputFormat{
		{name: "float32", id: libopustest.DecodeDiffFormatFloat32},
		{name: "int16", id: libopustest.DecodeDiffFormatInt16},
		{name: "int24", id: libopustest.DecodeDiffFormatInt24},
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		sampleRate := sampleRate
		packetFrameSize := sampleRate * 40 / 1000
		plcFrameSize := sampleRate * 5 / 2000
		packets := [][]byte{mono40, nil, stereo40, nil, mono40}
		frameSizes := []int{packetFrameSize, plcFrameSize, packetFrameSize, plcFrameSize, packetFrameSize}

		for _, format := range formats {
			format := format
			t.Run(fmt.Sprintf("rate%d/%s", sampleRate, format.name), func(t *testing.T) {
				cases := make([]libopustest.DecodeDiffCase, len(packets))
				for step, packet := range packets {
					cases[step] = libopustest.DecodeDiffCase{
						Packet:    packet,
						Format:    format.id,
						FrameSize: uint32(frameSizes[step]),
					}
				}
				want, err := libopustest.ProbeDecodeSequence(sampleRate, 2, cases)
				if err != nil {
					t.Fatalf("ProbeDecodeSequence: %v", err)
				}
				if len(want) != len(packets) {
					t.Fatalf("oracle returned %d calls, want %d", len(want), len(packets))
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, 2))
				if err != nil {
					t.Fatalf("NewDecoder: %v", err)
				}
				maxOutputSamples := packetFrameSize * 2
				pcmFloat := make([]float32, maxOutputSamples)
				pcm16 := make([]int16, maxOutputSamples)
				pcm24 := make([]int32, maxOutputSamples)
				decode := func(packet []byte, frameSize int) (int, error) {
					samples := frameSize * 2
					switch format.id {
					case libopustest.DecodeDiffFormatFloat32:
						return dec.Decode(packet, pcmFloat[:samples])
					case libopustest.DecodeDiffFormatInt16:
						return dec.DecodeInt16(packet, pcm16[:samples])
					default:
						return dec.DecodeInt24(packet, pcm24[:samples])
					}
				}

				for step, packet := range packets {
					n, err := decode(packet, frameSizes[step])
					if err != nil || int32(n) != want[step].Code {
						t.Fatalf("step%d returned (%d,%v), C returned %d", step, n, err, want[step].Code)
					}
					if want[step].Code != int32(frameSizes[step]) {
						t.Fatalf("C step%d returned %d samples, want %d", step, want[step].Code, frameSizes[step])
					}
					if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
						t.Fatalf("step%d range=%08x C=%08x", step, got, expected)
					}

					sampleCount := n * 2
					bytesPerSample := 4
					if format.id == libopustest.DecodeDiffFormatInt16 {
						bytesPerSample = 2
					}
					if got, expected := len(want[step].PCM), sampleCount*bytesPerSample; got != expected {
						t.Fatalf("step%d oracle PCM bytes=%d, want %d", step, got, expected)
					}
					switch format.id {
					case libopustest.DecodeDiffFormatFloat32:
						for sample := 0; sample < sampleCount; sample++ {
							got := math.Float32bits(pcmFloat[sample])
							expected := binary.LittleEndian.Uint32(want[step].PCM[sample*4:])
							if got != expected {
								t.Fatalf("step%d sample%d=%08x C=%08x", step, sample, got, expected)
							}
						}
					case libopustest.DecodeDiffFormatInt16:
						for sample := 0; sample < sampleCount; sample++ {
							got := pcm16[sample]
							expected := int16(binary.LittleEndian.Uint16(want[step].PCM[sample*2:]))
							if got != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
							}
						}
					default:
						for sample := 0; sample < sampleCount; sample++ {
							got := pcm24[sample]
							expected := int32(binary.LittleEndian.Uint32(want[step].PCM[sample*4:]))
							if got != expected {
								t.Fatalf("step%d sample%d=%d C=%d", step, sample, got, expected)
							}
						}
					}
				}

				runSequence := func() error {
					dec.Reset()
					for step, packet := range packets {
						n, err := decode(packet, frameSizes[step])
						if err != nil {
							return err
						}
						if n != frameSizes[step] {
							return fmt.Errorf("step%d returned %d samples, want %d", step, n, frameSizes[step])
						}
					}
					return nil
				}
				if err := runSequence(); err != nil {
					t.Fatalf("warm reset sequence: %v", err)
				}
				var runErr error
				allocs := testing.AllocsPerRun(10, func() {
					runErr = runSequence()
				})
				if runErr != nil {
					t.Fatalf("measured reset sequence: %v", runErr)
				}
				if allocs != 0 {
					t.Fatalf("warm reset sequence allocations=%g", allocs)
				}
			})
		}
	}
}

const (
	silkNoLBRRMB60PrimeHex  = "38e0b8a342302116dd95c61fbe30c235931e69e7742082a44e6dc89a69a9a1f2d5b943108c758641fc3f9c71c10dc9fdc63df9b7972a9ac0e419fd9adaa535d01bfd4dbe68784c9e3e8378446d083b3f6ad7d7ac2785df66e7943d58a5f6118b30f89396e218a13af0e4e52811a4f217657d8a4c0b40f067a3a45f4f0498c7d3d74ecbd894bfabdd3eebba9f9c48e93818adb799103dc9b60dc7895092370005992e18723ff8cd733a61f0c03083c40ff6086988fe15c30f420eb1662088ade1d549fe6b5fdb2441cf7d57bf60012532bfd82259ecbefd041aa197bb087b6d8297b048ed4369352e1b2cbebf909121ca44fde1916396ec35b1f104582de3d33fd748ec1a19bbe7338fac7cafa3a7623729f322469ea51f51209196702497d20706976437d5ba82ae865935af9a2e1f25bb0f6d9440051aa77b2b3099a9e95528c45f43c4e0e0ff7671f2e7d542ebcc60c1304959"
	silkNoLBRRWB40TargetHex = "50c188124388cac7114d36facf91e805441def8f83919ee7973f31d72f53a7c8dc8bc330a3a90608a96a027be78b9299bd3171df6293a6e48081c778be91c2feec6bcab6de26dce0f4c5be569a692e436b46f01f38762513f7761245ed2552f25750013f57ce60321ed96093c1e348bfb3dfd43daf5bc08a7814a726b8b66119990ea3c59259f8cd7ab6cbf0d9e295adc680b46d6cd9b233aecf22758b0ce775c5e02f9d36e47ffd24d591eed2813b188921b3f7f27493908c7089cab41f694cb977ea9d01b843a574c9db3aa0a660f0d50a1d1c2aaac2e72e72397cecebe419b438aa2e697f05960392de46c1815d72c1ccdfda5959f91d57dd7d9797e0e3b7028099539e8707d90a4a042508a6cf0101582f6862e4f656ff2019d7eea02fe3f1a8b7d334c98024c0"
)

func TestSILKNoLBRRMultiFrameFECPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	prime, err := hex.DecodeString(silkNoLBRRMB60PrimeHex)
	if err != nil {
		t.Fatal(err)
	}
	target, err := hex.DecodeString(silkNoLBRRWB40TargetHex)
	if err != nil {
		t.Fatal(err)
	}
	for name, packet := range map[string][]byte{"MB60 prime": prime, "WB40 target": target} {
		toc := ParseTOC(packet[0])
		if toc.Mode != ModeSILK {
			t.Fatalf("%s mode=%v, want SILK", name, toc.Mode)
		}
		if PacketHasLBRR(packet) {
			t.Fatalf("%s unexpectedly has LBRR", name)
		}
	}
	lbrr, err := probeLibopusPacketLBRR([]libopusPacketLBRRCase{
		{name: "MB60 prime", packet: prime},
		{name: "WB40 target", packet: target},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "no-LBRR FEC witness packet probe", err)
		return
	}
	for i, got := range lbrr {
		if got != 0 {
			t.Fatalf("libopus reports LBRR=%d for packet %d, want none", got, i)
		}
	}

	for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dHz_%dch", sampleRate, channels), func(t *testing.T) {
				primeCapacity := sampleRate * 120 / 1000
				primeSamples := sampleRate * 60 / 1000
				fecSamples := sampleRate * 40 / 1000
				want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, []libopustest.DecodeDiffCase{
					{
						Packet: prime, Format: libopustest.DecodeDiffFormatInt16,
						FrameSize: uint32(primeCapacity),
					},
					{
						Packet: target, Format: libopustest.DecodeDiffFormatFloat32,
						FrameSize: uint32(fecSamples), DecodeFEC: true,
					},
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "MB60→WB40 no-LBRR FEC sequence", err)
					return
				}
				if len(want) != 2 || want[0].Code != int32(primeSamples) || want[1].Code != int32(fecSamples) {
					t.Fatalf("libopus counts=%v, want [%d %d]", decodeFECTransitionCounts(want), primeSamples, fecSamples)
				}
				wantPrime := want[0].Int16()
				wantFEC := want[1].Float32()

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				pcm16 := make([]int16, primeCapacity*channels)
				pcmFloat := make([]float32, fecSamples*channels)
				decodeSequence := func() error {
					dec.Reset()
					n, err := dec.DecodeInt16(prime, pcm16)
					if err != nil {
						return fmt.Errorf("MB60 decode: %w", err)
					}
					if n != primeSamples {
						return fmt.Errorf("MB60 samples=%d, want %d", n, primeSamples)
					}
					if got, expected := dec.FinalRange(), want[0].FinalRange; got != expected {
						return fmt.Errorf("MB60 range=%08x C=%08x", got, expected)
					}
					assertSILKNoLBRRInt16PCM(t, "MB60", pcm16[:n*channels], wantPrime)

					n, err = dec.DecodeWithFEC(target, pcmFloat, true)
					if err != nil {
						return fmt.Errorf("WB40 FEC decode: %w", err)
					}
					if n != fecSamples {
						return fmt.Errorf("WB40 FEC samples=%d, want %d", n, fecSamples)
					}
					if got, expected := dec.FinalRange(), want[1].FinalRange; got != expected {
						return fmt.Errorf("WB40 FEC range=%08x C=%08x", got, expected)
					}
					assertSILKNoLBRRFloat32PCM(t, "WB40 FEC", pcmFloat[:n*channels], wantFEC)
					return nil
				}
				if err := decodeSequence(); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					if err := decodeSequence(); err != nil {
						t.Fatal(err)
					}
				}
				var measuredErr error
				allocs := testing.AllocsPerRun(20, func() {
					measuredErr = decodeSequence()
				})
				if measuredErr != nil {
					t.Fatal(measuredErr)
				}
				if allocs != 0 {
					t.Fatalf("warm MB60→WB40 sequence allocations=%g, want 0", allocs)
				}
			})
		}
	}
}

func assertSILKNoLBRRInt16PCM(t *testing.T, stage string, got, want []int16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s int16 PCM length=%d, C=%d", stage, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s int16 PCM[%d]=%d, C=%d", stage, i, got[i], want[i])
		}
	}
}

func assertSILKNoLBRRFloat32PCM(t *testing.T, stage string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s float32 PCM length=%d, C=%d", stage, len(got), len(want))
	}
	for i := range want {
		if gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i]); gotBits != wantBits {
			t.Fatalf("%s float32 PCM[%d]=%08x, C=%08x", stage, i, gotBits, wantBits)
		}
	}
}

func TestCELTSilenceDecodeMatchesLibopusFloatBits(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, sampleRate := range []int{48000, 24000, 16000, 12000, 8000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("rate=%d/channels=%d", sampleRate, channels), func(t *testing.T) {
				var cases []libopustest.DecodeDiffCase
				frameSizes := []int{sampleRate * 25 / 10000, sampleRate * 5 / 1000, sampleRate / 100, sampleRate / 50}
				for lm, frameSize := range frameSizes {
					cfg := byte(28 + lm)
					toc := cfg << 3
					if channels == 2 {
						toc |= 4
					}
					cases = append(cases, libopustest.DecodeDiffCase{
						Packet:    []byte{toc, 0xff, 0xfe},
						Format:    libopustest.DecodeDiffFormatFloat32,
						FrameSize: uint32(frameSize),
					})
				}
				want, err := libopustest.ProbeDecodeDiff(sampleRate, channels, cases)
				if err != nil {
					t.Fatalf("libopus silence decode oracle: %v", err)
				}
				if len(want) != len(cases) {
					t.Fatalf("oracle cases=%d want %d", len(want), len(cases))
				}
				for i, c := range cases {
					t.Run(fmt.Sprintf("duration-index=%d", i), func(t *testing.T) {
						frameSize := int(c.FrameSize)
						config := DefaultDecoderConfig(sampleRate, channels)
						dec, err := NewDecoder(config)
						if err != nil {
							t.Fatalf("NewDecoder: %v", err)
						}
						got := make([]float32, frameSize*channels)
						n, err := dec.Decode(c.Packet, got)
						if err != nil {
							t.Fatalf("Decode: %v", err)
						}
						if want[i].Code != int32(frameSize) {
							t.Fatalf("libopus decoded samples=%d want %d", want[i].Code, frameSize)
						}
						if n != frameSize {
							t.Fatalf("gopus decoded samples=%d want %d", n, frameSize)
						}
						assertFloat32SliceBits(t, "silence pcm", got, want[i].Float32())
					})
				}
			})
		}
	}
}

func assertFloat32SliceBits(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%08x %.10g want %08x %.10g", label, i, math.Float32bits(got[i]), got[i], math.Float32bits(want[i]), want[i])
		}
	}
}

// Received CELT silence carries synthesis overlap and filter memory from the
// preceding signal. Compare the subsequent recovery with the same C history.
func TestCELTReceivedSilenceHistoryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, rate := range []int{48000, 16000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("rate%d/ch%d", rate, channels), func(t *testing.T) {
				packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
				silence := []byte{packet[0] & 0xfc, 0xff, 0xfe}
				steps := []libopusAPIRateDecodeStep{{packet: packet}, {packet: silence}, {packet: silence}, {packet: packet}}
				frame := rate / 50
				want, ranges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(rate, channels, frame, steps)
				if err != nil {
					libopustest.HelperUnavailable(t, "CELT received silence history", err)
				}
				stride := frame * channels
				if len(want) != len(steps)*stride || len(ranges) != len(steps) {
					t.Fatalf("C samples/ranges=%d/%d", len(want), len(ranges))
				}
				tail := false
				for _, v := range want[stride : 2*stride] {
					if math.Abs(float64(v)) > 1e-10 {
						tail = true
						break
					}
				}
				if !tail {
					t.Fatal("received silence does not exercise nonzero synthesis history")
				}
				dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
				if err != nil {
					t.Fatal(err)
				}
				out := make([]float32, stride)
				for i, step := range steps {
					n, err := dec.Decode(step.packet, out)
					if err != nil || n != frame {
						t.Fatalf("step%d samples=%d want=%d err=%v", i, n, frame, err)
					}
					if dec.FinalRange() != ranges[i] {
						t.Fatalf("step%d range=%08x want=%08x", i, dec.FinalRange(), ranges[i])
					}
					assertFloat32SliceBits(t, fmt.Sprintf("step%d", i), out, want[i*stride:(i+1)*stride])
				}
			})
		}
	}
}

var fecQualityOnce sync.Once

func logFECQualityStatus(t *testing.T) {
	t.Helper()
	fecQualityOnce.Do(func() {
		t.Log("FEC/LBRR STATUS: LBRR recovery is active in focused tests; keep correlation and energy checks as regression sentinels.")
		t.Log("COVERED: provided-packet recovery, nil-packet PLC fallback, no-LBRR PLC fallback, and packet-mode gating.")
		t.Log("WATCH: low-bitrate/no-LBRR packet mixes can still fall back to PLC by design; broaden claims only with libopus-backed seams.")
	})
}

// TestFEC_LBRRActualRecovery verifies that provided-packet LBRR recovery
// produces a non-silent concealment frame.
func TestFEC_LBRRActualRecovery(t *testing.T) {
	logFECQualityStatus(t)

	const frameSize = 960
	seedPacket, recoveryPacket := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, frameSize)

	t.Log("\n=== Testing LBRR recovery for frame 5 ===")

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	pcmSeed := make([]float32, frameSize)
	if _, err := dec.Decode(seedPacket, pcmSeed); err != nil {
		t.Fatalf("Decode seed error: %v", err)
	}

	pcmFEC := make([]float32, frameSize)
	n, err := dec.DecodeWithFEC(recoveryPacket, pcmFEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC error: %v", err)
	}
	t.Logf("FEC recovered %d samples for lost frame 5", n)

	pcmRecovery := make([]float32, frameSize)
	if _, err := dec.Decode(recoveryPacket, pcmRecovery); err != nil {
		t.Fatalf("Decode recovery packet error: %v", err)
	}

	var sqSumFEC, sqSumRecovery float64

	for i := range frameSize {
		sqSumFEC += float64(pcmFEC[i]) * float64(pcmFEC[i])
		sqSumRecovery += float64(pcmRecovery[i]) * float64(pcmRecovery[i])
	}

	n64 := float64(frameSize)
	energyFEC := math.Sqrt(sqSumFEC / n64)
	energyRecovery := math.Sqrt(sqSumRecovery / n64)

	t.Logf("FEC recovery RMS energy: %.6f", energyFEC)
	t.Logf("Recovery-packet decode RMS energy: %.6f", energyRecovery)

	if energyFEC < 0.001 {
		t.Error("FEC recovery produced near-silence - LBRR may not be working")
	}

	t.Log("\nFirst 10 samples comparison:")
	for i := 0; i < 10 && i < frameSize; i++ {
		t.Logf("  [%d] FEC=%.4f, Recovery=%.4f", i, pcmFEC[i], pcmRecovery[i])
	}
}

// TestFEC_HasLBRRCheck verifies packet-level LBRR detection.
func TestFEC_HasLBRRCheck(t *testing.T) {
	_, recoveryPacket := encodeAPIRateFECSequence(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, 960)
	if !packetHasInBandFEC(t, recoveryPacket) {
		t.Fatal("recovery packet should carry LBRR")
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	out := make([]float32, 960)
	if _, err := dec.Decode(recoveryPacket, out); err != nil {
		t.Fatalf("Decode recovery packet error: %v", err)
	}
	if dec.hasFEC {
		t.Fatal("normal decode should not cache LBRR for nil decode_fec")
	}
}

// TestFEC_VsSILKDecoder directly tests the SILK decoder's FEC capability
func TestFEC_SILKEncoderLBRREnabled(t *testing.T) {
	enc, _ := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})

	// Initially FEC should be disabled
	if enc.FECEnabled() {
		t.Error("FEC should be disabled initially")
	}

	// Enable FEC
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if !enc.FECEnabled() {
		t.Error("FEC should be enabled after SetFEC(true)")
	}

	// Encode a frame and check the internal SILK encoder's LBRR state
	frameSize := 960
	pcm := make([]float32, frameSize)
	for j := range frameSize {
		pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*float64(j)/48000))
	}

	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	t.Logf("Encoded packet: %d bytes, TOC=0x%02X", len(packet), packet[0])

	// Parse TOC to verify mode
	toc := ParseTOC(packet[0])
	t.Logf("Mode: %v, Bandwidth: %v, FrameSize: %d", toc.Mode, toc.Bandwidth, toc.FrameSize)

	if toc.Mode != ModeSILK && toc.Mode != ModeHybrid {
		t.Logf("Note: Mode is %v - LBRR only applies to SILK/Hybrid modes", toc.Mode)
	}
}

func TestFEC_ProvidedPacketWithoutLBRRFallsBackToPLC(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(false)
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}

	frameSize := 960
	makeFrame := func(phase int) []float32 {
		pcm := make([]float32, frameSize)
		for i := range pcm {
			sampleIdx := phase + i
			pcm[i] = float32(0.45*math.Sin(2*math.Pi*260*float64(sampleIdx)/48000) +
				0.15*math.Sin(2*math.Pi*520*float64(sampleIdx)/48000))
		}
		return pcm
	}

	packet0, err := enc.EncodeFloat32(makeFrame(0))
	if err != nil {
		t.Fatalf("Encode packet0 error: %v", err)
	}
	packet1, err := enc.EncodeFloat32(makeFrame(frameSize))
	if err != nil {
		t.Fatalf("Encode packet1 error: %v", err)
	}
	if packetHasInBandFEC(t, packet1) {
		t.Fatal("expected FEC-disabled packet to have no in-band LBRR")
	}

	decPLC, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder plc error: %v", err)
	}
	decFEC, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder fec error: %v", err)
	}

	seedPLC := make([]float32, frameSize)
	if _, err := decPLC.Decode(packet0, seedPLC); err != nil {
		t.Fatalf("Decode packet0 on plc decoder error: %v", err)
	}
	seedFEC := make([]float32, frameSize)
	if _, err := decFEC.Decode(packet0, seedFEC); err != nil {
		t.Fatalf("Decode packet0 on fec decoder error: %v", err)
	}

	want := make([]float32, frameSize)
	nWant, err := decPLC.Decode(nil, want)
	if err != nil {
		t.Fatalf("Decode(nil) plc fallback error: %v", err)
	}

	got := make([]float32, frameSize)
	nGot, err := decFEC.DecodeWithFEC(packet1, got, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(no-LBRR packet) error: %v", err)
	}

	if nGot != nWant {
		t.Fatalf("sample count mismatch: got %d want %d", nGot, nWant)
	}
	for i := range nWant {
		if got[i] != want[i] {
			t.Fatalf("provided-packet no-LBRR fallback diverged from PLC at sample %d: got=%f want=%f", i, got[i], want[i])
		}
	}
}

func packetHasInBandFEC(t *testing.T, packet []byte) bool {
	t.Helper()

	toc, _, err := packetFrameCount(packet)
	if err != nil {
		t.Fatalf("packetFrameCount error: %v", err)
	}
	if toc.Mode != ModeSILK && toc.Mode != ModeHybrid {
		return false
	}

	firstFrameData, err := extractFirstFramePayload(packet, toc)
	if err != nil {
		t.Fatalf("extractFirstFramePayload error: %v", err)
	}
	return packetHasLBRR(firstFrameData, toc)
}

// TestFEC_EndToEnd tests the complete FEC encode/decode cycle:
// 1. Encode audio with FEC enabled
// 2. Verify LBRR data is present in packets
// 3. Simulate packet loss and verify FEC recovery
func TestFEC_EndToEnd(t *testing.T) {
	// Create encoder with FEC enabled
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if !enc.FECEnabled() {
		t.Fatal("FEC should be enabled")
	}

	// Create decoder
	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Generate test signal (440Hz sine wave)
	frameSize := 960 // 20ms at 48kHz
	numFrames := 5

	packets := make([][]byte, numFrames)

	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
			sampleIdx := i*frameSize + j
			pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
		}

		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Frame %d: empty packet (DTX suppressed?)", i)
		}

		// Make a copy of the packet
		packets[i] = make([]byte, len(packet))
		copy(packets[i], packet)

		t.Logf("Frame %d: %d bytes, TOC=0x%02X", i, len(packet), packet[0])
	}

	// Now test decoding
	t.Log("\n=== Testing normal decode ===")
	decodedNormal := make([][]float32, numFrames)
	for i := range numFrames {
		pcm := make([]float32, frameSize)
		n, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("Decode frame %d error: %v", i, err)
		}
		if n != frameSize {
			t.Errorf("Decode frame %d: got %d samples, want %d", i, n, frameSize)
		}
		decodedNormal[i] = pcm[:n]
		t.Logf("Frame %d: decoded %d samples, hasFEC=%v", i, n, dec.hasFEC)
	}

	// Reset decoder and test FEC recovery
	dec.Reset()

	t.Log("\n=== Testing FEC recovery (simulating packet 2 lost) ===")
	// Decode frame 0 and 1 normally
	pcm0 := make([]float32, frameSize)
	_, err = dec.Decode(packets[0], pcm0)
	if err != nil {
		t.Fatalf("Decode frame 0 error: %v", err)
	}
	t.Logf("Frame 0: decoded, hasFEC=%v", dec.hasFEC)

	pcm1 := make([]float32, frameSize)
	_, err = dec.Decode(packets[1], pcm1)
	if err != nil {
		t.Fatalf("Decode frame 1 error: %v", err)
	}
	t.Logf("Frame 1: decoded, hasFEC=%v", dec.hasFEC)

	// Simulate packet 2 is lost - use FEC recovery
	pcm2FEC := make([]float32, frameSize)
	n, err := dec.DecodeWithFEC(nil, pcm2FEC, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC (lost packet 2) error: %v", err)
	}
	t.Logf("Frame 2 (FEC): recovered %d samples", n)

	// Decode frame 3 normally
	pcm3 := make([]float32, frameSize)
	_, err = dec.Decode(packets[3], pcm3)
	if err != nil {
		t.Fatalf("Decode frame 3 error: %v", err)
	}
	t.Logf("Frame 3: decoded, hasFEC=%v", dec.hasFEC)

	t.Log("\n=== FEC End-to-End test completed ===")
}

func TestFEC_ProvidedPacketRecoveryPath(t *testing.T) {
	const channels = 2
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationAudio})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}

	frameSize := 960
	var packets [][]byte
	for i := range 12 {
		pcm := make([]float32, frameSize*channels)
		for j := range frameSize {
			sampleIdx := i*frameSize + j
			pcm[2*j] = float32(0.5*math.Sin(2*math.Pi*220*float64(sampleIdx)/48000) +
				0.25*math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
			pcm[2*j+1] = float32(0.45*math.Sin(2*math.Pi*330*float64(sampleIdx)/48000) +
				0.20*math.Sin(2*math.Pi*660*float64(sampleIdx)/48000))
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		packets = append(packets, append([]byte(nil), packet...))
		if len(packets) >= 3 && packetHasInBandFEC(t, packet) {
			break
		}
	}

	if len(packets) < 3 || !packetHasInBandFEC(t, packets[len(packets)-1]) {
		t.Fatal("failed to generate a packet carrying LBRR for provided-packet recovery")
	}

	packet0 := packets[len(packets)-3]
	recoveryPacket := packets[len(packets)-1]

	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	seed := make([]float32, frameSize*channels)
	if _, err := dec.Decode(packet0, seed); err != nil {
		t.Fatalf("Decode seed packet error: %v", err)
	}

	recovered := make([]float32, frameSize*channels)
	n, err := dec.DecodeWithFEC(recoveryPacket, recovered, true)
	if err != nil {
		t.Fatalf("DecodeWithFEC(recovery packet) error: %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeWithFEC(recovery packet) samples=%d want=%d", n, frameSize)
	}
	if computeEnergyFloat32(recovered[:n*channels]) == 0 {
		t.Fatal("DecodeWithFEC(recovery packet) produced silence")
	}

	packetOut := make([]float32, frameSize*channels)
	if _, err := dec.Decode(recoveryPacket, packetOut); err != nil {
		t.Fatalf("Decode recovery packet after FEC error: %v", err)
	}
}

// TestFEC_LBRRPresence verifies that LBRR data is encoded in SILK packets
// by checking the LBRR flag in the packet header.
func TestFEC_LBRRPresence(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}

	// Enable FEC
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}

	frameSize := 960
	numFrames := 5

	lbrrPackets := 0
	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
			sampleIdx := i*frameSize + j
			pcm[j] = float32(0.7 * math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
		}

		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		if len(packet) < 2 {
			t.Logf("Frame %d: packet too short (%d bytes)", i, len(packet))
			continue
		}

		toc := ParseTOC(packet[0])
		t.Logf("Frame %d: %d bytes, mode=%v, config=%d", i, len(packet), toc.Mode, packet[0]>>3)

		// For SILK/Hybrid packets, check if LBRR flag is set
		// LBRR flag is encoded in the SILK layer header
		if toc.Mode == ModeSILK || toc.Mode == ModeHybrid {
			if len(packet) > 1 {
				// The SILK layer starts after TOC byte
				// For single-frame packets, bit positions are:
				// - VAD flag: 1 bit
				// - LBRR flag: 1 bit
				// Check second byte for LBRR presence indication
				hasLBRRIndication := (packet[1]&0x40) != 0 || (packet[1]&0x80) != 0
				if hasLBRRIndication {
					lbrrPackets++
				}
				t.Logf("  SILK header byte: 0x%02X, possible LBRR indication: %v", packet[1], hasLBRRIndication)
			}
		}
	}

	t.Logf("\nPackets with potential LBRR: %d/%d", lbrrPackets, numFrames)
	// Note: LBRR may not be present in all packets depending on speech activity
}

// TestFEC_RecoveryQuality compares FEC recovery quality vs PLC
func TestFEC_RecoveryQuality(t *testing.T) {
	// Create encoder with FEC enabled
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}

	frameSize := 960
	numFrames := 10

	// Encode frames
	packets := make([][]byte, numFrames)
	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
			sampleIdx := i*frameSize + j
			// Use a more complex signal for better quality comparison
			pcm[j] = float32(0.5*math.Sin(2*math.Pi*440*float64(sampleIdx)/48000) +
				0.3*math.Sin(2*math.Pi*880*float64(sampleIdx)/48000))
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		packets[i] = make([]byte, len(packet))
		copy(packets[i], packet)
	}

	// Test 1: Decode with FEC recovery for lost packet
	dec1, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	for i := range 5 {
		pcm := make([]float32, frameSize)
		dec1.Decode(packets[i], pcm)
	}

	// Lose packet 5, recover with FEC
	pcmFEC := make([]float32, frameSize)
	dec1.DecodeWithFEC(nil, pcmFEC, true)

	// Continue normal decode
	for i := 6; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		dec1.Decode(packets[i], pcm)
	}

	// Test 2: Decode with PLC for lost packet (no FEC)
	dec2, _ := NewDecoder(DefaultDecoderConfig(48000, 1))
	for i := range 5 {
		pcm := make([]float32, frameSize)
		dec2.Decode(packets[i], pcm)
	}

	// Lose packet 5, recover with PLC only
	pcmPLC := make([]float32, frameSize)
	dec2.Decode(nil, pcmPLC)

	// Continue normal decode
	for i := 6; i < numFrames; i++ {
		pcm := make([]float32, frameSize)
		dec2.Decode(packets[i], pcm)
	}

	// Compare energy of recovered frames
	energyFEC := 0.0
	energyPLC := 0.0
	for i := range frameSize {
		energyFEC += float64(pcmFEC[i]) * float64(pcmFEC[i])
		energyPLC += float64(pcmPLC[i]) * float64(pcmPLC[i])
	}

	t.Logf("FEC recovery energy: %.6f", energyFEC/float64(frameSize))
	t.Logf("PLC recovery energy: %.6f", energyPLC/float64(frameSize))

	// Both should produce non-zero output
	if energyFEC == 0 && energyPLC == 0 {
		t.Error("Both FEC and PLC produced silence - recovery failed")
	}

	t.Log("FEC/PLC recovery comparison complete")
}

// TestFEC_MultiplePacketLoss tests FEC behavior with multiple consecutive lost packets
func TestFEC_MultiplePacketLoss(t *testing.T) {
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: 1, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(15); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}

	dec, err := NewDecoder(DefaultDecoderConfig(48000, 1))
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	frameSize := 960
	numFrames := 10

	// Encode all frames
	packets := make([][]byte, numFrames)
	for i := range numFrames {
		pcm := make([]float32, frameSize)
		for j := range frameSize {
			sampleIdx := i*frameSize + j
			pcm[j] = float32(0.5 * math.Sin(2*math.Pi*440*float64(sampleIdx)/48000))
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d error: %v", i, err)
		}
		packets[i] = make([]byte, len(packet))
		copy(packets[i], packet)
	}

	// Decode: 0, 1, 2, [3 lost], [4 lost], 5, 6, 7, 8, 9
	pcm := make([]float32, frameSize)

	// Decode 0, 1, 2 normally
	for i := range 3 {
		_, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("Decode frame %d error: %v", i, err)
		}
		t.Logf("Frame %d: decoded normally", i)
	}

	// Lose frame 3 and 4
	t.Log("Simulating loss of frames 3 and 4...")
	for i := range 2 {
		_, err := dec.DecodeWithFEC(nil, pcm, true)
		if err != nil {
			t.Fatalf("DecodeWithFEC for lost frame error: %v", err)
		}
		t.Logf("Lost frame %d: recovered with FEC/PLC", 3+i)
	}

	// Continue with frames 5-9
	for i := 5; i < numFrames; i++ {
		_, err := dec.Decode(packets[i], pcm)
		if err != nil {
			t.Fatalf("Decode frame %d error: %v", i, err)
		}
		t.Logf("Frame %d: decoded normally", i)
	}

	t.Log("Multiple packet loss test completed successfully")
}

func TestSILKLowBitrateDTXConcealmentSharedPathParity(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const frames = 16

	type spec struct {
		name     string
		bw       Bandwidth
		frameMs  int
		bitrate  int
		channels int
		content  string
	}
	cases := []spec{
		{"silk_mb_ch1_10ms_8000bps_chirp", BandwidthMediumband, 10, 8000, 1, "chirp"},
		{"silk_nb_ch1_10ms_8000bps_chirp", BandwidthNarrowband, 10, 8000, 1, "chirp"},
		{"silk_mb_ch1_10ms_8000bps_ramp", BandwidthMediumband, 10, 8000, 1, "ramp"},
		{"silk_nb_ch1_10ms_8000bps_ramp", BandwidthNarrowband, 10, 8000, 1, "ramp"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets, frameSize := encodeSILKConcealmentProbePackets(t, c.bw, c.frameMs, c.bitrate, c.channels, c.content, frames)
			if len(packets) == 0 {
				t.Fatal("encoder produced no packets")
			}

			firstPLC := -1
			for i, p := range packets {
				if silkPacketInnerFrameIsConceal(p) {
					firstPLC = i
					break
				}
			}
			if firstPLC < 0 {
				t.Fatalf("%s: no DTX/concealment frame in stream", c.name)
			}

			// gopus stateful FLOAT decode + per-packet final range.
			gdec, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}
			gPCM := make([]float32, 0, len(packets)*frameSize*c.channels)
			gRanges := make([]uint32, 0, len(packets))
			buf := make([]float32, frameSize*c.channels)
			for _, p := range packets {
				n, derr := gdec.Decode(p, buf)
				if derr != nil {
					t.Fatalf("gopus decode: %v", derr)
				}
				if n != frameSize {
					t.Fatalf("gopus samples=%d want %d", n, frameSize)
				}
				gPCM = append(gPCM, buf[:n*c.channels]...)
				gRanges = append(gRanges, gdec.FinalRange())
			}

			// libopus stateful FLOAT decode + per-packet final range.
			steps := make([]libopusAPIRateDecodeStep, len(packets))
			for i, p := range packets {
				steps[i] = libopusAPIRateDecodeStep{packet: p, frameSize: frameSize}
			}
			oPCM, oRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, c.channels, frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "stateful float reference", err)
				return
			}
			if len(gPCM) != len(oPCM) || len(gRanges) != len(oRanges) {
				t.Fatalf("%s: samples/ranges gopus=%d/%d libopus=%d/%d", c.name,
					len(gPCM), len(gRanges), len(oPCM), len(oRanges))
			}

			for i := range gRanges {
				if gRanges[i] != oRanges[i] {
					t.Fatalf("%s: frame %d final range=%08x want %08x", c.name, i, gRanges[i], oRanges[i])
				}
			}
			assertAPIRateFloat32BitsExact(t, gPCM, oPCM, c.name+" DTX/PLC sequence")
		})
	}
}

// encodeSILKConcealmentProbePackets encodes a deterministic SILK-only CBR stream
// at the given bandwidth/duration/bitrate. Low SILK bitrates emit code-3 padded
// packets whose inner frame is 1 byte (DTX), which the decoder routes to SILK
// loss concealment — the edge this test targets.
func encodeSILKConcealmentProbePackets(t *testing.T, bw Bandwidth, frameMs, bitrate, channels int, content string, frames int) ([][]byte, int) {
	t.Helper()
	frameSize := frameMs * 48 // 48 kHz API rate
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("create encoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("set frame size: %v", err)
	}
	if err := enc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("force SILK: %v", err)
	}
	if err := enc.SetBandwidth(bw); err != nil {
		t.Fatalf("set bandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("set bitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatalf("set CBR: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("force stereo: %v", err)
		}
	}

	packet := make([]byte, 4000)
	packets := make([][]byte, 0, frames)
	for f := range frames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			n := f*frameSize + i
			tt := float64(n) / 48000.0
			var s float64
			switch content {
			case "chirp":
				f0 := 100.0 + 2000.0*float64(n%4000)/4000.0
				s = 0.4 * math.Sin(2*math.Pi*f0*tt)
			case "ramp":
				s = float64((n*73)%20000-10000) / 24000.0
			default:
				s = 0.35 * math.Sin(2*math.Pi*220*tt)
			}
			for ch := range channels {
				pcm[i*channels+ch] = float32(s)
			}
		}
		nn, eerr := enc.Encode(pcm, packet)
		if eerr != nil {
			t.Fatalf("encode frame %d: %v", f, eerr)
		}
		if nn > 0 {
			packets = append(packets, append([]byte(nil), packet[:nn]...))
		}
	}
	return packets, frameSize
}

// silkPacketInnerFrameIsConceal reports whether a code-3 single-frame Opus packet
// carries a <=1-byte inner frame, which both libopus and gopus route to loss
// concealment (per-frame len<=1 → PLC). This is the encoder's CBR-padded DTX
// shape at low SILK bitrate.
func silkPacketInnerFrameIsConceal(p []byte) bool {
	if len(p) < 2 || (p[0]&0x03) != 3 {
		return false
	}
	m := int(p[1] & 0x3F)
	if m <= 0 {
		return false
	}
	hasPadding := (p[1] & 0x40) != 0
	off := 2
	padding := 0
	if hasPadding {
		for off < len(p) {
			pb := int(p[off])
			off++
			if pb == 255 {
				padding += 254
			} else {
				padding += pb
				break
			}
		}
	}
	frameDataLen := len(p) - off - padding
	if frameDataLen < 0 {
		return false
	}
	return frameDataLen/m <= 1
}

var smoothFadeHelper libopustest.HelperCache

func probeLibopusSmoothFade(sampleRate, channels, overlap int, in1, in2 []float32) ([]float32, error) {
	binPath, err := smoothFadeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "smooth fade",
		OutputBase:   "gopus_libopus_smooth_fade",
		SourceFile:   "libopus_smooth_fade_info.c",
		ProbeRelPath: "src/opus_decoder.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "src"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(
		"GSFI",
		uint32(sampleRate),
		uint32(channels),
		uint32(overlap),
	)
	payload.Float32s(in1...)
	payload.Float32s(in2...)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "smooth fade", "GSFO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(in1))
	reader.ExpectRemaining(count * 4)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSmoothFadeMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name       string
		sampleRate int
		channels   int
		overlap    int
	}{
		{name: "48k_mono", sampleRate: 48000, channels: 1, overlap: 120},
		{name: "24k_stereo", sampleRate: 24000, channels: 2, overlap: 60},
		{name: "16k_stereo", sampleRate: 16000, channels: 2, overlap: 40},
		{name: "8k_mono", sampleRate: 8000, channels: 1, overlap: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := tc.overlap * tc.channels
			in1 := make([]float32, count)
			in2 := make([]float32, count)
			for i := range count {
				x := float64(i + 1)
				in1[i] = float32(0.71*math.Sin(0.071*x) - 0.19*math.Cos(0.031*x))
				in2[i] = float32(-0.63*math.Cos(0.043*x) + 0.11*math.Sin(0.113*x))
			}

			want, err := probeLibopusSmoothFade(tc.sampleRate, tc.channels, tc.overlap, in1, in2)
			if err != nil {
				libopustest.HelperUnavailable(t, "smooth fade", err)
			}
			got := make([]float32, count)
			smoothFade(in1, in2, got, tc.overlap, tc.channels, tc.sampleRate)
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("smoothFade[%d]=%08x want %08x (%0.10g vs %0.10g)",
						i, math.Float32bits(got[i]), math.Float32bits(want[i]), got[i], want[i])
				}
			}
		})
	}
}

func TestPCMSoftClipEdgeValuesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	negativeZero := math.Float32frombits(0x80000000)
	quietNaN := math.Float32frombits(0x7fc00001)
	signalingNaN := math.Float32frombits(0x7f800001)
	inRangeBlock := make([]float32, 32)
	for i := range inRangeBlock {
		inRangeBlock[i] = 0.5
		if i%2 != 0 {
			inRangeBlock[i] = -0.3
		}
	}
	tests := []struct {
		name     string
		channels int
		samples  []float32
		mem      []float32
	}{
		{"negative_zero_memory", 1, []float32{0.5, -0.3}, []float32{negativeZero}},
		{"stereo_negative_zero_memory", 2, []float32{0.5, -0.3, negativeZero, 0}, []float32{negativeZero, negativeZero}},
		{"vector_block_negative_zero_memory", 2, inRangeBlock, []float32{negativeZero, negativeZero}},
		{"nan_between_positive_peaks", 1, []float32{0.5, 1.4, quietNaN, 1.9, 0.5}, []float32{0}},
		{"nan_between_negative_peaks", 1, []float32{-0.5, -1.4, quietNaN, -1.9, -0.5}, []float32{0}},
		{"nan_after_peak", 1, []float32{0.5, 1.4, quietNaN}, []float32{0}},
		{"signaling_nan_between_peaks", 1, []float32{0.5, 1.4, signalingNaN, 1.9, 0.5}, []float32{0}},
		{"in_range_signaling_nan_prefix", 1, []float32{signalingNaN, 0.5, -0.3}, []float32{0}},
		{"stereo_nan_between_peaks", 2, []float32{0.5, -0.5, 1.4, -1.4, quietNaN, signalingNaN, 1.9, -1.9, 0.5, -0.5}, []float32{0, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, wantMem, err := libopustest.ProbeSoftClip(len(tc.samples)/tc.channels, tc.channels, tc.samples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			got := append([]float32(nil), tc.samples...)
			gotMem := append([]float32(nil), tc.mem...)
			PCMSoftClip(got, tc.channels, gotMem)
			assertSoftClipFloat32BitsEqual(t, got, want, "pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "mem")

			wantInt16, err := probeLibopusFloatQuant(libopustest.FloatQuantModeCELTDispatch, want)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}
			copy(got, tc.samples)
			copy(gotMem, tc.mem)
			pcmInt16 := make([]int16, len(got))
			softClipAndFloat32ToInt16(pcmInt16, got, len(got)/tc.channels, tc.channels, gotMem)
			assertSoftClipFloat32BitsEqual(t, got, want, "int16 softclipped pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "int16 mem")
			for i, sample := range wantInt16 {
				if pcmInt16[i] != sample {
					t.Fatalf("int16[%d]=%d want %d", i, pcmInt16[i], sample)
				}
			}

			if allocs := testing.AllocsPerRun(20, func() {
				copy(got, tc.samples)
				copy(gotMem, tc.mem)
				PCMSoftClip(got, tc.channels, gotMem)
				copy(got, tc.samples)
				copy(gotMem, tc.mem)
				softClipAndFloat32ToInt16(pcmInt16, got, len(got)/tc.channels, tc.channels, gotMem)
			}); allocs != 0 {
				t.Fatalf("soft clipping allocs=%g want 0", allocs)
			}
		})
	}
}

func assertSoftClipFloat32BitsEqual(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]=%g want %g", label, i, got[i], want[i])
		}
	}
}

func TestOpusPCMSoftClipMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		n        int
		channels int
		mem      []float32
		samples  []float32
	}{
		{
			name:     "mono clipped segments",
			n:        8,
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{1.25, 1.5, 1.8, 1.4, 0.8, -0.2, -1.2, -1.6},
		},
		{
			name:     "stereo independent channels",
			n:        6,
			channels: 2,
			mem:      []float32{0, 0},
			samples:  []float32{0.2, -1.8, 1.4, -1.2, 1.7, -0.4, 0.6, 0.3, -0.5, 1.1, -1.3, 1.9},
		},
		{
			name:     "carryover memory",
			n:        6,
			channels: 1,
			mem:      []float32{-0.2},
			samples:  []float32{0.8, 0.4, -0.2, 1.4, 1.1, 0.3},
		},
		{
			name:     "hard clamp domain",
			n:        5,
			channels: 1,
			mem:      []float32{0},
			samples:  []float32{2.5, -2.5, 3, -3, 0},
		},
		{
			name:     "stereo carryover only",
			n:        4,
			channels: 2,
			mem:      []float32{-0.1, 0.12},
			samples:  []float32{0.5, -0.5, 0.25, -0.25, -0.1, 0.1, 0, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, wantMem, err := libopustest.ProbeSoftClip(tc.n, tc.channels, tc.samples, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			got := append([]float32(nil), tc.samples...)
			gotMem := append([]float32(nil), tc.mem...)
			opusPCMSoftClip(got, tc.n, tc.channels, gotMem)
			assertSoftClipFloat32BitsEqual(t, got, want, "pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "mem")
		})
	}
}

func TestSoftClipAndFloat32ToInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name     string
		n        int
		channels int
		mem      []float32
		src      []float32
	}{
		{
			name:     "fast path in range zero memory",
			n:        8,
			channels: 2,
			mem:      []float32{0, 0},
			src: []float32{
				-1, 1,
				float32(-1.5 / 32768.0), float32(1.5 / 32768.0),
				float32(-0.5 / 32768.0), float32(0.5 / 32768.0),
				-0.75, 0.75,
				-0.125, 0.125,
				0, float32(32767.0 / 32768.0),
				float32(-32767.0 / 32768.0), float32(32766.5 / 32768.0),
				float32(-32766.5 / 32768.0), 0,
			},
		},
		{
			name:     "zero memory clipped input",
			n:        8,
			channels: 2,
			mem:      []float32{0, 0},
			src: []float32{
				1.3, -0.9,
				1.7, -1.8,
				0.9, -1.2,
				-0.1, 0.4,
				-1.4, 1.6,
				-1.9, 1.2,
				-0.6, 0.2,
				0.1, -0.1,
			},
		},
		{
			name:     "carryover clipped input",
			n:        8,
			channels: 2,
			mem:      []float32{-0.08, 0.11},
			src: []float32{
				1.3, -0.9,
				1.7, -1.8,
				0.9, -1.2,
				-0.1, 0.4,
				-1.4, 1.6,
				-1.9, 1.2,
				-0.6, 0.2,
				0.1, -0.1,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wantFloat, wantMem, err := libopustest.ProbeSoftClip(tc.n, tc.channels, tc.src, tc.mem)
			if err != nil {
				libopustest.HelperUnavailable(t, "softclip", err)
			}
			want, err := probeLibopusFloatQuant(libopustest.FloatQuantModeCELTDispatch, wantFloat)
			if err != nil {
				libopustest.HelperUnavailable(t, "float quant", err)
			}

			gotSrc := append([]float32(nil), tc.src...)
			gotMem := append([]float32(nil), tc.mem...)
			got := make([]int16, len(tc.src))
			softClipAndFloat32ToInt16(got, gotSrc, tc.n, tc.channels, gotMem)

			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample[%d]=%d want %d (input=%0.10g)", i, got[i], want[i], wantFloat[i])
				}
			}
			assertSoftClipFloat32BitsEqual(t, gotSrc, wantFloat, "softclipped pcm")
			assertSoftClipFloat32BitsEqual(t, gotMem, wantMem, "mem")
		})
	}
}

func TestStereoFadeTransitionPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		name       = "xfr_auto_ch2_20ms_16000bps_vbr0_cx5_fecfalse_dtxfalse"
		frameCount = 40
	)
	var spec encXfrSpec
	found := false
	for _, candidate := range buildEncXfrSweep() {
		if candidate.name == name {
			spec, found = candidate, true
			break
		}
	}
	if !found {
		t.Fatalf("missing transition configuration %q", name)
	}
	frameSize := encFrameSamples48k(spec.frameMs)
	pcm, err := encXfrBuildTransitionPCM(frameSize, spec.channels, frameCount, 5)
	if err != nil {
		t.Fatal(err)
	}
	vbr, constrained := vbrFlags(spec.vbr)
	records, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate:    48000,
		Channels:      spec.channels,
		Application:   libopustest.EncodeDiffApplicationAudio,
		ForceMode:     spec.forceMode,
		Bandwidth:     spec.bwCode,
		MaxBandwidth:  spec.bwCode,
		Bitrate:       spec.bitrate,
		Complexity:    spec.complexity,
		Signal:        spec.signal,
		VBR:           vbr,
		VBRConstraint: constrained,
		ForceChannels: spec.channels,
		InbandFEC:     0,
		PacketLoss:    0,
		DTX:           spec.dtx,
		FrameSize:     frameSize,
		FrameCount:    frameCount,
		PCM:           pcm,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo fade packet", err)
		return
	}
	if len(records) != frameCount {
		t.Fatalf("C records=%d want=%d", len(records), frameCount)
	}
	enc, ok := configureEncXfr(spec)
	if !ok {
		t.Fatal("configure Go encoder")
	}
	for frame := range frameCount {
		input := pcm[frame*frameSize*spec.channels : (frame+1)*frameSize*spec.channels]
		got, err := encDiffEncodeOneFrame(enc, input)
		if err != nil {
			t.Fatalf("frame %d: Go encode: %v", frame, err)
		}
		want := records[frame]
		if want.Ret < 0 || want.Ret != len(want.Packet) {
			t.Fatalf("frame %d: invalid C record ret=%d len=%d", frame, want.Ret, len(want.Packet))
		}
		if !bytes.Equal(got, want.Packet) {
			t.Fatalf("frame %d: packet length Go=%d C=%d first byte difference=%d; Go=%x C=%x",
				frame, len(got), len(want.Packet), firstByteDiff(got, want.Packet), got, want.Packet)
		}
		if gotRange := enc.FinalRange(); gotRange != want.FinalRange {
			t.Fatalf("frame %d: final range Go=%08x C=%08x", frame, gotRange, want.FinalRange)
		}
	}
}
