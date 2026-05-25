package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	projectionMatrixModeOutFloat = uint32(0)
	projectionMatrixModeOutShort = uint32(1)
)

var projectionMatrixHelper libopustest.HelperCache

func probeLibopusProjectionMatrix(t *testing.T, mode uint32, rows, cols, frameSize int, matrix []int16, input []float32) *libopustest.OracleReader {
	t.Helper()
	binPath, err := projectionMatrixHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "projection matrix",
		OutputBase:   "gopus_libopus_projection_matrix",
		SourceFile:   "libopus_projection_matrix_info.c",
		ProbeRelPath: "src/mapping_matrix.h",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "src"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "projection matrix", err)
	}

	payload := libopustest.NewOraclePayload(
		"GPMI",
		mode,
		uint32(rows),
		uint32(cols),
		uint32(frameSize),
	)
	for _, v := range matrix {
		payload.I16(v)
	}
	for _, v := range input {
		payload.Float32(v)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "projection matrix", "GPMO")
	if err != nil {
		libopustest.HelperUnavailable(t, "projection matrix", err)
	}
	return reader
}

func TestProjectionDemixingFloatMatchesLibopusMatrixOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		rows      = 4
		cols      = 4
		frameSize = 3
	)
	matrix := []int16{
		16384, -8192, 32767, -16384,
		23170, 0, -23170, 8192,
		-10923, 10923, 5461, -5461,
		32767, -32768, 12345, -12345,
	}
	input := []float32{
		0, 0.25, -0.5, 0.9999695,
		-1, 1.25, -1.75, 0.0625,
		float32(math.Nextafter32(0.5, 1)), float32(math.Nextafter32(-0.5, -1)), 0.03125, -0.03125,
	}
	reader := probeLibopusProjectionMatrix(t, projectionMatrixModeOutFloat, rows, cols, frameSize, matrix, input)
	count := reader.Count(rows * frameSize)
	reader.ExpectRemaining(count * 4)
	want := make([]float32, count)
	for i := range want {
		want[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	got := make([]float32, len(input))
	applyProjectionDemixingMatrix32(got, input, matrix, make([]float32, cols), frameSize, rows, cols)
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("projection float[%d]=%08x want %08x (%0.10g vs %0.10g)",
				i, math.Float32bits(got[i]), math.Float32bits(want[i]), got[i], want[i])
		}
	}
}

func TestProjectionDemixingInt16MatchesLibopusMatrixOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		rows      = 4
		cols      = 4
		frameSize = 3
	)
	matrix := []int16{
		16384, -8192, 32767, -16384,
		23170, 0, -23170, 8192,
		-10923, 10923, 5461, -5461,
		32767, -32768, 12345, -12345,
	}
	input := []float32{
		0, 0.25, -0.5, 0.9999695,
		-1, 1.25, -1.75, 0.0625,
		float32(math.Nextafter32(0.5, 1)), float32(math.Nextafter32(-0.5, -1)), 0.03125, -0.03125,
	}
	reader := probeLibopusProjectionMatrix(t, projectionMatrixModeOutShort, rows, cols, frameSize, matrix, input)
	count := reader.Count(rows * frameSize)
	reader.ExpectRemaining(count * 2)
	want := make([]int16, count)
	for i := range want {
		want[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	got := make([]int16, len(input))
	applyProjectionDemixingMatrixInt16(got, input, matrix, make([]float32, cols), frameSize, rows, cols)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("projection int16[%d]=%d want %d", i, got[i], want[i])
		}
	}
}
