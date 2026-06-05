// Package testvectors: official RFC 6716 / RFC 8251 test-vector conformance gate.
//
// This is the canonical Opus conformance suite: the opus-codec.org test vectors
// (testvector01..12.bit plus reference testvectorNN.dec / testvectorNNm.dec
// decoded outputs) validated with the reference opus_compare tool, exactly the
// pass/fail bar libopus itself uses in tests/run_vectors.sh.
//
// Methodology (mirrors tests/run_vectors.sh stereo lane):
//
//  1. Parse each testvectorNN.bit (opus_demo length-prefixed framing).
//  2. Decode every packet with the gopus decoder, as a persistent 48 kHz stereo
//     decoder (output is always stereo-interleaved, matching opus_demo -d 48000 2),
//     into raw signed-16-bit little-endian PCM (.sw).
//  3. Run the reference opus_compare binary `-s -r 48000 <ref>.dec gopus.sw`
//     against BOTH reference outputs (.dec and m.dec). A vector PASSES if
//     opus_compare returns success (exit 0, perceptual quality Q >= 0) for at
//     least one reference -- the official RFC 8251 conformance criterion.
//
// Vector availability / CI: the binary vectors are not committed (gitignored
// under testdata/opus_testvectors/). ensureTestVectors() uses the already-cached
// copy when present and otherwise downloads the official opus-codec.org
// rfc8251 archive; if neither the vectors nor the reference opus_compare binary
// is available the gate skips cleanly, like the other oracle-gated tests.
//
// Gating: parity tier + GOPUS_STRICT_LIBOPUS_REF=1 (the CI conformance lane sets
// both). The arm64 CELT/hybrid <=1-ULP FMA drift (project_arm64_celt_1ulp_drift)
// is far below opus_compare's perceptual floor, so every vector is expected to
// PASS opus_compare on every platform; the test reports the Q margin so any
// regression toward the floor is visible.
package testvectors

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
)

// rfcConformanceRate is the rate the official vectors are decoded and compared
// at. opus_compare is hardcoded around a 48 kHz analysis window; run_vectors.sh
// invokes the suite at 48000, which is the canonical conformance rate.
const rfcConformanceRate = 48000

// opusCompareQuality extracts the perceptual quality metric opus_compare prints
// on stderr ("Opus quality metric: NN.N %%"). It is informational; the pass/fail
// decision is opus_compare's own exit status.
var opusCompareQuality = regexp.MustCompile(`Opus quality metric:\s*([0-9.+-]+)`)

// TestRFCConformanceOpusCompare decodes each official RFC 6716/8251 test vector
// with gopus and validates it against the reference decoded output using the
// reference opus_compare tool -- the official conformance pass/fail bar.
func TestRFCConformanceOpusCompare(t *testing.T) {
	requireTestTier(t, testTierParity)
	requireStrictLibopusReference(t)

	if err := ensureTestVectors(t); err != nil {
		t.Skipf("official test vectors unavailable: %v", err)
	}

	opusCompare, err := benchutil.OpusComparePath()
	if err != nil {
		t.Skipf("reference opus_compare unavailable: %v", err)
	}

	for _, name := range testVectorNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bitFile := filepath.Join(testVectorDir, name+".bit")
			decFile := filepath.Join(testVectorDir, name+".dec")
			mdecFile := filepath.Join(testVectorDir, name+"m.dec")

			pcm, err := decodeVectorStereoInt16(bitFile)
			if err != nil {
				t.Fatalf("gopus decode %s: %v", name, err)
			}

			swPath := filepath.Join(t.TempDir(), name+".sw")
			if err := writeInt16LE(swPath, pcm); err != nil {
				t.Fatalf("write decoded PCM: %v", err)
			}

			// Official run_vectors.sh stereo criterion: pass if opus_compare
			// accepts EITHER reference output.
			res1 := runOpusCompare(t, opusCompare, decFile, swPath)
			res2 := runOpusCompare(t, opusCompare, mdecFile, swPath)

			t.Logf("%s: opus_compare vs .dec -> pass=%v Q=%.2f%s; vs m.dec -> pass=%v Q=%.2f%s",
				name, res1.pass, res1.quality, res1.note, res2.pass, res2.quality, res2.note)

			if !res1.pass && !res2.pass {
				t.Errorf("FAILED official opus_compare conformance: neither reference matches\n  .dec:  %s\n  m.dec: %s",
					res1.detail, res2.detail)
			}
		})
	}
}

// decodeVectorStereoInt16 decodes a testvector .bit file with the gopus decoder
// as a persistent 48 kHz stereo decoder and returns the interleaved int16 PCM,
// matching opus_demo's `-d 48000 2` reference decode. Decode errors emit silence
// for the packet's frame (as opus_demo would on a hard error) so the output stays
// time-aligned with the reference -- a genuine decode failure then surfaces as an
// opus_compare miss rather than a length mismatch.
func decodeVectorStereoInt16(bitFile string) ([]int16, error) {
	packets, err := ReadBitstreamFile(bitFile)
	if err != nil {
		return nil, err
	}
	if len(packets) == 0 {
		return nil, fmt.Errorf("no packets in %s", bitFile)
	}

	const channels = 2
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(rfcConformanceRate, channels))
	if err != nil {
		return nil, err
	}

	var out []int16
	for _, pkt := range packets {
		samples, err := decodeInt16(dec, pkt.Data)
		if err != nil {
			frameSize := getFrameSizeFromConfig(0)
			if len(pkt.Data) > 0 {
				frameSize = getFrameSizeFromConfig(pkt.Data[0] >> 3)
			}
			out = append(out, make([]int16, frameSize*channels)...)
			continue
		}
		out = append(out, samples...)
	}
	return out, nil
}

// writeInt16LE writes interleaved int16 samples as raw little-endian PCM (.sw),
// the format opus_compare reads.
func writeInt16LE(path string, samples []int16) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	var buf [2]byte
	for _, s := range samples {
		binary.LittleEndian.PutUint16(buf[:], uint16(s))
		if _, err := w.Write(buf[:]); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

type opusCompareResult struct {
	pass    bool
	quality float64
	note    string
	detail  string
}

// runOpusCompare invokes the reference opus_compare binary in stereo mode at the
// conformance rate and reports whether it accepted the candidate (exit 0).
func runOpusCompare(t *testing.T, opusCompare, refFile, candidateFile string) opusCompareResult {
	t.Helper()

	if _, err := os.Stat(refFile); err != nil {
		return opusCompareResult{detail: fmt.Sprintf("reference missing: %v", err)}
	}

	cmd := exec.Command(opusCompare, "-s", "-r", strconv.Itoa(rfcConformanceRate), refFile, candidateFile)
	output, err := cmd.CombinedOutput()

	res := opusCompareResult{detail: string(output)}
	if m := opusCompareQuality.FindSubmatch(output); m != nil {
		if q, perr := strconv.ParseFloat(string(m[1]), 64); perr == nil {
			res.quality = q
		}
	}

	if err == nil {
		res.pass = true
		return res
	}
	if _, ok := err.(*exec.ExitError); ok {
		// Non-zero exit is opus_compare's "FAILS" verdict, not a tooling error.
		return res
	}
	res.detail = fmt.Sprintf("opus_compare exec error: %v\n%s", err, output)
	return res
}
