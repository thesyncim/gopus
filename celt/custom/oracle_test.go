//go:build gopus_custom && gopus_custom_oracle

// oracle_test.go provides byte-exact parity tests against a custom-modes
// libopus build.
//
// Oracle gate: this file is compiled ONLY when both gopus_custom and
// gopus_custom_oracle are set.  gopus_custom_oracle requires a libopus build
// compiled with --enable-custom-modes; the path is read from the environment
// variable GOPUS_CUSTOM_LIBOPUS_DIR (path to the source/build tree containing
// .libs/libopus.a and config.h).
//
// Oracle parity status:
//   The pinned libopus 1.6.1 in tmp_check/opus-1.6.1 was NOT built with
//   --enable-custom-modes (custom modes are #undef in its config.h).
//   Therefore oracle tests are gated here and will skip unless a custom-modes
//   build is provided.
//
// To run oracle tests:
//   1. Build libopus with --enable-custom-modes:
//        cd /some/path && ./configure --enable-custom-modes --enable-static
//        --disable-shared && make
//   2. Set GOPUS_CUSTOM_LIBOPUS_DIR=/some/path
//   3. Run: GOFLAGS="-tags=gopus_custom,gopus_custom_oracle" go test ./celt/custom/...

package custom_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

// customLibopusDir returns the path to a custom-modes libopus build, or ""
// if none is configured.
func customLibopusDir() string {
	return os.Getenv("GOPUS_CUSTOM_LIBOPUS_DIR")
}

func skipIfNoCustomOracle(t *testing.T) {
	t.Helper()
	if customLibopusDir() == "" {
		t.Skip("GOPUS_CUSTOM_LIBOPUS_DIR not set; skipping oracle parity test (see oracle_test.go for instructions)")
	}
}

// buildCustomOracleHelper builds the C oracle binary that drives libopus custom
// encode/decode.  Returns the path to the built binary.
func buildCustomOracleHelper(t *testing.T) string {
	t.Helper()
	libDir := customLibopusDir()
	if libDir == "" {
		t.Skip("GOPUS_CUSTOM_LIBOPUS_DIR not set")
	}
	ccPath, err := libopustooling.FindCCompiler()
	if err != nil {
		t.Skipf("C compiler unavailable: %v", err)
	}

	// Locate the oracle C source file.
	_, selfFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// The csrc directory is at <repo>/tools/csrc/
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(selfFile), "..", "..", ".."))
	srcFile := filepath.Join(repoRoot, "tools", "csrc", "libopus_custom_oracle.c")
	if _, err := os.Stat(srcFile); err != nil {
		t.Skipf("oracle C source not found at %s: %v", srcFile, err)
	}

	libopusStatic := filepath.Join(libDir, ".libs", "libopus.a")
	if _, err := os.Stat(libopusStatic); err != nil {
		t.Skipf("custom-modes libopus.a not found at %s: %v", libopusStatic, err)
	}

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "gopus_custom_oracle")

	args := []string{
		"-std=c99", "-O2",
		"-DHAVE_CONFIG_H",
		"-I", libDir,
		"-I", filepath.Join(libDir, "include"),
		"-I", filepath.Join(libDir, "celt"),
		"-I", filepath.Join(libDir, "silk"),
		"-I", filepath.Join(libDir, "src"),
		srcFile,
		libopusStatic,
		"-lm",
		"-o", outPath,
	}
	cmd := exec.Command(ccPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("build custom oracle: %v (%s)", err, bytes.TrimSpace(out))
	}
	return outPath
}

// runCustomOracle sends a binary protocol request to the oracle helper and
// returns the response.  Protocol:
//   Request:  "GCCO" + uint32(N) cases + N * [uint32 Fs, uint32 frameSize,
//             uint32 channels, uint32 maxBytes, uint32 nSamples,
//             nSamples*float32 PCM]
//   Response: "GCCO" + uint32(N) + N * [uint32 len + len bytes packet]
func runCustomOracle(t *testing.T, binPath string, cases []oracleCase) [][]byte {
	t.Helper()

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

	cmd := exec.Command(binPath)
	cmd.Stdin = &req
	out, err := cmd.Output()
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
	results := make([][]byte, n)
	for i := range results {
		var sz uint32
		if err := binary.Read(r, binary.LittleEndian, &sz); err != nil {
			t.Fatalf("oracle result[%d] size: %v", i, err)
		}
		pkt := make([]byte, sz)
		if _, err := r.Read(pkt); err != nil {
			t.Fatalf("oracle result[%d] payload: %v", i, err)
		}
		results[i] = pkt
	}
	return results
}

type oracleCase struct {
	fs, frameSize, channels, maxBytes int
	pcm                               []float32
}

// TestOracleParityStandardModes checks that for the four standard 48 kHz frame
// sizes our encode output matches libopus custom encode byte-for-byte.
// These use the same static modes so parity is expected to be exact.
func TestOracleParityStandardModes(t *testing.T) {
	skipIfNoCustomOracle(t)
	binPath := buildCustomOracleHelper(t)

	frameSizes := []int{120, 240, 480, 960}
	const maxBytes = 200

	var cases []oracleCase
	for _, sz := range frameSizes {
		pcm := generateSine(440.0, 48000, sz)
		cases = append(cases, oracleCase{48000, sz, 1, maxBytes, pcm})
	}

	wantPackets := runCustomOracle(t, binPath, cases)

	for i, tc := range cases {
		mode, err := custom.NewMode(tc.fs, tc.frameSize)
		if err != nil {
			t.Fatalf("case %d NewMode: %v", i, err)
		}
		enc, err := custom.NewEncoder(mode, tc.channels)
		if err != nil {
			t.Fatalf("case %d NewEncoder: %v", i, err)
		}
		_ = enc.SetVBR(false)
		_ = enc.SetComplexity(9)

		got, err := enc.EncodeFloat(tc.pcm, tc.maxBytes)
		if err != nil {
			t.Fatalf("case %d EncodeFloat: %v", i, err)
		}
		want := wantPackets[i]
		if !bytes.Equal(got, want) {
			t.Errorf("case %d (48000/%d): packet mismatch\n  got  (%d bytes): %x\n  want (%d bytes): %x",
				i, tc.frameSize, len(got), got, len(want), want)
		} else {
			t.Logf("case %d (48000/%d): %d bytes ✓", i, tc.frameSize, len(got))
		}
	}
}

// TestOracleDecodeStandard verifies that packets produced by the libopus custom
// encoder can be decoded by our CustomDecoder (and vice versa).
func TestOracleDecodeStandard(t *testing.T) {
	skipIfNoCustomOracle(t)
	binPath := buildCustomOracleHelper(t)

	sz := 960
	pcm := generateSine(880.0, 48000, sz)
	cases := []oracleCase{{48000, sz, 1, 200, pcm}}

	wantPackets := runCustomOracle(t, binPath, cases)
	oraclePkt := wantPackets[0]

	mode, err := custom.NewMode(48000, sz)
	if err != nil {
		t.Fatalf("NewMode: %v", err)
	}
	dec, err := custom.NewDecoder(mode, 1)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	decoded, err := dec.DecodeFloat(oraclePkt, sz)
	if err != nil {
		t.Fatalf("DecodeFloat oracle packet: %v", err)
	}
	if len(decoded) != sz {
		t.Fatalf("decoded length %d, want %d", len(decoded), sz)
	}

	// Compute energy to verify non-trivial output.
	var rms float64
	for _, s := range decoded {
		rms += float64(s) * float64(s)
	}
	rms = math.Sqrt(rms / float64(len(decoded)))
	t.Logf("oracle packet decode RMS: %.4f", rms)
	if rms < 1e-4 {
		t.Error("decoded oracle packet has no energy")
	}
}

// --- binary helpers -----------------------------------------------------------

func writeU32(b *bytes.Buffer, v uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	b.Write(buf[:])
}

func writef32(b *bytes.Buffer, v float32) {
	writeU32(b, math.Float32bits(v))
}

// Ensure the package is used (suppress import errors during build).
var _ = fmt.Sprintf
