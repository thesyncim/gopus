//go:build gopus_dred || gopus_unsupported_controls
// +build gopus_dred gopus_unsupported_controls

package encoder

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/dred/rdovae"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestDREDBitsTableMatchesLibopusReference(t *testing.T) {
	want := readLibopusDREDBitsTable(t)
	if len(want) != len(dredBitsTable) {
		t.Fatalf("dredBitsTable len=%d want %d", len(dredBitsTable), len(want))
	}
	for i, got := range dredBitsTable {
		if got != want[i] {
			t.Fatalf("dredBitsTable[%d]=%g want %g", i, got, want[i])
		}
	}
}

func TestComputeDREDEmissionPlanUsesFECControlFlag(t *testing.T) {
	withoutLBRR := newDREDPlanTestEncoder()
	withoutLBRR.fecEnabled = true
	withoutLBRR.lbrrCoded = false
	got, ok := withoutLBRR.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED with FEC enabled")
	}

	withLBRR := newDREDPlanTestEncoder()
	withLBRR.fecEnabled = true
	withLBRR.lbrrCoded = true
	want, ok := withLBRR.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED with LBRR coded")
	}
	if got != want {
		t.Fatalf("plan with FEC enabled differs by LBRR state: got %+v want %+v", got, want)
	}

	noFEC := newDREDPlanTestEncoder()
	noFEC.fecEnabled = false
	noFECPlan, ok := noFEC.computeDREDEmissionPlan(960)
	if !ok {
		t.Fatal("computeDREDEmissionPlan() disabled DRED without FEC")
	}
	if got == noFECPlan {
		t.Fatalf("FEC-enabled plan matched no-FEC plan: %+v", got)
	}
}

func newDREDPlanTestEncoder() *Encoder {
	return &Encoder{
		sampleRate: 48000,
		bitrate:    64000,
		packetLoss: 10,
		dred: &dredEncoderExtras{
			duration: 8,
			models: dredEncoderModels{
				encoder: &rdovae.EncoderModel{},
				pitch:   &lpcnetplc.PitchDNNModel{},
			},
		},
	}
}

func readLibopusDREDBitsTable(t *testing.T) []float64 {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	path := filepath.Join(repoRoot, "tmp_check", "opus-1.6.1", "src", "opus_encoder.c")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read libopus opus_encoder.c: %v", err)
	}

	re := regexp.MustCompile(`(?s)static\s+const\s+float\s+dred_bits_table\[16\]\s*=\s*\{([^}]*)\}`)
	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatal("libopus dred_bits_table not found")
	}
	fields := strings.Split(string(m[1]), ",")
	values := make([]float64, 0, len(fields))
	for _, field := range fields {
		value := strings.TrimSpace(field)
		value = strings.TrimSuffix(value, "f")
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("parse libopus dred_bits_table value %q: %v", field, err)
		}
		values = append(values, parsed)
	}
	return values
}
