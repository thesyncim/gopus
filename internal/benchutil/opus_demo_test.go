package benchutil

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
)

func TestReferenceToolOverridesStayInsideSelectedStampedTree(t *testing.T) {
	t.Setenv("GOPUS_LIBOPUS_REF_SCALAR", "auto")
	for _, tc := range []struct {
		env  string
		tool string
		path func() (string, error)
	}{
		{env: "OPUS_DEMO_PATH", tool: "opus_demo", path: OpusDemoPath},
		{env: "OPUS_COMPARE_PATH", tool: "opus_compare", path: OpusComparePath},
	} {
		t.Run(tc.env, func(t *testing.T) {
			external := filepath.Join(t.TempDir(), tc.tool)
			if err := os.WriteFile(external, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv(tc.env, external)
			_, err := tc.path()
			if err == nil {
				t.Fatalf("accepted executable override outside selected stamped tree: %s", external)
			}
			var configErr *libopustooling.LibopusReferenceConfigError
			if !errors.As(err, &configErr) {
				t.Fatalf("override error is not a typed configuration error: %T %v", err, err)
			}
		})
	}
}

func TestStrictHelperUnavailableIsFailureNotSkip(t *testing.T) {
	const childEnv = "GOPUS_BENCHUTIL_STRICT_CHILD"
	if os.Getenv(childEnv) == "1" {
		t.Setenv("GOPUS_STRICT_LIBOPUS_REF", "1")
		libopustest.HelperUnavailable(t, "strict probe", errors.New("missing oracle"))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestStrictHelperUnavailableIsFailureNotSkip$")
	cmd.Env = append(os.Environ(), childEnv+"=1", "GOPUS_STRICT_LIBOPUS_REF=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("strict helper-unavailable probe unexpectedly passed: %s", output)
	}
	if !strings.Contains(string(output), "libopus strict probe helper unavailable") || strings.Contains(string(output), "--- SKIP") {
		t.Fatalf("strict helper-unavailable probe did not fail instead of skip: %s", output)
	}
}

func TestWriteRepeatedRawFloat32(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "samples.f32")
	samples := []float32{0.25, -0.5, 1.0}
	if err := WriteRepeatedRawFloat32(path, samples, 2); err != nil {
		t.Fatalf("WriteRepeatedRawFloat32 failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(data) != len(samples)*2*4 {
		t.Fatalf("unexpected byte length: got=%d want=%d", len(data), len(samples)*2*4)
	}

	got := make([]float32, 0, len(samples)*2)
	for i := 0; i < len(data); i += 4 {
		got = append(got, math.Float32frombits(binary.LittleEndian.Uint32(data[i:i+4])))
	}
	want := []float32{0.25, -0.5, 1.0, 0.25, -0.5, 1.0}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("sample %d mismatch: got=%08x want=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func TestWriteRepeatedOpusDemoBitstream(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "packets.bit")
	packets := [][]byte{{0x01, 0x02}, {0xAA}}
	if err := WriteRepeatedOpusDemoBitstream(path, packets, 2); err != nil {
		t.Fatalf("WriteRepeatedOpusDemoBitstream failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	offset := 0
	for repeat := range 2 {
		for packetIdx, packet := range packets {
			if offset+8+len(packet) > len(data) {
				t.Fatalf("truncated stream at repeat=%d packet=%d", repeat, packetIdx)
			}
			if got := binary.BigEndian.Uint32(data[offset : offset+4]); got != uint32(len(packet)) {
				t.Fatalf("repeat=%d packet=%d len mismatch: got=%d want=%d", repeat, packetIdx, got, len(packet))
			}
			if got := binary.BigEndian.Uint32(data[offset+4 : offset+8]); got != 0 {
				t.Fatalf("repeat=%d packet=%d final range mismatch: got=%d want=0", repeat, packetIdx, got)
			}
			offset += 8
			for i, b := range packet {
				if data[offset+i] != b {
					t.Fatalf("repeat=%d packet=%d byte=%d mismatch: got=%02x want=%02x", repeat, packetIdx, i, data[offset+i], b)
				}
			}
			offset += len(packet)
		}
	}
	if offset != len(data) {
		t.Fatalf("unexpected trailing bytes: got=%d total=%d", offset, len(data))
	}
}
