//go:build gopus_custom

// oracle_test.go provides bit/sample-exact parity tests for the Opus Custom API
// against a libopus build configured with --enable-custom-modes.
//
// The reference tree is built on demand by libopustest.BuildCHelper with
// CustomRef set, which drives tools/ensure_libopus.sh LIBOPUS_ENABLE_CUSTOM=1
// (-> tmp_check/opus-1.6.1-custom) and links the oracle against its
// .libs/libopus.a. The oracle (tools/csrc/libopus_custom_oracle.c) creates an
// OpusCustomMode for each (Fs, frame_size), encodes one frame, decodes the
// resulting packet, and returns the packet bytes plus decoded PCM.
//
// Standard 48 kHz modes (120/240/480/960) reuse the static modes and are
// expected to be byte/sample-exact. Non-standard rates exercise gopus
// celt/custom for genuinely custom band layouts; these tests record whether
// gopus output actually matches a libopus custom-modes build or only its own
// round trip.
package custom_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var customOracleHelper libopustest.HelperCache

// customOracleHelperPath builds (once) the C oracle linked against the
// custom-modes libopus reference tree.
func customOracleHelperPath() (string, error) {
	return customOracleHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "opus custom",
		OutputBase:  "gopus_libopus_custom",
		SourceFile:  "libopus_custom_oracle.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src", "include"},
		Libs:        []string{libopustest.CustomRefPath(".libs", "libopus.a"), "-lm"},
		CustomRef:   true,
		DeadStrip:   true,
	})
}

type oracleCase struct {
	fs, frameSize, channels, maxBytes int
	pcm                               []float32
}

type oracleResult struct {
	status   int32
	encRange uint32
	decRange uint32
	packet   []byte
	decoded  []float32

	// Mode geometry from opus_custom_mode_create (see oracle protocol).
	overlap       int32
	nbEBands      int32
	effEBands     int32
	maxLM         int32
	nbShortMdcts  int32
	shortMdctSize int32
	preemph       [4]float32
	eBands        []int32
	logN          []int32
}

// runCustomOracle drives the libopus custom-modes oracle. Protocol matches
// tools/csrc/libopus_custom_oracle.c.
func runCustomOracle(t *testing.T, cases []oracleCase) []oracleResult {
	t.Helper()
	binPath, err := customOracleHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "opus custom", err)
		return nil
	}

	var req bytes.Buffer
	req.WriteString("GCCO")
	writeU32(&req, uint32(len(cases)))
	for _, c := range cases {
		writeU32(&req, uint32(c.fs))
		writeU32(&req, uint32(c.frameSize))
		writeU32(&req, uint32(c.channels))
		writeU32(&req, uint32(c.maxBytes))
		writeU32(&req, uint32(len(c.pcm)))
		for _, s := range c.pcm {
			writef32(&req, s)
		}
	}

	out, err := libopustest.RunHelper(binPath, req.Bytes())
	if err != nil {
		t.Fatalf("oracle exec: %v", err)
	}

	r := bytes.NewReader(out)
	magic := make([]byte, 4)
	if _, err := r.Read(magic); err != nil || string(magic) != "GCCO" {
		t.Fatalf("oracle bad magic: %q", magic)
	}
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		t.Fatalf("oracle read count: %v", err)
	}
	if int(n) != len(cases) {
		t.Fatalf("oracle returned %d results, want %d", n, len(cases))
	}
	results := make([]oracleResult, n)
	for i := range results {
		var status int32
		if err := binary.Read(r, binary.LittleEndian, &status); err != nil {
			t.Fatalf("oracle result[%d] status: %v", i, err)
		}
		var encRange, decRange, pktLen uint32
		if err := binary.Read(r, binary.LittleEndian, &encRange); err != nil {
			t.Fatalf("oracle result[%d] encRange: %v", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &decRange); err != nil {
			t.Fatalf("oracle result[%d] decRange: %v", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &pktLen); err != nil {
			t.Fatalf("oracle result[%d] pktLen: %v", i, err)
		}
		pkt := make([]byte, pktLen)
		if _, err := r.Read(pkt); pktLen > 0 && err != nil {
			t.Fatalf("oracle result[%d] packet: %v", i, err)
		}
		var nDec uint32
		if err := binary.Read(r, binary.LittleEndian, &nDec); err != nil {
			t.Fatalf("oracle result[%d] nDecoded: %v", i, err)
		}
		dec := make([]float32, nDec)
		for j := range dec {
			var bits uint32
			if err := binary.Read(r, binary.LittleEndian, &bits); err != nil {
				t.Fatalf("oracle result[%d] decoded[%d]: %v", i, j, err)
			}
			dec[j] = math.Float32frombits(bits)
		}

		res := oracleResult{status: status, encRange: encRange, decRange: decRange, packet: pkt, decoded: dec}
		readI32 := func(field string) int32 {
			var v int32
			if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
				t.Fatalf("oracle result[%d] %s: %v", i, field, err)
			}
			return v
		}
		res.overlap = readI32("overlap")
		res.nbEBands = readI32("nbEBands")
		res.effEBands = readI32("effEBands")
		res.maxLM = readI32("maxLM")
		res.nbShortMdcts = readI32("nbShortMdcts")
		res.shortMdctSize = readI32("shortMdctSize")
		for j := range res.preemph {
			var bits uint32
			if err := binary.Read(r, binary.LittleEndian, &bits); err != nil {
				t.Fatalf("oracle result[%d] preemph[%d]: %v", i, j, err)
			}
			res.preemph[j] = math.Float32frombits(bits)
		}
		nEdges := readI32("nEBandEdges")
		res.eBands = make([]int32, nEdges)
		for j := range res.eBands {
			res.eBands[j] = readI32("eBands")
		}
		nLogN := readI32("nLogN")
		res.logN = make([]int32, nLogN)
		for j := range res.logN {
			res.logN[j] = readI32("logN")
		}
		results[i] = res
	}
	return results
}

// gopusEncode runs the gopus celt/custom encoder for a case, mirroring the
// oracle's encoder configuration.
func gopusEncode(t *testing.T, tc oracleCase) ([]byte, *custom.CustomEncoder) {
	t.Helper()
	mode, err := custom.NewMode(tc.fs, tc.frameSize)
	if err != nil {
		t.Fatalf("NewMode(%d,%d): %v", tc.fs, tc.frameSize, err)
	}
	enc, err := custom.NewEncoder(mode, tc.channels)
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	_ = enc.SetVBR(false)
	_ = enc.SetConstrainedVBR(false)
	_ = enc.SetComplexity(9)
	_ = enc.SetLSBDepth(16)
	got, err := enc.EncodeFloat(tc.pcm, tc.maxBytes)
	if err != nil {
		t.Fatalf("EncodeFloat: %v", err)
	}
	return got, enc
}

// TestOracleParityStandardModes checks that the four standard 48 kHz frame
// sizes match libopus custom encode+decode byte/sample-for-byte. These reuse
// the static modes so parity is expected to be exact.
func TestOracleParityStandardModes(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}
	const maxBytes = 200

	var cases []oracleCase
	for _, sz := range frameSizes {
		cases = append(cases, oracleCase{48000, sz, 1, maxBytes, generateSine(440.0, 48000, sz)})
	}

	results := runCustomOracle(t, cases)
	for i, tc := range cases {
		if results[i].status < 0 {
			t.Fatalf("case %d (48000/%d): oracle encode failed status=%d", i, tc.frameSize, results[i].status)
		}
		got, _ := gopusEncode(t, tc)
		if !bytes.Equal(got, results[i].packet) {
			t.Errorf("case %d (48000/%d): packet mismatch\n  got  (%d): %x\n  want (%d): %x",
				i, tc.frameSize, len(got), got, len(results[i].packet), results[i].packet)
			continue
		}

		mode, _ := custom.NewMode(tc.fs, tc.frameSize)
		dec, err := custom.NewDecoder(mode, tc.channels)
		if err != nil {
			t.Fatalf("case %d NewDecoder: %v", i, err)
		}
		decoded, err := dec.DecodeFloat(results[i].packet, tc.frameSize)
		if err != nil {
			t.Fatalf("case %d DecodeFloat: %v", i, err)
		}
		if len(decoded) != len(results[i].decoded) {
			t.Errorf("case %d (48000/%d): decoded length %d, libopus %d", i, tc.frameSize, len(decoded), len(results[i].decoded))
		} else if d := firstSampleDivergence(decoded, results[i].decoded); d >= 0 {
			t.Errorf("case %d (48000/%d): decoded PCM diverges at sample %d (gopus=%v libopus=%v)",
				i, tc.frameSize, d, decoded[d], results[i].decoded[d])
		} else {
			t.Logf("case %d (48000/%d): %d-byte packet + %d samples exact", i, tc.frameSize, len(got), len(decoded))
		}
	}
}

// nonStandardCases enumerates several (Fs, frame_size) combinations libopus
// allows for custom modes that are NOT one of the four 48 kHz static modes.
// frame_size must be even, 40..1024, frame_size*1000 >= Fs, and the short block
// <= 3.3 ms (matches opus_custom_mode_create validation).
func nonStandardCases() []oracleCase {
	const maxBytes = 200
	specs := []struct{ fs, frameSize int }{
		{48000, 640}, // 48 kHz, non-power-of-two frame
		{44100, 882}, // 20 ms at 44.1 kHz
		{32000, 640}, // 20 ms at 32 kHz
		{24000, 480}, // 20 ms at 24 kHz
		{16000, 320}, // 20 ms at 16 kHz
		{8000, 160},  // 20 ms at 8 kHz
		{12000, 240}, // 20 ms at 12 kHz
	}
	var cases []oracleCase
	for _, s := range specs {
		if s.frameSize%2 != 0 {
			continue
		}
		pcm := generateSine(440.0, float64(s.fs), s.frameSize)
		cases = append(cases, oracleCase{s.fs, s.frameSize, 1, maxBytes, pcm})
	}
	return cases
}

// TestOracleParityNonStandardModes documents the non-standard custom-mode gap.
//
// For each non-standard (Fs, frame_size) it confirms that:
//  1. libopus --enable-custom-modes accepts the mode and produces a packet
//     (the oracle status is non-negative), and
//  2. gopus celt/custom correctly declines the mode with ErrNonStandard rather
//     than silently emitting a non-conformant bitstream.
//
// gopus does not yet reproduce a libopus custom-modes bitstream for arbitrary
// (Fs, frame_size): the CELT core threads the 48 kHz frame-size grid through
// band-bin scaling (celt.ScaledBandStart = eBand*frameSize/120), the mode config
// (celt.GetModeConfig / celt.ValidFrameSize accept only 120/240/480/960/1920),
// the overlap constant, and pre-emphasis. So even the Fs==400*shortMdctSize
// family (which shares the 48 kHz eBands/logN/cache/allocVectors tables) needs
// the overlap, MDCT length, effEBands clamp and per-rate pre-emphasis wired
// through the whole encode+decode driver before it can match libopus.
//
// To make the gap concrete, the subtest logs the libopus reference packet so a
// future native path can diff against it.
func TestOracleParityNonStandardModes(t *testing.T) {
	cases := nonStandardCases()
	results := runCustomOracle(t, cases)

	for i, tc := range cases {
		t.Run(fmt.Sprintf("Fs%d_frame%d", tc.fs, tc.frameSize), func(t *testing.T) {
			if results[i].status < 0 {
				t.Skipf("libopus rejected custom mode (Fs=%d frame=%d) status=%d", tc.fs, tc.frameSize, results[i].status)
			}

			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatalf("NewMode(%d,%d): %v", tc.fs, tc.frameSize, err)
			}
			if mode.IsStandard() {
				t.Fatalf("mode Fs=%d frame=%d unexpectedly flagged standard", tc.fs, tc.frameSize)
			}

			enc, err := custom.NewEncoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			if _, err := enc.EncodeFloat(tc.pcm, tc.maxBytes); !errors.Is(err, custom.ErrNonStandard) {
				t.Errorf("EncodeFloat non-standard mode: err = %v, want ErrNonStandard", err)
			}

			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			if _, err := dec.DecodeFloat(results[i].packet, tc.frameSize); !errors.Is(err, custom.ErrNonStandard) {
				t.Errorf("DecodeFloat non-standard mode: err = %v, want ErrNonStandard", err)
			}

			t.Logf("libopus custom reference (Fs=%d frame=%d): %d-byte packet, %d decoded samples (gopus declines: ErrNonStandard)",
				tc.fs, tc.frameSize, len(results[i].packet), len(results[i].decoded))
		})
	}
}

// TestOracleControlPlaneScaledBandFamily verifies that, for the
// Fs==400*shortMdctSize family, the gopus celt/custom control plane reproduces
// the libopus opus_custom_mode_create geometry exactly: the same short-MDCT
// decomposition (maxLM, nbShortMdcts, shortMdctSize), overlap, band edges
// (eBands), effEBands, logN and per-rate pre-emphasis. It also checks the new
// celt.ScaledBandStartBase / ScaledBandEndBase band-bin scaling against the
// libopus invariant eBands[i] << LM for the full (long-block) frame.
//
// This is the control-plane slice of the native Opus Custom path: the
// data-plane (overlap-add MDCT analysis/synthesis, windowing) is not yet wired,
// so EncodeFloat/DecodeFloat still decline these modes with ErrNonStandard.
func TestOracleControlPlaneScaledBandFamily(t *testing.T) {
	const maxBytes = 200
	// Family members: Fs == 400*shortMdctSize, all non-standard.
	specs := []struct{ fs, frameSize int }{
		{32000, 640},
		{24000, 480},
		{16000, 320},
		{12000, 240},
		{8000, 160},
	}
	var cases []oracleCase
	for _, s := range specs {
		cases = append(cases, oracleCase{s.fs, s.frameSize, 1, maxBytes, generateSine(440.0, float64(s.fs), s.frameSize)})
	}

	results := runCustomOracle(t, cases)
	for i, tc := range cases {
		t.Run(fmt.Sprintf("Fs%d_frame%d", tc.fs, tc.frameSize), func(t *testing.T) {
			if results[i].status < 0 {
				t.Skipf("libopus rejected custom mode (Fs=%d frame=%d) status=%d", tc.fs, tc.frameSize, results[i].status)
			}
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatalf("NewMode(%d,%d): %v", tc.fs, tc.frameSize, err)
			}
			if !mode.InScaledBandFamily() {
				t.Fatalf("mode Fs=%d frame=%d not flagged in scaled-band family", tc.fs, tc.frameSize)
			}

			r := results[i]
			if int32(mode.ShortMdctSize) != r.shortMdctSize {
				t.Errorf("shortMdctSize: gopus=%d libopus=%d", mode.ShortMdctSize, r.shortMdctSize)
			}
			if int32(mode.NbShortMdcts) != r.nbShortMdcts {
				t.Errorf("nbShortMdcts: gopus=%d libopus=%d", mode.NbShortMdcts, r.nbShortMdcts)
			}
			if int32(mode.MaxLM) != r.maxLM {
				t.Errorf("maxLM: gopus=%d libopus=%d", mode.MaxLM, r.maxLM)
			}
			if int32(mode.Overlap) != r.overlap {
				t.Errorf("overlap: gopus=%d libopus=%d", mode.Overlap, r.overlap)
			}
			if int32(mode.NbEBands) != r.nbEBands {
				t.Errorf("nbEBands: gopus=%d libopus=%d", mode.NbEBands, r.nbEBands)
			}
			if int32(mode.EffEBands) != r.effEBands {
				t.Errorf("effEBands: gopus=%d libopus=%d", mode.EffEBands, r.effEBands)
			}
			for j := range mode.Preemph {
				if mode.Preemph[j] != r.preemph[j] {
					t.Errorf("preemph[%d]: gopus=%v libopus=%v", j, mode.Preemph[j], r.preemph[j])
				}
			}
			if len(mode.EBands) != len(r.eBands) {
				t.Fatalf("eBands length: gopus=%d libopus=%d", len(mode.EBands), len(r.eBands))
			}
			for j := range mode.EBands {
				if int32(mode.EBands[j]) != r.eBands[j] {
					t.Errorf("eBands[%d]: gopus=%d libopus=%d", j, mode.EBands[j], r.eBands[j])
				}
			}
			if len(mode.LogN) != len(r.logN) {
				t.Fatalf("logN length: gopus=%d libopus=%d", len(mode.LogN), len(r.logN))
			}
			for j := range mode.LogN {
				if int32(mode.LogN[j]) != r.logN[j] {
					t.Errorf("logN[%d]: gopus=%d libopus=%d", j, mode.LogN[j], r.logN[j])
				}
			}

			// Band-bin scaling: the long-block coefficient index for band edge j
			// must equal eBands[j] << LM (== eBands[j] * nbShortMdcts), matching
			// libopus celt/bands.c. ScaledBandStartBase reproduces this when fed
			// the family's base short-MDCT size.
			lm := mode.MaxLM
			for band := 0; band < mode.NbEBands; band++ {
				wantStart := int(r.eBands[band]) << lm
				wantEnd := int(r.eBands[band+1]) << lm
				gotStart := celt.ScaledBandStartBase(band, mode.FrameSize, mode.ShortMdctSize)
				gotEnd := celt.ScaledBandEndBase(band, mode.FrameSize, mode.ShortMdctSize)
				if gotStart != wantStart {
					t.Errorf("ScaledBandStartBase(band=%d): got %d want %d", band, gotStart, wantStart)
				}
				if gotEnd != wantEnd {
					t.Errorf("ScaledBandEndBase(band=%d): got %d want %d", band, gotEnd, wantEnd)
				}
			}
		})
	}
}

// --- helpers ------------------------------------------------------------------

func firstSampleDivergence(a, b []float32) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func writeU32(b *bytes.Buffer, v uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	b.Write(buf[:])
}

func writef32(b *bytes.Buffer, v float32) {
	writeU32(b, math.Float32bits(v))
}
