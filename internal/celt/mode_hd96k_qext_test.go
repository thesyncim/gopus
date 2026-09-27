//go:build gopus_qext

package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var libopusQEXTModeHelper libopustest.HelperCache

func buildLibopusQEXTModeHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "celt qext mode",
		OutputBase:  "gopus_libopus_celt_qext_mode",
		SourceFile:  "libopus_celt_qext_mode_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"celt", "silk"},
		QEXTRef:     true,
		Libs:        []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

type hd96kOracleMode struct {
	Fs, Overlap, NbEBands, EffEBands   int
	MaxLM, NbShortMdcts, ShortMdctSize int
	Preemph                            [4]float32
	EBands, LogN                       []int16
	Window, Trig                       []float32
	FFTTwiddles                        []kissCpx
	MdctN, MdctMaxShift                int
}

func probeLibopusHD96kMode(t *testing.T) hd96kOracleMode {
	t.Helper()
	binPath, err := libopusQEXTModeHelper.Path(buildLibopusQEXTModeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt qext mode", err)
	}
	payload := libopustest.NewOraclePayload("GQMI", 1)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt qext mode", "GQMO")
	if err != nil {
		t.Fatalf("run qext mode oracle: %v", err)
	}
	var m hd96kOracleMode
	m.Fs = int(reader.U32())
	m.Overlap = int(reader.U32())
	m.NbEBands = int(reader.U32())
	m.EffEBands = int(reader.U32())
	m.MaxLM = int(reader.U32())
	m.NbShortMdcts = int(reader.U32())
	m.ShortMdctSize = int(reader.U32())
	for i := range m.Preemph {
		m.Preemph[i] = reader.Float32()
	}
	m.EBands = make([]int16, int(reader.U32()))
	for i := range m.EBands {
		m.EBands[i] = reader.I16()
	}
	m.LogN = make([]int16, int(reader.U32()))
	for i := range m.LogN {
		m.LogN[i] = reader.I16()
	}
	m.Window = make([]float32, int(reader.U32()))
	for i := range m.Window {
		m.Window[i] = reader.Float32()
	}
	m.MdctN = int(reader.U32())
	m.MdctMaxShift = int(reader.U32())
	m.Trig = make([]float32, int(reader.U32()))
	for i := range m.Trig {
		m.Trig[i] = reader.Float32()
	}
	m.FFTTwiddles = make([]kissCpx, int(reader.U32()))
	for i := range m.FFTTwiddles {
		m.FFTTwiddles[i] = kissCpx{r: reader.Float32(), i: reader.Float32()}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext mode oracle payload not fully consumed: %v", err)
	}
	return m
}

// checkF32Table compares every static table coefficient with the selected
// libopus QEXT mode, including the source literal's final float32 rounding.
func checkF32Table(t *testing.T, name string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length: got %d want %d", name, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s[%d]: got %08x want %08x", name, i,
				math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func TestHD96kModeMatchesLibopusQEXT(t *testing.T) {
	libopustest.RequireOracle(t)
	ref := probeLibopusHD96kMode(t)
	got := NewHD96kMode()

	if got.Fs != ref.Fs || got.Overlap != ref.Overlap ||
		got.NbEBands != ref.NbEBands || got.EffEBands != ref.EffEBands ||
		got.MaxLM != ref.MaxLM || got.NbShortMdcts != ref.NbShortMdcts ||
		got.ShortMdctSize != ref.ShortMdctSize ||
		got.MdctN != ref.MdctN || got.MdctMaxShift != ref.MdctMaxShift {
		t.Fatalf("scalar mismatch:\n got=%+v\n ref=Fs=%d overlap=%d nbE=%d effE=%d maxLM=%d nbShort=%d shortMdct=%d mdctN=%d maxShift=%d",
			got, ref.Fs, ref.Overlap, ref.NbEBands, ref.EffEBands, ref.MaxLM,
			ref.NbShortMdcts, ref.ShortMdctSize, ref.MdctN, ref.MdctMaxShift)
	}

	if len(got.EBands) != len(ref.EBands) {
		t.Fatalf("eBands length: got %d want %d", len(got.EBands), len(ref.EBands))
	}
	for i := range ref.EBands {
		if got.EBands[i] != ref.EBands[i] {
			t.Fatalf("eBands[%d]: got %d want %d", i, got.EBands[i], ref.EBands[i])
		}
	}

	if len(got.LogN) != len(ref.LogN) {
		t.Fatalf("logN length: got %d want %d", len(got.LogN), len(ref.LogN))
	}
	for i := range ref.LogN {
		if got.LogN[i] != ref.LogN[i] {
			t.Fatalf("logN[%d]: got %d want %d", i, got.LogN[i], ref.LogN[i])
		}
	}

	for i := range got.Preemph {
		if got.Preemph[i] != ref.Preemph[i] {
			t.Errorf("preemph[%d]: got %v want %v", i, got.Preemph[i], ref.Preemph[i])
		}
	}

	checkF32Table(t, "window240", got.Window, ref.Window)
	checkF32Table(t, "mdctTrig", got.MdctTrig, ref.Trig)
	fft := getKissFFTState(960)
	if len(fft.w) != len(ref.FFTTwiddles) {
		t.Fatalf("FFT twiddle count: got %d want %d", len(fft.w), len(ref.FFTTwiddles))
	}
	for i := range ref.FFTTwiddles {
		if math.Float32bits(fft.w[i].r) != math.Float32bits(ref.FFTTwiddles[i].r) ||
			math.Float32bits(fft.w[i].i) != math.Float32bits(ref.FFTTwiddles[i].i) {
			t.Fatalf("FFT twiddle[%d]: got (%08x,%08x) want (%08x,%08x)", i,
				math.Float32bits(fft.w[i].r), math.Float32bits(fft.w[i].i),
				math.Float32bits(ref.FFTTwiddles[i].r), math.Float32bits(ref.FFTTwiddles[i].i))
		}
	}
}
