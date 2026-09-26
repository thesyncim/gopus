//go:build arm64

package celt

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

type pairedToneLPCCase struct {
	name  string
	x     []float32
	delay int
}

// tone_lpc requires a positive correlation count. Its correlation kernel's
// empty loop is checked separately because the C function forbids that input.
func TestToneLPCCorrARMZeroCount(t *testing.T) {
	for _, delay := range []int{1, 3} {
		x := make([]float32, 2*delay)
		r00, r01, r02 := toneLPCCorr(x, 0, delay, 2*delay)
		if r00 != 0 || r01 != 0 || r02 != 0 {
			t.Fatalf("delay=%d zero-count correlation=(%g,%g,%g), want zeros", delay, r00, r01, r02)
		}
	}
}

var pairedToneLPCHelper libopustest.HelperCache

func pairedToneLPCHelperPath() (string, error) {
	return pairedToneLPCHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "paired CELT tone_lpc",
		OutputBase:   "gopus_libopus_tone_lpc",
		SourceFile:   "libopus_tone_lpc_oracle.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		ProbeRelPath: "config.h",
	})
}

func pairedToneLPCChirpInput(t *testing.T) []float32 {
	t.Helper()
	const (
		channels     = 2
		frameSize    = 240
		signalFrames = 200
		targetFrame  = 10
	)
	signal, err := testsignal.GenerateEncoderSignalVariant(testsignal.EncoderVariantChirpSweepV1, 48000, signalFrames*frameSize*channels, channels)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := testsignal.HashFloat32LE(signal), "12f974d2680f2bc3e4f70778dd26e1eadfe75da2980d7c750d8e25fbe697a927"; got != want {
		t.Fatalf("chirp input hash=%s want %s", got, want)
	}
	enc := NewEncoder(channels)
	enc.SetComplexity(10)
	enc.SetBandwidth(CELTFullband)
	enc.SetBitrate(128000)
	enc.SetMaxPayloadBytes(80)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(true)
	enc.SetDelayCompensationEnabled(false)
	enc.SetLSBDepth(24)
	enc.SetLSBQuantizationEnabled(true)
	enc.SetDCRejectEnabled(true)
	for frame := 0; frame <= targetFrame; frame++ {
		start := frame * frameSize * channels
		if _, err := enc.EncodeFrame(signal[start:start+frameSize*channels], frameSize); err != nil {
			t.Fatalf("chirp frame %d: %v", frame, err)
		}
	}
	if got, want := len(enc.scratch.transientX), 360; got < want {
		t.Fatalf("chirp tone input length=%d want at least %d", got, want)
	}
	x := append([]float32(nil), enc.scratch.transientX[:360]...)
	if got, want := testsignal.HashFloat32LE(x), "d71bd8e33ba7fae8029708c887a4b16f28227654dc9b0a06f6980c14df3211e6"; got != want {
		t.Fatalf("chirp frame 10 tone input hash=%s want selected-C %s", got, want)
	}
	return x
}

func pairedToneLPCCases(t *testing.T) []pairedToneLPCCase {
	t.Helper()
	rng := rand.New(rand.NewSource(0x4c504343))
	counts := []int{1, 2, 3, 4, 5, 7, 8, 479, 480, 481}
	cases := make([]pairedToneLPCCase, 0, len(counts)*2+6)
	for _, count := range counts {
		for _, delay := range []int{1, 3} {
			x := make([]float32, count+2*delay)
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			cases = append(cases, pairedToneLPCCase{fmt.Sprintf("count%d_delay%d", count, delay), x, delay})
		}
	}
	x := pairedToneLPCChirpInput(t)
	for _, delay := range []int{1, 2, 4, 8, 16, 32} {
		cases = append(cases, pairedToneLPCCase{fmt.Sprintf("chirp_frame10_delay%d", delay), x, delay})
	}
	return cases
}

func TestToneLPCPairedLibopusARMRawBits(t *testing.T) {
	variant := requirePairedCELTOracleMode(t)
	libopustest.RequireOracle(t)
	cases := pairedToneLPCCases(t)
	payload := libopustest.NewOraclePayloadVersion("GTLC", 2, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.delay))
		payload.Float32s(tc.x...)
	}
	path, err := pairedToneLPCHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired CELT tone_lpc", err)
		return
	}
	data, err := libopustest.RunHelper(path, payload.Bytes())
	if err != nil {
		libopustest.HelperUnavailable(t, "paired CELT tone_lpc", err)
		return
	}
	reader, version, err := libopustest.NewOracleReaderVersion("paired CELT tone_lpc", "GTLC", data)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("paired CELT tone_lpc version=%d want 2", version)
	}
	reader.Count(len(cases))
	t.Logf("paired libopus=%s cases=%d helper=%s", variant, len(cases), path)
	for _, tc := range cases {
		wantFail := reader.U32()
		want0, want1 := reader.U32(), reader.U32()
		wantFreq, wantToneishness := reader.U32(), reader.U32()
		if err := reader.Err(); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.name, func(t *testing.T) {
			got0, got1, ok := toneLPC(tc.x, tc.delay, false)
			gotFail := uint32(0)
			if !ok {
				gotFail = 1
			}
			if gotFail != wantFail || math.Float32bits(got0) != want0 || math.Float32bits(got1) != want1 {
				t.Fatalf("tone_lpc fail=%d lpc=(%08x,%08x), selected-C fail=%d lpc=(%08x,%08x)", gotFail, math.Float32bits(got0), math.Float32bits(got1), wantFail, want0, want1)
			}
			if len(tc.x) > 64 {
				gotFreq, gotToneishness := toneDetectFloat32Mono(tc.x, 48000, false)
				if math.Float32bits(gotFreq) != wantFreq || math.Float32bits(gotToneishness) != wantToneishness {
					t.Fatalf("tone_detect freq=%08x toneishness=%08x, selected-C freq=%08x toneishness=%08x", math.Float32bits(gotFreq), math.Float32bits(gotToneishness), wantFreq, wantToneishness)
				}
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
