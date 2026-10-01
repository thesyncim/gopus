//go:build gopus_qext && !gopus_fixed_point

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var hd96kToneDetectOracle libopustest.HelperCache

func hd96kToneDetectOraclePath() (string, error) {
	return hd96kToneDetectOracle.CHelperPath(libopustest.CHelperConfig{
		Label:        "native 96 kHz CELT tone_detect",
		OutputBase:   "gopus_libopus_hd96k_tone_detect",
		SourceFile:   "libopus_tone_lpc_oracle.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src"},
		QEXTRef:      true,
		Libs:         []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		ProbeRelPath: "config.h",
	})
}

func TestToneDetectFsUsesScaledCustomRate(t *testing.T) {
	enc := NewEncoder(1)
	enc.customScaleBase = 120
	enc.sampleRate = 16000
	if got := enc.toneDetectFs(); got != 16000 {
		t.Fatalf("scaled custom tone-detect rate=%d, want 16000", got)
	}

	// A partially configured custom mode retains the standard fallback used by
	// the encoder before mode setup completes.
	enc.sampleRate = 0
	if got := enc.toneDetectFs(); got != 48000 {
		t.Fatalf("unset custom tone-detect rate=%d, want 48000", got)
	}
}

func TestHD96kToneDetectUsesNativeSampleRate(t *testing.T) {
	libopustest.RequireOracle(t)

	enc := NewEncoder(1)
	if got := enc.toneDetectFs(); got != 48000 {
		t.Fatalf("standard CELT tone-detect rate=%d, want 48000", got)
	}
	enc.EnableHD96kMode()
	sampleRate := enc.toneDetectFs()
	if sampleRate != 96000 {
		t.Fatalf("native HD tone-detect rate=%d, want 96000", sampleRate)
	}

	const frameSize, overlap = 1920, 240
	x := make([]float32, frameSize+overlap)
	for i := range x {
		t := float64(i) / float64(sampleRate)
		x[i] = float32(0.31*math.Sin(2*math.Pi*137*t) + 0.17*math.Sin(2*math.Pi*12500*t))
	}

	path, err := hd96kToneDetectOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz CELT tone_detect", err)
		return
	}
	payload := libopustest.NewOraclePayloadVersion("GTLC", 3, 1)
	payload.U32(uint32(len(x)))
	payload.U32(1) // tone_lpc delay
	payload.U32(uint32(sampleRate))
	payload.Float32s(x...)
	data, err := libopustest.RunHelper(path, payload.Bytes())
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz CELT tone_detect", err)
		return
	}
	reader, version, err := libopustest.NewOracleReaderVersion("native 96 kHz CELT tone_detect", "GTLC", data)
	if err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("oracle version=%d, want 3", version)
	}
	reader.Count(1)
	wantFail := reader.U32()
	wantLPC0, wantLPC1 := reader.U32(), reader.U32()
	wantFreq, wantToneishness := reader.U32(), reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatal(err)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}

	lpc0, lpc1, ok := toneLPC(x, 1, false)
	gotFail := uint32(0)
	if !ok {
		gotFail = 1
	}
	if gotFail != wantFail || math.Float32bits(lpc0) != wantLPC0 || math.Float32bits(lpc1) != wantLPC1 {
		t.Fatalf("tone_lpc fail=%d coefficients=(%08x,%08x), selected C fail=%d coefficients=(%08x,%08x)",
			gotFail, math.Float32bits(lpc0), math.Float32bits(lpc1), wantFail, wantLPC0, wantLPC1)
	}
	gotFreq, gotToneishness := toneDetectFloat32Mono(x, sampleRate, false)
	if math.Float32bits(gotFreq) != wantFreq || math.Float32bits(gotToneishness) != wantToneishness {
		t.Fatalf("tone_detect freq/toneishness=(%08x,%08x), selected C=(%08x,%08x)",
			math.Float32bits(gotFreq), math.Float32bits(gotToneishness), wantFreq, wantToneishness)
	}

	scratch := make([]float32, len(x))
	var freqSink, toneishnessSink float32
	run := func() {
		freqSink, toneishnessSink = toneDetectScratchF32(x, 1, sampleRate, scratch)
	}
	run() // Warm the path before measuring steady-state allocations.
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("warm native HD tone-detect allocations=%g, want 0 (result=%08x/%08x)",
			allocs, math.Float32bits(freqSink), math.Float32bits(toneishnessSink))
	}
}
