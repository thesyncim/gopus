package gopus

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"runtime"
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
