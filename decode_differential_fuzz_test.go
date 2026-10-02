// decode_differential_fuzz_test.go — differential fuzz harness comparing gopus
// decode against the libopus C decoder oracle over a large seeded space of
// generated and structured-malformed Opus packets.
//
// Two complementary strategies (both seeded + CI-budgeted):
//
//   (a) ENCODE-then-DECODE: gopus encodes deterministic + random PCM across the
//       config space (mode/bandwidth/frame duration/bitrate/channels/FEC/DTX),
//       producing valid packets, then decodes each through both gopus and the
//       paired libopus oracle and asserts exact output lengths and PCM bits.
//
//   (b) STRUCTURED-MALFORMED: seeded mutations of valid packets (truncate, TOC
//       flip, frame-length corruption, padding edge cases, code-0/1/2/3
//       boundaries) are decoded through both. The invariant is AGREEMENT: either
//       both reject (gopus error class ↔ libopus negative code) or both accept
//       with identical PCM. A gopus accept-where-libopus-rejects (or vice versa)
//       or a panic is a divergence to root-cause.
//
// Both decode each packet through a FRESH decoder (oracle isolates per case; the
// Go side makes a new Decoder per case) so any failure minimises to one packet.
//
// Run with GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 for the full sweep.
//
// Scope: this harness hardens the DECODE path. Strategy (a) drives gopus's own
// encoder to produce inputs; its final count assertion requires every selected
// config to produce the expected packets and every format to decode exactly.

package gopus

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// diffFuzzBudget returns the per-stage iteration budget, shrunk under -short so
// the harness stays CI-friendly while remaining a substantial sweep otherwise.
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

// diffMode classifies a TOC byte's coding mode for tolerance selection.
type diffMode int

const (
	diffModeSILK diffMode = iota
	diffModeHybrid
	diffModeCELT
)

func tocMode(toc byte) diffMode {
	config := int(toc >> 3)
	switch {
	case config < 12:
		return diffModeSILK
	case config < 16:
		return diffModeHybrid
	default:
		return diffModeCELT
	}
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
