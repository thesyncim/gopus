//go:build gopus_custom_modes

// oracle_test.go provides bit/sample-exact parity tests for the Opus Custom API
// against a libopus build configured with --enable-custom-modes.
//
// The reference tree is built on demand by libopustest.BuildPublicAPIHelper
// with the current custom feature and instruction configuration. The oracle
// (tools/csrc/libopus_custom_oracle.c) creates an
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
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var customOracleHelper libopustest.HelperCache

// customOracleHelperPath builds (once) the C oracle linked against the
// custom-modes libopus reference tree.
func customOracleHelperPath() (string, error) {
	return customOracleHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "opus custom",
			OutputBase:  "gopus_libopus_custom",
			SourceFile:  "libopus_custom_oracle.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
}

type oracleCase struct {
	fs, frameSize, channels, maxBytes int
	pcm                               []float32
}

type oracleResult struct {
	status        int32
	encRange      uint32
	decRange      uint32
	packet        []byte
	decoded       []float32
	shortDecRange uint32
	decodedShort  []int16

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
	allocVectors  []int32
	cacheIndex    []int32
	cacheBits     []int32
	cacheCaps     []int32
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
		readI32Slice := func(name string) []int32 {
			n := readI32(name)
			s := make([]int32, n)
			for j := range s {
				s[j] = readI32(name)
			}
			return s
		}
		res.allocVectors = readI32Slice("allocVectors")
		res.cacheIndex = readI32Slice("cacheIndex")
		res.cacheBits = readI32Slice("cacheBits")
		res.cacheCaps = readI32Slice("cacheCaps")
		var shortCount uint32
		if err := binary.Read(r, binary.LittleEndian, &res.shortDecRange); err != nil {
			t.Fatalf("oracle result[%d] short range: %v", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &shortCount); err != nil {
			t.Fatalf("oracle result[%d] short length: %v", i, err)
		}
		if shortCount > uint32(cases[i].frameSize*cases[i].channels) {
			t.Fatalf("oracle result[%d] short length exceeds output capacity: %d", i, shortCount)
		}
		res.decodedShort = make([]int16, shortCount)
		if err := binary.Read(r, binary.LittleEndian, res.decodedShort); err != nil {
			t.Fatalf("oracle result[%d] short PCM: %v", i, err)
		}
		results[i] = res
	}
	if r.Len() != 0 {
		t.Fatalf("oracle response has %d trailing bytes", r.Len())
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
		if dec.FinalRange() != results[i].decRange {
			t.Fatalf("case%d decode range=%08x want=%08x", i, dec.FinalRange(), results[i].decRange)
		}
		assertCustomDecodeExact(t, fmt.Sprintf("standard/%d", tc.frameSize), decoded, results[i].decoded)
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

// TestOracleParityNonStandardModes checks exact packets, ranges and PCM against the paired live C build.
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
			if mode.InScaledBandFamily() {
				t.Skipf("Fs=%d frame=%d is in the scaled-band family (covered by TestOracleParityScaledBandFamily)", tc.fs, tc.frameSize)
			}

			enc, err := custom.NewEncoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewEncoder: %v", err)
			}
			packet, err := enc.EncodeFloat(tc.pcm, tc.maxBytes)
			if err != nil {
				t.Fatalf("EncodeFloat: %v", err)
			}
			if enc.FinalRange() != results[i].encRange {
				t.Errorf("Fs=%d frame=%d: encoder final range gopus=%08x libopus=%08x",
					tc.fs, tc.frameSize, enc.FinalRange(), results[i].encRange)
			} else if !bytes.Equal(packet, results[i].packet) {
				t.Errorf("Fs=%d frame=%d: encoded packet differs from libopus",
					tc.fs, tc.frameSize)
			}

			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(results[i].packet, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if dec.FinalRange() != results[i].decRange {
				t.Fatalf("decoder range=%08x want=%08x", dec.FinalRange(), results[i].decRange)
			}
			if len(decoded) != len(results[i].decoded) {
				t.Fatalf("Fs=%d frame=%d: decoded length gopus=%d libopus=%d",
					tc.fs, tc.frameSize, len(decoded), len(results[i].decoded))
			}
			assertCustomDecodeExact(t,
				fmt.Sprintf("Fs=%d frame=%d", tc.fs, tc.frameSize), decoded, results[i].decoded)
		})
	}
}

// TestOracleParityNonStandardStereo checks exact packets, ranges and PCM against the paired live C build.
func TestOracleParityNonStandardStereo(t *testing.T) {
	const maxBytes = 400
	specs := []struct{ fs, frameSize int }{
		{48000, 640}, // 48 kHz, non-power-of-two frame, nbEBands=19
	}
	var cases []oracleCase
	for _, s := range specs {
		pcm := generateSineStereo(440.0, 523.25, float64(s.fs), s.frameSize)
		cases = append(cases, oracleCase{s.fs, s.frameSize, 2, maxBytes, pcm})
	}
	results := runCustomOracle(t, cases)

	for i, tc := range cases {
		t.Run(fmt.Sprintf("Fs%d_frame%d_stereo", tc.fs, tc.frameSize), func(t *testing.T) {
			if results[i].status < 0 {
				t.Skipf("libopus rejected custom mode (Fs=%d frame=%d ch=2) status=%d", tc.fs, tc.frameSize, results[i].status)
			}
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatalf("NewMode(%d,%d): %v", tc.fs, tc.frameSize, err)
			}
			if mode.IsStandard() || mode.InScaledBandFamily() {
				t.Fatalf("Fs=%d frame=%d unexpectedly standard/scaled-family", tc.fs, tc.frameSize)
			}

			got, enc := gopusEncode(t, tc)
			if enc.FinalRange() != results[i].encRange {
				t.Errorf("Fs=%d frame=%d stereo: encoder final range gopus=%08x libopus=%08x",
					tc.fs, tc.frameSize, enc.FinalRange(), results[i].encRange)
			} else if !bytes.Equal(got, results[i].packet) {
				t.Errorf("Fs=%d frame=%d stereo: encoded packet differs from libopus",
					tc.fs, tc.frameSize)
			}

			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(results[i].packet, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if dec.FinalRange() != results[i].decRange {
				t.Fatalf("decoder range=%08x want=%08x", dec.FinalRange(), results[i].decRange)
			}
			if len(decoded) != len(results[i].decoded) {
				t.Fatalf("Fs=%d frame=%d stereo: decoded length gopus=%d libopus=%d",
					tc.fs, tc.frameSize, len(decoded), len(results[i].decoded))
			}
			assertCustomDecodeExact(t,
				fmt.Sprintf("Fs=%d frame=%d stereo", tc.fs, tc.frameSize), decoded, results[i].decoded)
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
// This is the control-plane slice of the native Opus Custom path; the matching
// data-plane encode/decode byte/sample parity for the family is covered by
// TestOracleParityScaledBandFamily.
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

// scaledBandFamilyCases enumerates the Fs==400*shortMdctSize non-standard
// custom modes that gopus reproduces natively (20 ms frames sharing the 48 kHz
// eBands/logN/allocVectors tables). All are mono CBR at the full per-frame
// budget, matching the oracle configuration.
func scaledBandFamilyCases() []oracleCase {
	const maxBytes = 200
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
	return cases
}

// TestOracleParityScaledBandFamily checks exact packets, ranges and PCM against the paired live C build.
func TestOracleParityScaledBandFamily(t *testing.T) {
	cases := scaledBandFamilyCases()
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
				t.Fatalf("Fs=%d frame=%d not flagged in scaled-band family", tc.fs, tc.frameSize)
			}

			got, enc := gopusEncode(t, tc)
			if enc.FinalRange() != results[i].encRange {
				t.Errorf("Fs=%d frame=%d: encoder final range gopus=%08x libopus=%08x",
					tc.fs, tc.frameSize, enc.FinalRange(), results[i].encRange)
			}
			if !bytes.Equal(got, results[i].packet) {
				t.Errorf("Fs=%d frame=%d: encoded packet differs from libopus", tc.fs, tc.frameSize)
			}

			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(results[i].packet, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if dec.FinalRange() != results[i].decRange {
				t.Fatalf("decoder range=%08x want=%08x", dec.FinalRange(), results[i].decRange)
			}
			if len(decoded) != len(results[i].decoded) {
				t.Fatalf("Fs=%d frame=%d: decoded length gopus=%d libopus=%d",
					tc.fs, tc.frameSize, len(decoded), len(results[i].decoded))
			}
			assertCustomDecodeExact(t,
				fmt.Sprintf("Fs=%d frame=%d", tc.fs, tc.frameSize), decoded, results[i].decoded)
		})
	}
}

// TestOracleControlPlaneNonStandard verifies that, for genuinely custom band
// layouts outside the Fs==400*shortMdctSize family (e.g. 48000/640), the gopus
// celt/custom mode-create control plane reproduces libopus
// opus_custom_mode_create() exactly: the per-mode band edges (eBands), logN,
// allocVectors (compute_allocation_table) and pulse cache index/bits/caps
// (compute_pulse_cache). These tables are the prerequisite for byte/sample-exact
// native encode/decode of such modes.
//
// Reference: libopus celt/modes.c compute_ebands/compute_allocation_table,
// celt/rate.c compute_pulse_cache.
func TestOracleControlPlaneNonStandard(t *testing.T) {
	const maxBytes = 200
	specs := []struct{ fs, frameSize int }{
		{48000, 640},
		{44100, 882},
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
			if mode.InScaledBandFamily() || mode.IsStandard() {
				t.Fatalf("Fs=%d frame=%d unexpectedly standard/scaled-family", tc.fs, tc.frameSize)
			}
			r := results[i]

			if len(mode.EBands) != len(r.eBands) {
				t.Fatalf("eBands length gopus=%d oracle=%d", len(mode.EBands), len(r.eBands))
			}
			for j := range mode.EBands {
				if int32(mode.EBands[j]) != r.eBands[j] {
					t.Errorf("eBands[%d]: gopus=%d oracle=%d", j, mode.EBands[j], r.eBands[j])
				}
			}
			for j := range mode.LogN {
				if int32(mode.LogN[j]) != r.logN[j] {
					t.Errorf("logN[%d]: gopus=%d oracle=%d", j, mode.LogN[j], r.logN[j])
				}
			}
			assertI32EqU8(t, "allocVectors", mode.AllocVectors, r.allocVectors)
			assertI32EqI16(t, "cacheIndex", mode.CacheIndex, r.cacheIndex)
			assertI32EqU8(t, "cacheBits", mode.CacheBits, r.cacheBits)
			assertI32EqU8(t, "cacheCaps", mode.CacheCaps, r.cacheCaps)
		})
	}
}

func assertI32EqU8(t *testing.T, name string, got []uint8, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s length gopus=%d oracle=%d", name, len(got), len(want))
		return
	}
	for k := range got {
		if int32(got[k]) != want[k] {
			t.Errorf("%s[%d]: gopus=%d oracle=%d", name, k, got[k], want[k])
		}
	}
}

func assertI32EqI16(t *testing.T, name string, got []int16, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s length gopus=%d oracle=%d", name, len(got), len(want))
		return
	}
	for k := range got {
		if int32(got[k]) != want[k] {
			t.Errorf("%s[%d]: gopus=%d oracle=%d", name, k, got[k], want[k])
		}
	}
}

// wideBandCases covers custom layouts beyond the standard 21-band table.
func wideBandCases() []struct{ fs, frame int } {
	return []struct{ fs, frame int }{
		{32000, 100},  // nbEBands 22, LM 0
		{32000, 200},  // nbEBands 22, LM 1
		{32000, 800},  // nbEBands 22, LM 3
		{44100, 120},  // nbEBands 22, LM 0
		{44100, 240},  // nbEBands 22, LM 1
		{44100, 480},  // nbEBands 22, LM 2
		{44100, 960},  // nbEBands 22, LM 3
		{44100, 1024}, // nbEBands 22, LM 3 (max frame)
		{48000, 1024}, // nbEBands 22, LM 3
		{96000, 600},  // nbEBands 23 (widest valid layout)
		{96000, 1024}, // nbEBands 22, LM 2
	}
}

// TestOracleWideBandModeParity checks mode admission and geometry against C.
func TestOracleWideBandModeParity(t *testing.T) {
	const maxBytes = 200
	specs := wideBandCases()
	var cases []oracleCase
	for _, s := range specs {
		cases = append(cases, oracleCase{s.fs, s.frame, 1, maxBytes, generateSine(440.0, float64(s.fs), s.frame)})
	}
	results := runCustomOracle(t, cases)

	for i, s := range specs {
		t.Run(fmt.Sprintf("Fs%d_frame%d", s.fs, s.frame), func(t *testing.T) {
			if results[i].status < 0 {
				t.Fatalf("libopus rejected known custom mode (Fs=%d frame=%d) status=%d", s.fs, s.frame, results[i].status)
			}
			mode, err := custom.NewMode(s.fs, s.frame)
			if err != nil {
				t.Fatalf("NewMode(%d,%d): %v", s.fs, s.frame, err)
			}
			if mode.NbEBands <= 21 {
				t.Fatalf("Fs=%d frame=%d expected nbEBands>21, got %d", s.fs, s.frame, mode.NbEBands)
			}
			if int32(mode.NbEBands) != int32(len(results[i].eBands)-1) {
				t.Errorf("nbEBands gopus=%d libopus=%d", mode.NbEBands, len(results[i].eBands)-1)
			}
			if _, err := custom.NewEncoder(mode, 1); err != nil {
				t.Errorf("NewEncoder: %v", err)
			}
			if _, err := custom.NewDecoder(mode, 1); err != nil {
				t.Errorf("NewDecoder: %v", err)
			}
		})
	}
}

// broadDecodeSweepCases enumerates a wide grid of non-standard custom modes
// with up to 21 bands, spanning all four short-block
// decompositions (LM 0..3), the Fs==400*shortMdctSize family and genuinely
// custom band layouts, several sample rates (8k..96k) and both channel counts.
// Every entry is a mode libopus --enable-custom-modes accepts.
func broadDecodeSweepCases() []oracleCase {
	const maxBytes = 200
	specs := []struct{ fs, frame int }{
		// LM=0 (no short blocks).
		{24000, 40}, {32000, 40}, {44100, 100}, {48000, 100}, {96000, 240},
		// LM=1.
		{12000, 40}, {16000, 60}, {24000, 80}, {44100, 200}, {96000, 480},
		{48000, 200}, {96000, 360},
		// LM=2.
		{12000, 80}, {16000, 120}, {24000, 160}, {48000, 360}, {96000, 800},
		{48000, 320}, {44100, 360}, {96000, 720},
		// LM=3 (20 ms equivalents and other non-power-of-two long frames).
		{16000, 320}, {24000, 480}, {32000, 640}, {48000, 640}, {44100, 720},
		{16000, 240}, {32000, 480}, {48000, 720}, {48000, 800},
	}
	var cases []oracleCase
	for _, s := range specs {
		for _, ch := range []int{1, 2} {
			var pcm []float32
			if ch == 1 {
				pcm = generateSine(440.0, float64(s.fs), s.frame)
			} else {
				pcm = generateSineStereo(440.0, 523.25, float64(s.fs), s.frame)
			}
			cases = append(cases, oracleCase{s.fs, s.frame, ch, maxBytes, pcm})
		}
	}
	return cases
}

// TestOracleDecodeParityBroadSweep checks every decoded sample bit and final
// range against the paired live C build.
func TestOracleDecodeParityBroadSweep(t *testing.T) {
	cases := broadDecodeSweepCases()
	results := runCustomOracle(t, cases)

	for i, tc := range cases {
		t.Run(fmt.Sprintf("Fs%d_frame%d_ch%d", tc.fs, tc.frameSize, tc.channels), func(t *testing.T) {
			if results[i].status < 0 {
				t.Skipf("libopus rejected custom mode (Fs=%d frame=%d ch=%d) status=%d",
					tc.fs, tc.frameSize, tc.channels, results[i].status)
			}
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatalf("NewMode(%d,%d): %v", tc.fs, tc.frameSize, err)
			}
			if mode.NbEBands > 21 {
				t.Fatalf("Fs=%d frame=%d unexpectedly exceeds this fixture group's 21 bands (nbEBands=%d)",
					tc.fs, tc.frameSize, mode.NbEBands)
			}
			dec, err := custom.NewDecoder(mode, tc.channels)
			if err != nil {
				t.Fatalf("NewDecoder: %v", err)
			}
			decoded, err := dec.DecodeFloat(results[i].packet, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFloat: %v", err)
			}
			if dec.FinalRange() != results[i].decRange {
				t.Fatalf("decoder range=%08x want=%08x", dec.FinalRange(), results[i].decRange)
			}
			if len(decoded) != len(results[i].decoded) {
				t.Fatalf("Fs=%d frame=%d ch=%d: decoded length gopus=%d libopus=%d",
					tc.fs, tc.frameSize, tc.channels, len(decoded), len(results[i].decoded))
			}
			assertCustomDecodeExact(t,
				fmt.Sprintf("Fs=%d frame=%d ch=%d", tc.fs, tc.frameSize, tc.channels), decoded, results[i].decoded)
		})
	}
}

// TestOracleEncodeParityBroadSweep checks exact packet bytes and final ranges
// against the paired C build for every supported mode in the decode sweep.
func TestOracleEncodeParityBroadSweep(t *testing.T) {
	cases := broadDecodeSweepCases()
	results := runCustomOracle(t, cases)

	for i, tc := range cases {
		t.Run(fmt.Sprintf("Fs%d_frame%d_ch%d", tc.fs, tc.frameSize, tc.channels), func(t *testing.T) {
			if results[i].status < 0 {
				t.Skipf("libopus rejected custom mode (Fs=%d frame=%d ch=%d) status=%d",
					tc.fs, tc.frameSize, tc.channels, results[i].status)
			}
			mode, err := custom.NewMode(tc.fs, tc.frameSize)
			if err != nil {
				t.Fatalf("NewMode(%d,%d): %v", tc.fs, tc.frameSize, err)
			}
			if mode.NbEBands > 21 {
				t.Fatalf("Fs=%d frame=%d unexpectedly exceeds this fixture group's 21 bands (nbEBands=%d)",
					tc.fs, tc.frameSize, mode.NbEBands)
			}

			got, enc := gopusEncode(t, tc)
			if len(got) != len(results[i].packet) {
				t.Fatalf("Fs=%d frame=%d ch=%d: packet length gopus=%d libopus=%d",
					tc.fs, tc.frameSize, tc.channels, len(got), len(results[i].packet))
			}
			if enc.FinalRange() != results[i].encRange {
				t.Fatalf("Fs=%d frame=%d ch=%d: encoder final range gopus=%08x libopus=%08x",
					tc.fs, tc.frameSize, tc.channels, enc.FinalRange(), results[i].encRange)
			}
			if !bytes.Equal(got, results[i].packet) {
				t.Errorf("Fs=%d frame=%d ch=%d: encoded packet differs from libopus",
					tc.fs, tc.frameSize, tc.channels)
			}
		})
	}
}

// --- helpers ------------------------------------------------------------------

// assertCustomDecodeExact compares every sample bit from the paired live C decoder.
func assertCustomDecodeExact(t *testing.T, label string, decoded, want []float32) {
	t.Helper()
	if len(decoded) != len(want) {
		t.Fatalf("%s samples=%d want=%d", label, len(decoded), len(want))
	}
	for i, v := range decoded {
		if math.Float32bits(v) != math.Float32bits(want[i]) {
			t.Fatalf("%s sample%d=%08x want=%08x", label, i, math.Float32bits(v), math.Float32bits(want[i]))
		}
	}
}

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
