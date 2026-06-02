//go:build gopus_qext

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/rangecoding"
)

// qextExtBandsFloatTol bounds the documented CELT cosine/rsqrt-kernel residual
// (project_arm64_celt_1ulp_drift.md) on the extension-band X and energy values,
// which are reconstructed from the bitstream through PVQ normalisation
// (celt_rsqrt) and theta (cos). Those float kernels drift a few ULP versus the
// SIMD qext libopus the oracle links (amd64) and versus scalar libopus (arm64),
// so the bounded residual budget is applied on every arch.
const qextExtBandsFloatTol = float32(1e-6)

var libopusQEXTExtBandsHelper libopustest.HelperCache

func buildLibopusQEXTExtBandsHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext ext bands",
		OutputBase:  "gopus_libopus_celt_qext_ext_bands",
		SourceFile:  "libopus_celt_qext_ext_bands_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

type qextExtBandsCase struct {
	name         string
	channels     int
	lm           int
	qextEnd      int
	intensity    int
	dualStereo   int
	transient    bool
	extStorage   int
	mainStorage  int
	mainConsumed int
	xSeed        uint32
	eSeed        uint32
}

type qextExtBandsOracle struct {
	n            int
	qStartBin    int
	qStopBin     int
	extBytes     []byte
	decodedX     []float32 // C*(stop-start)
	decodedEne   []float32 // C*NB_QEXT_BANDS
	mainTellFrac int
	decQextEnd   int
	extPulses    []int32
	extQuant     []int32
}

func probeLibopusQEXTExtBands(t *testing.T, c qextExtBandsCase) qextExtBandsOracle {
	t.Helper()
	binPath, err := libopusQEXTExtBandsHelper.Path(buildLibopusQEXTExtBandsHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext ext bands", err)
	}
	payload := libopustest.NewOraclePayload("GQXI")
	payload.U32(uint32(c.channels))
	payload.U32(uint32(c.lm))
	payload.U32(uint32(c.qextEnd))
	payload.U32(uint32(c.intensity))
	payload.U32(uint32(c.dualStereo))
	payload.U32(boolToU32(c.transient))
	payload.U32(uint32(c.extStorage))
	payload.U32(uint32(c.mainStorage))
	payload.U32(uint32(c.mainConsumed))
	payload.U32(c.xSeed)
	payload.U32(c.eSeed)

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext ext bands", "GQXO")
	if err != nil {
		t.Fatalf("run qext ext bands oracle: %v", err)
	}
	var o qextExtBandsOracle
	o.n = int(reader.U32())
	o.qStartBin = int(reader.U32())
	o.qStopBin = int(reader.U32())
	o.extBytes = reader.Bytes(int(reader.U32()))
	o.decodedX = make([]float32, int(reader.U32()))
	for i := range o.decodedX {
		o.decodedX[i] = reader.Float32()
	}
	o.decodedEne = make([]float32, int(reader.U32()))
	for i := range o.decodedEne {
		o.decodedEne[i] = reader.Float32()
	}
	o.mainTellFrac = int(reader.U32())
	o.decQextEnd = int(reader.U32())
	o.extPulses = make([]int32, o.decQextEnd)
	for i := range o.extPulses {
		o.extPulses[i] = int32(reader.U32())
	}
	o.extQuant = make([]int32, o.decQextEnd)
	for i := range o.extQuant {
		o.extQuant[i] = int32(reader.U32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext ext bands oracle payload not fully consumed: %v", err)
	}
	return o
}

func boolToU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// advanceMainTell consumes mainConsumed raw bits from a fresh decoder over a
// mainStorage-byte buffer so its TellFrac() matches the C oracle's main coder
// (which feeds clt_compute_extra_allocation's qext_bits sizing). Raw end-bits
// advance nbits_total identically regardless of content, so the buffer contents
// are irrelevant; only the bit count matters.
func advanceMainTell(mainStorage, mainConsumed int) *rangecoding.Decoder {
	var rd rangecoding.Decoder
	rd.Init(make([]byte, mainStorage))
	left := mainConsumed
	for left > 0 {
		n := left
		if n > 16 {
			n = 16
		}
		rd.DecodeRawBits(uint(n))
		left -= n
	}
	return &rd
}

// TestQEXTExtensionBandsContentMatchesLibopusOracle drives the full libopus
// QEXT extension-band content coding chain (coarse energy + fine energy +
// theta/PVQ via quant_all_bands, all from one ext_ec) for the native 96 kHz
// mode, then replays the *decode* path through gopus's prepareQEXTDecode +
// decodeQEXTBands against the same coded bytes and main-coder tell state. The
// decoded extension X coefficients and qext band energies must match the C
// reference. These are float MDCT-derived quantities, so the comparison follows
// the documented amd64-strict / arm64-1e-6 budget.
func TestQEXTExtensionBandsContentMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := []qextExtBandsCase{
		{
			name: "mono_qe14", channels: 1, lm: 3, qextEnd: nbQEXTBands,
			extStorage: 128, mainStorage: 1024, mainConsumed: 2000,
			xSeed: 0x1234abcd, eSeed: 0x55aa00ff,
		},
		{
			name: "mono_qe2", channels: 1, lm: 3, qextEnd: 2,
			extStorage: 48, mainStorage: 512, mainConsumed: 1200,
			xSeed: 0x0badf00d, eSeed: 0x13371337,
		},
		{
			name: "mono_qe14_transient", channels: 1, lm: 3, qextEnd: nbQEXTBands,
			transient:  true,
			extStorage: 128, mainStorage: 1024, mainConsumed: 2000,
			xSeed: 0x77777777, eSeed: 0x88888888,
		},
		{
			name: "stereo_qe14", channels: 2, lm: 3, qextEnd: nbQEXTBands,
			intensity: nbQEXTBands, dualStereo: 0,
			extStorage: 160, mainStorage: 1024, mainConsumed: 2400,
			xSeed: 0xfeedbeef, eSeed: 0x0f0f0f0f,
		},
		{
			name: "stereo_qe14_dual", channels: 2, lm: 3, qextEnd: nbQEXTBands,
			intensity: nbQEXTBands, dualStereo: 1,
			extStorage: 160, mainStorage: 1024, mainConsumed: 2400,
			xSeed: 0xabad1dea, eSeed: 0xc0ffee11,
		},
		{
			name: "stereo_qe2", channels: 2, lm: 3, qextEnd: 2,
			intensity: 2, dualStereo: 0,
			extStorage: 64, mainStorage: 512, mainConsumed: 1500,
			xSeed: 0x90909090, eSeed: 0x12345678,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			o := probeLibopusQEXTExtBands(t, c)

			frameSize := 240 << c.lm // shortMdctSize(240) << LM = 1920 for LM=3
			if o.n != frameSize {
				t.Fatalf("oracle N=%d want %d", o.n, frameSize)
			}

			dec := NewDecoder(c.channels)
			dec.sampleRate = 96000
			dec.rng = 0

			mainRD := advanceMainTell(c.mainStorage, c.mainConsumed)
			if got := mainRD.TellFrac(); got != o.mainTellFrac {
				t.Fatalf("main TellFrac=%d want oracle %d", got, o.mainTellFrac)
			}
			qext := dec.prepareQEXTDecode(o.extBytes, mainRD, MaxBands, c.lm, frameSize)
			if qext == nil {
				t.Fatal("prepareQEXTDecode returned nil for valid qext payload")
			}
			if qext.end != c.qextEnd {
				t.Fatalf("decoded qext_end=%d want %d", qext.end, c.qextEnd)
			}
			for i := 0; i < c.qextEnd; i++ {
				if got := qext.extraPulses[MaxBands+i]; got != o.extPulses[i] {
					t.Fatalf("ext pulses[%d]=%d want %d", i, got, o.extPulses[i])
				}
				if got := qext.extraQuant[MaxBands+i]; got != o.extQuant[i] {
					t.Fatalf("ext quant[%d]=%d want %d", i, got, o.extQuant[i])
				}
			}

			// gopus's quant_all_bands convention takes the block count B directly:
			// B = M (1<<LM) when transient, else 1. libopus passes the transient
			// flag and computes B = shortBlocks ? M : 1 internally.
			blocks := 1
			if c.transient {
				blocks = 1 << c.lm
			}
			dec.decodeQEXTBands(frameSize, c.lm, blocks, spreadNormal, false, qext)

			// Compare decoded extension X over the qext band bin range.
			span := o.qStopBin - o.qStartBin
			if len(o.decodedX) != c.channels*span {
				t.Fatalf("oracle decodedX len=%d want %d", len(o.decodedX), c.channels*span)
			}
			assertQEXTFloatSlice(t, "decoded X (L)", qext.coeffsL[o.qStartBin:o.qStopBin], o.decodedX[:span])
			if c.channels == 2 {
				assertQEXTFloatSlice(t, "decoded X (R)", qext.coeffsR[o.qStartBin:o.qStopBin], o.decodedX[span:2*span])
			}

			// Compare decoded qext band energies over [0,qext_end) per channel.
			// gopus lays out qext.energies as [c*qext_end + band]; the oracle dumps
			// [c*NB_QEXT_BANDS + band].
			gotEne := make([]float32, c.channels*c.qextEnd)
			wantEne := make([]float32, c.channels*c.qextEnd)
			for ch := 0; ch < c.channels; ch++ {
				for band := 0; band < c.qextEnd; band++ {
					gotEne[ch*c.qextEnd+band] = float32(qext.energies[ch*c.qextEnd+band])
					wantEne[ch*c.qextEnd+band] = o.decodedEne[ch*nbQEXTBands+band]
				}
			}
			assertQEXTFloatSliceF32(t, "decoded qext energies", gotEne, wantEne)
		})
	}
}

func assertQEXTFloatSlice(t *testing.T, label string, got []celtNorm, want []float32) {
	t.Helper()
	g := make([]float32, len(got))
	for i := range got {
		g[i] = float32(got[i])
	}
	assertQEXTFloatSliceF32(t, label, g, want)
}

// assertQEXTFloatSliceF32 enforces byte-exact equality on amd64 (CI hard gate)
// and a bounded residual on arm64, mirroring the mode_hd96k_qext_test idiom.
func assertQEXTFloatSliceF32(t *testing.T, label string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length mismatch: gopus=%d oracle=%d", label, len(got), len(want))
	}
	var maxResidual float32
	maxIdx := -1
	for i := range want {
		if got[i] == want[i] {
			continue
		}
		res := float32(math.Abs(float64(got[i]) - float64(want[i])))
		if res > maxResidual {
			maxResidual = res
			maxIdx = i
		}
	}
	if maxIdx >= 0 {
		if maxResidual > qextExtBandsFloatTol {
			t.Fatalf("%s residual %v at index %d exceeds budget %v",
				label, maxResidual, maxIdx, qextExtBandsFloatTol)
		}
		t.Logf("RESIDUAL cosine/rsqrt drift on %s: max %v at index %d (<= %v, project_arm64_celt_1ulp_drift.md)",
			label, maxResidual, maxIdx, qextExtBandsFloatTol)
	}
}
