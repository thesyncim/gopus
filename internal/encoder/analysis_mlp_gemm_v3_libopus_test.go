//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	analysisMLPGemmInputMagic  = "GMKI"
	analysisMLPGemmOutputMagic = "GMKO"
	analysisMLPSourceSHA256    = "162884422e9b91d368b695aa5f84d74f9585cbf2b13a45e6bbca65df880db06c"
)

var (
	analysisMLPGemmHelper    libopustest.HelperCache
	analysisMLPGemmAllocSink float32
)

type analysisMLPGemmCase struct {
	name     string
	features [25]float32
	state    [24]float32
}

type analysisMLPGemmResult struct {
	dense0 [32]float32
	state  [24]float32
	dense2 [2]float32
}

func TestAnalysisMLPGEMMMatchesLibopusV3(t *testing.T) {
	requireAnalysisMLPV3Target(t)
	libopustest.RequireOracle(t)
	if !analysisMLPTraceEnabled {
		t.Fatal("analysis MLP trace hook is disabled in this build")
	}

	cases := analysisMLPGemmCases(t)
	want, sourceHash, err := probeAnalysisMLPGemm(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "analysis MLP GEMM", err)
	}
	for i, tc := range cases {
		got := runAnalysisMLPGemm(tc.features, tc.state)
		compareAnalysisMLPGemmOutput(t, tc.name, "dense0", got.dense0[:], want[i].dense0[:])
		compareAnalysisMLPGemmOutput(t, tc.name, "GRU state", got.state[:], want[i].state[:])
		compareAnalysisMLPGemmOutput(t, tc.name, "dense2", got.dense2[:], want[i].dense2[:])
	}
	if t.Failed() {
		t.Fatalf("analysis MLP output differs from selected libopus src/mlp.c (%s)", sourceHash)
	}

	features := cases[1].features
	initialState := cases[1].state
	var dense0 [32]float32
	var state [24]float32
	var dense2 [2]float32
	run := func() {
		state = initialState
		layer0.ComputeDense(dense0[:], features[:])
		layer1.ComputeGRU(state[:], dense0[:])
		layer2.ComputeDense(dense2[:], state[:])
		analysisMLPGemmAllocSink = dense0[0] + state[0] + dense2[0]
	}
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("analysis MLP dense/GRU steady-state allocations=%g, want 0", allocs)
	}
}

func requireAnalysisMLPV3Target(t *testing.T) {
	t.Helper()
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err == nil && target == "v3" {
		return
	}
	message := "analysis MLP source oracle requires GOPUS_LIBOPUS_AMD64_TARGET=v3"
	if err != nil {
		message += ": " + err.Error()
	} else {
		message += fmt.Sprintf(", got %q", target)
	}
	if libopustest.StrictRefRequired() {
		t.Fatal(message)
	}
	t.Skip(message)
}

func analysisMLPGemmCases(t *testing.T) []analysisMLPGemmCase {
	t.Helper()
	const (
		fs        = 48000
		channels  = 2
		frameSize = 960
	)
	samples, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1, fs, frameSize*channels, channels,
	)
	if err != nil {
		t.Fatalf("generate actual analysis frame: %v", err)
	}
	clampToOpusDemoF32InPlace(samples)
	state := NewTonalityAnalysisState(fs)
	state.SetLSBDepth(24)
	oldHook := analysisMLPTraceHook
	t.Cleanup(func() { analysisMLPTraceHook = oldHook })
	var snapshot analysisMLPTraceSnapshot
	calls := 0
	analysisMLPTraceHook = func(got analysisMLPTraceSnapshot) {
		calls++
		snapshot = got
	}
	state.RunAnalysis(samples, frameSize, channels)
	analysisMLPTraceHook = oldHook
	if calls != 1 || snapshot.Frame != 0 || snapshot.Dense0Calls != 1 || snapshot.GRUCalls != 1 || snapshot.Dense2Calls != 1 {
		t.Fatalf("actual analysis hook calls=%d frame=%d dense0=%d GRU=%d dense2=%d", calls,
			snapshot.Frame, snapshot.Dense0Calls, snapshot.GRUCalls, snapshot.Dense2Calls)
	}
	cases := make([]analysisMLPGemmCase, 0, 9)
	cases = append(cases, analysisMLPGemmCase{
		name:     "actual-frame-0",
		features: snapshot.Dense0Input,
		state:    snapshot.GRUStateBefore,
	})
	for c := 0; c < 8; c++ {
		tc := analysisMLPGemmCase{name: fmt.Sprintf("bounded-f32-%d", c)}
		for i := range tc.features {
			tc.features[i] = float32((i*37+c*19)%127-63) * (1.0 / 32.0)
		}
		for i := range tc.state {
			tc.state[i] = float32((i*13+c*7)%101-50) * (1.0 / 64.0)
		}
		cases = append(cases, tc)
	}
	return cases
}

func runAnalysisMLPGemm(features [25]float32, initialState [24]float32) analysisMLPGemmResult {
	var got analysisMLPGemmResult
	layer0.ComputeDense(got.dense0[:], features[:])
	got.state = initialState
	layer1.ComputeGRU(got.state[:], got.dense0[:])
	layer2.ComputeDense(got.dense2[:], got.state[:])
	return got
}

func compareAnalysisMLPGemmOutput(t *testing.T, caseName, stage string, got, want []float32) {
	t.Helper()
	for i := range want {
		gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i])
		if gotBits != wantBits {
			t.Errorf("%s %s[%d]=%08x want %08x", caseName, stage, i, gotBits, wantBits)
			return
		}
	}
}

func probeAnalysisMLPGemm(cases []analysisMLPGemmCase) ([]analysisMLPGemmResult, string, error) {
	binPath, sourceHash, err := buildAnalysisMLPGemmHelper()
	if err != nil {
		return nil, "", err
	}
	payload := libopustest.NewOraclePayloadVersion(analysisMLPGemmInputMagic, 1, uint32(len(cases)))
	for _, tc := range cases {
		payload.Float32s(tc.features[:]...)
		payload.Float32s(tc.state[:]...)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "analysis MLP GEMM", analysisMLPGemmOutputMagic, 1)
	if err != nil {
		return nil, "", err
	}
	count := reader.Count(len(cases))
	if got := string(reader.Bytes(64)); got != sourceHash {
		return nil, "", fmt.Errorf("selected libopus src/mlp.c SHA256=%s want %s", got, sourceHash)
	}
	results := make([]analysisMLPGemmResult, count)
	for i := range results {
		for j := range results[i].dense0 {
			results[i].dense0[j] = reader.Float32()
		}
		for j := range results[i].state {
			results[i].state[j] = reader.Float32()
		}
		for j := range results[i].dense2 {
			results[i].dense2[j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, "", err
	}
	return results, sourceHash, nil
}

func buildAnalysisMLPGemmHelper() (string, string, error) {
	variant, err := libopustooling.ResolveLibopusReferenceVariant()
	if err != nil {
		return "", "", err
	}
	mlpPath := libopustest.RefPath("src", "mlp.c")
	root := filepath.Clean(filepath.Join(filepath.Dir(mlpPath), "..", "..", ".."))
	switch variant {
	case libopustooling.LibopusReferenceScalar:
		if !libopustooling.EnsureLibopusScalar(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected scalar libopus reference at %s", root)
		}
	case libopustooling.LibopusReferenceSIMD:
		if !libopustooling.EnsureLibopusSIMD(libopustooling.DefaultVersion, []string{root}) {
			return "", "", fmt.Errorf("ensure selected SIMD libopus reference at %s", root)
		}
	default:
		return "", "", fmt.Errorf("analysis MLP oracle selected unsupported reference variant %q", variant)
	}
	mlpSource, err := os.ReadFile(mlpPath)
	if err != nil {
		return "", "", fmt.Errorf("read selected libopus src/mlp.c: %w", err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(mlpSource))
	if sourceHash != analysisMLPSourceSHA256 {
		return "", "", fmt.Errorf("selected libopus src/mlp.c SHA256=%s want pinned %s", sourceHash, analysisMLPSourceSHA256)
	}
	config := libopustest.CHelperConfig{
		Label:      "libopus analysis MLP GEMM",
		OutputBase: "gopus_libopus_analysis_mlp_gemm",
		SourceFile: "libopus_analysis_mlp_gemm_kernel.c",
		CFlags: []string{
			"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
			fmt.Sprintf("-DGOPUS_MLP_SOURCE_SHA256=%q", sourceHash),
		},
		RefIncludes: []string{"include", "src", "celt", "silk", "silk/float"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
	path, err := analysisMLPGemmHelper.CHelperPath(config)
	if err != nil {
		return "", "", err
	}
	return path, sourceHash, nil
}
