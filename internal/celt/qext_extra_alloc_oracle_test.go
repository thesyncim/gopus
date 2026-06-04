//go:build gopus_qext

package celt

import (
	"bytes"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var libopusQEXTExtraAllocHelper libopustest.HelperCache

func buildLibopusQEXTExtraAllocHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext extra alloc",
		OutputBase:  "gopus_libopus_celt_qext_extra_alloc",
		SourceFile:  "libopus_celt_qext_extra_alloc_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

type qextExtraAllocCase struct {
	name         string
	channels     int
	lm           int
	start        int
	end          int
	qextEnd      int
	totalQ3      int32
	toneFreq     float32
	toneishness  float32
	storageBytes int
	bandLogE     []float32
	qextBandLogE []float32
}

type qextExtraAllocOracle struct {
	totBands  int
	encPulses []int32
	encQuant  []int32
	encBytes  []byte
	encTell   uint32
	decPulses []int32
	decQuant  []int32
}

func probeLibopusQEXTExtraAlloc(t *testing.T, c qextExtraAllocCase) qextExtraAllocOracle {
	t.Helper()
	binPath, err := libopusQEXTExtraAllocHelper.Path(buildLibopusQEXTExtraAllocHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext extra alloc", err)
	}
	payload := libopustest.NewOraclePayload("GQAI")
	payload.U32(uint32(c.channels))
	payload.U32(uint32(c.lm))
	payload.U32(uint32(c.start))
	payload.U32(uint32(c.end))
	payload.U32(uint32(c.qextEnd))
	payload.I32(c.totalQ3)
	payload.Float32(c.toneFreq)
	payload.Float32(c.toneishness)
	payload.U32(uint32(c.storageBytes))
	payload.U32(uint32(len(c.bandLogE)))
	payload.Float32s(c.bandLogE...)
	payload.U32(uint32(len(c.qextBandLogE)))
	payload.Float32s(c.qextBandLogE...)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext extra alloc", "GQAO")
	if err != nil {
		t.Fatalf("run qext extra alloc oracle: %v", err)
	}
	var o qextExtraAllocOracle
	o.totBands = int(reader.U32())
	o.encPulses = make([]int32, int(reader.U32()))
	for i := range o.encPulses {
		o.encPulses[i] = reader.I32()
	}
	o.encQuant = make([]int32, int(reader.U32()))
	for i := range o.encQuant {
		o.encQuant[i] = reader.I32()
	}
	o.encBytes = reader.Bytes(int(reader.U32()))
	o.encTell = reader.U32()
	o.decPulses = make([]int32, int(reader.U32()))
	for i := range o.decPulses {
		o.decPulses[i] = reader.I32()
	}
	o.decQuant = make([]int32, int(reader.U32()))
	for i := range o.decQuant {
		o.decQuant[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext extra alloc oracle payload not fully consumed: %v", err)
	}
	return o
}

// makeQEXTLogE builds reproducible main/qext band-energy vectors for the given
// channel count using a deterministic slope plus a per-channel offset.
func makeQEXTLogE(channels, bands int, base, slope, chanOffset float32) []float32 {
	out := make([]float32, channels*bands)
	for c := 0; c < channels; c++ {
		for i := 0; i < bands; i++ {
			out[c*bands+i] = base + slope*float32(i) + chanOffset*float32(c)
		}
	}
	return out
}

// TestComputeQEXTExtraAllocationMatchesLibopusOracle drives gopus's QEXT
// extension-band allocation (computeQEXTExtraAllocationEncode/Decode) with the
// same inputs as libopus clt_compute_extra_allocation() and asserts the
// per-band extra_pulses/extra_quant arrays and the encoded range-coder bytes
// match integer-for-integer on both the encode and decode side. These are
// integer tables, so a mismatch fails on every platform.
func TestComputeQEXTExtraAllocationMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	cfg, ok := computeQEXTModeConfig(96000, 240)
	if !ok {
		t.Fatal("computeQEXTModeConfig(96000,240)=false want true")
	}

	cases := []qextExtraAllocCase{
		{
			name: "mono_qe14_high", channels: 1, lm: 3, start: 0, end: MaxBands,
			qextEnd: nbQEXTBands, totalQ3: 4096, toneFreq: 0.5, toneishness: 0.3,
			storageBytes: 96,
			bandLogE:     makeQEXTLogE(1, MaxBands, 0.25, 0.05, 0),
			qextBandLogE: makeQEXTLogE(1, nbQEXTBands, -0.2, 0.08, 0),
		},
		{
			name: "stereo_qe14_high", channels: 2, lm: 3, start: 0, end: MaxBands,
			qextEnd: nbQEXTBands, totalQ3: 6144, toneFreq: 0.9, toneishness: 0.42,
			storageBytes: 96,
			bandLogE:     makeQEXTLogE(2, MaxBands, 0.25, 0.05, -0.1),
			qextBandLogE: makeQEXTLogE(2, nbQEXTBands, -0.2, 0.08, -0.05),
		},
		{
			name: "mono_qe2_low", channels: 1, lm: 3, start: 0, end: MaxBands,
			qextEnd: 2, totalQ3: 1536, toneFreq: 1.5, toneishness: 0.99,
			storageBytes: 48,
			bandLogE:     makeQEXTLogE(1, MaxBands, 0.1, 0.03, 0),
			qextBandLogE: makeQEXTLogE(1, nbQEXTBands, -0.4, 0.06, 0),
		},
		{
			name: "stereo_qe2_low", channels: 2, lm: 3, start: 0, end: MaxBands,
			qextEnd: 2, totalQ3: 2048, toneFreq: 0.2, toneishness: 0.7,
			storageBytes: 48,
			bandLogE:     makeQEXTLogE(2, MaxBands, 0.0, 0.04, 0.07),
			qextBandLogE: makeQEXTLogE(2, nbQEXTBands, -0.3, 0.05, 0.04),
		},
		{
			name: "mono_qe14_tiny_budget", channels: 1, lm: 3, start: 0, end: MaxBands,
			qextEnd: nbQEXTBands, totalQ3: 64, toneFreq: 0.5, toneishness: 0.3,
			storageBytes: 16,
			bandLogE:     makeQEXTLogE(1, MaxBands, 0.25, 0.05, 0),
			qextBandLogE: makeQEXTLogE(1, nbQEXTBands, -0.2, 0.08, 0),
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			o := probeLibopusQEXTExtraAlloc(t, c)
			if o.totBands != c.end+c.qextEnd {
				t.Fatalf("oracle totBands=%d want %d", o.totBands, c.end+c.qextEnd)
			}

			mainLogE := toCeltGLog(c.bandLogE)
			qextLogE := toCeltGLog(c.qextBandLogE)

			// Encode side.
			var enc rangecoding.Encoder
			encBuf := make([]byte, c.storageBytes)
			enc.Init(encBuf)

			encPulses := make([]int32, MaxBands+nbQEXTBands)
			encQuant := make([]int32, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationEncode(
				c.start, c.end, c.qextEnd, int(c.totalQ3), c.channels, c.lm,
				mainLogE, qextLogE, &cfg, c.toneFreq, c.toneishness,
				&enc, encPulses, encQuant,
			)
			enc.Done()
			// Compare the whole finalized storage buffer: the C oracle dumps all
			// storageBytes after ec_enc_done, and this allocation path uses no raw
			// back-of-buffer bits, so the buffers must be byte-identical.
			gotBytes := append([]byte(nil), enc.Buffer()[:c.storageBytes]...)

			// gopus indexes qext bands at [end .. end+qextEnd); the oracle dumps a
			// contiguous [start .. end+qextEnd) array. Build the comparison slice.
			gotEncPulses := gatherQEXTAlloc(encPulses, c.end, c.qextEnd)
			gotEncQuant := gatherQEXTAlloc(encQuant, c.end, c.qextEnd)

			assertIntSliceEqual(t, "enc extra_pulses", gotEncPulses, o.encPulses)
			assertIntSliceEqual(t, "enc extra_quant", gotEncQuant, o.encQuant)

			if !bytes.Equal(gotBytes, o.encBytes) {
				t.Fatalf("enc bytes mismatch\n gopus=%x\noracle=%x", gotBytes, o.encBytes)
			}

			// Decode side: replay the same storage-sized buffer so the
			// ec_tell_frac storage gate matches the C reference exactly.
			decBuf := make([]byte, c.storageBytes)
			copy(decBuf, o.encBytes)
			var dec rangecoding.Decoder
			dec.Init(decBuf)

			decPulses := make([]int32, MaxBands+nbQEXTBands)
			decQuant := make([]int32, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationDecodeWithMode(
				c.start, c.end, c.qextEnd, int(c.totalQ3), c.channels, c.lm,
				&dec, decPulses, decQuant, &cfg,
			)
			gotDecPulses := gatherQEXTAlloc(decPulses, c.end, c.qextEnd)
			gotDecQuant := gatherQEXTAlloc(decQuant, c.end, c.qextEnd)

			assertIntSliceEqual(t, "dec extra_pulses", gotDecPulses, o.decPulses)
			assertIntSliceEqual(t, "dec extra_quant", gotDecQuant, o.decQuant)
		})
	}
}

// gatherQEXTAlloc returns the contiguous [0..end+qextEnd) view of a gopus
// extra-allocation array, which stores main bands at [0,end) and qext bands at
// [MaxBands, MaxBands+qextEnd). end equals MaxBands for the 96 kHz mode, so the
// two regions are already contiguous.
func gatherQEXTAlloc(arr []int32, end, qextEnd int) []int32 {
	out := make([]int32, end+qextEnd)
	copy(out[:end], arr[:end])
	copy(out[end:], arr[MaxBands:MaxBands+qextEnd])
	return out
}

func toCeltGLog(in []float32) []celtGLog {
	out := make([]celtGLog, len(in))
	for i, v := range in {
		out[i] = celtGLog(v)
	}
	return out
}

func assertIntSliceEqual(t *testing.T, label string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: gopus=%d oracle=%d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] mismatch: gopus=%d oracle=%d\n gopus=%v\noracle=%v",
				label, i, got[i], want[i], got, want)
		}
	}
}
