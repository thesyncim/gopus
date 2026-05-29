//go:build gopus_qext

package celt

import (
	"runtime"
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
	Fs, Overlap, NbEBands, EffEBands       int
	MaxLM, NbShortMdcts, ShortMdctSize     int
	Preemph                                [4]float32
	EBands, LogN                           []int16
	Window, Trig                           []float32
	MdctN, MdctMaxShift                    int
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
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("qext mode oracle payload not fully consumed: %v", err)
	}
	return m
}

// hd96kArm64Tol bounds the honest darwin/arm64 cosine-kernel residual on the
// MDCT trig / window tables (root cause documented in
// project_arm64_celt_1ulp_drift.md). On amd64 (CI hard gate) the closed forms
// are byte-exact and any nonzero diff fails. Scalars and the integer
// eBands/logN tables must match exactly on every platform.
const hd96kArm64Tol = float32(1e-6)

// checkF32Table compares a computed float32 table against the libopus oracle.
// amd64 requires exact equality; arm64 logs a bounded residual instead of
// failing, matching the documented per-arch CELT float budget.
func checkF32Table(t *testing.T, name string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s length: got %d want %d", name, len(got), len(want))
	}
	isArm64 := runtime.GOARCH == "arm64"
	var maxResidual float32
	maxIdx := -1
	for i := range want {
		d := got[i] - want[i]
		if d < 0 {
			d = -d
		}
		if d == 0 {
			continue
		}
		if isArm64 {
			if d > maxResidual {
				maxResidual, maxIdx = d, i
			}
			continue
		}
		t.Fatalf("%s[%d]: got %v want %v (diff %v, amd64 must be exact)", name, i, got[i], want[i], got[i]-want[i])
	}
	if isArm64 && maxIdx >= 0 {
		if maxResidual > hd96kArm64Tol {
			t.Fatalf("%s arm64 residual %v at index %d exceeds budget %v", name, maxResidual, maxIdx, hd96kArm64Tol)
		}
		t.Logf("RESIDUAL arm64 cosine-kernel drift on %s: max %v at index %d (<= %v, project_arm64_celt_1ulp_drift.md)", name, maxResidual, maxIdx, hd96kArm64Tol)
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
}
